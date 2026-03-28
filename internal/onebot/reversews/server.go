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
// We keep a single active session (last connected wins).
type Server struct {
	logger *slog.Logger
	addr   string

	handler EventHandler

	httpSrv *http.Server

	mu      sync.RWMutex
	session *session
	stats   *stats.Stats
}

type session struct {
	conn *websocket.Conn
	remote string

	mu      sync.Mutex
	pending map[string]chan ob11.APIResponse
}

func New(addr string, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{addr: addr, logger: logger}
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
	sess := &session{conn: c, remote: r.RemoteAddr, pending: make(map[string]chan ob11.APIResponse)}

	// Swap session (last connection wins).
	s.mu.Lock()
	old := s.session
	s.session = sess
	s.mu.Unlock()
	if old != nil {
		_ = old.conn.Close(websocket.StatusNormalClosure, "replaced")
	}

	ctx := r.Context()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			break
		}

		// Debug log every received frame (events + API responses).
		// Note: caller must set logger level to Debug to see this output.
		s.logger.Debug("onebot ws recv", "remote", r.RemoteAddr, "bytes", len(data), "payload", truncateForLog(data, 8*1024))

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
			go s.handler(ctx, raw)
		}
	}

	_ = c.Close(websocket.StatusNormalClosure, "bye")
	s.logger.Info("onebot disconnected", "remote", r.RemoteAddr)

	// Clear session if it is still current.
	s.mu.Lock()
	if s.session == sess {
		s.session = nil
	}
	s.mu.Unlock()
}

func (sess *session) deliver(resp ob11.APIResponse) {
	sess.mu.Lock()
	ch, ok := sess.pending[resp.Echo]
	if ok {
		delete(sess.pending, resp.Echo)
	}
	sess.mu.Unlock()
	if ok {
		select {
		case ch <- resp:
		default:
		}
		close(ch)
	}
}

func (s *Server) Call(ctx context.Context, action string, params any) (ob11.APIResponse, error) {
	s.mu.RLock()
	sess := s.session
	s.mu.RUnlock()
	if sess == nil {
		return ob11.APIResponse{}, errors.New("onebot not connected")
	}

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
	sess.pending[echo] = ch
	sess.mu.Unlock()

	// Send.
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	s.logger.Debug("onebot ws send", "remote", sess.remote, "bytes", len(payload), "payload", truncateForLog(payload, 8*1024))
	if err := sess.conn.Write(writeCtx, websocket.MessageText, payload); err != nil {
		// cleanup pending
		sess.mu.Lock()
		delete(sess.pending, echo)
		sess.mu.Unlock()
		return ob11.APIResponse{}, fmt.Errorf("ws write: %w", err)
	}

	select {
	case <-ctx.Done():
		return ob11.APIResponse{}, ctx.Err()
	case resp := <-ch:
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
