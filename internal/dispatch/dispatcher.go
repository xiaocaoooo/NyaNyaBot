package dispatch

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/xiaocaoooo/nyanyabot/internal/onebot/ob11"
	"github.com/xiaocaoooo/nyanyabot/internal/plugin"
)

type Dispatcher struct {
	pm *plugin.Manager
}

func New(pm *plugin.Manager) *Dispatcher {
	return &Dispatcher{pm: pm}
}

// Dispatch routes a raw OneBot event to plugins.
func (d *Dispatcher) Dispatch(ctx context.Context, raw ob11.Event) {
	entries := d.pm.Entries()
	if len(entries) == 0 {
		return
	}

	postType := getString(raw, "post_type")
	if postType == "" {
		return
	}

	// 1) event listeners
	eventKey, eventKeyFull := computeEventKeys(raw)
	for pid, desc := range entries {
		p, _, ok := d.pm.Get(pid)
		if !ok {
			continue
		}
		for _, l := range desc.Events {
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

	content := deriveContent(raw)
	rawMsg := getString(raw, "raw_message")

	for pid, desc := range entries {
		p, _, ok := d.pm.Get(pid)
		if !ok {
			continue
		}

		for _, c := range desc.Commands {
			input := content
			if c.MatchRaw {
				input = rawMsg
			}
			if input == "" {
				continue
			}
			re, err := regexp.Compile(c.Pattern)
			if err != nil {
				continue
			}
			m := re.FindStringSubmatch(input)
			if len(m) == 0 {
				continue
			}
			cm := &plugin.CommandMatch{Full: m[0]}
			if len(m) > 1 {
				cm.Groups = append([]string(nil), m[1:]...)
			}
			_, _ = p.Handle(ctx, c.ID, raw, cm)
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
	s, _ := v.(string)
	return s
}
