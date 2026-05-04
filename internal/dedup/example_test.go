package dedup_test

import (
	"fmt"
	"time"

	"github.com/xiaocaoooo/nyanyabot/internal/dedup"
)

// Example_basicUsage 演示基本的消息去重用法
func Example_basicUsage() {
	// 创建一个 TTL 为 5 分钟的去重器
	deduper := dedup.NewMemoryDeduper(5 * time.Minute)

	groupID := int64(123456)
	messageSeq := int64(789)

	// 首次处理消息
	if deduper.TryMarkProcessed(groupID, messageSeq) {
		fmt.Println("Processing message for the first time")
	}

	// 尝试再次处理相同消息（会被拒绝）
	if !deduper.TryMarkProcessed(groupID, messageSeq) {
		fmt.Println("Message already processed, skipping")
	}

	// Output:
	// Processing message for the first time
	// Message already processed, skipping
}

// Example_checkProcessed 演示检查消息是否已处理
func Example_checkProcessed() {
	deduper := dedup.NewMemoryDeduper(5 * time.Minute)

	groupID := int64(123456)
	messageSeq := int64(789)

	// 检查未处理的消息
	if !deduper.IsProcessed(groupID, messageSeq) {
		fmt.Println("Message not processed yet")
	}

	// 标记消息为已处理
	deduper.TryMarkProcessed(groupID, messageSeq)

	// 再次检查
	if deduper.IsProcessed(groupID, messageSeq) {
		fmt.Println("Message has been processed")
	}

	// Output:
	// Message not processed yet
	// Message has been processed
}

// Example_cleanup 演示清理过期条目
func Example_cleanup() {
	deduper := dedup.NewMemoryDeduper(5 * time.Minute)

	// 添加一些消息
	for i := int64(0); i < 10; i++ {
		deduper.TryMarkProcessed(100, i)
	}

	fmt.Printf("Cache size before cleanup: %d\n", deduper.Size())

	// 清理过期条目（在这个例子中不会清理任何东西，因为都未过期）
	deduper.CleanExpired()

	fmt.Printf("Cache size after cleanup: %d\n", deduper.Size())

	// 清空所有缓存
	deduper.Clear()

	fmt.Printf("Cache size after clear: %d\n", deduper.Size())

	// Output:
	// Cache size before cleanup: 10
	// Cache size after cleanup: 10
	// Cache size after clear: 0
}
