package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	hclog "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"github.com/xiaocaoooo/nyanyabot/internal/onebot/ob11"
	papi "github.com/xiaocaoooo/nyanyabot/internal/plugin"
	"github.com/xiaocaoooo/nyanyabot/internal/plugin/transport"
)

// CronTimePlugin 是一个定时发送时间的测试插件
type CronTimePlugin struct {
	groupID int64 // 目标群聊ID
}

func (c *CronTimePlugin) Descriptor(ctx context.Context) (papi.Descriptor, error) {
	_ = ctx

	// 定义配置Schema：允许用户配置目标群号
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"group_id": {
				"type": "integer",
				"description": "要发送时间的群聊ID"
			}
		},
		"required": ["group_id"],
		"additionalProperties": false
	}`)
	def := json.RawMessage(`{"group_id": 0}`)

	return papi.Descriptor{
		Name:        "CronTime",
		PluginID:    "external.cron_time",
		Version:     "0.1.0",
		Author:      "nyanyabot",
		Description: "每分钟在特定群聊发送当前时间的测试插件",
		Dependencies:  []string{},
		Exports:       []papi.ExportSpec{},
		Config: &papi.ConfigSpec{
			Version:     "1",
			Description: "CronTime plugin config",
			Schema:      schema,
			Default:     def,
		},
		Commands: []papi.CommandListener{
			{
				Name:        "set_group",
				ID:          "cmd.set_group",
				Description: "设置发送时间的群号：/set_group 123456",
				Pattern:     `^/?set_group\s+(\d+)$`,
				MatchRaw:    true,
				Handler:     "HandleSetGroup",
			},
		},
		Crons: []papi.CronListener{
			{
				Name:        "send_time",
				ID:          "cron.send_time",
				Description: "每分钟发送当前时间",
				Schedule:    "0 * * * * *", // 每分钟执行一次（秒 分时日月周）
				Handler:     "HandleCronTime",
			},
		},
	}, nil
}

func (c *CronTimePlugin) Configure(ctx context.Context, config json.RawMessage) error {
	_ = ctx
	cfg := struct {
		GroupID int64 `json:"group_id"`
	}{GroupID: 0}
	if len(config) > 0 {
		_ = json.Unmarshal(config, &cfg)
	}
	c.groupID = cfg.GroupID
	return nil
}

func (c *CronTimePlugin) Invoke(ctx context.Context, method string, paramsJSON json.RawMessage, callerPluginID string) (json.RawMessage, error) {
	_ = ctx
	_ = method
	_ = paramsJSON
	_ = callerPluginID
	return nil, papi.NewStructuredError(papi.ErrorCodeNotFound, "method is not exported")
}

func (c *CronTimePlugin) Handle(ctx context.Context, listenerID string, eventRaw ob11.Event, match *papi.CommandMatch) (papi.HandleResult, error) {
	_ = ctx
	switch listenerID {
	case "cmd.set_group":
		return c.handleSetGroup(eventRaw, match)
	case "cron.send_time":
		return c.handleCronTime(eventRaw)
	default:
		return papi.HandleResult{}, nil
	}
}

func (c *CronTimePlugin) Shutdown(ctx context.Context) error {
	_ = ctx
	return nil
}

// handleSetGroup 处理设置群号命令
func (c *CronTimePlugin) handleSetGroup(eventRaw ob11.Event, match *papi.CommandMatch) (papi.HandleResult, error) {
	host := transport.Host()
	if host == nil {
		return papi.HandleResult{}, nil
	}

	var evt map[string]any
	if err := json.Unmarshal(eventRaw, &evt); err != nil {
		return papi.HandleResult{}, nil
	}

	// 获取群号
	if match != nil && len(match.Groups) > 0 {
		var newGroupID int64
		fmt.Sscanf(match.Groups[0], "%d", &newGroupID)
		c.groupID = newGroupID
	}

	msgType, _ := evt["message_type"].(string)
	groupID := evt["group_id"]
	userID := evt["user_id"]

	reply := fmt.Sprintf("已设置发送时间的群号为: %d", c.groupID)

	if msgType == "group" {
		_, _ = host.CallOneBot(context.Background(), "send_group_msg", map[string]any{
			"group_id": groupID,
			"message":  reply,
		})
	} else {
		_, _ = host.CallOneBot(context.Background(), "send_private_msg", map[string]any{
			"user_id":  userID,
			"message":  reply,
		})
	}

	return papi.HandleResult{}, nil
}

// handleCronTime 处理定时任务，发送当前时间
func (c *CronTimePlugin) handleCronTime(eventRaw ob11.Event) (papi.HandleResult, error) {
	host := transport.Host()
	if host == nil {
		return papi.HandleResult{}, nil
	}

	// 检查是否配置了群号
	if c.groupID == 0 {
		// 没有配置群号，不发送消息
		return papi.HandleResult{}, nil
	}

	// 获取当前时间
	now := time.Now()
	timeStr := now.Format("2006-01-02 15:04:05")
	weekday := now.Weekday()

	message := fmt.Sprintf("⏰ 当前时间：%s\n📅 星期%s", timeStr, weekday.String())

	// 发送群消息
	_, _ = host.CallOneBot(context.Background(), "send_group_msg", map[string]any{
		"group_id": c.groupID,
		"message":  message,
	})

	return papi.HandleResult{}, nil
}

func main() {
	logger := hclog.New(&hclog.LoggerOptions{
		Name:  "nyanyabot-plugin-cron-time",
		Level: hclog.Info,
	})

	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: transport.Handshake(),
		Plugins: plugin.PluginSet{
			transport.PluginName: &transport.Map{PluginImpl: &CronTimePlugin{}},
		},
		Logger: logger,
	})
}
