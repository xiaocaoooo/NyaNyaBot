package pluginhost

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	hclog "github.com/hashicorp/go-hclog"
	goplugin "github.com/hashicorp/go-plugin"
	"github.com/xiaocaoooo/nyanyabot/internal/configtmpl"
	"github.com/xiaocaoooo/nyanyabot/internal/onebot/ob11"
	papi "github.com/xiaocaoooo/nyanyabot/internal/plugin"
	"github.com/xiaocaoooo/nyanyabot/internal/plugin/transport"
)

type pluginProcess interface {
	Kill()
}

// TraceRecord 追踪记录，使用抽象数据结构
// Type 字段表示触发类型，Data 字段存储具体数据
type TraceRecord struct {
	TraceID    string
	PluginID   string
	ListenerID string
	Type       string
	Data       map[string]interface{}
	StartTime  time.Time
}

// Host manages out-of-process go-plugin plugins.
// It registers them into the in-process plugin.Manager (as RPC-backed Plugin impls).
type Host struct {
	mu      sync.Mutex
	clients []pluginProcess

	pm              *papi.Manager
	getPluginConfig func() map[string]json.RawMessage
	getGlobals      func() map[string]string

	callOneBot func(ctx context.Context, action string, params any, selfID int64, traceID string) (ob11.APIResponse, error)
	getStats   func(ctx context.Context) (transport.GetStatsReply, error)

	// 追踪系统
	muTrace         sync.RWMutex
	traceRecords    map[string]*TraceRecord
	pluginSentStats map[string]*atomic.Int64
	logger          *slog.Logger
}

func New(pm *papi.Manager, getPluginConfig func() map[string]json.RawMessage, getGlobals func() map[string]string, callOneBot func(ctx context.Context, action string, params any, selfID int64, traceID string) (ob11.APIResponse, error), getStats func(ctx context.Context) (transport.GetStatsReply, error)) *Host {
	if getPluginConfig == nil {
		getPluginConfig = func() map[string]json.RawMessage { return nil }
	}
	if getGlobals == nil {
		getGlobals = func() map[string]string { return nil }
	}
	return &Host{
		pm:              pm,
		getPluginConfig: getPluginConfig,
		getGlobals:      getGlobals,
		callOneBot:      callOneBot,
		getStats:        getStats,
		traceRecords:    make(map[string]*TraceRecord),
		pluginSentStats: make(map[string]*atomic.Int64),
		logger:          slog.Default(),
	}
}

// BeginTrace 开始一个新的追踪记录
func (h *Host) BeginTrace(traceID, pluginID, listenerID, traceType string, data map[string]interface{}) {
	h.muTrace.Lock()
	defer h.muTrace.Unlock()

	h.traceRecords[traceID] = &TraceRecord{
		TraceID:    traceID,
		PluginID:   pluginID,
		ListenerID: listenerID,
		Type:       traceType,
		Data:       data,
		StartTime:  time.Now(),
	}
}

// EndTrace 结束追踪记录
func (h *Host) EndTrace(traceID string) {
	h.muTrace.Lock()
	defer h.muTrace.Unlock()
	delete(h.traceRecords, traceID)
}

// GetTraceRecord 获取追踪记录
func (h *Host) GetTraceRecord(traceID string) (*TraceRecord, bool) {
	h.muTrace.RLock()
	defer h.muTrace.RUnlock()
	r, ok := h.traceRecords[traceID]
	return r, ok
}

// IncPluginSent 增加插件发送计数
func (h *Host) IncPluginSent(pluginID string) {
	h.muTrace.RLock()
	stat, ok := h.pluginSentStats[pluginID]
	h.muTrace.RUnlock()

	if !ok {
		h.muTrace.Lock()
		stat, ok = h.pluginSentStats[pluginID]
		if !ok {
			stat = &atomic.Int64{}
			h.pluginSentStats[pluginID] = stat
		}
		h.muTrace.Unlock()
	}
	stat.Add(1)
}

// GetPluginSentStats 获取插件发送统计
func (h *Host) GetPluginSentStats() map[string]int64 {
	h.muTrace.RLock()
	defer h.muTrace.RUnlock()

	result := make(map[string]int64, len(h.pluginSentStats))
	for pluginID, stat := range h.pluginSentStats {
		result[pluginID] = stat.Load()
	}
	return result
}

// GenerateTraceID 生成唯一的追踪ID
func (h *Host) GenerateTraceID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

