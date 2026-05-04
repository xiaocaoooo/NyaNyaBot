package dedup

import (
	"sync"
	"testing"
	"time"
)

// TestNewMemoryDeduper 测试创建去重器
func TestNewMemoryDeduper(t *testing.T) {
	ttl := 5 * time.Minute
	deduper := NewMemoryDeduper(ttl)
	if deduper == nil {
		t.Fatal("NewMemoryDeduper returned nil")
	}
	if deduper.entries == nil {
		t.Fatal("entries map not initialized")
	}
	if deduper.Size() != 0 {
		t.Errorf("expected size 0, got %d", deduper.Size())
	}
	if deduper.ttl != ttl {
		t.Errorf("expected ttl %v, got %v", ttl, deduper.ttl)
	}
}

// TestTryMarkProcessed_FirstTime 测试首次标记消息
func TestTryMarkProcessed_FirstTime(t *testing.T) {
	deduper := NewMemoryDeduper(5 * time.Minute)
	groupID := int64(12345)
	messageSeq := int64(67890)

	// 首次标记应该成功
	result := deduper.TryMarkProcessed(groupID, messageSeq)
	if !result {
		t.Error("expected TryMarkProcessed to return true for first time")
	}

	// 验证消息已被标记
	if !deduper.IsProcessed(groupID, messageSeq) {
		t.Error("message should be marked as processed")
	}

	// 验证缓存大小
	if deduper.Size() != 1 {
		t.Errorf("expected size 1, got %d", deduper.Size())
	}
}

// TestTryMarkProcessed_Duplicate 测试重复标记消息
func TestTryMarkProcessed_Duplicate(t *testing.T) {
	deduper := NewMemoryDeduper(5 * time.Minute)
	groupID := int64(12345)
	messageSeq := int64(67890)

	// 首次标记
	if !deduper.TryMarkProcessed(groupID, messageSeq) {
		t.Fatal("first TryMarkProcessed should succeed")
	}

	// 重复标记应该失败
	result := deduper.TryMarkProcessed(groupID, messageSeq)
	if result {
		t.Error("expected TryMarkProcessed to return false for duplicate")
	}

	// 缓存大小应该仍为 1
	if deduper.Size() != 1 {
		t.Errorf("expected size 1, got %d", deduper.Size())
	}
}

// TestTryMarkProcessed_Expired 测试过期后重新标记
func TestTryMarkProcessed_Expired(t *testing.T) {
	deduper := NewMemoryDeduper(50 * time.Millisecond)
	groupID := int64(12345)
	messageSeq := int64(67890)

	// 首次标记
	if !deduper.TryMarkProcessed(groupID, messageSeq) {
		t.Fatal("first TryMarkProcessed should succeed")
	}

	// 等待过期
	time.Sleep(100 * time.Millisecond)

	// 过期后应该可以重新标记
	result := deduper.TryMarkProcessed(groupID, messageSeq)
	if !result {
		t.Error("expected TryMarkProcessed to return true after expiration")
	}
}

// TestIsProcessed 测试检查消息处理状态
func TestIsProcessed(t *testing.T) {
	deduper := NewMemoryDeduper(5 * time.Minute)
	groupID := int64(12345)
	messageSeq := int64(67890)

	// 未标记前应该返回 false
	if deduper.IsProcessed(groupID, messageSeq) {
		t.Error("message should not be processed initially")
	}

	// 标记后应该返回 true
	deduper.TryMarkProcessed(groupID, messageSeq)
	if !deduper.IsProcessed(groupID, messageSeq) {
		t.Error("message should be processed after marking")
	}
}

// TestIsProcessed_Expired 测试过期消息的检查
func TestIsProcessed_Expired(t *testing.T) {
	deduper := NewMemoryDeduper(50 * time.Millisecond)
	groupID := int64(12345)
	messageSeq := int64(67890)

	// 标记消息
	deduper.TryMarkProcessed(groupID, messageSeq)

	// 等待过期
	time.Sleep(100 * time.Millisecond)

	// 过期后应该返回 false
	if deduper.IsProcessed(groupID, messageSeq) {
		t.Error("expired message should not be considered processed")
	}
}

