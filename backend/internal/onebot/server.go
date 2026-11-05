// Package onebot implements the OneBot 11 reverse WebSocket transport. It only
// owns the wire protocol; commands are supplied by the event handler.
package onebot

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gorilla/websocket"
)

const (
	maxPayload   = 5 << 20
	maxPending   = 64
	actionExpiry = 15 * time.Second
)

var identityRE = regexp.MustCompile(`^[1-9][0-9]*$`)

// Identity normalizes a OneBot numeric ID, which peers send as number or string.
func Identity(value any) string {
	switch value := value.(type) {
	case string:
		if identityRE.MatchString(value) {
			return value
		}
	case float64:
		if value > 0 && value <= 9007199254740991 && value == float64(uint64(value)) {
			return strconv.FormatUint(uint64(value), 10)
		}
	case json.Number:
		if identityRE.MatchString(string(value)) {
			return string(value)
		}
	}
	return ""
}

// Event is a decoded OneBot event object.
type Event map[string]any

// EventHandler receives message events from the verified peer.
type EventHandler func(context.Context, Event, *Peer)

// Config controls the reverse WebSocket endpoint.
type Config struct {
	SelfID string
	Token  string
}

// Peer is the connected OneBot implementation, such as NapCat.
type Peer struct {
	server *Server
	conn   *websocket.Conn
	write  sync.Mutex
}

// Connected reports whether this peer is still the active connection.
func (p *Peer) Connected() bool {
	return p != nil && p.server != nil && p.server.peer.Load() == p
}

// Call sends an action and waits for its echo. A failed call is not retried,
// because the peer may already have applied it.
func (p *Peer) Call(ctx context.Context, action string, params map[string]any) (any, error) {
	if !p.Connected() {
		return nil, errors.New("onebot is not connected")
	}
	return p.server.call(ctx, p, action, params)
}

// SendMessage sends text to the event's private or group destination.
func (p *Peer) SendMessage(ctx context.Context, event Event, text string) (any, error) {
	return p.SendSegments(ctx, event, []map[string]any{{"type": "text", "data": map[string]any{"text": text}}})
}

// SendSegments sends a message assembled from OneBot segments.
func (p *Peer) SendSegments(ctx context.Context, event Event, message []map[string]any) (any, error) {
	params := map[string]any{"message": message}
	switch event["message_type"] {
	case "private":
		params["user_id"] = event["user_id"]
		return p.Call(ctx, "send_private_msg", params)
	case "group":
		params["group_id"] = event["group_id"]
		return p.Call(ctx, "send_group_msg", params)
	}
	return nil, errors.New("unsupported onebot message destination")
}

type actionResult struct {
	data any
	err  error
}

type pendingAction struct {
	peer *Peer
	ch   chan actionResult
}

// Server is an authenticated, single-peer reverse WebSocket server. A second
// connection is rejected until the first one closes.
type Server struct {
	cfg      Config
	handler  EventHandler
	upgrader websocket.Upgrader
	peer     atomic.Pointer[Peer]
	mu       sync.Mutex
	pending  map[string]pendingAction
	seq      atomic.Uint64
}

// New validates the endpoint configuration.
func New(cfg Config, handler EventHandler) (*Server, error) {
	if !identityRE.MatchString(cfg.SelfID) || cfg.Token == "" {
		return nil, errors.New("onebot requires a numeric self_id and a token")
	}
	return &Server{
		cfg:      cfg,
		handler:  handler,
		upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
		pending:  make(map[string]pendingAction),
	}, nil
}

// ServeHTTP authenticates the peer and runs its connection.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	want := "Bearer " + s.cfg.Token
	auth := r.Header.Values("Authorization")
	if len(auth) != 1 || subtle.ConstantTimeCompare([]byte(auth[0]), []byte(want)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if ids := r.Header.Values("X-Self-ID"); len(ids) != 1 || ids[0] != s.cfg.SelfID {
		http.Error(w, "invalid self id", http.StatusForbidden)
		return
	}
	if s.peer.Load() != nil {
		http.Error(w, "peer already connected", http.StatusConflict)
		return
	}
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(maxPayload)
	s.serve(conn)
}

func (s *Server) serve(conn *websocket.Conn) {
	p := &Peer{server: s, conn: conn}
	defer func() { _ = conn.Close() }()
	if !s.peer.CompareAndSwap(nil, p) {
		return
	}
	defer func() {
		s.peer.CompareAndSwap(p, nil)
		s.failPending(p, errors.New("onebot disconnected"))
	}()

	verified := make(chan error, 1)
	go func() { verified <- p.verify() }()
	ready := false
	for {
		var object map[string]any
		if err := conn.ReadJSON(&object); err != nil {
			return
		}
		if echo, ok := object["echo"].(string); ok {
			s.resolve(echo, object)
			if !ready {
				if err := <-verified; err != nil {
					logger.LegacyPrintf("onebot", "[OneBot] peer verification failed: %v", err)
					return
				}
				ready = true
				logger.LegacyPrintf("onebot", "[OneBot] peer %s connected", s.cfg.SelfID)
			}
			continue
		}
		if ready && object["post_type"] == "message" && s.handler != nil {
			go s.handler(context.Background(), Event(object), p)
		}
	}
}

func (p *Peer) verify() error {
	ctx, cancel := context.WithTimeout(context.Background(), actionExpiry)
	defer cancel()
	data, err := p.Call(ctx, "get_login_info", map[string]any{})
	if err != nil {
		return err
	}
	info, ok := data.(map[string]any)
	if !ok || Identity(info["user_id"]) != p.server.cfg.SelfID {
		return errors.New("self id mismatch")
	}
	return nil
}

func (s *Server) call(ctx context.Context, p *Peer, action string, params map[string]any) (any, error) {
	echo := strconv.FormatUint(s.seq.Add(1), 10)
	ch := make(chan actionResult, 1)
	s.mu.Lock()
	if len(s.pending) >= maxPending {
		s.mu.Unlock()
		return nil, errors.New("too many pending onebot actions")
	}
	s.pending[echo] = pendingAction{peer: p, ch: ch}
	s.mu.Unlock()
	defer s.forget(echo)

	p.write.Lock()
	_ = p.conn.SetWriteDeadline(time.Now().Add(actionExpiry))
	err := p.conn.WriteJSON(map[string]any{"action": action, "params": params, "echo": echo})
	p.write.Unlock()
	if err != nil {
		return nil, fmt.Errorf("send onebot action: %w", err)
	}

	timeout := time.NewTimer(actionExpiry)
	defer timeout.Stop()
	select {
	case result := <-ch:
		return result.data, result.err
	case <-timeout.C:
		return nil, errors.New("onebot action timed out")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Server) forget(echo string) {
	s.mu.Lock()
	delete(s.pending, echo)
	s.mu.Unlock()
}

func (s *Server) resolve(echo string, response map[string]any) {
	s.mu.Lock()
	action, ok := s.pending[echo]
	delete(s.pending, echo)
	s.mu.Unlock()
	if !ok {
		return
	}
	if response["status"] != "ok" {
		action.ch <- actionResult{err: fmt.Errorf("onebot action failed: %v", response["message"])}
		return
	}
	action.ch <- actionResult{data: response["data"]}
}

func (s *Server) failPending(p *Peer, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for echo, action := range s.pending {
		if action.peer == p {
			delete(s.pending, echo)
			action.ch <- actionResult{err: err}
		}
	}
}
