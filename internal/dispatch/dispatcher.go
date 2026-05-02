package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/xiaocaoooo/nyanyabot/internal/config"
	"github.com/xiaocaoooo/nyanyabot/internal/onebot/ob11"
	"github.com/xiaocaoooo/nyanyabot/internal/plugin"
	"github.com/xiaocaoooo/nyanyabot/internal/stats"
)

// TraceProvider 提供追踪功能的接口
type TraceProvider interface {
	BeginTrace(traceID, pluginID, listenerID, traceType string, data map[string]interface{})
	EndTrace(traceID string)
	GenerateTraceID() string
}

// TraceIDSetter 允许设置 TraceID 的接口
type TraceIDSetter interface {
	SetTraceID(traceID string)
}

type Dispatcher struct {
	pm            *plugin.Manager
	logger        *slog.Logger
	stats         *stats.Stats
	getConfig     func() config.AppConfig
	traceProvider TraceProvider
}

func New(pm *plugin.Manager) *Dispatcher {
	return &Dispatcher{pm: pm, logger: slog.Default()}
}

func NewWithLogger(pm *plugin.Manager, logger *slog.Logger) *Dispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Dispatcher{pm: pm, logger: logger}
}

func NewWithLoggerAndStats(pm *plugin.Manager, logger *slog.Logger, s *stats.Stats) *Dispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Dispatcher{pm: pm, logger: logger, stats: s}
}

func (d *Dispatcher) SetConfigProvider(fn func() config.AppConfig) {
	d.getConfig = fn
}

func (d *Dispatcher) SetTraceProvider(tp TraceProvider) {
	d.traceProvider = tp
}

// Dispatch routes a raw OneBot event to plugins.
func (d *Dispatcher) Dispatch(ctx context.Context, raw ob11.Event) {
	entries := d.pm.Entries()
	if len(entries) == 0 {
		return
	}

	cfg := config.AppConfig{}
	if d.getConfig != nil {
		cfg = d.getConfig()
	}

	postType := getString(raw, "post_type")
	if postType == "" {
		return
	}

	// 提取消息元数据（用于追踪）
	groupID := getString(raw, "group_id")
	userID := getString(raw, "user_id")
	messageSeq := getString(raw, "message_seq")
	rawMsg := getString(raw, "raw_message")

	// 1) event listeners
	eventKey, eventKeyFull := computeEventKeys(raw)
	for pid, desc := range entries {
		if !cfg.IsPluginEnabled(pid) {
			continue
		}
		p, _, ok := d.pm.Get(pid)
		if !ok {
			continue
		}
		for _, l := range desc.Events {
			if !cfg.IsEventEnabled(pid, l.ID) {
				continue
			}
			if matchEvent(l.Event, eventKey, eventKeyFull) {
				// 生成 TraceID 并注册追踪记录
				traceID := ""
				if d.traceProvider != nil {
					traceID = d.traceProvider.GenerateTraceID()
					traceData := map[string]interface{}{
						"event_type": eventKey,
						"sub_type":   eventKeyFull,
					}
					if userID != "" {
						traceData["user_id"] = userID
					}
					if groupID != "" {
						traceData["group_id"] = groupID
					}
					d.traceProvider.BeginTrace(traceID, pid, l.ID, "event", traceData)
					defer d.traceProvider.EndTrace(traceID)
				}
				// 设置 TraceID（如果插件支持）
				if setter, ok := p.(TraceIDSetter); ok {
					setter.SetTraceID(traceID)
				}
				// no match info
				_, _ = p.Handle(ctx, l.ID, raw, nil)
			}
		}
	}

	// 2) command listeners (message only)
	if postType != "message" {
		return
	}

	// 统计接收的消息数
	if d.stats != nil {
		d.stats.IncRecv()
	}

	// Log: sender + raw_message
	senderInfo := ""
	var obj map[string]any
	if json.Unmarshal(raw, &obj) == nil {
		if sender, ok := obj["sender"].(map[string]any); ok {
			senderInfo, _ = sender["nickname"].(string)
		}
	}

	d.logger.Info("[dispatch] message received",
		"sender", userID,
		"nickname", senderInfo,
		"group_id", groupID,
		"raw_message", rawMsg,
	)

	content := deriveContent(raw)

	for pid, desc := range entries {
		if !cfg.IsPluginEnabled(pid) {
			continue
		}
		p, _, ok := d.pm.Get(pid)
		if !ok {
			continue
		}

		prefixPattern := cfg.MessagePrefix
		if control, ok := cfg.PluginControls[pid]; ok && strings.TrimSpace(control.CommandPrefix) != "" {
			prefixPattern = control.CommandPrefix
		}
		for _, c := range desc.Commands {
			if !cfg.IsCommandEnabled(pid, c.ID) {
				continue
			}
			input := content
			if c.MatchRaw {
				input = rawMsg
			}
			if input == "" {
				d.logger.Info("[dispatch] skipping command (input empty)",
					"plugin_id", pid,
					"command_id", c.ID,
					"match_raw", c.MatchRaw,
					"content", content,
					"rawMsg", rawMsg,
				)
				continue
			}
			strippedInput, matched, err := stripMessagePrefix(input, prefixPattern)
			if err != nil {
				d.logger.Info("[dispatch] prefix regex compile error",
					"plugin_id", pid,
					"command_id", c.ID,
					"message_prefix", prefixPattern,
					"error", err,
				)
				continue
			}
			if !matched {
				d.logger.Info("[dispatch] prefix not matched, skipping command",
					"plugin_id", pid,
					"command_id", c.ID,
					"message_prefix", prefixPattern,
					"input", input,
				)
				continue
			}
			input = strippedInput
			d.logger.Info("[dispatch] trying command",
				"plugin_id", pid,
				"command_id", c.ID,
				"pattern", c.Pattern,
				"input", input,
			)
			re, err := regexp.Compile(c.Pattern)
			if err != nil {
				d.logger.Info("[dispatch] regex compile error",
					"plugin_id", pid,
					"command_id", c.ID,
					"error", err,
				)
				continue
			}
			m := re.FindStringSubmatch(input)
			if len(m) == 0 {
				d.logger.Info("[dispatch] regex no match",
					"plugin_id", pid,
					"command_id", c.ID,
					"input", input,
					"pattern", c.Pattern,
				)
				continue
			}
			d.logger.Info("[dispatch] regex matched!",
				"plugin_id", pid,
				"command_id", c.ID,
				"full_match", m[0],
				"groups", m[1:],
			)
			cm := &plugin.CommandMatch{Full: m[0]}
			if len(m) > 1 {
				cm.Groups = append([]string(nil), m[1:]...)
			}
			d.logger.Info("[dispatch] calling plugin Handle",
				"plugin_id", pid,
				"command_id", c.ID,
			)

			// 生成 TraceID 并注册追踪记录
			traceID := ""
			if d.traceProvider != nil {
				traceID = d.traceProvider.GenerateTraceID()
				traceData := map[string]interface{}{
					"group_id":    groupID,
					"seq":         messageSeq,
					"user_id":     userID,
					"raw_message": rawMsg,
				}
				d.traceProvider.BeginTrace(traceID, pid, c.ID, "message", traceData)
				defer d.traceProvider.EndTrace(traceID)
			}

			// 设置 TraceID（如果插件支持）
			if setter, ok := p.(TraceIDSetter); ok {
				setter.SetTraceID(traceID)
			}

			if _, err := p.Handle(ctx, c.ID, raw, cm); err != nil {
				d.logger.Error("[dispatch] plugin Handle error",
					"plugin_id", pid,
					"command_id", c.ID,
					"error", err,
				)
			}
		}
	}
}

