package dispatch

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/xiaocaoooo/nyanyabot/internal/config"
	"github.com/xiaocaoooo/nyanyabot/internal/dedup"
	"github.com/xiaocaoooo/nyanyabot/internal/onebot/ob11"
	"github.com/xiaocaoooo/nyanyabot/internal/plugin"
)

type recordingPlugin struct {
	desc        plugin.Descriptor
	handled     []string
	receivedRaw []ob11.Event
}

func (p *recordingPlugin) Descriptor(ctx context.Context) (plugin.Descriptor, error) {
	_ = ctx
	return p.desc, nil
}

func (p *recordingPlugin) Configure(ctx context.Context, config json.RawMessage) error {
	_ = ctx
	_ = config
	return nil
}

func (p *recordingPlugin) Invoke(ctx context.Context, method string, paramsJSON json.RawMessage, callerPluginID string) (json.RawMessage, error) {
	_ = ctx
	_ = method
	_ = paramsJSON
	_ = callerPluginID
	return nil, nil
}

func (p *recordingPlugin) Handle(ctx context.Context, listenerID string, eventRaw ob11.Event, match *plugin.CommandMatch) (plugin.HandleResult, error) {
	_ = ctx
	_ = match
	p.handled = append(p.handled, listenerID)
	p.receivedRaw = append(p.receivedRaw, eventRaw)
	return plugin.HandleResult{}, nil
}

func (p *recordingPlugin) Shutdown(ctx context.Context) error {
	_ = ctx
	return nil
}

