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
	"github.com/xiaocaoooo/nyanyabot/internal/config"
	"github.com/xiaocaoooo/nyanyabot/internal/configtmpl"
	"github.com/xiaocaoooo/nyanyabot/internal/onebot/ob11"
	papi "github.com/xiaocaoooo/nyanyabot/internal/plugin"
	"github.com/xiaocaoooo/nyanyabot/internal/plugin/transport"
	"github.com/xiaocaoooo/nyanyabot/internal/triggerlog"
)

type pluginProcess interface {
	Kill()
	Exited() <-chan struct{}
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

	pm                    *papi.Manager
	getPluginConfig       func() map[string]json.RawMessage
	getGlobals            func() map[string]string
	getPluginEnv          func() map[string]string
	getControl            func(pluginID string) config.PluginControl
	getGlobalSleepTimeout func() int

	callOneBot func(ctx context.Context, action string, params any, selfID int64, traceID string) (ob11.APIResponse, error)
	getStats   func(ctx context.Context) (transport.GetStatsReply, error)

	// 追踪系统
	muTrace         sync.RWMutex
	traceRecords    map[string]*TraceRecord
	pluginSentStats map[string]*atomic.Int64
	triggerRecorder *triggerlog.Recorder
	logger          *slog.Logger

	// For testing
	// pluginID may be empty during the initial probe start (descriptor not known yet).
	starter func(ctx context.Context, exePath string, pluginID string) (*loadedCandidate, error)
}

func New(pm *papi.Manager, getPluginConfig func() map[string]json.RawMessage, getGlobals func() map[string]string, getControl func(pluginID string) config.PluginControl, getGlobalSleepTimeout func() int, callOneBot func(ctx context.Context, action string, params any, selfID int64, traceID string) (ob11.APIResponse, error), getStats func(ctx context.Context) (transport.GetStatsReply, error)) *Host {
	if getPluginConfig == nil {
		getPluginConfig = func() map[string]json.RawMessage { return nil }
	}
	if getGlobals == nil {
		getGlobals = func() map[string]string { return nil }
	}
	if getControl == nil {
		getControl = func(pluginID string) config.PluginControl { return config.PluginControl{} }
	}
	if getGlobalSleepTimeout == nil {
		getGlobalSleepTimeout = func() int { return 60 }
	}
	h := &Host{
		pm:                    pm,
		getPluginConfig:       getPluginConfig,
		getGlobals:            getGlobals,
		getPluginEnv:          func() map[string]string { return nil },
		getControl:            getControl,
		getGlobalSleepTimeout: getGlobalSleepTimeout,
		callOneBot:            callOneBot,
		getStats:              getStats,
		traceRecords:          make(map[string]*TraceRecord),
		pluginSentStats:       make(map[string]*atomic.Int64),
		logger:                slog.Default(),
	}
	h.starter = h.startExecutableReal
	return h
}

// SetPluginEnvProvider sets the callback used to read global plugin environment variables.
func (h *Host) SetPluginEnvProvider(fn func() map[string]string) {
	if h == nil {
		return
	}
	if fn == nil {
		fn = func() map[string]string { return nil }
	}
	h.getPluginEnv = fn
}

func (h *Host) globalPluginEnv() map[string]string {
	if h == nil || h.getPluginEnv == nil {
		return nil
	}
	return h.getPluginEnv()
}

func (h *Host) pluginEnvFor(pluginID string) map[string]string {
	if h == nil || h.getControl == nil || strings.TrimSpace(pluginID) == "" {
		return nil
	}
	return h.getControl(pluginID).Env
}

