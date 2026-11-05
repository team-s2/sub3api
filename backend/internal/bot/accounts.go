package bot

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const accountPageSize = 500

const (
	stateReady      = "ready"
	stateDisabled   = "disabled"
	statePeakPaused = "peak_paused"
	stateExhausted  = "quota_exhausted"
	stateInvalid    = "invalid"
)

func (a *App) accounts(ctx context.Context) ([]service.Account, error) {
	var all []service.Account
	for page := 1; ; page++ {
		accounts, total, err := a.admin.ListAccounts(ctx, page, accountPageSize, "", "", "", "", 0, "", "id", "asc")
		if err != nil {
			return nil, err
		}
		all = append(all, accounts...)
		if len(accounts) == 0 || int64(len(all)) >= total {
			return all, nil
		}
	}
}

// targets resolves a comma-separated list of names or IDs. Only bulk-safe
// commands accept the all keyword.
func (a *App) targets(ctx context.Context, value string, allowAll bool) ([]*service.Account, error) {
	accounts, err := a.accounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("账号查询失败：%w", err)
	}
	if value == "all" {
		if !allowAll {
			return nil, errors.New("该命令不支持 all。")
		}
		targets := make([]*service.Account, 0, len(accounts))
		for i := range accounts {
			targets = append(targets, &accounts[i])
		}
		return targets, nil
	}
	names := strings.Split(value, ",")
	targets := make([]*service.Account, 0, len(names))
	for _, name := range names {
		account, err := findAccount(accounts, name)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(targets, account) {
			targets = append(targets, account)
		}
	}
	return targets, nil
}

func findAccount(accounts []service.Account, name string) (*service.Account, error) {
	id, idErr := strconv.ParseInt(name, 10, 64)
	var match *service.Account
	for i := range accounts {
		account := &accounts[i]
		if idErr == nil && account.ID == id {
			return account, nil
		}
		if account.Name != name {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("账号名称 %s 不唯一，请使用 ID。", name)
		}
		match = account
	}
	if match == nil {
		return nil, fmt.Errorf("未找到账号：%s", name)
	}
	return match, nil
}

func accountState(account *service.Account) string {
	now := time.Now()
	switch {
	case account.Status != service.StatusActive:
		return stateInvalid
	case !account.Schedulable:
		return stateDisabled
	case account.IsZCodePeakPaused(now):
		return statePeakPaused
	case account.RateLimitResetAt != nil && account.RateLimitResetAt.After(now):
		return stateExhausted
	}
	return stateReady
}
