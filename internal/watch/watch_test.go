package watch

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/audemed44/logbook/internal/store"
)

func TestBurstAlertAndHeartbeat(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	src, _ := s.EnsureSource(ctx, "ledger", "")
	var lines []store.NewLine
	for range 12 {
		lines = append(lines, store.NewLine{SourceID: src.ID, Line: store.Line{Time: time.Now(), Level: 8, Msg: "mail sync failed"}})
	}
	if _, err := s.Write(ctx, lines, nil, nil); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var got []map[string]string
	pings := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/ping/abc" {
			pings++
			return
		}
		var m map[string]string
		json.NewDecoder(r.Body).Decode(&m)
		got = append(got, m)
	}))
	defer srv.Close()

	w := New(s, Config{Retention: time.Hour, MaxBytes: 1 << 30, NotifyURL: srv.URL + "/notify/logbook",
		BurstErrors: 10, HeartbeatURL: srv.URL + "/ping/abc", AppURL: "https://logbook.example"})
	w.round(ctx)
	w.round(ctx) // cooled down: no second alert
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0]["title"] != "Logbook: ledger: 12 errors in 5 minutes" || got[0]["type"] != "failure" ||
		!strings.Contains(got[0]["body"], "12× mail sync failed") || !strings.Contains(got[0]["body"], "/logs?source=ledger") {
		t.Fatalf("notifications: %v", got)
	}
	if pings != 2 {
		t.Fatalf("pings: %d", pings)
	}
}

func TestNoURL(t *testing.T) {
	_, err := http.Get("http://127.0.0.1:1/ping/secret-token")
	if err == nil || strings.Contains(noURL(err).Error(), "secret-token") {
		t.Fatalf("token in %v", noURL(err))
	}
	if e := errors.New("plain"); noURL(e) != e {
		t.Fatal("changed a plain error")
	}
}
