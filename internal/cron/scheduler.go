package cron

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/robfig/cron/v3"
	"github.com/xiaocaoooo/nyanyabot/internal/config"
	"github.com/xiaocaoooo/nyanyabot/internal/onebot/ob11"
	"github.com/xiaocaoooo/nyanyabot/internal/plugin"
)

// TraceProvider 提供追踪功能的接口
type TraceProvider interface {
	BeginTrace(traceID, pluginID, listenerID, traceType string, data map[string]interface{})
	EndTrace(traceID string)
	GenerateTraceID() string
}

// TraceIDSetter 允许设置 TraceID 的接口
type TraceIDSetter interface {
	SetTraceID(traceID string)
}

// Scheduler 管理插件的 cron 触发器
type Scheduler struct {
	pm            *plugin.Manager
	logger        *slog.Logger
	cron          *cron.Cron
	mu            sync.RWMutex
	entries       map[string]cron.EntryID // key: pluginID:cronID
	getConfig     func() config.AppConfig
	traceProvider TraceProvider
}

// NewScheduler 创建一个新的 cron 调度器
func NewScheduler(pm *plugin.Manager, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		pm:      pm,
		logger:  logger,
		entries: make(map[string]cron.EntryID),
		cron:    cron.New(cron.WithSeconds()), // 支持秒级精度
	}
}

// SetConfigProvider 设置配置提供函数
func (s *Scheduler) SetConfigProvider(fn func() config.AppConfig) {
	s.getConfig = fn
}

// SetTraceProvider 设置追踪提供者
func (s *Scheduler) SetTraceProvider(tp TraceProvider) {
	s.traceProvider = tp
}

// Start 启动 cron 调度器
func (s *Scheduler) Start() {
	s.cron.Start()
	s.logger.Info("cron scheduler started")
}

// Stop 停止 cron 调度器
func (s *Scheduler) Stop() {
	ctx := s.cron.Stop()
	<-ctx.Done()
	s.logger.Info("cron scheduler stopped")
}

// RegisterAllPlugins 注册所有已加载插件的 cron 任务
func (s *Scheduler) RegisterAllPlugins() {
	entries := s.pm.Entries()
	for pluginID, desc := range entries {
		s.RegisterPlugin(pluginID, desc)
	}
}

// RegisterPlugin 注册单个插件的所有 cron 任务
func (s *Scheduler) RegisterPlugin(pluginID string, desc plugin.Descriptor) {
	for _, c := range desc.Crons {
		s.addCronJob(pluginID, c)
	}
}

// UnregisterPlugin 注销单个插件的所有 cron 任务
func (s *Scheduler) UnregisterPlugin(pluginID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 删除该插件的所有任务
	for key, entryID := range s.entries {
		if isPluginKey(key, pluginID) {
			s.cron.Remove(entryID)
			delete(s.entries, key)
			s.logger.Info("removed cron job", "plugin_id", pluginID, "key", key)
		}
	}
}

// RefreshPlugin 刷新单个插件的 cron 任务（用于配置更新后）
func (s *Scheduler) RefreshPlugin(pluginID string, desc plugin.Descriptor) {
	// 先注销旧的
	s.UnregisterPlugin(pluginID)
	// 再注册新的
	s.RegisterPlugin(pluginID, desc)
}

