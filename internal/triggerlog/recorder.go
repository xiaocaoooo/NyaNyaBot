package triggerlog

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Recorder 负责记录触发器执行日志
type Recorder struct {
	logger *slog.Logger

	mu      sync.RWMutex
	queue   Queue
	store   *Store
	started bool
	stopCh  chan struct{}
	wg      sync.WaitGroup

	// 追踪正在执行的触发器
	traces sync.Map // map[traceID]*TraceContext

	// 插件触发日志队列和追踪
	pluginQueue  chan *PluginTriggerLog
	pluginTraces sync.Map // map[traceID]*PluginTraceContext
}

// TraceContext 追踪上下文
type TraceContext struct {
	Log       TriggerLog
	StartTime time.Time
}

// PluginTraceContext 插件追踪上下文
type PluginTraceContext struct {
	Log       *PluginTriggerLog
	StartTime time.Time
}

// NewRecorder 创建 Recorder 实例
func NewRecorder(logger *slog.Logger) *Recorder {
	return &Recorder{
		logger: logger,
		store:  NewStore(logger),
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
	r.pluginQueue = make(chan *PluginTriggerLog, 10000)
	r.stopCh = make(chan struct{})
	r.started = true

	r.wg.Add(2)
	go r.worker(ctx)
	go r.pluginWorker(ctx)

	r.logger.Info("triggerlog recorder started")
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
	pluginQueue := r.pluginQueue
	r.started = false
	r.mu.Unlock()

	// 等待 worker 退出
	r.wg.Wait()

	// 关闭队列
	if queue != nil {
		_ = queue.Close(ctx)
	}
	if pluginQueue != nil {
		close(pluginQueue)
	}

	// 关闭存储
	r.store.Close()

	r.logger.Info("triggerlog recorder stopped")
	return nil
}

// BeginTrace 开始记录触发器执行
// 返回 traceID，用于后续调用 EndTrace
func (r *Recorder) BeginTrace(ctx context.Context, triggerID int64, triggerName string, groupID int64, groupName string, userID int64, userName string, selfID int64, messageID string, rawMessage string, matchedText string) string {
	r.mu.RLock()
	started := r.started
	r.mu.RUnlock()

	if !started {
		return ""
	}

	traceID := generateTraceID()
	startTime := time.Now()

	trace := &TraceContext{
		Log: TriggerLog{
			TriggerID:   triggerID,
			TriggerName: triggerName,
			GroupID:     groupID,
			GroupName:   groupName,
			UserID:      userID,
			UserName:    userName,
			SelfID:      selfID,
			MessageID:   messageID,
			RawMessage:  rawMessage,
			MatchedText: matchedText,
			StartTime:   startTime,
		},
		StartTime: startTime,
	}

	r.traces.Store(traceID, trace)
	return traceID
}

// EndTrace 结束记录触发器执行
func (r *Recorder) EndTrace(ctx context.Context, traceID string, response string, success bool, errorMessage string) {
	r.mu.RLock()
	started := r.started
	queue := r.queue
	r.mu.RUnlock()

	if !started || queue == nil || traceID == "" {
		return
	}

	// 获取追踪上下文
	value, ok := r.traces.LoadAndDelete(traceID)
	if !ok {
		r.logger.Warn("trace not found", "trace_id", traceID)
		return
	}

	trace := value.(*TraceContext)
	endTime := time.Now()
	duration := endTime.Sub(trace.StartTime).Milliseconds()

	// 完善日志
	trace.Log.Response = response
	trace.Log.EndTime = endTime
	trace.Log.Duration = duration
	trace.Log.Success = success
	trace.Log.ErrorMessage = errorMessage
	trace.Log.CreatedAt = endTime

	// 非阻塞入队
	if err := queue.Enqueue(ctx, trace.Log); err != nil {
		if err == ErrQueueFull {
			r.logger.Warn("triggerlog queue full, dropping log")
		}
	}
}

// RecordTrace 直接记录一条完整的日志（不使用 BeginTrace/EndTrace）
func (r *Recorder) RecordTrace(ctx context.Context, log TriggerLog) {
	r.mu.RLock()
	started := r.started
	queue := r.queue
	r.mu.RUnlock()

	if !started || queue == nil {
		return
	}

	// 设置创建时间
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now()
	}

	// 非阻塞入队
	if err := queue.Enqueue(ctx, log); err != nil {
		if err == ErrQueueFull {
			r.logger.Warn("triggerlog queue full, dropping log")
		}
	}
}

