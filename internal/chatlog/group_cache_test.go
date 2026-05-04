package chatlog

import (
	"testing"
	"time"
)

func TestGroupCache_GetSet(t *testing.T) {
	cache := NewGroupCache(1 * time.Hour)

	// 初始未命中
	if _, ok := cache.Get(123); ok {
		t.Error("expected cache miss")
	}

	// 设置缓存
	cache.Set(123, "Test Group")

	// 命中
	name, ok := cache.Get(123)
	if !ok {
		t.Error("expected cache hit")
	}
	if name != "Test Group" {
		t.Errorf("expected 'Test Group', got '%s'", name)
	}
}

func TestGroupCache_TTL(t *testing.T) {
	cache := NewGroupCache(100 * time.Millisecond)

	cache.Set(123, "Test Group")

	// 立即读取应该命中
	if _, ok := cache.Get(123); !ok {
		t.Error("expected cache hit")
	}

	// 等待过期
	time.Sleep(150 * time.Millisecond)

	// 应该未命中
	if _, ok := cache.Get(123); ok {
		t.Error("expected cache miss after TTL")
	}
}

func TestGroupCache_Clear(t *testing.T) {
	cache := NewGroupCache(1 * time.Hour)

	cache.Set(123, "Group 1")
	cache.Set(456, "Group 2")

	// 清空
	cache.Clear()

	// 都应该未命中
	if _, ok := cache.Get(123); ok {
		t.Error("expected cache miss after clear")
	}
	if _, ok := cache.Get(456); ok {
		t.Error("expected cache miss after clear")
	}
}

func TestGroupCache_CleanExpired(t *testing.T) {
	cache := NewGroupCache(100 * time.Millisecond)

	cache.Set(123, "Group 1")
	cache.Set(456, "Group 2")

	// 等待过期
	time.Sleep(150 * time.Millisecond)

	// 添加新条目
	cache.Set(789, "Group 3")

	// 清理过期条目
	cache.CleanExpired()

	// 旧条目应该被清理
	if _, ok := cache.Get(123); ok {
		t.Error("expected expired entry to be cleaned")
	}
	if _, ok := cache.Get(456); ok {
		t.Error("expected expired entry to be cleaned")
	}

	// 新条目应该还在
	if _, ok := cache.Get(789); !ok {
		t.Error("expected new entry to remain")
	}
}

func TestGroupCache_Concurrent(t *testing.T) {
	cache := NewGroupCache(1 * time.Hour)

	done := make(chan struct{})

	// 并发写入
	for i := 0; i < 10; i++ {
		go func(id int64) {
			cache.Set(id, "Group")
			done <- struct{}{}
		}(int64(i))
	}

	// 等待所有写入完成
	for i := 0; i < 10; i++ {
		<-done
	}

	// 并发读取
	for i := 0; i < 10; i++ {
		go func(id int64) {
			cache.Get(id)
			done <- struct{}{}
		}(int64(i))
	}

	// 等待所有读取完成
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestGroupCache_UpdateExisting(t *testing.T) {
	cache := NewGroupCache(1 * time.Hour)

	cache.Set(123, "Old Name")

	// 更新
	cache.Set(123, "New Name")

	name, ok := cache.Get(123)
	if !ok {
		t.Error("expected cache hit")
	}
	if name != "New Name" {
		t.Errorf("expected 'New Name', got '%s'", name)
	}
}
