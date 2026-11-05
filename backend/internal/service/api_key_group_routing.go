package service

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	maxAPIKeyExtraGroups     = 16
	apiKeyRouteGroupCacheTTL = 30 * time.Second
)

// groupModelFit ranks how a group relates to a requested model.
type groupModelFit int

const (
	groupModelFitNone groupModelFit = iota
	// groupModelFitDetected means only the model name points at the group's platform.
	groupModelFitDetected
	// groupModelFitConfigured means an allowlist, route or account mapping names the model.
	groupModelFitConfigured
)

// SetCompositeRouteResolver connects the resolver used for model-based group routing.
func (s *APIKeyService) SetCompositeRouteResolver(resolver *CompositeRouteResolver) {
	s.compositeResolver = resolver
}

// HasExtraGroups reports whether the key routes between several groups.
func (k *APIKey) HasExtraGroups() bool {
	return k != nil && len(k.ExtraGroupIDs) > 0
}

// RoutingGroupIDs lists the key's groups in priority order.
func (k *APIKey) RoutingGroupIDs() []int64 {
	ids := make([]int64, 0, len(k.ExtraGroupIDs)+1)
	if k.GroupID != nil {
		ids = append(ids, *k.GroupID)
	}
	return append(ids, k.ExtraGroupIDs...)
}

// boundToGroup returns a copy of the key that behaves as a single-group key, so
// billing, subscriptions and scheduling need no knowledge of multi-group routing.
func (k *APIKey) boundToGroup(group *Group) *APIKey {
	routed := *k
	groupID := group.ID
	routed.GroupID = &groupID
	routed.Group = group
	if k.User != nil {
		// The cached override belongs to the primary group; nil makes checkRPM look it up.
		user := *k.User
		user.UserGroupRPMOverride = nil
		routed.User = &user
	}
	return &routed
}

// RouteAPIKeyByModel binds the key to the group that should serve model. Configured
// groups beat name detection, and key order breaks ties. usable filters out groups the
// user cannot currently use. The key is returned unchanged when no group fits, leaving
// the primary group to report the error.
func (s *APIKeyService) RouteAPIKeyByModel(ctx context.Context, apiKey *APIKey, model, endpoint string, usable func(*Group) bool) *APIKey {
	model = strings.TrimSpace(model)
	if !apiKey.HasExtraGroups() || model == "" {
		return apiKey
	}
	var selected *Group
	selectedFit := groupModelFitNone
	for _, group := range s.RoutingGroups(ctx, apiKey) {
		if !usable(group) {
			continue
		}
		if fit := s.groupFitForModel(ctx, group, model, endpoint); fit > selectedFit {
			selected, selectedFit = group, fit
		}
		if selectedFit == groupModelFitConfigured {
			break
		}
	}
	if selected == nil || (apiKey.GroupID != nil && selected.ID == *apiKey.GroupID) {
		return apiKey
	}
	return apiKey.boundToGroup(selected)
}

// RoutingGroups loads the key's active groups in priority order.
func (s *APIKeyService) RoutingGroups(ctx context.Context, apiKey *APIKey) []*Group {
	ids := apiKey.RoutingGroupIDs()
	groups := make([]*Group, 0, len(ids))
	for _, id := range ids {
		group := apiKey.Group
		if group == nil || group.ID != id {
			group = s.routeGroup(ctx, id)
		}
		if group != nil && group.IsActive() {
			groups = append(groups, group)
		}
	}
	return groups
}

func (s *APIKeyService) routeGroup(ctx context.Context, id int64) *Group {
	cacheKey := strconv.FormatInt(id, 10)
	if cached, found := s.routeGroupCache.Get(cacheKey); found {
		group, _ := cached.(*Group)
		return group
	}
	group, err := s.groupRepo.GetByIDLite(ctx, id)
	if err != nil {
		if !errors.Is(err, ErrGroupNotFound) {
			return nil
		}
		group = nil
	}
	s.routeGroupCache.SetDefault(cacheKey, group)
	return group
}

func (s *APIKeyService) groupFitForModel(ctx context.Context, group *Group, model, endpoint string) groupModelFit {
	allowlisted := group.ModelAllowlistEnabled()
	if allowlisted && !group.ModelAllowlist.Allows(model) {
		return groupModelFitNone
	}
	var fit groupModelFit
	if group.Platform == PlatformComposite {
		fit = s.compositeGroupFit(ctx, group.ID, model, endpoint)
	} else {
		fit = s.concreteGroupFit(ctx, group, model)
	}
	if allowlisted {
		return groupModelFitConfigured
	}
	return fit
}

func (s *APIKeyService) compositeGroupFit(ctx context.Context, groupID int64, model, endpoint string) groupModelFit {
	decision, err := s.compositeResolver.Resolve(ctx, groupID, model, endpoint)
	switch {
	case err != nil || !decision.Matched:
		return groupModelFitNone
	case decision.Source == CompositeRouteSourceDetector:
		return groupModelFitDetected
	default:
		return groupModelFitConfigured
	}
}

func (s *APIKeyService) concreteGroupFit(ctx context.Context, group *Group, model string) groupModelFit {
	if s.compositeResolver.GroupAccountsClaimModel(ctx, group.ID, model) {
		return groupModelFitConfigured
	}
	detected, ok := DetectModelPlatform(model)
	if !ok {
		return groupModelFitNone
	}
	if detected == group.Platform ||
		(group.Platform == PlatformAntigravity && (detected == PlatformAnthropic || detected == PlatformGemini)) {
		return groupModelFitDetected
	}
	return groupModelFitNone
}

// normalizeExtraGroupIDs keeps order while dropping duplicates and the primary group.
func normalizeExtraGroupIDs(primary *int64, ids []int64) []int64 {
	normalized := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id > 0 && (primary == nil || id != *primary) && !slices.Contains(normalized, id) {
			normalized = append(normalized, id)
		}
	}
	return normalized
}

func (s *APIKeyService) validateExtraGroups(ctx context.Context, user *User, primary *int64, ids []int64) ([]int64, error) {
	normalized := normalizeExtraGroupIDs(primary, ids)
	if len(normalized) == 0 {
		return nil, nil
	}
	if primary == nil {
		return nil, infraerrors.BadRequest("API_KEY_PRIMARY_GROUP_REQUIRED", "a primary group is required before adding extra groups")
	}
	if len(normalized) > maxAPIKeyExtraGroups {
		return nil, infraerrors.BadRequest("API_KEY_TOO_MANY_GROUPS", "too many extra groups")
	}
	for _, id := range normalized {
		group, err := s.groupRepo.GetByID(ctx, id)
		if err != nil {
			return nil, err
		}
		if !s.canUserBindGroup(ctx, user, group) {
			return nil, ErrGroupNotAllowed
		}
	}
	return normalized, nil
}
