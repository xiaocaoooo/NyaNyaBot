package web

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/a-h/templ"

	"github.com/xiaocaoooo/nyanyabot/internal/web/ui"

	"github.com/xiaocaoooo/nyanyabot/internal/config"
	"github.com/xiaocaoooo/nyanyabot/internal/plugin"
)

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

	// Pages.
	mux.HandleFunc("/config", s.handleConfigPage)
	mux.HandleFunc("/plugins/", s.handlePluginDetailPage)
	mux.HandleFunc("/plugins", s.handlePluginsPage)
	mux.HandleFunc("/", s.handleDashboard)

	return mux
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

func (s *Server) handlePluginDetailPage(w http.ResponseWriter, r *http.Request) {
	// /plugins/{pluginID}
	if !strings.HasPrefix(r.URL.Path, "/plugins/") {
		s.renderHTML(w, r, ui.NotFoundPage(r.URL.Path), http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	pid := strings.TrimPrefix(r.URL.Path, "/plugins/")
	pid = strings.TrimSpace(pid)
	pid = strings.Trim(pid, "/")
	if pid == "" {
		http.Redirect(w, r, "/plugins", http.StatusSeeOther)
		return
	}
	_, desc, ok := s.pm.Get(pid)
	if !ok {
		s.renderHTML(w, r, ui.NotFoundPage(r.URL.Path), http.StatusNotFound)
		return
	}
	s.renderHTML(w, r, ui.PluginDetailPage(desc), http.StatusOK)
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
