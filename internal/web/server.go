package web

import (
	"encoding/json"
	"net/http"
	"strings"

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

	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/plugins", s.handlePlugins)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("nyanyabot webui placeholder\n"))
	})

	return mux
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
