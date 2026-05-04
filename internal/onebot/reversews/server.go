package reversews

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/xiaocaoooo/nyanyabot/internal/onebot/ob11"
	"github.com/xiaocaoooo/nyanyabot/internal/stats"
	"nhooyr.io/websocket"
)

// EventHandler is called for each raw event frame received from NapCat/OneBot.
// The payload is the raw JSON bytes.
type EventHandler func(ctx context.Context, raw ob11.Event)

// Server implements OneBot 11 reverse websocket.
// NapCat (the bot side) connects to us, and we:
//   - receive events from the bot
//   - send API requests to the bot
//   - receive API responses from the bot
//
// We support multiple bot sessions, keyed by self_id.
type Server struct {
	logger *slog.Logger
	addr   string

	handler EventHandler

	httpSrv *http.Server

	mu       sync.RWMutex
	sessions map[int64]*session
	stats    *stats.Stats
}

// BotInfo contains information about a connected bot.
type BotInfo struct {
	SelfID      int64     `json:"self_id"`
	Nickname    string    `json:"nickname"`
	RemoteAddr  string    `json:"remote_addr"`
	ConnectedAt time.Time `json:"connected_at"`
	GroupCount  int       `json:"group_count"`
	Groups      []Group   `json:"groups"`
}

// Group contains information about a group.
type Group struct {
	GroupID        int64  `json:"group_id"`
	GroupName      string `json:"group_name"`
	MemberCount    int    `json:"member_count"`
	MaxMemberCount int    `json:"max_member_count"`
}

type session struct {
	conn        *websocket.Conn
	remote      string
	selfID      int64
	nickname    string
	connectedAt time.Time
	groups      []Group

	mu      sync.Mutex
	pending map[string]chan ob11.APIResponse
	closed  bool
}

func New(addr string, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		addr:     addr,
		logger:   logger,
		sessions: make(map[int64]*session),
	}
}

func (s *Server) SetEventHandler(h EventHandler) {
	s.handler = h
}

func (s *Server) SetStats(st *stats.Stats) {
	s.stats = st
}

func (s *Server) Addr() string { return s.addr }

