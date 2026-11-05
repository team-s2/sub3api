package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// ZCode 重置卡分 5 小时和周两种，只重置各自窗口。卡由客户端调用 /reset/opportunity
// 触发发放：托管账号没有桌面端挂机，由这里的巡检代为领取，节奏对齐 ZCode Desktop。
const (
	ZCodeResetTypeFiveHour = "FIVE_HOUR"
	ZCodeResetTypeWeek     = "WEEK"

	zcodeResetStatusPath      = "/api/v1/coding-plan/reset/status"
	zcodeResetOpportunityPath = "/api/v1/coding-plan/reset/opportunity"
	zcodeResetUsePath         = "/api/v1/coding-plan/reset/use"

	zcodeExtraPeakPause = "zcode_peak_pause"
	zcodeExtraAutoClaim = "zcode_auto_claim"

	zcodeCodeNotEligible = "3301"
	zcodeCodeRetryable   = "2007"

	zcodeSweepInterval     = time.Minute
	zcodeSweepAccountLimit = 30 * time.Second
	zcodeInventoryTTL      = 10 * time.Minute
	zcodeClaimGrantedDelay = 10 * time.Minute
	zcodeClaimLimitedDelay = 10 * time.Minute
	zcodeClaimRetryDelay   = 5 * time.Minute
	zcodeCardExpiringSoon  = 3 * time.Minute

	zcodePeakStartHour = 14
	zcodePeakEndHour   = 18
)

// BigModel 的时段按北京时间计算，与服务器时区无关。
var zcodePeakZone = time.FixedZone("CST", 8*3600)

// isZCodePeakHour 报告是否处于全价时段：工作日 14:00–18:00（不含法定节假日调休）。
func isZCodePeakHour(now time.Time) bool {
	local := now.In(zcodePeakZone)
	weekday := local.Weekday()
	return weekday != time.Saturday && weekday != time.Sunday &&
		local.Hour() >= zcodePeakStartHour && local.Hour() < zcodePeakEndHour
}

// IsZCodeAccount 报告账号是否由 ZCode OAuth 登录，只有这类账号持有重置卡凭据字段。
func (a *Account) IsZCodeAccount() bool {
	return a != nil && a.Platform == PlatformZhipu && a.GetCredential("zcode_user_id") != ""
}

// IsZCodePeakPaused 报告账号此刻是否因高峰暂停调度。只影响新请求的选择，
// 进行中的请求和卡券巡检不受影响。
func (a *Account) IsZCodePeakPaused(now time.Time) bool {
	if a.Platform != PlatformZhipu || !isZCodePeakHour(now) {
		return false
	}
	return a.getExtraBool(zcodeExtraPeakPause)
}

// ZCodeAutoClaimEnabled 报告是否自动领卡，未设置时默认开启。
func (a *Account) ZCodeAutoClaimEnabled() bool {
	enabled, set := a.Extra[zcodeExtraAutoClaim].(bool)
	return !set || enabled
}

// ZCodeResetCard 是一张可用的重置卡。
type ZCodeResetCard struct {
	Type      string    `json:"type"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ZCodeResetInventory 是账号的重置卡库存。
type ZCodeResetInventory struct {
	Cards      []ZCodeResetCard `json:"cards"`
	ObservedAt time.Time        `json:"observed_at"`
}

// zcodeResetAuth 是重置接口要求的双令牌身份。
type zcodeResetAuth struct {
	jwt         string
	accessToken string
	org         string
	project     string
	team        bool
	proxyURL    string
}

func (auth zcodeResetAuth) call(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	headers := http.Header{"X-Bigmodel-Authorization": {auth.accessToken}, "Bigmodel-Target-Type": {"PERSONAL"}}
	if auth.team {
		headers["Bigmodel-Target-Type"] = []string{"TEAM"}
		headers["Bigmodel-Organization"] = []string{auth.org}
		headers["Bigmodel-Project"] = []string{auth.project}
	}
	return zcodeAPICall(ctx, zcodeRequest{
		Method: method, URL: ZCodeRootURL + path, Auth: "Bearer " + auth.jwt, Headers: headers, ProxyURL: auth.proxyURL, Body: body,
	})
}

func (auth zcodeResetAuth) status(ctx context.Context, now time.Time) (*ZCodeResetInventory, error) {
	data, err := auth.call(ctx, http.MethodGet, zcodeResetStatusPath, nil)
	if err != nil {
		return nil, err
	}
	var payload struct {
		FiveHour []struct {
			ExpireAt int64 `json:"expire_at"`
		} `json:"available_five_hour_resets"`
		Week []struct {
			ExpireAt int64 `json:"expire_at"`
		} `json:"available_week_resets"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("invalid reset status: %w", err)
	}
	inventory := &ZCodeResetInventory{Cards: []ZCodeResetCard{}, ObservedAt: now}
	for _, card := range payload.FiveHour {
		inventory.Cards = append(inventory.Cards, ZCodeResetCard{Type: ZCodeResetTypeFiveHour, ExpiresAt: time.UnixMilli(card.ExpireAt)})
	}
	for _, card := range payload.Week {
		inventory.Cards = append(inventory.Cards, ZCodeResetCard{Type: ZCodeResetTypeWeek, ExpiresAt: time.UnixMilli(card.ExpireAt)})
	}
	return inventory, nil
}

