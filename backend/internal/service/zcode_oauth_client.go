package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ZCode OAuth 协议常量。
//
// 参考实现：CLIProxyAPI 的 zcode 插件与上游开源的 omc-zcode-oauth 插件，
// 以及 ZCode Desktop 3.2.5 的真实抓包。智谱把「账号身份」与「推理凭据」
// 拆成两套：
//   - 身份：zcode CLI 授权流程（init → 浏览器授权 → poll）产出 ZCode 会话
//     JWT 与 z.ai / bigmodel 站点 access_token；
//   - 推理：CodingPlan 项目 API key（形如 id.secret，长期有效），业务接口
//     里可复用或自动开通。
//
// 本客户端只做这两步，不含 relay（zcode-plan 中转）套餐逻辑。
const (
	// ZCodeRootURL 是 ZCode CLI 授权与 ultra 推理端点的根地址。
	ZCodeRootURL = "https://zcode.z.ai"
	// ZCodeUltraAnthropicBaseURL 是 ultra 推理端点基址（Anthropic Messages 协议，
	// 以 CodingPlan key 鉴权）。官方按 1.5x 配额口径计量，即扣减 ≈0.67x 直连。
	ZCodeUltraAnthropicBaseURL = "https://zcode.z.ai/api/v1/ultra/anthropic"

	// ZCodeSiteZAI / ZCodeSiteBigModel 是 ZCode OAuth 的两个登录入口。
	ZCodeSiteZAI      = "zai"
	ZCodeSiteBigModel = "bigmodel"

	// zcodeAPIKeyName 是自动开通的项目 key 名称，与 CPA/OMC 插件保持一致，
	// 这样同一个账号在多处部署时复用同一把 key，不重复生成。
	zcodeAPIKeyName = "zcode-api-key"

	zcodeCLIInitPath = "/api/v1/oauth/cli/init"
	zcodeCLIPollPath = "/api/v1/oauth/cli/poll/"
	zcodeTokenPath   = "/api/v1/oauth/token"

	// zcodeDefaultPollInterval 与 zcodeDefaultFlowTTL 是上游缺省字段时的兜底。
	zcodeDefaultPollInterval = 5 * time.Second
	zcodeDefaultFlowTTL      = 10 * time.Minute
)

// ZCodeSiteRootURL 返回站点业务接口根地址。
func ZCodeSiteRootURL(site string) string {
	if site == ZCodeSiteBigModel {
		return "https://bigmodel.cn"
	}
	return "https://api.z.ai"
}

// ZCodeSiteBaseURL 返回站点原生 Anthropic 直连基址（relay 模式用，仅供展示/对照）。
func ZCodeSiteBaseURL(site string) string {
	if site == ZCodeSiteBigModel {
		return "https://open.bigmodel.cn/api/anthropic"
	}
	return "https://api.z.ai/api/anthropic"
}

// IsValidZCodeSite 报告站点标识是否合法。
func IsValidZCodeSite(site string) bool {
	return site == ZCodeSiteZAI || site == ZCodeSiteBigModel
}

// ZCodeLoginSession 是一次进行中的 OAuth 登录。Bearer / Device 在整个流程中
// 保持不变：设备号由 Bearer 派生，上游据此把 init 与 poll 关联到同一客户端。
type ZCodeLoginSession struct {
	Site         string
	FlowID       string
	AuthorizeURL string
	// Bearer 是发给上游的本地随机 state（hex 编码），Device 是其 UUIDv4 形态派生值。
	Bearer    string
	Device    string
	Interval  time.Duration
	ExpiresAt time.Time
	ProxyURL  string
}

