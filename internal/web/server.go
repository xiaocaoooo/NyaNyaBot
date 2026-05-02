package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"sort"
	"strings"

	"github.com/xiaocaoooo/nyanyabot/internal/config"
	"github.com/xiaocaoooo/nyanyabot/internal/configtmpl"
	"github.com/xiaocaoooo/nyanyabot/internal/plugin"
	"github.com/xiaocaoooo/nyanyabot/internal/stats"
)

type pluginConfigPatch struct {
	Config json.RawMessage `json:"config"`
}

type pluginSwitchPatch struct {
	Enabled       *bool           `json:"enabled,omitempty"`
	Commands      map[string]bool `json:"commands,omitempty"`
	Events        map[string]bool `json:"events,omitempty"`
	CommandPrefix *string         `json:"prefix,omitempty"`
}

type pluginStateView struct {
	Enabled       bool            `json:"enabled"`
	Commands      map[string]bool `json:"commands"`
	Events        map[string]bool `json:"events"`
	CommandPrefix string          `json:"command_prefix"`
}

type pluginListItem struct {
	plugin.Descriptor
	State pluginStateView `json:"state"`
}

type Server struct {
	store         *config.Store
	pm            *plugin.Manager
	statsProvider StatsProvider
	frontend      fs.FS
	sessions      *sessionManager
}

// StatsProvider 提供统计信息的接口
type StatsProvider interface {
	Snapshot() stats.Snapshot
}

func New(store *config.Store, pm *plugin.Manager) *Server {
	return &Server{store: store, pm: pm, frontend: frontendFS(), sessions: newSessionManager()}
}

func (s *Server) SetStatsProvider(sp StatsProvider) {
	s.statsProvider = sp
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/auth/login", s.handleAuthLogin)
	mux.HandleFunc("/api/auth/logout", s.handleAuthLogout)
	mux.HandleFunc("/api/auth/status", s.handleAuthStatus)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/globals", s.handleGlobals)
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/api/plugins", s.handlePlugins)
	mux.HandleFunc("/api/plugins/", s.handlePluginSubAPI)

	// Serve exported Next.js static UI for all non-API routes.
	mux.HandleFunc("/", s.handleFrontend)

	return s.authMiddleware(mux)
}

// hotApplyAllPluginConfigs reapplies current globals into each persisted plugin config and calls Configure.
// This is used when globals change so plugins can take effect without editing each plugin config.
func (s *Server) hotApplyAllPluginConfigs(ctx context.Context, cfg config.AppConfig) {
	if s == nil || s.pm == nil {
		return
	}
	for pluginID, raw := range cfg.Plugins {
		p, _, ok := s.pm.Get(pluginID)
		if !ok {
			continue
		}
		patched, err := configtmpl.Apply(raw, cfg.Globals)
		if err != nil {
			patched = raw
		}
		_ = p.Configure(ctx, patched)
	}
}

func buildPluginState(cfg config.AppConfig, desc plugin.Descriptor) pluginStateView {
	state := pluginStateView{
		Enabled:  cfg.IsPluginEnabled(desc.PluginID),
		Commands: make(map[string]bool, len(desc.Commands)),
		Events:   make(map[string]bool, len(desc.Events)),
	}
	if control, ok := cfg.PluginControls[desc.PluginID]; ok {
		state.CommandPrefix = control.CommandPrefix
	}
	for _, command := range desc.Commands {
		state.Commands[command.ID] = cfg.IsCommandEnabled(desc.PluginID, command.ID)
	}
	for _, event := range desc.Events {
		state.Events[event.ID] = cfg.IsEventEnabled(desc.PluginID, event.ID)
	}
	return state
}

func applyPluginSwitchPatch(control config.PluginControl, patch pluginSwitchPatch) config.PluginControl {
	if patch.Enabled != nil {
		control.Disabled = !*patch.Enabled
	}
	if patch.CommandPrefix != nil {
		control.CommandPrefix = strings.TrimSpace(*patch.CommandPrefix)
	}
	if patch.Commands != nil {
		control.DisabledCommands = applyListenerSwitches(control.DisabledCommands, patch.Commands)
	}
	if patch.Events != nil {
		control.DisabledEvents = applyListenerSwitches(control.DisabledEvents, patch.Events)
	}
	return control
}

