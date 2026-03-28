package config

import (
	"crypto/rand"
	"encoding/base64"
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
	OneBot OneBotConfig `json:"onebot"`
	WebUI  WebUIConfig  `json:"webui"`
	// Globals are user-defined variables for config templating.
	// They can be referenced in plugin config strings as ${global:name}.
	// To keep a literal placeholder, use \${global:name} (will become ${global:name} without substitution).
	Globals map[string]string          `json:"globals,omitempty"`
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
	Password   string `json:"password"`
}

func Default() AppConfig {
	return AppConfig{
		OneBot: OneBotConfig{
			ReverseWS: ReverseWSConfig{
				ListenAddr: defaultReverseWSListen,
			},
		},
		WebUI:   WebUIConfig{ListenAddr: "0.0.0.0:3000"},
		Globals: make(map[string]string),
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
			cfg := Default()
			if _, err := s.ensureDefaultsLocked(&cfg); err != nil {
				return AppConfig{}, err
			}
			s.cfg = cfg
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
	changed, err := s.ensureDefaultsLocked(&cfg)
	if err != nil {
		return AppConfig{}, err
	}

	s.cfg = cfg
	if !changed {
		return s.cfg, nil
	}
	return s.cfg, s.saveLocked(s.cfg)
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
	if _, err := s.ensureDefaultsLocked(&cfg); err != nil {
		return AppConfig{}, err
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

func (s *Store) ensureDefaultsLocked(cfg *AppConfig) (bool, error) {
	changed := false

	if cfg == nil {
		return false, errors.New("config is nil")
	}
	if cfg.OneBot.ReverseWS.ListenAddr == "" {
		cfg.OneBot.ReverseWS.ListenAddr = defaultReverseWSListen
		changed = true
	}
	if cfg.WebUI.ListenAddr == "" {
		cfg.WebUI.ListenAddr = "0.0.0.0:3000"
		changed = true
	}
	if cfg.WebUI.Password == "" {
		password, err := generateWebUIPassword(24)
		if err != nil {
			return false, err
		}
		cfg.WebUI.Password = password
		changed = true
	}
	if cfg.Globals == nil {
		cfg.Globals = make(map[string]string)
		changed = true
	}
	if cfg.Plugins == nil {
		cfg.Plugins = make(map[string]json.RawMessage)
		changed = true
	}
	return changed, nil
}

func generateWebUIPassword(length int) (string, error) {
	if length <= 0 {
		return "", errors.New("password length must be positive")
	}
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(buf)
	if len(encoded) < length {
		return "", errors.New("generated password is too short")
	}
	return encoded[:length], nil
}