// ZCodeCredential 是 OAuth 登录 + 项目 key 开通后的最终凭据。
type ZCodeCredential struct {
	// Site 是登录入口（zai / bigmodel）。
	Site string
	// JWT 是 ZCode 会话令牌（relay 中转鉴权用；ultra 模式仅作身份与套餐探测）。
	JWT string
	// SiteAccessToken 是 z.ai / bigmodel 站点的 access_token，业务接口鉴权用。
	SiteAccessToken string
	// Device 是与本次登录配套的设备号。
	Device string
	UserID string
	Email  string
	Name   string
	// Plan 是检测到的 CodingPlan 产品名（可能为空）。
	Plan string
	// APIKey 是 CodingPlan 项目 API key（id.secret），ultra 端点鉴权用。
	APIKey string
	// Org / Project 是 key 所属的机构与项目（团队套餐时非默认机构）。
	Org     string
	Project string
	// Team 报告 key 是否来自团队套餐席位。
	Team bool
}

// MaskedAPIKey 返回打码后的 key，用于管理端回显。
func (c *ZCodeCredential) MaskedAPIKey() string {
	if c == nil || c.APIKey == "" {
		return ""
	}
	parts := strings.SplitN(c.APIKey, ".", 2)
	if len(parts) != 2 {
		return "****"
	}
	return parts[0] + ".****"
}

// zcodeAPIError 是上游业务错误（HTTP 状态 + 业务 code）。
type zcodeAPIError struct {
	Status  int
	Code    string
	Message string
}

