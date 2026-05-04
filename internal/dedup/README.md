# Dedup Package

消息去重包，提供基于内存的消息去重功能，防止重复处理相同的消息。

## 功能特性

- ✅ 基于 `(group_id, message_seq)` 的唯一标识
- ✅ 支持自定义 TTL（过期时间）
- ✅ 线程安全（使用 `sync.RWMutex`）
- ✅ 支持过期条目自动清理
- ✅ 高性能（读操作 ~70ns，写操作 ~338ns）
- ✅ 零依赖（仅使用标准库）

## 接口定义

```go
type Deduper interface {
    // TryMarkProcessed 尝试标记消息为已处理
    // 返回 true 表示这是首次处理该消息（应该继续处理）
    // 返回 false 表示该消息已经处理过（应该跳过）
    TryMarkProcessed(groupID int64, messageSeq int64) bool
}
```

## 使用方法

### 基本用法

```go
import (
    "time"
    "github.com/xiaocaoooo/nyanyabot/internal/dedup"
)

// 创建去重器，TTL 为 5 分钟
deduper := dedup.NewMemoryDeduper(5 * time.Minute)

// 尝试标记消息为已处理
groupID := int64(123456)
messageSeq := int64(789)

if deduper.TryMarkProcessed(groupID, messageSeq) {
    // 首次处理，继续执行业务逻辑
    processMessage(groupID, messageSeq)
} else {
    // 重复消息，跳过处理
    log.Info("Duplicate message, skipping")
}
```

### 检查消息状态

```go
// 检查消息是否已被处理
if deduper.IsProcessed(groupID, messageSeq) {
    log.Info("Message already processed")
}
```

### 清理过期条目

```go
// 定期清理过期条目以释放内存
ticker := time.NewTicker(10 * time.Minute)
go func() {
    for range ticker.C {
        deduper.CleanExpired()
        log.Info("Cleaned expired entries", "size", deduper.Size())
    }
}()
```

### 清空所有缓存

```go
// 清空所有缓存（通常用于测试或重置）
deduper.Clear()
```

## API 文档

### NewMemoryDeduper

```go
func NewMemoryDeduper(ttl time.Duration) *MemoryDeduper
```

创建基于内存的消息去重器。

**参数:**
- `ttl`: 缓存条目的默认过期时间

**返回:**
- `*MemoryDeduper`: 去重器实例

### TryMarkProcessed

```go
func (d *MemoryDeduper) TryMarkProcessed(groupID int64, messageSeq int64) bool
```

尝试标记消息为已处理。此方法是原子操作，线程安全。

**参数:**
- `groupID`: 群组 ID
- `messageSeq`: 消息序列号

**返回:**
- `true`: 消息未被处理过（或已过期），已成功标记为已处理
- `false`: 消息已被处理且未过期，拒绝重复处理

### IsProcessed

```go
func (d *MemoryDeduper) IsProcessed(groupID int64, messageSeq int64) bool
```

检查消息是否已被处理。此方法只读，不会修改缓存状态。

**参数:**
- `groupID`: 群组 ID
- `messageSeq`: 消息序列号

**返回:**
- `true`: 消息已被处理且未过期
- `false`: 消息未被处理或已过期

### Clear

```go
func (d *MemoryDeduper) Clear()
```

清空所有缓存条目。此操作会立即删除所有已记录的消息处理状态。

### CleanExpired

```go
func (d *MemoryDeduper) CleanExpired()
```

清理所有过期的缓存条目。建议定期调用此方法以释放内存。

**时间复杂度:** O(n)，n 为当前缓存条目数

### Size

```go
func (d *MemoryDeduper) Size() int
```

返回当前缓存中的条目数量。

**注意:** 返回的数量包含已过期但尚未清理的条目。如需准确的有效条目数，应先调用 `CleanExpired()`。

## 性能指标

基准测试结果（Intel Core i5-10400F @ 2.90GHz）:

```
BenchmarkTryMarkProcessed-12    	 3862518	       338.3 ns/op	     136 B/op	       1 allocs/op
BenchmarkIsProcessed-12         	17190868	        70.32 ns/op	       0 B/op	       0 allocs/op
BenchmarkCleanExpired-12        	    7857	    157011 ns/op	       0 B/op	       0 allocs/op
```

- **TryMarkProcessed**: ~338ns/op，每次操作分配 136 字节
- **IsProcessed**: ~70ns/op，零内存分配
- **CleanExpired**: ~157µs/op（10000 条目）

## 并发安全

所有方法都是线程安全的：

- 使用 `sync.RWMutex` 保护并发访问
- 读操作（`IsProcessed`）使用读锁，允许并发读取
- 写操作（`TryMarkProcessed`, `Clear`, `CleanExpired`）使用写锁，保证数据一致性

## 测试覆盖

包含完整的单元测试和并发测试：

```bash
# 运行所有测试
go test -v ./internal/dedup/

# 运行基准测试
go test -bench=. -benchmem ./internal/dedup/

# 运行示例测试
go test -v ./internal/dedup/ -run Example
```

## 设计考虑

### 为什么使用 (group_id, message_seq) 作为唯一键？

- `group_id`: 区分不同的群组
- `message_seq`: OneBot 协议中的消息序列号，在同一群组内唯一

### 为什么需要 TTL？

- 防止内存无限增长
- 允许过期消息重新处理（例如系统重启后）
- 平衡内存使用和去重效果

### 为什么使用 struct 作为 map key 而不是字符串？

- 性能更好（避免字符串拼接和分配）
- 类型安全（编译时检查）
- 内存效率更高

## 未来改进

可能的改进方向：

- [ ] 支持持久化存储（Redis、数据库等）
- [ ] 支持分布式去重
- [ ] 添加统计信息（命中率、过期率等）
- [ ] 支持 LRU 淘汰策略
- [ ] 支持批量操作

## 许可证

与项目主许可证相同。
