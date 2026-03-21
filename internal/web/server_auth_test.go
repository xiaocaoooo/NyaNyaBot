package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xiaocaoooo/nyanyabot/internal/config"
	"github.com/xiaocaoooo/nyanyabot/internal/plugin"
)

func newTestHandler(t *testing.T) (http.Handler, *config.Store) {
	t.Helper()

	store, err := config.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.LoadOrCreateDefault(); err != nil {
		t.Fatalf("load store: %v", err)
	}
	s := New(store, plugin.NewManager())
	return s.Handler(), store
}

func TestAPIRequiresAuth(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected %d, got %d", http.StatusUnauthorized, rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "unauthorized") {
		t.Fatalf("expected unauthorized body, got %q", rec.Body.String())
	}
}

func TestPageRedirectsToLogin(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/plugins?tab=installed", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected %d, got %d", http.StatusFound, rec.Code)
	}
	location := rec.Header().Get("Location")
	expected := "/login/?next=%2Fplugins%3Ftab%3Dinstalled"
	if location != expected {
		t.Fatalf("expected Location %q, got %q", expected, location)
	}
}

func TestLoginAndLogoutFlow(t *testing.T) {
	handler, store := newTestHandler(t)
	password := store.Get().WebUI.Password
	if password == "" {
		t.Fatal("expected generated webui password")
	}

	// Wrong password should be rejected.
	wrongReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(`{"password":"wrong"}`))
	wrongRec := httptest.NewRecorder()
	handler.ServeHTTP(wrongRec, wrongReq)
	if wrongRec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password expected %d, got %d", http.StatusUnauthorized, wrongRec.Code)
	}

	// Correct password creates a session cookie.
	loginBody, _ := json.Marshal(map[string]string{"password": password})
	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginBody))
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("correct password expected %d, got %d", http.StatusOK, loginRec.Code)
	}

	var sessionCookie *http.Cookie
	for _, c := range loginRec.Result().Cookies() {
		if c.Name == sessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected session cookie")
	}

	// Authenticated call should pass.
	authedReq := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	authedReq.AddCookie(sessionCookie)
	authedRec := httptest.NewRecorder()
	handler.ServeHTTP(authedRec, authedReq)
	if authedRec.Code != http.StatusOK {
		t.Fatalf("authenticated request expected %d, got %d", http.StatusOK, authedRec.Code)
	}

	// Status endpoint should report authenticated.
	statusReq := httptest.NewRequest(http.MethodGet, "/api/auth/status", nil)
	statusReq.AddCookie(sessionCookie)
	statusRec := httptest.NewRecorder()
	handler.ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("auth status expected %d, got %d", http.StatusOK, statusRec.Code)
	}
	if !strings.Contains(statusRec.Body.String(), `"authenticated":true`) {
		t.Fatalf("expected authenticated status, got %q", statusRec.Body.String())
	}

	// Logout should invalidate session.
	logoutReq := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	logoutReq.AddCookie(sessionCookie)
	logoutRec := httptest.NewRecorder()
	handler.ServeHTTP(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusOK {
		t.Fatalf("logout expected %d, got %d", http.StatusOK, logoutRec.Code)
	}

	afterLogoutReq := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	afterLogoutReq.AddCookie(sessionCookie)
	afterLogoutRec := httptest.NewRecorder()
	handler.ServeHTTP(afterLogoutRec, afterLogoutReq)
	if afterLogoutRec.Code != http.StatusUnauthorized {
		t.Fatalf("after logout expected %d, got %d", http.StatusUnauthorized, afterLogoutRec.Code)
	}
}
