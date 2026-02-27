package plugin

import (
	"context"

	"github.com/xiaocaoooo/nyanyabot/internal/onebot/ob11"
)

// Plugin is the in-process plugin interface.
//
// This repo's design doc mentions go-plugin (out-of-process). For now we provide
// an in-process interface with the same semantics, so we can finish the bot
// core first. It can be bridged to go-plugin later.
type Plugin interface {
	Descriptor(ctx context.Context) (Descriptor, error)
	Handle(ctx context.Context, listenerID string, eventRaw ob11.Event, match *CommandMatch) (HandleResult, error)
	Shutdown(ctx context.Context) error
}

// CallOneBotFunc is provided to plugins so they can invoke OneBot actions.
// This matches the design doc "command style" CallOneBot.
type CallOneBotFunc func(ctx context.Context, action string, params any) (CallResult, error)

// Descriptor mirrors designs/plugin_interface.md.
type Descriptor struct {
	Name        string            `json:"name"`
	PluginID    string            `json:"plugin_id"`
	Version     string            `json:"version"`
	Author      string            `json:"author"`
	Description string            `json:"description"`
	Commands    []CommandListener `json:"commands"`
	Events      []EventListener   `json:"events"`
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