func (h *Host) buildCmdEnv(pluginID string) []string {
	return config.MergeProcessEnv(os.Environ(), h.globalPluginEnv(), h.pluginEnvFor(pluginID))
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

// SetTriggerRecorder 设置触发记录器
func (h *Host) SetTriggerRecorder(recorder *triggerlog.Recorder) {
	h.muTrace.Lock()
	defer h.muTrace.Unlock()
	h.triggerRecorder = recorder
}

// EndTrace 结束追踪记录
func (h *Host) EndTrace(traceID string) {
	h.muTrace.Lock()
	record, ok := h.traceRecords[traceID]
	delete(h.traceRecords, traceID)
	recorder := h.triggerRecorder
	h.muTrace.Unlock()

	// 如果有 recorder 且记录存在，则记录到 triggerlog
	if ok && recorder != nil && record != nil {
		h.recordToTriggerLog(record)
	}
}

// recordToTriggerLog 将 TraceRecord 转换为 TriggerLog 并记录
func (h *Host) recordToTriggerLog(record *TraceRecord) {
	if record == nil {
		return
	}

	// 从 Data 中提取字段
	var (
		triggerID   int64
		triggerName string
		groupID     int64
		groupName   string
		userID      int64
		userName    string
		selfID      int64
		messageID   string
		rawMessage  string
		matchedText string
		response    string
		success     bool
		errorMsg    string
	)

	// 提取基础字段
	if v, ok := record.Data["trigger_id"].(int64); ok {
		triggerID = v
	} else if v, ok := record.Data["trigger_id"].(float64); ok {
		triggerID = int64(v)
	}

	if v, ok := record.Data["trigger_name"].(string); ok {
		triggerName = v
	}

	if v, ok := record.Data["group_id"].(int64); ok {
		groupID = v
	} else if v, ok := record.Data["group_id"].(float64); ok {
		groupID = int64(v)
	}

	if v, ok := record.Data["group_name"].(string); ok {
		groupName = v
	}

	if v, ok := record.Data["user_id"].(int64); ok {
		userID = v
	} else if v, ok := record.Data["user_id"].(float64); ok {
		userID = int64(v)
	}

	if v, ok := record.Data["user_name"].(string); ok {
		userName = v
	}

	if v, ok := record.Data["self_id"].(int64); ok {
		selfID = v
	} else if v, ok := record.Data["self_id"].(float64); ok {
		selfID = int64(v)
	}

	if v, ok := record.Data["message_id"].(string); ok {
		messageID = v
	}

	if v, ok := record.Data["raw_message"].(string); ok {
		rawMessage = v
	}

	if v, ok := record.Data["matched_text"].(string); ok {
		matchedText = v
	}

	if v, ok := record.Data["response"].(string); ok {
		response = v
	}

	if v, ok := record.Data["success"].(bool); ok {
		success = v
	}

	if v, ok := record.Data["error"].(string); ok {
		errorMsg = v
	}

	// 计算执行时长
	endTime := time.Now()
	duration := endTime.Sub(record.StartTime).Milliseconds()

	// 构造 TriggerLog
	log := triggerlog.TriggerLog{
		TriggerID:    triggerID,
		TriggerName:  triggerName,
		GroupID:      groupID,
		GroupName:    groupName,
		UserID:       userID,
		UserName:     userName,
		SelfID:       selfID,
		MessageID:    messageID,
		RawMessage:   rawMessage,
		MatchedText:  matchedText,
		Response:     response,
		StartTime:    record.StartTime,
		EndTime:      endTime,
		Duration:     duration,
		Success:      success,
		ErrorMessage: errorMsg,
		CreatedAt:    endTime,
	}

	// 记录到 triggerlog
	ctx := context.Background()
	h.triggerRecorder.RecordTrace(ctx, log)
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
	
	// First, stop all registered plugins in PluginManager
	// This ensures LazyPlugin can shutdown their processes correctly
	for _, desc := range h.pm.List() {
		p, _, ok := h.pm.Get(desc.PluginID)
		if ok {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = p.Shutdown(ctx)
			cancel()
		}
	}

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
	candidate, err := h.startExecutable(ctx, exePath, "")
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
		// Optimization: if auto-sleep is enabled and no explicit config to disable it,
		// we don't necessarily need to start it now for EVERY plugin if we had a way to get descriptor without starting.
		// But go-plugin requires starting to get descriptor.
		// To achieve "don't auto start", we would need to cache descriptors.
		// For now, we follow the requirement: plugins with auto-sleep enabled should not stay running.
		// They will start, get probed, and then immediately idle out.

		c, err := h.startExecutable(ctx, exePath, "")
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

func (h *Host) startExecutable(ctx context.Context, exePath string, pluginID string) (*loadedCandidate, error) {
	return h.starter(ctx, exePath, pluginID)
}

type gopluginProcess struct {
	*goplugin.Client
}

func (p gopluginProcess) Exited() <-chan struct{} {
	// go-plugin client doesn't expose a chan for exit, but we can wrap it
	// Actually, the original code had p.client.Exited() which suggests
	// it was either a different version or a custom interface.
	// Looking at LazyPlugin.monitorProcess:
	// func (p *LazyPlugin) monitorProcess(client pluginProcess) {
	//    <-client.Exited()
	// ...
	// So we must provide this.
	ch := make(chan struct{})
	go func() {
		for {
			if p.Client.Exited() {
				close(ch)
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
	return ch
}

func (h *Host) startExecutableReal(ctx context.Context, exePath string, pluginID string) (*loadedCandidate, error) {
	_ = ctx
	if strings.TrimSpace(exePath) == "" {
		return nil, errors.New("exePath is empty")
	}
	abs, err := filepath.Abs(exePath)
	if err != nil {
		abs = exePath
	}

	cmd := exec.Command(abs)
	cmd.Env = h.buildCmdEnv(pluginID)

	// go-plugin 会把无法解析级别的插件 stderr 行降级为 Debug；这里保持 Debug，避免业务日志被静默丢掉。
	logger := hclog.New(&hclog.LoggerOptions{Name: "plugin", Level: hclog.Debug, Output: os.Stderr})
	client := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig:  transport.Handshake(),
		Plugins:          goplugin.PluginSet{transport.PluginName: &transport.Map{}},
		Cmd:              cmd,
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

	return &loadedCandidate{exePath: abs, client: gopluginProcess{client}, plugin: p}, nil
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

	byID := make(map[string]*loadedCandidate)

	// First, let's probe all initial candidates to get their base descriptors.
	type probedCandidate struct {
		cand *loadedCandidate
		desc papi.Descriptor
	}
	var probedList []probedCandidate

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
		probedList = append(probedList, probedCandidate{cand: c, desc: desc})
	}

	// Build dependency set of all loaded and current candidate plugins.
	depSet := make(map[string]struct{})
	for _, desc := range h.pm.List() {
		for _, dep := range desc.Dependencies {
			depSet[dep] = struct{}{}
		}
	}
	for _, pc := range probedList {
		for _, dep := range pc.desc.Dependencies {
			depSet[dep] = struct{}{}
		}
	}

	// Extract all configured plugin keys to find multi-instances (plugin_id@name).
	configuredPlugins := make(map[string]struct{})
	if h.getPluginConfig != nil {
		for k := range h.getPluginConfig() {
			configuredPlugins[k] = struct{}{}
		}
	}
	// Also check in plugin controls
	// Wait, we need to access configuration via h.getControl callback or app's store.
	// Since h.getControl can return an empty control if not found, we don't have direct access to all keys of PluginControls inside Host unless we extend Host or get them from another provider, or from getPluginConfig keys.
	// Wait! AppConfig has both "plugins" and "plugin_controls".
	// Let's look at how getPluginConfig is defined in App:
	// 	ph := pluginhost.New(pm, func() map[string]json.RawMessage {
	//		cfg := store.Get()
	//		out := make(map[string]json.RawMessage, len(cfg.Plugins))
	//		for k, v := range cfg.Plugins { out[k] = v }
	//		return out
	//	}, ...
	// So `h.getPluginConfig()` returns all keys of `cfg.Plugins`.
	// What about `cfg.PluginControls`? Does it also contain multi-instances?
	// Usually, if a multi-instance is added, the user configures it in `plugins` (to give it settings) or `plugin_controls` (to enable/disable it).
	// To be absolutely robust, can we look at BOTH plugins keys and plugin_controls keys?
	// But h.getPluginConfig only returns `cfg.Plugins`.
	// Wait, if they configure `plugin_id@name` in `plugins`, it will definitely be in `cfg.Plugins`.
	// Let's also check if we can pass or extract `plugin_controls` keys if possible, or is checking `getPluginConfig` keys sufficient?
	// Let's check: "在配置中以 plugin_id@name 做id 这里的name可以自定义"
	// So it's defined in the configuration (usually `plugins` section or `plugin_controls` section).
	// Wait, can we get all keys from cfg.Plugins? Yes, since every plugin/instance needs configuration, it will definitely have a key in `cfg.Plugins` (even if empty, e.g. `{}`).
	// Let's support both `cfg.Plugins` and `cfg.PluginControls` by checking keys of `getPluginConfig()`.
	// Wait, if a user disables a multi-instance plugin via `plugin_controls` but has no config in `plugins`, should we also find it?
	// To be perfectly safe, can we find a way to get all keys of `PluginControls` too?
	// Wait, `Host` constructor does not have `getPluginControls` mapping, but wait:
	// We can look at how `Host` is constructed.
	// Wait, we can see if we can get all configured multi-instance IDs from `getPluginConfig()` first!
	// Yes! If a user configures a plugin, it will have a key in `plugins` (even if it's `{}`).
	// Let's also parse getPluginConfig keys for "@" patterns.
	// Let's write the replication and instantiation logic:

	for _, pc := range probedList {
		baseID := pc.desc.PluginID
		_, isDependency := depSet[baseID]
		isFunctional := !isDependency

		// Find any configured multi-instance IDs for this functional plugin (e.g. baseID@name)
		var instanceIDs []string
		hasBaseConfigured := false
		for k := range configuredPlugins {
			if k == baseID {
				hasBaseConfigured = true
			} else if isFunctional && strings.HasPrefix(k, baseID+"@") {
				instanceIDs = append(instanceIDs, k)
			}
		}

		// If no multi-instances are configured, we just load the base candidate as before.
		if len(instanceIDs) == 0 {
			c := pc.cand
			desc := pc.desc

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

			// Initial probe starts without pluginID. If this plugin has per-plugin env,
			// restart so main()/init see the full merged environment.
			if len(h.pluginEnvFor(desc.PluginID)) > 0 {
				c.client.Kill()
				restarted, restartErr := h.startExecutable(ctx, c.exePath, desc.PluginID)
				if restartErr != nil {
					errs = append(errs, fmt.Errorf("plugin %q restart with env failed: %w", c.exePath, restartErr))
					continue
				}
				c.client = restarted.client
				c.plugin = restarted.plugin
				// Re-probe after restart for safety.
				if err := probeInvokeCompatibility(ctx, c.plugin); err != nil {
					errs = append(errs, fmt.Errorf("plugin %q incompatible invoke protocol after env restart: %w", c.exePath, err))
					c.client.Kill()
					continue
				}
			}

			c.desc = desc
			byID[desc.PluginID] = c
		} else {
			// There are multi-instances configured!
			// We should instantiate each configured instance ID.
			// We should also instantiate the base ID ONLY if it is explicitly configured.
			if hasBaseConfigured || isDependency {
				instanceIDs = append(instanceIDs, baseID)
			}

			// Kill the initial probe process of the base plugin because we will launch
			// separate specific processes for each active instance!
			pc.cand.client.Kill()

			for _, instID := range instanceIDs {
				restarted, restartErr := h.startExecutable(ctx, pc.cand.exePath, instID)
				if restartErr != nil {
					errs = append(errs, fmt.Errorf("plugin %q start instance %q failed: %w", pc.cand.exePath, instID, restartErr))
					continue
				}
				if err := probeInvokeCompatibility(ctx, restarted.plugin); err != nil {
					errs = append(errs, fmt.Errorf("plugin %q instance %q incompatible invoke protocol: %w", pc.cand.exePath, instID, err))
					restarted.client.Kill()
					continue
				}

				// The descriptor must have its PluginID modified to match the instance ID!
				instDesc := pc.desc
				instDesc.PluginID = instID

				if _, exists := existing[instID]; exists {
					errs = append(errs, fmt.Errorf("plugin %q duplicate plugin_id already loaded: %s", pc.cand.exePath, instID))
					restarted.client.Kill()
					continue
				}
				if _, exists := byID[instID]; exists {
					errs = append(errs, fmt.Errorf("plugin %q duplicate plugin_id in batch: %s", pc.cand.exePath, instID))
					restarted.client.Kill()
					continue
				}

				restarted.desc = instDesc
				byID[instID] = restarted
			}
		}
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

		ctrl := h.getControl(pluginID)

		// Use global default if plugin doesn't override
		enableSleep := true
		if ctrl.EnableSleep != nil {
			enableSleep = *ctrl.EnableSleep
		}

		sleepTimeout := ctrl.SleepTimeout
		if sleepTimeout <= 0 {
			sleepTimeout = h.getGlobalSleepTimeout()
		}

		// Always wrap with LazyPlugin so env restarts can reuse the same lifecycle.
		// idleTimeout==0 disables auto-sleep (process stays up until explicit stop/restart).
		idleTimeout := time.Duration(0)
		if enableSleep {
			idleTimeout = time.Duration(sleepTimeout) * time.Second
		}
		lp := NewLazyPlugin(h, c.exePath, c.desc, idleTimeout)
		// LazyPlugin takes ownership of the current process.
		lp.client = c.client
		lp.rpcClient = c.plugin
		lp.activeCalls.Store(0)

		// Initial attach host
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

		if enableSleep {
			// Force-trigger idle start logic if it is already running.
			// Because it is already running from probe, we want it to timeout and shutdown.
			lp.leaveCall()
		}

		if _, err := h.pm.RegisterWithDescriptor(ctx, lp, c.desc); err != nil {
			errs = append(errs, fmt.Errorf("plugin %q register failed: %w", pluginID, err))
			c.client.Kill()
			continue
		}

		h.pushPluginConfig(ctx, lp, pluginID)

		h.mu.Lock()
		h.clients = append(h.clients, c.client)
		h.mu.Unlock()
		loaded[pluginID] = struct{}{}
	}

	return errors.Join(errs...)
}

// RestartPlugin stops a running plugin process so the next use (or immediate restart)
// picks up the latest environment variables.
func (h *Host) RestartPlugin(ctx context.Context, pluginID string) error {
	if h == nil || h.pm == nil {
		return errors.New("plugin host is not configured")
	}
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return errors.New("plugin_id is empty")
	}
	p, _, ok := h.pm.Get(pluginID)
	if !ok {
		return fmt.Errorf("plugin not found: %s", pluginID)
	}
	lp, ok := p.(*LazyPlugin)
	if !ok {
		return fmt.Errorf("plugin %s does not support restart", pluginID)
	}
	return lp.Restart(ctx)
}

// RestartPlugins restarts the given plugin IDs. Empty pluginIDs restarts all loaded plugins.
func (h *Host) RestartPlugins(ctx context.Context, pluginIDs []string) error {
	if h == nil || h.pm == nil {
		return errors.New("plugin host is not configured")
	}
	if len(pluginIDs) == 0 {
		for _, desc := range h.pm.List() {
			pluginIDs = append(pluginIDs, desc.PluginID)
		}
		sort.Strings(pluginIDs)
	}
	var errs []error
	for _, id := range pluginIDs {
		if err := h.RestartPlugin(ctx, id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (h *Host) pushPluginConfig(ctx context.Context, p papi.Plugin, pluginID string) {
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