func (auth zcodeResetAuth) use(ctx context.Context, resetType, idempotencyKey string) error {
	data, err := auth.call(ctx, http.MethodPost, zcodeResetUsePath, map[string]string{"reset_type": resetType, "idempotency_key": idempotencyKey})
	if err != nil {
		return err
	}
	var payload struct {
		Used bool `json:"used"`
	}
	if err := json.Unmarshal(data, &payload); err != nil || !payload.Used {
		return errors.New("upstream did not consume the reset card")
	}
	return nil
}

// zcodeClaimState 是单个账号的领卡状态。进程重启后清空，最多多发一次幂等请求。
type zcodeClaimState struct {
	nextClaimAt time.Time
	retryKey    string
	inventory   *ZCodeResetInventory
}

// ZCodeResetService 巡检 ZCode 账号的重置卡，并提供查询与使用接口。
type ZCodeResetService struct {
	accountRepo AccountRepository
	proxyRepo   ProxyRepository
	mu          sync.Mutex
	states      map[int64]*zcodeClaimState
	stopCh      chan struct{}
	stopOnce    sync.Once
	wg          sync.WaitGroup
}

// NewZCodeResetService 构造服务。
func NewZCodeResetService(accountRepo AccountRepository, proxyRepo ProxyRepository) *ZCodeResetService {
	return &ZCodeResetService{
		accountRepo: accountRepo,
		proxyRepo:   proxyRepo,
		states:      make(map[int64]*zcodeClaimState),
		stopCh:      make(chan struct{}),
	}
}

// Start 启动巡检。
func (s *ZCodeResetService) Start() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(zcodeSweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.sweep(context.Background())
			case <-s.stopCh:
				return
			}
		}
	}()
}

// Stop 停止巡检并等待当前一轮结束。
func (s *ZCodeResetService) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
}

func (s *ZCodeResetService) sweep(ctx context.Context) {
	accounts, err := s.accountRepo.ListByPlatform(ctx, PlatformZhipu)
	if err != nil {
		logger.LegacyPrintf("service.zcode", "[ZCode] list accounts failed: %v", err)
		return
	}
	for i := range accounts {
		account := &accounts[i]
		if !account.IsZCodeAccount() || !account.IsActive() {
			continue
		}
		accountCtx, cancel := context.WithTimeout(ctx, zcodeSweepAccountLimit)
		s.sweepAccount(accountCtx, account, time.Now())
		cancel()
	}
}

// sweepAccount 只看账号是否启用：高峰暂停的账号同样要领卡，否则临期的卡会白白过期。
func (s *ZCodeResetService) sweepAccount(ctx context.Context, account *Account, now time.Time) {
	auth, err := s.resetAuth(ctx, account)
	if err != nil {
		return
	}
	state := s.state(account.ID)
	refresh := state.inventory == nil || now.Sub(state.inventory.ObservedAt) >= zcodeInventoryTTL
	if account.ZCodeAutoClaimEnabled() && !now.Before(state.nextClaimAt) {
		refresh = s.claim(ctx, account.ID, auth, state, now) || refresh
	}
	if refresh {
		inventory, statusErr := auth.status(ctx, now)
		if statusErr != nil {
			logger.LegacyPrintf("service.zcode", "[ZCode] account %d reset status failed: %v", account.ID, statusErr)
			return
		}
		s.storeInventory(state, inventory)
	}
	s.useExpiringCards(ctx, account.ID, auth, state, now)
}

