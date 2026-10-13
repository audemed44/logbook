// Command logbook serves Logbook's API and frontend from one small binary.
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // the runtime image may have no zoneinfo; TZ needs this

	"github.com/audemed44/logbook/internal/collect"
	"github.com/audemed44/logbook/internal/docker"
	"github.com/audemed44/logbook/internal/logging"
	"github.com/audemed44/logbook/internal/server"
	"github.com/audemed44/logbook/internal/store"
	"github.com/audemed44/logbook/internal/watch"
	"github.com/audemed44/logbook/web"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}
	logging.Setup("logbook", os.Stderr)

	token := os.Getenv("LOGBOOK_TOKEN")
	if token == "" {
		slog.Error("set LOGBOOK_TOKEN: it's what you sign in with, and what Foyer uses for the widget")
		os.Exit(1)
	}
	dataDir := env("LOGBOOK_DATA_DIR", "/data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		slog.Error("could not create the data folder", "err", err)
		os.Exit(1)
	}
	db, err := store.Open(filepath.Join(dataDir, "logbook.db"))
	if err != nil {
		slog.Error("could not open the database", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		panic(err)
	}
	cfg, err := loadConfig()
	if err != nil {
		slog.Error("bad configuration", "err", err)
		os.Exit(1)
	}
	watcher := watch.New(db, watch.Config{
		Retention: cfg.retention, MaxBytes: cfg.maxBytes, NotifyURL: os.Getenv("LOGBOOK_NOTIFY_URL"),
		BurstErrors: cfg.burstErrors, HeartbeatURL: os.Getenv("LOGBOOK_HEARTBEAT_URL"), AppURL: os.Getenv("LOGBOOK_URL"),
	})
	collector := collect.New(docker.New(env("LOGBOOK_DOCKER_SOCKET", "/var/run/docker.sock")), db, collect.Config{
		Limits: cfg.limits, MaxLine: cfg.maxLine, Backfill: cfg.backfill, Exclude: cfg.exclude,
		OnQuota: func(source string, quota int) {
			watcher.Notify(ctx, source+" hit its daily quota",
				fmt.Sprintf("%d lines today: the rest of its lines are dropped until midnight.", quota), "warning")
		},
	})
	go collector.Run(ctx)
	go watcher.Run(ctx)

	app := server.New(server.Options{
		Store: db, Collector: collector, Watcher: watcher, Token: token, FoyerURL: foyerURL(), Web: dist,
		Limits: server.Limits{
			MaxBytes: cfg.maxBytes, RetentionDay: int(cfg.retention.Hours() / 24), DailyQuota: cfg.limits.DailyQuota,
			PerMinute: cfg.limits.PerMinute, PerMessage: cfg.limits.PerMessage, MaxLine: cfg.maxLine,
			BurstErrors: cfg.burstErrors, Notify: os.Getenv("LOGBOOK_NOTIFY_URL") != "",
			Heartbeat: os.Getenv("LOGBOOK_HEARTBEAT_URL") != "", Excluded: os.Getenv("LOGBOOK_EXCLUDE"),
		},
	})

	srv := &http.Server{
		Addr:              ":" + env("LOGBOOK_PORT", "8080"),
		Handler:           app.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          logging.ServerErrorLog(),
		// Requests end on shutdown too, so live tails let go.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	slog.Info("logbook listening", "addr", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

// healthcheck is the image's HEALTHCHECK: the runtime image has no curl.
func healthcheck() int {
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + env("LOGBOOK_PORT", "8080") + "/healthz")
	if err != nil {
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return 1
	}
	return 0
}

// foyerURL is HOMEPAGE_URL, the link back to Foyer in the header, when
// it's an http(s) address.
func foyerURL() string {
	u := os.Getenv("HOMEPAGE_URL")
	if u != "" && !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
		slog.Warn("HOMEPAGE_URL isn't an http(s) address; ignoring it", "url", u)
		return ""
	}
	return u
}
