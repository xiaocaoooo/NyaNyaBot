package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/a-h/templ"

	"github.com/xiaocaoooo/nyanyabot/internal/web/ui"

	"github.com/xiaocaoooo/nyanyabot/internal/config"
	"github.com/xiaocaoooo/nyanyabot/internal/plugin"
	"github.com/xiaocaoooo/nyanyabot/internal/web/schemaform"
)

type pluginConfigPatch struct {
	Config json.RawMessage `json:"config"`
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
		// Ensure it's a JSON object (or empty).
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

		// Persist.
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

		// Hot-apply.
		p, _, ok := s.pm.Get(pluginID)
		if ok {
			_ = p.Configure(r.Context(), cfg.Plugins[pluginID])
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

type Server struct {
	store *config.Store
	pm    *plugin.Manager
}

func New(store *config.Store, pm *plugin.Manager) *Server {
	return &Server{store: store, pm: pm}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Assets.
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assetsFS()))))

	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/plugins", s.handlePlugins)
	mux.HandleFunc("/api/plugins/", s.handlePluginSubAPI)

	// Pages.
	mux.HandleFunc("/config", s.handleConfigPage)
	mux.HandleFunc("/plugins/", s.handlePluginSubPage)
	mux.HandleFunc("/plugins", s.handlePluginsPage)
	mux.HandleFunc("/", s.handleDashboard)

	return mux
}

