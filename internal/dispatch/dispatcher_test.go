package dispatch

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"reflect"
	"testing"

	"github.com/xiaocaoooo/nyanyabot/internal/config"
	"github.com/xiaocaoooo/nyanyabot/internal/onebot/ob11"
	"github.com/xiaocaoooo/nyanyabot/internal/plugin"
)

type recordingPlugin struct {
	desc    plugin.Descriptor
	handled []string
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
	_ = eventRaw
	_ = match
	p.handled = append(p.handled, listenerID)
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

	disp.Dispatch(context.Background(), messageEvent(`{"post_type":"message","message_type":"private","raw_message":"/ping","message":"/ping"}`))

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

	disp.Dispatch(context.Background(), messageEvent(`{"post_type":"message","message_type":"private","raw_message":"/ping","message":"/ping"}`))

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

	disp.Dispatch(context.Background(), messageEvent(`{"post_type":"message","message_type":"private","raw_message":"/ping","message":"/ping"}`))
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

	disp.Dispatch(context.Background(), messageEvent(`{"post_type":"message","message_type":"private","raw_message":"#ping","message":"#ping"}`))
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
	disp.Dispatch(context.Background(), messageEvent(`{"post_type":"message","message_type":"private","raw_message":"ping","message":"ping"}`))
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
	disp.Dispatch(context.Background(), messageEvent(`{"post_type":"message","message_type":"private","raw_message":"/ping","message":"/ping"}`))
	if !reflect.DeepEqual(p.handled, []string{"cmd.ping"}) {
		t.Fatalf("expected command with prefix to match, got %#v", p.handled)
	}
}
