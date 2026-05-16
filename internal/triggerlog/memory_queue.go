package triggerlog

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// MemoryQueue 实现基于内存 channel 的日志队列
type MemoryQueue struct {
	ch       chan TriggerLog
	closed   atomic.Bool
	mu       sync.Mutex
	nextID   int64
	capacity int
}

// NewMemoryQueue 创建内存队列
func NewMemoryQueue(capacity int) *MemoryQueue {
	return &MemoryQueue{
		ch:       make(chan TriggerLog, capacity),
		capacity: capacity,
	}
}

// Enqueue 非阻塞入队，队列满返回 ErrQueueFull，关闭后返回 ErrQueueClosed
func (q *MemoryQueue) Enqueue(ctx context.Context, log TriggerLog) error {
	if q.closed.Load() {
		return ErrQueueClosed
	}

	select {
	case q.ch <- log:
		return nil
	default:
		return ErrQueueFull
	}
}

// ConsumeBatch 批量消费日志，阻塞等待首条，然后尽量 drain 到 max
func (q *MemoryQueue) ConsumeBatch(ctx context.Context, max int) ([]QueuedLog, error) {
	if max <= 0 {
		return nil, nil
	}

	var result []QueuedLog

	// 阻塞等待第一条日志
	select {
	case log, ok := <-q.ch:
		if !ok {
			return nil, ErrQueueClosed
		}
		q.mu.Lock()
		id := q.nextID
		q.nextID++
		q.mu.Unlock()
		result = append(result, QueuedLog{
			ID:  fmt.Sprintf("%d", id),
			Log: log,
		})
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// 尽量 drain 更多日志（非阻塞）
	for len(result) < max {
		select {
		case log, ok := <-q.ch:
			if !ok {
				return result, nil
			}
			q.mu.Lock()
			id := q.nextID
			q.nextID++
			q.mu.Unlock()
			result = append(result, QueuedLog{
				ID:  fmt.Sprintf("%d", id),
				Log: log,
			})
		default:
			return result, nil
		}
	}

	return result, nil
}

// Ack 确认日志（内存队列为 no-op）
func (q *MemoryQueue) Ack(ctx context.Context, logs []QueuedLog) error {
	return nil
}

// Close 关闭队列，拒绝新日志
func (q *MemoryQueue) Close(ctx context.Context) error {
	if q.closed.Swap(true) {
		return nil // 已经关闭
	}
	close(q.ch)
	return nil
}
