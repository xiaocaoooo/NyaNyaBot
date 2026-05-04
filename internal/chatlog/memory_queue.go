package chatlog

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// MemoryQueue 实现基于内存 channel 的消息队列
type MemoryQueue struct {
	ch       chan GroupMessage
	closed   atomic.Bool
	mu       sync.Mutex
	nextID   int64
	capacity int
}

// NewMemoryQueue 创建内存队列
func NewMemoryQueue(capacity int) *MemoryQueue {
	return &MemoryQueue{
		ch:       make(chan GroupMessage, capacity),
		capacity: capacity,
	}
}

// Enqueue 非阻塞入队，队列满返回 ErrQueueFull，关闭后返回 ErrQueueClosed
func (q *MemoryQueue) Enqueue(ctx context.Context, msg GroupMessage) error {
	if q.closed.Load() {
		return ErrQueueClosed
	}

	select {
	case q.ch <- msg:
		return nil
	default:
		return ErrQueueFull
	}
}

// ConsumeBatch 批量消费消息，阻塞等待首条，然后尽量 drain 到 max
func (q *MemoryQueue) ConsumeBatch(ctx context.Context, max int) ([]QueuedMessage, error) {
	if max <= 0 {
		return nil, nil
	}

	var result []QueuedMessage

	// 阻塞等待第一条消息
	select {
	case msg, ok := <-q.ch:
		if !ok {
			return nil, ErrQueueClosed
		}
		q.mu.Lock()
		id := q.nextID
		q.nextID++
		q.mu.Unlock()
		result = append(result, QueuedMessage{
			ID:      fmt.Sprintf("%d", id),
			Message: msg,
		})
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// 尽量 drain 更多消息（非阻塞）
	for len(result) < max {
		select {
		case msg, ok := <-q.ch:
			if !ok {
				return result, nil
			}
			q.mu.Lock()
			id := q.nextID
			q.nextID++
			q.mu.Unlock()
			result = append(result, QueuedMessage{
				ID:      fmt.Sprintf("%d", id),
				Message: msg,
			})
		default:
			return result, nil
		}
	}

	return result, nil
}

// Ack 确认消息（内存队列为 no-op）
func (q *MemoryQueue) Ack(ctx context.Context, messages []QueuedMessage) error {
	return nil
}

// Close 关闭队列，拒绝新消息
func (q *MemoryQueue) Close(ctx context.Context) error {
	if q.closed.Swap(true) {
		return nil // 已经关闭
	}
	close(q.ch)
	return nil
}
