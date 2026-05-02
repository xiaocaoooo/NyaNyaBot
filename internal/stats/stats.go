package stats

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Stats 记录运行时统计信息
type Stats struct {
	recvCount       atomic.Int64 // 收到的消息数
	sentCount       atomic.Int64 // 发送的消息数
	startTime       time.Time    // 启动时间
	pluginSentStats sync.Map     // pluginID → *atomic.Int64
}

// Snapshot 是统计信息的快照
type Snapshot struct {
	RecvCount       int64            `json:"recv_count"`
	SentCount       int64            `json:"sent_count"`
	PluginSentStats map[string]int64 `json:"plugin_sent_stats,omitempty"`
	StartTime       time.Time        `json:"start_time"`
	Uptime          string           `json:"uptime"`
}

// New 创建一个新的 Stats 实例
func New() *Stats {
	return &Stats{
		startTime: time.Now(),
	}
}

// IncRecv 增加接收消息计数
func (s *Stats) IncRecv() {
	s.recvCount.Add(1)
}

// IncSent 增加发送消息计数
func (s *Stats) IncSent() {
	s.sentCount.Add(1)
}

// IncSentByPlugin 增加指定插件的发送消息计数
func (s *Stats) IncSentByPlugin(pluginID string) {
	if s == nil || pluginID == "" {
		return
	}
	val, _ := s.pluginSentStats.LoadOrStore(pluginID, &atomic.Int64{})
	if counter, ok := val.(*atomic.Int64); ok {
		counter.Add(1)
	}
}

// GetPluginSentStats 获取各插件的发送统计
func (s *Stats) GetPluginSentStats() map[string]int64 {
	result := make(map[string]int64)
	s.pluginSentStats.Range(func(key, value interface{}) bool {
		if pluginID, ok := key.(string); ok {
			if counter, ok := value.(*atomic.Int64); ok {
				result[pluginID] = counter.Load()
			}
		}
		return true
	})
	return result
}

// Snapshot 返回当前统计信息的快照
func (s *Stats) Snapshot() Snapshot {
	return Snapshot{
		RecvCount:       s.recvCount.Load(),
		SentCount:       s.sentCount.Load(),
		PluginSentStats: s.GetPluginSentStats(),
		StartTime:       s.startTime,
		Uptime:          s.FormatUptime(),
	}
}

// FormatUptime 格式化运行时间
// 例如: "12秒", "12分34秒", "12小时45分23秒", "23天23小时23分23秒"
func (s *Stats) FormatUptime() string {
	return FormatDuration(time.Since(s.startTime))
}

// FormatDuration 将 time.Duration 格式化为易读的中文格式
func FormatDuration(d time.Duration) string {
	d = d.Round(time.Second)

	days := int64(d / (24 * time.Hour))
	d -= time.Duration(days) * 24 * time.Hour

	hours := int64(d / time.Hour)
	d -= time.Duration(hours) * time.Hour

	minutes := int64(d / time.Minute)
	d -= time.Duration(minutes) * time.Minute

	seconds := int64(d / time.Second)

	var parts []string

	if days > 0 {
		parts = append(parts, fmt.Sprintf("%d天", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%d小时", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%d分", minutes))
	}
	if seconds > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%d秒", seconds))
	}

	result := ""
	for _, p := range parts {
		result += p
	}
	return result
}
