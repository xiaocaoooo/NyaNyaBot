package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Manager holds plugins and exposes lookup utilities.
//
// Note: This is a minimal manager (no hot reload yet).
type Manager struct {
	mu              sync.RWMutex
	plugins         map[string]*pluginEntry // key = plugin_id
	isPluginEnabled func(pluginID string) bool
}

type pluginEntry struct {
	p         Plugin
	desc      Descriptor
	depSet    map[string]struct{}
	exportSet map[string]struct{}
}

func NewManager() *Manager {
	return &Manager{plugins: make(map[string]*pluginEntry)}
}

func (m *Manager) SetPluginEnabledChecker(fn func(pluginID string) bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.isPluginEnabled = fn
}

func (m *Manager) Register(ctx context.Context, p Plugin) (Descriptor, error) {
	d, err := p.Descriptor(ctx)
	if err != nil {
		return Descriptor{}, err
	}
	return m.RegisterWithDescriptor(ctx, p, d)
}

func (m *Manager) RegisterWithDescriptor(ctx context.Context, p Plugin, d Descriptor) (Descriptor, error) {
	_ = ctx
	normalized, err := ValidateDescriptor(d)
	if err != nil {
		return Descriptor{}, err
	}
	entry := &pluginEntry{
		p:         p,
		desc:      normalized,
		depSet:    make(map[string]struct{}, len(normalized.Dependencies)),
		exportSet: make(map[string]struct{}, len(normalized.Exports)),
	}
	for _, dep := range normalized.Dependencies {
		entry.depSet[dep] = struct{}{}
	}
	for _, exp := range normalized.Exports {
		entry.exportSet[exp.Name] = struct{}{}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.plugins[normalized.PluginID]; ok {
		return Descriptor{}, fmt.Errorf("plugin already registered: %s", normalized.PluginID)
	}
	m.plugins[normalized.PluginID] = entry
	return normalized, nil
}

func (m *Manager) List() []Descriptor {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Descriptor, 0, len(m.plugins))
	for _, e := range m.plugins {
		out = append(out, e.desc)
	}
	return out
}

func (m *Manager) Get(pluginID string) (Plugin, Descriptor, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.plugins[pluginID]
	if !ok {
		return nil, Descriptor{}, false
	}
	return e.p, e.desc, true
}

// Entries returns a snapshot of plugins and descriptors.
func (m *Manager) Entries() map[string]Descriptor {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]Descriptor, len(m.plugins))
	for k, e := range m.plugins {
		out[k] = e.desc
	}
	return out
}

func (m *Manager) CallDependency(ctx context.Context, callerPluginID string, targetPluginID string, method string, params json.RawMessage) (json.RawMessage, *StructuredError) {
	callerPluginID = strings.TrimSpace(callerPluginID)
	targetPluginID = strings.TrimSpace(targetPluginID)
	method = strings.TrimSpace(method)

	m.mu.RLock()
	callerEntry, callerOK := m.plugins[callerPluginID]
	targetEntry, targetOK := m.plugins[targetPluginID]
	checker := m.isPluginEnabled
	m.mu.RUnlock()

	isPluginEnabled := func(pluginID string) bool {
		if checker == nil {
			return true
		}
		return checker(pluginID)
	}

	if !callerOK {
		return nil, NewStructuredError(ErrorCodeForbidden, "caller plugin is not registered")
	}
	if !isPluginEnabled(callerPluginID) {
		return nil, NewStructuredError(ErrorCodeForbidden, fmt.Sprintf("plugin %q is disabled", callerPluginID))
	}
	if _, ok := callerEntry.depSet[targetPluginID]; !ok {
		return nil, NewStructuredError(ErrorCodeForbidden, fmt.Sprintf("plugin %q does not depend on %q", callerPluginID, targetPluginID))
	}
	if !targetOK {
		return nil, NewStructuredError(ErrorCodeNotFound, fmt.Sprintf("target plugin %q not found", targetPluginID))
	}
	if !isPluginEnabled(targetPluginID) {
		return nil, NewStructuredError(ErrorCodeForbidden, fmt.Sprintf("plugin %q is disabled", targetPluginID))
	}
	if _, ok := targetEntry.exportSet[method]; !ok {
		return nil, NewStructuredError(ErrorCodeNotFound, fmt.Sprintf("method %q is not exported by %q", method, targetPluginID))
	}

	result, err := targetEntry.p.Invoke(ctx, method, params, callerPluginID)
	if err == nil {
		return result, nil
	}
	if se := AsStructuredError(err); se != nil {
		switch se.Code {
		case ErrorCodeForbidden, ErrorCodeNotFound, ErrorCodeInvalidParams, ErrorCodeInternal:
			return nil, se
		default:
			return nil, NewStructuredError(ErrorCodeInternal, se.Error())
		}
	}
	return nil, NewStructuredError(ErrorCodeInternal, err.Error())
}

func ValidateDescriptor(d Descriptor) (Descriptor, error) {
	d.PluginID = strings.TrimSpace(d.PluginID)
	if d.PluginID == "" {
		return Descriptor{}, errors.New("plugin_id is empty")
	}

	seenDeps := make(map[string]struct{}, len(d.Dependencies))
	deps := make([]string, 0, len(d.Dependencies))
	for _, dep := range d.Dependencies {
		dep = strings.TrimSpace(dep)
		if dep == "" {
			return Descriptor{}, fmt.Errorf("plugin %q has empty dependency", d.PluginID)
		}
		if dep == d.PluginID {
			return Descriptor{}, fmt.Errorf("plugin %q cannot depend on itself", d.PluginID)
		}
		if _, exists := seenDeps[dep]; exists {
			return Descriptor{}, fmt.Errorf("plugin %q has duplicate dependency %q", d.PluginID, dep)
		}
		seenDeps[dep] = struct{}{}
		deps = append(deps, dep)
	}
	d.Dependencies = deps

	seenExports := make(map[string]struct{}, len(d.Exports))
	exports := make([]ExportSpec, 0, len(d.Exports))
	for _, exp := range d.Exports {
		exp.Name = strings.TrimSpace(exp.Name)
		exp.Description = strings.TrimSpace(exp.Description)
		if exp.Name == "" {
			return Descriptor{}, fmt.Errorf("plugin %q has export with empty name", d.PluginID)
		}
		if _, exists := seenExports[exp.Name]; exists {
			return Descriptor{}, fmt.Errorf("plugin %q has duplicate export %q", d.PluginID, exp.Name)
		}
		seenExports[exp.Name] = struct{}{}
		exports = append(exports, exp)
	}
	d.Exports = exports
	return d, nil
}