func applyListenerSwitches(disabled []string, patch map[string]bool) []string {
	disabledSet := make(map[string]struct{}, len(disabled))
	for _, listenerID := range disabled {
		listenerID = strings.TrimSpace(listenerID)
		if listenerID == "" {
			continue
		}
		disabledSet[listenerID] = struct{}{}
	}
	for listenerID, enabled := range patch {
		listenerID = strings.TrimSpace(listenerID)
		if listenerID == "" {
			continue
		}
		if enabled {
			delete(disabledSet, listenerID)
			continue
		}
		disabledSet[listenerID] = struct{}{}
	}
	out := make([]string, 0, len(disabledSet))
	for listenerID := range disabledSet {
		out = append(out, listenerID)
	}
	return out
}

func validateListenerSwitchIDs(desc plugin.Descriptor, commands map[string]bool, events map[string]bool) error {
	if commands != nil {
		allowedCommands := make(map[string]struct{}, len(desc.Commands))
		for _, command := range desc.Commands {
			allowedCommands[command.ID] = struct{}{}
		}
		for listenerID := range commands {
			listenerID = strings.TrimSpace(listenerID)
			if listenerID == "" {
				return errors.New("command listener id is empty")
			}
			if _, ok := allowedCommands[listenerID]; !ok {
				return fmt.Errorf("unknown command listener %q", listenerID)
			}
		}
	}
	if events != nil {
		allowedEvents := make(map[string]struct{}, len(desc.Events))
		for _, event := range desc.Events {
			allowedEvents[event.ID] = struct{}{}
		}
		for listenerID := range events {
			listenerID = strings.TrimSpace(listenerID)
			if listenerID == "" {
				return errors.New("event listener id is empty")
			}
			if _, ok := allowedEvents[listenerID]; !ok {
				return fmt.Errorf("unknown event listener %q", listenerID)
			}
		}
	}
	return nil
}

func (s *Server) handleFrontend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if strings.HasPrefix(r.URL.Path, "/api/") {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	cleaned := path.Clean("/" + r.URL.Path)
	rel := strings.TrimPrefix(cleaned, "/")
	if rel == "" || rel == "." {
		rel = "index.html"
	}

	candidates := []string{rel}
	if !strings.Contains(path.Base(rel), ".") {
		candidates = append(candidates, path.Join(rel, "index.html"), rel+".html")
	}
	// Backward compatibility: old Go WebUI used nested plugin/config routes.
	if strings.HasPrefix(rel, "plugins/") {
		candidates = append(candidates, "plugins/index.html")
	}
	if strings.HasPrefix(rel, "config/") || strings.HasPrefix(rel, "globals") {
		candidates = append(candidates, "config/index.html")
	}

	for _, name := range candidates {
		if s.serveFrontendFile(w, r, name, http.StatusOK) {
			return
		}
	}

	if s.serveFrontendFile(w, r, "404.html", http.StatusNotFound) {
		return
	}
	http.NotFound(w, r)
}

func (s *Server) serveFrontendFile(w http.ResponseWriter, r *http.Request, name string, status int) bool {
	if s == nil || s.frontend == nil {
		return false
	}

	file, err := fs.ReadFile(s.frontend, name)
	if err != nil {
		return false
	}

	contentType := mime.TypeByExtension(path.Ext(name))
	if contentType == "" {
		contentType = http.DetectContentType(file)
	}
	w.Header().Set("content-type", contentType)

	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	if r.Method == http.MethodHead {
		return true
	}
	_, _ = w.Write(file)
	return true
}

