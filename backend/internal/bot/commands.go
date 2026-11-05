package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/onebot"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const helpTemplate = `%[1]s

- quota [all]：查看账号额度图，all 包含已停用账号
- flow [5m|1h|1d|1w]：查看费用流图，默认 1d
- status：查看账号状态统计
- on|off <账号>[,<账号>...]：启用或停用调度（账号可用 all）
- priority <账号>[,<账号>...] <数值>：设置优先级，数值越小越优先（账号可用 all）
- weight <账号>[,<账号>...] <数值>：设置负载权重（账号可用 all）
- limit <账号>[,<账号>...] <并发数>：设置并发上限（账号可用 all）
- mv <账号> <新名称>：修改名称
- rm <账号>[,<账号>...]：删除账号
- oauth <名称> [分组]：通过 OAuth 新建 Codex 账号，cancel 取消
- reset <账号> [5h|week]：查看 ZCode 重置卡，或使用一张
- peak <账号>[,<账号>...] on|off：设置 ZCode 账号高峰时段暂停（账号可用 all）

账号可写名称或 ID。`

func (a *App) execute(ctx context.Context, event onebot.Event, peer Peer, text string) {
	if !a.isCommand(text) {
		a.continueLogin(ctx, event, peer, text)
		return
	}
	line, _, _ := strings.Cut(text, "\n")
	args := strings.Fields(strings.TrimPrefix(line, a.command))
	if len(args) == 0 || args[0] == "help" {
		a.reply(ctx, event, peer, fmt.Sprintf(helpTemplate, a.command))
		return
	}
	command, args := args[0], args[1:]
	var result string
	switch command {
	case "quota", "sub":
		result = a.quota(ctx, event, peer, args)
	case "flow":
		result = a.flowImage(ctx, event, peer, args)
	case "status":
		result = a.status(ctx)
	case "on", "off":
		result = a.toggle(ctx, command, args)
	case "priority":
		result = a.setNumber(ctx, command, args, 0, func(input *service.UpdateAccountInput, value int) { input.Priority = &value })
	case "weight":
		result = a.setNumber(ctx, command, args, 1, func(input *service.UpdateAccountInput, value int) { input.LoadFactor = &value })
	case "limit":
		result = a.setNumber(ctx, command, args, 1, func(input *service.UpdateAccountInput, value int) { input.Concurrency = &value })
	case "mv":
		result = a.rename(ctx, args)
	case "rm":
		result = a.remove(ctx, args)
	case "oauth":
		result = a.startLogin(ctx, event, args)
	case "reset":
		result = a.reset(ctx, args)
	case "peak":
		result = a.peak(ctx, args)
	default:
		result = "未知命令，请发送 " + a.command + " help 查看帮助。"
	}
	if result != "" {
		a.reply(ctx, event, peer, result)
	}
}

func (a *App) status(ctx context.Context) string {
	accounts, err := a.accounts(ctx)
	if err != nil {
		return "账号查询失败：" + err.Error()
	}
	counts := map[string]int{}
	for i := range accounts {
		counts[accountState(&accounts[i])]++
	}
	return fmt.Sprintf("账号共 %d 个：可调度 %d，已停用 %d，高峰暂停 %d，额度耗尽 %d，异常 %d。",
		len(accounts), counts[stateReady], counts[stateDisabled], counts[statePeakPaused], counts[stateExhausted], counts[stateInvalid])
}

func (a *App) toggle(ctx context.Context, command string, args []string) string {
	if len(args) != 1 {
		return fmt.Sprintf("用法：%s %s <账号>[,<账号>...]", a.command, command)
	}
	targets, err := a.targets(ctx, args[0], true)
	if err != nil {
		return err.Error()
	}
	return a.applyEach(targets, func(account *service.Account) error {
		_, err := a.admin.SetAccountSchedulable(ctx, account.ID, command == "on")
		return err
	})
}

func (a *App) setNumber(ctx context.Context, command string, args []string, minimum int, set func(*service.UpdateAccountInput, int)) string {
	if len(args) != 2 {
		return fmt.Sprintf("用法：%s %s <账号>[,<账号>...] <数值>", a.command, command)
	}
	value, err := strconv.Atoi(args[1])
	if err != nil || value < minimum {
		return fmt.Sprintf("数值必须是不小于 %d 的整数。", minimum)
	}
	targets, err := a.targets(ctx, args[0], true)
	if err != nil {
		return err.Error()
	}
	return a.applyEach(targets, func(account *service.Account) error {
		input := &service.UpdateAccountInput{}
		set(input, value)
		_, err := a.admin.UpdateAccount(ctx, account.ID, input)
		return err
	})
}

func (a *App) rename(ctx context.Context, args []string) string {
	if len(args) != 2 {
		return fmt.Sprintf("用法：%s mv <账号> <新名称>", a.command)
	}
	targets, err := a.targets(ctx, args[0], false)
	if err != nil {
		return err.Error()
	}
	if len(targets) != 1 {
		return "一次只能修改一个账号。"
	}
	if _, err := a.admin.UpdateAccount(ctx, targets[0].ID, &service.UpdateAccountInput{Name: args[1]}); err != nil {
		return "修改失败：" + err.Error()
	}
	return "已修改名称：" + args[1]
}

func (a *App) remove(ctx context.Context, args []string) string {
	if len(args) != 1 {
		return fmt.Sprintf("用法：%s rm <账号>[,<账号>...]", a.command)
	}
	targets, err := a.targets(ctx, args[0], false)
	if err != nil {
		return err.Error()
	}
	return a.applyEach(targets, func(account *service.Account) error {
		return a.admin.DeleteAccount(ctx, account.ID)
	})
}

// applyEach keeps going after a failure and reports every failed account.
func (a *App) applyEach(targets []*service.Account, apply func(*service.Account) error) string {
	var failures []string
	for _, account := range targets {
		if err := apply(account); err != nil {
			failures = append(failures, fmt.Sprintf("%s：%v", account.Name, err))
		}
	}
	done := len(targets) - len(failures)
	if len(failures) == 0 {
		return fmt.Sprintf("已处理 %d 个账号。", done)
	}
	return fmt.Sprintf("已处理 %d 个账号，失败 %d 个：\n%s", done, len(failures), strings.Join(failures, "\n"))
}
