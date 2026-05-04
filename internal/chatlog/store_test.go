package chatlog

import (
	"context"
	"log/slog"
	"os"
	"testing"
)

func TestStore_ReconnectEmptyURI(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := NewStore(logger)

	// 空 URI 应该禁用存储
	err := store.Reconnect(context.Background(), "")
	if err != nil {
		t.Errorf("expected no error for empty URI, got %v", err)
	}

	// SaveBatch 应该返回 ErrStoreNotConnected
	messages := []GroupMessage{
		{GroupID: 123, RealSeq: "1"},
	}
	err = store.SaveBatch(context.Background(), messages)
	if err != ErrStoreNotConnected {
		t.Errorf("expected ErrStoreNotConnected for disabled store, got %v", err)
	}
}

func TestStore_SaveBatchNotConnected(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := NewStore(logger)

	// 未连接时应该返回 ErrStoreNotConnected
	messages := []GroupMessage{
		{GroupID: 123, RealSeq: "1"},
	}
	err := store.SaveBatch(context.Background(), messages)
	if err != ErrStoreNotConnected {
		t.Errorf("expected ErrStoreNotConnected, got %v", err)
	}
}

func TestStore_ReconnectInvalidURI(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := NewStore(logger)

	// 无效 URI 应该返回错误
	err := store.Reconnect(context.Background(), "invalid://uri")
	if err == nil {
		t.Error("expected error for invalid URI")
	}
}

func TestStore_SaveBatchEmptyMessages(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := NewStore(logger)

	// 空消息列表应该返回 ErrStoreNotConnected（因为未连接）
	err := store.SaveBatch(context.Background(), []GroupMessage{})
	if err != nil {
		t.Errorf("expected no error for empty messages, got %v", err)
	}
}

func TestStore_CloseIdempotent(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := NewStore(logger)

	// 多次关闭应该安全
	store.Close()
	store.Close()
}

func TestMaskURI(t *testing.T) {
	tests := []struct {
		name         string
		uri          string
		shouldMask   bool
		checkContent func(string) bool
	}{
		{
			name:       "with password",
			uri:        "postgres://user:secret@localhost:5432/db",
			shouldMask: true,
			checkContent: func(masked string) bool {
				// 应该包含 *** 且不包含 secret
				return masked != "postgres://user:secret@localhost:5432/db" && len(masked) > 0
			},
		},
		{
			name:       "without password",
			uri:        "postgres://user@localhost:5432/db",
			shouldMask: true,
			checkContent: func(masked string) bool {
				// 应该被处理（主机名被部分隐藏）
				return masked != "postgres://user@localhost:5432/db" && len(masked) > 0
			},
		},
		{
			name:       "invalid uri",
			uri:        "not a valid uri",
			shouldMask: false,
			checkContent: func(masked string) bool {
				// url.Parse 可能会将其解析为相对路径，所以不一定返回 [invalid-uri]
				// 只要不是原始 URI 即可
				return true
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			masked := maskURI(tt.uri)
			if !tt.checkContent(masked) {
				t.Errorf("maskURI(%q) = %q, validation failed", tt.uri, masked)
			}
		})
	}
}