type hostAPI struct {
	host           *Host
	callerPluginID string
	call           func(ctx context.Context, action string, params any, selfID int64, traceID string) (ob11.APIResponse, error)
	callDependency func(ctx context.Context, callerPluginID string, targetPluginID string, method string, params json.RawMessage) (json.RawMessage, *papi.StructuredError)
	getStats       func(ctx context.Context) (transport.GetStatsReply, error)
}

func (h hostAPI) CallOneBot(ctx context.Context, action string, params any, selfID int64, traceID string) (ob11.APIResponse, error) {
	if h.call == nil {
		return ob11.APIResponse{}, errors.New("host onebot callback is not configured")
	}

	// 如果有活跃的 TraceID，记录追踪信息
	if traceID != "" && h.host != nil {
		if record, ok := h.host.GetTraceRecord(traceID); ok {
			h.host.logger.Info("plugin CallOneBot",
				"trace_id", traceID,
				"plugin_id", h.callerPluginID,
				"listener_id", record.ListenerID,
				"action", action,
				"self_id", selfID,
				"type", record.Type,
			)
		}
		// 增加插件发送统计
		h.host.IncPluginSent(h.callerPluginID)
	}

	return h.call(ctx, action, params, selfID, traceID)
}

func (h hostAPI) CallDependency(ctx context.Context, targetPluginID string, method string, params json.RawMessage) (json.RawMessage, *papi.StructuredError) {
	if h.callDependency == nil {
		return nil, papi.NewStructuredError(papi.ErrorCodeInternal, "host dependency callback is not configured")
	}
	return h.callDependency(ctx, h.callerPluginID, targetPluginID, method, params)
}

func (h hostAPI) GetStats(ctx context.Context) (transport.GetStatsReply, error) {
	if h.getStats == nil {
		return transport.GetStatsReply{}, errors.New("host getStats callback is not configured")
	}
	return h.getStats(ctx)
}

func (h *Host) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.clients {
		c.Kill()
	}
	h.clients = nil
}

func (h *Host) callDependency(ctx context.Context, callerPluginID string, targetPluginID string, method string, params json.RawMessage) (json.RawMessage, *papi.StructuredError) {
	if h.pm == nil {
		return nil, papi.NewStructuredError(papi.ErrorCodeInternal, "plugin manager is not configured")
	}
	return h.pm.CallDependency(ctx, callerPluginID, targetPluginID, method, params)
}

// LoadExec starts a plugin executable and registers it.
func (h *Host) LoadExec(ctx context.Context, exePath string) error {
	candidate, err := h.startExecutable(ctx, exePath)
	if err != nil {
		return err
	}
	return h.loadStartedCandidates(ctx, []*loadedCandidate{candidate})
}

// LoadDir loads all executable plugin files under dir.
// Convention: files starting with "nyanyabot-plugin-".
func (h *Host) LoadDir(ctx context.Context, dir string) error {
	execPaths, err := discoverPluginExecutables(dir)
	if err != nil {
		return err
	}
	if len(execPaths) == 0 {
		return nil
	}

	candidates := make([]*loadedCandidate, 0, len(execPaths))
	var loadErrs []error

	// Phase 1: start all plugin processes first.
	for _, exePath := range execPaths {
		c, err := h.startExecutable(ctx, exePath)
		if err != nil {
			loadErrs = append(loadErrs, fmt.Errorf("start plugin %q: %w", exePath, err))
			continue
		}
		candidates = append(candidates, c)
	}

	if len(candidates) == 0 {
		return errors.Join(loadErrs...)
	}

	if err := h.loadStartedCandidates(ctx, candidates); err != nil {
		loadErrs = append(loadErrs, err)
	}
	return errors.Join(loadErrs...)
}

type loadedCandidate struct {
	exePath string
	client  pluginProcess
	plugin  *transport.PluginRPCClient
	desc    papi.Descriptor
}

func (h *Host) startExecutable(ctx context.Context, exePath string) (*loadedCandidate, error) {
	_ = ctx
	if strings.TrimSpace(exePath) == "" {
		return nil, errors.New("exePath is empty")
	}
	abs, err := filepath.Abs(exePath)
	if err != nil {
		abs = exePath
	}

	// go-plugin 会把无法解析级别的插件 stderr 行降级为 Debug；这里保持 Debug，避免业务日志被静默丢掉。
	logger := hclog.New(&hclog.LoggerOptions{Name: "plugin", Level: hclog.Debug, Output: os.Stderr})
	client := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig:  transport.Handshake(),
		Plugins:          goplugin.PluginSet{transport.PluginName: &transport.Map{}},
		Cmd:              exec.Command(abs),
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolNetRPC},
		Logger:           logger,
		StartTimeout:     10 * time.Second,
		AutoMTLS:         false,
	})

	rpcClient, err := client.Client()
	if err != nil {
		client.Kill()
		return nil, err
	}

	raw, err := rpcClient.Dispense(transport.PluginName)
	if err != nil {
		client.Kill()
		return nil, err
	}

	p, ok := raw.(*transport.PluginRPCClient)
	if !ok {
		client.Kill()
		return nil, fmt.Errorf("unexpected plugin type: %T", raw)
	}

	return &loadedCandidate{exePath: abs, client: client, plugin: p}, nil
}

