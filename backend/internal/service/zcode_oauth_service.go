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

// zcodeOAuthSession 是一次登录的服务端状态。mu 贯穿轮询全程：管理端可能并发
// 轮询，而开通项目 key 不能重复执行。
type zcodeOAuthSession struct {
	mu          sync.Mutex
	flow        *ZCodeLoginSession
	createdAt   time.Time
	credential  *ZCodeCredential
	provisioned bool
	provisionKo string
}

// ZCodeOAuthService 管理 ZCode OAuth 登录流程：发起授权、轮询、开通 CodingPlan
// 项目 key，并把结果整理成可直接落库的账号凭据。
type ZCodeOAuthService struct {
	client      *ZCodeOAuthClient
	proxyRepo   ProxyRepository
	accountRepo AccountRepository
	mu          sync.Mutex
	sessions    map[string]*zcodeOAuthSession
}

// NewZCodeOAuthService 构造服务。
func NewZCodeOAuthService(proxyRepo ProxyRepository, accountRepo AccountRepository) *ZCodeOAuthService {
	return &ZCodeOAuthService{
		client:      NewZCodeOAuthClient(),
		proxyRepo:   proxyRepo,
		accountRepo: accountRepo,
		sessions:    make(map[string]*zcodeOAuthSession),
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
	s.sessions[state] = &zcodeOAuthSession{flow: flow, createdAt: time.Now()}
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
	session, err := s.session(state)
	if err != nil {
		return nil, err
	}
	session.mu.Lock()
	defer session.mu.Unlock()

	flow := session.flow
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
			session.credential = credential
		case ZCodePollDeclined:
			result.Status = string(ZCodePollDeclined)
			result.Message = "zcode sign-in was declined"
			return result, nil
		default:
			return result, nil
		}
	}

	credential := session.credential
	if !session.provisioned {
		session.provisioned = true
		if provisionErr := s.client.ProvisionAPIKey(ctx, credential, flow.ProxyURL); provisionErr != nil {
			session.provisionKo = provisionErr.Error()
		}
	}
	result.Status = string(ZCodePollReady)
	result.Email = credential.Email
	result.Name = credential.Name
	result.UserID = credential.UserID
	result.Plan = credential.Plan
	result.Team = credential.Team
	result.HasAPIKey = credential.APIKey != ""
	result.MaskedAPIKey = credential.MaskedAPIKey()
	result.Warning = session.provisionKo
	return result, nil
}

// ZCodeAccountOptions 是管理端创建账号时可调的选项。
type ZCodeAccountOptions struct {
	Name        string
	Notes       *string
	APIKey      string
	ProxyID     *int64
	Concurrency int
	Priority    int
	GroupIDs    []int64
	PeakPause   bool
}

// ZCodeAccountDraft 是已完成登录要落库的结果。ExistingID 非 0 时表示同一 ZCode
// 用户已有账号，应更新其凭据而不是新建：重新登录是令牌失效后唯一的续期方式。
type ZCodeAccountDraft struct {
	ExistingID int64
	Input      *CreateAccountInput
}