// BeginPluginTrace 开始记录插件触发执行
// 返回 traceID，用于后续调用 EndPluginTrace
func (r *Recorder) BeginPluginTrace(ctx context.Context, pluginID, listenerID, listenerType string, groupID, userID, selfID, messageID int64, messageSeq string, triggerData map[string]interface{}) string {
	r.mu.RLock()
	started := r.started
	r.mu.RUnlock()

	if !started {
		return ""
	}

	traceID := generateTraceID()
	startTime := time.Now()

	// 确保 triggerData 不为 nil
	if triggerData == nil {
		triggerData = make(map[string]interface{})
	}

	trace := &PluginTraceContext{
		Log: &PluginTriggerLog{
			TraceID:      traceID,
			PluginID:     pluginID,
			ListenerID:   listenerID,
			ListenerType: listenerType,
			GroupID:      groupID,
			UserID:       userID,
			SelfID:       selfID,
			MessageID:    messageID,
			MessageSeq:   messageSeq,
			TriggerData:  triggerData,
			TriggeredAt:  startTime,
		},
		StartTime: startTime,
	}

	r.pluginTraces.Store(traceID, trace)
	return traceID
}

// EndPluginTrace 结束记录插件触发执行
func (r *Recorder) EndPluginTrace(ctx context.Context, traceID string, success bool, errorMessage string) {
	r.mu.RLock()
	started := r.started
	pluginQueue := r.pluginQueue
	r.mu.RUnlock()

	if !started || pluginQueue == nil || traceID == "" {
		return
	}

	// 获取追踪上下文
	value, ok := r.pluginTraces.LoadAndDelete(traceID)
	if !ok {
		r.logger.Warn("plugin trace not found", "trace_id", traceID)
		return
	}

	trace := value.(*PluginTraceContext)
	endTime := time.Now()
	duration := int(endTime.Sub(trace.StartTime).Milliseconds())

	// 完善日志
	trace.Log.Success = success
	trace.Log.DurationMs = duration
	trace.Log.ErrorMessage = errorMessage
	trace.Log.RecordedAt = endTime

	// 非阻塞入队
	select {
	case pluginQueue <- trace.Log:
		// 成功入队
	default:
		r.logger.Warn("plugin triggerlog queue full, dropping log", "trace_id", traceID)
	}
}

// RecordPluginTrace 直接记录一条完整的插件触发日志（不使用 BeginPluginTrace/EndPluginTrace）
func (r *Recorder) RecordPluginTrace(ctx context.Context, log *PluginTriggerLog) {
	r.mu.RLock()
	started := r.started
	pluginQueue := r.pluginQueue
	r.mu.RUnlock()

	if !started || pluginQueue == nil || log == nil {
		return
	}

	// 设置时间戳
	if log.TriggeredAt.IsZero() {
		log.TriggeredAt = time.Now()
	}
	if log.RecordedAt.IsZero() {
		log.RecordedAt = time.Now()
	}
	if log.TriggerData == nil {
		log.TriggerData = make(map[string]interface{})
	}

	// 非阻塞入队
	select {
	case pluginQueue <- log:
		// 成功入队
	default:
		r.logger.Warn("plugin triggerlog queue full, dropping log", "trace_id", log.TraceID)
	}
}

// Reconnect 重新连接数据库
func (r *Recorder) Reconnect(ctx context.Context, databaseURI string) error {
	return r.store.Reconnect(ctx, databaseURI)
}

// Query 查询日志
func (r *Recorder) Query(ctx context.Context, params QueryParams) ([]TriggerLog, error) {
	return r.store.Query(ctx, params)
}

