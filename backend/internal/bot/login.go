package bot

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/onebot"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const loginExpiry = 10 * time.Minute

// pendingLogin waits for the sender to paste the OAuth callback URL.
type pendingLogin struct {
	sessionID string
	name      string
	groupIDs  []int64
	expiresAt time.Time
}

// conversationKey scopes a login to one sender in one conversation.
func conversationKey(event onebot.Event) string {
	return onebot.Identity(event["group_id"]) + ":" + onebot.Identity(event["user_id"])
}

func (a *App) hasPending(event onebot.Event) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	login, ok := a.pending[conversationKey(event)]
	return ok && time.Now().Before(login.expiresAt)
}

func (a *App) startLogin(ctx context.Context, event onebot.Event, args []string) string {
	if len(args) == 0 || len(args) > 2 {
		return fmt.Sprintf("用法：%s oauth <名称> [分组]", a.command)
	}
	var groupIDs []int64
	if len(args) == 2 {
		group, err := a.findGroup(ctx, args[1])
		if err != nil {
			return err.Error()
		}
		groupIDs = []int64{group.ID}
	}
	result, err := a.oauth.GenerateAuthURL(ctx, nil, openai.DefaultRedirectURI, openai.OAuthPlatformOpenAI)
	if err != nil {
		return "登录启动失败：" + err.Error()
	}
	a.mu.Lock()
	a.pending[conversationKey(event)] = pendingLogin{sessionID: result.SessionID, name: args[0], groupIDs: groupIDs, expiresAt: time.Now().Add(loginExpiry)}
	a.mu.Unlock()
	return "请打开以下链接完成登录，然后发送回调 URL，发送 cancel 取消：\n" + result.AuthURL
}

func (a *App) findGroup(ctx context.Context, name string) (*service.Group, error) {
	groups, err := a.admin.GetAllGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("分组查询失败：%w", err)
	}
	id, idErr := strconv.ParseInt(name, 10, 64)
	for i := range groups {
		if groups[i].Name == name || idErr == nil && groups[i].ID == id {
			return &groups[i], nil
		}
	}
	return nil, fmt.Errorf("未找到分组：%s", name)
}

// continueLogin handles the sender's next message after an OAuth prompt. Text
// that is neither a callback URL nor cancel is ignored, so a group chat can continue.
func (a *App) continueLogin(ctx context.Context, event onebot.Event, peer Peer, text string) {
	if strings.EqualFold(text, "cancel") {
		if _, ok := a.takeLogin(event); ok {
			a.reply(ctx, event, peer, "登录已取消。")
		}
		return
	}
	callback, err := url.Parse(text)
	if err != nil || callback.Query().Get("code") == "" || callback.Query().Get("state") == "" {
		return
	}
	login, ok := a.takeLogin(event)
	if !ok {
		return
	}
	a.reply(ctx, event, peer, a.finishLogin(ctx, login, callback.Query()))
}

func (a *App) takeLogin(event onebot.Event) (pendingLogin, bool) {
	key := conversationKey(event)
	a.mu.Lock()
	defer a.mu.Unlock()
	login, ok := a.pending[key]
	delete(a.pending, key)
	return login, ok && time.Now().Before(login.expiresAt)
}

func (a *App) finishLogin(ctx context.Context, login pendingLogin, query url.Values) string {
	token, err := a.oauth.ExchangeCode(ctx, &service.OpenAIExchangeCodeInput{
		SessionID:   login.sessionID,
		Code:        query.Get("code"),
		State:       query.Get("state"),
		RedirectURI: openai.DefaultRedirectURI,
	})
	if err != nil {
		return "登录失败：" + err.Error()
	}
	account, err := a.admin.CreateAccount(ctx, &service.CreateAccountInput{
		Name:        login.name,
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeOAuth,
		Credentials: a.oauth.BuildAccountCredentials(token),
		Concurrency: 3,
		Priority:    50,
		GroupIDs:    login.groupIDs,
	})
	if err != nil {
		return "账号创建失败：" + err.Error()
	}
	return fmt.Sprintf("账号已创建：#%d %s", account.ID, account.Name)
}