// claim 跑一次领卡状态机，返回是否发了新卡。3301 是日常的未达条件，不算失败。
func (s *ZCodeResetService) claim(ctx context.Context, accountID int64, auth zcodeResetAuth, state *zcodeClaimState, now time.Time) bool {
	key := state.retryKey
	if key == "" {
		generated, err := newZCodeIdempotencyKey()
		if err != nil {
			return false
		}
		key = generated
	}
	data, err := auth.call(ctx, http.MethodPost, zcodeResetOpportunityPath, map[string]string{"idempotency_key": key})
	outcome := zcodeClaimOutcome(data, err, now)
	state.nextClaimAt = outcome.nextAt
	state.retryKey = ""
	if outcome.retry {
		state.retryKey = key
	}
	if outcome.logged != nil {
		logger.LegacyPrintf("service.zcode", "[ZCode] account %d claim failed: %v", accountID, outcome.logged)
	}
	if outcome.granted {
		logger.LegacyPrintf("service.zcode", "[ZCode] account %d was granted a reset card", accountID)
	}
	return outcome.granted
}

type zcodeClaimResult struct {
	granted bool
	retry   bool
	nextAt  time.Time
	logged  error
}

func zcodeClaimOutcome(data json.RawMessage, err error, now time.Time) zcodeClaimResult {
	var upstream *zcodeAPIError
	switch {
	case err == nil:
		var payload struct {
			Granted bool `json:"granted"`
		}
		if json.Unmarshal(data, &payload) != nil {
			return zcodeClaimResult{nextAt: now.Add(zcodeClaimRetryDelay), logged: errors.New("invalid claim response")}
		}
		return zcodeClaimResult{granted: payload.Granted, nextAt: now.Add(zcodeClaimGrantedDelay)}
	case !errors.As(err, &upstream), upstream.Code == zcodeCodeRetryable, upstream.Status >= 500:
		return zcodeClaimResult{retry: true, nextAt: now.Add(zcodeClaimRetryDelay), logged: err}
	case upstream.Code == zcodeCodeNotEligible:
		return zcodeClaimResult{nextAt: zcodeNextClaimAt(upstream.Data, now)}
	case upstream.Status == http.StatusTooManyRequests, upstream.Code == strconv.Itoa(http.StatusTooManyRequests):
		return zcodeClaimResult{nextAt: now.Add(zcodeClaimLimitedDelay)}
	}
	return zcodeClaimResult{nextAt: now.Add(zcodeClaimLimitedDelay), logged: err}
}

// zcodeNextClaimAt 取上游给的下次时间，保底 5 分钟。next_try_at 是毫秒时间戳。
func zcodeNextClaimAt(data json.RawMessage, now time.Time) time.Time {
	nextAt := now.Add(zcodeClaimRetryDelay)
	var payload struct {
		NextTryAt int64 `json:"next_try_at"`
	}
	if json.Unmarshal(data, &payload) == nil && payload.NextTryAt > 0 {
		if server := time.UnixMilli(payload.NextTryAt); server.After(nextAt) {
			return server
		}
	}
	return nextAt
}

// useExpiringCards 在卡过期前用掉它。幂等键由卡本身派生，超时重试不会多花一张。
func (s *ZCodeResetService) useExpiringCards(ctx context.Context, accountID int64, auth zcodeResetAuth, state *zcodeClaimState, now time.Time) {
	inventory := s.currentInventory(state)
	if inventory == nil {
		return
	}
	used := false
	for _, card := range inventory.Cards {
		if !card.ExpiresAt.After(now) || card.ExpiresAt.Sub(now) > zcodeCardExpiringSoon {
			continue
		}
		digest := sha256.Sum256(fmt.Appendf(nil, "%d:%s:%d", accountID, card.Type, card.ExpiresAt.UnixMilli()))
		if err := auth.use(ctx, card.Type, hex.EncodeToString(digest[:16])); err != nil {
			logger.LegacyPrintf("service.zcode", "[ZCode] account %d auto reset failed: %v", accountID, err)
			continue
		}
		logger.LegacyPrintf("service.zcode", "[ZCode] account %d used a %s card before it expired", accountID, card.Type)
		used = true
	}
	if used {
		s.storeInventory(state, nil)
	}
}

// Inventory 返回账号的重置卡库存，缓存过期或 force 时向上游查询。
func (s *ZCodeResetService) Inventory(ctx context.Context, accountID int64, force bool) (*ZCodeResetInventory, error) {
	account, auth, err := s.managedAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	state := s.state(account.ID)
	now := time.Now()
	if cached := s.currentInventory(state); !force && cached != nil && now.Sub(cached.ObservedAt) < zcodeInventoryTTL {
		return cached, nil
	}
	inventory, err := auth.status(ctx, now)
	if err != nil {
		return nil, zcodeUpstreamError(err)
	}
	s.storeInventory(state, inventory)
	return inventory, nil
}

