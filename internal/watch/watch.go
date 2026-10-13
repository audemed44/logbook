// Package watch runs Logbook's upkeep every minute: prune to the size cap
// and retention, alert on error bursts, and ping Lookout's heartbeat.
package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/audemed44/logbook/internal/logging"
	"github.com/audemed44/logbook/internal/store"
)

type Config struct {
	Retention time.Duration
	MaxBytes  int64
	// NotifyURL takes Apprise's JSON body (Lookout's /notify/<key>).
	NotifyURL string
	// BurstErrors in BurstWindow from one container sends an alert, at
	// most once per Cooldown for that container. 0 turns alerts off.
	BurstErrors int
	BurstWindow time.Duration
	Cooldown    time.Duration
	// HeartbeatURL is pinged after each healthy round (Lookout's
	// /ping/<token>).
	HeartbeatURL string
	// AppURL links alerts back to Logbook.
	AppURL string
}

type Watcher struct {
	store *store.Store
	cfg   Config
	http  *http.Client

	mu        sync.Mutex
	alerted   map[string]time.Time
	lastPrune time.Time
	LastRound Round
}

// Round is what the last upkeep did, for the UI.
type Round struct {
	At     time.Time    `json:"at"`
	Pruned store.Pruned `json:"pruned"`
	Err    string       `json:"err,omitempty"`
}

func New(s *store.Store, cfg Config) *Watcher {
	if cfg.BurstWindow == 0 {
		cfg.BurstWindow = 5 * time.Minute
	}
	if cfg.Cooldown == 0 {
		cfg.Cooldown = time.Hour
	}
	return &Watcher{store: s, cfg: cfg, http: &http.Client{Timeout: 10 * time.Second}, alerted: map[string]time.Time{}}
}

func (w *Watcher) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	w.round(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.round(ctx)
		}
	}
}

func (w *Watcher) Last() Round {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.LastRound
}

func (w *Watcher) round(ctx context.Context) {
	ctx, run := logging.QuietJob(ctx, "upkeep")
	r := Round{At: time.Now()}
	err := w.prune(ctx, &r)
	if err == nil {
		err = w.bursts(ctx)
	}
	if err == nil {
		w.ping(ctx)
	} else {
		r.Err = err.Error()
	}
	run.End(err, "expired", r.Pruned.Expired, "for_size", r.Pruned.ForSize)
	w.mu.Lock()
	w.LastRound = r
	w.mu.Unlock()
}

// prune runs every ten minutes, or right away when the database is over
// its cap.
func (w *Watcher) prune(ctx context.Context, r *Round) error {
	u, err := w.store.Usage(ctx)
	if err != nil {
		return err
	}
	over := w.cfg.MaxBytes > 0 && u.Bytes > w.cfg.MaxBytes
	if !over && time.Since(w.lastPrune) < 10*time.Minute {
		return nil
	}
	w.lastPrune = time.Now()
	p, err := w.store.Prune(ctx, w.cfg.Retention, w.cfg.MaxBytes)
	r.Pruned = p
	if err != nil {
		return fmt.Errorf("prune: %w", err)
	}
	if p.ForSize > 0 {
		slog.WarnContext(ctx, "pruned the oldest lines to stay under the size cap", "lines", p.ForSize, "max_bytes", w.cfg.MaxBytes)
	}
	return nil
}

// bursts alerts on containers with many errors in the last few minutes.
func (w *Watcher) bursts(ctx context.Context) error {
	if w.cfg.BurstErrors <= 0 || w.cfg.NotifyURL == "" {
		return nil
	}
	since := time.Now().Add(-w.cfg.BurstWindow)
	counts, err := w.store.ErrorsSince(ctx, since)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(counts))
	for name, n := range counts {
		if n >= w.cfg.BurstErrors && time.Since(w.alerted[name]) >= w.cfg.Cooldown {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		w.alerted[name] = time.Now()
		probs, err := w.store.Problems(ctx, since, 8, 50)
		if err != nil {
			return err
		}
		var top []string
		for _, p := range probs {
			if p.Source == name && len(top) < 3 {
				top = append(top, fmt.Sprintf("%d× %s", p.Count, p.Msg))
			}
		}
		title := fmt.Sprintf("%s: %d errors in %d minutes", name, counts[name], int(w.cfg.BurstWindow.Minutes()))
		body := strings.Join(top, "\n")
		if w.cfg.AppURL != "" {
			body += "\n" + strings.TrimRight(w.cfg.AppURL, "/") + "/logs?source=" + url.QueryEscape(name) + "&level=error"
		}
		w.Notify(ctx, title, body, "failure")
	}
	return nil
}

// Notify sends one notification, Apprise style. Failures are logged, not
// returned: a down notifier mustn't stop the upkeep.
func (w *Watcher) Notify(ctx context.Context, title, body, typ string) {
	if w.cfg.NotifyURL == "" {
		return
	}
	b, _ := json.Marshal(map[string]string{"title": "Logbook: " + title, "body": body, "type": typ, "tag": "logbook"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.cfg.NotifyURL, bytes.NewReader(b))
	if err != nil {
		slog.WarnContext(ctx, "notify failed", "err", noURL(err))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.http.Do(req)
	if err != nil {
		slog.WarnContext(ctx, "notify failed", "err", noURL(err))
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		slog.WarnContext(ctx, "notify failed", "status", resp.StatusCode)
		return
	}
	slog.InfoContext(ctx, "notification sent", "title", title)
}

func (w *Watcher) ping(ctx context.Context) {
	if w.cfg.HeartbeatURL == "" {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.cfg.HeartbeatURL, nil)
	if err != nil {
		return
	}
	resp, err := w.http.Do(req)
	if err != nil {
		slog.WarnContext(ctx, "heartbeat ping failed", "err", noURL(err))
		return
	}
	resp.Body.Close()
}

// noURL drops the address from an HTTP client error: the notify and ping
// URLs carry tokens.
func noURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}
	return err
}
