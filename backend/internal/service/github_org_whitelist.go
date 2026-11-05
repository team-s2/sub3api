package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
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
