package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadOrCreateDefaultGeneratesWebUIPassword(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	cfg, err := store.LoadOrCreateDefault()
	if err != nil {
		t.Fatalf("load or create default: %v", err)
	}
	if cfg.WebUI.Password == "" {
		t.Fatal("expected generated webui password")
	}
	if len(cfg.WebUI.Password) != 24 {
		t.Fatalf("expected password length 24, got %d", len(cfg.WebUI.Password))
	}

	raw, err := os.ReadFile(filepath.Join(filepath.Dir(store.path), "config.json"))
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	var fromDisk AppConfig
	if err := json.Unmarshal(raw, &fromDisk); err != nil {
		t.Fatalf("unmarshal config file: %v", err)
	}
	if fromDisk.WebUI.Password == "" {
		t.Fatal("expected webui password persisted to disk")
	}
}

func TestLoadOrCreateDefaultBackfillsMissingWebUIPassword(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	initial := []byte(`{
  "onebot": {"reverse_ws": {"listen_addr": "0.0.0.0:3001"}},
  "webui": {"listen_addr": "0.0.0.0:3000"}
}`)
	if err := os.WriteFile(store.path, initial, 0o644); err != nil {
		t.Fatalf("write initial config: %v", err)
	}

	cfg, err := store.LoadOrCreateDefault()
	if err != nil {
		t.Fatalf("load existing config: %v", err)
	}
	if cfg.WebUI.Password == "" {
		t.Fatal("expected password to be backfilled")
	}
	if cfg.PluginControls == nil {
		t.Fatal("expected plugin controls to be initialized")
	}

	raw, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	var fromDisk AppConfig
	if err := json.Unmarshal(raw, &fromDisk); err != nil {
		t.Fatalf("unmarshal config file: %v", err)
	}
	if fromDisk.WebUI.Password == "" {
		t.Fatal("expected backfilled password to be saved")
	}
}

func TestPluginControlIsEmptyWithCommandOverrides(t *testing.T) {
	control := PluginControl{
		CommandOverrides: map[string][]Override{
			"global": {{Pattern: `^alias$`, Replacement: "actual"}},
		},
	}

	if control.IsEmpty() {
		t.Fatal("expected plugin control with command overrides to be non-empty")
	}
}

func TestLoadOrCreateDefaultNormalizesPluginControls(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	initial := []byte(`{
  "onebot": {"reverse_ws": {"listen_addr": "0.0.0.0:3001"}},
  "webui": {"listen_addr": "0.0.0.0:3000", "password": "pwd"},
  "plugin_controls": {
    " external.demo ": {
      "disabled_commands": [" cmd.b ", "", "cmd.a", "cmd.a"],
      "disabled_events": ["evt.b", "evt.a", "evt.a"]
    },
    "external.empty": {},
    "": {"disabled": true},
    "external.prefix": {
      "command_prefix": " /plugin"
    },
    "external.overrides": {
      "command_overrides": {
        " global ": [
          {"pattern": " ^别名$ ", "replacement": " 正式 "},
          {"pattern": "", "replacement": "ignored"}
        ],
        "": [{"pattern": "ignored", "replacement": "ignored"}]
      }
    }
  }
}`)
	if err := os.WriteFile(store.path, initial, 0o644); err != nil {
		t.Fatalf("write initial config: %v", err)
	}

	cfg, err := store.LoadOrCreateDefault()
	if err != nil {
		t.Fatalf("load existing config: %v", err)
	}

	control, ok := cfg.PluginControls["external.demo"]
	if !ok {
		t.Fatalf("expected normalized plugin control entry, got %#v", cfg.PluginControls)
	}
	if !reflect.DeepEqual(control.DisabledCommands, []string{"cmd.a", "cmd.b"}) {
		t.Fatalf("unexpected disabled commands: %#v", control.DisabledCommands)
	}
	if !reflect.DeepEqual(control.DisabledEvents, []string{"evt.a", "evt.b"}) {
		t.Fatalf("unexpected disabled events: %#v", control.DisabledEvents)
	}
	if _, ok := cfg.PluginControls["external.empty"]; ok {
		t.Fatalf("expected empty plugin control to be removed: %#v", cfg.PluginControls)
	}
	if cfg.PluginControls["external.prefix"].CommandPrefix != "/plugin" {
		t.Fatalf("expected plugin command prefix to be normalized, got %#v", cfg.PluginControls)
	}
	overridesControl, ok := cfg.PluginControls["external.overrides"]
	if !ok {
		t.Fatalf("expected command-overrides-only plugin control to be kept, got %#v", cfg.PluginControls)
	}
	expectedOverrides := map[string][]Override{
		"global": {{Pattern: `^别名$`, Replacement: "正式"}},
	}
	if !reflect.DeepEqual(overridesControl.CommandOverrides, expectedOverrides) {
		t.Fatalf("unexpected normalized command overrides: %#v", overridesControl.CommandOverrides)
	}
	if cfg.IsPluginEnabled("external.demo") != true {
		t.Fatal("expected plugin to stay enabled when disabled flag is false")
	}
	if cfg.IsCommandEnabled("external.demo", "cmd.a") != false {
		t.Fatal("expected cmd.a to be disabled")
	}
	if cfg.IsEventEnabled("external.demo", "evt.a") != false {
		t.Fatal("expected evt.a to be disabled")
	}

	raw, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	var fromDisk AppConfig
	if err := json.Unmarshal(raw, &fromDisk); err != nil {
		t.Fatalf("unmarshal config file: %v", err)
	}
	if !reflect.DeepEqual(fromDisk.PluginControls, cfg.PluginControls) {
		t.Fatalf("expected normalized plugin controls persisted, got %#v", fromDisk.PluginControls)
	}
}

