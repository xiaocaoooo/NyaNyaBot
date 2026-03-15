package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/xiaocaoooo/nyanyabot/internal/config"
	"github.com/xiaocaoooo/nyanyabot/internal/dispatch"
	"github.com/xiaocaoooo/nyanyabot/internal/onebot/ob11"
	"github.com/xiaocaoooo/nyanyabot/internal/onebot/reversews"
	"github.com/xiaocaoooo/nyanyabot/internal/plugin"
	"github.com/xiaocaoooo/nyanyabot/internal/pluginhost"
	"github.com/xiaocaoooo/nyanyabot/internal/util"
	"github.com/xiaocaoooo/nyanyabot/internal/web"
)

type App struct {
	Logger *slog.Logger
	Store  *config.Store
	PM     *plugin.Manager
	PH     *pluginhost.Host
	Disp   *dispatch.Dispatcher
	OB     *reversews.Server
	Web    *http.Server
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

	pm := plugin.NewManager()
	disp := dispatch.New(pm)

	ob := reversews.New(cfg.OneBot.ReverseWS.ListenAddr, logger)
	ob.SetEventHandler(func(evCtx context.Context, raw ob11.Event) {
		_ = evCtx // per connection ctx
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
	}, func(c context.Context, action string, params any) (ob11.APIResponse, error) {
		return ob.Call(c, action, params)
	})

	webSrv := web.New(store, pm)
	httpSrv := &http.Server{
		Addr:              cfg.WebUI.ListenAddr,
		Handler:           webSrv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	return &App{
		Logger: logger,
		Store:  store,
		PM:     pm,
		PH:     ph,
		Disp:   disp,
		OB:     ob,
		Web:    httpSrv,
	}, nil
}
