package plugin

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/xiaocaoooo/nyanyabot/internal/onebot/ob11"
)

type stubPlugin struct {
	desc   Descriptor
	invoke func(ctx context.Context, method string, paramsJSON json.RawMessage, callerPluginID string) (json.RawMessage, error)
}

func (s *stubPlugin) Descriptor(ctx context.Context) (Descriptor, error) {
	_ = ctx
	return s.desc, nil
}

func (s *stubPlugin) Configure(ctx context.Context, config json.RawMessage) error {
	_ = ctx
	_ = config
	return nil
}

func (s *stubPlugin) Invoke(ctx context.Context, method string, paramsJSON json.RawMessage, callerPluginID string) (json.RawMessage, error) {
	if s.invoke != nil {
		return s.invoke(ctx, method, paramsJSON, callerPluginID)
	}
	return nil, NewStructuredError(ErrorCodeNotFound, "method is not exported")
}

func (s *stubPlugin) Handle(ctx context.Context, listenerID string, eventRaw ob11.Event, match *CommandMatch) (HandleResult, error) {
	_ = ctx
	_ = listenerID
	_ = eventRaw
	_ = match
	return HandleResult{}, nil
}

func (s *stubPlugin) Shutdown(ctx context.Context) error {
	_ = ctx
	return nil
}

func TestValidateDescriptorDependencyAndExportRules(t *testing.T) {
	base := Descriptor{PluginID: "external.test"}

	cases := []struct {
		name string
		desc Descriptor
	}{
		{
			name: "empty dependency",
			desc: Descriptor{PluginID: base.PluginID, Dependencies: []string{""}},
		},
		{
			name: "duplicate dependency",
			desc: Descriptor{PluginID: base.PluginID, Dependencies: []string{"external.a", "external.a"}},
		},
		{
			name: "self dependency",
			desc: Descriptor{PluginID: base.PluginID, Dependencies: []string{base.PluginID}},
		},
		{
			name: "empty export name",
			desc: Descriptor{PluginID: base.PluginID, Exports: []ExportSpec{{Name: ""}}},
		},
		{
			name: "duplicate export name",
			desc: Descriptor{PluginID: base.PluginID, Exports: []ExportSpec{{Name: "ping"}, {Name: "ping"}}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ValidateDescriptor(tc.desc); err == nil {
				t.Fatalf("expected validation error")
			}
		})
	}
}

func TestCallDependencyRejectsUndeclaredDependency(t *testing.T) {
	m := NewManager()
	caller := &stubPlugin{desc: Descriptor{PluginID: "external.caller", Dependencies: []string{}, Exports: []ExportSpec{}}}
	target := &stubPlugin{desc: Descriptor{PluginID: "external.target", Exports: []ExportSpec{{Name: "target.echo"}}}}

	if _, err := m.Register(context.Background(), caller); err != nil {
		t.Fatalf("register caller: %v", err)
	}
	if _, err := m.Register(context.Background(), target); err != nil {
		t.Fatalf("register target: %v", err)
	}

	_, serr := m.CallDependency(context.Background(), "external.caller", "external.target", "target.echo", json.RawMessage(`{"x":1}`))
	if serr == nil {
		t.Fatalf("expected structured error")
	}
	if serr.Code != ErrorCodeForbidden {
		t.Fatalf("expected FORBIDDEN, got %s", serr.Code)
	}
}

func TestCallDependencyRejectsDisabledPlugins(t *testing.T) {
	cases := []struct {
		name       string
		disabledID string
	}{
		{name: "disabled caller", disabledID: "external.caller"},
		{name: "disabled target", disabledID: "external.target"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewManager()
			m.SetPluginEnabledChecker(func(pluginID string) bool {
				return pluginID != tc.disabledID
			})
			caller := &stubPlugin{desc: Descriptor{PluginID: "external.caller", Dependencies: []string{"external.target"}}}
			target := &stubPlugin{desc: Descriptor{PluginID: "external.target", Exports: []ExportSpec{{Name: "target.echo"}}}}

			if _, err := m.Register(context.Background(), caller); err != nil {
				t.Fatalf("register caller: %v", err)
			}
			if _, err := m.Register(context.Background(), target); err != nil {
				t.Fatalf("register target: %v", err)
			}

			_, serr := m.CallDependency(context.Background(), "external.caller", "external.target", "target.echo", json.RawMessage(`{}`))
			if serr == nil {
				t.Fatalf("expected structured error")
			}
			if serr.Code != ErrorCodeForbidden {
				t.Fatalf("expected FORBIDDEN, got %#v", serr)
			}
		})
	}
}

