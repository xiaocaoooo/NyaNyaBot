package chatlog

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// GroupMessage 表示一条群消息记录
type GroupMessage struct {
	GroupID         int64             `json:"group_id"`
	RealSeq         string            `json:"real_seq"`
	GroupName       string            `json:"group_name"`
	UserID          int64             `json:"user_id"`
	UserDisplayName string            `json:"user_display_name"`
	RawMessage      string            `json:"raw_message"`
	MessageSegments []json.RawMessage `json:"message_segments"`
	RecordedAt      time.Time         `json:"recorded_at"`
}

// QueuedMessage 表示队列中的消息（面向未来 Valkey Streams）
type QueuedMessage struct {
	ID      string       // 消息 ID（Valkey Stream ID 或内存队列序号）
	Message GroupMessage // 消息内容
}

// Queue 定义消息队列接口（面向未来 Valkey Streams）
type Queue interface {
	// Enqueue 将消息加入队列
	Enqueue(ctx context.Context, msg GroupMessage) error

	// ConsumeBatch 批量消费消息，最多返回 max 条
	ConsumeBatch(ctx context.Context, max int) ([]QueuedMessage, error)

	// Ack 确认消息已处理
	Ack(ctx context.Context, messages []QueuedMessage) error

	// Close 关闭队列
	Close(ctx context.Context) error
}

// OneBotCaller 定义 OneBot API 调用接口
type OneBotCaller interface {
	// CallAPI 调用 OneBot API
	CallAPI(ctx context.Context, action string, params interface{}) (json.RawMessage, error)
}

// 错误定义
var (
	ErrQueueFull         = errors.New("chatlog: queue is full")
	ErrQueueClosed       = errors.New("chatlog: queue is closed")
	ErrInvalidSeq        = errors.New("chatlog: invalid or missing real_seq")
	ErrStoreNotConnected = errors.New("chatlog: store not connected")
)
