package config

import (
	"encoding/json"
	"os"
	"path/filepath"
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
