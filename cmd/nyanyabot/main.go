package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/xiaocaoooo/nyanyabot/internal/app"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	a, err := app.New(ctx, logger)
	if err != nil {
		logger.Error("app init failed", "err", err)
		os.Exit(1)
	}

	// Load external plugins from ./plugins (if any).
	if err := a.PH.LoadDir(ctx, "plugins"); err != nil {
		logger.Error("load plugins dir failed", "err", err)
	}

	// Print plugin info at startup.
	plugins := a.PM.List()
	logger.Info("plugins loaded", "count", len(plugins))
	for _, p := range plugins {
		logger.Info(
			"plugin",
			"plugin_id", p.PluginID,
			"name", p.Name,
			"version", p.Version,
			"author", p.Author,
			"commands", len(p.Commands),
			"events", len(p.Events),
		)

		for _, c := range p.Commands {
			logger.Info(
				"plugin command",
				"plugin_id", p.PluginID,
				"id", c.ID,
				"name", c.Name,
				"pattern", c.Pattern,
				"match_raw", c.MatchRaw,
				"handler", c.Handler,
			)
		}
		for _, e := range p.Events {
			logger.Info(
				"plugin event",
				"plugin_id", p.PluginID,
				"id", e.ID,
				"name", e.Name,
				"event", e.Event,
				"handler", e.Handler,
			)
		}
	}

	// Start WebUI.
	go func() {
		cfg := a.Store.Get()
		webuiURL, autoLoginURL := buildWebUIURLs(a.Web.Addr, cfg.WebUI.Password)
		logger.Info("webui listening", "addr", a.Web.Addr, "url", webuiURL, "auto_login_url", autoLoginURL)
		err := a.Web.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("webui server error", "err", err)
			stop()
		}
	}()

	// Start OneBot reverse ws server (blocks until ctx done).
	go func() {
		if err := a.OB.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("onebot reverse ws error", "err", err)
			stop()
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = a.Web.Shutdown(shutdownCtx)
	_ = a.OB.Shutdown(shutdownCtx)
	a.PH.Close()
}

func buildWebUIURLs(listenAddr string, password string) (string, string) {
	hostPort := normalizeDisplayHostPort(listenAddr)
	baseURL := fmt.Sprintf("http://%s/", hostPort)
	autoLoginURL := fmt.Sprintf("http://%s/login/?password=%s", hostPort, url.QueryEscape(password))
	return baseURL, autoLoginURL
}

func normalizeDisplayHostPort(listenAddr string) string {
	listenAddr = strings.TrimSpace(listenAddr)
	if listenAddr == "" {
		return "0.0.0.0:3000"
	}

	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return listenAddr
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}