// AccountDraft 由已完成的登录生成账号内容。未提供 APIKey 时使用自动开通的 key。
func (s *ZCodeOAuthService) AccountDraft(ctx context.Context, state string, options ZCodeAccountOptions) (*ZCodeAccountDraft, error) {
	session, err := s.session(state)
	if err != nil {
		return nil, err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	credential := session.credential
	if credential == nil {
		return nil, infraerrors.New(http.StatusBadRequest, "ZCODE_OAUTH_NOT_READY", "zcode sign-in has not completed")
	}
	credentials := buildZCodeAccountCredentials(credential, options.APIKey)
	if credentials["api_key"] == "" {
		return nil, infraerrors.New(http.StatusBadRequest, "ZCODE_OAUTH_API_KEY_REQUIRED", "CodingPlan API key is required: automatic provisioning failed, paste the key manually")
	}
	existingID, err := s.findAccount(ctx, credential)
	if err != nil {
		return nil, err
	}
	return &ZCodeAccountDraft{ExistingID: existingID, Input: &CreateAccountInput{
		Name:        zcodeAccountName(options.Name, credential),
		Notes:       options.Notes,
		Platform:    PlatformZhipu,
		Type:        AccountTypeAPIKey,
		Credentials: credentials,
		Extra: map[string]any{
			// ultra 端点只认 Authorization: Bearer（实测 x-api-key 返回异常响应）。
			"anthropic_apikey_auth_scheme": AnthropicAPIKeyAuthSchemeAuthorizationBearer,
			zcodeExtraPeakPause:            options.PeakPause,
		},
		ProxyID:     options.ProxyID,
		Concurrency: max(options.Concurrency, 1),
		Priority:    options.Priority,
		GroupIDs:    options.GroupIDs,
	}}, nil
}

// Finish 丢弃已落库登录的会话，避免令牌继续留在内存。
func (s *ZCodeOAuthService) Finish(state string) {
	s.mu.Lock()
	delete(s.sessions, strings.TrimSpace(state))
	s.mu.Unlock()
}

func (s *ZCodeOAuthService) findAccount(ctx context.Context, credential *ZCodeCredential) (int64, error) {
	accounts, err := s.accountRepo.ListByPlatform(ctx, PlatformZhipu)
	if err != nil {
		return 0, infraerrors.New(http.StatusServiceUnavailable, "ZCODE_OAUTH_ACCOUNT_LOOKUP_FAILED", "account lookup is temporarily unavailable")
	}
	for i := range accounts {
		account := &accounts[i]
		if account.GetCredential("zcode_user_id") == credential.UserID && account.GetCredential("zcode_site") == credential.Site {
			return account.ID, nil
		}
	}
	return 0, nil
}

func zcodeAccountName(name string, credential *ZCodeCredential) string {
	switch name = strings.TrimSpace(name); {
	case name != "":
		return name
	case credential.Email != "":
		return "ZCode " + credential.Email
	case credential.Name != "":
		return "ZCode " + credential.Name
	}
	return "ZCode OAuth"
}

// buildZCodeAccountCredentials 把 OAuth 凭据整理成账号 credentials。
// apiKeyOverride 非空时优先使用（管理员手工贴入的 CodingPlan key）。
//
// 账号按「正常计费」配置：平台沿用 zhipu，上游协议固定 anthropic（ultra 端点只有
// Messages 协议），base_url 指向 ultra 端点，接入模式记为 coding（沿用 Coding Plan
// 用量窗口的冷却/探测行为）。
func buildZCodeAccountCredentials(credential *ZCodeCredential, apiKeyOverride string) map[string]any {
	apiKey := strings.TrimSpace(apiKeyOverride)
	if apiKey == "" {
		apiKey = credential.APIKey
	}
	baseURL := ZCodeUltraBaseURL(credential.Site)
	credentials := map[string]any{
		"api_key":      apiKey,
		"base_url":     baseURL,
		"api_protocol": APIProtocolAnthropic,
		"account_mode": AccountModeCoding,
		"api_base_urls": map[string]any{
			APIProtocolAnthropic: baseURL,
		},
		"upstream_mode":      ZCodeUpstreamModeUltra,
		"zcode_site":         credential.Site,
		"zcode_jwt":          credential.JWT,
		"zcode_access_token": credential.SiteAccessToken,
		"zcode_device":       credential.Device,
		"zcode_user_id":      credential.UserID,
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
		// 团队版额度查询按这两个字段带组织/项目请求头。
		credentials["zhipu_organization"] = credential.Org
		credentials["zhipu_project"] = credential.Project
	}
	return credentials
}

func (s *ZCodeOAuthService) session(state string) (*zcodeOAuthSession, error) {
	state = strings.TrimSpace(state)
	if state == "" {
		return nil, infraerrors.New(http.StatusBadRequest, "ZCODE_OAUTH_STATE_REQUIRED", "missing zcode login state")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[state]
	if !ok {
		return nil, infraerrors.New(http.StatusNotFound, "ZCODE_OAUTH_STATE_NOT_FOUND", "zcode login session expired, start sign-in again")
	}
	if time.Since(session.createdAt) > zcodeOAuthSessionTTL {
		delete(s.sessions, state)
		return nil, infraerrors.New(http.StatusNotFound, "ZCODE_OAUTH_STATE_EXPIRED", "zcode login session expired, start sign-in again")
	}
	return session, nil
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
