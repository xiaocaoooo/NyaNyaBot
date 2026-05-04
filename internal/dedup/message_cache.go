package dedup

import (
	"fmt"
	"sync"
	"time"
)

// messageKey 消息的唯一标识
// 使用 (group_id, message_seq) 组合作为唯一键
type messageKey struct {
	groupID    int64
	messageSeq int64
}

// String 返回消息键的字符串表示，用于调试
func (k messageKey) String() string {
	return fmt.Sprintf("group:%d_seq:%d", k.groupID, k.messageSeq)
}

// cacheEntry 缓存条目
// 记录消息的处理状态和过期时间
type cacheEntry struct {
	// expiresAt 过期时间点
	// 超过此时间的条目将被视为无效
	expiresAt time.Time
}

// MemoryDeduper 基于内存的消息去重实现
// 使用 map 存储消息处理状态，通过 RWMutex 保证并发安全
type MemoryDeduper struct {
	// mu 读写锁，保护 entries 的并发访问
	mu sync.RWMutex

	// entries 存储消息键到缓存条目的映射
	entries map[messageKey]*cacheEntry

	// ttl 缓存条目的默认过期时间
	ttl time.Duration
}

// NewMemoryDeduper 创建基于内存的消息去重器
// 参数:
//   - ttl: 缓存条目的默认过期时间
//
// 返回一个空的去重器实例，可立即使用
func NewMemoryDeduper(ttl time.Duration) *MemoryDeduper {
	return &MemoryDeduper{
		entries: make(map[messageKey]*cacheEntry),
		ttl:     ttl,
	}
}

// TryMarkProcessed 尝试标记消息为已处理
// 参数:
//   - groupID: 群组 ID
//   - messageSeq: 消息序列号
//
// 返回:
//   - true: 消息未被处理过（或已过期），已成功标记为已处理
//   - false: 消息已被处理且未过期，拒绝重复处理
//
// 此方法是原子操作，线程安全
func (d *MemoryDeduper) TryMarkProcessed(groupID int64, messageSeq int64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	key := messageKey{
		groupID:    groupID,
		messageSeq: messageSeq,
	}

	// 检查是否已存在且未过期
	if entry, exists := d.entries[key]; exists {
		if time.Now().Before(entry.expiresAt) {
			// 消息已处理且未过期，拒绝重复处理
			return false
		}
		// 已过期，可以重新处理
	}

	// 标记为已处理，设置过期时间
	d.entries[key] = &cacheEntry{
		expiresAt: time.Now().Add(d.ttl),
	}

	return true
}

// IsProcessed 检查消息是否已被处理
// 参数:
//   - groupID: 群组 ID
//   - messageSeq: 消息序列号
//
// 返回:
//   - true: 消息已被处理且未过期
//   - false: 消息未被处理或已过期
//
// 此方法只读，不会修改缓存状态
func (d *MemoryDeduper) IsProcessed(groupID int64, messageSeq int64) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()

	key := messageKey{
		groupID:    groupID,
		messageSeq: messageSeq,
	}

	entry, exists := d.entries[key]
	if !exists {
		return false
	}

	// 检查是否过期
	return time.Now().Before(entry.expiresAt)
}

// Clear 清空所有缓存条目
// 此操作会立即删除所有已记录的消息处理状态
// 通常用于测试或重置场景
func (d *MemoryDeduper) Clear() {
	d.mu.Lock()
	defer d.mu.Unlock()

	// 创建新的 map，让 GC 回收旧的
	d.entries = make(map[messageKey]*cacheEntry)
}

// CleanExpired 清理所有过期的缓存条目
// 遍历所有条目，删除已过期的记录
// 建议定期调用此方法以释放内存
//
// 时间复杂度: O(n)，n 为当前缓存条目数
func (d *MemoryDeduper) CleanExpired() {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	for key, entry := range d.entries {
		if now.After(entry.expiresAt) || now.Equal(entry.expiresAt) {
			delete(d.entries, key)
		}
	}
}

// Size 返回当前缓存中的条目数量
// 注意: 返回的数量包含已过期但尚未清理的条目
// 如需准确的有效条目数，应先调用 CleanExpired()
//
// 返回:
//   - 当前缓存中的总条目数（包括过期条目）
func (d *MemoryDeduper) Size() int {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return len(d.entries)
}
