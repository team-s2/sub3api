package bot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var resetTypes = map[string]string{"5h": service.ZCodeResetTypeFiveHour, "week": service.ZCodeResetTypeWeek}

func (a *App) reset(ctx context.Context, args []string) string {
	if len(args) == 0 || len(args) > 2 {
		return fmt.Sprintf("用法：%s reset <账号> [5h|week]", a.command)
	}
	targets, err := a.targets(ctx, args[0], false)
	if err != nil {
		return err.Error()
	}
	if len(targets) != 1 {
		return "一次只能操作一个账号。"
	}
	account := targets[0]
	if len(args) == 1 {
		inventory, err := a.resets.Inventory(ctx, account.ID, true)
		if err != nil {
			return "重置卡查询失败：" + err.Error()
		}
		return formatResetCards(account.Name, inventory)
	}
	resetType, ok := resetTypes[args[1]]
	if !ok {
		return fmt.Sprintf("用法：%s reset <账号> [5h|week]", a.command)
	}
	inventory, err := a.resets.UseCard(ctx, account.ID, resetType)
	if err != nil {
		return "重置失败：" + err.Error()
	}
	return "已使用重置卡。\n" + formatResetCards(account.Name, inventory)
}

func formatResetCards(name string, inventory *service.ZCodeResetInventory) string {
	if len(inventory.Cards) == 0 {
		return name + " 暂无可用重置卡。"
	}
	lines := []string{name + " 的重置卡："}
	for _, card := range inventory.Cards {
		label := "周"
		if card.Type == service.ZCodeResetTypeFiveHour {
			label = "5 小时"
		}
		lines = append(lines, fmt.Sprintf("- %s卡，%s 到期", label, card.ExpiresAt.In(imageZone).Format(time.DateTime)))
	}
	return strings.Join(lines, "\n")
}

func (a *App) peak(ctx context.Context, args []string) string {
	if len(args) != 2 || args[1] != "on" && args[1] != "off" {
		return fmt.Sprintf("用法：%s peak <账号>[,<账号>...] on|off", a.command)
	}
	targets, err := a.targets(ctx, args[0], true)
	if err != nil {
		return err.Error()
	}
	enabled := args[1] == "on"
	zcode := make([]*service.Account, 0, len(targets))
	for _, account := range targets {
		if account.IsZCodeAccount() {
			zcode = append(zcode, account)
		}
	}
	if len(zcode) == 0 {
		return "没有 ZCode 账号。"
	}
	return a.applyEach(zcode, func(account *service.Account) error {
		return a.resets.UpdateSettings(ctx, account.ID, &enabled, nil)
	})
}
