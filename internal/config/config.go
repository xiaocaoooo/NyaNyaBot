package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
	// GlobalAccess controls access across all plugins and commands.
	GlobalAccess AccessControl `json:"global_access,omitempty"`
	ChatLog      ChatLogConfig `json:"chat_log"`
	TriggerLog   TriggerLogConfig `json:"trigger_log"`
	// MessageDedup enables message deduplication based on group_id + message_seq.
	// Only applies to group messages. Defaults to true.
	MessageDedup *bool       `json:"message_dedup,omitempty"`
	Dedup        DedupConfig `json:"dedup"`
	// GlobalSleepTimeout defines the default sleep timeout in seconds for plugins.
	GlobalSleepTimeout int `json:"global_sleep_timeout,omitempty"`
}

// AccessControl defines whitelist and blacklist for users and groups.
type AccessControl struct {
	WhiteListUsers  []int64 `json:"whitelist_users,omitempty"`
	BlackListUsers  []int64 `json:"blacklist_users,omitempty"`
	WhiteListGroups []int64 `json:"whitelist_groups,omitempty"`
	BlackListGroups []int64 `json:"blacklist_groups,omitempty"`
}

// Allowed returns true if the user and group are allowed based on the rules.
func (ac AccessControl) Allowed(userID, groupID int64) bool {
	// 1. Blacklist check (highest priority)
	for _, id := range ac.BlackListUsers {
		if id == userID {
			return false
		}
	}
	if groupID > 0 {
		for _, id := range ac.BlackListGroups {
			if id == groupID {
				return false
			}
		}
	}

	// 2. Whitelist check
	hasUserWhitelist := len(ac.WhiteListUsers) > 0
	hasGroupWhitelist := len(ac.WhiteListGroups) > 0

	if !hasUserWhitelist && !hasGroupWhitelist {
		// Both empty: allow all
		return true
	}

	// Mixed mode OR logic: if either user or group is whitelisted, allow.
	if hasUserWhitelist {
		for _, id := range ac.WhiteListUsers {
			if id == userID {
				return true
			}
		}
	}
	if hasGroupWhitelist && groupID > 0 {
		for _, id := range ac.WhiteListGroups {
			if id == groupID {
				return true
			}
		}
	}

	// Has whitelist but no match
	return false
}

func (ac AccessControl) IsEmpty() bool {
	return len(ac.WhiteListUsers) == 0 && len(ac.BlackListUsers) == 0 &&
		len(ac.WhiteListGroups) == 0 && len(ac.BlackListGroups) == 0
}

// PluginControl stores host-side enable/disable state.
type PluginControl struct {
	Disabled         bool                     `json:"disabled,omitempty"`
	DisabledCommands []string                 `json:"disabled_commands,omitempty"`
	DisabledEvents   []string                 `json:"disabled_events,omitempty"`
	DisabledCrons    []string                 `json:"disabled_crons,omitempty"`
	Access           AccessControl            `json:"access,omitempty"`
	CommandAccess    map[string]AccessControl `json:"command_access,omitempty"`
	EventAccess      map[string]AccessControl `json:"event_access,omitempty"`
	// CommandPrefix overrides AppConfig.MessagePrefix for this plugin.
	CommandPrefix string `json:"command_prefix,omitempty"`
	// EnableSleep nil=use global, true/false=override
	EnableSleep *bool `json:"enable_sleep,omitempty"`
	// SleepTimeout 0=use global, >0=override
	SleepTimeout int `json:"sleep_timeout,omitempty"`
}

func (pc PluginControl) IsEmpty() bool {
	if pc.Disabled || len(pc.DisabledCommands) > 0 || len(pc.DisabledEvents) > 0 || len(pc.DisabledCrons) > 0 {
		return false
	}
	if !pc.Access.IsEmpty() || len(pc.CommandAccess) > 0 || len(pc.EventAccess) > 0 {
		return false
	}
	if pc.CommandPrefix != "" || (pc.EnableSleep != nil && !*pc.EnableSleep) || (pc.SleepTimeout != 0 && pc.SleepTimeout != 60) {
		return false
	}
	return true
}

type OneBotConfig struct {
	ReverseWS ReverseWSConfig `json:"reverse_ws"`
}

type ReverseWSConfig struct {
	ListenAddr string `json:"listen_addr"`
}

type WebUIConfig struct {
	ListenAddr      string `json:"listen_addr"`
	Password        string `json:"password"`
	AutoRefresh     *bool  `json:"auto_refresh,omitempty"`
	RefreshInterval int    `json:"refresh_interval"`
}

type ChatLogQueueConfig struct {
	Type string `json:"type,omitempty"`
}

type ChatLogConfig struct {
	DatabaseURI string              `json:"database_uri"`
	Queue       *ChatLogQueueConfig `json:"queue,omitempty"`
}

