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
	Action string          `json:"action"`
	Params json.RawMessage `json:"params"`
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

// HostAPI is implemented by host and exposed to plugin.
type HostAPI interface {
	CallOneBot(ctx context.Context, action string, params any) (ob11.APIResponse, error)
	CallDependency(ctx context.Context, targetPluginID string, method string, params json.RawMessage) (json.RawMessage, *plugin.StructuredError)
}