func TestStoreUpdateKeepsCommandOverridesOnlyPluginControl(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.LoadOrCreateDefault(); err != nil {
		t.Fatalf("load default config: %v", err)
	}

	updated, err := store.Update(func(c *AppConfig) {
		c.PluginControls[" external.overrides "] = PluginControl{
			CommandOverrides: map[string][]Override{
				" cmd.alias ": {{Pattern: ` ^别名$ `, Replacement: " 正式 "}},
			},
		}
	})
	if err != nil {
		t.Fatalf("update store: %v", err)
	}

	control, ok := updated.PluginControls["external.overrides"]
	if !ok {
		t.Fatalf("expected command-overrides-only plugin control to be kept after update, got %#v", updated.PluginControls)
	}
	expectedOverrides := map[string][]Override{
		"cmd.alias": {{Pattern: `^别名$`, Replacement: "正式"}},
	}
	if !reflect.DeepEqual(control.CommandOverrides, expectedOverrides) {
		t.Fatalf("unexpected command overrides after update: %#v", control.CommandOverrides)
	}

	raw, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	var fromDisk AppConfig
	if err := json.Unmarshal(raw, &fromDisk); err != nil {
		t.Fatalf("unmarshal config file: %v", err)
	}
	if !reflect.DeepEqual(fromDisk.PluginControls["external.overrides"].CommandOverrides, expectedOverrides) {
		t.Fatalf("expected command overrides persisted, got %#v", fromDisk.PluginControls["external.overrides"].CommandOverrides)
	}
}

