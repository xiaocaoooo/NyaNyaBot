package chatlog

import (
	"sync"
	"time"
)

// GroupCache 提供 group_id -> group_name 的 TTL 缓存
type GroupCache struct {
	mu      sync.RWMutex
	entries map[int64]*cacheEntry
	ttl     time.Duration
}

type cacheEntry struct {
	name      string
	expiresAt time.Time
}

// NewGroupCache 创建群名称缓存
func NewGroupCache(ttl time.Duration) *GroupCache {
	return &GroupCache{
		entries: make(map[int64]*cacheEntry),
		ttl:     ttl,
	}
}

// Get 获取缓存的群名称，未命中或过期返回空字符串和 false
func (c *GroupCache) Get(groupID int64) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[groupID]
	if !ok {
		return "", false
	}

	if time.Now().After(entry.expiresAt) {
		return "", false
	}

	return entry.name, true
}

// Set 设置群名称缓存
func (c *GroupCache) Set(groupID int64, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[groupID] = &cacheEntry{
		name:      name,
		expiresAt: time.Now().Add(c.ttl),
	}
}

// Clear 清空缓存
func (c *GroupCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[int64]*cacheEntry)
}

// CleanExpired 清理过期条目
func (c *GroupCache) CleanExpired() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for id, entry := range c.entries {
		if now.After(entry.expiresAt) {
			delete(c.entries, id)
		}
	}
}