func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleWS)

	s.httpSrv = &http.Server{
		Addr:              s.addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("onebot reverse-ws listening", "addr", s.addr)
		if err := s.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		_ = s.httpSrv.Shutdown(context.Background())
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpSrv == nil {
		return nil
	}
	return s.httpSrv.Shutdown(ctx)
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // local reverse ws, no origin check
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		s.logger.Warn("ws accept failed", "err", err)
		return
	}

	s.logger.Info("onebot connected", "remote", r.RemoteAddr)
	sess := &session{
		conn:        c,
		remote:      r.RemoteAddr,
		pending:     make(map[string]chan ob11.APIResponse),
		connectedAt: time.Now(),
	}

	ctx := r.Context()

	// Start read loop in goroutine BEFORE calling any APIs.
	// This ensures API responses can be received.
	readLoopDone := make(chan struct{})
	readLoopCtx, cancelReadLoop := context.WithCancel(ctx)
	defer cancelReadLoop()

	go func() {
		defer close(readLoopDone)
		for {
			_, data, err := c.Read(readLoopCtx)
			if err != nil {
				return
			}

			// Debug log every received frame (events + API responses).
			// Note: caller must set logger level to Debug to see this output.
			s.logger.Debug("onebot ws recv", "self_id", sess.selfID, "remote", r.RemoteAddr, "bytes", len(data), "payload", truncateForLog(data, 8*1024))

			// Determine whether this frame is an API response (has echo+status) or an event.
			var probe struct {
				Echo   string `json:"echo"`
				Status string `json:"status"`
			}
			_ = json.Unmarshal(data, &probe)
			if probe.Echo != "" && probe.Status != "" {
				var resp ob11.APIResponse
				if err := json.Unmarshal(data, &resp); err == nil {
					sess.deliver(resp)
					continue
				}
			}

			if s.handler != nil {
				// Copy because data buffer is reused by library in some implementations.
				raw := json.RawMessage(append([]byte(nil), data...))
				// Ensure self_id is in the event
				raw = s.ensureSelfID(raw, sess.selfID)
				go s.handler(readLoopCtx, raw)
			}
		}
	}()

	// Now call initialization APIs. The read loop is running, so responses will be received.
	// Get bot login info first.
	// Note: This call uses the session directly (not via sessions map),
	// so it works during initialization before the session is registered.
	loginInfo, err := s.callWithSession(ctx, sess, "get_login_info", nil)
	if err != nil {
		s.logger.Error("failed to get login info", "remote", r.RemoteAddr, "err", err)
		_ = c.Close(websocket.StatusPolicyViolation, "failed to get login info")
		cancelReadLoop()
		<-readLoopDone
		return
	}

	var loginData struct {
		UserID   int64  `json:"user_id"`
		Nickname string `json:"nickname"`
	}
	if err := json.Unmarshal(loginInfo.Data, &loginData); err != nil || loginData.UserID == 0 {
		s.logger.Error("invalid login info", "remote", r.RemoteAddr, "err", err, "data", string(loginInfo.Data))
		_ = c.Close(websocket.StatusPolicyViolation, "invalid login info")
		cancelReadLoop()
		<-readLoopDone
		return
	}

	sess.selfID = loginData.UserID
	sess.nickname = loginData.Nickname

	s.logger.Info("bot identified", "self_id", sess.selfID, "nickname", sess.nickname, "remote", r.RemoteAddr)

	// Get group list.
	// Note: This call also uses the session directly (not via sessions map),
	// so it works during initialization before the session is registered.
	groupListResp, err := s.callWithSession(ctx, sess, "get_group_list", nil)
	if err != nil {
		s.logger.Warn("failed to get group list", "self_id", sess.selfID, "err", err)
		// Don't disconnect, just leave groups empty
	} else {
		var groupsData []struct {
			GroupID        int64  `json:"group_id"`
			GroupName      string `json:"group_name"`
			MemberCount    int    `json:"member_count"`
			MaxMemberCount int    `json:"max_member_count"`
		}
		if err := json.Unmarshal(groupListResp.Data, &groupsData); err != nil {
			s.logger.Warn("failed to parse group list", "self_id", sess.selfID, "err", err)
		} else {
			sess.groups = make([]Group, len(groupsData))
			for i, g := range groupsData {
				sess.groups[i] = Group{
					GroupID:        g.GroupID,
					GroupName:      g.GroupName,
					MemberCount:    g.MemberCount,
					MaxMemberCount: g.MaxMemberCount,
				}
			}
			s.logger.Info("bot groups loaded", "self_id", sess.selfID, "group_count", len(sess.groups))
		}
	}

	// Register session (replace old session with same self_id)
	s.mu.Lock()
	old := s.sessions[sess.selfID]
	s.sessions[sess.selfID] = sess
	s.mu.Unlock()

	if old != nil {
		s.logger.Info("replacing old session", "self_id", sess.selfID, "old_remote", old.remote)
		old.close("replaced by new connection")
	}

	// Wait for read loop to finish (connection closed or context cancelled)
	<-readLoopDone

	_ = c.Close(websocket.StatusNormalClosure, "bye")
	s.logger.Info("onebot disconnected", "self_id", sess.selfID, "remote", r.RemoteAddr)

	// Clear session if it is still current.
	s.mu.Lock()
	if s.sessions[sess.selfID] == sess {
		delete(s.sessions, sess.selfID)
	}
	s.mu.Unlock()

	// Clean up pending requests
	sess.close("connection closed")
}

// ensureSelfID ensures the event has self_id field set to the connection's self_id.
func (s *Server) ensureSelfID(raw json.RawMessage, selfID int64) json.RawMessage {
	var event map[string]interface{}
	if err := json.Unmarshal(raw, &event); err != nil {
		// If we can't parse it, return as-is
		s.logger.Warn("ensureSelfID: failed to unmarshal event", "err", err, "raw", truncateForLog(raw, 512))
		return raw
	}

	// Inject or overwrite self_id
	event["self_id"] = selfID

	// Re-marshal
	result, err := json.Marshal(event)
	if err != nil {
		// If marshal fails, return original
		s.logger.Warn("ensureSelfID: failed to marshal event", "err", err, "self_id", selfID)
		return raw
	}
	return result
}

func (sess *session) deliver(resp ob11.APIResponse) {
	sess.mu.Lock()
	ch, ok := sess.pending[resp.Echo]
	if ok {
		delete(sess.pending, resp.Echo)
	}
	sess.mu.Unlock()
	if ok {
		// Send response and close channel.
		// Use select+default to avoid blocking if receiver is gone.
		select {
		case ch <- resp:
		default:
		}
		close(ch)
	}
}

func (sess *session) close(reason string) {
	sess.mu.Lock()
	if sess.closed {
		sess.mu.Unlock()
		return
	}
	sess.closed = true
	// Take ownership of all pending channels and clear the map.
	pending := sess.pending
	sess.pending = make(map[string]chan ob11.APIResponse)
	sess.mu.Unlock()

	// Close all pending channels to unblock waiters.
	// Safe because we own these channels and no other goroutine will close them.
	for _, ch := range pending {
		close(ch)
	}

	_ = sess.conn.Close(websocket.StatusNormalClosure, reason)
}