func TestCallDependencyNotFoundCases(t *testing.T) {
	m := NewManager()
	caller := &stubPlugin{desc: Descriptor{PluginID: "external.caller", Dependencies: []string{"external.target", "external.missing"}, Exports: []ExportSpec{}}}
	target := &stubPlugin{desc: Descriptor{PluginID: "external.target", Exports: []ExportSpec{{Name: "target.echo"}}}}

	if _, err := m.Register(context.Background(), caller); err != nil {
		t.Fatalf("register caller: %v", err)
	}
	if _, err := m.Register(context.Background(), target); err != nil {
		t.Fatalf("register target: %v", err)
	}

	_, serr := m.CallDependency(context.Background(), "external.caller", "external.missing", "target.echo", json.RawMessage(`{}`))
	if serr == nil || serr.Code != ErrorCodeNotFound {
		t.Fatalf("expected NOT_FOUND for missing target, got %#v", serr)
	}

	_, serr = m.CallDependency(context.Background(), "external.caller", "external.target", "target.unknown", json.RawMessage(`{}`))
	if serr == nil || serr.Code != ErrorCodeNotFound {
		t.Fatalf("expected NOT_FOUND for unexported method, got %#v", serr)
	}
}

func TestCallDependencyPassesCallerAndMapsErrors(t *testing.T) {
	m := NewManager()

	var capturedCaller string
	target := &stubPlugin{
		desc: Descriptor{
			PluginID: "external.target",
			Exports:  []ExportSpec{{Name: "target.echo"}, {Name: "target.invalid"}},
		},
		invoke: func(ctx context.Context, method string, paramsJSON json.RawMessage, callerPluginID string) (json.RawMessage, error) {
			_ = ctx
			capturedCaller = callerPluginID
			switch method {
			case "target.echo":
				var payload map[string]any
				if err := json.Unmarshal(paramsJSON, &payload); err != nil {
					return nil, NewStructuredError(ErrorCodeInvalidParams, "bad params")
				}
				out, _ := json.Marshal(map[string]any{"caller": callerPluginID, "payload": payload})
				return out, nil
			case "target.invalid":
				return nil, NewStructuredError(ErrorCodeInvalidParams, "invalid params")
			default:
				return nil, NewStructuredError(ErrorCodeNotFound, "method not found")
			}
		},
	}
	caller := &stubPlugin{desc: Descriptor{PluginID: "external.caller", Dependencies: []string{"external.target"}, Exports: []ExportSpec{}}}

	if _, err := m.Register(context.Background(), caller); err != nil {
		t.Fatalf("register caller: %v", err)
	}
	if _, err := m.Register(context.Background(), target); err != nil {
		t.Fatalf("register target: %v", err)
	}

	result, serr := m.CallDependency(context.Background(), "external.caller", "external.target", "target.echo", json.RawMessage(`{"hello":"world"}`))
	if serr != nil {
		t.Fatalf("unexpected structured error: %#v", serr)
	}
	if capturedCaller != "external.caller" {
		t.Fatalf("caller not passed through, got %q", capturedCaller)
	}
	var got map[string]any
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if got["caller"] != "external.caller" {
		t.Fatalf("unexpected caller in result: %#v", got)
	}

	_, serr = m.CallDependency(context.Background(), "external.caller", "external.target", "target.invalid", json.RawMessage(`{}`))
	if serr == nil || serr.Code != ErrorCodeInvalidParams {
		t.Fatalf("expected INVALID_PARAMS, got %#v", serr)
	}
}
