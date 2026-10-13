package collect

import (
	"strings"
	"testing"
	"time"

	"github.com/audemed44/logbook/internal/parse"
)

func TestGateRepeats(t *testing.T) {
	g := newGate(Limits{PerMessage: 5, PerMinute: 100, DailyQuota: 1000}, nil)
	at := time.Date(2026, 10, 13, 10, 0, 0, 0, time.Local)
	kept := 0
	for i := range 50 {
		// Numbers differ, the message's shape doesn't.
		v := g.admit(at.Add(time.Duration(i)*time.Second/2), parse.Entry{Level: parse.Error, Msg: "retry " + string(rune('0'+i%10)) + " failed"})
		if v.keep {
			kept++
		}
	}
	if kept != 5 {
		t.Fatalf("kept %d", kept)
	}
	v := g.admit(at.Add(2*time.Minute), parse.Entry{Level: parse.Info, Msg: "fine"})
	if !v.keep || len(v.notes) != 1 || !strings.HasPrefix(v.notes[0].entry.Msg, "held back repeats of: retry") || v.notes[0].entry.Attrs != `{"suppressed":45}` ||
		v.notes[0].entry.Kind != "suppressed" {
		t.Fatalf("after the minute: %+v", v)
	}
}

func TestGatePerMinute(t *testing.T) {
	g := newGate(Limits{PerMessage: 1000, PerMinute: 10}, nil)
	at := time.Date(2026, 10, 13, 10, 0, 0, 0, time.Local)
	kept := 0
	for i := range 30 {
		if g.admit(at, parse.Entry{Msg: "line " + strings.Repeat("x", i)}).keep {
			kept++
		}
	}
	if kept != 10 {
		t.Fatalf("kept %d", kept)
	}
}

func TestGateDailyQuota(t *testing.T) {
	g := newGate(Limits{PerMessage: 1000, PerMinute: 1000, DailyQuota: 100}, func(time.Time) int { return 90 })
	at := time.Date(2026, 10, 13, 23, 0, 0, 0, time.Local)
	kept, quota := 0, 0
	for i := range 50 {
		v := g.admit(at.Add(time.Duration(i)*time.Second), parse.Entry{Msg: "x" + strings.Repeat("y", i)})
		if v.keep {
			kept++
		}
		for _, n := range v.notes {
			if n.quota {
				quota++
			}
		}
	}
	if kept != 10 || quota != 1 {
		t.Fatalf("kept %d, quota notes %d", kept, quota)
	}
	// After midnight the count starts again (from what's stored that day).
	g.dayCount = func(time.Time) int { return 0 }
	if !g.admit(at.Add(2*time.Hour), parse.Entry{Msg: "new day"}).keep {
		t.Fatal("a new day is still over quota")
	}
}

func TestShape(t *testing.T) {
	if shape("took 123ms on try 4") != "took #ms on try #" {
		t.Fatal(shape("took 123ms on try 4"))
	}
}

func TestGateFlood(t *testing.T) {
	g := newGate(Limits{PerMessage: 5, PerMinute: 10}, nil)
	at := time.Date(2026, 10, 13, 10, 0, 0, 0, time.Local)
	for i := range 99 {
		if g.admit(at, parse.Entry{Msg: "spam"}).flood {
			t.Fatalf("flood at line %d", i+1)
		}
	}
	if !g.admit(at, parse.Entry{Msg: "spam"}).flood {
		t.Fatal("100 lines in a minute with PerMinute 10 isn't a flood")
	}
	if g.admit(at.Add(time.Minute), parse.Entry{Msg: "calm"}).flood {
		t.Fatal("still a flood in the next minute")
	}
}