func computeEventKeys(raw ob11.Event) (key string, full string) {
	postType := getString(raw, "post_type")
	key = postType
	full = ""
	suffix := ""
	switch postType {
	case "notice":
		suffix = getString(raw, "notice_type")
	case "meta_event":
		suffix = getString(raw, "meta_event_type")
	case "request":
		suffix = getString(raw, "request_type")
	case "message":
		suffix = getString(raw, "message_type")
	}
	if suffix != "" {
		full = postType + "." + suffix
	}
	return
}

func matchEvent(sel, key, full string) bool {
	if sel == "" {
		return false
	}
	if strings.Contains(sel, ".") {
		return sel == full
	}
	return sel == key
}

func deriveContent(raw ob11.Event) string {
	// If message is string -> itself.
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	m, ok := obj["message"]
	if !ok {
		return ""
	}

	switch v := m.(type) {
	case string:
		return v
	case []any:
		var b strings.Builder
		for _, seg := range v {
			segObj, ok := seg.(map[string]any)
			if !ok {
				continue
			}
			t, _ := segObj["type"].(string)
			if t != "text" {
				continue
			}
			data, ok := segObj["data"].(map[string]any)
			if !ok {
				continue
			}
			text, _ := data["text"].(string)
			b.WriteString(text)
		}
		return b.String()
	default:
		return ""
	}
}

func stripMessagePrefix(msg string, pattern string) (string, bool, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return msg, false, err
	}
	loc := re.FindStringSubmatchIndex(msg)
	if loc == nil || len(loc) < 2 || loc[0] != 0 {
		return msg, false, nil
	}
	m := re.FindStringSubmatch(msg)
	if len(m) == 0 {
		return msg, false, nil
	}
	if idx := re.SubexpIndex("content"); idx >= 0 && idx < len(m) && m[idx] != "" {
		return m[idx], true, nil
	}
	if len(m) >= 2 {
		return strings.TrimPrefix(msg, m[0]), true, nil
	}
	return msg[loc[1]:], true, nil
}

func getString(raw ob11.Event, key string) string {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	v, ok := obj[key]
	if !ok {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		return fmt.Sprintf("%.0f", val)
	default:
		return fmt.Sprintf("%v", val)
	}
}
