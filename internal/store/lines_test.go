package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func add(t *testing.T, s *Store, src Source, at time.Time, level int, msg string, mods ...func(*Line)) {
	t.Helper()
	l := Line{Time: at, Level: level, Msg: msg}
	for _, m := range mods {
		m(&l)
	}
	if _, err := s.Write(context.Background(), []NewLine{{Line: l, SourceID: src.ID}}, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestLinesQueries(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	a, err := s.EnsureSource(ctx, "ledger", "ghcr.io/audemed44/ledger")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.EnsureSource(ctx, "keep", "")
	if again, _ := s.EnsureSource(ctx, "ledger", ""); again.ID != a.ID || again.Image != "ghcr.io/audemed44/ledger" {
		t.Fatalf("source again: %+v", again)
	}
	now := time.Now().Truncate(time.Millisecond)
	add(t, s, a, now.Add(-3*time.Minute), 0, "mail sync done", func(l *Line) { l.Kind = "job"; l.Trace = "t1" })
	add(t, s, a, now.Add(-2*time.Minute), 8, "mail sync failed", func(l *Line) { l.Err = "imap: connection reset"; l.Trace = "t1" })
	add(t, s, b, now.Add(-1*time.Minute), 4, "snapshot slow", func(l *Line) { l.Attrs = `{"repo":"hdd"}` })
	add(t, s, b, now, 8, "snapshot failed")

	all, err := s.Lines(ctx, Filter{})
	if err != nil || len(all) != 4 || all[0].Msg != "snapshot failed" {
		t.Fatalf("all: %v %+v", err, all)
	}
	got, _ := s.Lines(ctx, Filter{Sources: []string{"ledger"}, HasLevel: true, MinLevel: 8})
	if len(got) != 1 || got[0].Err != "imap: connection reset" {
		t.Fatalf("ledger errors: %+v", got)
	}
	got, _ = s.Lines(ctx, Filter{Q: "conn"})
	if len(got) != 1 || got[0].Source != "ledger" {
		t.Fatalf("search in err: %+v", got)
	}
	got, err = s.Lines(ctx, Filter{Q: `"hdd"`})
	if err != nil || len(got) != 1 || got[0].Msg != "snapshot slow" {
		t.Fatalf("search in attrs, quotes made safe: %+v", got)
	}
	got, _ = s.Lines(ctx, Filter{Trace: "t1"})
	if len(got) != 2 {
		t.Fatalf("trace: %+v", got)
	}
	page, _ := s.Lines(ctx, Filter{Limit: 2})
	rest, _ := s.Lines(ctx, Filter{Limit: 2, Before: CursorOf(page[1])})
	if len(rest) != 2 || rest[0].Msg != "mail sync failed" {
		t.Fatalf("page 2: %+v", rest)
	}
	if _, err := s.Lines(ctx, Filter{Before: "nonsense"}); err != ErrBadQuery {
		t.Fatalf("bad cursor: %v", err)
	}
	newer, _ := s.Lines(ctx, Filter{After: all[2].ID})
	if len(newer) != 2 || newer[0].ID != all[1].ID {
		t.Fatalf("after: %+v", newer)
	}
	around, _ := s.Around(ctx, all[2].ID, 5)
	if len(around) != 2 || around[0].Msg != "mail sync done" {
		t.Fatalf("around: %+v", around)
	}
	probs, _ := s.Problems(ctx, now.Add(-time.Hour), 4, 10)
	if len(probs) != 3 || probs[0].Level != 8 {
		t.Fatalf("problems: %+v", probs)
	}
}

func TestCountsAndSources(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	a, _ := s.EnsureSource(ctx, "lookout", "")
	hour := time.Now().Unix() / 3600
	now := time.Now()
	for range 2 {
		if _, err := s.Write(ctx, nil, []Count{{SourceID: a.ID, Hour: hour, Lines: 10, Errors: 2, Dropped: 1}},
			[]Cursor{{SourceID: a.ID, At: now, LastLine: now}}); err != nil {
			t.Fatal(err)
		}
	}
	day := time.Now().Truncate(time.Hour)
	n, _ := s.LinesSince(ctx, day)
	if n[a.ID] != 20 {
		t.Fatalf("since: %v", n)
	}
	src, _ := s.Sources(ctx, day)
	if len(src) != 1 || src[0].Lines24h != 20 || src[0].Errors24h != 4 || src[0].Dropped != 2 || src[0].Today != 20 {
		t.Fatalf("sources: %+v", src)
	}
	again, _ := s.EnsureSource(ctx, "lookout", "")
	if again.Cursor.UnixNano() != now.UnixNano() {
		t.Fatalf("cursor %v, want %v", again.Cursor, now)
	}
	hours, _ := s.Hourly(ctx, time.Now().Add(-5*time.Hour), "")
	if len(hours) != 6 || hours[5].Lines != 20 {
		t.Fatalf("hourly: %+v", hours)
	}
}

func TestPrune(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	src, _ := s.EnsureSource(ctx, "noisy", "")
	old := time.Now().Add(-40 * 24 * time.Hour)
	add(t, s, src, old, 8, "ancient")

	// About 6 MB of lines.
	now := time.Now()
	batch := make([]NewLine, 0, 1000)
	for i := range 6000 {
		batch = append(batch, NewLine{SourceID: src.ID, Line: Line{
			Time: now.Add(time.Duration(i) * time.Millisecond), Level: 8,
			Msg: fmt.Sprintf("failure %d %s", i, strings.Repeat("payload ", 100)),
		}})
		if len(batch) == cap(batch) {
			if _, err := s.Write(ctx, batch, nil, nil); err != nil {
				t.Fatal(err)
			}
			batch = batch[:0]
		}
	}
	before, _ := s.FileBytes(ctx)
	p, err := s.Prune(ctx, 30*24*time.Hour, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	if p.Expired != 1 || p.ForSize == 0 {
		t.Fatalf("pruned %+v", p)
	}
	u, _ := s.Usage(ctx)
	if u.Bytes > 2<<20 {
		t.Fatalf("still %d bytes in use", u.Bytes)
	}
	after, _ := s.FileBytes(ctx)
	if after >= before/2 {
		t.Fatalf("file didn't shrink: %d → %d", before, after)
	}
	newest, _ := s.Lines(ctx, Filter{Limit: 1})
	if !strings.HasPrefix(newest[0].Msg, "failure 5999 ") {
		t.Fatalf("pruned the newest: %.30s", newest[0].Msg)
	}
	if got, _ := s.Lines(ctx, Filter{Q: "ancient"}); len(got) != 0 {
		t.Fatal("the search index kept a pruned line")
	}
}
