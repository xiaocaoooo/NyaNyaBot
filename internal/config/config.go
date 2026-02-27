package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/xiaocaoooo/nyanyabot/internal/util"
)

const defaultReverseWSListen = "0.0.0.0:3001"

// AppConfig is persisted under data/config.json.
// All user-generated config must live inside the data directory.
type AppConfig struct {
	OneBot  OneBotConfig               `json:"onebot"`
	WebUI   WebUIConfig                `json:"webui"`
	Plugins map[string]json.RawMessage `json:"plugins,omitempty"`
}

type OneBotConfig struct {
	ReverseWS ReverseWSConfig `json:"reverse_ws"`
}

type ReverseWSConfig struct {
	ListenAddr string `json:"listen_addr"`
}

type WebUIConfig struct {
	ListenAddr string `json:"listen_addr"`
}

func Default() AppConfig {
	return AppConfig{
		OneBot: OneBotConfig{
			ReverseWS: ReverseWSConfig{
				ListenAddr: defaultReverseWSListen,
			},
		},
		WebUI:   WebUIConfig{ListenAddr: "127.0.0.1:3000"},
		Plugins: make(map[string]json.RawMessage),
	}
}

type Store struct {
	mu   sync.RWMutex
	path string
	cfg  AppConfig
}

func NewStore(dataDir string) (*Store, error) {
	if dataDir == "" {
		return nil, errors.New("dataDir is empty")
	}
	if err := util.EnsureDir(dataDir); err != nil {
		return nil, err
	}
	return &Store{path: filepath.Join(dataDir, "config.json")}, nil
}

func (s *Store) LoadOrCreateDefault() (AppConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.cfg = Default()
			return s.cfg, s.saveLocked(s.cfg)
		}
		return AppConfig{}, err
	}

	var cfg AppConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		// If config file is corrupted, do not overwrite it automatically.
		return AppConfig{}, err
	}

	// Apply defaults for new fields.
	if cfg.OneBot.ReverseWS.ListenAddr == "" {
		cfg.OneBot.ReverseWS.ListenAddr = defaultReverseWSListen
	}
	if cfg.WebUI.ListenAddr == "" {
		cfg.WebUI.ListenAddr = "127.0.0.1:3000"
	}
	if cfg.Plugins == nil {
		cfg.Plugins = make(map[string]json.RawMessage)
	}

	s.cfg = cfg
	return s.cfg, nil
}

func (s *Store) Get() AppConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *Store) Update(fn func(*AppConfig)) (AppConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg := s.cfg
	fn(&cfg)

	// Keep defaults / sanitize.
	if cfg.OneBot.ReverseWS.ListenAddr == "" {
		cfg.OneBot.ReverseWS.ListenAddr = defaultReverseWSListen
	}
	if cfg.WebUI.ListenAddr == "" {
		cfg.WebUI.ListenAddr = "127.0.0.1:3000"
	}
	if cfg.Plugins == nil {
		cfg.Plugins = make(map[string]json.RawMessage)
	}

	if err := s.saveLocked(cfg); err != nil {
		return AppConfig{}, err
	}
	s.cfg = cfg
	return s.cfg, nil
}

func (s *Store) saveLocked(cfg AppConfig) error {
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	// Write atomically.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