// callWithSession sends an API call through a specific session.
func (s *Server) callWithSession(ctx context.Context, sess *session, action string, params any) (ob11.APIResponse, error) {
	echo, err := randEcho()
	if err != nil {
		return ob11.APIResponse{}, err
	}

	req := ob11.APIRequest{Action: action, Params: params, Echo: echo}
	payload, err := json.Marshal(req)
	if err != nil {
		return ob11.APIResponse{}, err
	}

	ch := make(chan ob11.APIResponse, 1)
	sess.mu.Lock()
	if sess.closed {
		sess.mu.Unlock()
		return ob11.APIResponse{}, errors.New("session closed")
	}
	sess.pending[echo] = ch
	sess.mu.Unlock()

	// Send.
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	s.logger.Debug("onebot ws send", "self_id", sess.selfID, "remote", sess.remote, "bytes", len(payload), "payload", truncateForLog(payload, 8*1024))

	// 统计发送消息数
	if s.stats != nil {
		s.stats.IncSent()
	}

	if err := sess.conn.Write(writeCtx, websocket.MessageText, payload); err != nil {
		// cleanup pending and close channel to unblock waiters
		sess.mu.Lock()
		delete(sess.pending, echo)
		sess.mu.Unlock()
		close(ch)
		return ob11.APIResponse{}, fmt.Errorf("ws write: %w", err)
	}

	select {
	case <-ctx.Done():
		return ob11.APIResponse{}, ctx.Err()
	case resp, ok := <-ch:
		if !ok {
			return ob11.APIResponse{}, errors.New("session closed while waiting for response")
		}
		// 统计发送成功的消息数
		if s.stats != nil && resp.Status == "ok" {
			switch action {
			case "send_group_msg", "send_private_msg", "send_msg":
				s.stats.IncSent()
			}
		}
		return resp, nil
	case <-time.After(30 * time.Second):
		return ob11.APIResponse{}, errors.New("onebot call timeout")
	}
}

// Call sends an API call to a bot.
// - If no bots are connected, returns an error.
// - If exactly one bot is connected, sends to that bot.
// - If multiple bots are connected, returns an error asking to use CallWithBot.
func (s *Server) Call(ctx context.Context, action string, params any) (ob11.APIResponse, error) {
	s.mu.RLock()
	count := len(s.sessions)
	var sess *session
	if count == 1 {
		for _, v := range s.sessions {
			sess = v
			break
		}
	}
	s.mu.RUnlock()

	if count == 0 {
		return ob11.APIResponse{}, errors.New("onebot not connected")
	}
	if count > 1 {
		return ob11.APIResponse{}, errors.New("multiple bots connected, use CallWithBot to specify self_id")
	}

	return s.callWithSession(ctx, sess, action, params)
}

// CallWithBot sends an API call to a specific bot by self_id.
func (s *Server) CallWithBot(ctx context.Context, selfID int64, action string, params any) (ob11.APIResponse, error) {
	s.mu.RLock()
	sess := s.sessions[selfID]
	s.mu.RUnlock()

	if sess == nil {
		return ob11.APIResponse{}, fmt.Errorf("bot %d not connected", selfID)
	}

	return s.callWithSession(ctx, sess, action, params)
}

// SendGroupMessage sends a group message to a specific bot.
func (s *Server) SendGroupMessage(ctx context.Context, selfID int64, groupID int64, message any) (ob11.APIResponse, error) {
	params := map[string]any{
		"group_id": groupID,
		"message":  message,
	}
	return s.CallWithBot(ctx, selfID, "send_group_msg", params)
}

// GetBots returns a snapshot of all connected bots.
func (s *Server) GetBots() []BotInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	bots := make([]BotInfo, 0, len(s.sessions))
	for _, sess := range s.sessions {
		sess.mu.Lock()
		bot := BotInfo{
			SelfID:      sess.selfID,
			Nickname:    sess.nickname,
			RemoteAddr:  sess.remote,
			ConnectedAt: sess.connectedAt,
			GroupCount:  len(sess.groups),
			Groups:      append([]Group(nil), sess.groups...), // copy
		}
		sess.mu.Unlock()
		bots = append(bots, bot)
	}
	return bots
}

// GetBotIDs 返回所有连接的 Bot 的 self_id 列表
func (s *Server) GetBotIDs() []int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := make([]int64, 0, len(s.sessions))
	for selfID := range s.sessions {
		ids = append(ids, selfID)
	}
	return ids
}

// SnapshotBots is an alias for GetBots.
func (s *Server) SnapshotBots() []BotInfo {
	return s.GetBots()
}

func randEcho() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func truncateForLog(b []byte, max int) string {
	if max <= 0 {
		return ""
	}
	if len(b) <= max {
		if utf8.Valid(b) {
			return string(b)
		}
		return fmt.Sprintf("%x", b)
	}
	cut := b[:max]
	if utf8.Valid(cut) {
		return string(cut) + "..."
	}
	return fmt.Sprintf("%x", cut) + "..."
}

// For debugging: ensure slog is linked even if not used by caller.
var _ = slog.LevelInfo