func (h *Host) loadStartedCandidates(ctx context.Context, candidates []*loadedCandidate) error {
	var errs []error
	if len(candidates) == 0 {
		return nil
	}
	if h.pm == nil {
		for _, c := range candidates {
			if c != nil && c.client != nil {
				c.client.Kill()
			}
		}
		return errors.New("plugin manager is not configured")
	}

	existing := make(map[string]struct{})
	for id := range h.pm.Entries() {
		existing[id] = struct{}{}
	}

	byID := make(map[string]*loadedCandidate, len(candidates))

	// Phase 2: read descriptors and static validate.
	for _, c := range candidates {
		if err := probeInvokeCompatibility(ctx, c.plugin); err != nil {
			errs = append(errs, fmt.Errorf("plugin %q incompatible invoke protocol: %w", c.exePath, err))
			c.client.Kill()
			continue
		}

		desc, err := c.plugin.Descriptor(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("plugin %q describe failed: %w", c.exePath, err))
			c.client.Kill()
			continue
		}
		desc, err = papi.ValidateDescriptor(desc)
		if err != nil {
			errs = append(errs, fmt.Errorf("plugin %q invalid descriptor: %w", c.exePath, err))
			c.client.Kill()
			continue
		}
		if _, exists := existing[desc.PluginID]; exists {
			errs = append(errs, fmt.Errorf("plugin %q duplicate plugin_id already loaded: %s", c.exePath, desc.PluginID))
			c.client.Kill()
			continue
		}
		if _, exists := byID[desc.PluginID]; exists {
			errs = append(errs, fmt.Errorf("plugin %q duplicate plugin_id in batch: %s", c.exePath, desc.PluginID))
			c.client.Kill()
			continue
		}
		c.desc = desc
		byID[desc.PluginID] = c
	}

	if len(byID) == 0 {
		return errors.Join(errs...)
	}

	descs := make(map[string]papi.Descriptor, len(byID))
	for id, c := range byID {
		descs[id] = c.desc
	}

	order, rejected := resolveDependencyOrder(descs, existing)
	rejectCandidates(byID, rejected)
	for pluginID, reason := range rejected {
		errs = append(errs, fmt.Errorf("plugin %q rejected: %s", pluginID, reason))
	}

	loaded := make(map[string]struct{}, len(existing)+len(order))
	for id := range existing {
		loaded[id] = struct{}{}
	}

	// Phase 3 & 4: topo-ordered register + configure.
	for _, pluginID := range order {
		c, ok := byID[pluginID]
		if !ok {
			continue
		}
		if !depsReady(c.desc.Dependencies, loaded) {
			errs = append(errs, fmt.Errorf("plugin %q skipped: dependency failed during registration", pluginID))
			c.client.Kill()
			continue
		}

		api := hostAPI{
			host:           h,
			callerPluginID: pluginID,
			call:           h.callOneBot,
			callDependency: h.callDependency,
			getStats:       h.getStats,
		}
		if err := c.plugin.AttachHost(ctx, api); err != nil {
			errs = append(errs, fmt.Errorf("plugin %q attach host failed: %w", pluginID, err))
			c.client.Kill()
			continue
		}

		if _, err := h.pm.RegisterWithDescriptor(ctx, c.plugin, c.desc); err != nil {
			errs = append(errs, fmt.Errorf("plugin %q register failed: %w", pluginID, err))
			c.client.Kill()
			continue
		}

		h.pushPluginConfig(ctx, c.plugin, pluginID)

		h.mu.Lock()
		h.clients = append(h.clients, c.client)
		h.mu.Unlock()
		loaded[pluginID] = struct{}{}
	}

	return errors.Join(errs...)
}

