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

func (p *webStubPlugin) Status(ctx context.Context) (string, error) {
	_ = ctx
	return "running", nil
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
	item := items[0]
	if item.State.Enabled {
		t.Fatalf("expected plugin to be disabled")
	}
	if !item.State.Commands["cmd.one"] {
		t.Fatalf("expected cmd.one enabled, got %#v", item.State.Commands)
	}
	if !item.State.Events["evt.one"] {
		t.Fatalf("expected evt.one enabled, got %#v", item.State.Events)
	}

	if item.State.CommandPrefix != "" {
		t.Fatalf("expected command prefix to be empty when omitted, got %q", item.State.CommandPrefix)
	}

	if items[0].PluginID != "external.demo" {
		t.Fatalf("unexpected plugin id: %s", items[0].PluginID)
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

	body := bytes.NewBufferString(`{"enabled":false,"commands":{"cmd.one":false},"events":{"evt.one":false},"prefix":"/@"}`)
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
	if response.State.CommandPrefix != "/@" {
		t.Fatalf("expected command prefix in response: %#v", response.State.CommandPrefix)
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

func TestHandlePluginEnvGetAndPut(t *testing.T) {
	handler, store := newPluginTestHandler(t)
	session := loginTestSession(t, handler, store)

	var restartScopes []string
	// re-create server with env change hook via store already used
	s := New(store, plugin.NewManager())
	s.SetPluginEnvChangeHandler(func(ctx context.Context, scope string, pluginID string) {
		restartScopes = append(restartScopes, scope+":"+pluginID)
	})
	handler = s.Handler()
	session = loginTestSession(t, handler, store)

	putBody := bytes.NewBufferString(`{"plugin_env":{"HTTP_PROXY":"http://127.0.0.1:7890"," EMPTY ":"","=bad":"x"}}`)
	// invalid key should fail
	req := httptest.NewRequest(http.MethodPut, "/api/plugin-env", putBody)
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request for invalid env key, got %d body=%s", rec.Code, rec.Body.String())
	}

	putBody = bytes.NewBufferString(`{"plugin_env":{"HTTP_PROXY":"http://127.0.0.1:7890","LANG":"C.UTF-8"}}`)
	req = httptest.NewRequest(http.MethodPut, "/api/plugin-env", putBody)
	req.AddCookie(session)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("put plugin-env expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !reflect.DeepEqual(restartScopes, []string{"global:"}) {
		t.Fatalf("expected global restart callback, got %#v", restartScopes)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/plugin-env", nil)
	req.AddCookie(session)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get plugin-env expected 200, got %d", rec.Code)
	}
	var got struct {
		PluginEnv map[string]string `json:"plugin_env"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.PluginEnv["HTTP_PROXY"] != "http://127.0.0.1:7890" || got.PluginEnv["LANG"] != "C.UTF-8" {
		t.Fatalf("unexpected plugin_env: %#v", got.PluginEnv)
	}

	// identical put should not re-trigger restart
	restartScopes = nil
	putBody = bytes.NewBufferString(`{"plugin_env":{"HTTP_PROXY":"http://127.0.0.1:7890","LANG":"C.UTF-8"}}`)
	req = httptest.NewRequest(http.MethodPut, "/api/plugin-env", putBody)
	req.AddCookie(session)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("second put expected 200, got %d", rec.Code)
	}
	if len(restartScopes) != 0 {
		t.Fatalf("expected no restart on unchanged env, got %#v", restartScopes)
	}
}

func TestHandlePluginSwitchesEnvRestartsPlugin(t *testing.T) {
	pm := plugin.NewManager()
	if _, err := pm.Register(context.Background(), &webStubPlugin{desc: plugin.Descriptor{
		PluginID: "external.demo",
		Name:     "Demo",
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	store, err := config.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if _, err := store.LoadOrCreateDefault(); err != nil {
		t.Fatalf("load: %v", err)
	}

	var restarts []string
	s := New(store, pm)
	s.SetPluginEnvChangeHandler(func(ctx context.Context, scope string, pluginID string) {
		restarts = append(restarts, scope+":"+pluginID)
	})
	handler := s.Handler()
	session := loginTestSession(t, handler, store)

	body := bytes.NewBufferString(`{"env":{"CHROME_PATH":"/usr/bin/chromium","EMPTY":""}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/plugins/external.demo/switches", body)
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !reflect.DeepEqual(restarts, []string{"plugin:external.demo"}) {
		t.Fatalf("expected plugin restart, got %#v", restarts)
	}

	control := store.Get().PluginControls["external.demo"]
	if control.Env["CHROME_PATH"] != "/usr/bin/chromium" || control.Env["EMPTY"] != "" {
		t.Fatalf("unexpected env: %#v", control.Env)
	}

	// list state includes env
	req = httptest.NewRequest(http.MethodGet, "/api/plugins", nil)
	req.AddCookie(session)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list expected 200, got %d", rec.Code)
	}
	var items []pluginListItem
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(items) != 1 || items[0].State.Env["CHROME_PATH"] != "/usr/bin/chromium" {
		t.Fatalf("expected env in state, got %#v", items)
	}
}
