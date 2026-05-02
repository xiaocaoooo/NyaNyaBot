package transport

// This package defines the go-plugin transport between host and plugin.
//
// We use net/rpc for simplicity and compatibility (no .proto generation).
// The RPC surface follows designs/plugin_interface.md:
//   - Describe() -> Descriptor
//   - Invoke(method, params, caller_plugin_id) -> result_json | structured error
//   - Handle(listener_id, event_raw_json, match?) -> HandleResult
//   - Shutdown()
// Plus a host service exposed to plugins:
//   - CallOneBot(action, params) -> APIResponse
//   - CallDependency(target_plugin_id, method, params_json) -> result_json | structured error

import (
	"context"
	"encoding/json"
	"time"

	"github.com/xiaocaoooo/nyanyabot/internal/onebot/ob11"
	"github.com/xiaocaoooo/nyanyabot/internal/plugin"
)

type DescribeReply = plugin.Descriptor

type ConfigureArgs struct {
	Config json.RawMessage `json:"config"`
}

type HandleArgs struct {
	ListenerID   string          `json:"listener_id"`
	EventRawJSON json.RawMessage `json:"event_raw_json"`
	Match        *plugin.CommandMatch
	TraceID      string          `json:"trace_id"`
}

type HandleReply = plugin.HandleResult

type InvokeArgs struct {
	Method         string          `json:"method"`
	Params         json.RawMessage `json:"params"`
	CallerPluginID string          `json:"caller_plugin_id"`
}

type InvokeReply struct {
	Result json.RawMessage         `json:"result"`
	Error  *plugin.StructuredError `json:"error,omitempty"`
}

type CallOneBotArgs struct {
	Action  string          `json:"action"`
	Params  json.RawMessage `json:"params"`
	TraceID string          `json:"trace_id"`
}

type CallOneBotReply struct {
	Resp ob11.APIResponse `json:"resp"`
}

type CallDependencyArgs struct {
	TargetPluginID string          `json:"target_plugin_id"`
	Method         string          `json:"method"`
	Params         json.RawMessage `json:"params"`
}

type CallDependencyReply struct {
	Result json.RawMessage         `json:"result"`
	Error  *plugin.StructuredError `json:"error,omitempty"`
}

// GetStatsArgs 为空，保留以保持一致性
type GetStatsArgs struct{}

// GetStatsReply 返回运行统计信息
type GetStatsReply struct {
	RecvCount int64     `json:"recv_count"`
	SentCount int64     `json:"sent_count"`
	StartTime time.Time `json:"start_time"`
	Uptime    string    `json:"uptime"`
}

// HostAPI is implemented by host and exposed to plugin.
type HostAPI interface {
	CallOneBot(ctx context.Context, action string, params any, traceID string) (ob11.APIResponse, error)
	CallDependency(ctx context.Context, targetPluginID string, method string, params json.RawMessage) (json.RawMessage, *plugin.StructuredError)
	GetStats(ctx context.Context) (GetStatsReply, error)
}
