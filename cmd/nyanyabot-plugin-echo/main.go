package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
		Dependencies: []string{
			"external.configdump",
		},
		Exports: []papi.ExportSpec{},
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
				MatchRaw: false,
				Handler:  "HandleEcho",
			},
			{
				Name:        "echo_cfg",
				ID:          "cmd.echo.cfg",
				Description: "调用 external.configdump 导出函数并回显结果",
				Pattern:     `^/?echo_cfg(?:\s+(pretty))?$`,
				MatchRaw:    false,
				Handler:     "HandleEchoCfg",
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

func (e *Echo) Invoke(ctx context.Context, method string, paramsJSON json.RawMessage, callerPluginID string) (json.RawMessage, error) {
	_ = ctx
	_ = method
	_ = paramsJSON
	_ = callerPluginID
	return nil, papi.NewStructuredError(papi.ErrorCodeNotFound, "method is not exported")
}

func (e *Echo) Handle(ctx context.Context, listenerID string, eventRaw ob11.Event, match *papi.CommandMatch) (papi.HandleResult, error) {
	_ = ctx
	switch listenerID {
	case "cmd.echo":
		return e.handleEcho(eventRaw, match)
	case "cmd.echo.cfg":
		return e.handleEchoCfg(eventRaw, match)
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
		content, _ := evt["content"].(string)
		re := regexp.MustCompile(`^/?echo\s+(.+)$`)
		m := re.FindStringSubmatch(content)
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

	sendMessage(host, msgType, evt, text)
	return papi.HandleResult{}, nil
}

func (e *Echo) handleEchoCfg(eventRaw ob11.Event, match *papi.CommandMatch) (papi.HandleResult, error) {
	host := transport.Host()
	if host == nil {
		return papi.HandleResult{}, nil
	}

	var evt map[string]any
	if err := json.Unmarshal(eventRaw, &evt); err != nil {
		return papi.HandleResult{}, nil
	}

	msgType, _ := evt["message_type"].(string)
	pretty := match != nil && len(match.Groups) > 0 && match.Groups[0] == "pretty"

	resultJSON, err := host.CallDependency(context.Background(), "external.configdump", "configdump.snapshot", map[string]any{
		"pretty": pretty,
	})
	if err != nil {
		if serr := papi.AsStructuredError(err); serr != nil {
			sendMessage(host, msgType, evt, fmt.Sprintf("dep call failed: %s (%s)", serr.Message, serr.Code))
		} else {
			sendMessage(host, msgType, evt, "dep call failed: "+err.Error())
		}
		return papi.HandleResult{}, nil
	}

	reply := string(resultJSON)
	if pretty {
		var out bytes.Buffer
		if err := json.Indent(&out, resultJSON, "", "  "); err == nil {
			reply = out.String()
		}
	}
	sendMessage(host, msgType, evt, "dep result: "+reply)
	return papi.HandleResult{}, nil
}

func sendMessage(host *transport.HostRPCClient, msgType string, evt map[string]any, text string) {
	if host == nil || text == "" {
		return
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