func (h *Host) pushPluginConfig(ctx context.Context, p *transport.PluginRPCClient, pluginID string) {
	if h == nil || p == nil || h.getPluginConfig == nil {
		return
	}
	cfgs := h.getPluginConfig()
	if cfgs == nil {
		return
	}

	globals := map[string]string(nil)
	if h.getGlobals != nil {
		globals = h.getGlobals()
	}
	if cfg, ok := cfgs[pluginID]; ok {
		if patched, err := configtmpl.Apply(cfg, globals); err == nil {
			_ = p.Configure(ctx, patched)
		} else {
			// Fallback: if templating fails, still pass raw config.
			_ = p.Configure(ctx, cfg)
		}
		return
	}

	// Always call Configure with empty object so plugin can reset.
	_ = p.Configure(ctx, json.RawMessage("{}"))
}

func discoverPluginExecutables(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	paths := make([]string, 0, len(entries))
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
		paths = append(paths, filepath.Join(dir, name))
	}
	sort.Strings(paths)
	return paths, nil
}

func probeInvokeCompatibility(ctx context.Context, p *transport.PluginRPCClient) error {
	if p == nil {
		return errors.New("plugin rpc client is nil")
	}
	_, err := p.Invoke(ctx, "__nyanyabot_probe__", json.RawMessage("{}"), "__host_probe__")
	if err == nil {
		return nil
	}
	if papi.AsStructuredError(err) != nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "can't find method") && strings.Contains(msg, "Plugin.Invoke") {
		return errors.New("plugin does not implement Invoke")
	}
	if strings.Contains(msg, "method") && strings.Contains(msg, "Invoke") && strings.Contains(msg, "not found") {
		return errors.New("plugin does not implement Invoke")
	}
	return err
}

func depsReady(deps []string, loaded map[string]struct{}) bool {
	for _, dep := range deps {
		if _, ok := loaded[dep]; !ok {
			return false
		}
	}
	return true
}

func resolveDependencyOrder(descs map[string]papi.Descriptor, alreadyLoaded map[string]struct{}) ([]string, map[string]string) {
	rejected := make(map[string]string)
	if len(descs) == 0 {
		return nil, rejected
	}

	if alreadyLoaded == nil {
		alreadyLoaded = map[string]struct{}{}
	}

	// Propagate missing/failed dependencies first.
	for {
		changed := false
		for pluginID, desc := range descs {
			if _, failed := rejected[pluginID]; failed {
				continue
			}
			for _, dep := range desc.Dependencies {
				if _, ok := descs[dep]; ok {
					if _, depFailed := rejected[dep]; depFailed {
						rejected[pluginID] = fmt.Sprintf("dependency %q is unavailable", dep)
						changed = true
						break
					}
					continue
				}
				if _, ok := alreadyLoaded[dep]; ok {
					continue
				}
				rejected[pluginID] = fmt.Sprintf("missing dependency %q", dep)
				changed = true
				break
			}
		}
		if !changed {
			break
		}
	}

	active := make(map[string]struct{}, len(descs))
	for pluginID := range descs {
		if _, failed := rejected[pluginID]; !failed {
			active[pluginID] = struct{}{}
		}
	}

	if len(active) == 0 {
		return nil, rejected
	}

	indegree := make(map[string]int, len(active))
	edges := make(map[string][]string, len(active))
	for pluginID := range active {
		indegree[pluginID] = 0
	}
	for pluginID := range active {
		desc := descs[pluginID]
		for _, dep := range desc.Dependencies {
			if _, ok := active[dep]; !ok {
				continue
			}
			edges[dep] = append(edges[dep], pluginID)
			indegree[pluginID]++
		}
	}

	queue := make([]string, 0, len(active))
	for pluginID, deg := range indegree {
		if deg == 0 {
			queue = append(queue, pluginID)
		}
	}
	sort.Strings(queue)

	order := make([]string, 0, len(active))
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		order = append(order, current)

		for _, next := range edges[current] {
			indegree[next]--
			if indegree[next] == 0 {
				queue = append(queue, next)
			}
		}
		sort.Strings(queue)
	}

	if len(order) < len(active) {
		inOrder := make(map[string]struct{}, len(order))
		for _, id := range order {
			inOrder[id] = struct{}{}
		}
		for pluginID := range active {
			if _, ok := inOrder[pluginID]; ok {
				continue
			}
			rejected[pluginID] = "cyclic dependency detected"
		}
	}

	finalOrder := make([]string, 0, len(order))
	for _, id := range order {
		if _, failed := rejected[id]; failed {
			continue
		}
		finalOrder = append(finalOrder, id)
	}
	return finalOrder, rejected
}

func rejectCandidates(byID map[string]*loadedCandidate, rejected map[string]string) {
	for pluginID := range rejected {
		c, ok := byID[pluginID]
		if !ok {
			continue
		}
		if c.client != nil {
			c.client.Kill()
		}
		delete(byID, pluginID)
	}
}
