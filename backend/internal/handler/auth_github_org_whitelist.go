package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// https://docs.github.com/en/rest/orgs/members#get-an-organization-membership-for-the-authenticated-user
func (h *AuthHandler) verifyGitHubOrganizationMembership(ctx context.Context, cfg config.EmailOAuthProviderConfig, token *emailOAuthTokenResponse, profile *emailOAuthProfile) error {
	policy, err := h.settingSvc.GetGitHubOrgWhitelist(ctx)
	if err != nil || !policy.Enabled {
		return err
	}
	if len(policy.Organizations) == 0 {
		return infraerrors.Forbidden("GITHUB_ORG_ACCESS_DENIED", "GitHub account is not a member of an allowed organization")
	}
	apiURL, err := url.Parse(cfg.UserInfoURL)
	if err != nil {
		return infraerrors.ServiceUnavailable("GITHUB_ORG_CHECK_FAILED", "GitHub organization membership could not be verified")
	}
	apiURL.RawQuery = ""
	apiURL.Fragment = ""
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	for _, organization := range policy.Organizations {
		membershipURL := apiURL.JoinPath("memberships", "orgs", organization)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, membershipURL.String(), nil)
		if err != nil {
			return infraerrors.ServiceUnavailable("GITHUB_ORG_CHECK_FAILED", "GitHub organization membership could not be verified")
		}
		request.Header.Set("Authorization", "Bearer "+token.AccessToken)
		request.Header.Set("Accept", "application/vnd.github+json")
		request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		response, err := client.Do(request)
		if err != nil {
			return infraerrors.ServiceUnavailable("GITHUB_ORG_CHECK_FAILED", "GitHub organization membership could not be verified")
		}
		if response.StatusCode == http.StatusNotFound {
			_ = response.Body.Close()
			continue
		}
		if response.StatusCode != http.StatusOK {
			_ = response.Body.Close()
			return infraerrors.ServiceUnavailable("GITHUB_ORG_CHECK_FAILED", "GitHub organization membership could not be verified; check OAuth organization access")
		}
		var membership struct {
			State        string `json:"state"`
			Organization struct {
				Login string `json:"login"`
			} `json:"organization"`
		}
		err = json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(&membership)
		_ = response.Body.Close()
		if err != nil {
			return infraerrors.ServiceUnavailable("GITHUB_ORG_CHECK_FAILED", "GitHub organization membership response is invalid")
		}
		if membership.State == "active" && strings.EqualFold(membership.Organization.Login, organization) {
			if profile.Metadata == nil {
				profile.Metadata = make(map[string]any)
			}
			profile.Metadata[service.GitHubOrgWhitelistProofKey] = policy.MembershipProof(profile.Subject, time.Now())
			return nil
		}
	}
	return infraerrors.Forbidden("GITHUB_ORG_ACCESS_DENIED", "GitHub account is not a member of an allowed organization")
}
