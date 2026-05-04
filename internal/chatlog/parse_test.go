package chatlog

import (
	"encoding/json"
	"testing"
)

func TestParseGroupMessage_Valid(t *testing.T) {
	event := json.RawMessage(`{
		"post_type": "message",
		"message_type": "group",
		"group_id": 123456,
		"user_id": 789012,
		"self_id": 111222,
		"real_seq": 100,
		"raw_message": "hello world",
		"sender": {
			"card": "Alice",
			"nickname": "alice123"
		},
		"message": [
			{"type": "text", "data": {"text": "hello"}},
			{"type": "text", "data": {"text": " world"}}
		]
	}`)

	msg, err := ParseGroupMessage(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if msg == nil {
		t.Fatal("expected message, got nil")
	}

	if msg.GroupID != 123456 {
		t.Errorf("expected group_id 123456, got %d", msg.GroupID)
	}

	if msg.UserID != 789012 {
		t.Errorf("expected user_id 789012, got %d", msg.UserID)
	}

	if msg.SelfID != 111222 {
		t.Errorf("expected self_id 111222, got %d", msg.SelfID)
	}

	if msg.RealSeq != "100" {
		t.Errorf("expected real_seq '100', got '%s'", msg.RealSeq)
	}

	if msg.RawMessage != "hello world" {
		t.Errorf("expected raw_message 'hello world', got '%s'", msg.RawMessage)
	}

	if msg.UserDisplayName != "Alice" {
		t.Errorf("expected user_display_name 'Alice', got '%s'", msg.UserDisplayName)
	}

	if len(msg.MessageSegments) != 2 {
		t.Errorf("expected 2 message segments, got %d", len(msg.MessageSegments))
	}
}

func TestParseGroupMessage_CardOverNickname(t *testing.T) {
	event := json.RawMessage(`{
		"post_type": "message",
		"message_type": "group",
		"group_id": 123456,
		"user_id": 789012,
		"real_seq": 100,
		"sender": {
			"card": "Alice",
			"nickname": "alice123"
		}
	}`)

	msg, err := ParseGroupMessage(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if msg.UserDisplayName != "Alice" {
		t.Errorf("expected card 'Alice', got '%s'", msg.UserDisplayName)
	}
}

func TestParseGroupMessage_NicknameWhenNoCard(t *testing.T) {
	event := json.RawMessage(`{
		"post_type": "message",
		"message_type": "group",
		"group_id": 123456,
		"user_id": 789012,
		"real_seq": 100,
		"sender": {
			"nickname": "alice123"
		}
	}`)

	msg, err := ParseGroupMessage(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if msg.UserDisplayName != "alice123" {
		t.Errorf("expected nickname 'alice123', got '%s'", msg.UserDisplayName)
	}
}

func TestParseGroupMessage_EmptyCardFallbackToNickname(t *testing.T) {
	event := json.RawMessage(`{
		"post_type": "message",
		"message_type": "group",
		"group_id": 123456,
		"user_id": 789012,
		"real_seq": 100,
		"sender": {
			"card": "",
			"nickname": "alice123"
		}
	}`)

	msg, err := ParseGroupMessage(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if msg.UserDisplayName != "alice123" {
		t.Errorf("expected nickname 'alice123', got '%s'", msg.UserDisplayName)
	}
}

func TestParseGroupMessage_MissingRealSeq(t *testing.T) {
	event := json.RawMessage(`{
		"post_type": "message",
		"message_type": "group",
		"group_id": 123456,
		"user_id": 789012
	}`)

	msg, err := ParseGroupMessage(event)
	if err != ErrInvalidSeq {
		t.Errorf("expected ErrInvalidSeq, got %v", err)
	}

	if msg != nil {
		t.Error("expected nil message")
	}
}

func TestParseGroupMessage_ZeroRealSeq(t *testing.T) {
	event := json.RawMessage(`{
		"post_type": "message",
		"message_type": "group",
		"group_id": 123456,
		"user_id": 789012,
		"real_seq": 0
	}`)

	msg, err := ParseGroupMessage(event)
	if err != ErrInvalidSeq {
		t.Errorf("expected ErrInvalidSeq, got %v", err)
	}

	if msg != nil {
		t.Error("expected nil message")
	}
}

func TestParseGroupMessage_NotGroupMessage(t *testing.T) {
	event := json.RawMessage(`{
		"post_type": "message",
		"message_type": "private",
		"user_id": 789012,
		"real_seq": 100
	}`)

	msg, err := ParseGroupMessage(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if msg != nil {
		t.Error("expected nil for non-group message")
	}
}

func TestParseGroupMessage_EmptyMessageSegments(t *testing.T) {
	event := json.RawMessage(`{
		"post_type": "message",
		"message_type": "group",
		"group_id": 123456,
		"user_id": 789012,
		"real_seq": 100
	}`)

	msg, err := ParseGroupMessage(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if msg == nil {
		t.Fatal("expected message, got nil")
	}

	if msg.MessageSegments == nil {
		t.Error("expected empty slice, got nil")
	}

	if len(msg.MessageSegments) != 0 {
		t.Errorf("expected 0 segments, got %d", len(msg.MessageSegments))
	}
}

func TestParseGroupMessage_NoMessageSeqFallback(t *testing.T) {
	// 确保不会回退到 message_seq
	event := json.RawMessage(`{
		"post_type": "message",
		"message_type": "group",
		"group_id": 123456,
		"user_id": 789012,
		"message_seq": 999
	}`)

	msg, err := ParseGroupMessage(event)
	if err != ErrInvalidSeq {
		t.Errorf("expected ErrInvalidSeq when real_seq missing, got %v", err)
	}

	if msg != nil {
		t.Error("expected nil message when real_seq missing")
	}
}

func TestParseGroupMessage_RealSeqString(t *testing.T) {
	event := json.RawMessage(`{
		"post_type": "message",
		"message_type": "group",
		"group_id": 123456,
		"user_id": 789012,
		"real_seq": "abc123"
	}`)

	msg, err := ParseGroupMessage(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if msg == nil {
		t.Fatal("expected message, got nil")
	}

	if msg.RealSeq != "abc123" {
		t.Errorf("expected real_seq 'abc123', got '%s'", msg.RealSeq)
	}
}

func TestParseGroupMessage_RealSeqNumber(t *testing.T) {
	event := json.RawMessage(`{
		"post_type": "message",
		"message_type": "group",
		"group_id": 123456,
		"user_id": 789012,
		"real_seq": 12345.0
	}`)

	msg, err := ParseGroupMessage(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if msg == nil {
		t.Fatal("expected message, got nil")
	}

	if msg.RealSeq != "12345" {
		t.Errorf("expected real_seq '12345', got '%s'", msg.RealSeq)
	}
}

func TestParseGroupMessage_RealSeqEmptyString(t *testing.T) {
	event := json.RawMessage(`{
		"post_type": "message",
		"message_type": "group",
		"group_id": 123456,
		"user_id": 789012,
		"real_seq": ""
	}`)

	msg, err := ParseGroupMessage(event)
	if err != ErrInvalidSeq {
		t.Errorf("expected ErrInvalidSeq for empty string, got %v", err)
	}

	if msg != nil {
		t.Error("expected nil message for empty real_seq")
	}
}

func TestParseGroupMessage_RealSeqStringZero(t *testing.T) {
	event := json.RawMessage(`{
		"post_type": "message",
		"message_type": "group",
		"group_id": 123456,
		"user_id": 789012,
		"real_seq": "0"
	}`)

	msg, err := ParseGroupMessage(event)
	if err != ErrInvalidSeq {
		t.Errorf("expected ErrInvalidSeq for string '0', got %v", err)
	}

	if msg != nil {
		t.Error("expected nil message for real_seq '0'")
	}
}

func TestParseGroupMessage_MissingSelfID(t *testing.T) {
	event := json.RawMessage(`{
		"post_type": "message",
		"message_type": "group",
		"group_id": 123456,
		"user_id": 789012,
		"real_seq": 100
	}`)

	msg, err := ParseGroupMessage(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if msg == nil {
		t.Fatal("expected message, got nil")
	}

	if msg.SelfID != 0 {
		t.Errorf("expected self_id 0 when missing, got %d", msg.SelfID)
	}
}

func TestParseGroupMessage_SelfIDEqualUserID(t *testing.T) {
	// 即使 self_id == user_id，也应该正常解析（过滤在 dispatcher 层）
	event := json.RawMessage(`{
		"post_type": "message",
		"message_type": "group",
		"group_id": 123456,
		"user_id": 789012,
		"self_id": 789012,
		"real_seq": 100
	}`)

	msg, err := ParseGroupMessage(event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if msg == nil {
		t.Fatal("expected message, got nil")
	}

	if msg.SelfID != 789012 {
		t.Errorf("expected self_id 789012, got %d", msg.SelfID)
	}

	if msg.UserID != 789012 {
		t.Errorf("expected user_id 789012, got %d", msg.UserID)
	}
}
