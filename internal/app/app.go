package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/xiaocaoooo/nyanyabot/internal/chatlog"
	"github.com/xiaocaoooo/nyanyabot/internal/config"
	"github.com/xiaocaoooo/nyanyabot/internal/cron"
	"github.com/xiaocaoooo/nyanyabot/internal/dispatch"
	"github.com/xiaocaoooo/nyanyabot/internal/onebot/ob11"
	"github.com/xiaocaoooo/nyanyabot/internal/onebot/reversews"
	"github.com/xiaocaoooo/nyanyabot/internal/plugin"
	"github.com/xiaocaoooo/nyanyabot/internal/plugin/transport"
	"github.com/xiaocaoooo/nyanyabot/internal/pluginhost"
	"github.com/xiaocaoooo/nyanyabot/internal/stats"
	"github.com/xiaocaoooo/nyanyabot/internal/util"
	"github.com/xiaocaoooo/nyanyabot/internal/web"
)

// oneBotCallerAdapter adapts reversews.Server.Call to chatlog.OneBotCaller interface
type oneBotCallerAdapter struct {
	ob *reversews.Server
}

func (a *oneBotCallerAdapter) CallAPI(ctx context.Context, action string, params interface{}) (json.RawMessage, error) {
	resp, err := a.ob.Call(ctx, action, params)
	if err != nil {
		return nil, err
	}
	return resp.Data, nil
}

type App struct {
	Logger  *slog.Logger
	Store   *config.Store
	PM      *plugin.Manager
	PH      *pluginhost.Host
	Disp    *dispatch.Dispatcher
	Cron    *cron.Scheduler
	OB      *reversews.Server
	Web     *http.Server
	Stats   *stats.Stats
	ChatLog *chatlog.Recorder
}

func New(ctx context.Context, logger *slog.Logger) (*App, error) {
	if logger == nil {
		logger = slog.Default()
	}

	dataDir := util.WorkspaceDataDir()
	store, err := config.NewStore(dataDir)
	if err != nil {
		return nil, err
	}
	cfg, err := store.LoadOrCreateDefault()
	if err != nil {
		return nil, err
	}

	st := stats.New()
	pm := plugin.NewManager()
	pm.SetPluginEnabledChecker(func(pluginID string) bool {
		return store.Get().IsPluginEnabled(pluginID)
	})
	disp := dispatch.NewWithLoggerAndStats(pm, logger, st)
	disp.SetConfigProvider(store.Get)

	// Create cron scheduler
	cronScheduler := cron.NewScheduler(pm, logger)
	cronScheduler.SetConfigProvider(store.Get)

	ob := reversews.New(cfg.OneBot.ReverseWS.ListenAddr, logger)
	ob.SetStats(st)

	// Create chatlog recorder with adapter for ob.Call
	chatRecorder := chatlog.NewRecorder(logger, &oneBotCallerAdapter{ob: ob})

	// Connect to database if configured
	if cfg.ChatLog.DatabaseURI != "" {
		if err := chatRecorder.Reconnect(ctx, cfg.ChatLog.DatabaseURI); err != nil {
			logger.Warn("chatlog initial connect failed", "err", err)
		}
	}

	ob.SetEventHandler(func(evCtx context.Context, raw ob11.Event) {
		_ = evCtx // per connection ctx
		// Record event first (non-blocking)
		chatRecorder.HandleEvent(ctx, raw)
		// Dispatch using app ctx so plugins can continue briefly even if connection ctx is canceled.
		disp.Dispatch(ctx, raw)
	})

	ph := pluginhost.New(pm, func() map[string]json.RawMessage {
		// Return a snapshot; callers must not mutate.
		cfg := store.Get()
		out := make(map[string]json.RawMessage, len(cfg.Plugins))
		for k, v := range cfg.Plugins {
			out[k] = v
		}
		return out
	}, func() map[string]string {
		cfg := store.Get()
		out := make(map[string]string, len(cfg.Globals))
		for k, v := range cfg.Globals {
			out[k] = v
		}
		return out
	}, func(c context.Context, action string, params any, traceID string) (ob11.APIResponse, error) {
		// 如果有 TraceID，记录追踪信息
		if traceID != "" {
			logger.Debug("plugin sending OneBot request",
				"trace_id", traceID,
				"action", action,
			)
		}
		resp, err := ob.Call(c, action, params)
		if traceID != "" {
			if err != nil {
				logger.Debug("plugin OneBot request failed",
					"trace_id", traceID,
					"action", action,
					"error", err,
				)
			} else {
				logger.Debug("plugin OneBot request succeeded",
					"trace_id", traceID,
					"action", action,
					"status", resp.Status,
				)
			}
		}
		return resp, err
	}, func(ctx context.Context) (transport.GetStatsReply, error) {
		snap := st.Snapshot()
		return transport.GetStatsReply{
			RecvCount: snap.RecvCount,
			SentCount: snap.SentCount,
			StartTime: snap.StartTime,
			Uptime:    snap.Uptime,
		}, nil
	})

	// 连接追踪系统
	disp.SetTraceProvider(ph)
	cronScheduler.SetTraceProvider(ph)

	webSrv := web.New(store, pm)
	webSrv.SetStatsProvider(st)
	webSrv.SetChatLogConfigChangeHandler(func(ctx context.Context, chatLogCfg config.ChatLogConfig) {
		if err := chatRecorder.Reconnect(ctx, chatLogCfg.DatabaseURI); err != nil {
			logger.Error("chatlog runtime reconnect failed", "err", err)
		}
	})
	httpSrv := &http.Server{
		Addr:              cfg.WebUI.ListenAddr,
		Handler:           webSrv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	return &App{
		Logger:  logger,
		Store:   store,
		PM:      pm,
		PH:      ph,
		Disp:    disp,
		Cron:    cronScheduler,
		OB:      ob,
		Web:     httpSrv,
		Stats:   st,
		ChatLog: chatRecorder,
	}, nil
}
