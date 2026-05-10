package plugin

import (
	"context"
	"encoding/json"

	"github.com/xiaocaoooo/nyanyabot/internal/onebot/ob11"
)

// Plugin is the in-process plugin interface.
//
// This repo's design doc mentions go-plugin (out-of-process). For now we provide
// an in-process interface with the same semantics, so we can finish the bot
// core first. It can be bridged to go-plugin later.
type Plugin interface {
	Descriptor(ctx context.Context) (Descriptor, error)
	// Configure pushes plugin configuration from host to plugin.
	// config is an arbitrary JSON object, typically stored in host config.
	// Host should call this once right after plugin load, and whenever config changes.
	Configure(ctx context.Context, config json.RawMessage) error
	// Invoke handles cross-plugin method calls routed by host.
	// callerPluginID is set by host and cannot be forged by plugin side.
	Invoke(ctx context.Context, method string, paramsJSON json.RawMessage, callerPluginID string) (resultJSON json.RawMessage, err error)
	Handle(ctx context.Context, listenerID string, eventRaw ob11.Event, match *CommandMatch) (HandleResult, error)
	Shutdown(ctx context.Context) error
}

// CallOneBotFunc is provided to plugins so they can invoke OneBot actions.
// This matches the design doc "command style" CallOneBot.
type CallOneBotFunc func(ctx context.Context, action string, params any) (CallResult, error)

// Descriptor mirrors designs/plugin_interface.md.
type Descriptor struct {
	Name        string `json:"name"`
	PluginID    string `json:"plugin_id"`
	Version     string `json:"version"`
	Author      string `json:"author"`
	Description string `json:"description"`
	// Dependencies declares direct plugin dependencies that can be invoked at runtime.
	Dependencies []string `json:"dependencies"`
	// Exports declares callable methods this plugin exposes to other plugins.
	Exports []ExportSpec `json:"exports"`
	// Config declares the plugin configuration contract.
	// If nil, the plugin does not accept configuration.
	Config   *ConfigSpec       `json:"config,omitempty"`
	Commands []CommandListener `json:"commands"`
	Events   []EventListener   `json:"events"`
	Crons    []CronListener    `json:"crons"`
}

// ConfigSpec describes a plugin's configuration schema and defaults.
// Schema is expected to be JSON Schema (draft-07/2020-12), but treated as opaque JSON by host.
type ConfigSpec struct {
	Version     string          `json:"version,omitempty"`
	Description string          `json:"description,omitempty"`
	Schema      json.RawMessage `json:"schema,omitempty"`
	Default     json.RawMessage `json:"default,omitempty"`
}

type ExportSpec struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	ParamsSchema json.RawMessage `json:"params_schema"`
	ResultSchema json.RawMessage `json:"result_schema"`
}

type CommandListener struct {
	Name        string `json:"name"`
	ID          string `json:"id"`
	Description string `json:"description"`
	Pattern     string `json:"pattern"`
	MatchRaw    bool   `json:"match_raw"`
	Handler     string `json:"handler"`
}

type EventListener struct {
	Name        string `json:"name"`
	ID          string `json:"id"`
	Description string `json:"description"`
	Event       string `json:"event"`
	Handler     string `json:"handler"`
}

type CronListener struct {
	Name        string `json:"name"`
	ID          string `json:"id"`
	Description string `json:"description"`
	Schedule    string `json:"schedule"`
	Handler     string `json:"handler"`
}

// CommandMatch captures regexp matches.
type CommandMatch struct {
	Full   string   `json:"full"`
	Groups []string `json:"groups"`
}

type HandleResult struct {
	// Reserved for future: let plugin return declarative actions.
	// For now plugins should directly call CallOneBot.
}

type CallResult struct {
	Raw ob11.APIResponse
}

// EnsureDescriptorArrays ensures all array fields in Descriptor are non-nil
func EnsureDescriptorArrays(d *Descriptor) {
	if d.Commands == nil {
		d.Commands = []CommandListener{}
	}
	if d.Events == nil {
		d.Events = []EventListener{}
	}
	if d.Crons == nil {
		d.Crons = []CronListener{}
	}
	if d.Dependencies == nil {
		d.Dependencies = []string{}
	}
	if d.Exports == nil {
		d.Exports = []ExportSpec{}
	}
	
	// 验证并修复 Exports 中的 ParamsSchema 字段
	for i := range d.Exports {
		if d.Exports[i].ParamsSchema == nil || len(d.Exports[i].ParamsSchema) == 0 {
			d.Exports[i].ParamsSchema = json.RawMessage("{}")
		} else {
			var tmp interface{}
			if err := json.Unmarshal(d.Exports[i].ParamsSchema, &tmp); err != nil {
				d.Exports[i].ParamsSchema = json.RawMessage("{}")
			}
		}
		
		// 验证并修复 ResultSchema 字段
		if d.Exports[i].ResultSchema == nil || len(d.Exports[i].ResultSchema) == 0 {
			d.Exports[i].ResultSchema = json.RawMessage("{}")
		} else {
			var tmp interface{}
			if err := json.Unmarshal(d.Exports[i].ResultSchema, &tmp); err != nil {
				d.Exports[i].ResultSchema = json.RawMessage("{}")
			}
		}
	}
	
	// 确保 Config 字段的 JSON 数据有效
	if d.Config != nil {
		// 验证并修复 Schema 字段
		if d.Config.Schema == nil || len(d.Config.Schema) == 0 {
			d.Config.Schema = json.RawMessage("{}")
		} else {
			var tmp interface{}
			if err := json.Unmarshal(d.Config.Schema, &tmp); err != nil {
				d.Config.Schema = json.RawMessage("{}")
			}
		}
		
		// 验证并修复 Default 字段
		if d.Config.Default == nil || len(d.Config.Default) == 0 {
			d.Config.Default = json.RawMessage("{}")
		} else {
			var tmp interface{}
			if err := json.Unmarshal(d.Config.Default, &tmp); err != nil {
				d.Config.Default = json.RawMessage("{}")
			}
		}
	}
}
