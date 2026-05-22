package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xiaocaoooo/nyanyabot/internal/onebot/ob11"
	papi "github.com/xiaocaoooo/nyanyabot/internal/plugin"
	"github.com/xiaocaoooo/nyanyabot/internal/plugin/transport"
)

// LazyPlugin implements papi.Plugin and manages an out-of-process plugin's lifecycle.
// It starts the plugin process on demand and stops it after a period of inactivity.
type LazyPlugin struct {
	mu sync.Mutex

	pluginID string
	exePath  string
	host     *Host

	// Lifecycle management
	client     pluginProcess
	rpcClient  *transport.PluginRPCClient
	isStarting bool
	startCond  *sync.Cond

	// Cache for restart
	descriptor papi.Descriptor
	lastConfig json.RawMessage

	// Activity tracking
	activeCalls atomic.Int64
	idleTimer   *time.Timer
	idleTimeout time.Duration

	// Monitor
	crashed atomic.Bool
}

func NewLazyPlugin(host *Host, exePath string, desc papi.Descriptor, idleTimeout time.Duration) *LazyPlugin {
	p := &LazyPlugin{
		pluginID:    desc.PluginID,
		exePath:     exePath,
		host:        host,
		descriptor:  desc,
		idleTimeout: idleTimeout,
	}
	p.startCond = sync.NewCond(&p.mu)
	return p
}

func (p *LazyPlugin) Descriptor(ctx context.Context) (papi.Descriptor, error) {
	return p.descriptor, nil
}

func (p *LazyPlugin) Configure(ctx context.Context, config json.RawMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.lastConfig = config
	if p.rpcClient != nil {
		return p.rpcClient.Configure(ctx, config)
	}
	return nil
}

func (p *LazyPlugin) Invoke(ctx context.Context, method string, paramsJSON json.RawMessage, callerPluginID string) (json.RawMessage, error) {
	if err := p.enterCall(ctx); err != nil {
		return nil, err
	}
	defer p.leaveCall()

	p.mu.Lock()
	client := p.rpcClient
	p.mu.Unlock()

	return client.Invoke(ctx, method, paramsJSON, callerPluginID)
}

func (p *LazyPlugin) Handle(ctx context.Context, listenerID string, eventRaw ob11.Event, match *papi.CommandMatch) (papi.HandleResult, error) {
	if err := p.enterCall(ctx); err != nil {
		return papi.HandleResult{}, err
	}
	defer p.leaveCall()

	p.mu.Lock()
	client := p.rpcClient
	p.mu.Unlock()

	return client.Handle(ctx, listenerID, eventRaw, match)
}

func (p *LazyPlugin) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.stopLocked(ctx)
}

// EnsureStarted ensures the plugin process is running.
func (p *LazyPlugin) EnsureStarted(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	for p.isStarting {
		p.startCond.Wait()
	}

	if p.rpcClient != nil {
		return nil
	}

	p.isStarting = true
	defer func() {
		p.isStarting = false
		p.startCond.Broadcast()
	}()

	exePath := p.exePath
	pluginID := p.pluginID

	// Release lock during I/O
	p.mu.Unlock()
	candidate, err := p.host.startExecutable(ctx, exePath)
	p.mu.Lock()

	if err != nil {
		return fmt.Errorf("failed to start plugin %s: %w", pluginID, err)
	}

	// Check again if someone else started it while we were waiting/starting
	if p.rpcClient != nil {
		candidate.client.Kill()
		return nil
	}

	p.client = candidate.client
	p.rpcClient = candidate.plugin
	p.crashed.Store(false)

	// Monitor process exit
	go p.monitorProcess(p.client)

	// Attach host API
	api := hostAPI{
		host:           p.host,
		callerPluginID: p.pluginID,
		call:           p.host.callOneBot,
		callDependency: p.host.callDependency,
		getStats:       p.host.getStats,
	}

	rpcClient := p.rpcClient
	p.mu.Unlock()
	err = rpcClient.AttachHost(ctx, api)
	p.mu.Lock()

	if err != nil {
		p.stopLocked(ctx)
		return fmt.Errorf("failed to attach host to plugin %s: %w", p.pluginID, err)
	}

	// Re-apply configuration
	if p.lastConfig != nil {
		lastConfig := p.lastConfig
		p.mu.Unlock()
		if err := rpcClient.Configure(ctx, lastConfig); err != nil {
			p.host.logger.Warn("failed to re-configure plugin on restart", "plugin_id", p.pluginID, "error", err)
		}
		p.mu.Lock()
	} else {
		// If no config cached, use host's push logic to be safe
		p.mu.Unlock()
		p.host.pushPluginConfig(ctx, rpcClient, p.pluginID)
		p.mu.Lock()
	}

	p.host.logger.Info("lazy plugin started", "plugin_id", p.pluginID)
	return nil
}

