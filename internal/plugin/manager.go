package plugin

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Manager holds plugins and exposes lookup utilities.
//
// Note: This is a minimal manager (no hot reload yet).
type Manager struct {
	mu      sync.RWMutex
	plugins map[string]*pluginEntry // key = plugin_id
}

type pluginEntry struct {
	p    Plugin
	desc Descriptor
}

func NewManager() *Manager {
	return &Manager{plugins: make(map[string]*pluginEntry)}
}

func (m *Manager) Register(ctx context.Context, p Plugin) (Descriptor, error) {
	d, err := p.Descriptor(ctx)
	if err != nil {
		return Descriptor{}, err
	}
	if d.PluginID == "" {
		return Descriptor{}, errors.New("plugin_id is empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.plugins[d.PluginID]; ok {
		return Descriptor{}, fmt.Errorf("plugin already registered: %s", d.PluginID)
	}
	m.plugins[d.PluginID] = &pluginEntry{p: p, desc: d}
	return d, nil
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