func (e *zcodeAPIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("zcode upstream HTTP %d (code %s): %s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("zcode upstream HTTP %d: %s", e.Status, e.Message)
}

// ZCodeOAuthClient 执行 ZCode 授权流程并开通 CodingPlan 项目 key。
// 无状态：每次调用自行构造 http.Client（按会话里的 ProxyURL 决定是否走代理）。
type ZCodeOAuthClient struct{}

// NewZCodeOAuthClient 构造客户端。
func NewZCodeOAuthClient() *ZCodeOAuthClient {
	return &ZCodeOAuthClient{}
}

// BeginLogin 发起授权流程，返回授权地址与会话信息。
func (c *ZCodeOAuthClient) BeginLogin(ctx context.Context, site, proxyURL string) (*ZCodeLoginSession, error) {
	if !IsValidZCodeSite(site) {
		return nil, fmt.Errorf("invalid zcode site: %s", site)
	}
	seed := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, seed); err != nil {
		return nil, fmt.Errorf("failed to generate login state: %w", err)
	}
	bearer, device := deriveZCodeStateValues(seed)

	data, err := zcodeAPICall(ctx, proxyURL, http.MethodPost, ZCodeRootURL+zcodeCLIInitPath,
		"Bearer "+bearer, device, nil, map[string]string{"provider": site})
	if err != nil {
		return nil, err
	}
	var payload struct {
		FlowID       string          `json:"flow_id"`
		AuthorizeURL string          `json:"authorize_url"`
		PollInterval json.RawMessage `json:"poll_interval_sec"`
		ExpiresAt    json.RawMessage `json:"expires_at"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("zcode returned an invalid login flow: %w", err)
	}
	if payload.FlowID == "" || payload.AuthorizeURL == "" {
		return nil, fmt.Errorf("zcode returned an incomplete login flow")
	}
	authorizeURL, err := url.Parse(payload.AuthorizeURL)
	if err != nil || authorizeURL.Scheme != "https" || authorizeURL.Host == "" {
		return nil, fmt.Errorf("zcode returned an invalid authorization url")
	}

	interval := zcodeDefaultPollInterval
	if secs := zcodeNumeric(payload.PollInterval); secs > 0 {
		interval = time.Duration(secs) * time.Second
	}
	expiresAt := time.Now().Add(zcodeDefaultFlowTTL)
	if ts := zcodeNumeric(payload.ExpiresAt); ts > 0 {
		expiresAt = time.Unix(int64(ts), 0)
	}
	return &ZCodeLoginSession{
		Site:         site,
		FlowID:       payload.FlowID,
		AuthorizeURL: payload.AuthorizeURL,
		Bearer:       bearer,
		Device:       device,
		Interval:     interval,
		ExpiresAt:    expiresAt,
		ProxyURL:     proxyURL,
	}, nil
}

// ZCodePollStatus 是轮询结果的三种状态。
type ZCodePollStatus string

const (
	ZCodePollPending  ZCodePollStatus = "pending"
	ZCodePollReady    ZCodePollStatus = "ready"
	ZCodePollDeclined ZCodePollStatus = "declined"
)

// PollLogin 查询授权进度；ready 时返回身份凭据（尚未开通项目 key）。
func (c *ZCodeOAuthClient) PollLogin(ctx context.Context, session *ZCodeLoginSession) (ZCodePollStatus, *ZCodeCredential, error) {
	if session == nil {
		return ZCodePollPending, nil, fmt.Errorf("missing zcode login session")
	}
	data, err := zcodeAPICall(ctx, session.ProxyURL, http.MethodGet,
		ZCodeRootURL+zcodeCLIPollPath+url.PathEscape(session.FlowID),
		"Bearer "+session.Bearer, session.Device, nil, nil)
	if err != nil {
		// 4xx 视为该流程终止，其余（网络抖动、5xx、429）保持 pending 由前端继续轮询。
		var upstream *zcodeAPIError
		if ok := asZCodeAPIError(err, &upstream); ok && upstream.Status >= 400 && upstream.Status < 500 &&
			upstream.Status != http.StatusRequestTimeout && upstream.Status != http.StatusTooManyRequests {
			return ZCodePollDeclined, nil, err
		}
		return ZCodePollPending, nil, nil
	}

	var payload struct {
		Status string `json:"status"`
		Token  string `json:"token"`
		ZAI    struct {
			Access string `json:"access_token"`
		} `json:"zai"`
		BigModel struct {
			Access    string `json:"access_token"`
			Alternate string `json:"accessToken"`
		} `json:"bigmodel"`
		User struct {
			Email string          `json:"email"`
			Name  string          `json:"name"`
			ID    json.RawMessage `json:"user_id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return ZCodePollDeclined, nil, fmt.Errorf("zcode returned an invalid login response: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(payload.Status)) {
	case "", "pending":
		return ZCodePollPending, nil, nil
	case "ready":
	default:
		return ZCodePollDeclined, nil, nil
	}

	siteToken := payload.ZAI.Access
	if session.Site == ZCodeSiteBigModel {
		siteToken = payload.BigModel.Access
		if siteToken == "" {
			siteToken = payload.BigModel.Alternate
		}
	}
	if payload.Token == "" || siteToken == "" {
		return ZCodePollDeclined, nil, fmt.Errorf("zcode login response is missing required credentials")
	}

	email := strings.TrimSuffix(strings.TrimSpace(payload.User.Email), "@phone.local")
	credential := &ZCodeCredential{
		Site:            session.Site,
		JWT:             payload.Token,
		SiteAccessToken: siteToken,
		Device:          session.Device,
		UserID:          zcodeJSONScalar(payload.User.ID),
		Email:           email,
		Name:            strings.TrimSpace(payload.User.Name),
	}
	if credential.UserID == "" {
		credential.UserID = email
	}
	if credential.UserID == "" {
		credential.UserID = credential.Name
	}
	return ZCodePollReady, credential, nil
}

