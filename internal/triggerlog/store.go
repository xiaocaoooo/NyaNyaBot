package triggerlog

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 管理 PostgreSQL 连接和日志存储
type Store struct {
	mu     sync.RWMutex
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// NewStore 创建存储实例
func NewStore(logger *slog.Logger) *Store {
	return &Store{
		logger: logger,
	}
}

// Reconnect 重新连接数据库
// 空 URI 关闭并禁用；非空时先创建新 pool、Ping、建表成功后再替换旧 pool
// 日志中不输出完整 URI
func (s *Store) Reconnect(ctx context.Context, uri string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 关闭旧连接
	if s.pool != nil {
		s.pool.Close()
		s.pool = nil
	}

	// 空 URI 表示禁用
	if uri == "" {
		s.logger.Info("triggerlog store disabled")
		return nil
	}

	// 创建新连接池
	pool, err := pgxpool.New(ctx, uri)
	if err != nil {
		s.logger.Error("failed to create pool", "error", err)
		return fmt.Errorf("create pool: %w", err)
	}

	// Ping 测试连接
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		s.logger.Error("failed to ping database", "error", err)
		return fmt.Errorf("ping database: %w", err)
	}

	// 建表
	if err := s.createTable(ctx, pool); err != nil {
		pool.Close()
		s.logger.Error("failed to create table", "error", err)
		return fmt.Errorf("create table: %w", err)
	}

	// 替换旧 pool
	s.pool = pool

	// 日志中隐藏敏感信息
	maskedURI := maskURI(uri)
	s.logger.Info("triggerlog store connected", "uri", maskedURI)

	return nil
}