// QueryPluginTriggerLogs 查询插件触发日志
func (r *Recorder) QueryPluginTriggerLogs(ctx context.Context, params PluginTriggerLogQueryParams) ([]PluginTriggerLog, error) {
	return r.store.QueryPluginTriggerLogs(ctx, params)
}

// GetStatistics 获取统计信息
func (r *Recorder) GetStatistics(ctx context.Context, params QueryParams) (*Statistics, error) {
	return r.store.GetStatistics(ctx, params)
}

// GetPluginTriggerLogStatistics 获取插件触发日志统计信息
func (r *Recorder) GetPluginTriggerLogStatistics(ctx context.Context, params PluginTriggerLogQueryParams) (*PluginTriggerLogStatistics, error) {
	return r.store.GetPluginTriggerLogStatistics(ctx, params)
}

// worker 批量消费队列、批量写 PostgreSQL
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

// processBatch 处理一批日志
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

	// 提取日志
	logs := make([]TriggerLog, 0, len(queued))
	for _, q := range queued {
		logs = append(logs, q.Log)
	}

	// 批量写入数据库
	if err := r.store.SaveBatch(ctx, logs); err != nil {
		if err == ErrStoreNotConnected {
			// Store 未连接时静默丢弃日志（优雅降级）
			// 这是正常情况：数据库未配置或禁用时
			r.logger.Debug("store not connected, logs discarded", "count", len(logs))
		} else {
			r.logger.Error("failed to save batch", "error", err, "count", len(logs))
		}
		// 即使保存失败也要 Ack，避免队列堆积
		_ = queue.Ack(ctx, queued)
		return
	}

	// Ack
	_ = queue.Ack(ctx, queued)

	r.logger.Debug("saved log batch", "count", len(logs))
}

// pluginWorker 批量消费插件触发日志队列、批量写 PostgreSQL
func (r *Recorder) pluginWorker(ctx context.Context) {
	defer r.wg.Done()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.processPluginBatch(ctx)
		}
	}
}

// processPluginBatch 处理一批插件触发日志
func (r *Recorder) processPluginBatch(ctx context.Context) {
	r.mu.RLock()
	pluginQueue := r.pluginQueue
	r.mu.RUnlock()

	if pluginQueue == nil {
		return
	}

	// 批量消费（最多 100 条）
	batchCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	var logs []*PluginTriggerLog

	// 阻塞等待第一条日志
	select {
	case log, ok := <-pluginQueue:
		if !ok {
			return
		}
		logs = append(logs, log)
	case <-batchCtx.Done():
		return
	}

	// 尽量 drain 更多日志（非阻塞）
	for len(logs) < 100 {
		select {
		case log, ok := <-pluginQueue:
			if !ok {
				break
			}
			logs = append(logs, log)
		default:
			goto saveBatch
		}
	}

saveBatch:
	if len(logs) == 0 {
		return
	}

	// 批量写入数据库
	if err := r.store.SavePluginTriggerLogBatch(ctx, logs); err != nil {
		if err == ErrStoreNotConnected {
			// Store 未连接时静默丢弃日志（优雅降级）
			r.logger.Debug("store not connected, plugin logs discarded", "count", len(logs))
		} else {
			r.logger.Error("failed to save plugin log batch", "error", err, "count", len(logs))
		}
		return
	}

	r.logger.Debug("saved plugin log batch", "count", len(logs))
}

// GetStats 获取统计信息（用于监控）
func (r *Recorder) GetStats() map[string]interface{} {
	r.mu.RLock()
	started := r.started
	r.mu.RUnlock()

	// 统计正在追踪的数量
	traceCount := 0
	r.traces.Range(func(key, value interface{}) bool {
		traceCount++
		return true
	})

	// 统计插件追踪数量
	pluginTraceCount := 0
	r.pluginTraces.Range(func(key, value interface{}) bool {
		pluginTraceCount++
		return true
	})

	return map[string]interface{}{
		"started":            started,
		"trace_count":        traceCount,
		"plugin_trace_count": pluginTraceCount,
	}
}

// generateTraceID 生成追踪 ID
func generateTraceID() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), time.Now().Unix())
}
