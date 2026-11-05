package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// zcodeOAuthSessionTTL 是单次授权流程在服务端保留的最长时间（含开通项目 key）。
const zcodeOAuthSessionTTL = 30 * time.Minute

// ZCodeUpstreamModeUltra 标记账号走 ultra 端点（CodingPlan key + 官方 1.5x
// 配额口径）。账号本身仍按正常倍率计费，这里只用于显式表达路由目标。
const ZCodeUpstreamModeUltra = "ultra"

// ZCodeSiteOption 是管理端可选的登录入口。
type ZCodeSiteOption struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ZCodeAuthURLResult 是发起授权后返回给管理端的结果。
type ZCodeAuthURLResult struct {
	AuthURL   string `json:"auth_url"`
	State     string `json:"state"`
	Site      string `json:"site"`
	ExpiresAt int64  `json:"expires_at"`
}

// ZCodePollResult 是轮询结果；ready 时附带账号信息与项目 key 状态。
type ZCodePollResult struct {
	Status       string `json:"status"`
	Site         string `json:"site"`
	ExpiresAt    int64  `json:"expires_at"`
	Message      string `json:"message,omitempty"`
	Email        string `json:"email,omitempty"`
	Name         string `json:"name,omitempty"`
	UserID       string `json:"user_id,omitempty"`
	Plan         string `json:"plan,omitempty"`
	Team         bool   `json:"team,omitempty"`
	HasAPIKey    bool   `json:"has_api_key"`
	MaskedAPIKey string `json:"masked_api_key,omitempty"`
	Warning      string `json:"warning,omitempty"`
}

type zcodeOAuthSession struct {
	flow        *ZCodeLoginSession
	proxyURL    string
	createdAt   time.Time
	credential  *ZCodeCredential
	provisioned bool
	provisionKo string
}

// ZCodeOAuthService 管理 ZCode OAuth 登录流程：发起授权、轮询、开通 CodingPlan
// 项目 key，并把结果整理成可直接落库的账号凭据。
type ZCodeOAuthService struct {
	client    *ZCodeOAuthClient
	proxyRepo ProxyRepository
	mu        sync.Mutex
	sessions  map[string]*zcodeOAuthSession
}

// NewZCodeOAuthService 构造服务；proxyRepo 可为 nil（此时不支持指定代理）。
func NewZCodeOAuthService(proxyRepo ProxyRepository) *ZCodeOAuthService {
	return &ZCodeOAuthService{
		client:    NewZCodeOAuthClient(),
		proxyRepo: proxyRepo,
		sessions:  make(map[string]*zcodeOAuthSession),
	}
}

// Sites 返回可选的登录入口。两个入口共用同一份凭据形态，只是品牌与授权域不同。
func (s *ZCodeOAuthService) Sites() []ZCodeSiteOption {
	return []ZCodeSiteOption{
		{ID: ZCodeSiteZAI, Name: "Z.AI", Description: "chat.z.ai 账号（海外站点）"},
		{ID: ZCodeSiteBigModel, Name: "BigModel", Description: "bigmodel.cn 账号（国内站点）"},
	}
}

// StartLogin 发起一次 OAuth 授权流程。
func (s *ZCodeOAuthService) StartLogin(ctx context.Context, site string, proxyID *int64) (*ZCodeAuthURLResult, error) {
	site = strings.ToLower(strings.TrimSpace(site))
	if site == "" {
		site = ZCodeSiteBigModel
	}
	if !IsValidZCodeSite(site) {
		return nil, infraerrors.New(http.StatusBadRequest, "ZCODE_OAUTH_INVALID_SITE", "unsupported zcode site")
	}
	proxyURL, err := s.resolveProxyURL(ctx, proxyID)
	if err != nil {
		return nil, err
	}
	flow, err := s.client.BeginLogin(ctx, site, proxyURL)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "ZCODE_OAUTH_INIT_FAILED", "failed to start zcode login: %v", err)
	}

	state, err := newZCodeState()
	if err != nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "ZCODE_OAUTH_STATE_FAILED", "failed to generate login state")
	}
	s.mu.Lock()
	s.pruneLocked()
	s.sessions[state] = &zcodeOAuthSession{flow: flow, proxyURL: proxyURL, createdAt: time.Now()}
	s.mu.Unlock()

	return &ZCodeAuthURLResult{
		AuthURL:   flow.AuthorizeURL,
		State:     state,
		Site:      site,
		ExpiresAt: flow.ExpiresAt.Unix(),
	}, nil
}

// Poll 轮询授权进度。首次 ready 时会自动开通/复用 CodingPlan 项目 key。
func (s *ZCodeOAuthService) Poll(ctx context.Context, state string) (*ZCodePollResult, error) {
	session, flow, err := s.session(state)
	if err != nil {
		return nil, err
	}
	result := &ZCodePollResult{
		Status:    string(ZCodePollPending),
		Site:      flow.Site,
		ExpiresAt: flow.ExpiresAt.Unix(),
	}

	if session.credential == nil {
		status, credential, pollErr := s.client.PollLogin(ctx, flow)
		if pollErr != nil {
			result.Status = string(ZCodePollDeclined)
			result.Message = pollErr.Error()
			return result, nil
		}
		switch status {
		case ZCodePollReady:
			s.mu.Lock()
			session.credential = credential
			s.mu.Unlock()
		case ZCodePollDeclined:
			result.Status = string(ZCodePollDeclined)
			result.Message = "zcode sign-in was declined"
			return result, nil
		default:
			return result, nil
		}
	}

	s.mu.Lock()
	credential := session.credential
	provisioned := session.provisioned
	provisionKo := session.provisionKo
	s.mu.Unlock()

	if credential == nil {
		return result, nil
	}
	result.Status = string(ZCodePollReady)
	result.Email = credential.Email
	result.Name = credential.Name
	result.UserID = credential.UserID

	// 只有首次需要真正调用上游开通；后续轮询复用缓存结果。
	if !provisioned {
		provisionErr := s.client.ProvisionAPIKey(ctx, credential)
		provisionKo = ""
		if provisionErr != nil {
			provisionKo = provisionErr.Error()
		}
		s.mu.Lock()
		session.provisioned = true
		session.provisionKo = provisionKo
		s.mu.Unlock()
	}
	result.Plan = credential.Plan
	result.Team = credential.Team
	result.HasAPIKey = credential.APIKey != ""
	result.MaskedAPIKey = credential.MaskedAPIKey()
	if provisionKo != "" {
		result.Warning = provisionKo
	}
	return result, nil
}