// ProvisionAPIKey 用登录身份在业务接口上复用/开通 CodingPlan 项目 key。
// 开通成功时填充 credential.APIKey / Plan / Org / Project / Team。
func (c *ZCodeOAuthClient) ProvisionAPIKey(ctx context.Context, credential *ZCodeCredential) error {
	if credential == nil {
		return fmt.Errorf("missing zcode credential")
	}
	root := ZCodeSiteRootURL(credential.Site)
	bizToken := credential.SiteAccessToken
	if credential.Site == ZCodeSiteZAI {
		// z.ai 需要先用站点 access_token 换业务令牌。
		data, err := zcodeAPICall(ctx, "", http.MethodPost, "https://api.z.ai/api/auth/z/login",
			"", "", nil, map[string]string{"token": credential.SiteAccessToken})
		if err != nil {
			return fmt.Errorf("zai business login failed: %w", err)
		}
		var payload struct {
			Access string `json:"access_token"`
		}
		if err := json.Unmarshal(data, &payload); err != nil || payload.Access == "" {
			return fmt.Errorf("zai business login returned no access token")
		}
		bizToken = "Bearer " + payload.Access
	}

	customer, err := c.fetchCustomer(ctx, root, bizToken)
	if err != nil {
		return err
	}

	// 先尝试个人套餐（默认机构 / 默认项目，排除团队项目类型 "2"）。
	if org, project, ok := zcodePickPersonalScope(customer); ok {
		key, keyErr := c.ensureProjectAPIKey(ctx, root, bizToken, org, project, false)
		if keyErr == nil {
			credential.APIKey = key
			credential.Org = org
			credential.Project = project
			if plan := c.fetchSubscriptionPlan(ctx, root, key); plan != "" {
				credential.Plan = plan
			}
			return nil
		}
		err = keyErr
	}

	// 再尝试团队席位。
	if org, project, plan, ok := c.findTeamSeat(ctx, root, bizToken, customer); ok {
		key, keyErr := c.ensureProjectAPIKey(ctx, root, bizToken, org, project, true)
		if keyErr != nil {
			return keyErr
		}
		credential.APIKey = key
		credential.Org = org
		credential.Project = project
		credential.Plan = plan
		credential.Team = true
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("this account has no GLM Coding Plan subscription")
}

type zcodeCustomerProject struct {
	ID   string
	Name string
	Type string
}

type zcodeCustomerOrg struct {
	ID       string
	Name     string
	Projects []zcodeCustomerProject
}

func (c *ZCodeOAuthClient) fetchCustomer(ctx context.Context, root, bizToken string) ([]zcodeCustomerOrg, error) {
	data, err := zcodeAPICall(ctx, "", http.MethodGet, root+"/api/biz/customer/getCustomerInfo", bizToken, "", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("account lookup failed: %w", err)
	}
	var payload struct {
		Organizations []struct {
			ID       json.RawMessage `json:"organizationId"`
			Name     string          `json:"organizationName"`
			Projects []struct {
				ID   json.RawMessage `json:"projectId"`
				Name string          `json:"projectName"`
				Type json.RawMessage `json:"projectType"`
			} `json:"projects"`
		} `json:"organizations"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("invalid customer information")
	}
	out := make([]zcodeCustomerOrg, 0, len(payload.Organizations))
	for _, org := range payload.Organizations {
		item := zcodeCustomerOrg{ID: zcodeJSONScalar(org.ID), Name: org.Name}
		for _, project := range org.Projects {
			item.Projects = append(item.Projects, zcodeCustomerProject{
				ID:   zcodeJSONScalar(project.ID),
				Name: project.Name,
				Type: zcodeJSONScalar(project.Type),
			})
		}
		out = append(out, item)
	}
	return out, nil
}

// zcodePickPersonalScope 选出个人机构/项目：优先名称含「默认机构」「默认项目」的条目。
func zcodePickPersonalScope(orgs []zcodeCustomerOrg) (string, string, bool) {
	var pickOrg, pickProject string
	for _, org := range orgs {
		if org.ID == "" {
			continue
		}
		var project string
		for _, candidate := range org.Projects {
			if candidate.ID == "" || candidate.Type == "2" {
				continue
			}
			if project == "" || strings.Contains(candidate.Name, "默认项目") {
				project = candidate.ID
			}
			if strings.Contains(candidate.Name, "默认项目") {
				break
			}
		}
		if project == "" {
			continue
		}
		if pickOrg == "" || strings.Contains(org.Name, "默认机构") {
			pickOrg, pickProject = org.ID, project
		}
		if strings.Contains(org.Name, "默认机构") {
			break
		}
	}
	return pickOrg, pickProject, pickOrg != ""
}

// findTeamSeat 在团队项目里寻找生效且席位有效的 CodingPlan。
func (c *ZCodeOAuthClient) findTeamSeat(ctx context.Context, root, bizToken string, orgs []zcodeCustomerOrg) (string, string, string, bool) {
	for _, org := range orgs {
		for _, project := range org.Projects {
			if org.ID == "" || project.ID == "" || project.Type != "2" {
				continue
			}
			headers := zcodeTeamHeaders(org.ID, project.ID)
			data, err := zcodeAPICall(ctx, "", http.MethodGet,
				root+"/api/biz/team/subscribe/product/querySubscribeDetail", bizToken, "", headers, nil)
			if err != nil {
				continue
			}
			var detail struct {
				Has     *bool  `json:"hasSubscription"`
				Status  string `json:"status"`
				Grant   string `json:"memberGrantStatus"`
				Product string `json:"productName"`
			}
			if err := json.Unmarshal(data, &detail); err != nil {
				continue
			}
			if (detail.Has == nil || *detail.Has) &&
				strings.EqualFold(detail.Status, "EFFECTIVE") &&
				strings.EqualFold(detail.Grant, "VALID") {
				plan := detail.Product
				if plan == "" {
					plan = "GLM Coding Team"
				}
				return org.ID, project.ID, plan, true
			}
		}
	}
	return "", "", "", false
}

func zcodeTeamHeaders(org, project string) http.Header {
	return http.Header{
		"Bigmodel-Organization": {org},
		"Bigmodel-Project":      {project},
		"Set-Language":          {"zh"},
	}
}

// ensureProjectAPIKey 复用或新建项目 API key，并取回完整明文（id.secret）。
func (c *ZCodeOAuthClient) ensureProjectAPIKey(ctx context.Context, root, bizToken, org, project string, team bool) (string, error) {
	endpoint := root + "/api/biz/v1/organization/" + url.PathEscape(org) + "/projects/" + url.PathEscape(project) + "/api_keys"
	var headers http.Header
	if team {
		headers = zcodeTeamHeaders(org, project)
	}
	data, err := zcodeAPICall(ctx, "", http.MethodGet, endpoint, bizToken, "", headers, nil)
	if err != nil {
		return "", fmt.Errorf("project key lookup failed: %w", err)
	}
	var keys []struct {
		Name   string          `json:"name"`
		APIKey json.RawMessage `json:"apiKey"`
		Type   json.RawMessage `json:"keyType"`
	}
	if err := json.Unmarshal(data, &keys); err != nil {
		return "", fmt.Errorf("invalid project key list")
	}
	keyID := ""
	for _, key := range keys {
		if key.Name != zcodeAPIKeyName {
			continue
		}
		if team && zcodeJSONScalar(key.Type) != "2" {
			continue
		}
		if id := zcodeJSONScalar(key.APIKey); id != "" {
			keyID = id
		}
	}
	if keyID == "" {
		body := map[string]any{"name": zcodeAPIKeyName}
		if team {
			body["keyType"] = 2
		}
		created, createErr := zcodeAPICall(ctx, "", http.MethodPost, endpoint, bizToken, "", headers, body)
		if createErr != nil {
			return "", fmt.Errorf("project key creation failed: %w", createErr)
		}
		var payload struct {
			APIKey json.RawMessage `json:"apiKey"`
		}
		if err := json.Unmarshal(created, &payload); err != nil {
			return "", fmt.Errorf("invalid project key response")
		}
		keyID = zcodeJSONScalar(payload.APIKey)
		if keyID == "" {
			return "", fmt.Errorf("project key response has no identifier")
		}
	}
	copied, err := zcodeAPICall(ctx, "", http.MethodGet, endpoint+"/copy/"+url.PathEscape(keyID), bizToken, "", headers, nil)
	if err != nil {
		return "", fmt.Errorf("project key secret lookup failed: %w", err)
	}
	var payload struct {
		Secret string `json:"secretKey"`
	}
	if err := json.Unmarshal(copied, &payload); err != nil || payload.Secret == "" {
		return "", fmt.Errorf("project key response has no secret")
	}
	return keyID + "." + payload.Secret, nil
}

// fetchSubscriptionPlan 用项目 key 查订阅列表，返回生效产品名（尽力而为）。
func (c *ZCodeOAuthClient) fetchSubscriptionPlan(ctx context.Context, root, apiKey string) string {
	data, err := zcodeAPICall(ctx, "", http.MethodGet, root+"/api/biz/subscription/list", apiKey, "", nil, nil)
	if err != nil {
		return ""
	}
	var subs []struct {
		Status  string `json:"status"`
		Product string `json:"productName"`
	}
	if json.Unmarshal(data, &subs) != nil {
		return ""
	}
	for _, sub := range subs {
		if strings.EqualFold(sub.Status, "VALID") {
			return sub.Product
		}
	}
	return ""
}

// deriveZCodeStateValues 由同一份随机种子派生 (bearer, device)：
// bearer 为种子的 hex；device 为 sha256("zcode-device-v1\x00" + seed) 前 16 字节，
// 并置位 UUIDv4 版本/变体位。与 ZCode 客户端行为一致。
func deriveZCodeStateValues(seed []byte) (string, string) {
	digest := sha256.Sum256(append([]byte("zcode-device-v1\x00"), seed...))
	b := digest[:16]
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	device := fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:16])
	return hex.EncodeToString(seed), device
}

// zcodeAPICall 调用 ZCode 业务接口并解出 {code,msg,data} 信封里的 data。
func zcodeAPICall(ctx context.Context, proxyURL, method, target, auth, device string, extra http.Header, body any) (json.RawMessage, error) {
	var payload []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("invalid request body: %w", err)
		}
		payload = encoded
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ZCode/3.2.5")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if device != "" {
		req.Header.Set("X-Device-Mid", device)
	}
	for key, values := range extra {
		req.Header[key] = append([]string(nil), values...)
	}

	client, err := zcodeHTTPClient(proxyURL)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("zcode request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("zcode response read failed: %w", err)
	}

	var envelope struct {
		Code json.RawMessage `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, &zcodeAPIError{Status: resp.StatusCode, Message: strings.TrimSpace(string(raw))}
		}
		return nil, fmt.Errorf("zcode returned a non-JSON response")
	}
	code := zcodeJSONScalar(envelope.Code)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || (code != "" && code != "0" && code != "200") {
		return nil, &zcodeAPIError{Status: resp.StatusCode, Code: code, Message: envelope.Msg}
	}
	return envelope.Data, nil
}

// zcodeHTTPClient 按需构造带代理的 http.Client。
func zcodeHTTPClient(proxyURL string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 60 * time.Second
	if strings.TrimSpace(proxyURL) != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy url: %w", err)
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	return &http.Client{Transport: transport, Timeout: 60 * time.Second}, nil
}

func asZCodeAPIError(err error, target **zcodeAPIError) bool {
	for err != nil {
		if typed, ok := err.(*zcodeAPIError); ok {
			*target = typed
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}

// zcodeJSONScalar 把可能是字符串/数字/布尔/对象的 JSON 值转成字符串。
func zcodeJSONScalar(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var number json.Number
	if err := json.Unmarshal(trimmed, &number); err == nil {
		return number.String()
	}
	return strings.TrimSpace(string(trimmed))
}

// zcodeNumeric 解析可能是字符串/数字的 JSON 数值。
func zcodeNumeric(raw json.RawMessage) float64 {
	text := zcodeJSONScalar(raw)
	if text == "" {
		return 0
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0
	}
	return value
}
