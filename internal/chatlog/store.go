package chatlog

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 管理 PostgreSQL 连接和消息存储
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
		s.logger.Info("chatlog store disabled")
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
	s.logger.Info("chatlog store connected", "uri", maskedURI)

	return nil
}

// createTable 创建表和索引
func (s *Store) createTable(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS group_message_logs (
			group_id BIGINT NOT NULL,
			real_seq TEXT NOT NULL,
			group_name TEXT NOT NULL DEFAULT '',
			user_id BIGINT NOT NULL,
			user_display_name TEXT NOT NULL DEFAULT '',
			raw_message TEXT NOT NULL DEFAULT '',
			message_segments JSONB NOT NULL DEFAULT '[]'::jsonb,
			recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT group_message_logs_group_real_seq_key UNIQUE (group_id, real_seq)
		);

		CREATE INDEX IF NOT EXISTS idx_group_message_logs_group_id 
			ON group_message_logs (group_id);
		
		CREATE INDEX IF NOT EXISTS idx_group_message_logs_recorded_at 
			ON group_message_logs (recorded_at);
		
		CREATE INDEX IF NOT EXISTS idx_group_message_logs_user_id 
			ON group_message_logs (user_id);
	`)
	return err
}

// SaveBatch 批量保存消息
// 使用 ON CONFLICT ON CONSTRAINT group_message_logs_group_real_seq_key DO NOTHING
func (s *Store) SaveBatch(ctx context.Context, messages []GroupMessage) error {
	if len(messages) == 0 {
		return nil
	}

	s.mu.RLock()
	pool := s.pool
	s.mu.RUnlock()

	if pool == nil {
		return ErrStoreNotConnected
	}

	batch := &pgx.Batch{}
	for _, msg := range messages {
		segmentsJSON, err := json.Marshal(msg.MessageSegments)
		if err != nil {
			s.logger.Warn("failed to marshal message_segments", "error", err)
			continue
		}

		batch.Queue(`
			INSERT INTO group_message_logs 
				(group_id, real_seq, group_name, user_id, user_display_name, raw_message, message_segments, recorded_at)
			VALUES 
				($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT ON CONSTRAINT group_message_logs_group_real_seq_key DO NOTHING
		`, msg.GroupID, msg.RealSeq, msg.GroupName, msg.UserID, msg.UserDisplayName, msg.RawMessage, segmentsJSON, msg.RecordedAt)
	}

	results := pool.SendBatch(ctx, batch)
	defer results.Close()

	// 消费所有结果
	for i := 0; i < len(messages); i++ {
		_, err := results.Exec()
		if err != nil {
			s.logger.Warn("failed to save message", "error", err, "index", i)
		}
	}

	return nil
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
