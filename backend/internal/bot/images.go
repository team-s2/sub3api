package bot

import (
	"context"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"time"
	_ "time/tzdata" // images use Asia/Shanghai whatever the host provides

	"github.com/Wei-Shaw/sub2api/internal/onebot"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/render"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	fiveHours = 5 * time.Hour
	sevenDays = 7 * 24 * time.Hour
)

var (
	imageZone  = mustLoadLocation("Asia/Shanghai")
	flowPeriod = regexp.MustCompile(`^([1-9][0-9]{0,3})([mhdw])$`)
)

func mustLoadLocation(name string) *time.Location {
	location, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return location
}

func (a *App) quota(ctx context.Context, event onebot.Event, peer Peer, args []string) string {
	if len(args) > 1 || len(args) == 1 && args[0] != "all" {
		return fmt.Sprintf("用法：%s quota [all]", a.command)
	}
	accounts, err := a.accounts(ctx)
	if err != nil {
		return "账号查询失败：" + err.Error()
	}
	shown := make([]*service.Account, 0, len(accounts))
	ids := make([]int64, 0, len(accounts))
	for i := range accounts {
		if len(args) == 1 || accountState(&accounts[i]) != stateDisabled {
			shown = append(shown, &accounts[i])
			ids = append(ids, accounts[i].ID)
		}
	}
	if len(shown) == 0 {
		return "当前没有账号。"
	}
	usage, usageErrors, err := a.usage.GetUsageBatch(ctx, ids, false)
	if err != nil {
		return "额度查询失败：" + err.Error()
	}
	stats, err := a.usage.GetTodayStatsBatch(ctx, ids)
	if err != nil {
		logger.LegacyPrintf("bot", "[Bot] today stats failed: %v", err)
	}
	rows := make([]render.QuotaRow, 0, len(shown))
	for _, account := range shown {
		rows = append(rows, quotaRow(account, usage[account.ID], usageErrors[account.ID], stats[account.ID]))
	}
	png, err := render.Quota(rows, imageZone, "", time.Now())
	if err != nil {
		logger.LegacyPrintf("bot", "[Bot] quota image failed: %v", err)
		return quotaText(rows)
	}
	if !sendImage(ctx, event, peer, png) {
		return quotaText(rows)
	}
	return ""
}

func quotaRow(account *service.Account, usage *service.UsageInfo, usageError string, stats *service.WindowStats) render.QuotaRow {
	state := accountState(account)
	row := render.QuotaRow{
		Name:    fmt.Sprintf("#%d %s", account.ID, account.Name),
		Plan:    account.Platform + " " + account.Type,
		Enabled: state != stateDisabled,
		State:   state,
	}
	for _, group := range account.Groups {
		if group != nil {
			row.Tags = append(row.Tags, group.Name)
		}
	}
	if stats != nil {
		requests := stats.Requests
		row.Requests = &requests
	}
	if usage != nil {
		row.FiveHour = quotaWindow(usage.FiveHour, fiveHours)
		row.Weekly = quotaWindow(usage.SevenDay, sevenDays)
	}
	switch {
	case usageError != "":
		row.Issue = "额度查询失败，余量未知"
	case row.FiveHour == nil && row.Weekly == nil:
		row.Issue = "暂无额度数据"
	}
	return row
}

func quotaWindow(progress *service.UsageProgress, length time.Duration) *render.QuotaWindow {
	if progress == nil {
		return nil
	}
	used := progress.Utilization
	window := &render.QuotaWindow{UsedPercent: &used, Length: length}
	if progress.ResetsAt != nil {
		window.ResetsAt = *progress.ResetsAt
	}
	return window
}

func quotaText(rows []render.QuotaRow) string {
	text := "账号额度："
	for _, row := range rows {
		text += "\n- " + row.Name + " " + row.Plan
		for _, window := range []struct {
			label string
			value *render.QuotaWindow
		}{{"5h", row.FiveHour}, {"周", row.Weekly}} {
			if window.value != nil {
				text += fmt.Sprintf(" %s %.0f%%", window.label, *window.value.UsedPercent)
			}
		}
	}
	return text
}

func parsePeriod(value string) (time.Duration, bool) {
	match := flowPeriod.FindStringSubmatch(value)
	if match == nil {
		return 0, false
	}
	count, _ := strconv.Atoi(match[1])
	unit := map[string]time.Duration{"m": time.Minute, "h": time.Hour, "d": 24 * time.Hour, "w": sevenDays}[match[2]]
	return time.Duration(count) * unit, true
}

func (a *App) flowImage(ctx context.Context, event onebot.Event, peer Peer, args []string) string {
	period := "1d"
	if len(args) == 1 {
		period = args[0]
	}
	length, ok := parsePeriod(period)
	if !ok || len(args) > 1 {
		return fmt.Sprintf("用法：%s flow [5m|1h|1d|1w]", a.command)
	}
	rows, err := a.flow.ListUsageFlow(ctx, time.Now().Add(-length))
	if err != nil {
		return "流量查询失败：" + err.Error()
	}
	if len(rows) == 0 {
		return "最近 " + period + " 没有用量。"
	}
	png, err := render.Flow(flowResult(period, rows))
	if err != nil {
		logger.LegacyPrintf("bot", "[Bot] flow image failed: %v", err)
		return "流图绘制失败：" + err.Error()
	}
	if !sendImage(ctx, event, peer, png) {
		return "图片发送失败。"
	}
	return ""
}

func flowResult(period string, rows []service.UsageFlowRow) render.FlowResult {
	items := make([]render.FlowItem, 0, len(rows))
	for _, row := range rows {
		cost := new(big.Rat).SetFloat64(row.CostUSD).FloatString(10)
		total := strconv.FormatInt(row.TotalTokens, 10)
		input := strconv.FormatInt(row.InputTokens, 10)
		cached := strconv.FormatInt(row.CacheTokens, 10)
		items = append(items, render.FlowItem{
			ActorID:          "user:" + strconv.FormatInt(row.UserID, 10),
			ActorName:        row.UserName,
			Model:            row.Model,
			SubscriptionID:   strconv.FormatInt(row.AccountID, 10),
			SubscriptionName: row.AccountName,
			Requests:         row.Requests,
			PricedRequests:   row.Requests,
			CostUSD:          &cost,
			TotalTokens:      &total,
			InputTokens:      &input,
			CachedTokens:     &cached,
		})
	}
	return render.FlowResult{Period: period, Items: items}
}
