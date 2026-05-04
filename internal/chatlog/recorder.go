package chatlog

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/xiaocaoooo/nyanyabot/internal/onebot/ob11"
)

// Recorder 负责接收事件、解析、入队、批量消费、补全群名、写入数据库
type Recorder struct {
	logger *slog.Logger
	caller OneBotCaller

	mu      sync.RWMutex
	queue   Queue
	store   *Store
	cache   *GroupCache
	started bool
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

// NewRecorder 创建 Recorder 实例
func NewRecorder(logger *slog.Logger, caller OneBotCaller) *Recorder {
	return &Recorder{
		logger: logger,
		caller: caller,
		store:  NewStore(logger),
		cache:  NewGroupCache(1 * time.Hour),
	}
}

// Start 启动 recorder worker
func (r *Recorder) Start(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.started {
		return
	}

	r.queue = NewMemoryQueue(10000)
	r.stopCh = make(chan struct{})
	r.started = true

	r.wg.Add(1)
	go r.worker(ctx)

	r.logger.Info("chatlog recorder started")
}

// Stop 停止 recorder
func (r *Recorder) Stop(ctx context.Context) error {
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		return nil
	}

	close(r.stopCh)
	queue := r.queue
	r.started = false
	r.mu.Unlock()

	// 等待 worker 退出
	r.wg.Wait()

	// 关闭队列
	if queue != nil {
		_ = queue.Close(ctx)
	}

	// 关闭存储
	r.store.Close()

	r.logger.Info("chatlog recorder stopped")
	return nil
}

// HandleEvent 处理事件（非阻塞）
// 若未启用/无 store 则 no-op；只解析并入队，不调用 OneBot API，不写 DB
func (r *Recorder) HandleEvent(ctx context.Context, raw ob11.Event) {
	r.mu.RLock()
	started := r.started
	queue := r.queue
	r.mu.RUnlock()

	if !started || queue == nil {
		return
	}

	// 解析消息
	msg, err := ParseGroupMessage(raw)
	if err != nil {
		if err != ErrInvalidSeq {
			r.logger.Warn("failed to parse group message", "error", err)
		}
		return
	}

	if msg == nil {
		return // 不是群消息
	}

	// 非阻塞入队
	if err := queue.Enqueue(ctx, *msg); err != nil {
		if err == ErrQueueFull {
			r.logger.Warn("chatlog queue full, dropping message")
		}
	}
}

// Reconnect 重新连接数据库
func (r *Recorder) Reconnect(ctx context.Context, databaseURI string) error {
	return r.store.Reconnect(ctx, databaseURI)
}

// worker 批量消费队列、补全 group_name、批量写 PostgreSQL
func (r *Recorder) worker(ctx context.Context) {
	defer r.wg.Done()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.processBatch(ctx)
		}
	}
}

// processBatch 处理一批消息
func (r *Recorder) processBatch(ctx context.Context) {
	r.mu.RLock()
	queue := r.queue
	r.mu.RUnlock()

	if queue == nil {
		return
	}

	// 批量消费（最多 100 条）
	batchCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	queued, err := queue.ConsumeBatch(batchCtx, 100)
	if err != nil {
		if err != context.DeadlineExceeded && err != context.Canceled {
			r.logger.Warn("failed to consume batch", "error", err)
		}
		return
	}

	if len(queued) == 0 {
		return
	}

	// 补全 group_name
	messages := make([]GroupMessage, 0, len(queued))
	for _, q := range queued {
		msg := q.Message
		if msg.GroupName == "" {
			msg.GroupName = r.fetchGroupName(ctx, msg.GroupID, msg.SelfID)
		}
		messages = append(messages, msg)
	}

	// 批量写入数据库
	if err := r.store.SaveBatch(ctx, messages); err != nil {
		if err == ErrStoreNotConnected {
			r.logger.Warn("store not connected, messages not saved", "count", len(messages))
		} else {
			r.logger.Error("failed to save batch", "error", err, "count", len(messages))
		}
		return
	}

	// Ack
	_ = queue.Ack(ctx, queued)

	r.logger.Debug("saved message batch", "count", len(messages))
}

// fetchGroupName 获取群名称（缓存优先，必要时调用 get_group_info）
func (r *Recorder) fetchGroupName(ctx context.Context, groupID int64, selfID int64) string {
	// 先查缓存
	if name, ok := r.cache.Get(groupID); ok {
		return name
	}

	// 调用 API
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	params := map[string]interface{}{
		"group_id": groupID,
	}

	data, err := r.caller.CallAPIWithBot(ctx, selfID, "get_group_info", params)
	if err != nil {
		r.logger.Warn("failed to get group info", "group_id", groupID, "self_id", selfID, "error", err)
		return ""
	}

	// 解析响应
	var result struct {
		GroupName string `json:"group_name"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		r.logger.Warn("failed to parse group info", "group_id", groupID, "error", err)
		return ""
	}

	// 更新缓存
	if result.GroupName != "" {
		r.cache.Set(groupID, result.GroupName)
	}

	return result.GroupName
}

// GetStats 获取统计信息（用于监控）
func (r *Recorder) GetStats() map[string]interface{} {
	r.mu.RLock()
	started := r.started
	r.mu.RUnlock()

	return map[string]interface{}{
		"started": started,
	}
}