// createTable 创建表和索引
func (s *Store) createTable(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS trigger_logs (
			id BIGSERIAL PRIMARY KEY,
			trigger_id BIGINT NOT NULL,
			trigger_name TEXT NOT NULL DEFAULT '',
			group_id BIGINT NOT NULL,
			group_name TEXT NOT NULL DEFAULT '',
			user_id BIGINT NOT NULL,
			user_name TEXT NOT NULL DEFAULT '',
			self_id BIGINT NOT NULL,
			message_id TEXT NOT NULL DEFAULT '',
			raw_message TEXT NOT NULL DEFAULT '',
			matched_text TEXT NOT NULL DEFAULT '',
			response TEXT NOT NULL DEFAULT '',
			start_time TIMESTAMPTZ NOT NULL,
			end_time TIMESTAMPTZ NOT NULL,
			duration BIGINT NOT NULL DEFAULT 0,
			success BOOLEAN NOT NULL DEFAULT true,
			error_message TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE INDEX IF NOT EXISTS idx_trigger_logs_trigger_id 
			ON trigger_logs (trigger_id);
		
		CREATE INDEX IF NOT EXISTS idx_trigger_logs_group_id 
			ON trigger_logs (group_id);
		
		CREATE INDEX IF NOT EXISTS idx_trigger_logs_user_id 
			ON trigger_logs (user_id);
		
		CREATE INDEX IF NOT EXISTS idx_trigger_logs_self_id 
			ON trigger_logs (self_id);
		
		CREATE INDEX IF NOT EXISTS idx_trigger_logs_created_at 
			ON trigger_logs (created_at);
		
		CREATE INDEX IF NOT EXISTS idx_trigger_logs_start_time 
			ON trigger_logs (start_time);
		
		CREATE INDEX IF NOT EXISTS idx_trigger_logs_success 
			ON trigger_logs (success);

		-- 创建插件触发日志表
		CREATE TABLE IF NOT EXISTS plugin_trigger_logs (
			id BIGSERIAL PRIMARY KEY,
			trace_id TEXT NOT NULL UNIQUE,
			plugin_id TEXT NOT NULL,
			listener_id TEXT NOT NULL,
			listener_type TEXT NOT NULL,
			group_id BIGINT NOT NULL DEFAULT 0,
			user_id BIGINT NOT NULL DEFAULT 0,
			self_id BIGINT NOT NULL DEFAULT 0,
			message_id BIGINT NOT NULL DEFAULT 0,
			message_seq TEXT NOT NULL DEFAULT '',
			trigger_data JSONB NOT NULL DEFAULT '{}'::jsonb,
			success BOOLEAN NOT NULL DEFAULT false,
			duration_ms INTEGER NOT NULL DEFAULT 0,
			error_message TEXT NOT NULL DEFAULT '',
			triggered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		-- 单列索引
		CREATE INDEX IF NOT EXISTS idx_plugin_trigger_logs_trace_id 
			ON plugin_trigger_logs (trace_id);
		
		CREATE INDEX IF NOT EXISTS idx_plugin_trigger_logs_plugin_id 
			ON plugin_trigger_logs (plugin_id);
		
		CREATE INDEX IF NOT EXISTS idx_plugin_trigger_logs_listener_id 
			ON plugin_trigger_logs (listener_id);
		
		CREATE INDEX IF NOT EXISTS idx_plugin_trigger_logs_listener_type 
			ON plugin_trigger_logs (listener_type);
		
		CREATE INDEX IF NOT EXISTS idx_plugin_trigger_logs_group_id 
			ON plugin_trigger_logs (group_id);
		
		CREATE INDEX IF NOT EXISTS idx_plugin_trigger_logs_user_id 
			ON plugin_trigger_logs (user_id);
		
		CREATE INDEX IF NOT EXISTS idx_plugin_trigger_logs_self_id 
			ON plugin_trigger_logs (self_id);
		
		CREATE INDEX IF NOT EXISTS idx_plugin_trigger_logs_triggered_at 
			ON plugin_trigger_logs (triggered_at);
		
		CREATE INDEX IF NOT EXISTS idx_plugin_trigger_logs_success 
			ON plugin_trigger_logs (success);

		-- 复合索引
		CREATE INDEX IF NOT EXISTS idx_plugin_trigger_logs_plugin_triggered 
			ON plugin_trigger_logs (plugin_id, triggered_at);
		
		CREATE INDEX IF NOT EXISTS idx_plugin_trigger_logs_group_triggered 
			ON plugin_trigger_logs (group_id, triggered_at);
		
		CREATE INDEX IF NOT EXISTS idx_plugin_trigger_logs_plugin_listener_triggered 
			ON plugin_trigger_logs (plugin_id, listener_id, triggered_at);
	`)
	return err
}

// SaveBatch 批量保存日志
func (s *Store) SaveBatch(ctx context.Context, logs []TriggerLog) error {
	if len(logs) == 0 {
		return nil
	}

	s.mu.RLock()
	pool := s.pool
	s.mu.RUnlock()

	if pool == nil {
		return ErrStoreNotConnected
	}

	batch := &pgx.Batch{}
	for _, log := range logs {
		batch.Queue(`
			INSERT INTO trigger_logs 
				(trigger_id, trigger_name, group_id, group_name, user_id, user_name, 
				 self_id, message_id, raw_message, matched_text, response, 
				 start_time, end_time, duration, success, error_message, created_at)
			VALUES 
				($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
		`, log.TriggerID, log.TriggerName, log.GroupID, log.GroupName, log.UserID, log.UserName,
			log.SelfID, log.MessageID, log.RawMessage, log.MatchedText, log.Response,
			log.StartTime, log.EndTime, log.Duration, log.Success, log.ErrorMessage, log.CreatedAt)
	}

	results := pool.SendBatch(ctx, batch)
	defer results.Close()

	// 消费所有结果
	for i := 0; i < len(logs); i++ {
		_, err := results.Exec()
		if err != nil {
			s.logger.Warn("failed to save log", "error", err, "index", i)
		}
	}

	return nil
}

// SavePluginTriggerLog 保存单条插件触发日志
func (s *Store) SavePluginTriggerLog(ctx context.Context, log *PluginTriggerLog) error {
	if log == nil {
		return nil
	}

	s.mu.RLock()
	pool := s.pool
	s.mu.RUnlock()

	if pool == nil {
		return ErrStoreNotConnected
	}

	_, err := pool.Exec(ctx, `
		INSERT INTO plugin_trigger_logs 
			(trace_id, plugin_id, listener_id, listener_type, group_id, user_id, 
			 self_id, message_id, message_seq, trigger_data, success, duration_ms, 
			 error_message, triggered_at, recorded_at)
		VALUES 
			($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		ON CONFLICT (trace_id) DO UPDATE SET
			success = EXCLUDED.success,
			duration_ms = EXCLUDED.duration_ms,
			error_message = EXCLUDED.error_message,
			recorded_at = EXCLUDED.recorded_at
	`, log.TraceID, log.PluginID, log.ListenerID, log.ListenerType, log.GroupID, log.UserID,
		log.SelfID, log.MessageID, log.MessageSeq, log.TriggerData, log.Success, log.DurationMs,
		log.ErrorMessage, log.TriggeredAt, log.RecordedAt)

	if err != nil {
		s.logger.Warn("failed to save plugin trigger log", "error", err, "trace_id", log.TraceID)
		return fmt.Errorf("save plugin trigger log: %w", err)
	}

	return nil
}

// SavePluginTriggerLogBatch 批量保存插件触发日志
func (s *Store) SavePluginTriggerLogBatch(ctx context.Context, logs []*PluginTriggerLog) error {
	if len(logs) == 0 {
		return nil
	}

	s.mu.RLock()
	pool := s.pool
	s.mu.RUnlock()

	if pool == nil {
		return ErrStoreNotConnected
	}

	batch := &pgx.Batch{}
	for _, log := range logs {
		if log == nil {
			continue
		}
		batch.Queue(`
			INSERT INTO plugin_trigger_logs 
				(trace_id, plugin_id, listener_id, listener_type, group_id, user_id, 
				 self_id, message_id, message_seq, trigger_data, success, duration_ms, 
				 error_message, triggered_at, recorded_at)
			VALUES 
				($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
			ON CONFLICT (trace_id) DO UPDATE SET
				success = EXCLUDED.success,
				duration_ms = EXCLUDED.duration_ms,
				error_message = EXCLUDED.error_message,
				recorded_at = EXCLUDED.recorded_at
		`, log.TraceID, log.PluginID, log.ListenerID, log.ListenerType, log.GroupID, log.UserID,
			log.SelfID, log.MessageID, log.MessageSeq, log.TriggerData, log.Success, log.DurationMs,
			log.ErrorMessage, log.TriggeredAt, log.RecordedAt)
	}

	results := pool.SendBatch(ctx, batch)
	defer results.Close()

	// 消费所有结果
	for i := 0; i < len(logs); i++ {
		_, err := results.Exec()
		if err != nil {
			if logs[i] != nil {
				s.logger.Warn("failed to save plugin trigger log", "error", err, "index", i, "trace_id", logs[i].TraceID)
			}
		}
	}

	return nil
}

// Query 查询日志
func (s *Store) Query(ctx context.Context, params QueryParams) ([]TriggerLog, error) {
	s.mu.RLock()
	pool := s.pool
	s.mu.RUnlock()

	if pool == nil {
		return nil, ErrStoreNotConnected
	}

	// 验证 OrderBy 字段（白名单）
	if params.OrderBy != "" {
		if !isValidOrderByField(params.OrderBy) {
			return nil, ErrInvalidOrderBy
		}
	} else {
		params.OrderBy = "created_at" // 默认按创建时间排序
	}

	// 构建查询
	query := "SELECT id, trigger_id, trigger_name, group_id, group_name, user_id, user_name, " +
		"self_id, message_id, raw_message, matched_text, response, " +
		"start_time, end_time, duration, success, error_message, created_at " +
		"FROM trigger_logs WHERE 1=1"

	args := []interface{}{}
	argIndex := 1

	if params.TriggerID != nil {
		query += fmt.Sprintf(" AND trigger_id = $%d", argIndex)
		args = append(args, *params.TriggerID)
		argIndex++
	}

	if params.GroupID != nil {
		query += fmt.Sprintf(" AND group_id = $%d", argIndex)
		args = append(args, *params.GroupID)
		argIndex++
	}

	if params.UserID != nil {
		query += fmt.Sprintf(" AND user_id = $%d", argIndex)
		args = append(args, *params.UserID)
		argIndex++
	}

	if params.SelfID != nil {
		query += fmt.Sprintf(" AND self_id = $%d", argIndex)
		args = append(args, *params.SelfID)
		argIndex++
	}

	if params.Success != nil {
		query += fmt.Sprintf(" AND success = $%d", argIndex)
		args = append(args, *params.Success)
		argIndex++
	}

	if params.StartTime != nil {
		query += fmt.Sprintf(" AND start_time >= $%d", argIndex)
		args = append(args, *params.StartTime)
		argIndex++
	}

	if params.EndTime != nil {
		query += fmt.Sprintf(" AND end_time <= $%d", argIndex)
		args = append(args, *params.EndTime)
		argIndex++
	}

	// 排序
	orderDir := "ASC"
	if params.OrderDesc {
		orderDir = "DESC"
	}
	query += fmt.Sprintf(" ORDER BY %s %s", params.OrderBy, orderDir)

	// 分页
	if params.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIndex)
		args = append(args, params.Limit)
		argIndex++
	}

	if params.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argIndex)
		args = append(args, params.Offset)
		argIndex++
	}

	// 执行查询
	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query logs: %w", err)
	}
	defer rows.Close()

	var logs []TriggerLog
	for rows.Next() {
		var log TriggerLog
		err := rows.Scan(
			&log.ID, &log.TriggerID, &log.TriggerName, &log.GroupID, &log.GroupName,
			&log.UserID, &log.UserName, &log.SelfID, &log.MessageID, &log.RawMessage,
			&log.MatchedText, &log.Response, &log.StartTime, &log.EndTime, &log.Duration,
			&log.Success, &log.ErrorMessage, &log.CreatedAt,
		)
		if err != nil {
			s.logger.Warn("failed to scan log", "error", err)
			continue
		}
		logs = append(logs, log)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}

	return logs, nil
}

// GetStatistics 获取统计信息
func (s *Store) GetStatistics(ctx context.Context, params QueryParams) (*Statistics, error) {
	s.mu.RLock()
	pool := s.pool
	s.mu.RUnlock()

	if pool == nil {
		return nil, ErrStoreNotConnected
	}

	stats := &Statistics{}

	// 构建 WHERE 子句
	whereClause, args := buildWhereClause(params)

	// 总体统计
	query := "SELECT COUNT(*), " +
		"SUM(CASE WHEN success THEN 1 ELSE 0 END), " +
		"SUM(CASE WHEN NOT success THEN 1 ELSE 0 END), " +
		"AVG(duration) " +
		"FROM trigger_logs " + whereClause

	err := pool.QueryRow(ctx, query, args...).Scan(
		&stats.TotalCount,
		&stats.SuccessCount,
		&stats.FailureCount,
		&stats.AvgDuration,
	)
	if err != nil {
		return nil, fmt.Errorf("query total stats: %w", err)
	}

	// Top 触发器
	topTriggersQuery := "SELECT trigger_id, trigger_name, COUNT(*), " +
		"CAST(SUM(CASE WHEN success THEN 1 ELSE 0 END) AS FLOAT) / COUNT(*) * 100, " +
		"AVG(duration) " +
		"FROM trigger_logs " + whereClause +
		" GROUP BY trigger_id, trigger_name " +
		"ORDER BY COUNT(*) DESC LIMIT 10"

	rows, err := pool.Query(ctx, topTriggersQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("query top triggers: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var stat TriggerStat
		if err := rows.Scan(&stat.TriggerID, &stat.TriggerName, &stat.Count, &stat.SuccessRate, &stat.AvgDuration); err != nil {
			s.logger.Warn("failed to scan trigger stat", "error", err)
			continue
		}
		stats.TopTriggers = append(stats.TopTriggers, stat)
	}

	// Top 群组
	topGroupsQuery := "SELECT group_id, group_name, COUNT(*), " +
		"CAST(SUM(CASE WHEN success THEN 1 ELSE 0 END) AS FLOAT) / COUNT(*) * 100 " +
		"FROM trigger_logs " + whereClause +
		" GROUP BY group_id, group_name " +
		"ORDER BY COUNT(*) DESC LIMIT 10"

	rows, err = pool.Query(ctx, topGroupsQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("query top groups: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var stat GroupStat
		if err := rows.Scan(&stat.GroupID, &stat.GroupName, &stat.Count, &stat.SuccessRate); err != nil {
			s.logger.Warn("failed to scan group stat", "error", err)
			continue
		}
		stats.TopGroups = append(stats.TopGroups, stat)
	}

	// 小时统计（最近 24 小时）
	hourlyQuery := "SELECT date_trunc('hour', start_time) as hour, COUNT(*), " +
		"SUM(CASE WHEN success THEN 1 ELSE 0 END), " +
		"SUM(CASE WHEN NOT success THEN 1 ELSE 0 END) " +
		"FROM trigger_logs " + whereClause +
		" AND start_time >= NOW() - INTERVAL '24 hours' " +
		"GROUP BY hour ORDER BY hour DESC"

	rows, err = pool.Query(ctx, hourlyQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("query hourly stats: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var stat HourlyStat
		if err := rows.Scan(&stat.Hour, &stat.Count, &stat.SuccessCount, &stat.FailureCount); err != nil {
			s.logger.Warn("failed to scan hourly stat", "error", err)
			continue
		}
		stats.HourlyStats = append(stats.HourlyStats, stat)
	}

	// 日统计（最近 30 天）
	dailyQuery := "SELECT date_trunc('day', start_time) as day, COUNT(*), " +
		"SUM(CASE WHEN success THEN 1 ELSE 0 END), " +
		"SUM(CASE WHEN NOT success THEN 1 ELSE 0 END) " +
		"FROM trigger_logs " + whereClause +
		" AND start_time >= NOW() - INTERVAL '30 days' " +
		"GROUP BY day ORDER BY day DESC"

	rows, err = pool.Query(ctx, dailyQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("query daily stats: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var stat DailyStat
		if err := rows.Scan(&stat.Date, &stat.Count, &stat.SuccessCount, &stat.FailureCount); err != nil {
			s.logger.Warn("failed to scan daily stat", "error", err)
			continue
		}
		stats.DailyStats = append(stats.DailyStats, stat)
	}

	return stats, nil
}

// Close 关闭存储
func (s *Store) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pool != nil {
		s.pool.Close()
		s.pool = nil
	}
}

// buildWhereClause 构建 WHERE 子句
func buildWhereClause(params QueryParams) (string, []interface{}) {
	where := "WHERE 1=1"
	args := []interface{}{}
	argIndex := 1

	if params.TriggerID != nil {
		where += fmt.Sprintf(" AND trigger_id = $%d", argIndex)
		args = append(args, *params.TriggerID)
		argIndex++
	}

	if params.GroupID != nil {
		where += fmt.Sprintf(" AND group_id = $%d", argIndex)
		args = append(args, *params.GroupID)
		argIndex++
	}

	if params.UserID != nil {
		where += fmt.Sprintf(" AND user_id = $%d", argIndex)
		args = append(args, *params.UserID)
		argIndex++
	}

	if params.SelfID != nil {
		where += fmt.Sprintf(" AND self_id = $%d", argIndex)
		args = append(args, *params.SelfID)
		argIndex++
	}

	if params.Success != nil {
		where += fmt.Sprintf(" AND success = $%d", argIndex)
		args = append(args, *params.Success)
		argIndex++
	}

	if params.StartTime != nil {
		where += fmt.Sprintf(" AND start_time >= $%d", argIndex)
		args = append(args, *params.StartTime)
		argIndex++
	}

	if params.EndTime != nil {
		where += fmt.Sprintf(" AND end_time <= $%d", argIndex)
		args = append(args, *params.EndTime)
		argIndex++
	}

	return where, args
}

// isValidOrderByField 验证 OrderBy 字段（白名单）
func isValidOrderByField(field string) bool {
	validFields := map[string]bool{
		// trigger_logs 表字段
		"id":         true,
		"trigger_id": true,
		"group_id":   true,
		"user_id":    true,
		"self_id":    true,
		"start_time": true,
		"end_time":   true,
		"duration":   true,
		"created_at": true,
		// plugin_trigger_logs 表字段
		"trace_id":      true,
		"plugin_id":     true,
		"listener_id":   true,
		"listener_type": true,
		"message_id":    true,
		"triggered_at":  true,
		"recorded_at":   true,
		"duration_ms":   true,
	}
	return validFields[field]
}

// QueryPluginTriggerLogs 查询插件触发日志
func (s *Store) QueryPluginTriggerLogs(ctx context.Context, params PluginTriggerLogQueryParams) ([]PluginTriggerLog, error) {
	s.mu.RLock()
	pool := s.pool
	s.mu.RUnlock()

	if pool == nil {
		return nil, ErrStoreNotConnected
	}

	// 验证 OrderBy 字段（白名单）
	if params.OrderBy != "" {
		if !isValidOrderByField(params.OrderBy) {
			return nil, ErrInvalidOrderBy
		}
	} else {
		params.OrderBy = "triggered_at" // 默认按触发时间排序
	}

	// 构建查询
	query := "SELECT id, trace_id, plugin_id, listener_id, listener_type, " +
		"group_id, user_id, self_id, message_id, message_seq, trigger_data, " +
		"success, duration_ms, error_message, triggered_at, recorded_at " +
		"FROM plugin_trigger_logs WHERE 1=1"

	args := []interface{}{}
	argIndex := 1

	if params.PluginID != nil {
		query += fmt.Sprintf(" AND plugin_id = $%d", argIndex)
		args = append(args, *params.PluginID)
		argIndex++
	}

	if params.ListenerID != nil {
		query += fmt.Sprintf(" AND listener_id = $%d", argIndex)
		args = append(args, *params.ListenerID)
		argIndex++
	}

	if params.ListenerType != nil {
		query += fmt.Sprintf(" AND listener_type = $%d", argIndex)
		args = append(args, *params.ListenerType)
		argIndex++
	}

	if params.GroupID != nil {
		query += fmt.Sprintf(" AND group_id = $%d", argIndex)
		args = append(args, *params.GroupID)
		argIndex++
	}

	if params.UserID != nil {
		query += fmt.Sprintf(" AND user_id = $%d", argIndex)
		args = append(args, *params.UserID)
		argIndex++
	}

	if params.TraceID != nil {
		query += fmt.Sprintf(" AND trace_id = $%d", argIndex)
		args = append(args, *params.TraceID)
		argIndex++
	}

	if params.MessageSeq != nil {
		query += fmt.Sprintf(" AND message_seq = $%d", argIndex)
		args = append(args, *params.MessageSeq)
		argIndex++
	}

	if params.Success != nil {
		query += fmt.Sprintf(" AND success = $%d", argIndex)
		args = append(args, *params.Success)
		argIndex++
	}

	if params.StartTime != nil {
		query += fmt.Sprintf(" AND triggered_at >= $%d", argIndex)
		args = append(args, *params.StartTime)
		argIndex++
	}

	if params.EndTime != nil {
		query += fmt.Sprintf(" AND triggered_at <= $%d", argIndex)
		args = append(args, *params.EndTime)
		argIndex++
	}

	// 排序
	orderDir := "ASC"
	if params.OrderDesc {
		orderDir = "DESC"
	}
	query += fmt.Sprintf(" ORDER BY %s %s", params.OrderBy, orderDir)

	// 分页
	if params.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIndex)
		args = append(args, params.Limit)
		argIndex++
	}

	if params.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argIndex)
		args = append(args, params.Offset)
		argIndex++
	}

	// 执行查询
	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query plugin trigger logs: %w", err)
	}
	defer rows.Close()

	var logs []PluginTriggerLog
	for rows.Next() {
		var log PluginTriggerLog
		err := rows.Scan(
			&log.ID, &log.TraceID, &log.PluginID, &log.ListenerID, &log.ListenerType,
			&log.GroupID, &log.UserID, &log.SelfID, &log.MessageID, &log.MessageSeq,
			&log.TriggerData, &log.Success, &log.DurationMs, &log.ErrorMessage,
			&log.TriggeredAt, &log.RecordedAt,
		)
		if err != nil {
			s.logger.Warn("failed to scan plugin trigger log", "error", err)
			continue
		}
		logs = append(logs, log)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}

	return logs, nil
}

// GetPluginTriggerLogStatistics 获取插件触发日志统计信息
func (s *Store) GetPluginTriggerLogStatistics(ctx context.Context, params PluginTriggerLogQueryParams) (*PluginTriggerLogStatistics, error) {
	s.mu.RLock()
	pool := s.pool
	s.mu.RUnlock()

	if pool == nil {
		return nil, ErrStoreNotConnected
	}

	// 构建 WHERE 子句
	whereClause, args := buildPluginTriggerLogWhereClause(params)

	// 总体统计
	query := "SELECT COUNT(*), " +
		"SUM(CASE WHEN success THEN 1 ELSE 0 END), " +
		"SUM(CASE WHEN NOT success THEN 1 ELSE 0 END), " +
		"AVG(duration_ms) " +
		"FROM plugin_trigger_logs " + whereClause

	// 使用可空类型扫描聚合函数结果
	var totalCount, successCount, failedCount *int64
	var avgDuration *float64

	err := pool.QueryRow(ctx, query, args...).Scan(
		&totalCount,
		&successCount,
		&failedCount,
		&avgDuration,
	)
	if err != nil {
		return nil, fmt.Errorf("query plugin trigger log stats: %w", err)
	}

	// 处理 NULL 值，使用默认值 0
	stats := &PluginTriggerLogStatistics{
		TotalCount:    0,
		SuccessCount:  0,
		FailedCount:   0,
		AvgDurationMs: 0,
	}

	if totalCount != nil {
		stats.TotalCount = *totalCount
	}
	if successCount != nil {
		stats.SuccessCount = *successCount
	}
	if failedCount != nil {
		stats.FailedCount = *failedCount
	}
	if avgDuration != nil {
		stats.AvgDurationMs = *avgDuration
	}

	return stats, nil
}

// buildPluginTriggerLogWhereClause 构建插件触发日志 WHERE 子句
func buildPluginTriggerLogWhereClause(params PluginTriggerLogQueryParams) (string, []interface{}) {
	where := "WHERE 1=1"
	args := []interface{}{}
	argIndex := 1

	if params.PluginID != nil {
		where += fmt.Sprintf(" AND plugin_id = $%d", argIndex)
		args = append(args, *params.PluginID)
		argIndex++
	}

	if params.ListenerID != nil {
		where += fmt.Sprintf(" AND listener_id = $%d", argIndex)
		args = append(args, *params.ListenerID)
		argIndex++
	}

	if params.ListenerType != nil {
		where += fmt.Sprintf(" AND listener_type = $%d", argIndex)
		args = append(args, *params.ListenerType)
		argIndex++
	}

	if params.GroupID != nil {
		where += fmt.Sprintf(" AND group_id = $%d", argIndex)
		args = append(args, *params.GroupID)
		argIndex++
	}

	if params.UserID != nil {
		where += fmt.Sprintf(" AND user_id = $%d", argIndex)
		args = append(args, *params.UserID)
		argIndex++
	}

	if params.TraceID != nil {
		where += fmt.Sprintf(" AND trace_id = $%d", argIndex)
		args = append(args, *params.TraceID)
		argIndex++
	}

	if params.MessageSeq != nil {
		where += fmt.Sprintf(" AND message_seq = $%d", argIndex)
		args = append(args, *params.MessageSeq)
		argIndex++
	}

	if params.Success != nil {
		where += fmt.Sprintf(" AND success = $%d", argIndex)
		args = append(args, *params.Success)
		argIndex++
	}

	if params.StartTime != nil {
		where += fmt.Sprintf(" AND triggered_at >= $%d", argIndex)
		args = append(args, *params.StartTime)
		argIndex++
	}

	if params.EndTime != nil {
		where += fmt.Sprintf(" AND triggered_at <= $%d", argIndex)
		args = append(args, *params.EndTime)
		argIndex++
	}

	return where, args
}

// maskURI 隐藏 URI 中的密码
func maskURI(uri string) string {
	parsed, err := url.Parse(uri)
	if err != nil {
		return "[invalid-uri]"
	}

	if parsed.User != nil {
		parsed.User = url.UserPassword(parsed.User.Username(), "***")
	}

	// 隐藏主机名的一部分
	if parsed.Host != "" {
		parts := strings.Split(parsed.Host, ":")
		if len(parts) > 0 {
			host := parts[0]
			if len(host) > 4 {
				host = host[:2] + "***" + host[len(host)-2:]
			}
			if len(parts) > 1 {
				parsed.Host = host + ":" + parts[1]
			} else {
				parsed.Host = host
			}
		}
	}

	return parsed.String()
}