func (p *LazyPlugin) enterCall(ctx context.Context) error {
	if err := p.EnsureStarted(ctx); err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.idleTimer != nil {
		p.idleTimer.Stop()
		p.idleTimer = nil
	}

	p.activeCalls.Add(1)
	return nil
}

func (p *LazyPlugin) leaveCall() {
	p.mu.Lock()
	defer p.mu.Unlock()

	newCount := p.activeCalls.Add(-1)
	if newCount < 0 {
		p.host.logger.Error("lazy plugin active calls underflow", "plugin_id", p.pluginID, "count", newCount)
		p.activeCalls.Store(0)
		newCount = 0
	}

	if newCount == 0 {
		if p.idleTimer != nil {
			p.idleTimer.Stop()
		}
		if p.idleTimeout > 0 {
			p.idleTimer = time.AfterFunc(p.idleTimeout, func() {
				p.onIdle()
			})
		}
	}
}

func (p *LazyPlugin) onIdle() {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Double check under lock
	if p.activeCalls.Load() > 0 || p.rpcClient == nil || p.isStarting {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	p.host.logger.Info("lazy plugin idling, stopping process", "plugin_id", p.pluginID)
	if err := p.stopLocked(ctx); err != nil {
		p.host.logger.Error("failed to stop idle plugin", "plugin_id", p.pluginID, "error", err)
	}
}

func (p *LazyPlugin) stopLocked(ctx context.Context) error {
	if p.idleTimer != nil {
		p.idleTimer.Stop()
		p.idleTimer = nil
	}

	rpcClient := p.rpcClient
	client := p.client
	p.client = nil
	p.rpcClient = nil

	if client == nil {
		return nil
	}

	// Try graceful shutdown
	if rpcClient != nil {
		_ = rpcClient.Shutdown(ctx)
	}

	// Kill the process
	client.Kill()
	return nil
}

func (p *LazyPlugin) monitorProcess(client pluginProcess) {
	<-client.Exited()

	p.mu.Lock()
	defer p.mu.Unlock()

	// If client is still the same, it means it crashed unexpectedly
	if p.client == client {
		p.host.logger.Warn("lazy plugin process exited unexpectedly", "plugin_id", p.pluginID)
		p.client = nil
		p.rpcClient = nil
		p.crashed.Store(true)

		// Trigger automatic restart if not idling
		if p.activeCalls.Load() > 0 {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if err := p.EnsureStarted(ctx); err != nil {
					p.host.logger.Error("failed to auto-restart crashed plugin", "plugin_id", p.pluginID, "error", err)
				}
			}()
		}
	}
}

func (p *LazyPlugin) Status(ctx context.Context) (string, error) {
	p.mu.Lock()
	client := p.client
	rpc := p.rpcClient
	p.mu.Unlock()

	if client == nil {
		if p.crashed.Load() {
			return "Crashed", nil
		}
		return "Sleeping", nil
	}

	if rpc != nil {
		if p.activeCalls.Load() == 0 {
			return "Idle", nil
		}
		// Use a short timeout for status check to avoid blocking
		sctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		status, err := rpc.Status(sctx)
		if err == nil && status != "" {
			return status, nil
		}
	}

	return "Running", nil
}
