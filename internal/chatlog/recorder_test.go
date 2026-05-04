package chatlog

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/xiaocaoooo/nyanyabot/internal/onebot/ob11"
)

// mockOneBotCaller 模拟 OneBot API 调用
type mockOneBotCaller struct {
	groupInfo map[int64]string
	callCount int
}

func (m *mockOneBotCaller) CallAPI(ctx context.Context, action string, params interface{}) (json.RawMessage, error) {
	m.callCount++
	if action == "get_group_info" {
		if p, ok := params.(map[string]interface{}); ok {
			if groupID, ok := p["group_id"].(int64); ok {
				if name, ok := m.groupInfo[groupID]; ok {
					return json.RawMessage(`{"group_name":"` + name + `"}`), nil
				}
			}
		}
	}
	return nil, nil
}

func TestRecorder_StartStop(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	caller := &mockOneBotCaller{groupInfo: make(map[int64]string)}
	recorder := NewRecorder(logger, caller)

	ctx := context.Background()

	// 启动
	recorder.Start(ctx)

	stats := recorder.GetStats()
	if !stats["started"].(bool) {
		t.Error("expected recorder to be started")
	}

	// 停止
	err := recorder.Stop(ctx)
	if err != nil {
		t.Errorf("failed to stop recorder: %v", err)
	}

	stats = recorder.GetStats()
	if stats["started"].(bool) {
		t.Error("expected recorder to be stopped")
	}
}

func TestRecorder_HandleEventNotStarted(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	caller := &mockOneBotCaller{groupInfo: make(map[int64]string)}
	recorder := NewRecorder(logger, caller)

	ctx := context.Background()

	// 未启动时处理事件应该是 no-op
	event := ob11.Event(`{
		"post_type": "message",
		"message_type": "group",
		"group_id": 123,
		"user_id": 456,
		"real_seq": 1,
		"raw_message": "test"
	}`)

	recorder.HandleEvent(ctx, event)

	// 不应该崩溃
}

func TestRecorder_HandleEventNonGroupMessage(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	caller := &mockOneBotCaller{groupInfo: make(map[int64]string)}
	recorder := NewRecorder(logger, caller)

	ctx := context.Background()
	recorder.Start(ctx)
	defer recorder.Stop(ctx)

	// 私聊消息应该被忽略
	event := ob11.Event(`{
		"post_type": "message",
		"message_type": "private",
		"user_id": 456,
		"real_seq": 1,
		"raw_message": "test"
	}`)

	recorder.HandleEvent(ctx, event)

	// 不应该崩溃
}

func TestRecorder_HandleEventMissingRealSeq(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	caller := &mockOneBotCaller{groupInfo: make(map[int64]string)}
	recorder := NewRecorder(logger, caller)

	ctx := context.Background()
	recorder.Start(ctx)
	defer recorder.Stop(ctx)

	// 缺少 real_seq 应该被忽略
	event := ob11.Event(`{
		"post_type": "message",
		"message_type": "group",
		"group_id": 123,
		"user_id": 456,
		"raw_message": "test"
	}`)

	recorder.HandleEvent(ctx, event)

	// 不应该崩溃
}

func TestRecorder_WorkerProcessesBatch(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	caller := &mockOneBotCaller{
		groupInfo: map[int64]string{
			123: "Test Group",
		},
	}
	recorder := NewRecorder(logger, caller)

	ctx := context.Background()
	recorder.Start(ctx)
	defer recorder.Stop(ctx)

	// 发送一条消息
	event := ob11.Event(`{
		"post_type": "message",
		"message_type": "group",
		"group_id": 123,
		"user_id": 456,
		"real_seq": 1,
		"raw_message": "test message",
		"sender": {
			"nickname": "TestUser"
		}
	}`)

	recorder.HandleEvent(ctx, event)

	// 等待 worker 处理
	time.Sleep(1500 * time.Millisecond)

	// 验证 API 被调用（补全 group_name）
	if caller.callCount == 0 {
		t.Error("expected get_group_info to be called")
	}
}

func TestRecorder_ReconnectDatabase(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	caller := &mockOneBotCaller{groupInfo: make(map[int64]string)}
	recorder := NewRecorder(logger, caller)

	ctx := context.Background()

	// 空 URI 应该禁用存储
	err := recorder.Reconnect(ctx, "")
	if err != nil {
		t.Errorf("expected no error for empty URI, got %v", err)
	}

	// 无效 URI 应该返回错误
	err = recorder.Reconnect(ctx, "invalid://uri")
	if err == nil {
		t.Error("expected error for invalid URI")
	}
}

func TestRecorder_DoubleStart(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	caller := &mockOneBotCaller{groupInfo: make(map[int64]string)}
	recorder := NewRecorder(logger, caller)

	ctx := context.Background()

	// 第一次启动
	recorder.Start(ctx)

	// 第二次启动应该是 no-op
	recorder.Start(ctx)

	// 停止
	err := recorder.Stop(ctx)
	if err != nil {
		t.Errorf("failed to stop recorder: %v", err)
	}
}

func TestRecorder_StopNotStarted(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	caller := &mockOneBotCaller{groupInfo: make(map[int64]string)}
	recorder := NewRecorder(logger, caller)

	ctx := context.Background()

	// 未启动时停止应该是 no-op
	err := recorder.Stop(ctx)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}