func (s *Server) handlePluginSubAPI(w http.ResponseWriter, r *http.Request) {
	// /api/plugins/{pluginID}/config
	path := strings.TrimPrefix(r.URL.Path, "/api/plugins/")
	path = strings.Trim(path, "/")
	if path == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) == 2 && parts[1] == "config" {
		s.handlePluginConfigAPI(w, r, parts[0])
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func (s *Server) renderHTML(w http.ResponseWriter, r *http.Request, c templ.Component, status int) {
	templ.Handler(c,
		templ.WithStatus(status),
		templ.WithContentType("text/html; charset=utf-8"),
	).ServeHTTP(w, r)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		s.renderHTML(w, r, ui.NotFoundPage(r.URL.Path), http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	plugins := s.pm.List()
	cfg := s.store.Get()
	s.renderHTML(w, r, ui.Dashboard(cfg, plugins), http.StatusOK)
}

func (s *Server) handlePluginsPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/plugins" {
		s.renderHTML(w, r, ui.NotFoundPage(r.URL.Path), http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	plugins := s.pm.List()
	s.renderHTML(w, r, ui.PluginsPage(plugins), http.StatusOK)
}

func (s *Server) handlePluginSubPage(w http.ResponseWriter, r *http.Request) {
	// /plugins/{pluginID}
	// /plugins/{pluginID}/config
	path := strings.TrimPrefix(r.URL.Path, "/plugins/")
	path = strings.Trim(path, "/")
	if path == "" {
		http.Redirect(w, r, "/plugins", http.StatusSeeOther)
		return
	}
	parts := strings.Split(path, "/")
	pid := strings.TrimSpace(parts[0])
	if pid == "" {
		http.Redirect(w, r, "/plugins", http.StatusSeeOther)
		return
	}
	if len(parts) == 2 && parts[1] == "config" {
		s.handlePluginConfigPage(w, r, pid)
		return
	}
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		_, desc, ok := s.pm.Get(pid)
		if !ok {
			s.renderHTML(w, r, ui.NotFoundPage(r.URL.Path), http.StatusNotFound)
			return
		}
		s.renderHTML(w, r, ui.PluginDetailPage(desc), http.StatusOK)
		return
	}

	s.renderHTML(w, r, ui.NotFoundPage(r.URL.Path), http.StatusNotFound)
}

func (s *Server) handlePluginConfigPage(w http.ResponseWriter, r *http.Request, pluginID string) {
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		s.renderHTML(w, r, ui.NotFoundPage(r.URL.Path), http.StatusNotFound)
		return
	}
	_, desc, ok := s.pm.Get(pluginID)
	if !ok {
		s.renderHTML(w, r, ui.NotFoundPage(r.URL.Path), http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodGet:
		cfg := s.store.Get()
		curRaw := json.RawMessage("{}")
		if v, ok := cfg.Plugins[pluginID]; ok && len(v) > 0 {
			curRaw = v
		} else if desc.Config != nil && len(desc.Config.Default) > 0 {
			curRaw = desc.Config.Default
		}
		cur := string(curRaw)

		fieldsHTML := ""
		if desc.Config != nil && len(desc.Config.Schema) > 0 {
			if form, err := schemaform.Parse(desc.Config.Schema); err == nil {
				vals, _ := schemaform.ApplyDefaults(curRaw, desc.Config.Default, form)
				fieldsHTML = "<input type=\"hidden\" name=\"mode\" value=\"schema\">" + schemaform.RenderHTML(form, vals)
			}
		}
		if fieldsHTML == "" {
			// Fallback to raw JSON editor if schema unsupported.
			fieldsHTML = "<div class=\"field\"><label for=\"plugin_config_json\">JSON</label><textarea id=\"plugin_config_json\" name=\"plugin_config_json\" rows=\"14\" spellcheck=\"false\" placeholder=\"{}\">" + templ.EscapeString(cur) + "</textarea></div>" +
				"<input type=\"hidden\" name=\"mode\" value=\"json\">"
		}
		flashOK := ""
		if r.URL.Query().Get("saved") == "1" {
			flashOK = "已保存并立即生效"
		}
		s.renderHTML(w, r, ui.PluginConfigPage(desc, cur, fieldsHTML, flashOK, ""), http.StatusOK)
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			s.renderHTML(w, r, ui.PluginConfigPage(desc, "{}", "", "", "表单解析失败: "+err.Error()), http.StatusBadRequest)
			return
		}
		action := strings.TrimSpace(r.FormValue("action"))
		mode := strings.TrimSpace(r.FormValue("mode"))
		var raw string
		switch action {
		case "reset":
			if desc.Config != nil && len(desc.Config.Default) > 0 {
				raw = string(desc.Config.Default)
			} else {
				raw = "{}"
			}
		default:
			if mode == "schema" && desc.Config != nil && len(desc.Config.Schema) > 0 {
				form, err := schemaform.Parse(desc.Config.Schema)
				if err != nil {
					s.renderHTML(w, r, ui.PluginConfigPage(desc, "{}", "", "", "Schema 解析失败: "+err.Error()), http.StatusBadRequest)
					return
				}
				obj, err := schemaform.CoerceFromForm(r.PostForm, form)
				if err != nil {
					// Re-render fields with current (best-effort) defaults.
					cfg2 := s.store.Get()
					curRaw2 := json.RawMessage("{}")
					if v, ok := cfg2.Plugins[pluginID]; ok && len(v) > 0 {
						curRaw2 = v
					} else if desc.Config != nil && len(desc.Config.Default) > 0 {
						curRaw2 = desc.Config.Default
					}
					vals, _ := schemaform.ApplyDefaults(curRaw2, desc.Config.Default, form)
					fieldsHTML := schemaform.RenderHTML(form, vals)
					s.renderHTML(w, r, ui.PluginConfigPage(desc, string(curRaw2), fieldsHTML, "", err.Error()), http.StatusBadRequest)
					return
				}
				b, _ := json.Marshal(obj)
				raw = string(b)
			} else {
				raw = r.FormValue("plugin_config_json")
				if strings.TrimSpace(raw) == "" {
					raw = "{}"
				}
			}
		}

		// Validate JSON object.
		b := bytes.TrimSpace([]byte(raw))
		if len(b) == 0 {
			b = []byte("{}")
		}
		if b[0] != '{' {
			s.renderHTML(w, r, ui.PluginConfigPage(desc, raw, "", "", "配置必须是 JSON 对象（以 { 开头）"), http.StatusBadRequest)
			return
		}
		var tmp any
		if err := json.Unmarshal(b, &tmp); err != nil {
			s.renderHTML(w, r, ui.PluginConfigPage(desc, raw, "", "", "JSON 无法解析: "+err.Error()), http.StatusBadRequest)
			return
		}

		// Persist.
		cfg, err := s.store.Update(func(c *config.AppConfig) {
			if c.Plugins == nil {
				c.Plugins = make(map[string]json.RawMessage)
			}
			c.Plugins[pluginID] = json.RawMessage(b)
		})
		if err != nil {
			s.renderHTML(w, r, ui.PluginConfigPage(desc, raw, "", "", "保存失败: "+err.Error()), http.StatusInternalServerError)
			return
		}

		// Hot-apply.
		p2, _, ok := s.pm.Get(pluginID)
		if ok {
			_ = p2.Configure(r.Context(), cfg.Plugins[pluginID])
		}
		http.Redirect(w, r, "/plugins/"+pluginID+"/config?saved=1", http.StatusSeeOther)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleConfigPage(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := s.store.Get()
		flashOK := ""
		if r.URL.Query().Get("saved") == "1" {
			flashOK = "已保存（重启后生效）"
		}
		s.renderHTML(w, r, ui.ConfigPage(cfg, flashOK, ""), http.StatusOK)
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			cfg := s.store.Get()
			s.renderHTML(w, r, ui.ConfigPage(cfg, "", "表单解析失败: "+err.Error()), http.StatusBadRequest)
			return
		}
		webAddr := strings.TrimSpace(r.FormValue("webui_listen_addr"))
		obAddr := strings.TrimSpace(r.FormValue("onebot_reverse_ws_listen_addr"))

		_, err := s.store.Update(func(c *config.AppConfig) {
			if webAddr != "" {
				c.WebUI.ListenAddr = webAddr
			} else {
				c.WebUI.ListenAddr = ""
			}
			if obAddr != "" {
				c.OneBot.ReverseWS.ListenAddr = obAddr
			} else {
				c.OneBot.ReverseWS.ListenAddr = ""
			}
		})
		if err != nil {
			cfg := s.store.Get()
			s.renderHTML(w, r, ui.ConfigPage(cfg, "", "保存失败: "+err.Error()), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/config?saved=1", http.StatusSeeOther)
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
