package chatlog

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestMemoryQueue_EnqueueDequeue(t *testing.T) {
	q := NewMemoryQueue(10)
	ctx := context.Background()

	msg := GroupMessage{
		GroupID: 123,
		RealSeq: "1",
	}

	// 入队
	if err := q.Enqueue(ctx, msg); err != nil {
		t.Fatalf("failed to enqueue: %v", err)
	}

	// 消费
	batch, err := q.ConsumeBatch(ctx, 10)
	if err != nil {
		t.Fatalf("failed to consume: %v", err)
	}

	if len(batch) != 1 {
		t.Fatalf("expected 1 message, got %d", len(batch))
	}

	if batch[0].Message.GroupID != 123 {
		t.Errorf("expected group_id 123, got %d", batch[0].Message.GroupID)
	}
}

func TestMemoryQueue_Full(t *testing.T) {
	q := NewMemoryQueue(2)
	ctx := context.Background()

	msg := GroupMessage{GroupID: 123, RealSeq: "1"}

	// 填满队列
	if err := q.Enqueue(ctx, msg); err != nil {
		t.Fatalf("failed to enqueue 1: %v", err)
	}
	if err := q.Enqueue(ctx, msg); err != nil {
		t.Fatalf("failed to enqueue 2: %v", err)
	}

	// 第三条应该失败
	if err := q.Enqueue(ctx, msg); err != ErrQueueFull {
		t.Errorf("expected ErrQueueFull, got %v", err)
	}
}

func TestMemoryQueue_Closed(t *testing.T) {
	q := NewMemoryQueue(10)
	ctx := context.Background()

	// 关闭队列
	if err := q.Close(ctx); err != nil {
		t.Fatalf("failed to close: %v", err)
	}

	// 入队应该失败
	msg := GroupMessage{GroupID: 123, RealSeq: "1"}
	if err := q.Enqueue(ctx, msg); err != ErrQueueClosed {
		t.Errorf("expected ErrQueueClosed, got %v", err)
	}
}

func TestMemoryQueue_ConsumeBatch(t *testing.T) {
	q := NewMemoryQueue(100)
	ctx := context.Background()

	// 入队 5 条消息
	for i := 1; i <= 5; i++ {
		msg := GroupMessage{GroupID: 123, RealSeq: fmt.Sprintf("%d", i)}
		if err := q.Enqueue(ctx, msg); err != nil {
			t.Fatalf("failed to enqueue %d: %v", i, err)
		}
	}

	// 消费最多 3 条
	batch, err := q.ConsumeBatch(ctx, 3)
	if err != nil {
		t.Fatalf("failed to consume: %v", err)
	}

	if len(batch) != 3 {
		t.Errorf("expected 3 messages, got %d", len(batch))
	}

	// 再消费剩余的
	batch, err = q.ConsumeBatch(ctx, 10)
	if err != nil {
		t.Fatalf("failed to consume: %v", err)
	}

	if len(batch) != 2 {
		t.Errorf("expected 2 messages, got %d", len(batch))
	}
}

func TestMemoryQueue_ConsumeBatchBlocking(t *testing.T) {
	q := NewMemoryQueue(10)
	ctx := context.Background()

	done := make(chan struct{})
	var batch []QueuedMessage
	var err error

	// 启动消费者（会阻塞等待）
	go func() {
		batch, err = q.ConsumeBatch(ctx, 10)
		close(done)
	}()

	// 等待一小段时间确保消费者已阻塞
	time.Sleep(50 * time.Millisecond)

	// 入队一条消息
	msg := GroupMessage{GroupID: 123, RealSeq: "1"}
	if err := q.Enqueue(ctx, msg); err != nil {
		t.Fatalf("failed to enqueue: %v", err)
	}

	// 等待消费者完成
	select {
	case <-done:
		if err != nil {
			t.Fatalf("consume failed: %v", err)
		}
		if len(batch) != 1 {
			t.Errorf("expected 1 message, got %d", len(batch))
		}
	case <-time.After(1 * time.Second):
		t.Fatal("consume did not complete")
	}
}

func TestMemoryQueue_ConsumeBatchDrain(t *testing.T) {
	q := NewMemoryQueue(100)
	ctx := context.Background()

	// 快速入队 10 条消息
	for i := 1; i <= 10; i++ {
		msg := GroupMessage{GroupID: 123, RealSeq: fmt.Sprintf("%d", i)}
		if err := q.Enqueue(ctx, msg); err != nil {
			t.Fatalf("failed to enqueue %d: %v", i, err)
		}
	}

	// 消费最多 100 条（应该 drain 所有 10 条）
	batch, err := q.ConsumeBatch(ctx, 100)
	if err != nil {
		t.Fatalf("failed to consume: %v", err)
	}

	if len(batch) != 10 {
		t.Errorf("expected 10 messages, got %d", len(batch))
	}
}

func TestMemoryQueue_Ack(t *testing.T) {
	q := NewMemoryQueue(10)
	ctx := context.Background()

	msg := GroupMessage{GroupID: 123, RealSeq: "1"}
	if err := q.Enqueue(ctx, msg); err != nil {
		t.Fatalf("failed to enqueue: %v", err)
	}

	batch, err := q.ConsumeBatch(ctx, 10)
	if err != nil {
		t.Fatalf("failed to consume: %v", err)
	}

	// Ack 应该是 no-op
	if err := q.Ack(ctx, batch); err != nil {
		t.Errorf("ack failed: %v", err)
	}
}

func TestMemoryQueue_ContextCancellation(t *testing.T) {
	q := NewMemoryQueue(10)
	ctx, cancel := context.WithCancel(context.Background())

	// 立即取消
	cancel()

	// 消费应该返回 context.Canceled
	_, err := q.ConsumeBatch(ctx, 10)
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}