// TriggerLogConfig stores configuration for trigger logging.
type TriggerLogConfig struct {
	Enabled       bool   `json:"enabled"`
	DatabaseURI   string `json:"database_uri"`
	QueueSize     int    `json:"queue_size"`
	BatchSize     int    `json:"batch_size"`
	BatchInterval string `json:"batch_interval"`
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
	WebUI: WebUIConfig{
		ListenAddr:      "0.0.0.0:3000",
		AutoRefresh:     &[]bool{true}[0],
		RefreshInterval: 1,
	},
	MessagePrefix:  defaultMessagePrefix,
	Globals:        make(map[string]string),
	Plugins:        make(map[string]json.RawMessage),
	PluginControls: make(map[string]PluginControl),
	ChatLog:        ChatLogConfig{DatabaseURI: ""},
	TriggerLog: TriggerLogConfig{
		Enabled:       false,
		DatabaseURI:   "",
		QueueSize:     1000,
		BatchSize:     100,
		BatchInterval: "5s",
	},
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
	if cfg.GlobalSleepTimeout <= 0 {
		cfg.GlobalSleepTimeout = 60
		changed = true
	}
	if cfg.WebUI.ListenAddr == "" {
		cfg.WebUI.ListenAddr = "0.0.0.0:3000"
		changed = true
	}
	if cfg.WebUI.AutoRefresh == nil {
		autoRefresh := true
		cfg.WebUI.AutoRefresh = &autoRefresh
		changed = true
	}
	if cfg.WebUI.RefreshInterval <= 0 {
		cfg.WebUI.RefreshInterval = 1
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
	cfg.GlobalAccess = normalizeAccessControl(cfg.GlobalAccess)
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
	// Ensure TriggerLog config has valid defaults.
	if cfg.TriggerLog.QueueSize <= 0 {
		cfg.TriggerLog.QueueSize = 1000
		changed = true
	}
	if cfg.TriggerLog.BatchSize <= 0 {
		cfg.TriggerLog.BatchSize = 100
		changed = true
	}
	if cfg.TriggerLog.BatchInterval == "" {
		cfg.TriggerLog.BatchInterval = "5s"
		changed = true
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

func (c AppConfig) IsAllowed(pluginID, listenerID string, isCommand bool, userID, groupID int64) bool {
	// 1) Global level
	if !c.GlobalAccess.Allowed(userID, groupID) {
		return false
	}

	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return true
	}

	control, ok := c.PluginControls[pluginID]
	if !ok {
		return true
	}

	// 2) Plugin level
	if !control.Access.Allowed(userID, groupID) {
		return false
	}

	// 3) Listener level (Command or Event)
	listenerID = strings.TrimSpace(listenerID)
	if listenerID == "" {
		return true
	}

	if isCommand {
		if ac, ok := control.CommandAccess[listenerID]; ok {
			return ac.Allowed(userID, groupID)
		}
	} else {
		if ac, ok := control.EventAccess[listenerID]; ok {
			return ac.Allowed(userID, groupID)
		}
	}

	return true
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
		control.Access = normalizeAccessControl(control.Access)
		if control.CommandAccess != nil {
			newCA := make(map[string]AccessControl, len(control.CommandAccess))
			for k, v := range control.CommandAccess {
				newCA[strings.TrimSpace(k)] = normalizeAccessControl(v)
			}
			control.CommandAccess = newCA
		}
		if control.EventAccess != nil {
			newEA := make(map[string]AccessControl, len(control.EventAccess))
			for k, v := range control.EventAccess {
				newEA[strings.TrimSpace(k)] = normalizeAccessControl(v)
			}
			control.EventAccess = newEA
		}
		if control.EnableSleep == nil {
			sleep := true
			control.EnableSleep = &sleep
		}
		if control.SleepTimeout <= 0 {
			control.SleepTimeout = 60
		}
		if control.IsEmpty() {
			continue
		}
		out[pluginID] = control
	}
	return out
}

func normalizeAccessControl(ac AccessControl) AccessControl {
	ac.WhiteListUsers = normalizeInt64Slice(ac.WhiteListUsers)
	ac.BlackListUsers = normalizeInt64Slice(ac.BlackListUsers)
	ac.WhiteListGroups = normalizeInt64Slice(ac.WhiteListGroups)
	ac.BlackListGroups = normalizeInt64Slice(ac.BlackListGroups)
	return ac
}

func normalizeInt64Slice(in []int64) []int64 {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]int64, 0, len(in))
	for _, item := range in {
		s := strconv.FormatInt(item, 10)
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
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
		if (leftControl.EnableSleep == nil) != (rightControl.EnableSleep == nil) {
			return false
		}
		if leftControl.EnableSleep != nil && *leftControl.EnableSleep != *rightControl.EnableSleep {
			return false
		}
		if leftControl.SleepTimeout != rightControl.SleepTimeout {
			return false
		}
		if !stringSlicesEqual(leftControl.DisabledCommands, rightControl.DisabledCommands) {
			return false
		}
		if !stringSlicesEqual(leftControl.DisabledEvents, rightControl.DisabledEvents) {
			return false
		}
		if !accessControlEqual(leftControl.Access, rightControl.Access) {
			return false
		}
		if !accessControlMapEqual(leftControl.CommandAccess, rightControl.CommandAccess) {
			return false
		}
		if !accessControlMapEqual(leftControl.EventAccess, rightControl.EventAccess) {
			return false
		}
	}
	return true
}

func accessControlEqual(left, right AccessControl) bool {
	return int64SlicesEqual(left.WhiteListUsers, right.WhiteListUsers) &&
		int64SlicesEqual(left.BlackListUsers, right.BlackListUsers) &&
		int64SlicesEqual(left.WhiteListGroups, right.WhiteListGroups) &&
		int64SlicesEqual(left.BlackListGroups, right.BlackListGroups)
}

func accessControlMapEqual(left, right map[string]AccessControl) bool {
	if len(left) != len(right) {
		return false
	}
	for k, v := range left {
		rv, ok := right[k]
		if !ok || !accessControlEqual(v, rv) {
			return false
		}
	}
	return true
}

func int64SlicesEqual(left, right []int64) bool {
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
