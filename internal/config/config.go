package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/xiaocaoooo/nyanyabot/internal/util"
)

const defaultReverseWSListen = "0.0.0.0:3001"
const defaultMessagePrefix = `^/(?P<content>.+)$`

// AppConfig is persisted under data/config.json.
// All user-generated config must live inside the data directory.
type AppConfig struct {
	OneBot OneBotConfig `json:"onebot"`
	WebUI  WebUIConfig  `json:"webui"`
	// MessagePrefix is applied before command pattern matching.
	// It must be a RE2 regular expression.
	MessagePrefix string `json:"message_prefix,omitempty"`
	// Globals are user-defined variables for config templating.
	// They can be referenced in plugin config strings as ${global:name}.
	// To keep a literal placeholder, use \\${global:name} (will become ${global:name} without substitution).
	Globals map[string]string          `json:"globals,omitempty"`
	Plugins map[string]json.RawMessage `json:"plugins,omitempty"`
	// PluginControls stores host-side runtime switches for plugins and listeners.
	PluginControls map[string]PluginControl `json:"plugin_controls,omitempty"`
	ChatLog        ChatLogConfig            `json:"chat_log"`
	// MessageDedup enables message deduplication based on group_id + message_seq.
	// Only applies to group messages. Defaults to true.
	MessageDedup *bool       `json:"message_dedup,omitempty"`
	Dedup        DedupConfig `json:"dedup"`
}

// PluginControl stores host-side enable/disable state.
type PluginControl struct {
	Disabled         bool     `json:"disabled,omitempty"`
	DisabledCommands []string `json:"disabled_commands,omitempty"`
	DisabledEvents   []string `json:"disabled_events,omitempty"`
	DisabledCrons    []string `json:"disabled_crons,omitempty"`
	// CommandPrefix overrides AppConfig.MessagePrefix for this plugin.
	CommandPrefix string `json:"command_prefix,omitempty"`
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

type ChatLogQueueConfig struct {
	Type string `json:"type,omitempty"`
}

type ChatLogConfig struct {
	DatabaseURI string              `json:"database_uri"`
	Queue       *ChatLogQueueConfig `json:"queue,omitempty"`
}

type DedupConfig struct {
	Enabled    bool   `json:"enabled"`
	Backend    string `json:"backend"`
	TTLSeconds int    `json:"ttl_seconds"`
}

func Default() AppConfig {
	return AppConfig{
		OneBot: OneBotConfig{
			ReverseWS: ReverseWSConfig{
				ListenAddr: defaultReverseWSListen,
			},
		},
		WebUI:          WebUIConfig{ListenAddr: "0.0.0.0:3000"},
		MessagePrefix:  defaultMessagePrefix,
		Globals:        make(map[string]string),
		Plugins:        make(map[string]json.RawMessage),
		PluginControls: make(map[string]PluginControl),
		ChatLog:        ChatLogConfig{DatabaseURI: ""},
		Dedup: DedupConfig{
			Enabled:    true,
			Backend:    "memory",
			TTLSeconds: 3600,
		},
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
	if cfg.MessagePrefix == "" {
		cfg.MessagePrefix = defaultMessagePrefix
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
	if cfg.PluginControls == nil {
		cfg.PluginControls = make(map[string]PluginControl)
		changed = true
	}
	if cfg.MessageDedup == nil {
		dedupEnabled := true
		cfg.MessageDedup = &dedupEnabled
		changed = true
	}
	normalizedControls := normalizePluginControls(cfg.PluginControls)
	if !pluginControlsEqual(cfg.PluginControls, normalizedControls) {
		cfg.PluginControls = normalizedControls
		changed = true
	}
	// Ensure Dedup config has valid defaults.
	if cfg.Dedup.Backend == "" {
		cfg.Dedup.Backend = "memory"
		changed = true
	}
	if cfg.Dedup.TTLSeconds <= 0 {
		cfg.Dedup.TTLSeconds = 3600
		changed = true
	}
	// Validate Dedup backend.
	if cfg.Dedup.Backend != "memory" && cfg.Dedup.Backend != "redis" {
		return false, errors.New("dedup backend must be 'memory' or 'redis'")
	}
	return changed, nil
}

func (c AppConfig) IsPluginEnabled(pluginID string) bool {
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return true
	}
	control, ok := c.PluginControls[pluginID]
	if !ok {
		return true
	}
	return !control.Disabled
}

func (c AppConfig) IsCommandEnabled(pluginID string, listenerID string) bool {
	listenerID = strings.TrimSpace(listenerID)
	if listenerID == "" {
		return true
	}
	control, ok := c.PluginControls[strings.TrimSpace(pluginID)]
	if !ok {
		return true
	}
	for _, disabledID := range control.DisabledCommands {
		if disabledID == listenerID {
			return false
		}
	}
	return true
}

func (c AppConfig) IsEventEnabled(pluginID string, listenerID string) bool {
	listenerID = strings.TrimSpace(listenerID)
	if listenerID == "" {
		return true
	}
	control, ok := c.PluginControls[strings.TrimSpace(pluginID)]
	if !ok {
		return true
	}
	for _, disabledID := range control.DisabledEvents {
		if disabledID == listenerID {
			return false
		}
	}
	return true
}

func (c AppConfig) IsCronEnabled(pluginID string, listenerID string) bool {
	listenerID = strings.TrimSpace(listenerID)
	if listenerID == "" {
		return true
	}
	control, ok := c.PluginControls[strings.TrimSpace(pluginID)]
	if !ok {
		return true
	}
	for _, disabledID := range control.DisabledCrons {
		if disabledID == listenerID {
			return false
		}
	}
	return true
}

// IsMessageDedupEnabled returns whether message deduplication is enabled.
// Defaults to true if not explicitly set.
func (c AppConfig) IsMessageDedupEnabled() bool {
	if c.MessageDedup == nil {
		return true
	}
	return *c.MessageDedup
}

func normalizePluginControls(in map[string]PluginControl) map[string]PluginControl {
	if in == nil {
		return map[string]PluginControl{}
	}
	out := make(map[string]PluginControl, len(in))
	for pluginID, control := range in {
		pluginID = strings.TrimSpace(pluginID)
		if pluginID == "" {
			continue
		}
		control.DisabledCommands = normalizeStringSlice(control.DisabledCommands)
		control.DisabledEvents = normalizeStringSlice(control.DisabledEvents)
		control.DisabledCrons = normalizeStringSlice(control.DisabledCrons)
		control.CommandPrefix = strings.TrimSpace(control.CommandPrefix)
		if !control.Disabled && len(control.DisabledCommands) == 0 && len(control.DisabledEvents) == 0 && control.CommandPrefix == "" {
			continue
		}
		out[pluginID] = control
	}
	return out
}

func normalizeStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}

func pluginControlsEqual(left map[string]PluginControl, right map[string]PluginControl) bool {
	if len(left) != len(right) {
		return false
	}
	for pluginID, leftControl := range left {
		rightControl, ok := right[pluginID]
		if !ok {
			return false
		}
		if leftControl.Disabled != rightControl.Disabled {
			return false
		}
		if leftControl.CommandPrefix != strings.TrimSpace(rightControl.CommandPrefix) {
			return false
		}
		if !stringSlicesEqual(leftControl.DisabledCommands, rightControl.DisabledCommands) {
			return false
		}
		if !stringSlicesEqual(leftControl.DisabledEvents, rightControl.DisabledEvents) {
			return false
		}
	}
	return true
}

func stringSlicesEqual(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
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
