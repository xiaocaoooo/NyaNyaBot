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

type Dispatcher struct {
	pm        *plugin.Manager
	logger    *slog.Logger
	stats     *stats.Stats
	getConfig func() config.AppConfig
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
	groupID := getString(raw, "group_id")
	senderID := getString(raw, "user_id")
	rawMsg := getString(raw, "raw_message")

	senderInfo := ""
	var obj map[string]any
	if json.Unmarshal(raw, &obj) == nil {
		if sender, ok := obj["sender"].(map[string]any); ok {
			senderInfo, _ = sender["nickname"].(string)
		}
	}

	d.logger.Info("[dispatch] message received",
		"sender", senderID,
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