func TestAccessControl(t *testing.T) {
	tests := []struct {
		name    string
		ac      AccessControl
		userID  int64
		groupID int64
		want    bool
	}{
		{
			name:    "empty access control",
			ac:      AccessControl{},
			userID:  123,
			groupID: 456,
			want:    true,
		},
		{
			name:    "user in blacklist",
			ac:      AccessControl{BlackListUsers: []int64{123}},
			userID:  123,
			groupID: 456,
			want:    false,
		},
		{
			name:    "group in blacklist",
			ac:      AccessControl{BlackListGroups: []int64{456}},
			userID:  123,
			groupID: 456,
			want:    false,
		},
		{
			name:    "user in whitelist",
			ac:      AccessControl{WhiteListUsers: []int64{123}},
			userID:  123,
			groupID: 456,
			want:    true,
		},
		{
			name:    "user not in whitelist",
			ac:      AccessControl{WhiteListUsers: []int64{789}},
			userID:  123,
			groupID: 456,
			want:    false,
		},
		{
			name:    "group in whitelist",
			ac:      AccessControl{WhiteListGroups: []int64{456}},
			userID:  123,
			groupID: 456,
			want:    true,
		},
		{
			name:    "group not in whitelist",
			ac:      AccessControl{WhiteListGroups: []int64{789}},
			userID:  123,
			groupID: 456,
			want:    false,
		},
		{
			name:    "mixed whitelist OR - user match",
			ac:      AccessControl{WhiteListUsers: []int64{123}, WhiteListGroups: []int64{789}},
			userID:  123,
			groupID: 456,
			want:    true,
		},
		{
			name:    "mixed whitelist OR - group match",
			ac:      AccessControl{WhiteListUsers: []int64{789}, WhiteListGroups: []int64{456}},
			userID:  123,
			groupID: 456,
			want:    true,
		},
		{
			name:    "mixed whitelist OR - neither match",
			ac:      AccessControl{WhiteListUsers: []int64{789}, WhiteListGroups: []int64{101}},
			userID:  123,
			groupID: 456,
			want:    false,
		},
		{
			name: "blacklist priority over whitelist",
			ac: AccessControl{
				BlackListUsers: []int64{123},
				WhiteListUsers: []int64{123},
			},
			userID:  123,
			groupID: 456,
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ac.Allowed(tt.userID, tt.groupID); got != tt.want {
				t.Errorf("AccessControl.Allowed() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAppConfigIsAllowed(t *testing.T) {
	cfg := AppConfig{
		GlobalAccess: AccessControl{
			BlackListUsers: []int64{999},
		},
		PluginControls: map[string]PluginControl{
			"plugin.test": {
				Access: AccessControl{
					BlackListGroups: []int64{888},
				},
				CommandAccess: map[string]AccessControl{
					"cmd.secret": {
						WhiteListUsers: []int64{123},
					},
				},
			},
		},
	}

	tests := []struct {
		name       string
		pluginID   string
		listenerID string
		isCommand  bool
		userID     int64
		groupID    int64
		want       bool
	}{
		{
			name:       "global blacklist",
			pluginID:   "any",
			listenerID: "any",
			isCommand:  true,
			userID:     999,
			groupID:    100,
			want:       false,
		},
		{
			name:       "plugin blacklist",
			pluginID:   "plugin.test",
			listenerID: "any",
			isCommand:  true,
			userID:     123,
			groupID:    888,
			want:       false,
		},
		{
			name:       "command whitelist - match",
			pluginID:   "plugin.test",
			listenerID: "cmd.secret",
			isCommand:  true,
			userID:     123,
			groupID:    100,
			want:       true,
		},
		{
			name:       "command whitelist - mismatch",
			pluginID:   "plugin.test",
			listenerID: "cmd.secret",
			isCommand:  true,
			userID:     456,
			groupID:    100,
			want:       false,
		},
		{
			name:       "normal command - allow",
			pluginID:   "plugin.test",
			listenerID: "cmd.normal",
			isCommand:  true,
			userID:     456,
			groupID:    100,
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cfg.IsAllowed(tt.pluginID, tt.listenerID, tt.isCommand, tt.userID, tt.groupID); got != tt.want {
				t.Errorf("AppConfig.IsAllowed() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPluginControlIsEmptyWithEnv(t *testing.T) {
	control := PluginControl{
		Env: map[string]string{"FOO": "bar"},
	}
	if control.IsEmpty() {
		t.Fatal("expected plugin control with env to be non-empty")
	}
}

func TestNormalizeStringMapDropsEmptyKeysKeepsEmptyValues(t *testing.T) {
	in := map[string]string{
		"  FOO  ": "bar",
		"":        "drop",
		"   ":     "drop2",
		"EMPTY":   "",
	}
	got := NormalizeStringMap(in)
	want := map[string]string{
		"FOO":   "bar",
		"EMPTY": "",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeStringMap() = %#v, want %#v", got, want)
	}
}

func TestMergeProcessEnvOverrideOrder(t *testing.T) {
	host := []string{"HOST_ONLY=1", "SHARED=host", "ALSO=host"}
	global := map[string]string{"SHARED": "global", "GLOBAL_ONLY": "g", "ALSO": "global"}
	plugin := map[string]string{"SHARED": "plugin", "PLUGIN_ONLY": "p", "EMPTY": ""}

	got := MergeProcessEnv(host, global, plugin)
	env := map[string]string{}
	for _, e := range got {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			t.Fatalf("invalid env entry %q", e)
		}
		env[k] = v
	}

	checks := map[string]string{
		"HOST_ONLY":   "1",
		"SHARED":      "plugin",
		"ALSO":        "global",
		"GLOBAL_ONLY": "g",
		"PLUGIN_ONLY": "p",
		"EMPTY":       "",
	}
	for k, want := range checks {
		if env[k] != want {
			t.Errorf("env[%q]=%q, want %q", k, env[k], want)
		}
	}
}

func TestValidateEnvKey(t *testing.T) {
	if err := ValidateEnvKey("FOO"); err != nil {
		t.Fatalf("valid key rejected: %v", err)
	}
	if err := ValidateEnvKey("  "); err == nil {
		t.Fatal("expected empty key error")
	}
	if err := ValidateEnvKey("A=B"); err == nil {
		t.Fatal("expected '=' key error")
	}
}

func TestLoadOrCreateDefaultInitializesPluginEnv(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	cfg, err := store.LoadOrCreateDefault()
	if err != nil {
		t.Fatalf("load or create default: %v", err)
	}
	if cfg.PluginEnv == nil {
		t.Fatal("expected PluginEnv to be initialized")
	}
}