// TestClear 测试清空缓存
func TestClear(t *testing.T) {
	deduper := NewMemoryDeduper(5 * time.Minute)

	// 添加多条消息
	deduper.TryMarkProcessed(100, 1)
	deduper.TryMarkProcessed(100, 2)
	deduper.TryMarkProcessed(200, 1)

	if deduper.Size() != 3 {
		t.Errorf("expected size 3, got %d", deduper.Size())
	}

	// 清空缓存
	deduper.Clear()

	if deduper.Size() != 0 {
		t.Errorf("expected size 0 after clear, got %d", deduper.Size())
	}

	// 验证消息不再被标记
	if deduper.IsProcessed(100, 1) {
		t.Error("message should not be processed after clear")
	}
}

// TestCleanExpired 测试清理过期条目
func TestCleanExpired(t *testing.T) {
	deduper := NewMemoryDeduper(5 * time.Minute)
	shortTTL := 50 * time.Millisecond

	// 添加短期消息（手动设置过期时间）
	deduper.mu.Lock()
	deduper.entries[messageKey{100, 1}] = &cacheEntry{expiresAt: time.Now().Add(shortTTL)}
	deduper.entries[messageKey{100, 2}] = &cacheEntry{expiresAt: time.Now().Add(shortTTL)}
	deduper.mu.Unlock()

	// 添加长期消息
	deduper.TryMarkProcessed(200, 1)

	if deduper.Size() != 3 {
		t.Errorf("expected size 3, got %d", deduper.Size())
	}

	// 等待短期消息过期
	time.Sleep(100 * time.Millisecond)

	// 清理过期条目
	deduper.CleanExpired()

	// 应该只剩下 1 条
	if deduper.Size() != 1 {
		t.Errorf("expected size 1 after cleanup, got %d", deduper.Size())
	}

	// 验证长期消息仍然存在
	if !deduper.IsProcessed(200, 1) {
		t.Error("long-lived message should still be processed")
	}

	// 验证短期消息已被清理
	if deduper.IsProcessed(100, 1) {
		t.Error("expired message should not be processed")
	}
}

// TestSize 测试获取缓存大小
func TestSize(t *testing.T) {
	deduper := NewMemoryDeduper(5 * time.Minute)

	// 初始大小应为 0
	if deduper.Size() != 0 {
		t.Errorf("expected initial size 0, got %d", deduper.Size())
	}

	// 添加消息
	deduper.TryMarkProcessed(100, 1)
	if deduper.Size() != 1 {
		t.Errorf("expected size 1, got %d", deduper.Size())
	}

	deduper.TryMarkProcessed(100, 2)
	if deduper.Size() != 2 {
		t.Errorf("expected size 2, got %d", deduper.Size())
	}

	// 重复添加不应增加大小
	deduper.TryMarkProcessed(100, 1)
	if deduper.Size() != 2 {
		t.Errorf("expected size 2 after duplicate, got %d", deduper.Size())
	}
}

// TestDifferentGroups 测试不同群组的消息隔离
func TestDifferentGroups(t *testing.T) {
	deduper := NewMemoryDeduper(5 * time.Minute)
	messageSeq := int64(12345)

	// 在不同群组中标记相同序列号的消息
	if !deduper.TryMarkProcessed(100, messageSeq) {
		t.Error("should mark message in group 100")
	}
	if !deduper.TryMarkProcessed(200, messageSeq) {
		t.Error("should mark message in group 200")
	}

	// 两个群组的消息应该独立
	if deduper.Size() != 2 {
		t.Errorf("expected size 2, got %d", deduper.Size())
	}

	// 验证两个群组的消息都被标记
	if !deduper.IsProcessed(100, messageSeq) {
		t.Error("message in group 100 should be processed")
	}
	if !deduper.IsProcessed(200, messageSeq) {
		t.Error("message in group 200 should be processed")
	}
}

