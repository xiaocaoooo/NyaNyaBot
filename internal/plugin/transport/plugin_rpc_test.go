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

type rpcStubPlugin struct{}

func (p *rpcStubPlugin) Descriptor(ctx context.Context) (papi.Descriptor, error) {
	_ = ctx
	return papi.Descriptor{PluginID: "external.stub", Exports: []papi.ExportSpec{{Name: "stub.ok"}}}, nil
}

func (p *rpcStubPlugin) Configure(ctx context.Context, config json.RawMessage) error {
	_ = ctx
	_ = config
	return nil
}

func (p *rpcStubPlugin) Invoke(ctx context.Context, method string, paramsJSON json.RawMessage, callerPluginID string) (json.RawMessage, error) {
	_ = ctx
	_ = paramsJSON
	_ = callerPluginID
	switch method {
	case "stub.ok":
		return json.RawMessage(`{"ok":true}`), nil
	case "stub.invalid":
		return nil, papi.NewStructuredError(papi.ErrorCodeInvalidParams, "invalid params")
	default:
		return nil, papi.NewStructuredError(papi.ErrorCodeNotFound, "method not found")
	}
}

func (p *rpcStubPlugin) Handle(ctx context.Context, listenerID string, eventRaw ob11.Event, match *papi.CommandMatch) (papi.HandleResult, error) {
	_ = ctx
	_ = listenerID
	_ = eventRaw
	_ = match
	return papi.HandleResult{}, nil
}

func (p *rpcStubPlugin) Shutdown(ctx context.Context) error {
	_ = ctx
	return nil
}

type rpcStubHost struct{}

func (h *rpcStubHost) CallOneBot(ctx context.Context, action string, params any) (ob11.APIResponse, error) {
	_ = ctx
	_ = action
	_ = params
	return ob11.APIResponse{}, nil
}

func (h *rpcStubHost) CallDependency(ctx context.Context, targetPluginID string, method string, params json.RawMessage) (json.RawMessage, *papi.StructuredError) {
	_ = ctx
	_ = targetPluginID
	switch method {
	case "host.echo":
		return params, nil
	case "host.forbidden":
		return nil, papi.NewStructuredError(papi.ErrorCodeForbidden, "forbidden")
	default:
		return nil, papi.NewStructuredError(papi.ErrorCodeNotFound, "not found")
	}
}

func (h *rpcStubHost) GetStats(ctx context.Context) (GetStatsReply, error) {
	_ = ctx
	return GetStatsReply{}, nil
}

func TestPluginInvokeStructuredErrorRoundTrip(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	srv := rpc.NewServer()
	if err := srv.RegisterName("Plugin", &PluginRPCServer{Impl: &rpcStubPlugin{}}); err != nil {
		t.Fatalf("register plugin rpc server: %v", err)
	}
	go srv.ServeConn(serverConn)

	client := &PluginRPCClient{client: rpc.NewClient(clientConn)}

	res, err := client.Invoke(context.Background(), "stub.ok", json.RawMessage(`{}`), "external.caller")
	if err != nil {
		t.Fatalf("invoke stub.ok: %v", err)
	}
	if string(res) != `{"ok":true}` {
		t.Fatalf("unexpected invoke result: %s", string(res))
	}

	_, err = client.Invoke(context.Background(), "stub.invalid", json.RawMessage(`{}`), "external.caller")
	if err == nil {
		t.Fatalf("expected structured error")
	}
	serr := papi.AsStructuredError(err)
	if serr == nil {
		t.Fatalf("expected structured error type, got %T: %v", err, err)
	}
	if serr.Code != papi.ErrorCodeInvalidParams {
		t.Fatalf("expected INVALID_PARAMS, got %s", serr.Code)
	}
}

func TestHostCallDependencyStructuredErrorRoundTrip(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	srv := rpc.NewServer()
	if err := srv.RegisterName("Plugin", &HostRPCServer{Impl: &rpcStubHost{}}); err != nil {
		t.Fatalf("register host rpc server: %v", err)
	}
	go srv.ServeConn(serverConn)

	client := &HostRPCClient{client: rpc.NewClient(clientConn)}

	res, err := client.CallDependency(context.Background(), "external.target", "host.echo", map[string]any{"a": 1})
	if err != nil {
		t.Fatalf("call dependency host.echo: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(res, &got); err != nil {
		t.Fatalf("decode dependency result: %v", err)
	}
	if got["a"] != float64(1) {
		t.Fatalf("unexpected dependency payload: %#v", got)
	}

	_, err = client.CallDependency(context.Background(), "external.target", "host.forbidden", map[string]any{})
	if err == nil {
		t.Fatalf("expected structured error for host.forbidden")
	}
	serr := papi.AsStructuredError(err)
	if serr == nil {
		t.Fatalf("expected structured error type, got %T: %v", err, err)
	}
	if serr.Code != papi.ErrorCodeForbidden {
		t.Fatalf("expected FORBIDDEN, got %s", serr.Code)
	}
}
