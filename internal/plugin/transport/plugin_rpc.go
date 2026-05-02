package transport

import (
	"context"
	"encoding/json"
	"net/rpc"
	"sync"

	"github.com/hashicorp/go-plugin"
	"github.com/xiaocaoooo/nyanyabot/internal/onebot/ob11"
	papi "github.com/xiaocaoooo/nyanyabot/internal/plugin"
)

// traceIDStorage 是线程本地存储，用于在 Handle 和 CallOneBot 之间传递 TraceID
var traceIDStorage = struct {
	sync.RWMutex
	id string
}{}

func setCurrentTraceID(id string) {
	traceIDStorage.Lock()
	traceIDStorage.id = id
	traceIDStorage.Unlock()
}

func getCurrentTraceID() string {
	traceIDStorage.RLock()
	defer traceIDStorage.RUnlock()
	return traceIDStorage.id
}

func clearCurrentTraceID() {
	traceIDStorage.Lock()
	traceIDStorage.id = ""
	traceIDStorage.Unlock()
}

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

func (s *PluginRPCServer) Configure(args ConfigureArgs, _ *struct{}) error {
	return s.Impl.Configure(context.Background(), args.Config)
}

func (s *PluginRPCServer) Invoke(args InvokeArgs, resp *InvokeReply) error {
	result, err := s.Impl.Invoke(context.Background(), args.Method, args.Params, args.CallerPluginID)
	if err != nil {
		resp.Error = papi.NormalizeStructuredError(err, papi.ErrorCodeInternal)
		return nil
	}
	resp.Result = result
	resp.Error = nil
	return nil
}

func (s *PluginRPCServer) Handle(args HandleArgs, resp *HandleReply) error {
	// 将 TraceID 存入线程本地存储，供后续的 CallOneBot 使用
	setCurrentTraceID(args.TraceID)
	defer clearCurrentTraceID()

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

// AttachHost is called by host to set up the host callback client in plugin process.
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
	client  *rpc.Client
	broker  *plugin.MuxBroker
	traceID string // 待传递的 TraceID
}

// In plugin process we keep a global host RPC client, so plugin implementations
// can call back into host APIs (CallOneBot / CallDependency / GetStats).
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

func (c *PluginRPCClient) Invoke(ctx context.Context, method string, paramsJSON json.RawMessage, callerPluginID string) (json.RawMessage, error) {
	_ = ctx
	var resp InvokeReply
	args := InvokeArgs{Method: method, Params: paramsJSON, CallerPluginID: callerPluginID}
	if err := c.client.Call("Plugin.Invoke", args, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	return resp.Result, nil
}

func (c *PluginRPCClient) Handle(ctx context.Context, listenerID string, eventRaw ob11.Event, match *papi.CommandMatch) (papi.HandleResult, error) {
	_ = ctx
	var resp HandleReply
	args := HandleArgs{ListenerID: listenerID, EventRawJSON: json.RawMessage(eventRaw), Match: match, TraceID: c.traceID}
	if err := c.client.Call("Plugin.Handle", args, &resp); err != nil {
		return papi.HandleResult{}, err
	}
	return resp, nil
}

func (c *PluginRPCClient) SetTraceID(traceID string) {
	c.traceID = traceID
}

func (c *PluginRPCClient) Configure(ctx context.Context, config json.RawMessage) error {
	_ = ctx
	var out struct{}
	return c.client.Call("Plugin.Configure", ConfigureArgs{Config: config}, &out)
}

func (c *PluginRPCClient) Shutdown(ctx context.Context) error {
	_ = ctx
	var out struct{}
	return c.client.Call("Plugin.Shutdown", struct{}{}, &out)
}

func (c *PluginRPCClient) AttachHost(ctx context.Context, host HostAPI) error {
	_ = ctx
	if c.broker == nil || host == nil {
		return nil
	}
	bid := ServeHostAPI(c.broker, host)
	if bid == 0 {
		return nil
	}
	var out struct{}
	return c.client.Call("Plugin.AttachHost", AttachHostArgs{BrokerID: bid}, &out)
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
	r, err := s.Impl.CallOneBot(context.Background(), args.Action, params, args.TraceID)
	if err != nil {
		return err
	}
	resp.Resp = r
	return nil
}

func (s *HostRPCServer) CallDependency(args CallDependencyArgs, resp *CallDependencyReply) error {
	result, serr := s.Impl.CallDependency(context.Background(), args.TargetPluginID, args.Method, args.Params)
	resp.Result = result
	resp.Error = serr
	return nil
}

// GetStats 实现 HostRPCServer.GetStats
func (s *HostRPCServer) GetStats(_ GetStatsArgs, resp *GetStatsReply) error {
	r, err := s.Impl.GetStats(context.Background())
	if err != nil {
		return err
	}
	*resp = r
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
	// 从线程本地存储获取 TraceID
	traceID := getCurrentTraceID()
	// Note: when served via MuxBroker.AcceptAndServe, service name is always "Plugin".
	if err := c.client.Call("Plugin.CallOneBot", CallOneBotArgs{Action: action, Params: b, TraceID: traceID}, &resp); err != nil {
		return ob11.APIResponse{}, err
	}
	return resp.Resp, nil
}

func (c *HostRPCClient) CallDependency(ctx context.Context, targetPluginID string, method string, params any) (json.RawMessage, error) {
	_ = ctx
	b, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	var resp CallDependencyReply
	if err := c.client.Call("Plugin.CallDependency", CallDependencyArgs{TargetPluginID: targetPluginID, Method: method, Params: b}, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	return resp.Result, nil
}

// GetStats 实现 HostRPCClient.GetStats
func (c *HostRPCClient) GetStats(ctx context.Context) (GetStatsReply, error) {
	_ = ctx
	var resp GetStatsReply
	if err := c.client.Call("Plugin.GetStats", GetStatsArgs{}, &resp); err != nil {
		return GetStatsReply{}, err
	}
	return resp, nil
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
	// Host is optional in host process. If provided, it is attached immediately.
	// Most code paths should call PluginRPCClient.AttachHost with caller-bound HostAPI after descriptor handshake.
	Host HostAPI
}

func Handshake() plugin.HandshakeConfig { return handshake }

func (m *Map) Server(b *plugin.MuxBroker) (interface{}, error) {
	// In plugin process: expose Plugin service.
	return &PluginRPCServer{Impl: m.PluginImpl, B: b}, nil
}

func (m *Map) Client(b *plugin.MuxBroker, c *rpc.Client) (interface{}, error) {
	pc := &PluginRPCClient{client: c, broker: b}
	if m.Host != nil {
		_ = pc.AttachHost(context.Background(), m.Host)
	}
	return pc, nil
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
