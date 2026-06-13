package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xiaocaoooo/nyanyabot/internal/config"
	"github.com/xiaocaoooo/nyanyabot/internal/configtmpl"
	"github.com/xiaocaoooo/nyanyabot/internal/onebot/reversews"
	"github.com/xiaocaoooo/nyanyabot/internal/plugin"
	"github.com/xiaocaoooo/nyanyabot/internal/stats"
	"github.com/xiaocaoooo/nyanyabot/internal/triggerlog"
	"github.com/xiaocaoooo/nyanyabot/internal/util"
)

type pluginConfigPatch struct {
	Config json.RawMessage `json:"config"`
}

type pluginSwitchPatch struct {
	Enabled          *bool                           `json:"enabled,omitempty"`
	Commands         map[string]bool                 `json:"commands,omitempty"`
	Events           map[string]bool                 `json:"events,omitempty"`
	CommandPrefix    *string                         `json:"prefix,omitempty"`
	EnableSleep      *bool                           `json:"enable_sleep,omitempty"`
	SleepTimeout     *int                            `json:"sleep_timeout,omitempty"`
	Access           *config.AccessControl           `json:"access,omitempty"`
	CommandAccess    map[string]config.AccessControl `json:"command_access,omitempty"`
	EventAccess      map[string]config.AccessControl `json:"event_access,omitempty"`
	CommandOverrides map[string][]config.Override    `json:"command_overrides,omitempty"`
}

type pluginStateView struct {
	Enabled          bool                            `json:"enabled"`
	Commands         map[string]bool                 `json:"commands"`
	Events           map[string]bool                 `json:"events"`
	CommandPrefix    string                          `json:"command_prefix"`
	EnableSleep      bool                            `json:"enable_sleep"`
	SleepTimeout     int                             `json:"sleep_timeout"`
	Status           string                          `json:"status"`
	Access           config.AccessControl            `json:"access"`
	CommandAccess    map[string]config.AccessControl `json:"command_access"`
	EventAccess      map[string]config.AccessControl `json:"event_access"`
	CommandOverrides map[string][]config.Override    `json:"command_overrides"`
}

type pluginListItem struct {
	plugin.Descriptor
	State pluginStateView `json:"state"`
}

type Server struct {
	store                    *config.Store
	pm                       *plugin.Manager
	statsProvider            StatsProvider
	reverseWSServer          *reversews.Server
	triggerRecorder          *triggerlog.Recorder
	frontend                 fs.FS
	sessions                 *sessionManager
	onChatLogConfigChange    func(context.Context, config.ChatLogConfig)
	onTriggerLogConfigChange func(context.Context, config.TriggerLogConfig)
}

// StatsProvider 提供统计信息的接口
type StatsProvider interface {
	Snapshot() stats.Snapshot
}

func New(store *config.Store, pm *plugin.Manager) *Server {
	return &Server{store: store, pm: pm, frontend: frontendFS(), sessions: newSessionManager()}
}

func (s *Server) SetStatsProvider(sp StatsProvider) {
	s.statsProvider = sp
}

func (s *Server) SetReverseWSServer(rws *reversews.Server) {
	s.reverseWSServer = rws
}

func (s *Server) SetChatLogConfigChangeHandler(fn func(context.Context, config.ChatLogConfig)) {
	s.onChatLogConfigChange = fn
}

func (s *Server) SetTriggerLogConfigChangeHandler(fn func(context.Context, config.TriggerLogConfig)) {
	s.onTriggerLogConfigChange = fn
}

func (s *Server) SetTriggerRecorder(recorder *triggerlog.Recorder) {
	s.triggerRecorder = recorder
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/auth/login", s.handleAuthLogin)
	mux.HandleFunc("/api/auth/logout", s.handleAuthLogout)
	mux.HandleFunc("/api/auth/status", s.handleAuthStatus)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/globals", s.handleGlobals)
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/api/bots", s.handleBots)
	mux.HandleFunc("/api/plugins", s.handlePlugins)
	mux.HandleFunc("/api/plugins/", s.handlePluginSubAPI)
	mux.HandleFunc("/api/trigger-logs", s.handleTriggerLogs)
	mux.HandleFunc("/api/trigger-logs/stats", s.handleTriggerLogsStats)
	mux.HandleFunc("/api/info", s.handleInfo)

	// Serve exported Next.js static UI for all non-API routes.
	mux.HandleFunc("/", s.handleFrontend)

	return s.authMiddleware(mux)
}

