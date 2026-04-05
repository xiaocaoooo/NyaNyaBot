package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/xiaocaoooo/nyanyabot/internal/config"
	"github.com/xiaocaoooo/nyanyabot/internal/onebot/ob11"
	"github.com/xiaocaoooo/nyanyabot/internal/plugin"
)

type webStubPlugin struct {
	desc plugin.Descriptor
}

func (p *webStubPlugin) Descriptor(ctx context.Context) (plugin.Descriptor, error) {
	_ = ctx
	return p.desc, nil
}

func (p *webStubPlugin) Configure(ctx context.Context, config json.RawMessage) error {
	_ = ctx
	_ = config
	return nil
}

func (p *webStubPlugin) Invoke(ctx context.Context, method string, paramsJSON json.RawMessage, callerPluginID string) (json.RawMessage, error) {
	_ = ctx
	_ = method
	_ = paramsJSON
	_ = callerPluginID
	return nil, nil
}

func (p *webStubPlugin) Handle(ctx context.Context, listenerID string, eventRaw ob11.Event, match *plugin.CommandMatch) (plugin.HandleResult, error) {
	_ = ctx
	_ = listenerID
	_ = eventRaw
	_ = match
	return plugin.HandleResult{}, nil
}

func (p *webStubPlugin) Shutdown(ctx context.Context) error {
	_ = ctx
	return nil
}

func newPluginTestHandler(t *testing.T, plugins ...plugin.Plugin) (http.Handler, *config.Store) {
	t.Helper()

	store, err := config.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.LoadOrCreateDefault(); err != nil {
		t.Fatalf("load store: %v", err)
	}

	pm := plugin.NewManager()
	for _, p := range plugins {
		if _, err := pm.Register(context.Background(), p); err != nil {
			t.Fatalf("register plugin: %v", err)
		}
	}

	s := New(store, pm)
	return s.Handler(), store
}

func loginTestSession(t *testing.T, handler http.Handler, store *config.Store) *http.Cookie {
	t.Helper()
	password := store.Get().WebUI.Password
	loginBody, _ := json.Marshal(map[string]string{"password": password})
	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginBody))
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login expected %d, got %d", http.StatusOK, loginRec.Code)
	}
	for _, c := range loginRec.Result().Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	t.Fatal("expected session cookie")
	return nil
}

func TestHandlePluginsReturnsMergedState(t *testing.T) {
	handler, store := newPluginTestHandler(t, &webStubPlugin{desc: plugin.Descriptor{
		PluginID: "external.demo",
		Name:     "Demo",
		Commands: []plugin.CommandListener{{ID: "cmd.one"}, {ID: "cmd.two"}},
		Events:   []plugin.EventListener{{ID: "evt.one"}},
	}})
	if _, err := store.Update(func(c *config.AppConfig) {
		c.PluginControls["external.demo"] = config.PluginControl{
			Disabled:         true,
			DisabledCommands: []string{"cmd.two"},
		}
	}); err != nil {
		t.Fatalf("update store: %v", err)
	}

	session := loginTestSession(t, handler, store)
	req := httptest.NewRequest(http.MethodGet, "/api/plugins", nil)
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d", http.StatusOK, rec.Code)
	}

	var items []pluginListItem
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(items))
	}
	if items[0].PluginID != "external.demo" {
		t.Fatalf("unexpected plugin id: %s", items[0].PluginID)
	}
	if items[0].State.Enabled {
		t.Fatal("expected plugin to be disabled")
	}
	expectedCommands := map[string]bool{"cmd.one": true, "cmd.two": false}
	if !reflect.DeepEqual(items[0].State.Commands, expectedCommands) {
		t.Fatalf("unexpected command state: %#v", items[0].State.Commands)
	}
	expectedEvents := map[string]bool{"evt.one": true}
	if !reflect.DeepEqual(items[0].State.Events, expectedEvents) {
		t.Fatalf("unexpected event state: %#v", items[0].State.Events)
	}
}

func TestHandlePluginSwitchesPersistsState(t *testing.T) {
	handler, store := newPluginTestHandler(t, &webStubPlugin{desc: plugin.Descriptor{
		PluginID: "external.demo",
		Name:     "Demo",
		Commands: []plugin.CommandListener{{ID: "cmd.one"}},
		Events:   []plugin.EventListener{{ID: "evt.one"}},
	}})
	session := loginTestSession(t, handler, store)

	body := bytes.NewBufferString(`{"enabled":false,"commands":{"cmd.one":false},"events":{"evt.one":false}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/plugins/external.demo/switches", body)
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d (%s)", http.StatusOK, rec.Code, rec.Body.String())
	}

	var response struct {
		OK    bool            `json:"ok"`
		State pluginStateView `json:"state"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !response.OK || response.State.Enabled {
		t.Fatalf("unexpected response: %#v", response)
	}
	if response.State.Commands["cmd.one"] {
		t.Fatalf("expected cmd.one disabled, got %#v", response.State.Commands)
	}
	if response.State.Events["evt.one"] {
		t.Fatalf("expected evt.one disabled, got %#v", response.State.Events)
	}

	cfg := store.Get()
	control, ok := cfg.PluginControls["external.demo"]
	if !ok {
		t.Fatalf("expected plugin control persisted, got %#v", cfg.PluginControls)
	}
	if !control.Disabled {
		t.Fatalf("expected plugin disabled, got %#v", control)
	}
	if !reflect.DeepEqual(control.DisabledCommands, []string{"cmd.one"}) {
		t.Fatalf("unexpected disabled commands: %#v", control.DisabledCommands)
	}
	if !reflect.DeepEqual(control.DisabledEvents, []string{"evt.one"}) {
		t.Fatalf("unexpected disabled events: %#v", control.DisabledEvents)
	}

	secondBody := bytes.NewBufferString(`{"commands":{"cmd.one":true}}`)
	secondReq := httptest.NewRequest(http.MethodPut, "/api/plugins/external.demo/switches", secondBody)
	secondReq.AddCookie(session)
	secondRec := httptest.NewRecorder()
	handler.ServeHTTP(secondRec, secondReq)
	if secondRec.Code != http.StatusOK {
		t.Fatalf("second update expected %d, got %d (%s)", http.StatusOK, secondRec.Code, secondRec.Body.String())
	}

	cfg = store.Get()
	control = cfg.PluginControls["external.demo"]
	if !control.Disabled {
		t.Fatalf("expected plugin disabled to remain unchanged, got %#v", control)
	}
	if len(control.DisabledCommands) != 0 {
		t.Fatalf("expected cmd.one re-enabled, got %#v", control.DisabledCommands)
	}
	if !reflect.DeepEqual(control.DisabledEvents, []string{"evt.one"}) {
		t.Fatalf("expected disabled events unchanged, got %#v", control.DisabledEvents)
	}
}

func TestHandlePluginSwitchesRejectsUnknownListener(t *testing.T) {
	handler, store := newPluginTestHandler(t, &webStubPlugin{desc: plugin.Descriptor{
		PluginID: "external.demo",
		Commands: []plugin.CommandListener{{ID: "cmd.one"}},
	}})
	session := loginTestSession(t, handler, store)

	body := bytes.NewBufferString(`{"commands":{"cmd.unknown":false}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/plugins/external.demo/switches", body)
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d", http.StatusBadRequest, rec.Code)
	}
}