func newTestDispatcher(t *testing.T, cfg config.AppConfig, plugins ...*recordingPlugin) *Dispatcher {
	t.Helper()
	pm := plugin.NewManager()
	for _, p := range plugins {
		if _, err := pm.Register(context.Background(), p); err != nil {
			t.Fatalf("register plugin %s: %v", p.desc.PluginID, err)
		}
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	disp := NewWithLogger(pm, logger)
	disp.SetConfigProvider(func() config.AppConfig { return cfg })
	return disp
}

func messageEvent(raw string) ob11.Event {
	return ob11.Event([]byte(raw))
}

func TestDispatchSkipsDisabledPlugin(t *testing.T) {
	disabledPlugin := &recordingPlugin{desc: plugin.Descriptor{
		PluginID: "external.disabled",
		Commands: []plugin.CommandListener{{ID: "cmd.disabled", Pattern: `^/ping$`}},
		Events:   []plugin.EventListener{{ID: "evt.disabled", Event: "message"}},
	}}
	enabledPlugin := &recordingPlugin{desc: plugin.Descriptor{
		PluginID: "external.enabled",
		Commands: []plugin.CommandListener{{ID: "cmd.enabled", Pattern: `^/ping$`}},
		Events:   []plugin.EventListener{{ID: "evt.enabled", Event: "message"}},
	}}

	disp := newTestDispatcher(t, config.AppConfig{
		PluginControls: map[string]config.PluginControl{
			"external.disabled": {Disabled: true},
		},
	}, disabledPlugin, enabledPlugin)

	disp.Dispatch(context.Background(), messageEvent(`{"post_type":"message","message_type":"group","user_id":123,"self_id":456,"raw_message":"/ping","message":"/ping"}`))

	if len(disabledPlugin.handled) != 0 {
		t.Fatalf("expected disabled plugin to be skipped, got %#v", disabledPlugin.handled)
	}
	if !reflect.DeepEqual(enabledPlugin.handled, []string{"evt.enabled", "cmd.enabled"}) {
		t.Fatalf("unexpected enabled plugin handlers: %#v", enabledPlugin.handled)
	}
}

func TestDispatchSkipsDisabledEventListener(t *testing.T) {
	p := &recordingPlugin{desc: plugin.Descriptor{
		PluginID: "external.events",
		Events: []plugin.EventListener{
			{ID: "evt.enabled", Event: "message"},
			{ID: "evt.disabled", Event: "message"},
		},
	}}

	disp := newTestDispatcher(t, config.AppConfig{
		PluginControls: map[string]config.PluginControl{
			"external.events": {DisabledEvents: []string{"evt.disabled"}},
		},
	}, p)

	disp.Dispatch(context.Background(), messageEvent(`{"post_type":"message","message_type":"group","raw_message":"hello","message":"hello"}`))

	if !reflect.DeepEqual(p.handled, []string{"evt.enabled"}) {
		t.Fatalf("unexpected handled events: %#v", p.handled)
	}
}

func TestDispatchSkipsDisabledCommandListener(t *testing.T) {
	p := &recordingPlugin{desc: plugin.Descriptor{
		PluginID: "external.commands",
		Commands: []plugin.CommandListener{
			{ID: "cmd.enabled", Pattern: `^/ping$`},
			{ID: "cmd.disabled", Pattern: `^/ping$`},
		},
	}}

	disp := newTestDispatcher(t, config.AppConfig{
		PluginControls: map[string]config.PluginControl{
			"external.commands": {DisabledCommands: []string{"cmd.disabled"}},
		},
	}, p)

	disp.Dispatch(context.Background(), messageEvent(`{"post_type":"message","message_type":"group","user_id":123,"self_id":456,"raw_message":"/ping","message":"/ping"}`))

	if !reflect.DeepEqual(p.handled, []string{"cmd.enabled"}) {
		t.Fatalf("unexpected handled commands: %#v", p.handled)
	}
}

func TestDispatchStripsGlobalPrefix(t *testing.T) {
	p := &recordingPlugin{desc: plugin.Descriptor{
		PluginID: "external.commands",
		Commands: []plugin.CommandListener{{ID: "cmd.ping", Pattern: `^ping$`}},
	}}
	disp := newTestDispatcher(t, config.AppConfig{
		MessagePrefix: `^/(?P<content>.+)$`,
	}, p)

	disp.Dispatch(context.Background(), messageEvent(`{"post_type":"message","message_type":"group","user_id":123,"self_id":456,"raw_message":"/ping","message":"/ping"}`))
	if !reflect.DeepEqual(p.handled, []string{"cmd.ping"}) {
		t.Fatalf("expected global prefix stripped and command matched, got %#v", p.handled)
	}
}

func TestDispatchStripsPluginPrefix(t *testing.T) {
	p := &recordingPlugin{desc: plugin.Descriptor{
		PluginID: "external.commands",
		Commands: []plugin.CommandListener{{ID: "cmd.ping", Pattern: `^ping$`}},
	}}
	disp := newTestDispatcher(t, config.AppConfig{
		MessagePrefix: `^/(?P<content>.+)$`,
		PluginControls: map[string]config.PluginControl{
			"external.commands": {CommandPrefix: `^#(?P<content>.+)$`},
		},
	}, p)

	disp.Dispatch(context.Background(), messageEvent(`{"post_type":"message","message_type":"group","user_id":123,"self_id":456,"raw_message":"#ping","message":"#ping"}`))
	if !reflect.DeepEqual(p.handled, []string{"cmd.ping"}) {
		t.Fatalf("expected plugin prefix override to match, got %#v", p.handled)
	}
}

func TestDispatchSkipsCommandWithoutPrefixMatch(t *testing.T) {
	p := &recordingPlugin{desc: plugin.Descriptor{
		PluginID: "external.commands",
		Commands: []plugin.CommandListener{{ID: "cmd.ping", Pattern: `^ping$`}},
	}}
	disp := newTestDispatcher(t, config.AppConfig{
		MessagePrefix: `^/(?P<content>.+)$`,
	}, p)

	// Without matching prefix, command should be skipped
	disp.Dispatch(context.Background(), messageEvent(`{"post_type":"message","message_type":"group","user_id":123,"self_id":456,"raw_message":"ping","message":"ping"}`))
	if len(p.handled) != 0 {
		t.Fatalf("expected command without prefix to be skipped in strict mode, got %#v", p.handled)
	}
}

func TestDispatchMatchesCommandWithPrefix(t *testing.T) {
	p := &recordingPlugin{desc: plugin.Descriptor{
		PluginID: "external.commands",
		Commands: []plugin.CommandListener{{ID: "cmd.ping", Pattern: `^ping$`}},
	}}
	disp := newTestDispatcher(t, config.AppConfig{
		MessagePrefix: `^/(?P<content>.+)$`,
	}, p)

	// With matching prefix, command should match
	disp.Dispatch(context.Background(), messageEvent(`{"post_type":"message","message_type":"group","user_id":123,"self_id":456,"raw_message":"/ping","message":"/ping"}`))
	if !reflect.DeepEqual(p.handled, []string{"cmd.ping"}) {
		t.Fatalf("expected command with prefix to match, got %#v", p.handled)
	}
}

func TestDeriveContentFromStringMessage(t *testing.T) {
	raw := messageEvent(`{"post_type":"message","message":"hello world"}`)
	got := deriveContent(raw)
	want := "hello world"
	if got != want {
		t.Fatalf("deriveContent(string message) = %q, want %q", got, want)
	}
}

func TestDeriveContentFromTextSegments(t *testing.T) {
	raw := messageEvent(`{
		"post_type":"message",
		"message":[
			{"type":"text","data":{"text":"hello "}},
			{"type":"image","data":{"file":"abc.jpg"}},
			{"type":"at","data":{"qq":"123456"}},
			{"type":"text","data":{"text":"world"}}
		]
	}`)
	got := deriveContent(raw)
	want := "hello world"
	if got != want {
		t.Fatalf("deriveContent(text+image+at+text) = %q, want %q", got, want)
	}
}

func TestDeriveContentFromNoTextSegments(t *testing.T) {
	raw := messageEvent(`{
		"post_type":"message",
		"message":[
			{"type":"image","data":{"file":"abc.jpg"}},
			{"type":"at","data":{"qq":"123456"}}
		]
	}`)
	got := deriveContent(raw)
	want := ""
	if got != want {
		t.Fatalf("deriveContent(no text segments) = %q, want %q", got, want)
	}
}

func TestDispatchInjectsContentForStringMessage(t *testing.T) {
	p := &recordingPlugin{desc: plugin.Descriptor{
		PluginID: "external.events",
		Events:   []plugin.EventListener{{ID: "evt.message", Event: "message"}},
	}}
	disp := newTestDispatcher(t, config.AppConfig{}, p)

	disp.Dispatch(context.Background(), messageEvent(`{"post_type":"message","message":"hello world"}`))

	if len(p.receivedRaw) != 1 {
		t.Fatalf("expected 1 event, got %d", len(p.receivedRaw))
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(p.receivedRaw[0], &parsed); err != nil {
		t.Fatalf("unmarshal received event: %v", err)
	}

	content, ok := parsed["content"]
	if !ok {
		t.Fatalf("expected content field in received event, got keys: %v", reflect.ValueOf(parsed).MapKeys())
	}
	if content != "hello world" {
		t.Fatalf("expected content=%q, got %q", "hello world", content)
	}
}

func TestDispatchInjectsContentForMixedSegments(t *testing.T) {
	p := &recordingPlugin{desc: plugin.Descriptor{
		PluginID: "external.commands",
		Commands: []plugin.CommandListener{{ID: "cmd.test", Pattern: `^hello`}},
	}}
	disp := newTestDispatcher(t, config.AppConfig{
		MessagePrefix: `^/(?P<content>.+)$`,
	}, p)

	disp.Dispatch(context.Background(), messageEvent(`{
		"post_type":"message",
		"message_type":"group",
		"user_id":123,
		"self_id":456,
		"message":[
			{"type":"text","data":{"text":"/hello "}},
			{"type":"image","data":{"file":"abc.jpg"}},
			{"type":"at","data":{"qq":"123456"}},
			{"type":"text","data":{"text":"world"}}
		],
		"raw_message":"/hello world"
	}`))

	if len(p.receivedRaw) != 1 {
		t.Fatalf("expected 1 event, got %d", len(p.receivedRaw))
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(p.receivedRaw[0], &parsed); err != nil {
		t.Fatalf("unmarshal received event: %v", err)
	}

	content, ok := parsed["content"]
	if !ok {
		t.Fatalf("expected content field in received event")
	}
	if content != "/hello world" {
		t.Fatalf("expected content=%q (text segments only), got %q", "/hello world", content)
	}
}

func TestDispatchInjectsEmptyContentForNoTextSegments(t *testing.T) {
	p := &recordingPlugin{desc: plugin.Descriptor{
		PluginID: "external.events",
		Events:   []plugin.EventListener{{ID: "evt.message", Event: "message"}},
	}}
	disp := newTestDispatcher(t, config.AppConfig{}, p)

	disp.Dispatch(context.Background(), messageEvent(`{
		"post_type":"message",
		"message":[
			{"type":"image","data":{"file":"abc.jpg"}},
			{"type":"at","data":{"qq":"123456"}}
		]
	}`))

	if len(p.receivedRaw) != 1 {
		t.Fatalf("expected 1 event, got %d", len(p.receivedRaw))
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(p.receivedRaw[0], &parsed); err != nil {
		t.Fatalf("unmarshal received event: %v", err)
	}

	content, ok := parsed["content"]
	if !ok {
		t.Fatalf("expected content field in received event")
	}
	if content != "" {
		t.Fatalf("expected empty content, got %q", content)
	}
}

func TestDispatchDoesNotInjectContentForNonMessageEvent(t *testing.T) {
	p := &recordingPlugin{desc: plugin.Descriptor{
		PluginID: "external.events",
		Events:   []plugin.EventListener{{ID: "evt.notice", Event: "notice"}},
	}}
	disp := newTestDispatcher(t, config.AppConfig{}, p)

	disp.Dispatch(context.Background(), ob11.Event([]byte(`{"post_type":"notice","notice_type":"group_upload"}`)))

	if len(p.receivedRaw) != 1 {
		t.Fatalf("expected 1 event, got %d", len(p.receivedRaw))
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(p.receivedRaw[0], &parsed); err != nil {
		t.Fatalf("unmarshal received event: %v", err)
	}

	if _, ok := parsed["content"]; ok {
		t.Fatalf("expected no content field for non-message event, got: %v", parsed)
	}
}

func TestDispatchDeduplicatesMessages(t *testing.T) {
	p := &recordingPlugin{desc: plugin.Descriptor{
		PluginID: "external.commands",
		Commands: []plugin.CommandListener{{ID: "cmd.test", Pattern: `^test$`}},
	}}
	dedupEnabled := true
	disp := newTestDispatcher(t, config.AppConfig{
		MessagePrefix: `^/(?P<content>.+)$`,
		MessageDedup:  &dedupEnabled,
	}, p)
	// 设置 deduper
	disp.deduper = dedup.NewMemoryDeduper(5 * time.Minute)

	// 发送相同的消息两次（相同 group_id 和 message_seq）
	msg := `{"post_type":"message","message_type":"group","group_id":"123456","message_seq":"100","user_id":"789","self_id":"456","raw_message":"/test","message":"/test"}`
	disp.Dispatch(context.Background(), messageEvent(msg))
	disp.Dispatch(context.Background(), messageEvent(msg))

	// 第二次应该被去重，只处理一次
	if len(p.handled) != 1 {
		t.Fatalf("expected message to be deduplicated, got %d handlers called", len(p.handled))
	}
	if p.handled[0] != "cmd.test" {
		t.Fatalf("unexpected handler called: %s", p.handled[0])
	}
}

func TestDispatchDedupDifferentGroups(t *testing.T) {
	p := &recordingPlugin{desc: plugin.Descriptor{
		PluginID: "external.commands",
		Commands: []plugin.CommandListener{{ID: "cmd.test", Pattern: `^test$`}},
	}}
	dedupEnabled := true
	disp := newTestDispatcher(t, config.AppConfig{
		MessagePrefix: `^/(?P<content>.+)$`,
		MessageDedup:  &dedupEnabled,
	}, p)
	// 设置 deduper
	disp.deduper = dedup.NewMemoryDeduper(5 * time.Minute)

	// 发送相同 message_seq 但不同 group_id 的消息
	msg1 := `{"post_type":"message","message_type":"group","group_id":"111","message_seq":"100","user_id":"789","self_id":"456","raw_message":"/test","message":"/test"}`
	msg2 := `{"post_type":"message","message_type":"group","group_id":"222","message_seq":"100","user_id":"789","self_id":"456","raw_message":"/test","message":"/test"}`
	disp.Dispatch(context.Background(), messageEvent(msg1))
	disp.Dispatch(context.Background(), messageEvent(msg2))

	// 不同群的消息应该独立处理，不会互相去重
	if len(p.handled) != 2 {
		t.Fatalf("expected both messages to be processed independently, got %d handlers called", len(p.handled))
	}
}

func TestDispatchDedupDisabled(t *testing.T) {
	p := &recordingPlugin{desc: plugin.Descriptor{
		PluginID: "external.commands",
		Commands: []plugin.CommandListener{{ID: "cmd.test", Pattern: `^test$`}},
	}}
	dedupDisabled := false
	disp := newTestDispatcher(t, config.AppConfig{
		MessagePrefix: `^/(?P<content>.+)$`,
		MessageDedup:  &dedupDisabled,
	}, p)
	// 设置 deduper（即使设置了，也应该因为配置禁用而不使用）
	disp.deduper = dedup.NewMemoryDeduper(5 * time.Minute)

	// 发送相同的消息两次（使用不同的 message_seq 避免与其他测试冲突）
	msg := `{"post_type":"message","message_type":"group","group_id":"999","message_seq":"999","user_id":"789","self_id":"456","raw_message":"/test","message":"/test"}`
	disp.Dispatch(context.Background(), messageEvent(msg))
	disp.Dispatch(context.Background(), messageEvent(msg))

	// 禁用去重时，两次消息都应该被处理
	if len(p.handled) != 2 {
		t.Fatalf("expected both messages to be processed when dedup is disabled, got %d handlers called", len(p.handled))
	}
}

func TestDispatchDedupNonGroupMessage(t *testing.T) {
	p := &recordingPlugin{desc: plugin.Descriptor{
		PluginID: "external.events",
		Events:   []plugin.EventListener{{ID: "evt.message", Event: "message"}},
	}}
	dedupEnabled := true
	disp := newTestDispatcher(t, config.AppConfig{
		MessageDedup: &dedupEnabled,
	}, p)
	// 设置 deduper
	disp.deduper = dedup.NewMemoryDeduper(5 * time.Minute)

	// 发送相同的私聊消息两次（message_type 不是 group）
	msg := `{"post_type":"message","message_type":"private","user_id":"789","self_id":"456","message_seq":"100","raw_message":"hello","message":"hello"}`
	disp.Dispatch(context.Background(), messageEvent(msg))
	disp.Dispatch(context.Background(), messageEvent(msg))

	// 非群消息不会进行去重，event listener 会处理两次
	// 注意：去重逻辑只在 command listeners 部分生效，且只对群消息生效
	if len(p.handled) != 2 {
		t.Fatalf("expected non-group messages not to be deduplicated, got %d handlers called", len(p.handled))
	}
}
