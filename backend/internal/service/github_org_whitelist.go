package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"slices"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const (
	SettingKeyGitHubOAuthOrgWhitelist = "github_oauth_org_whitelist"
	GitHubOrgWhitelistProofKey        = "github_org_whitelist_proof"
	githubOrgWhitelistProofLifetime   = 10 * time.Minute
)

var githubOrganizationName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,37}[a-z0-9])?$`)

// GitHubOrgWhitelist restricts GitHub OAuth to active members of any listed organization.
type GitHubOrgWhitelist struct {
	Enabled       bool     `json:"enabled"`
	Organizations []string `json:"organizations"`
}

func (p *GitHubOrgWhitelist) Normalize() error {
	organizations := make([]string, 0, len(p.Organizations))
	for _, organization := range p.Organizations {
		organization = strings.ToLower(strings.TrimSpace(organization))
		if !githubOrganizationName.MatchString(organization) {
			return fmt.Errorf("invalid GitHub organization name: %q", organization)
		}
		if !slices.Contains(organizations, organization) {
			organizations = append(organizations, organization)
		}
	}
	if len(organizations) > 20 {
		return fmt.Errorf("at most 20 GitHub organizations are allowed")
	}
	slices.Sort(organizations)
	p.Organizations = organizations
	return nil
}

func parseGitHubOrgWhitelist(raw string) (GitHubOrgWhitelist, error) {
	policy := GitHubOrgWhitelist{Organizations: []string{}}
	if strings.TrimSpace(raw) != "" {
		if !strings.HasPrefix(strings.TrimSpace(raw), "{") {
			return policy, fmt.Errorf("GitHub organization whitelist must be an object")
		}
		if err := json.Unmarshal([]byte(raw), &policy); err != nil {
			return policy, fmt.Errorf("decode GitHub organization whitelist: %w", err)
		}
	}
	return policy, policy.Normalize()
}

func (s *SettingService) GetGitHubOrgWhitelist(ctx context.Context) (GitHubOrgWhitelist, error) {
	if s == nil || s.settingRepo == nil {
		return GitHubOrgWhitelist{}, infraerrors.ServiceUnavailable("GITHUB_ORG_WHITELIST_UNAVAILABLE", "GitHub organization policy is unavailable")
	}
	values, err := s.settingRepo.GetMultiple(ctx, []string{SettingKeyGitHubOAuthOrgWhitelist})
	if err != nil {
		return GitHubOrgWhitelist{}, infraerrors.ServiceUnavailable("GITHUB_ORG_WHITELIST_UNAVAILABLE", "GitHub organization policy is unavailable").WithCause(err)
	}
	policy, err := parseGitHubOrgWhitelist(values[SettingKeyGitHubOAuthOrgWhitelist])
	if err != nil {
		return policy, infraerrors.ServiceUnavailable("GITHUB_ORG_WHITELIST_UNAVAILABLE", "GitHub organization policy is invalid").WithCause(err)
	}
	return policy, nil
}

type githubOrgWhitelistProof struct {
	Subject   string `json:"subject"`
	Policy    string `json:"policy"`
	ExpiresAt int64  `json:"expires_at"`
}

func (p GitHubOrgWhitelist) fingerprint() string {
	data, _ := json.Marshal(p)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// MembershipProof is stored only in server-side OAuth state after GitHub confirms membership.
// It binds the check to one GitHub ID and policy without retaining the OAuth access token.
func (p GitHubOrgWhitelist) MembershipProof(subject string, checkedAt time.Time) string {
	data, _ := json.Marshal(githubOrgWhitelistProof{
		Subject: subject, Policy: p.fingerprint(), ExpiresAt: checkedAt.Add(githubOrgWhitelistProofLifetime).Unix(),
	})
	return string(data)
}

func (s *AuthService) ValidateGitHubOrgWhitelistProof(ctx context.Context, subject, raw string) error {
	if s == nil || s.settingService == nil {
		return infraerrors.ServiceUnavailable("GITHUB_ORG_WHITELIST_UNAVAILABLE", "GitHub organization policy is unavailable")
	}
	policy, err := s.settingService.GetGitHubOrgWhitelist(ctx)
	if err != nil || !policy.Enabled {
		return err
	}
	var proof githubOrgWhitelistProof
	now := time.Now()
	if len(policy.Organizations) == 0 || json.Unmarshal([]byte(raw), &proof) != nil ||
		strings.TrimSpace(subject) == "" || proof.Subject != subject || proof.Policy != policy.fingerprint() ||
		proof.ExpiresAt <= now.Unix() || proof.ExpiresAt > now.Add(githubOrgWhitelistProofLifetime).Unix() {
		return infraerrors.Forbidden("GITHUB_ORG_ACCESS_DENIED", "GitHub organization membership must be verified; sign in with GitHub again")
	}
	return nil
}

// LoginGitHubOrgMember signs in a GitHub identity whose organization membership was
// just verified. Membership is the only admission rule: the account is provisioned
// regardless of the registration settings, and every member is an administrator.
func (s *AuthService) LoginGitHubOrgMember(ctx context.Context, input EmailOAuthIdentityInput) (*TokenPair, *User, error) {
	if s == nil || s.userRepo == nil || s.entClient == nil {
		return nil, nil, ErrServiceUnavailable
	}
	const provider = "github"
	input.ProviderType, input.ProviderKey = provider, provider
	input.ProviderSubject = strings.TrimSpace(input.ProviderSubject)
	input.Email = strings.TrimSpace(strings.ToLower(input.Email))
	proof, _ := input.UpstreamMetadata[GitHubOrgWhitelistProofKey].(string)
	if proof == "" {
		return nil, nil, infraerrors.Forbidden("GITHUB_ORG_ACCESS_DENIED", "GitHub organization membership must be verified; sign in with GitHub again")
	}
	if err := s.ValidateGitHubOrgWhitelistProof(ctx, input.ProviderSubject, proof); err != nil {
		return nil, nil, err
	}

	// The GitHub ID owns the account, so a changed primary email keeps the same user.
	user, err := s.findEmailOAuthIdentityOwner(ctx, provider, provider, input.ProviderSubject)
	if err != nil {
		return nil, nil, err
	}
	created := false
	if user == nil {
		if user, created, err = s.findOrCreateGitHubOrgMember(ctx, input); err != nil {
			return nil, nil, err
		}
	}
	if !user.IsActive() {
		return nil, nil, ErrUserNotActive
	}
	if err := s.ensureEmailOAuthIdentity(ctx, user.ID, input); err != nil {
		return nil, nil, err
	}
	if user.Role != RoleAdmin {
		user.Role = RoleAdmin
		if err := s.userRepo.Update(ctx, user, UserUpdateFields{Role: true}); err != nil {
			logger.LegacyPrintf("service.auth", "[Auth] Failed to promote GitHub organization member %d: %v", user.ID, err)
			return nil, nil, ErrServiceUnavailable
		}
	}
	if !created {
		if err := s.ApplyProviderDefaultSettingsOnFirstBind(ctx, user.ID, provider); err != nil {
			logger.LegacyPrintf("service.auth", "[Auth] Failed to apply github first bind defaults: %v", err)
		}
	}
	s.RecordSuccessfulLogin(ctx, user.ID)

	tokenPair, err := s.GenerateTokenPair(ctx, user, "")
	if err != nil {
		return nil, nil, fmt.Errorf("generate token pair: %w", err)
	}
	return tokenPair, user, nil
}

func (s *AuthService) findOrCreateGitHubOrgMember(ctx context.Context, input EmailOAuthIdentityInput) (*User, bool, error) {
	if !input.EmailVerified {
		return nil, false, infraerrors.Forbidden("OAUTH_EMAIL_NOT_VERIFIED", "oauth email is not verified")
	}
	if _, err := mail.ParseAddress(input.Email); err != nil || len(input.Email) > 255 {
		return nil, false, infraerrors.BadRequest("INVALID_EMAIL", "invalid email")
	}
	if isReservedEmail(input.Email) {
		return nil, false, ErrEmailReserved
	}
	user, err := s.userRepo.GetByEmail(ctx, input.Email)
	if err == nil {
		return user, false, nil
	}
	if !errors.Is(err, ErrUserNotFound) {
		logger.LegacyPrintf("service.auth", "[Auth] Database error during github oauth login: %v", err)
		return nil, false, ErrServiceUnavailable
	}
	return s.insertEmailOAuthUser(ctx, input.Email, input.Username, input.ProviderType, RoleAdmin)
}
