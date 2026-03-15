package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	hclog "github.com/hashicorp/go-hclog"
	goplugin "github.com/hashicorp/go-plugin"
	"github.com/xiaocaoooo/nyanyabot/internal/configtmpl"
	"github.com/xiaocaoooo/nyanyabot/internal/onebot/ob11"
	papi "github.com/xiaocaoooo/nyanyabot/internal/plugin"
	"github.com/xiaocaoooo/nyanyabot/internal/plugin/transport"
)

// Host manages out-of-process go-plugin plugins.
// It registers them into the in-process plugin.Manager (as RPC-backed Plugin impls).
type Host struct {
	mu      sync.Mutex
	clients []*goplugin.Client

	pm              *papi.Manager
	getPluginConfig func() map[string]json.RawMessage
	getGlobals      func() map[string]string

	callOneBot func(ctx context.Context, action string, params any) (ob11.APIResponse, error)
}

func New(pm *papi.Manager, getPluginConfig func() map[string]json.RawMessage, getGlobals func() map[string]string, callOneBot func(ctx context.Context, action string, params any) (ob11.APIResponse, error)) *Host {
	if getPluginConfig == nil {
		getPluginConfig = func() map[string]json.RawMessage { return nil }
	}
	if getGlobals == nil {
		getGlobals = func() map[string]string { return nil }
	}
	return &Host{pm: pm, getPluginConfig: getPluginConfig, getGlobals: getGlobals, callOneBot: callOneBot}
}

type hostAPI struct {
	call func(ctx context.Context, action string, params any) (ob11.APIResponse, error)
}

func (h hostAPI) CallOneBot(ctx context.Context, action string, params any) (ob11.APIResponse, error) {
	return h.call(ctx, action, params)
}

func (h *Host) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.clients {
		c.Kill()
	}
	h.clients = nil
}

// LoadExec starts a plugin executable and registers it.
func (h *Host) LoadExec(ctx context.Context, exePath string) error {
	if exePath == "" {
		return errors.New("exePath is empty")
	}
	abs, err := filepath.Abs(exePath)
	if err != nil {
		abs = exePath
	}

	logger := hclog.New(&hclog.LoggerOptions{Name: "plugin", Level: hclog.Info})
	client := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig:  transport.Handshake(),
		Plugins:          goplugin.PluginSet{transport.PluginName: &transport.Map{Host: hostAPI{call: h.callOneBot}}},
		Cmd:              exec.Command(abs),
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolNetRPC},
		Logger:           logger,
		StartTimeout:     10 * time.Second,
		AutoMTLS:         false,
	})

	rpcClient, err := client.Client()
	if err != nil {
		client.Kill()
		return err
	}

	raw, err := rpcClient.Dispense(transport.PluginName)
	if err != nil {
		client.Kill()
		return err
	}

	p, ok := raw.(*transport.PluginRPCClient)
	if !ok {
		client.Kill()
		return fmt.Errorf("unexpected plugin type: %T", raw)
	}

	desc, err := h.pm.Register(ctx, p)
	if err != nil {
		client.Kill()
		return err
	}

	// Push config right after registration.
	if h.getPluginConfig != nil {
		cfgs := h.getPluginConfig()
		if cfgs != nil {
			globals := map[string]string(nil)
			if h.getGlobals != nil {
				globals = h.getGlobals()
			}
			if cfg, ok := cfgs[desc.PluginID]; ok {
				if patched, err := configtmpl.Apply(cfg, globals); err == nil {
					_ = p.Configure(ctx, patched)
				} else {
					// Fallback: if templating fails, still pass raw config.
					_ = p.Configure(ctx, cfg)
				}
			} else {
				// Always call Configure with empty object so plugin can reset.
				_ = p.Configure(ctx, json.RawMessage("{}"))
			}
		}
	}

	h.mu.Lock()
	h.clients = append(h.clients, client)
	h.mu.Unlock()
	return nil
}

// LoadDir loads all executable plugin files under dir.
// Convention: files starting with "nyanyabot-plugin-".
func (h *Host) LoadDir(ctx context.Context, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) == 0 || name[0] == '.' {
			continue
		}
		if !strings.HasPrefix(name, "nyanyabot-plugin-") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.Mode()&0o111 == 0 {
			continue
		}
		_ = h.LoadExec(ctx, filepath.Join(dir, name))
	}
	return nil
}
