package transport

import (
	"context"
	"encoding/json"
	"net/rpc"

	"github.com/hashicorp/go-plugin"
	"github.com/xiaocaoooo/nyanyabot/internal/onebot/ob11"
	papi "github.com/xiaocaoooo/nyanyabot/internal/plugin"
)

// ===== Plugin-side service (called by host) =====

type PluginRPCServer struct {
	Impl papi.Plugin
	B    *plugin.MuxBroker
}

func (s *PluginRPCServer) Describe(_ struct{}, resp *DescribeReply) error {
	d, err := s.Impl.Descriptor(context.Background())
	if err != nil {
		return err
	}
	*resp = d
	return nil
}

func (s *PluginRPCServer) Handle(args HandleArgs, resp *HandleReply) error {
	r, err := s.Impl.Handle(context.Background(), args.ListenerID, args.EventRawJSON, args.Match)
	if err != nil {
		return err
	}
	*resp = r
	return nil
}

func (s *PluginRPCServer) Shutdown(_ struct{}, _ *struct{}) error {
	return s.Impl.Shutdown(context.Background())
}

type AttachHostArgs struct {
	BrokerID uint32 `json:"broker_id"`
}

// AttachHost is called by host right after dispensing the plugin.
// It sets up a long-lived RPC client back to host APIs (CallOneBot).
func (s *PluginRPCServer) AttachHost(args AttachHostArgs, _ *struct{}) error {
	if s.B == nil {
		return nil
	}
	conn, err := s.B.Dial(args.BrokerID)
	if err != nil {
		return err
	}
	// rpc.Client will Close() the underlying connection.
	hc := &HostRPCClient{client: rpc.NewClient(conn)}
	SetHost(hc)
	return nil
}

// PluginRPCClient is used on the host to call into the plugin.
type PluginRPCClient struct {
	client *rpc.Client
}

// In plugin process we keep a global host RPC client, so plugin implementations
// can call back into host APIs (CallOneBot).
var hostClient *HostRPCClient

func SetHost(c *HostRPCClient) { hostClient = c }

func Host() *HostRPCClient { return hostClient }

// Descriptor implements internal/plugin.Plugin.
func (c *PluginRPCClient) Descriptor(ctx context.Context) (papi.Descriptor, error) {
	return c.Describe(ctx)
}

func (c *PluginRPCClient) Describe(ctx context.Context) (papi.Descriptor, error) {
	_ = ctx
	var resp DescribeReply
	if err := c.client.Call("Plugin.Describe", struct{}{}, &resp); err != nil {
		return papi.Descriptor{}, err
	}
	return resp, nil
}

func (c *PluginRPCClient) Handle(ctx context.Context, listenerID string, eventRaw ob11.Event, match *papi.CommandMatch) (papi.HandleResult, error) {
	_ = ctx
	var resp HandleReply
	args := HandleArgs{ListenerID: listenerID, EventRawJSON: json.RawMessage(eventRaw), Match: match}
	if err := c.client.Call("Plugin.Handle", args, &resp); err != nil {
		return papi.HandleResult{}, err
	}
	return resp, nil
}

func (c *PluginRPCClient) Shutdown(ctx context.Context) error {
	_ = ctx
	var out struct{}
	return c.client.Call("Plugin.Shutdown", struct{}{}, &out)
}

// ===== Host-side service (called by plugin) =====

type HostRPCServer struct {
	Impl HostAPI
}

func (s *HostRPCServer) CallOneBot(args CallOneBotArgs, resp *CallOneBotReply) error {
	var params any
	if len(args.Params) > 0 {
		if err := json.Unmarshal(args.Params, &params); err != nil {
			return err
		}
	}
	r, err := s.Impl.CallOneBot(context.Background(), args.Action, params)
	if err != nil {
		return err
	}
	resp.Resp = r
	return nil
}

// HostRPCClient is used in the plugin process to call host services.
type HostRPCClient struct {
	client *rpc.Client
}

func (c *HostRPCClient) CallOneBot(ctx context.Context, action string, params any) (ob11.APIResponse, error) {
	_ = ctx
	b, err := json.Marshal(params)
	if err != nil {
		return ob11.APIResponse{}, err
	}
	var resp CallOneBotReply
	// Note: when served via MuxBroker.AcceptAndServe, service name is always "Plugin".
	if err := c.client.Call("Plugin.CallOneBot", CallOneBotArgs{Action: action, Params: b}, &resp); err != nil {
		return ob11.APIResponse{}, err
	}
	return resp.Resp, nil
}

// ===== go-plugin wiring =====

const PluginName = "nyanyabot_plugin"

var handshake = plugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "NYANYABOT_PLUGIN",
	MagicCookieValue: "1",
}

// Map implements plugin.Plugin for the main plugin RPC service.
// This is dispensed by the host.
type Map struct {
	// PluginImpl is only set in plugin process.
	PluginImpl papi.Plugin
	// Host is only set in host process; used to expose CallOneBot to plugin.
	Host HostAPI
}

func Handshake() plugin.HandshakeConfig { return handshake }

func (m *Map) Server(b *plugin.MuxBroker) (interface{}, error) {
	// In plugin process: expose Plugin service.
	return &PluginRPCServer{Impl: m.PluginImpl, B: b}, nil
}

func (m *Map) Client(b *plugin.MuxBroker, c *rpc.Client) (interface{}, error) {
	// In host process: attach host API callback over broker, then return plugin client.
	if m.Host != nil && b != nil {
		bid := ServeHostAPI(b, m.Host)
		_ = c.Call("Plugin.AttachHost", AttachHostArgs{BrokerID: bid}, &struct{}{})
	}
	return &PluginRPCClient{client: c}, nil
}

// ServeHostAPI serves the host API over a brokered net/rpc stream.
// The service name on the brokered connection is always "Plugin".
func ServeHostAPI(b *plugin.MuxBroker, host HostAPI) (brokerID uint32) {
	if b == nil || host == nil {
		return 0
	}
	bid := b.NextId()
	go b.AcceptAndServe(bid, &HostRPCServer{Impl: host})
	return bid
}
