package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/audemed44/logbook/internal/collect"
	"github.com/audemed44/logbook/internal/store"
)

func newLogServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	ledger, _ := db.EnsureSource(ctx, "ledger", "")
	keep, _ := db.EnsureSource(ctx, "keep", "")
	now := time.Now()
	hour := now.Unix() / 3600
	_, err = db.Write(ctx, []store.NewLine{
		{SourceID: ledger.ID, Line: store.Line{Time: now.Add(-2 * time.Hour), Level: 0, Msg: "mail sync done", Trace: "abc"}},
		{SourceID: ledger.ID, Line: store.Line{Time: now.Add(-time.Hour), Level: 8, Msg: "mail sync failed", Err: "imap timeout", Trace: "abc"}},
		{SourceID: keep.ID, Line: store.Line{Time: now, Level: 4, Msg: "snapshot slow"}},
	}, []store.Count{{SourceID: ledger.ID, Hour: hour, Lines: 2, Errors: 1}, {SourceID: keep.ID, Hour: hour, Lines: 1, Warns: 1, Dropped: 7}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	web := fstest.MapFS{"index.html": {Data: []byte("<!doctype html>app")}}
	s := New(Options{Store: db, Token: token, Web: web, Collector: collect.New(nil, db, collect.Config{}),
		Limits: Limits{MaxBytes: 256 << 20}})
	return s, db
}

func TestLinesAPI(t *testing.T) {
	s, _ := newLogServer(t)
	h := s.Handler()
	get := func(path string) linesPage {
		t.Helper()
		rec := do(h, "GET", path, "", true)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
		}
		var p linesPage
		json.NewDecoder(rec.Body).Decode(&p)
		return p
	}
	if p := get("/api/lines"); len(p.Lines) != 3 || p.Lines[0].Source != "keep" || p.Next != "" {
		t.Fatalf("all: %+v", p)
	}
	if p := get("/api/lines?level=warn"); len(p.Lines) != 2 {
		t.Fatalf("warn+: %+v", p)
	}
	if p := get("/api/lines?source=ledger&q=imap"); len(p.Lines) != 1 || p.Lines[0].Err != "imap timeout" {
		t.Fatalf("search: %+v", p)
	}
	if p := get("/api/lines?since=90m"); len(p.Lines) != 2 {
		t.Fatalf("since: %+v", p)
	}
	if p := get("/api/lines?trace=abc"); len(p.Lines) != 2 {
		t.Fatalf("trace: %+v", p)
	}
	p := get("/api/lines?limit=2")
	if p.Next == "" {
		t.Fatal("no next cursor")
	}
	if rest := get("/api/lines?limit=2&before=" + p.Next); len(rest.Lines) != 1 || rest.Lines[0].Msg != "mail sync done" {
		t.Fatalf("next page: %+v", rest)
	}
	for _, bad := range []string{"/api/lines?level=loud", "/api/lines?since=yesterday", "/api/lines?before=x"} {
		if rec := do(h, "GET", bad, "", true); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", bad, rec.Code)
		}
	}
	all := get("/api/lines")
	rec := do(h, "GET", "/api/lines/"+itoa(all.Lines[0].ID)+"/around", "", true)
	var around []store.Line
	json.NewDecoder(rec.Body).Decode(&around)
	if rec.Code != http.StatusOK || len(around) != 1 {
		t.Fatalf("around: %d %+v", rec.Code, around)
	}
	if rec := do(h, "GET", "/api/lines/999/around", "", true); rec.Code != http.StatusNotFound {
		t.Fatalf("around missing: %d", rec.Code)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestOverviewSourcesWidget(t *testing.T) {
	s, _ := newLogServer(t)
	h := s.Handler()
	rec := do(h, "GET", "/api/overview", "", true)
	var ov struct {
		Usage    store.Usage     `json:"usage"`
		Hours    []store.Bucket  `json:"hours"`
		Problems []store.Problem `json:"problems"`
	}
	json.NewDecoder(rec.Body).Decode(&ov)
	if rec.Code != http.StatusOK || ov.Usage.Lines != 3 || len(ov.Hours) != 24 || len(ov.Problems) != 2 {
		t.Fatalf("overview: %d %+v", rec.Code, ov)
	}
	rec = do(h, "GET", "/api/sources", "", true)
	var src []sourceOut
	json.NewDecoder(rec.Body).Decode(&src)
	if len(src) != 2 || src[0].Name != "keep" || src[0].Dropped != 7 || src[0].Status != "gone" {
		t.Fatalf("sources: %+v", src)
	}
	rec = do(h, "GET", "/api/foyer/widget", "", true)
	var widget foyerWidget
	json.NewDecoder(rec.Body).Decode(&widget)
	if widget.Version != 1 || widget.Stats[0].Value != "1" || widget.Stats[0].Tone != "bad" || len(widget.Items) != 1 ||
		widget.Items[0].URL != "/logs?source=ledger&level=error" || widget.Stats[3].Label != "Held back" {
		t.Fatalf("widget %+v", widget)
	}
}

func TestTail(t *testing.T) {
	s, _ := newLogServer(t)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/tail?source=ledger&level=error", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("type %q", resp.Header.Get("Content-Type"))
	}
	s.Collector.Hub.Publish([]store.Line{
		{ID: 10, Source: "keep", Level: 8, Msg: "other app"},
		{ID: 11, Source: "ledger", Level: 0, Msg: "too quiet"},
		{ID: 12, Source: "ledger", Level: 8, Msg: "wanted"},
	})
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			var l store.Line
			json.Unmarshal([]byte(data), &l)
			if l.ID != 12 {
				t.Fatalf("got %+v", l)
			}
			return
		}
	}
	t.Fatal("no line")
}
