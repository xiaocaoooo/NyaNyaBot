package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/xiaocaoooo/nyanyabot/internal/config"
	"github.com/xiaocaoooo/nyanyabot/internal/onebot/reversews"
	"github.com/xiaocaoooo/nyanyabot/internal/plugin"
	"github.com/xiaocaoooo/nyanyabot/internal/stats"
)

func TestBotsAPI(t *testing.T) {
	// 创建临时配置存储
	tmpDir, err := os.MkdirTemp("", "nyanyabot-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	store, err := config.NewStore(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	// 创建组件
	pm := plugin.NewManager()
	st := stats.New()
	rws := reversews.New(":0", nil)

	// 创建 web server
	srv := New(store, pm)
	srv.SetStatsProvider(st)
	srv.SetReverseWSServer(rws)

	// 创建有效会话
	sessionID, _, err := srv.sessions.create(time.Now())
	if err != nil {
		t.Fatal(err)
	}

	// 测试 /api/bots 端点
	req := httptest.NewRequest(http.MethodGet, "/api/bots", nil)
	req.AddCookie(&http.Cookie{
		Name:  "nyanyabot_session",
		Value: sessionID,
	})
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var resp BotsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// 验证响应结构
	if resp.Bots == nil {
		t.Error("Bots field is nil")
	}

	if !resp.GroupChatOnly {
		t.Error("Expected group_chat_only=true")
	}

	if resp.DedupeKey != "group_id+real_seq" {
		t.Errorf("Expected dedupe_key='group_id+real_seq', got %s", resp.DedupeKey)
	}

	if resp.TotalBots != 0 {
		t.Errorf("Expected 0 bots (no connections), got %d", resp.TotalBots)
	}

	if resp.OnlineBots != 0 {
		t.Errorf("Expected 0 online bots, got %d", resp.OnlineBots)
	}

	// 验证全局统计字段存在（平铺字段，兼容性）
	if resp.GlobalRecvCount < 0 {
		t.Error("GlobalRecvCount should be >= 0")
	}

	if resp.GlobalSentCount < 0 {
		t.Error("GlobalSentCount should be >= 0")
	}

	if resp.GlobalStartTime == "" {
		t.Error("GlobalStartTime should not be empty")
	}

	if resp.GlobalUptime == "" {
		t.Error("GlobalUptime should not be empty")
	}

	// 验证嵌套的 stats 对象
	if resp.Stats == nil {
		t.Error("Stats object should not be nil")
	} else {
		if resp.Stats.RecvCount < 0 {
			t.Error("Stats.RecvCount should be >= 0")
		}
		if resp.Stats.SentCount < 0 {
			t.Error("Stats.SentCount should be >= 0")
		}
		if resp.Stats.FilteredSelfCount < 0 {
			t.Error("Stats.FilteredSelfCount should be >= 0")
		}
		if resp.Stats.FilteredNonGroupCount < 0 {
			t.Error("Stats.FilteredNonGroupCount should be >= 0")
		}
		if resp.Stats.DedupCount < 0 {
			t.Error("Stats.DedupCount should be >= 0")
		}
		if resp.Stats.StartTime == "" {
			t.Error("Stats.StartTime should not be empty")
		}
		if resp.Stats.Uptime == "" {
			t.Error("Stats.Uptime should not be empty")
		}
	}
}
