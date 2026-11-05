// Package bot runs chat commands over OneBot. Commands act on upstream accounts
// through the same services as the admin API.
package bot

import (
	"context"
	"encoding/base64"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/onebot"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	maxConcurrentCommands = 8
	commandTimeout        = 120 * time.Second
)

// Peer is the transport boundary the commands need.
type Peer interface {
	SendMessage(context.Context, onebot.Event, string) (any, error)
	SendSegments(context.Context, onebot.Event, []map[string]any) (any, error)
}

// App executes chat commands.
type App struct {
	cfg     config.OneBotConfig
	command string
	admin   service.AdminService
	usage   *service.AccountUsageService
	flow    service.UsageFlowReader
	oauth   *service.OpenAIOAuthService
	sem     chan struct{}
	mu      sync.Mutex
	pending map[string]pendingLogin
}

// New creates the command layer.
func New(cfg config.OneBotConfig, admin service.AdminService, usage *service.AccountUsageService, flow service.UsageFlowReader, oauth *service.OpenAIOAuthService) *App {
	return &App{
		cfg:     cfg,
		command: "/" + strings.TrimPrefix(cfg.CommandPrefix, "/"),
		admin:   admin,
		usage:   usage,
		flow:    flow,
		oauth:   oauth,
		sem:     make(chan struct{}, maxConcurrentCommands),
		pending: make(map[string]pendingLogin),
	}
}

// ProvideOneBotServer returns the bot's server, or nil when the bot is disabled.
func ProvideOneBotServer(cfg *config.Config, admin service.AdminService, usage *service.AccountUsageService, flow service.UsageFlowReader, oauth *service.OpenAIOAuthService) (*onebot.Server, error) {
	if !cfg.OneBot.Enabled {
		return nil, nil
	}
	app := New(cfg.OneBot, admin, usage, flow, oauth)
	return onebot.New(onebot.Config{SelfID: cfg.OneBot.SelfID, Token: cfg.OneBot.Token}, func(ctx context.Context, event onebot.Event, peer *onebot.Peer) {
		app.Handle(ctx, event, peer)
	})
}

// Handle runs one message event with bounded concurrency.
func (a *App) Handle(ctx context.Context, event onebot.Event, peer Peer) {
	if !a.authorized(event) {
		return
	}
	text := messageText(event)
	if !a.isCommand(text) && !a.hasPending(event) {
		return
	}
	select {
	case a.sem <- struct{}{}:
	default:
		a.reply(ctx, event, peer, "当前操作较多，请稍后重试。")
		return
	}
	defer func() { <-a.sem }()
	work, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	a.execute(work, event, peer, text)
}

// authorized admits listed groups, and listed users in private chat.
func (a *App) authorized(event onebot.Event) bool {
	switch event["message_type"] {
	case "group":
		return slices.Contains(a.cfg.AllowedGroups, onebot.Identity(event["group_id"]))
	case "private":
		return slices.Contains(a.cfg.AllowedUsers, onebot.Identity(event["user_id"]))
	}
	return false
}

func (a *App) isCommand(text string) bool {
	rest, ok := strings.CutPrefix(text, a.command)
	return ok && (rest == "" || strings.ContainsAny(rest[:1], " \t\r\n"))
}

func (a *App) reply(ctx context.Context, event onebot.Event, peer Peer, text string) {
	if _, err := peer.SendMessage(ctx, event, text); err != nil {
		logger.LegacyPrintf("bot", "[Bot] reply failed: %v", err)
	}
}

func sendImage(ctx context.Context, event onebot.Event, peer Peer, png []byte) bool {
	file := "base64://" + base64.StdEncoding.EncodeToString(png)
	_, err := peer.SendSegments(ctx, event, []map[string]any{{"type": "image", "data": map[string]any{"file": file}}})
	return err == nil
}

// messageText joins the text segments of a message event.
func messageText(event onebot.Event) string {
	if text, ok := event["message"].(string); ok {
		return strings.TrimSpace(text)
	}
	segments, _ := event["message"].([]any)
	var b strings.Builder
	for _, item := range segments {
		segment, _ := item.(map[string]any)
		if segment["type"] != "text" {
			continue
		}
		data, _ := segment["data"].(map[string]any)
		text, _ := data["text"].(string)
		b.WriteString(text)
	}
	if b.Len() == 0 {
		raw, _ := event["raw_message"].(string)
		return strings.TrimSpace(raw)
	}
	return strings.TrimSpace(b.String())
}