// hotApplyAllPluginConfigs reapplies current globals into each persisted plugin config and calls Configure.
// This is used when globals change so plugins can take effect without editing each plugin config.
func (s *Server) hotApplyAllPluginConfigs(ctx context.Context, cfg config.AppConfig) {
	if s == nil || s.pm == nil {
		return
	}
	for pluginID, raw := range cfg.Plugins {
		p, _, ok := s.pm.Get(pluginID)
		if !ok {
			continue
		}
		patched, err := configtmpl.Apply(raw, cfg.Globals)
		if err != nil {
			patched = raw
		}
		_ = p.Configure(ctx, patched)
	}
}

func buildPluginState(ctx context.Context, pm *plugin.Manager, cfg config.AppConfig, desc plugin.Descriptor) pluginStateView {
	state := pluginStateView{
		Enabled:      cfg.IsPluginEnabled(desc.PluginID),
		Commands:     make(map[string]bool, len(desc.Commands)),
		Events:       make(map[string]bool, len(desc.Events)),
		Status:       "Unknown",
		EnableSleep:  true,
		SleepTimeout: 60,
	}
	if control, ok := cfg.PluginControls[desc.PluginID]; ok {
		state.CommandPrefix = control.CommandPrefix
		state.Access = control.Access
		state.CommandAccess = control.CommandAccess
		state.EventAccess = control.EventAccess
		state.CommandOverrides = control.CommandOverrides
		if control.EnableSleep != nil {
			state.EnableSleep = *control.EnableSleep
		} else {
			state.EnableSleep = true
		}
		if control.SleepTimeout > 0 {
			state.SleepTimeout = control.SleepTimeout
		} else {
			state.SleepTimeout = cfg.GlobalSleepTimeout
		}
	} else {
		state.SleepTimeout = cfg.GlobalSleepTimeout
	}
	// 确保 Commands 和 Events 的遍历顺序确定性
	commandKeys := make([]string, 0, len(desc.Commands))
	for _, command := range desc.Commands {
		commandKeys = append(commandKeys, command.ID)
	}
	sort.Strings(commandKeys)
	for _, commandID := range commandKeys {
		state.Commands[commandID] = cfg.IsCommandEnabled(desc.PluginID, commandID)
	}

	eventKeys := make([]string, 0, len(desc.Events))
	for _, event := range desc.Events {
		eventKeys = append(eventKeys, event.ID)
	}
	sort.Strings(eventKeys)
	for _, eventID := range eventKeys {
		state.Events[eventID] = cfg.IsEventEnabled(desc.PluginID, eventID)
	}

	if p, _, ok := pm.Get(desc.PluginID); ok {
		// Use background context for status check to avoid blocking web requests indefinitely.
		// For LazyPlugin, this status check is safe and doesn't trigger wake-up.
		status, err := p.Status(context.Background())
		if err == nil {
			state.Status = status
		} else {
			state.Status = "Crashed" // If status returns error, assume it's crashed or unreachable
		}
	}

	return state
}

func applyPluginSwitchPatch(control config.PluginControl, patch pluginSwitchPatch) config.PluginControl {
	if patch.Enabled != nil {
		control.Disabled = !*patch.Enabled
	}
	if patch.CommandPrefix != nil {
		control.CommandPrefix = strings.TrimSpace(*patch.CommandPrefix)
	}
	if patch.EnableSleep != nil {
		control.EnableSleep = patch.EnableSleep
	}
	if patch.SleepTimeout != nil {
		control.SleepTimeout = *patch.SleepTimeout
	}
	if patch.Commands != nil {
		control.DisabledCommands = applyListenerSwitches(control.DisabledCommands, patch.Commands)
	}
	if patch.Events != nil {
		control.DisabledEvents = applyListenerSwitches(control.DisabledEvents, patch.Events)
	}
	if patch.Access != nil {
		control.Access = *patch.Access
	}
	if patch.CommandAccess != nil {
		control.CommandAccess = patch.CommandAccess
	}
	if patch.EventAccess != nil {
		control.EventAccess = patch.EventAccess
	}
	if patch.CommandOverrides != nil {
		control.CommandOverrides = patch.CommandOverrides
	}
	return control
}

func applyListenerSwitches(disabled []string, patch map[string]bool) []string {
	disabledSet := make(map[string]struct{}, len(disabled))
	for _, listenerID := range disabled {
		listenerID = strings.TrimSpace(listenerID)
		if listenerID == "" {
			continue
		}
		disabledSet[listenerID] = struct{}{}
	}
	for listenerID, enabled := range patch {
		listenerID = strings.TrimSpace(listenerID)
		if listenerID == "" {
			continue
		}
		if enabled {
			delete(disabledSet, listenerID)
			continue
		}
		disabledSet[listenerID] = struct{}{}
	}
	out := make([]string, 0, len(disabledSet))
	for listenerID := range disabledSet {
		out = append(out, listenerID)
	}
	return out
}

