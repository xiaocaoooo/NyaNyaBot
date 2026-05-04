package dedup

// Deduper 定义消息去重接口
type Deduper interface {
	// TryMarkProcessed 尝试标记消息为已处理
	// 返回 true 表示这是首次处理该消息（应该继续处理）
	// 返回 false 表示该消息已经处理过（应该跳过）
	TryMarkProcessed(groupID int64, messageSeq int64) bool
}
