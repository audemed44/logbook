package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/audemed44/logbook/internal/parse"
	"github.com/audemed44/logbook/internal/store"
)

// filterFrom reads the query string shared by /api/lines and /api/tail:
//
//	source  container names, comma separated
//	level   debug, info, warn or error: that level and worse
//	kind    request, job, audit, docker, suppressed, limit
//	q       words to find in the message, error and fields
//	trace   one request or job run
//	since   15m, 24h, 7d, … or an RFC 3339 time; until likewise
func filterFrom(r *http.Request) (store.Filter, error) {
	q := r.URL.Query()
	f := store.Filter{Kind: q.Get("kind"), Q: q.Get("q"), Trace: q.Get("trace"), Before: q.Get("before")}
	for _, s := range strings.Split(q.Get("source"), ",") {
		if s = strings.TrimSpace(s); s != "" {
			f.Sources = append(f.Sources, s)
		}
	}
	if l := q.Get("level"); l != "" {
		lvl, ok := levels[l]
		if !ok {
			return f, fmt.Errorf("level: one of debug, info, warn, error")
		}
		f.MinLevel, f.HasLevel = lvl, true
	}
	var err error
	if f.Since, err = when(q.Get("since")); err != nil {
		return f, fmt.Errorf("since: %w", err)
	}
	if f.Until, err = when(q.Get("until")); err != nil {
		return f, fmt.Errorf("until: %w", err)
	}
	if a := q.Get("after"); a != "" {
		if f.After, err = strconv.ParseInt(a, 10, 64); err != nil {
			return f, errors.New("after: an id")
		}
	}
	if l := q.Get("limit"); l != "" {
		f.Limit, _ = strconv.Atoi(l)
	}
	return f, nil
}

var levels = map[string]int{"debug": parse.Debug, "info": parse.Info, "warn": parse.Warn, "error": parse.Error}

// when reads "15m", "24h", "7d" as that long ago, or an RFC 3339 time.
func when(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if strings.HasSuffix(s, "d") {
		if n, err := strconv.Atoi(strings.TrimSuffix(s, "d")); err == nil && n > 0 {
			return time.Now().AddDate(0, 0, -n), nil
		}
	}
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return time.Now().Add(-d), nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return t, errors.New("a duration like 24h or 7d, or an RFC 3339 time")
	}
	return t, nil
}

type linesPage struct {
	Lines []store.Line `json:"lines"`
	// Next continues with older lines (as before=…); "" at the end.
	Next string `json:"next"`
}

func (s *Server) listLines(w http.ResponseWriter, r *http.Request) {
	f, err := filterFrom(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 200
	}
	lines, err := s.Store.Lines(r.Context(), f)
	if errors.Is(err, store.ErrBadQuery) {
		writeError(w, http.StatusBadRequest, "before: a cursor from next")
		return
	}
	if err != nil {
		storeError(w, r, err)
		return
	}
	page := linesPage{Lines: lines}
	if f.After == 0 && len(lines) == f.Limit {
		page.Next = store.CursorOf(lines[len(lines)-1])
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) around(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	lines, err := s.Store.Around(r.Context(), id, 25)
	if err != nil {
		storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, lines)
}

// tail streams new lines that match the filter as server-sent events. A
// "missed" event means the tail fell behind and should reload.
func (s *Server) tail(w http.ResponseWriter, r *http.Request) {
	f, err := filterFrom(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.Collector == nil {
		writeError(w, http.StatusServiceUnavailable, "not collecting")
		return
	}
	sub := s.Collector.Hub.Subscribe()
	if sub == nil {
		writeError(w, http.StatusTooManyRequests, "too many live tails open")
		return
	}
	defer s.Collector.Hub.Unsubscribe(sub)
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_ = rc.Flush()

	match := matcher(f)
	keepAlive := time.NewTicker(25 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepAlive.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return
			}
		case <-sub.Missed:
			if _, err := fmt.Fprint(w, "event: missed\ndata: {}\n\n"); err != nil {
				return
			}
		case l := <-sub.C:
			if !match(l) {
				continue
			}
			b, _ := json.Marshal(l)
			if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
				return
			}
		}
		if err := rc.Flush(); err != nil {
			return
		}
	}
}