func validateListenerSwitchIDs(desc plugin.Descriptor, commands map[string]bool, events map[string]bool) error {
	if commands != nil {
		allowedCommands := make(map[string]struct{}, len(desc.Commands))
		for _, command := range desc.Commands {
			allowedCommands[command.ID] = struct{}{}
		}
		for listenerID := range commands {
			listenerID = strings.TrimSpace(listenerID)
			if listenerID == "" {
				return errors.New("command listener id is empty")
			}
			if _, ok := allowedCommands[listenerID]; !ok {
				return fmt.Errorf("unknown command listener %q", listenerID)
			}
		}
	}
	if events != nil {
		allowedEvents := make(map[string]struct{}, len(desc.Events))
		for _, event := range desc.Events {
			allowedEvents[event.ID] = struct{}{}
		}
		for listenerID := range events {
			listenerID = strings.TrimSpace(listenerID)
			if listenerID == "" {
				return errors.New("event listener id is empty")
			}
			if _, ok := allowedEvents[listenerID]; !ok {
				return fmt.Errorf("unknown event listener %q", listenerID)
			}
		}
	}
	return nil
}

func (s *Server) handleFrontend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if strings.HasPrefix(r.URL.Path, "/api/") {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	cleaned := path.Clean("/" + r.URL.Path)
	rel := strings.TrimPrefix(cleaned, "/")
	if rel == "" || rel == "." {
		rel = "index.html"
	}

	candidates := []string{rel}
	if !strings.Contains(path.Base(rel), ".") {
		candidates = append(candidates, path.Join(rel, "index.html"), rel+".html")
	}
	// Backward compatibility: old Go WebUI used nested plugin/config routes.
	if strings.HasPrefix(rel, "plugins/") {
		candidates = append(candidates, "plugins/index.html")
	}
	if strings.HasPrefix(rel, "config/") || strings.HasPrefix(rel, "globals") {
		candidates = append(candidates, "config/index.html")
	}

	for _, name := range candidates {
		if s.serveFrontendFile(w, r, name, http.StatusOK) {
			return
		}
	}

	if s.serveFrontendFile(w, r, "404.html", http.StatusNotFound) {
		return
	}
	http.NotFound(w, r)
}

func (s *Server) serveFrontendFile(w http.ResponseWriter, r *http.Request, name string, status int) bool {
	if s == nil || s.frontend == nil {
		return false
	}

	file, err := fs.ReadFile(s.frontend, name)
	if err != nil {
		return false
	}

	contentType := mime.TypeByExtension(path.Ext(name))
	if contentType == "" {
		contentType = http.DetectContentType(file)
	}
	w.Header().Set("content-type", contentType)

	if status != http.StatusOK {
		w.WriteHeader(status)
	}
	if r.Method == http.MethodHead {
		return true
	}
	_, _ = w.Write(file)
	return true
}

func (s *Server) handlePluginConfigAPI(w http.ResponseWriter, r *http.Request, pluginID string) {
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "plugin_id is empty"})
		return
	}
	if _, _, ok := s.pm.Get(pluginID); !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "plugin not found"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		cfg := s.store.Get()
		if v, ok := cfg.Plugins[pluginID]; ok && len(v) > 0 {
			writeJSON(w, http.StatusOK, map[string]any{"plugin_id": pluginID, "config": json.RawMessage(v)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"plugin_id": pluginID, "config": json.RawMessage("{}")})
	case http.MethodPut:
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		var patch pluginConfigPatch
		if err := dec.Decode(&patch); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}

		b := bytes.TrimSpace([]byte(patch.Config))
		if len(b) == 0 {
			b = []byte("{}")
		}
		if b[0] != '{' {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "config must be a JSON object"})
			return
		}
		var tmp any
		if err := json.Unmarshal(b, &tmp); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json: " + err.Error()})
			return
		}

		cfg, err := s.store.Update(func(c *config.AppConfig) {
			if c.Plugins == nil {
				c.Plugins = make(map[string]json.RawMessage)
			}
			c.Plugins[pluginID] = json.RawMessage(b)
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}

		p, _, ok := s.pm.Get(pluginID)
		if ok {
			patched, err := configtmpl.Apply(cfg.Plugins[pluginID], cfg.Globals)
			if err != nil {
				patched = cfg.Plugins[pluginID]
			}
			_ = p.Configure(r.Context(), patched)
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handlePluginSwitchesAPI(w http.ResponseWriter, r *http.Request, pluginID string) {
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "plugin_id is empty"})
		return
	}

	_, desc, ok := s.pm.Get(pluginID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "plugin not found"})
		return
	}
	if r.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var patch pluginSwitchPatch
	if err := dec.Decode(&patch); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if err := validateListenerSwitchIDs(desc, patch.Commands, patch.Events); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	cfg, err := s.store.Update(func(c *config.AppConfig) {
		if c.PluginControls == nil {
			c.PluginControls = make(map[string]config.PluginControl)
		}
		control := c.PluginControls[pluginID]
		c.PluginControls[pluginID] = applyPluginSwitchPatch(control, patch)
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"state": buildPluginState(r.Context(), s.pm, cfg, desc),
	})
}

