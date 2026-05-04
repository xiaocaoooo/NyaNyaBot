package transport

import (
	"context"
	"encoding/json"
	"net"
	"net/rpc"
	"testing"

	"github.com/xiaocaoooo/nyanyabot/internal/onebot/ob11"
	papi "github.com/xiaocaoooo/nyanyabot/internal/plugin"
)

// TestContextSelfIDPropagation 测试 self_id 通过 context 传递
func TestContextSelfIDPropagation(t *testing.T) {
	// 创建一个测试插件，它会检查 context 中的 self_id
	var receivedSelfID int64
	testPlugin := &testContextPlugin{
		handleFunc: func(ctx context.Context, listenerID string, eventRaw ob11.Event, match *papi.CommandMatch) (papi.HandleResult, error) {
			receivedSelfID = GetSelfID(ctx)
			return papi.HandleResult{}, nil
		},
	}

	// 设置 RPC 服务器
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	srv := rpc.NewServer()
	if err := srv.RegisterName("Plugin", &PluginRPCServer{Impl: testPlugin}); err != nil {
		t.Fatalf("register plugin rpc server: %v", err)
	}
	go srv.ServeConn(serverConn)

	client := &PluginRPCClient{client: rpc.NewClient(clientConn)}

	// 创建包含 self_id 的事件
	eventJSON := json.RawMessage(`{"self_id": 12345, "post_type": "message", "message": "test"}`)

	// 调用 Handle
	_, err := client.Handle(context.Background(), "test_listener", eventJSON, nil)
	if err != nil {
		t.Fatalf("handle failed: %v", err)
	}

	// 验证 self_id 被正确传递
	if receivedSelfID != 12345 {
		t.Errorf("expected self_id 12345, got %d", receivedSelfID)
	}
}

// TestContextTraceIDPropagation 测试 trace_id 通过 context 传递
func TestContextTraceIDPropagation(t *testing.T) {
	var receivedTraceID string
	testPlugin := &testContextPlugin{
		handleFunc: func(ctx context.Context, listenerID string, eventRaw ob11.Event, match *papi.CommandMatch) (papi.HandleResult, error) {
			receivedTraceID = GetTraceID(ctx)
			return papi.HandleResult{}, nil
		},
	}

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	srv := rpc.NewServer()
	if err := srv.RegisterName("Plugin", &PluginRPCServer{Impl: testPlugin}); err != nil {
		t.Fatalf("register plugin rpc server: %v", err)
	}
	go srv.ServeConn(serverConn)

	client := &PluginRPCClient{client: rpc.NewClient(clientConn), traceID: "test-trace-123"}

	eventJSON := json.RawMessage(`{"post_type": "message", "message": "test"}`)

	_, err := client.Handle(context.Background(), "test_listener", eventJSON, nil)
	if err != nil {
		t.Fatalf("handle failed: %v", err)
	}

	if receivedTraceID != "test-trace-123" {
		t.Errorf("expected trace_id 'test-trace-123', got '%s'", receivedTraceID)
	}
}

// TestHostCallOneBotReceivesSelfID 测试 Host 端接收到 self_id
func TestHostCallOneBotReceivesSelfID(t *testing.T) {
	var receivedSelfID int64
	testHost := &testContextHost{
		callOneBotFunc: func(ctx context.Context, action string, params any, selfID int64, traceID string) (ob11.APIResponse, error) {
			receivedSelfID = selfID
			return ob11.APIResponse{}, nil
		},
	}

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	srv := rpc.NewServer()
	if err := srv.RegisterName("Plugin", &HostRPCServer{Impl: testHost}); err != nil {
		t.Fatalf("register host rpc server: %v", err)
	}
	go srv.ServeConn(serverConn)

	client := &HostRPCClient{client: rpc.NewClient(clientConn)}

	// 创建带有 self_id 的 context
	ctx := WithSelfID(context.Background(), 67890)

	_, err := client.CallOneBot(ctx, "send_msg", map[string]any{"message": "test"})
	if err != nil {
		t.Fatalf("CallOneBot failed: %v", err)
	}

	if receivedSelfID != 67890 {
		t.Errorf("expected self_id 67890, got %d", receivedSelfID)
	}
}

// 测试辅助类型
type testContextPlugin struct {
	handleFunc func(context.Context, string, ob11.Event, *papi.CommandMatch) (papi.HandleResult, error)
}

func (p *testContextPlugin) Descriptor(ctx context.Context) (papi.Descriptor, error) {
	return papi.Descriptor{PluginID: "test.context"}, nil
}

func (p *testContextPlugin) Configure(ctx context.Context, config json.RawMessage) error {
	return nil
}

func (p *testContextPlugin) Invoke(ctx context.Context, method string, paramsJSON json.RawMessage, callerPluginID string) (json.RawMessage, error) {
	return nil, nil
}

func (p *testContextPlugin) Handle(ctx context.Context, listenerID string, eventRaw ob11.Event, match *papi.CommandMatch) (papi.HandleResult, error) {
	if p.handleFunc != nil {
		return p.handleFunc(ctx, listenerID, eventRaw, match)
	}
	return papi.HandleResult{}, nil
}

func (p *testContextPlugin) Shutdown(ctx context.Context) error {
	return nil
}

type testContextHost struct {
	callOneBotFunc func(context.Context, string, any, int64, string) (ob11.APIResponse, error)
}

func (h *testContextHost) CallOneBot(ctx context.Context, action string, params any, selfID int64, traceID string) (ob11.APIResponse, error) {
	if h.callOneBotFunc != nil {
		return h.callOneBotFunc(ctx, action, params, selfID, traceID)
	}
	return ob11.APIResponse{}, nil
}

func (h *testContextHost) CallDependency(ctx context.Context, targetPluginID string, method string, params json.RawMessage) (json.RawMessage, *papi.StructuredError) {
	return nil, nil
}

func (h *testContextHost) GetStats(ctx context.Context) (GetStatsReply, error) {
	return GetStatsReply{}, nil
}
