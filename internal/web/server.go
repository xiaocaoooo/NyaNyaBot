package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/xiaocaoooo/nyanyabot/internal/config"
	"github.com/xiaocaoooo/nyanyabot/internal/configtmpl"
	"github.com/xiaocaoooo/nyanyabot/internal/plugin"
)

type pluginConfigPatch struct {
	Config json.RawMessage `json:"config"`
}

type Server struct {
	store    *config.Store
	pm       *plugin.Manager
	frontend fs.FS
	sessions *sessionManager
}

func New(store *config.Store, pm *plugin.Manager) *Server {
	return &Server{store: store, pm: pm, frontend: frontendFS(), sessions: newSessionManager()}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/auth/login", s.handleAuthLogin)
	mux.HandleFunc("/api/auth/logout", s.handleAuthLogout)
	mux.HandleFunc("/api/auth/status", s.handleAuthStatus)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/globals", s.handleGlobals)
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
	w.WriteHeader(http.StatusNotFound)
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
	writeJSON(w, http.StatusOK, s.pm.List())
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
