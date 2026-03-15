package main

import (
	"context"
	"encoding/json"
	"regexp"
	"sync"

	hclog "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"github.com/xiaocaoooo/nyanyabot/internal/onebot/ob11"
	papi "github.com/xiaocaoooo/nyanyabot/internal/plugin"
	"github.com/xiaocaoooo/nyanyabot/internal/plugin/transport"
)

type Echo struct {
	mu  sync.RWMutex
	cfg struct {
		Prefix string `json:"prefix"`
	}
}

func (e *Echo) Descriptor(ctx context.Context) (papi.Descriptor, error) {
	_ = ctx
	schema := json.RawMessage(`{"type":"object","properties":{"prefix":{"type":"string","description":"echo 回复前缀"}},"additionalProperties":true}`)
	def := json.RawMessage(`{"prefix":""}`)
	return papi.Descriptor{
		Name:        "Echo",
		PluginID:    "external.echo",
		Version:     "0.1.0",
		Author:      "nyanyabot",
		Description: "Echo test plugin (go-plugin)",
		Config: &papi.ConfigSpec{
			Version:     "1",
			Description: "Echo plugin config",
			Schema:      schema,
			Default:     def,
		},
		Commands: []papi.CommandListener{
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
	}, nil
}

func (e *Echo) Configure(ctx context.Context, config json.RawMessage) error {
	_ = ctx
	// Merge with defaults.
	cfg := struct {
		Prefix string `json:"prefix"`
	}{Prefix: ""}
	if len(config) > 0 {
		_ = json.Unmarshal(config, &cfg)
	}
	e.mu.Lock()
	e.cfg.Prefix = cfg.Prefix
	e.mu.Unlock()
	return nil
}

func (e *Echo) Handle(ctx context.Context, listenerID string, eventRaw ob11.Event, match *papi.CommandMatch) (papi.HandleResult, error) {
	_ = ctx
	switch listenerID {
	case "cmd.echo":
		return e.handleEcho(eventRaw, match)
	default:
		return papi.HandleResult{}, nil
	}
}

func (e *Echo) Shutdown(ctx context.Context) error {
	_ = ctx
	return nil
}

func (e *Echo) handleEcho(eventRaw ob11.Event, match *papi.CommandMatch) (papi.HandleResult, error) {
	host := transport.Host()
	if host == nil {
		return papi.HandleResult{}, nil
	}

	var evt map[string]any
	if err := json.Unmarshal(eventRaw, &evt); err != nil {
		return papi.HandleResult{}, nil
	}

	msgType, _ := evt["message_type"].(string)

	text := ""
	if match != nil {
		if len(match.Groups) > 0 {
			text = match.Groups[0]
		} else {
			text = match.Full
		}
	}
	if text == "" {
		raw, _ := evt["raw_message"].(string)
		re := regexp.MustCompile(`^/?echo\s+(.+)$`)
		m := re.FindStringSubmatch(raw)
		if len(m) >= 2 {
			text = m[1]
		}
	}
	if text == "" {
		return papi.HandleResult{}, nil
	}

	e.mu.RLock()
	prefix := e.cfg.Prefix
	e.mu.RUnlock()
	if prefix != "" {
		text = prefix + text
	}

	if msgType == "group" {
		groupID := evt["group_id"]
		_, _ = host.CallOneBot(context.Background(), "send_group_msg", map[string]any{
			"group_id": groupID,
			"message":  text,
		})
	} else {
		userID := evt["user_id"]
		_, _ = host.CallOneBot(context.Background(), "send_private_msg", map[string]any{
			"user_id": userID,
			"message": text,
		})
	}

	return papi.HandleResult{}, nil
}

func main() {
	logger := hclog.New(&hclog.LoggerOptions{Name: "nyanyabot-plugin-echo", Level: hclog.Info})

	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: transport.Handshake(),
		Plugins: plugin.PluginSet{
			transport.PluginName: &transport.Map{PluginImpl: &Echo{}},
		},
		Logger: logger,
	})
}
