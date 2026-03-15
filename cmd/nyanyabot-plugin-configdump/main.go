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

type ConfigDump struct {
	mu        sync.RWMutex
	rawConfig json.RawMessage
	prefix    string
}

func (p *ConfigDump) Descriptor(ctx context.Context) (papi.Descriptor, error) {
	_ = ctx

	schema := json.RawMessage(`{"type":"object","properties":{"prefix":{"type":"string","description":"回复前缀（用于验证热更新是否生效）"}},"additionalProperties":true}`)
	def := json.RawMessage(`{"prefix":"CFG: "}`)

	return papi.Descriptor{
		Name:        "ConfigDump",
		PluginID:    "external.configdump",
		Version:     "0.1.0",
		Author:      "nyanyabot",
		Description: "Test plugin: reply with the current runtime config (hot updated via Configure)",
		Config: &papi.ConfigSpec{
			Version:     "1",
			Description: "ConfigDump plugin config",
			Schema:      schema,
			Default:     def,
		},
		Commands: []papi.CommandListener{
			{
				Name:        "cfg",
				ID:          "cmd.cfg",
				Description: "输入 /cfg 或 /cfg pretty，返回插件当前配置（用于测试热更新）",
				Pattern:     `^/?cfg(?:\s+(pretty))?$`,
				MatchRaw:    true,
				Handler:     "HandleCfg",
			},
		},
	}, nil
}

func (p *ConfigDump) Configure(ctx context.Context, config json.RawMessage) error {
	_ = ctx
	b := json.RawMessage(config)
	if len(b) == 0 {
		b = json.RawMessage("{}")
	}

	var parsed struct {
		Prefix string `json:"prefix"`
	}
	_ = json.Unmarshal(b, &parsed)
	if parsed.Prefix == "" {
		parsed.Prefix = "CFG: "
	}

	p.mu.Lock()
	p.rawConfig = append([]byte(nil), b...)
	p.prefix = parsed.Prefix
	p.mu.Unlock()
	return nil
}

func (p *ConfigDump) Handle(ctx context.Context, listenerID string, eventRaw ob11.Event, match *papi.CommandMatch) (papi.HandleResult, error) {
	_ = ctx
	switch listenerID {
	case "cmd.cfg":
		return p.handleCfg(eventRaw, match)
	default:
		return papi.HandleResult{}, nil
	}
}

func (p *ConfigDump) Shutdown(ctx context.Context) error {
	_ = ctx
	return nil
}

func (p *ConfigDump) handleCfg(eventRaw ob11.Event, match *papi.CommandMatch) (papi.HandleResult, error) {
	host := transport.Host()
	if host == nil {
		return papi.HandleResult{}, nil
	}

	var evt map[string]any
	if err := json.Unmarshal(eventRaw, &evt); err != nil {
		return papi.HandleResult{}, nil
	}

	msgType, _ := evt["message_type"].(string)

	pretty := false
	if match != nil && len(match.Groups) > 0 {
		pretty = match.Groups[0] == "pretty"
	} else {
		raw, _ := evt["raw_message"].(string)
		re := regexp.MustCompile(`^/?cfg(?:\s+(pretty))?$`)
		m := re.FindStringSubmatch(raw)
		if len(m) >= 2 && m[1] == "pretty" {
			pretty = true
		}
	}

	p.mu.RLock()
	prefix := p.prefix
	rawCfg := append([]byte(nil), p.rawConfig...)
	p.mu.RUnlock()
	if len(rawCfg) == 0 {
		rawCfg = []byte("{}")
	}

	cfgText := string(rawCfg)
	if pretty {
		var out bytes.Buffer
		if err := json.Indent(&out, rawCfg, "", "  "); err == nil {
			cfgText = out.String()
		}
	}

	reply := fmt.Sprintf("%sconfig = %s", prefix, cfgText)

	if msgType == "group" {
		groupID := evt["group_id"]
		_, _ = host.CallOneBot(context.Background(), "send_group_msg", map[string]any{
			"group_id": groupID,
			"message":  reply,
		})
	} else {
		userID := evt["user_id"]
		_, _ = host.CallOneBot(context.Background(), "send_private_msg", map[string]any{
			"user_id": userID,
			"message": reply,
		})
	}

	return papi.HandleResult{}, nil
}

func main() {
	logger := hclog.New(&hclog.LoggerOptions{Name: "nyanyabot-plugin-configdump", Level: hclog.Info})

	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: transport.Handshake(),
		Plugins: plugin.PluginSet{
			transport.PluginName: &transport.Map{PluginImpl: &ConfigDump{}},
		},
		Logger: logger,
	})
}
