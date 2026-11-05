package admin

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// ZCodeOAuthHandler 暴露 ZCode（Z.ai / BigModel）OAuth 登录与账号开通接口。
// 登录成功后自动复用/开通 CodingPlan 项目 key，账号固定走 ultra 端点。
type ZCodeOAuthHandler struct {
	zcodeOAuthService *service.ZCodeOAuthService
	adminService      service.AdminService
}

// NewZCodeOAuthHandler 构造 handler。
func NewZCodeOAuthHandler(zcodeOAuthService *service.ZCodeOAuthService, adminService service.AdminService) *ZCodeOAuthHandler {
	return &ZCodeOAuthHandler{
		zcodeOAuthService: zcodeOAuthService,
		adminService:      adminService,
	}
}

// GetSites 返回可选的登录入口（Z.AI / BigModel）。
func (h *ZCodeOAuthHandler) GetSites(c *gin.Context) {
	response.Success(c, h.zcodeOAuthService.Sites())
}

type ZCodeGenerateAuthURLRequest struct {
	Site    string `json:"site"`
	ProxyID *int64 `json:"proxy_id"`
}

// GenerateAuthURL 发起一次 OAuth 授权，返回浏览器授权地址与本地 state。
func (h *ZCodeOAuthHandler) GenerateAuthURL(c *gin.Context) {
	var req ZCodeGenerateAuthURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	result, err := h.zcodeOAuthService.StartLogin(c.Request.Context(), req.Site, req.ProxyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

type ZCodePollRequest struct {
	State string `json:"state" binding:"required"`
}

// Poll 轮询授权进度；首次 ready 时自动开通 CodingPlan 项目 key。
func (h *ZCodeOAuthHandler) Poll(c *gin.Context) {
	var req ZCodePollRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	result, err := h.zcodeOAuthService.Poll(c.Request.Context(), req.State)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

type ZCodeCreateAccountRequest struct {
	State       string  `json:"state" binding:"required"`
	Name        string  `json:"name"`
	Notes       *string `json:"notes"`
	APIKey      string  `json:"api_key"`
	ProxyID     *int64  `json:"proxy_id"`
	Concurrency int     `json:"concurrency"`
	Priority    int     `json:"priority"`
	GroupIDs    []int64 `json:"group_ids"`
}

// CreateAccount 用已完成的登录创建账号。
// 未提供 api_key 时使用登录流程自动开通的 CodingPlan key。
func (h *ZCodeOAuthHandler) CreateAccount(c *gin.Context) {
	var req ZCodeCreateAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	credential, err := h.zcodeOAuthService.CredentialFor(req.State)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	credentials := h.zcodeOAuthService.BuildAccountCredentials(credential, req.APIKey)
	if strings.TrimSpace(credentials["api_key"].(string)) == "" {
		response.BadRequest(c, "CodingPlan API key is required: automatic provisioning failed, paste the key manually")
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		switch {
		case credential.Email != "":
			name = "ZCode " + credential.Email
		case credential.Name != "":
			name = "ZCode " + credential.Name
		default:
			name = "ZCode OAuth"
		}
	}

	concurrency := req.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}

	account, err := h.adminService.CreateAccount(c.Request.Context(), &service.CreateAccountInput{
		Name:        name,
		Notes:       req.Notes,
		Platform:    service.PlatformZhipu,
		Type:        service.AccountTypeAPIKey,
		Credentials: credentials,
		Extra: map[string]any{
			// ultra 端点只认 Authorization: Bearer（实测 x-api-key 返回异常响应）。
			"anthropic_apikey_auth_scheme": service.AnthropicAPIKeyAuthSchemeAuthorizationBearer,
		},
		ProxyID:     req.ProxyID,
		Concurrency: concurrency,
		Priority:    req.Priority,
		GroupIDs:    req.GroupIDs,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.AccountFromService(account))
}
