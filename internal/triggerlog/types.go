package triggerlog

import (
	"context"
	"errors"
	"time"
)

// TriggerLog 表示一条触发器执行记录
type TriggerLog struct {
	ID           int64     `json:"id"`
	TriggerID    int64     `json:"trigger_id"`
	TriggerName  string    `json:"trigger_name"`
	GroupID      int64     `json:"group_id"`
	GroupName    string    `json:"group_name"`
	UserID       int64     `json:"user_id"`
	UserName     string    `json:"user_name"`
	SelfID       int64     `json:"self_id"`
	MessageID    string    `json:"message_id"`
	RawMessage   string    `json:"raw_message"`
	MatchedText  string    `json:"matched_text"`
	Response     string    `json:"response"`
	StartTime    time.Time `json:"start_time"`
	EndTime      time.Time `json:"end_time"`
	Duration     int64     `json:"duration"` // 毫秒
	Success      bool      `json:"success"`
	ErrorMessage string    `json:"error_message"`
	CreatedAt    time.Time `json:"created_at"`
}

// QueryParams 查询参数
type QueryParams struct {
	TriggerID *int64     `json:"trigger_id"`
	GroupID   *int64     `json:"group_id"`
	UserID    *int64     `json:"user_id"`
	SelfID    *int64     `json:"self_id"`
	Success   *bool      `json:"success"`
	StartTime *time.Time `json:"start_time"`
	EndTime   *time.Time `json:"end_time"`
	Limit     int        `json:"limit"`
	Offset    int        `json:"offset"`
	OrderBy   string     `json:"order_by"` // 需要白名单验证
	OrderDesc bool       `json:"order_desc"`
}

// Statistics 统计信息
type Statistics struct {
	TotalCount   int64         `json:"total_count"`
	SuccessCount int64         `json:"success_count"`
	FailureCount int64         `json:"failure_count"`
	AvgDuration  float64       `json:"avg_duration"` // 毫秒
	TopTriggers  []TriggerStat `json:"top_triggers"`
	TopGroups    []GroupStat   `json:"top_groups"`
	HourlyStats  []HourlyStat  `json:"hourly_stats"`
	DailyStats   []DailyStat   `json:"daily_stats"`
}

// TriggerStat 触发器统计
type TriggerStat struct {
	TriggerID   int64   `json:"trigger_id"`
	TriggerName string  `json:"trigger_name"`
	Count       int64   `json:"count"`
	SuccessRate float64 `json:"success_rate"`
	AvgDuration float64 `json:"avg_duration"`
}

// GroupStat 群组统计
type GroupStat struct {
	GroupID     int64   `json:"group_id"`
	GroupName   string  `json:"group_name"`
	Count       int64   `json:"count"`
	SuccessRate float64 `json:"success_rate"`
}

// HourlyStat 小时统计
type HourlyStat struct {
	Hour         time.Time `json:"hour"`
	Count        int64     `json:"count"`
	SuccessCount int64     `json:"success_count"`
	FailureCount int64     `json:"failure_count"`
}

// DailyStat 日统计
type DailyStat struct {
	Date         time.Time `json:"date"`
	Count        int64     `json:"count"`
	SuccessCount int64     `json:"success_count"`
	FailureCount int64     `json:"failure_count"`
}

// PluginTriggerLog 插件触发日志记录
type PluginTriggerLog struct {
	ID           int64                  `json:"id"`
	TraceID      string                 `json:"trace_id"`
	PluginID     string                 `json:"plugin_id"`
	ListenerID   string                 `json:"listener_id"`
	ListenerType string                 `json:"listener_type"`
	GroupID      int64                  `json:"group_id"`
	UserID       int64                  `json:"user_id"`
	SelfID       int64                  `json:"self_id"`
	MessageID    int64                  `json:"message_id"`
	MessageSeq   string                 `json:"message_seq"`
	TriggerData  map[string]interface{} `json:"trigger_data"`
	Success      bool                   `json:"success"`
	DurationMs   int                    `json:"duration_ms"`
	ErrorMessage string                 `json:"error_message"`
	TriggeredAt  time.Time              `json:"triggered_at"`
	RecordedAt   time.Time              `json:"recorded_at"`
}

// PluginTriggerLogQueryParams 插件触发日志查询参数
type PluginTriggerLogQueryParams struct {
	GroupID      *int64     `json:"group_id"`
	UserID       *int64     `json:"user_id"`
	PluginID     *string    `json:"plugin_id"`
	ListenerID   *string    `json:"listener_id"`
	ListenerType *string    `json:"listener_type"`
	TraceID      *string    `json:"trace_id"`
	MessageSeq   *string    `json:"message_seq"`
	Success      *bool      `json:"success"`
	StartTime    *time.Time `json:"start_time"`
	EndTime      *time.Time `json:"end_time"`
	Limit        int        `json:"limit"`
	Offset       int        `json:"offset"`
	OrderBy      string     `json:"order_by"` // 需要白名单验证
	OrderDesc    bool       `json:"order_desc"`
}

// PluginTriggerLogStatistics 插件触发日志统计信息
type PluginTriggerLogStatistics struct {
	TotalCount    int64   `json:"total_count"`
	SuccessCount  int64   `json:"success_count"`
	FailedCount   int64   `json:"failed_count"`
	AvgDurationMs float64 `json:"avg_duration_ms"`
}

// QueuedLog 表示队列中的日志
type QueuedLog struct {
	ID  string     // 消息 ID（内存队列序号）
	Log TriggerLog // 日志内容
}

// Queue 定义日志队列接口
type Queue interface {
	// Enqueue 将日志加入队列
	Enqueue(ctx context.Context, log TriggerLog) error

	// ConsumeBatch 批量消费日志，最多返回 max 条
	ConsumeBatch(ctx context.Context, max int) ([]QueuedLog, error)

	// Ack 确认日志已处理
	Ack(ctx context.Context, logs []QueuedLog) error

	// Close 关闭队列
	Close(ctx context.Context) error
}

// 错误定义
var (
	ErrQueueFull         = errors.New("triggerlog: queue is full")
	ErrQueueClosed       = errors.New("triggerlog: queue is closed")
	ErrStoreNotConnected = errors.New("triggerlog: store not connected")
	ErrInvalidOrderBy    = errors.New("triggerlog: invalid order_by field")
)
