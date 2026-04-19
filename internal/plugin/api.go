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
