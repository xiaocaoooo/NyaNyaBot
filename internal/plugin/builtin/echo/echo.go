package echo

import (
	"context"
	"encoding/json"
	"regexp"

	"github.com/xiaocaoooo/nyanyabot/internal/onebot/ob11"
	"github.com/xiaocaoooo/nyanyabot/internal/plugin"
)

type Plugin struct {
	CallOneBot plugin.CallOneBotFunc
}

func (p *Plugin) Descriptor(ctx context.Context) (plugin.Descriptor, error) {
	_ = ctx
	return plugin.Descriptor{
		Name:        "Echo",
		PluginID:    "builtin.echo",
		Version:     "0.1.0",
		Author:      "nyanyabot",
		Description: "Echo test plugin",
		Commands: []plugin.CommandListener{
			{
				Name:        "echo",
				ID:          "cmd.echo",
				Description: "匹配 /echo xxx 并回声",
				// Accept both "/echo ..." and "echo ...".
				Pattern:  `^/?echo\s+(.+)$`,
				MatchRaw: true,
				Handler:  "HandleEcho",
			},
		},
		Events: nil,
	}, nil
}

func (p *Plugin) Handle(ctx context.Context, listenerID string, eventRaw ob11.Event, match *plugin.CommandMatch) (plugin.HandleResult, error) {
	switch listenerID {
	case "cmd.echo":
		return p.handleEcho(ctx, eventRaw, match)
	default:
		return plugin.HandleResult{}, nil
	}
}

func (p *Plugin) Shutdown(ctx context.Context) error {
	_ = ctx
	return nil
}

func (p *Plugin) handleEcho(ctx context.Context, eventRaw ob11.Event, match *plugin.CommandMatch) (plugin.HandleResult, error) {
	if p.CallOneBot == nil {
		return plugin.HandleResult{}, nil
	}

	// Extract reply target from event (group_id/user_id + message_type)
	var evt map[string]any
	if err := json.Unmarshal(eventRaw, &evt); err != nil {
		return plugin.HandleResult{}, nil
	}

	msgType, _ := evt["message_type"].(string)
	if msgType == "" {
		// also support post_type-only? ignore
		msgType = ""
	}

	text := ""
	if match != nil {
		if len(match.Groups) > 0 {
			text = match.Groups[0]
		} else {
			text = match.Full
		}
	}
	if text == "" {
		// fallback: try raw_message regexp
		raw, _ := evt["raw_message"].(string)
		re := regexp.MustCompile(`^/?echo\s+(.+)$`)
		m := re.FindStringSubmatch(raw)
		if len(m) >= 2 {
			text = m[1]
		}
	}
	if text == "" {
		return plugin.HandleResult{}, nil
	}

	if msgType == "group" {
		groupID := evt["group_id"]
		_, _ = p.CallOneBot(ctx, "send_group_msg", map[string]any{
			"group_id": groupID,
			"message":  text,
		})
	} else {
		userID := evt["user_id"]
		_, _ = p.CallOneBot(ctx, "send_private_msg", map[string]any{
			"user_id": userID,
			"message": text,
		})
	}
	return plugin.HandleResult{}, nil
}
