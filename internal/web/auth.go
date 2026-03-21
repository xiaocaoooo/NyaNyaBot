package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookieName   = "nyanyabot_session"
	sessionMaxAgeSecond = 30 * 24 * 60 * 60
)

var sessionTTL = time.Duration(sessionMaxAgeSecond) * time.Second

type sessionManager struct {
	mu       sync.Mutex
	sessions map[string]time.Time
}

func newSessionManager() *sessionManager {
	return &sessionManager{sessions: make(map[string]time.Time)}
}

func (m *sessionManager) create(now time.Time) (string, time.Time, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", time.Time{}, err
	}
	sessionID := base64.RawURLEncoding.EncodeToString(b)
	expiresAt := now.Add(sessionTTL)

	m.mu.Lock()
	m.cleanupExpiredLocked(now)
	m.sessions[sessionID] = expiresAt
	m.mu.Unlock()
	return sessionID, expiresAt, nil
}

func (m *sessionManager) valid(sessionID string, now time.Time) bool {
	if strings.TrimSpace(sessionID) == "" {
		return false
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupExpiredLocked(now)

	expiresAt, ok := m.sessions[sessionID]
	return ok && now.Before(expiresAt)
}

func (m *sessionManager) delete(sessionID string) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	m.mu.Lock()
	delete(m.sessions, sessionID)
	m.mu.Unlock()
}

func (m *sessionManager) cleanupExpiredLocked(now time.Time) {
	for sessionID, expiresAt := range m.sessions {
		if !now.Before(expiresAt) {
			delete(m.sessions, sessionID)
		}
	}
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	if next == nil {
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.isPublicPath(r.URL.Path) || s.isAuthenticated(r) {
			next.ServeHTTP(w, r)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}

		s.redirectToLogin(w, r)
	})
}

func (s *Server) isPublicPath(path string) bool {
	switch path {
	case "/api/auth/login", "/api/auth/logout", "/api/auth/status", "/favicon.ico", "/robots.txt":
		return true
	}
	if strings.HasPrefix(path, "/api/auth/") {
		return true
	}
	if path == "/login" || path == "/login/" || strings.HasPrefix(path, "/login/") {
		return true
	}
	if strings.HasPrefix(path, "/_next/") || strings.HasPrefix(path, "/assets/") {
		return true
	}
	return false
}

func (s *Server) redirectToLogin(w http.ResponseWriter, r *http.Request) {
	loginURL := &url.URL{Path: "/login/"}
	next := r.URL.RequestURI()
	if next != "" {
		q := loginURL.Query()
		q.Set("next", next)
		loginURL.RawQuery = q.Encode()
	}
	http.Redirect(w, r, loginURL.String(), http.StatusFound)
}

func (s *Server) isAuthenticated(r *http.Request) bool {
	if s == nil || s.sessions == nil {
		return false
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	return s.sessions.valid(cookie.Value, time.Now())
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	var req struct {
		Password string `json:"password"`
	}
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	cfg := s.store.Get()
	if !secureEqual(req.Password, cfg.WebUI.Password) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid password"})
		return
	}

	sessionID, expiresAt, err := s.sessions.create(time.Now())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionID,
		Path:     "/",
		MaxAge:   sessionMaxAgeSecond,
		Expires:  expiresAt,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		s.sessions.delete(cookie.Value)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": s.isAuthenticated(r)})
}

func secureEqual(a, b string) bool {
	hashA := sha256.Sum256([]byte(a))
	hashB := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(hashA[:], hashB[:]) == 1
}
