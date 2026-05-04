package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	hclog "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"github.com/xiaocaoooo/nyanyabot/internal/onebot/ob11"
	papi "github.com/xiaocaoooo/nyanyabot/internal/plugin"
	"github.com/xiaocaoooo/nyanyabot/internal/plugin/transport"
)

type StatusPlugin struct {
	mu sync.RWMutex
}

func (p *StatusPlugin) Descriptor(ctx context.Context) (papi.Descriptor, error) {
	_ = ctx
	return papi.Descriptor{
		Name:         "Builtin Status",
		PluginID:     "builtin.status",
		Version:      "0.1.0",
		Author:       "nyanyabot",
		Description:  "显示 NyaNyaBot 运行状态",
		Dependencies: []string{},
		Exports:      []papi.ExportSpec{},
		Config:       nil,
		Commands: []papi.CommandListener{
			{
				Name:        "status",
				ID:          "cmd.status",
				Description: "显示机器人状态（收发消息数、运行时间）",
				Pattern:     `(?i)^nyanya(bot)?$`,
				MatchRaw:    false,
				Handler:     "HandleStatus",
			},
		},
	}, nil
}

func (p *StatusPlugin) Configure(ctx context.Context, config json.RawMessage) error {
	return nil
}

func (p *StatusPlugin) Invoke(ctx context.Context, method string, paramsJSON json.RawMessage, callerPluginID string) (json.RawMessage, error) {
	return nil, papi.NewStructuredError(papi.ErrorCodeNotFound, "method is not exported")
}

func (p *StatusPlugin) Handle(ctx context.Context, listenerID string, eventRaw ob11.Event, match *papi.CommandMatch) (papi.HandleResult, error) {
	switch listenerID {
	case "cmd.status":
		return p.handleStatus(ctx, eventRaw, match)
	default:
		return papi.HandleResult{}, nil
	}
}

func (p *StatusPlugin) Shutdown(ctx context.Context) error {
	return nil
}

func (p *StatusPlugin) handleStatus(ctx context.Context, eventRaw ob11.Event, match *papi.CommandMatch) (papi.HandleResult, error) {
	host := transport.Host()
	if host == nil {
		return papi.HandleResult{}, nil
	}

	// 获取统计信息
	statsReply, err := host.GetStats(ctx)
	if err != nil {
		return papi.HandleResult{}, err
	}

	// 解析事件
	var evt map[string]any
	if err := json.Unmarshal(eventRaw, &evt); err != nil {
		return papi.HandleResult{}, nil
	}

	// 格式化回复消息
	reply := fmt.Sprintf("NyaNyaBot\n收/发: %d/%d\n运行时间: %s",
		statsReply.RecvCount,
		statsReply.SentCount,
		statsReply.Uptime,
	)

	// 发送回复
	msgType, _ := evt["message_type"].(string)
	if msgType == "group" {
		groupID := evt["group_id"]
		_, _ = host.CallOneBot(ctx, "send_group_msg", map[string]any{
			"group_id": groupID,
			"message":  reply,
		})
	} else {
		userID := evt["user_id"]
		_, _ = host.CallOneBot(ctx, "send_private_msg", map[string]any{
			"user_id": userID,
			"message": reply,
		})
	}

	return papi.HandleResult{}, nil
}

func main() {
	logger := hclog.New(&hclog.LoggerOptions{Name: "nyanyabot-plugin-builtin-status", Level: hclog.Info})

	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: transport.Handshake(),
		Plugins: plugin.PluginSet{
			transport.PluginName: &transport.Map{PluginImpl: &StatusPlugin{}},
		},
		Logger: logger,
	})
}
