package collect

import (
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/audemed44/logbook/internal/parse"
)

// Limits bound what one container can put in the database, so a failing
// app writes a few lines a minute and a count, not gigabytes.
type Limits struct {
	// PerMessage is how many lines with the same level and (roughly) the
	// same message a container may write in a minute; the rest are
	// counted, and one summary line follows when the minute ends.
	PerMessage int
	// PerMinute caps all of a container's lines in a minute.
	PerMinute int
	// Flood is how many lines in a minute make a container a flood: past
	// it, Logbook stops reading that container until the minute ends and
	// then picks up from the end of its log, so a runaway app costs it
	// next to nothing. 0 means 10× PerMinute.
	Flood int
	// DailyQuota caps a container's lines in a day (local midnight to
	// midnight). Past it, its lines are dropped and counted until the
	// next day, after one line saying so.
	DailyQuota int
}

// gate applies Limits to one source. Windows follow the lines' own
// timestamps, so catching up on an hour of backlog isn't squeezed into one
// minute. Hold mu around admit and roll.
type gate struct {
	mu     sync.Mutex
	limits Limits
	// dayCount reads how many lines a source already stored on a day,
	// for a gate that starts mid-day (after a restart).
	dayCount func(day time.Time) int

	day      time.Time // local midnight of the day being counted
	today    int
	quotaHit bool

	window   time.Time // start of the current minute
	total    int
	seen     int // every line offered this minute, kept or not
	perMsg   map[string]*msgCount
	overflow int       // dropped when too many distinct messages
	lastSeen time.Time // wall clock of the last entry, for quiet minutes
}

type msgCount struct {
	level   int
	msg     string
	n       int
	dropped int
}

const maxKeys = 2000

func newGate(l Limits, dayCount func(time.Time) int) *gate {
	return &gate{limits: l, dayCount: dayCount, perMsg: map[string]*msgCount{}}
}

// verdict is what to do with one entry.
type verdict struct {
	keep bool
	// flood: the container is past Limits.Flood this minute; stop
	// reading it for now.
	flood bool
	// notes are lines the gate adds: summaries of a minute's drops, the
	// quota notice. They're stored whatever the limits say.
	notes []note
}

type note struct {
	at    time.Time
	entry parse.Entry
	// quota is set on the line that says the daily quota was reached.
	quota bool
}

// admit decides about one entry at time t.
func (g *gate) admit(t time.Time, e parse.Entry) verdict {
	var v verdict
	g.lastSeen = time.Now()
	v.notes = g.roll(t)
	g.seen++
	if f := g.floodLimit(); f > 0 && g.seen >= f {
		v.flood = true
	}
	day := midnight(t)
	// A late line from the day before only meets the minute limits.
	if day.After(g.day) {
		g.day, g.quotaHit, g.today = day, false, 0
		if g.dayCount != nil {
			g.today = g.dayCount(day)
		}
	}
	if g.limits.DailyQuota > 0 && day.Equal(g.day) && g.today >= g.limits.DailyQuota {
		if !g.quotaHit {
			g.quotaHit = true
			v.notes = append(v.notes, note{at: t, quota: true, entry: parse.Entry{
				Level: parse.Warn, Kind: "limit",
				Msg:   "daily quota reached: dropping this container's lines until midnight",
				Attrs: `{"quota":` + strconv.Itoa(g.limits.DailyQuota) + `}`,
			}})
		}
		return v
	}
	key := strconv.Itoa(e.Level) + "\x00" + shape(e.Msg)
	c := g.perMsg[key]
	if c == nil {
		if len(g.perMsg) >= maxKeys {
			g.overflow++
			return v
		}
		c = &msgCount{level: e.Level, msg: e.Msg}
		g.perMsg[key] = c
	}
	if (g.limits.PerMessage > 0 && c.n >= g.limits.PerMessage) || (g.limits.PerMinute > 0 && g.total >= g.limits.PerMinute) {
		c.dropped++
		return v
	}
	c.n++
	g.total++
	if day.Equal(g.day) {
		g.today++
	}
	v.keep = true
	return v
}

func (g *gate) floodLimit() int {
	if g.limits.Flood > 0 {
		return g.limits.Flood
	}
	return 10 * g.limits.PerMinute
}

// roll ends the minute when t is past it and returns its summaries.
func (g *gate) roll(t time.Time) []note {
	if !g.window.IsZero() && t.Sub(g.window) < time.Minute {
		return nil
	}
	var out []note
	end := g.window.Add(time.Minute)
	if g.window.IsZero() {
		end = t
	}
	for _, c := range g.perMsg {
		if c.dropped == 0 {
			continue
		}
		out = append(out, note{at: end, entry: parse.Entry{
			Level: c.level, Kind: "suppressed",
			Msg:   "held back repeats of: " + c.msg,
			Attrs: `{"suppressed":` + strconv.Itoa(c.dropped) + `}`,
		}})
	}
	if g.overflow > 0 {
		out = append(out, note{at: end, entry: parse.Entry{
			Level: parse.Warn, Kind: "suppressed",
			Msg:   "held back lines: too many different messages in a minute",
			Attrs: `{"suppressed":` + strconv.Itoa(g.overflow) + `}`,
		}})
	}
	g.window = t.Truncate(time.Minute)
	g.total, g.overflow, g.seen = 0, 0, 0
	clear(g.perMsg)
	return out
}

// shape is a message with its numbers taken out, so "retry 3 of 5" and
// "retry 4 of 5" count as the same message.
func shape(msg string) string {
	if len(msg) > 200 {
		msg = msg[:200]
	}
	var b strings.Builder
	digits := false
	for _, r := range msg {
		if unicode.IsDigit(r) {
			if !digits {
				b.WriteByte('#')
			}
			digits = true
			continue
		}
		digits = false
		b.WriteRune(r)
	}
	return b.String()
}

func midnight(t time.Time) time.Time {
	y, m, d := t.Local().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}
