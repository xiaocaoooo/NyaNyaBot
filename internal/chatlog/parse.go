package chatlog

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/xiaocaoooo/nyanyabot/internal/onebot/ob11"
)

// ParseGroupMessage 从 ob11.Event 解析群消息
// 只记录 post_type == "message" && message_type == "group"
// RealSeq 只取事件字段 real_seq，不允许回退 message_seq；缺失/空则返回 ErrInvalidSeq
// user_display_name 逻辑为 sender.card > sender.nickname
// message_segments 来自事件 message，缺失则 []
func ParseGroupMessage(raw ob11.Event) (*GroupMessage, error) {
	var event map[string]interface{}
	if err := json.Unmarshal(raw, &event); err != nil {
		return nil, err
	}

	// 只处理群消息
	postType, _ := event["post_type"].(string)
	messageType, _ := event["message_type"].(string)
	if postType != "message" || messageType != "group" {
		return nil, nil // 不是群消息，跳过
	}

	// 提取 real_seq（必须存在且非零/非空）
	// 支持 string、float64、json.Number、整数类型
	realSeqRaw, ok := event["real_seq"]
	if !ok {
		return nil, ErrInvalidSeq
	}

	realSeq := parseRealSeq(realSeqRaw)
	if realSeq == "" {
		return nil, ErrInvalidSeq
	}

	// 提取 group_id
	groupID, ok := event["group_id"].(float64)
	if !ok {
		return nil, nil // 缺少 group_id，跳过
	}

	// 提取 user_id
	userID, ok := event["user_id"].(float64)
	if !ok {
		return nil, nil // 缺少 user_id，跳过
	}

	// 提取 self_id（Bot 自身 QQ 号），缺失时默认 0
	selfID := int64(0)
	if selfIDRaw, ok := event["self_id"].(float64); ok {
		selfID = int64(selfIDRaw)
	}

	// 提取 raw_message
	rawMessage, _ := event["raw_message"].(string)

	// 提取 user_display_name: sender.card > sender.nickname
	var userDisplayName string
	if sender, ok := event["sender"].(map[string]interface{}); ok {
		if card, ok := sender["card"].(string); ok && card != "" {
			userDisplayName = card
		} else if nickname, ok := sender["nickname"].(string); ok {
			userDisplayName = nickname
		}
	}

	// 提取 message_segments
	var messageSegments []json.RawMessage
	if message, ok := event["message"].([]interface{}); ok {
		for _, seg := range message {
			segBytes, err := json.Marshal(seg)
			if err == nil {
				messageSegments = append(messageSegments, segBytes)
			}
		}
	}
	if messageSegments == nil {
		messageSegments = []json.RawMessage{}
	}

	return &GroupMessage{
		GroupID:         int64(groupID),
		RealSeq:         realSeq,
		GroupName:       "", // 稍后由 recorder 补全
		UserID:          int64(userID),
		UserDisplayName: userDisplayName,
		RawMessage:      rawMessage,
		MessageSegments: messageSegments,
		RecordedAt:      time.Now(),
		SelfID:          selfID,
	}, nil
}

// parseRealSeq 将 real_seq 转换为字符串
// 支持 string、float64、json.Number、int/int64/uint/uint64
// 返回空字符串表示无效（0 或空字符串）
func parseRealSeq(v interface{}) string {
	switch val := v.(type) {
	case string:
		if val == "" || val == "0" {
			return ""
		}
		return val
	case float64:
		if val == 0 {
			return ""
		}
		return fmt.Sprintf("%.0f", val)
	case json.Number:
		s := val.String()
		if s == "" || s == "0" {
			return ""
		}
		return s
	case int:
		if val == 0 {
			return ""
		}
		return fmt.Sprintf("%d", val)
	case int64:
		if val == 0 {
			return ""
		}
		return fmt.Sprintf("%d", val)
	case uint:
		if val == 0 {
			return ""
		}
		return fmt.Sprintf("%d", val)
	case uint64:
		if val == 0 {
			return ""
		}
		return fmt.Sprintf("%d", val)
	default:
		return ""
	}
}