func (s *Server) handlePluginSubAPI(w http.ResponseWriter, r *http.Request) {
	// /api/plugins/{pluginID}/config
	trimmed := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/plugins/"), "/")
	if trimmed == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	parts := strings.Split(trimmed, "/")
	if len(parts) == 2 && parts[1] == "config" {
		s.handlePluginConfigAPI(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "switches" {
		s.handlePluginSwitchesAPI(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "test-override" {
		s.handleTestOverrideAPI(w, r, parts[0])
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func (s *Server) handleTestOverrideAPI(w http.ResponseWriter, r *http.Request, _ string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	var req struct {
		Input     string                   `json:"input"`
		Overrides []util.Override          `json:"overrides"`
		Commands  []plugin.CommandListener `json:"commands"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	// 1. Apply overrides
	result := util.ApplyOverrides(req.Input, req.Overrides)

	// 2. Check if the result matches any command
	var matchInfo any
	for _, cmd := range req.Commands {
		re, err := regexp.Compile(cmd.Pattern)
		if err != nil {
			continue
		}
		match := re.FindStringSubmatch(result)
		if match == nil {
			continue
		}

		groups := make(map[string]string)
		groupNames := re.SubexpNames()
		for i, name := range groupNames {
			if i != 0 && name != "" && i < len(match) {
				groups[name] = match[i]
			}
		}

		matchInfo = map[string]any{
			"command_id":   cmd.ID,
			"command_name": cmd.Name,
			"groups":       groups,
		}
		break
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"result":     result,
		"match_info": matchInfo,
	})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.statsProvider == nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": "stats not available"})
		return
	}
	snap := s.statsProvider.Snapshot()
	writeJSON(w, http.StatusOK, snap)
}

// BotDTO 是前端友好的 bot 信息响应结构
type BotDTO struct {
	SelfID      int64      `json:"self_id"`
	Nickname    string     `json:"nickname"`
	Online      bool       `json:"online"`
	RemoteAddr  string     `json:"remote_addr"`
	ConnectedAt string     `json:"connected_at"`
	GroupCount  int        `json:"group_count"`
	Groups      []GroupDTO `json:"groups"`
}

// GroupDTO 是前端友好的群组信息响应结构
type GroupDTO struct {
	GroupID        int64  `json:"group_id"`
	GroupName      string `json:"group_name"`
	MemberCount    int    `json:"member_count"`
	MaxMemberCount int    `json:"max_member_count"`
}

// StatsDTO 是前端友好的统计信息响应结构
type StatsDTO struct {
	RecvCount             int64  `json:"recv_count"`
	SentCount             int64  `json:"sent_count"`
	FilteredSelfCount     int64  `json:"filtered_self_count"`
	FilteredNonGroupCount int64  `json:"filtered_non_group_count"`
	DedupCount            int64  `json:"dedup_count"`
	StartTime             string `json:"start_time"`
	Uptime                string `json:"uptime"`
}

// BotsResponse 是 /api/bots 的响应结构
type BotsResponse struct {
	Bots            []BotDTO  `json:"bots"`
	TotalBots       int       `json:"total_bots"`
	OnlineBots      int       `json:"online_bots"`
	TotalGroups     int       `json:"total_groups"`
	GroupChatOnly   bool      `json:"group_chat_only"`
	DedupeKey       string    `json:"dedupe_key"`
	Stats           *StatsDTO `json:"stats,omitempty"`
	GlobalRecvCount int64     `json:"global_recv_count,omitempty"`
	GlobalSentCount int64     `json:"global_sent_count,omitempty"`
	GlobalStartTime string    `json:"global_start_time,omitempty"`
	GlobalUptime    string    `json:"global_uptime,omitempty"`
}

func (s *Server) handleBots(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if s.reverseWSServer == nil {
		writeJSON(w, http.StatusOK, BotsResponse{
			Bots:          []BotDTO{},
			TotalBots:     0,
			OnlineBots:    0,
			TotalGroups:   0,
			GroupChatOnly: true,
			DedupeKey:     "group_id+real_seq",
		})
		return
	}

	// 获取 bot 快照
	botInfos := s.reverseWSServer.GetBots()

	// 转换为前端 DTO
	bots := make([]BotDTO, 0, len(botInfos))
	totalGroups := 0

	for _, info := range botInfos {
		groups := make([]GroupDTO, 0, len(info.Groups))
		for _, g := range info.Groups {
			groups = append(groups, GroupDTO{
				GroupID:        g.GroupID,
				GroupName:      g.GroupName,
				MemberCount:    g.MemberCount,
				MaxMemberCount: g.MaxMemberCount,
			})
		}

		bots = append(bots, BotDTO{
			SelfID:      info.SelfID,
			Nickname:    info.Nickname,
			Online:      true, // 在快照中的都是在线的
			RemoteAddr:  info.RemoteAddr,
			ConnectedAt: info.ConnectedAt.Format("2006-01-02T15:04:05Z07:00"),
			GroupCount:  info.GroupCount,
			Groups:      groups,
		})

		totalGroups += info.GroupCount
	}

	resp := BotsResponse{
		Bots:          bots,
		TotalBots:     len(bots),
		OnlineBots:    len(bots),
		TotalGroups:   totalGroups,
		GroupChatOnly: true,
		DedupeKey:     "group_id+real_seq",
	}

	// 如果有 stats provider，添加全局统计
	if s.statsProvider != nil {
		snap := s.statsProvider.Snapshot()
		// 嵌套的 stats 对象（前端友好）
		resp.Stats = &StatsDTO{
			RecvCount:             snap.RecvCount,
			SentCount:             snap.SentCount,
			FilteredSelfCount:     snap.FilteredSelfCount,
			FilteredNonGroupCount: snap.FilteredNonGroupCount,
			DedupCount:            snap.DedupCount,
			StartTime:             snap.StartTime.Format("2006-01-02T15:04:05Z07:00"),
			Uptime:                snap.Uptime,
		}
		// 保留平铺字段以兼容现有前端
		resp.GlobalRecvCount = snap.RecvCount
		resp.GlobalSentCount = snap.SentCount
		resp.GlobalStartTime = snap.StartTime.Format("2006-01-02T15:04:05Z07:00")
		resp.GlobalUptime = snap.Uptime
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGlobals(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := s.store.Get()
		if cfg.Globals == nil {
			cfg.Globals = map[string]string{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"globals": cfg.Globals})
	case http.MethodPut:
		var patch struct {
			Globals map[string]string `json:"globals"`
		}
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&patch); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}

		cfg, err := s.store.Update(func(c *config.AppConfig) {
			if c.Globals == nil {
				c.Globals = make(map[string]string)
			}
			c.Globals = make(map[string]string, len(patch.Globals))
			keys := make([]string, 0, len(patch.Globals))
			for k := range patch.Globals {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				v := patch.Globals[k]
				k = strings.TrimSpace(k)
				if k == "" {
					continue
				}
				c.Globals[k] = strings.TrimSpace(v)
			}
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		s.hotApplyAllPluginConfigs(r.Context(), cfg)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "globals": cfg.Globals})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handlePlugins(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	fmt.Printf("DEBUG: handlePlugins called\n")
	cfg := s.store.Get()
	plugins := s.pm.List()
	fmt.Printf("DEBUG: plugins count: %d\n", len(plugins))
	sort.Slice(plugins, func(i int, j int) bool {
		if plugins[i].Name == plugins[j].Name {
			return plugins[i].PluginID < plugins[j].PluginID
		}
		return plugins[i].Name < plugins[j].Name
	})
	items := make([]pluginListItem, 0, len(plugins))
	for _, desc := range plugins {
		// 确保所有数组字段非 nil
		plugin.EnsureDescriptorArrays(&desc)

		state := buildPluginState(r.Context(), s.pm, cfg, desc)

		items = append(items, pluginListItem{
			Descriptor: desc,
			State:      state,
		})
	}
	fmt.Printf("DEBUG: about to write JSON, items count: %d\n", len(items))
	if len(items) > 0 {
		fmt.Printf("DEBUG: first item: %+v\n", items[0])
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.store.Get())
	case http.MethodPut:
		var patch struct {
			OneBot *struct {
				ReverseWS *struct {
					ListenAddr *string `json:"listen_addr"`
				} `json:"reverse_ws"`
			} `json:"onebot"`
			WebUI *struct {
				ListenAddr      *string `json:"listen_addr"`
				Password        *string `json:"password"`
				AutoRefresh     *bool   `json:"auto_refresh"`
				RefreshInterval *int    `json:"refresh_interval"`
			} `json:"webui"`
			MessagePrefix      *string `json:"message_prefix"`
			GlobalSleepTimeout *int    `json:"global_sleep_timeout"`
			ChatLog            *struct {
				DatabaseURI *string `json:"database_uri"`
			} `json:"chat_log"`
			TriggerLog *struct {
				Enabled       *bool   `json:"enabled"`
				DatabaseURI   *string `json:"database_uri"`
				QueueSize     *int    `json:"queue_size"`
				BatchSize     *int    `json:"batch_size"`
				BatchInterval *string `json:"batch_interval"`
			} `json:"trigger_log"`
		}
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&patch); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}

		cfg, err := s.store.Update(func(c *config.AppConfig) {
			if patch.OneBot != nil && patch.OneBot.ReverseWS != nil && patch.OneBot.ReverseWS.ListenAddr != nil {
				c.OneBot.ReverseWS.ListenAddr = strings.TrimSpace(*patch.OneBot.ReverseWS.ListenAddr)
			}
			if patch.WebUI != nil {
				if patch.WebUI.ListenAddr != nil {
					c.WebUI.ListenAddr = strings.TrimSpace(*patch.WebUI.ListenAddr)
				}
				if patch.WebUI.Password != nil {
					c.WebUI.Password = strings.TrimSpace(*patch.WebUI.Password)
				}
				if patch.WebUI.AutoRefresh != nil {
					c.WebUI.AutoRefresh = patch.WebUI.AutoRefresh
				}
				if patch.WebUI.RefreshInterval != nil {
					c.WebUI.RefreshInterval = *patch.WebUI.RefreshInterval
				}
			}
			if patch.MessagePrefix != nil {
				c.MessagePrefix = strings.TrimSpace(*patch.MessagePrefix)
			}
			if patch.GlobalSleepTimeout != nil {
				c.GlobalSleepTimeout = *patch.GlobalSleepTimeout
			}
			if patch.ChatLog != nil && patch.ChatLog.DatabaseURI != nil {
				c.ChatLog.DatabaseURI = strings.TrimSpace(*patch.ChatLog.DatabaseURI)
			}
			if patch.TriggerLog != nil {
				if patch.TriggerLog.Enabled != nil {
					c.TriggerLog.Enabled = *patch.TriggerLog.Enabled
				}
				if patch.TriggerLog.DatabaseURI != nil {
					c.TriggerLog.DatabaseURI = strings.TrimSpace(*patch.TriggerLog.DatabaseURI)
				}
				if patch.TriggerLog.QueueSize != nil {
					c.TriggerLog.QueueSize = *patch.TriggerLog.QueueSize
				}
				if patch.TriggerLog.BatchSize != nil {
					c.TriggerLog.BatchSize = *patch.TriggerLog.BatchSize
				}
				if patch.TriggerLog.BatchInterval != nil {
					c.TriggerLog.BatchInterval = strings.TrimSpace(*patch.TriggerLog.BatchInterval)
				}
			}
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		if patch.ChatLog != nil && patch.ChatLog.DatabaseURI != nil && s.onChatLogConfigChange != nil {
			s.onChatLogConfigChange(r.Context(), cfg.ChatLog)
		}
		if patch.TriggerLog != nil && s.onTriggerLogConfigChange != nil {
			// 只要 TriggerLog 有任何变化就触发回调
			if patch.TriggerLog.Enabled != nil || patch.TriggerLog.DatabaseURI != nil ||
				patch.TriggerLog.QueueSize != nil || patch.TriggerLog.BatchSize != nil ||
				patch.TriggerLog.BatchInterval != nil {
				s.onTriggerLogConfigChange(r.Context(), cfg.TriggerLog)
			}
		}
		writeJSON(w, http.StatusOK, cfg)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		fmt.Printf("ERROR: JSON encode failed: %v\n", err)
	}
}

// TriggerLogDTO 触发记录 DTO
type TriggerLogDTO struct {
	TraceID      string                 `json:"trace_id"`
	PluginID     string                 `json:"plugin_id"`
	ListenerID   string                 `json:"listener_id"`
	ListenerType string                 `json:"listener_type"`
	GroupID      int64                  `json:"group_id"`
	UserID       int64                  `json:"user_id"`
	SelfID       int64                  `json:"self_id"`
	MessageID    int64                  `json:"message_id"`
	MessageSeq   string                 `json:"message_seq"`
	TriggerData  map[string]interface{} `json:"trigger_data"`
	Success      bool                   `json:"success"`
	DurationMs   int                    `json:"duration_ms"`
	ErrorMessage string                 `json:"error_message"`
	TriggeredAt  string                 `json:"triggered_at"`
	RecordedAt   string                 `json:"recorded_at"`
}

// TriggerLogsResponse 触发记录查询响应
type TriggerLogsResponse struct {
	Records  []TriggerLogDTO                       `json:"records"`
	Total    int                                   `json:"total"`
	Page     int                                   `json:"page"`
	PageSize int                                   `json:"page_size"`
	Stats    triggerlog.PluginTriggerLogStatistics `json:"stats"`
}

func (s *Server) handleTriggerLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if s.triggerRecorder == nil {
		writeJSON(w, http.StatusOK, TriggerLogsResponse{
			Records:  []TriggerLogDTO{},
			Total:    0,
			Page:     1,
			PageSize: 20,
			Stats: triggerlog.PluginTriggerLogStatistics{
				TotalCount:    0,
				SuccessCount:  0,
				FailedCount:   0,
				AvgDurationMs: 0,
			},
		})
		return
	}

	// 解析查询参数
	query := r.URL.Query()
	params := triggerlog.PluginTriggerLogQueryParams{}

	// 解析 group_id
	if groupIDStr := query.Get("group_id"); groupIDStr != "" {
		var groupID int64
		if _, err := fmt.Sscanf(groupIDStr, "%d", &groupID); err == nil {
			params.GroupID = &groupID
		}
	}

	// 解析 user_id
	if userIDStr := query.Get("user_id"); userIDStr != "" {
		var userID int64
		if _, err := fmt.Sscanf(userIDStr, "%d", &userID); err == nil {
			params.UserID = &userID
		}
	}

	// 解析 plugin_id
	if pluginID := query.Get("plugin_id"); pluginID != "" {
		params.PluginID = &pluginID
	}

	// 解析 listener_id
	if listenerID := query.Get("listener_id"); listenerID != "" {
		params.ListenerID = &listenerID
	}

	// 解析 listener_type
	if listenerType := query.Get("listener_type"); listenerType != "" {
		params.ListenerType = &listenerType
	}

	// 解析 trace_id
	if traceID := query.Get("trace_id"); traceID != "" {
		params.TraceID = &traceID
	}

	// 解析 message_seq
	if messageSeq := query.Get("message_seq"); messageSeq != "" {
		params.MessageSeq = &messageSeq
	}

	// 解析 success
	if successStr := query.Get("success"); successStr != "" {
		if successStr == "true" {
			success := true
			params.Success = &success
		} else if successStr == "false" {
			success := false
			params.Success = &success
		}
	}

	// 解析时间范围
	if startTimeStr := query.Get("start_time"); startTimeStr != "" {
		if startTime, err := time.Parse(time.RFC3339, startTimeStr); err == nil {
			params.StartTime = &startTime
		}
	}

	if endTimeStr := query.Get("end_time"); endTimeStr != "" {
		if endTime, err := time.Parse(time.RFC3339, endTimeStr); err == nil {
			params.EndTime = &endTime
		}
	}

	// 解析排序
	if sortBy := query.Get("sort_by"); sortBy != "" {
		params.OrderBy = sortBy
	}
	params.OrderDesc = query.Get("sort_desc") == "true"

	// 解析分页参数
	page := 1
	if pageStr := query.Get("page"); pageStr != "" {
		if p, err := fmt.Sscanf(pageStr, "%d", &page); err == nil && p > 0 {
			if page < 1 {
				page = 1
			}
		}
	}

	pageSize := 20
	if pageSizeStr := query.Get("page_size"); pageSizeStr != "" {
		if ps, err := fmt.Sscanf(pageSizeStr, "%d", &pageSize); err == nil && ps > 0 {
			if pageSize < 1 {
				pageSize = 1
			} else if pageSize > 100 {
				pageSize = 100
			}
		}
	}

	params.Limit = pageSize
	params.Offset = (page - 1) * pageSize

	// 查询日志
	logs, err := s.triggerRecorder.QueryPluginTriggerLogs(r.Context(), params)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	// 获取统计信息
	stats, err := s.triggerRecorder.GetPluginTriggerLogStatistics(r.Context(), params)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	// 转换为 DTO
	dtos := make([]TriggerLogDTO, 0, len(logs))
	for _, log := range logs {
		dtos = append(dtos, TriggerLogDTO{
			TraceID:      log.TraceID,
			PluginID:     log.PluginID,
			ListenerID:   log.ListenerID,
			ListenerType: log.ListenerType,
			GroupID:      log.GroupID,
			UserID:       log.UserID,
			SelfID:       log.SelfID,
			MessageID:    log.MessageID,
			MessageSeq:   log.MessageSeq,
			TriggerData:  log.TriggerData,
			Success:      log.Success,
			DurationMs:   log.DurationMs,
			ErrorMessage: log.ErrorMessage,
			TriggeredAt:  log.TriggeredAt.Format(time.RFC3339),
			RecordedAt:   log.RecordedAt.Format(time.RFC3339),
		})
	}

	writeJSON(w, http.StatusOK, TriggerLogsResponse{
		Records:  dtos,
		Total:    int(stats.TotalCount),
		Page:     page,
		PageSize: pageSize,
		Stats:    *stats,
	})
}

func (s *Server) handleTriggerLogsStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if s.triggerRecorder == nil {
		writeJSON(w, http.StatusOK, triggerlog.PluginTriggerLogStatistics{
			TotalCount:    0,
			SuccessCount:  0,
			FailedCount:   0,
			AvgDurationMs: 0,
		})
		return
	}

	// 解析查询参数（用于过滤统计）
	query := r.URL.Query()
	params := triggerlog.PluginTriggerLogQueryParams{}

	// 解析 group_id
	if groupIDStr := query.Get("group_id"); groupIDStr != "" {
		var groupID int64
		if _, err := fmt.Sscanf(groupIDStr, "%d", &groupID); err == nil {
			params.GroupID = &groupID
		}
	}

	// 解析 user_id
	if userIDStr := query.Get("user_id"); userIDStr != "" {
		var userID int64
		if _, err := fmt.Sscanf(userIDStr, "%d", &userID); err == nil {
			params.UserID = &userID
		}
	}

	// 解析 plugin_id
	if pluginID := query.Get("plugin_id"); pluginID != "" {
		params.PluginID = &pluginID
	}

	// 解析 listener_id
	if listenerID := query.Get("listener_id"); listenerID != "" {
		params.ListenerID = &listenerID
	}

	// 解析 listener_type
	if listenerType := query.Get("listener_type"); listenerType != "" {
		params.ListenerType = &listenerType
	}

	// 解析时间范围
	if startTimeStr := query.Get("start_time"); startTimeStr != "" {
		if startTime, err := time.Parse(time.RFC3339, startTimeStr); err == nil {
			params.StartTime = &startTime
		}
	}

	if endTimeStr := query.Get("end_time"); endTimeStr != "" {
		if endTime, err := time.Parse(time.RFC3339, endTimeStr); err == nil {
			params.EndTime = &endTime
		}
	}

	// 获取统计信息
	stats, err := s.triggerRecorder.GetPluginTriggerLogStatistics(r.Context(), params)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	idStr := r.URL.Query().Get("id")
	typeStr := r.URL.Query().Get("type")
	if idStr == "" || typeStr == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing id or type"})
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid id"})
		return
	}

	if s.reverseWSServer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "onebot not connected"})
		return
	}

	var name string
	var action string
	var params map[string]any

	if typeStr == "user" {
		action = "get_stranger_info"
		params = map[string]any{"user_id": idStr}
	} else if typeStr == "group" {
		action = "get_group_info"
		params = map[string]any{"group_id": idStr}
	} else {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid type"})
		return
	}

	botIDs := s.reverseWSServer.GetBotIDs()
	if len(botIDs) == 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "onebot not connected"})
		return
	}
	sort.Slice(botIDs, func(i, j int) bool { return botIDs[i] < botIDs[j] })

	selectedSelfID := botIDs[0]
	if selfIDStr := r.URL.Query().Get("self_id"); selfIDStr != "" {
		selfID, err := strconv.ParseInt(selfIDStr, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid self_id"})
			return
		}
		selectedSelfID = selfID
	} else if typeStr == "group" {
		foundGroupBot := false
		for _, bot := range s.reverseWSServer.GetBots() {
			for _, group := range bot.Groups {
				if group.GroupID == id {
					selectedSelfID = bot.SelfID
					foundGroupBot = true
					break
				}
			}
			if foundGroupBot {
				break
			}
		}
	}

	resp, err := s.reverseWSServer.CallWithBot(r.Context(), selectedSelfID, action, params)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	if resp.Status != "ok" {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": resp.Msg})
		return
	}

	if typeStr == "user" {
		var data struct {
			Nickname string `json:"nickname"`
		}
		if err := json.Unmarshal(resp.Data, &data); err == nil {
			name = data.Nickname
		}
	} else {
		var data struct {
			GroupName string `json:"group_name"`
		}
		if err := json.Unmarshal(resp.Data, &data); err == nil {
			name = data.GroupName
		}
	}

	if name == "" {
		name = idStr
	}

	writeJSON(w, http.StatusOK, map[string]string{"name": name})
}