// addCronJob 添加单个 cron 任务
func (s *Scheduler) addCronJob(pluginID string, c plugin.CronListener) {
	key := makeCronKey(pluginID, c.ID)

	// 检查是否已存在
	s.mu.Lock()
	if existingID, ok := s.entries[key]; ok {
		s.cron.Remove(existingID)
		delete(s.entries, key)
	}
	s.mu.Unlock()

	// 获取插件实例
	p, _, ok := s.pm.Get(pluginID)
	if !ok {
		s.logger.Error("plugin not found for cron job", "plugin_id", pluginID, "cron_id", c.ID)
		return
	}

	// 创建任务函数
	job := func() {
		// 检查 cron 是否被禁用
		if s.getConfig != nil {
			cfg := s.getConfig()
			// 1. 检查插件整体是否被禁用
			if !cfg.IsPluginEnabled(pluginID) {
				s.logger.Debug("plugin disabled, skipping cron job",
					"plugin_id", pluginID,
					"cron_id", c.ID,
				)
				return
			}
			// 2. 检查特定 cron 是否被禁用
			if !cfg.IsCronEnabled(pluginID, c.ID) {
				s.logger.Debug("cron job disabled, skipping",
					"plugin_id", pluginID,
					"cron_id", c.ID,
				)
				return
			}
		}

		s.logger.Info("cron job triggered",
			"plugin_id", pluginID,
			"cron_id", c.ID,
			"schedule", c.Schedule,
		)

		// 构造一个虚拟的 cron 触发事件
		event := ob11.Event(s.buildCronEvent(pluginID, c))

		// 生成 TraceID 并注册追踪记录
		traceID := ""
		if s.traceProvider != nil {
			traceID = s.traceProvider.GenerateTraceID()
			traceData := map[string]interface{}{
				"schedule":  c.Schedule,
				"cron_name": c.Name,
			}
			s.traceProvider.BeginTrace(traceID, pluginID, c.ID, "cron", traceData)
			defer s.traceProvider.EndTrace(traceID)
		}

		// 设置 TraceID（如果插件支持）
		if setter, ok := p.(TraceIDSetter); ok {
			setter.SetTraceID(traceID)
		}

		// 调用插件的 Handle 方法
		ctx := context.Background()
		if _, err := p.Handle(ctx, c.ID, event, nil); err != nil {
			s.logger.Error("cron job handler error",
				"plugin_id", pluginID,
				"cron_id", c.ID,
				"error", err,
			)
		}
	}

	// 添加 cron 任务
	entryID, err := s.cron.AddFunc(c.Schedule, job)
	if err != nil {
		s.logger.Error("failed to add cron job",
			"plugin_id", pluginID,
			"cron_id", c.ID,
			"schedule", c.Schedule,
			"error", err,
		)
		return
	}

	s.mu.Lock()
	s.entries[key] = entryID
	s.mu.Unlock()

	s.logger.Info("cron job registered",
		"plugin_id", pluginID,
		"cron_id", c.ID,
		"schedule", c.Schedule,
		"name", c.Name,
	)
}

// buildCronEvent 构建 cron 触发事件
func (s *Scheduler) buildCronEvent(pluginID string, c plugin.CronListener) []byte {
	event := map[string]any{
		"post_type":     "cron",
		"time":          0,
		"self_id":       0,
		"plugin_id":     pluginID,
		"cron_id":       c.ID,
		"cron_name":     c.Name,
		"cron_schedule": c.Schedule,
	}
	data, _ := json.Marshal(event)
	return data
}

func makeCronKey(pluginID, cronID string) string {
	return pluginID + ":" + cronID
}

func isPluginKey(key, pluginID string) bool {
	return len(key) > len(pluginID)+1 && key[:len(pluginID)+1] == pluginID+":"
}

// GetEntries 获取当前所有注册的 cron 任务信息
func (s *Scheduler) GetEntries() []CronEntryInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries := s.cron.Entries()
	result := make([]CronEntryInfo, 0, len(entries))

	for key, id := range s.entries {
		for _, entry := range entries {
			if entry.ID == id {
				result = append(result, CronEntryInfo{
					Key:       key,
					NextRun:   entry.Next,
					PrevRun:   entry.Prev,
					Effective: entry.Valid(),
				})
				break
			}
		}
	}
	return result
}

// CronEntryInfo 表示一个 cron 任务的信息
type CronEntryInfo struct {
	Key       string
	NextRun   any
	PrevRun   any
	Effective bool
}