func (s *Server) handlePluginConfigAPI(w http.ResponseWriter, r *http.Request, pluginID string) {
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "plugin_id is empty"})
		return
	}
	if _, _, ok := s.pm.Get(pluginID); !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "plugin not found"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		cfg := s.store.Get()
		if v, ok := cfg.Plugins[pluginID]; ok && len(v) > 0 {
			writeJSON(w, http.StatusOK, map[string]any{"plugin_id": pluginID, "config": json.RawMessage(v)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"plugin_id": pluginID, "config": json.RawMessage("{}")})
	case http.MethodPut:
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		var patch pluginConfigPatch
		if err := dec.Decode(&patch); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}

		b := bytes.TrimSpace([]byte(patch.Config))
		if len(b) == 0 {
			b = []byte("{}")
		}
		if b[0] != '{' {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "config must be a JSON object"})
			return
		}
		var tmp any
		if err := json.Unmarshal(b, &tmp); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json: " + err.Error()})
			return
		}

		cfg, err := s.store.Update(func(c *config.AppConfig) {
			if c.Plugins == nil {
				c.Plugins = make(map[string]json.RawMessage)
			}
			c.Plugins[pluginID] = json.RawMessage(b)
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}

		p, _, ok := s.pm.Get(pluginID)
		if ok {
			patched, err := configtmpl.Apply(cfg.Plugins[pluginID], cfg.Globals)
			if err != nil {
				patched = cfg.Plugins[pluginID]
			}
			_ = p.Configure(r.Context(), patched)
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handlePluginSwitchesAPI(w http.ResponseWriter, r *http.Request, pluginID string) {
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "plugin_id is empty"})
		return
	}

	_, desc, ok := s.pm.Get(pluginID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "plugin not found"})
		return
	}
	if r.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var patch pluginSwitchPatch
	if err := dec.Decode(&patch); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if err := validateListenerSwitchIDs(desc, patch.Commands, patch.Events); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	cfg, err := s.store.Update(func(c *config.AppConfig) {
		if c.PluginControls == nil {
			c.PluginControls = make(map[string]config.PluginControl)
		}
		control := c.PluginControls[pluginID]
		c.PluginControls[pluginID] = applyPluginSwitchPatch(control, patch)
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"state": buildPluginState(cfg, desc),
	})
}

func (s *Server) handlePluginSubAPI(w http.ResponseWriter, r *http.Request) {
	// /api/plugins/{pluginID}/config
	trimmed := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/plugins/"), "/")
	if trimmed == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	parts := strings.Split(trimmed, "/")
	if len(parts) == 2 && parts[1] == "config" {
		s.handlePluginConfigAPI(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "switches" {
		s.handlePluginSwitchesAPI(w, r, parts[0])
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.statsProvider == nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": "stats not available"})
		return
	}
	snap := s.statsProvider.Snapshot()
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) handleGlobals(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := s.store.Get()
		if cfg.Globals == nil {
			cfg.Globals = map[string]string{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"globals": cfg.Globals})
	case http.MethodPut:
		var patch struct {
			Globals map[string]string `json:"globals"`
		}
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&patch); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}

		cfg, err := s.store.Update(func(c *config.AppConfig) {
			if c.Globals == nil {
				c.Globals = make(map[string]string)
			}
			c.Globals = make(map[string]string, len(patch.Globals))
			for k, v := range patch.Globals {
				k = strings.TrimSpace(k)
				if k == "" {
					continue
				}
				c.Globals[k] = strings.TrimSpace(v)
			}
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		s.hotApplyAllPluginConfigs(r.Context(), cfg)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "globals": cfg.Globals})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handlePlugins(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	cfg := s.store.Get()
	plugins := s.pm.List()
	sort.Slice(plugins, func(i int, j int) bool {
		if plugins[i].Name == plugins[j].Name {
			return plugins[i].PluginID < plugins[j].PluginID
		}
		return plugins[i].Name < plugins[j].Name
	})
	items := make([]pluginListItem, 0, len(plugins))
	for _, desc := range plugins {
		items = append(items, pluginListItem{
			Descriptor: desc,
			State:      buildPluginState(cfg, desc),
		})
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.store.Get())
	case http.MethodPut:
		var patch struct {
			OneBot *struct {
				ReverseWS *struct {
					ListenAddr *string `json:"listen_addr"`
				} `json:"reverse_ws"`
			} `json:"onebot"`
			WebUI *struct {
				ListenAddr *string `json:"listen_addr"`
				Password   *string `json:"password"`
			} `json:"webui"`
			MessagePrefix *string `json:"message_prefix"`
		}
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&patch); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}

		cfg, err := s.store.Update(func(c *config.AppConfig) {
			if patch.OneBot != nil && patch.OneBot.ReverseWS != nil && patch.OneBot.ReverseWS.ListenAddr != nil {
				c.OneBot.ReverseWS.ListenAddr = strings.TrimSpace(*patch.OneBot.ReverseWS.ListenAddr)
			}
			if patch.WebUI != nil && patch.WebUI.ListenAddr != nil {
				c.WebUI.ListenAddr = strings.TrimSpace(*patch.WebUI.ListenAddr)
			}
			if patch.WebUI != nil && patch.WebUI.Password != nil {
				c.WebUI.Password = strings.TrimSpace(*patch.WebUI.Password)
			}
			if patch.MessagePrefix != nil {
				c.MessagePrefix = strings.TrimSpace(*patch.MessagePrefix)
			}
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, cfg)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}
