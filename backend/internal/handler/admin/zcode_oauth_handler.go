package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// ZCodeOAuthHandler 暴露 ZCode（Z.ai / BigModel）OAuth 登录、账号开通与重置卡接口。
// 登录成功后自动复用/开通 CodingPlan 项目 key，账号固定走 ultra 端点。
type ZCodeOAuthHandler struct {
	zcodeOAuthService *service.ZCodeOAuthService
	zcodeResetService *service.ZCodeResetService
	adminService      service.AdminService
}

// NewZCodeOAuthHandler 构造 handler。
func NewZCodeOAuthHandler(zcodeOAuthService *service.ZCodeOAuthService, zcodeResetService *service.ZCodeResetService, adminService service.AdminService) *ZCodeOAuthHandler {
	return &ZCodeOAuthHandler{
		zcodeOAuthService: zcodeOAuthService,
		zcodeResetService: zcodeResetService,
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
	PeakPause   bool    `json:"peak_pause"`
}

// CreateAccount 用已完成的登录创建账号；同一 ZCode 用户重新登录时更新其凭据。
func (h *ZCodeOAuthHandler) CreateAccount(c *gin.Context) {
	var req ZCodeCreateAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	ctx := c.Request.Context()
	draft, err := h.zcodeOAuthService.AccountDraft(ctx, req.State, service.ZCodeAccountOptions{
		Name:        req.Name,
		Notes:       req.Notes,
		APIKey:      req.APIKey,
		ProxyID:     req.ProxyID,
		Concurrency: req.Concurrency,
		Priority:    req.Priority,
		GroupIDs:    req.GroupIDs,
		PeakPause:   req.PeakPause,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var account *service.Account
	if draft.ExistingID != 0 {
		account, err = h.adminService.UpdateAccount(ctx, draft.ExistingID, &service.UpdateAccountInput{Credentials: draft.Input.Credentials})
	} else {
		account, err = h.adminService.CreateAccount(ctx, draft.Input)
	}
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.zcodeOAuthService.Finish(req.State)
	response.Success(c, dto.AccountFromService(account))
}

type ZCodeResetRequest struct {
	ResetType string `json:"reset_type" binding:"required"`
}

// GetResetCards 返回账号可用的重置卡。force=true 时绕过缓存向上游查询。
func (h *ZCodeOAuthHandler) GetResetCards(c *gin.Context) {
	id, ok := zcodeAccountID(c)
	if !ok {
		return
	}
	inventory, err := h.zcodeResetService.Inventory(c.Request.Context(), id, c.Query("force") == "true")
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, inventory)
}

// UseResetCard 消耗一张重置卡，立即重置对应额度窗口。
func (h *ZCodeOAuthHandler) UseResetCard(c *gin.Context) {
	id, ok := zcodeAccountID(c)
	if !ok {
		return
	}
	var req ZCodeResetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	inventory, err := h.zcodeResetService.UseCard(c.Request.Context(), id, req.ResetType)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, inventory)
}

type ZCodeSettingsRequest struct {
	PeakPause *bool `json:"peak_pause"`
	AutoClaim *bool `json:"auto_claim"`
}

// UpdateSettings 设置高峰暂停与自动领卡开关，未提供的字段保持不变。
func (h *ZCodeOAuthHandler) UpdateSettings(c *gin.Context) {
	id, ok := zcodeAccountID(c)
	if !ok {
		return
	}
	var req ZCodeSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := h.zcodeResetService.UpdateSettings(c.Request.Context(), id, req.PeakPause, req.AutoClaim); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"id": id})
}

func zcodeAccountID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return 0, false
	}
	return id, true
}