// matcher is the filter applied in memory, for live lines. Words match
// anywhere, ignoring case.
func matcher(f store.Filter) func(store.Line) bool {
	sources := map[string]bool{}
	for _, s := range f.Sources {
		sources[s] = true
	}
	words := strings.Fields(strings.ToLower(f.Q))
	return func(l store.Line) bool {
		if len(sources) > 0 && !sources[l.Source] {
			return false
		}
		if f.HasLevel && l.Level < f.MinLevel {
			return false
		}
		if f.Kind != "" && l.Kind != f.Kind {
			return false
		}
		if f.Trace != "" && l.Trace != f.Trace {
			return false
		}
		if len(words) > 0 {
			hay := strings.ToLower(l.Msg + " " + l.Err + " " + l.Attrs)
			for _, w := range words {
				if !strings.Contains(hay, w) {
					return false
				}
			}
		}
		return true
	}
}

type sourceOut struct {
	store.SourceStats
	Status string `json:"status"` // following, stopped, excluded, gone
}

func (s *Server) listSources(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.Sources(r.Context(), midnight(time.Now()))
	if err != nil {
		storeError(w, r, err)
		return
	}
	status := map[string]string{}
	if s.Collector != nil {
		status = s.Collector.Status()
	}
	out := make([]sourceOut, 0, len(list))
	for _, src := range list {
		st := status[src.Name]
		if st == "" {
			st = "gone"
		}
		out = append(out, sourceOut{SourceStats: src, Status: st})
		delete(status, src.Name)
	}
	// Excluded containers never get a source; list them too.
	for name, st := range status {
		if st == "excluded" {
			out = append(out, sourceOut{SourceStats: store.SourceStats{Name: name}, Status: st})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// Limits are the caps Logbook runs with, shown in the UI.
type Limits struct {
	MaxBytes     int64  `json:"max_bytes"`
	RetentionDay int    `json:"retention_days"`
	DailyQuota   int    `json:"daily_quota"`
	PerMinute    int    `json:"per_minute"`
	PerMessage   int    `json:"per_message"`
	MaxLine      int    `json:"max_line"`
	BurstErrors  int    `json:"burst_errors"`
	Notify       bool   `json:"notify"`
	Heartbeat    bool   `json:"heartbeat"`
	Excluded     string `json:"excluded,omitempty"`
}

type overview struct {
	Usage    store.Usage     `json:"usage"`
	FileSize int64           `json:"file_bytes"`
	Limits   Limits          `json:"limits"`
	Hours    []store.Bucket  `json:"hours"`
	Problems []store.Problem `json:"problems"`
	Upkeep   any             `json:"upkeep,omitempty"`
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var out overview
	var err error
	if out.Usage, err = s.Store.Usage(ctx); err != nil {
		storeError(w, r, err)
		return
	}
	if out.FileSize, err = s.Store.FileBytes(ctx); err != nil {
		storeError(w, r, err)
		return
	}
	since := time.Now().Add(-23 * time.Hour).Truncate(time.Hour)
	if out.Hours, err = s.Store.Hourly(ctx, since, r.URL.Query().Get("source")); err != nil {
		storeError(w, r, err)
		return
	}
	if out.Problems, err = s.Store.Problems(ctx, time.Now().Add(-24*time.Hour), parse.Warn, 20); err != nil {
		storeError(w, r, err)
		return
	}
	out.Limits = s.Limits
	if s.Watcher != nil {
		out.Upkeep = s.Watcher.Last()
	}
	writeJSON(w, http.StatusOK, out)
}

func midnight(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}