// CredentialFor 返回已完成登录的凭据，供创建账号使用。
func (s *ZCodeOAuthService) CredentialFor(state string) (*ZCodeCredential, error) {
	session, _, err := s.session(state)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	credential := session.credential
	s.mu.Unlock()
	if credential == nil {
		return nil, infraerrors.New(http.StatusBadRequest, "ZCODE_OAUTH_NOT_READY", "zcode sign-in has not completed")
	}
	return credential, nil
}

// BuildAccountCredentials 把 OAuth 凭据整理成账号 credentials。
// apiKeyOverride 非空时优先使用（管理员手工贴入的 CodingPlan key）。
//
// 账号按「正常计费」配置：平台沿用 zhipu，上游协议固定 anthropic（ultra 端点只有
// Messages 协议），base_url 指向 ultra 端点，接入模式记为 coding（沿用 Coding Plan
// 用量窗口的冷却/探测行为）。
func (s *ZCodeOAuthService) BuildAccountCredentials(credential *ZCodeCredential, apiKeyOverride string) map[string]any {
	apiKey := strings.TrimSpace(apiKeyOverride)
	if apiKey == "" {
		apiKey = credential.APIKey
	}
	credentials := map[string]any{
		"api_key":      apiKey,
		"base_url":     ZCodeUltraAnthropicBaseURL,
		"api_protocol": APIProtocolAnthropic,
		"account_mode": AccountModeCoding,
		"api_base_urls": map[string]any{
			APIProtocolAnthropic: ZCodeUltraAnthropicBaseURL,
		},
		"upstream_mode": ZCodeUpstreamModeUltra,
		"zcode_site":    credential.Site,
		"zcode_jwt":     credential.JWT,
		"zcode_device":  credential.Device,
		"zcode_user_id": credential.UserID,
	}
	if credential.Email != "" {
		credentials["zcode_email"] = credential.Email
	}
	if credential.Plan != "" {
		credentials["zcode_plan"] = credential.Plan
	}
	if credential.Org != "" && credential.Project != "" {
		credentials["zcode_org"] = credential.Org
		credentials["zcode_project"] = credential.Project
	}
	if credential.Team {
		credentials["zcode_team"] = true
	}
	return credentials
}

func (s *ZCodeOAuthService) session(state string) (*zcodeOAuthSession, *ZCodeLoginSession, error) {
	state = strings.TrimSpace(state)
	if state == "" {
		return nil, nil, infraerrors.New(http.StatusBadRequest, "ZCODE_OAUTH_STATE_REQUIRED", "missing zcode login state")
	}
	s.mu.Lock()
	session, ok := s.sessions[state]
	s.mu.Unlock()
	if !ok {
		return nil, nil, infraerrors.New(http.StatusNotFound, "ZCODE_OAUTH_STATE_NOT_FOUND", "zcode login session expired, start sign-in again")
	}
	if time.Since(session.createdAt) > zcodeOAuthSessionTTL {
		s.mu.Lock()
		delete(s.sessions, state)
		s.mu.Unlock()
		return nil, nil, infraerrors.New(http.StatusNotFound, "ZCODE_OAUTH_STATE_EXPIRED", "zcode login session expired, start sign-in again")
	}
	return session, session.flow, nil
}

func (s *ZCodeOAuthService) pruneLocked() {
	now := time.Now()
	for state, session := range s.sessions {
		if now.Sub(session.createdAt) > zcodeOAuthSessionTTL {
			delete(s.sessions, state)
		}
	}
}

func (s *ZCodeOAuthService) resolveProxyURL(ctx context.Context, proxyID *int64) (string, error) {
	if proxyID == nil || *proxyID == 0 {
		return "", nil
	}
	if s.proxyRepo == nil {
		return "", infraerrors.New(http.StatusBadRequest, "ZCODE_OAUTH_PROXY_UNSUPPORTED", "proxy selection is not available")
	}
	proxy, err := s.proxyRepo.GetByID(ctx, *proxyID)
	if err != nil {
		if errors.Is(err, ErrProxyNotFound) {
			return "", infraerrors.New(http.StatusBadRequest, "ZCODE_OAUTH_PROXY_NOT_FOUND", "configured proxy was not found")
		}
		return "", infraerrors.New(http.StatusServiceUnavailable, "ZCODE_OAUTH_PROXY_LOOKUP_FAILED", "proxy lookup is temporarily unavailable")
	}
	if proxy == nil {
		return "", infraerrors.New(http.StatusBadRequest, "ZCODE_OAUTH_PROXY_NOT_FOUND", "configured proxy was not found")
	}
	return proxy.URL(), nil
}

func newZCodeState() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
