package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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