// TestConcurrency 测试并发安全性
func TestConcurrency(t *testing.T) {
	deduper := NewMemoryDeduper(5 * time.Minute)
	concurrency := 100
	messagesPerGoroutine := 100

	var wg sync.WaitGroup
	wg.Add(concurrency)

	// 并发标记消息
	for i := 0; i < concurrency; i++ {
		go func(goroutineID int) {
			defer wg.Done()
			for j := 0; j < messagesPerGoroutine; j++ {
				groupID := int64(goroutineID)
				messageSeq := int64(j)
				deduper.TryMarkProcessed(groupID, messageSeq)
			}
		}(i)
	}

	wg.Wait()

	// 验证所有消息都被标记
	expectedSize := concurrency * messagesPerGoroutine
	if deduper.Size() != expectedSize {
		t.Errorf("expected size %d, got %d", expectedSize, deduper.Size())
	}

	// 并发读取
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func(goroutineID int) {
			defer wg.Done()
			for j := 0; j < messagesPerGoroutine; j++ {
				groupID := int64(goroutineID)
				messageSeq := int64(j)
				if !deduper.IsProcessed(groupID, messageSeq) {
					t.Errorf("message (%d, %d) should be processed", groupID, messageSeq)
				}
			}
		}(i)
	}

	wg.Wait()
}

// TestConcurrentCleanup 测试并发清理
func TestConcurrentCleanup(t *testing.T) {
	deduper := NewMemoryDeduper(5 * time.Minute)

	// 添加一些消息
	for i := 0; i < 1000; i++ {
		deduper.TryMarkProcessed(int64(i), int64(i))
	}

	var wg sync.WaitGroup
	wg.Add(3)

	// 并发清理
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			deduper.CleanExpired()
			time.Sleep(time.Millisecond)
		}
	}()

	// 并发读取
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			deduper.IsProcessed(int64(i%1000), int64(i%1000))
			time.Sleep(time.Millisecond)
		}
	}()

	// 并发写入
	go func() {
		defer wg.Done()
		for i := 1000; i < 1100; i++ {
			deduper.TryMarkProcessed(int64(i), int64(i))
			time.Sleep(time.Millisecond)
		}
	}()

	wg.Wait()
}

// TestMessageKeyString 测试消息键的字符串表示
func TestMessageKeyString(t *testing.T) {
	key := messageKey{
		groupID:    12345,
		messageSeq: 67890,
	}

	expected := "group:12345_seq:67890"
	if key.String() != expected {
		t.Errorf("expected %s, got %s", expected, key.String())
	}
}

// BenchmarkTryMarkProcessed 基准测试标记消息性能
func BenchmarkTryMarkProcessed(b *testing.B) {
	deduper := NewMemoryDeduper(5 * time.Minute)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		deduper.TryMarkProcessed(int64(i%1000), int64(i))
	}
}

// BenchmarkIsProcessed 基准测试检查消息性能
func BenchmarkIsProcessed(b *testing.B) {
	deduper := NewMemoryDeduper(5 * time.Minute)

	// 预填充数据
	for i := 0; i < 1000; i++ {
		deduper.TryMarkProcessed(int64(i), int64(i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		deduper.IsProcessed(int64(i%1000), int64(i%1000))
	}
}

// BenchmarkCleanExpired 基准测试清理过期条目性能
func BenchmarkCleanExpired(b *testing.B) {
	deduper := NewMemoryDeduper(5 * time.Minute)

	// 预填充数据
	for i := 0; i < 10000; i++ {
		deduper.TryMarkProcessed(int64(i), int64(i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		deduper.CleanExpired()
	}
}