// UseCard 消耗一张指定类型的卡并返回最新库存。
func (s *ZCodeResetService) UseCard(ctx context.Context, accountID int64, resetType string) (*ZCodeResetInventory, error) {
	resetType = strings.ToUpper(strings.TrimSpace(resetType))
	if resetType != ZCodeResetTypeFiveHour && resetType != ZCodeResetTypeWeek {
		return nil, infraerrors.New(http.StatusBadRequest, "ZCODE_RESET_INVALID_TYPE", "reset_type must be FIVE_HOUR or WEEK")
	}
	account, auth, err := s.managedAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	key, err := newZCodeIdempotencyKey()
	if err != nil {
		return nil, infraerrors.New(http.StatusInternalServerError, "ZCODE_RESET_KEY_FAILED", "failed to generate idempotency key")
	}
	if err := auth.use(ctx, resetType, key); err != nil {
		return nil, zcodeUpstreamError(err)
	}
	logger.LegacyPrintf("service.zcode", "[ZCode] account %d used a %s card on request", account.ID, resetType)
	state := s.state(account.ID)
	s.storeInventory(state, nil)
	return s.Inventory(ctx, account.ID, true)
}

// UpdateSettings 写入账号开关，nil 表示不修改。
func (s *ZCodeResetService) UpdateSettings(ctx context.Context, accountID int64, peakPause, autoClaim *bool) error {
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return err
	}
	if account.Platform != PlatformZhipu {
		return infraerrors.New(http.StatusBadRequest, "ZCODE_ACCOUNT_REQUIRED", "account is not a zhipu account")
	}
	updates := map[string]any{}
	if peakPause != nil {
		updates[zcodeExtraPeakPause] = *peakPause
	}
	if autoClaim != nil {
		updates[zcodeExtraAutoClaim] = *autoClaim
	}
	if len(updates) == 0 {
		return nil
	}
	return s.accountRepo.UpdateExtra(ctx, accountID, updates)
}

func (s *ZCodeResetService) managedAccount(ctx context.Context, accountID int64) (*Account, zcodeResetAuth, error) {
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, zcodeResetAuth{}, err
	}
	if !account.IsZCodeAccount() {
		return nil, zcodeResetAuth{}, infraerrors.New(http.StatusBadRequest, "ZCODE_ACCOUNT_REQUIRED", "account did not sign in through ZCode OAuth")
	}
	auth, err := s.resetAuth(ctx, account)
	if err != nil {
		return nil, zcodeResetAuth{}, err
	}
	return account, auth, nil
}

func (s *ZCodeResetService) resetAuth(ctx context.Context, account *Account) (zcodeResetAuth, error) {
	auth := zcodeResetAuth{
		jwt:         account.GetCredential("zcode_jwt"),
		accessToken: account.GetCredential("zcode_access_token"),
		org:         account.GetCredential("zcode_org"),
		project:     account.GetCredential("zcode_project"),
	}
	auth.team, _ = account.Credentials["zcode_team"].(bool)
	if auth.jwt == "" || auth.accessToken == "" {
		return auth, infraerrors.New(http.StatusBadRequest, "ZCODE_RELOGIN_REQUIRED", "account has no reset credentials, sign in with ZCode OAuth again")
	}
	if account.ProxyID != nil {
		proxy, err := s.proxyRepo.GetByID(ctx, *account.ProxyID)
		if err != nil {
			return auth, fmt.Errorf("load account proxy: %w", err)
		}
		auth.proxyURL = proxy.URL()
	}
	return auth, nil
}

func (s *ZCodeResetService) state(accountID int64) *zcodeClaimState {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.states[accountID]
	if !ok {
		state = &zcodeClaimState{}
		s.states[accountID] = state
	}
	return state
}

func (s *ZCodeResetService) currentInventory(state *zcodeClaimState) *ZCodeResetInventory {
	s.mu.Lock()
	defer s.mu.Unlock()
	return state.inventory
}

func (s *ZCodeResetService) storeInventory(state *zcodeClaimState, inventory *ZCodeResetInventory) {
	s.mu.Lock()
	state.inventory = inventory
	s.mu.Unlock()
}

func zcodeUpstreamError(err error) error {
	var upstream *zcodeAPIError
	if errors.As(err, &upstream) && (upstream.Status == http.StatusUnauthorized || upstream.Status == http.StatusForbidden) {
		return infraerrors.New(http.StatusBadGateway, "ZCODE_RELOGIN_REQUIRED", "zcode rejected the stored login, sign in with ZCode OAuth again")
	}
	return infraerrors.Newf(http.StatusBadGateway, "ZCODE_RESET_FAILED", "zcode reset request failed: %v", err)
}

func newZCodeIdempotencyKey() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
