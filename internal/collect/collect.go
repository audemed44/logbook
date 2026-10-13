// Package collect follows every container's logs through the Docker
// socket, parses them, applies the per-container limits, and writes them
// to the store in batches.
package collect

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/audemed44/logbook/internal/docker"
	"github.com/audemed44/logbook/internal/logging"
	"github.com/audemed44/logbook/internal/parse"
	"github.com/audemed44/logbook/internal/store"
)

type Config struct {
	Limits  Limits
	MaxLine int // bytes kept of one line's message, error and fields
	// Backfill is how far back to read a container seen for the first
	// time.
	Backfill time.Duration
	// Exclude names containers not to collect. A container can also opt
	// out with the label logbook.exclude=true.
	Exclude map[string]bool
	// OnQuota is called when a container reaches its daily quota.
	OnQuota func(source string, quota int)
}

type Collector struct {
	docker *docker.Client
	store  *store.Store
	cfg    Config
	Hub    *Hub

	in chan record

	mu        sync.Mutex
	following map[string]context.CancelFunc // by container id
	names     map[int64]string              // source id → name
	gates     map[int64]*gate
	status    map[string]string // container name → following, excluded, stopped
}

// record is what followers hand the writer: a line to store, or only a
// drop to count, plus how far the source has been read.
type record struct {
	sourceID int64
	line     *store.Line
	dropped  int
	at       time.Time // Docker's timestamp: the source's new cursor
	read     bool      // at is a line read from Docker, so it moves the cursor
}

func New(d *docker.Client, s *store.Store, cfg Config) *Collector {
	if cfg.MaxLine <= 0 {
		cfg.MaxLine = 8 << 10
	}
	return &Collector{
		docker: d, store: s, cfg: cfg, Hub: NewHub(),
		in:        make(chan record, 4096),
		following: map[string]context.CancelFunc{},
		names:     map[int64]string{},
		gates:     map[int64]*gate{},
		status:    map[string]string{},
	}
}

// Status is each container's collection state, by name.
func (c *Collector) Status() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]string, len(c.status))
	for k, v := range c.status {
		out[k] = v
	}
	return out
}

// Run collects until ctx ends.
func (c *Collector) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); c.write(ctx) }()
	go func() { defer wg.Done(); c.watchEvents(ctx) }()
	c.reconcile(ctx)
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case <-t.C:
			c.reconcile(ctx)
		}
	}
}

// reconcile starts following running containers that aren't followed yet:
// at start, and as a backstop for missed events.
func (c *Collector) reconcile(ctx context.Context) {
	ctrs, err := c.docker.List(ctx)
	if err != nil {
		if ctx.Err() == nil {
			slog.ErrorContext(ctx, "list containers failed", "err", err)
		}
		return
	}
	for _, ctr := range ctrs {
		if ctr.Running {
			c.follow(ctx, ctr.ID)
		} else {
			c.mu.Lock()
			if _, ok := c.status[ctr.Name]; !ok || c.status[ctr.Name] == "following" {
				if _, busy := c.following[ctr.ID]; !busy {
					c.status[ctr.Name] = "stopped"
				}
			}
			c.mu.Unlock()
		}
	}
}

func (c *Collector) watchEvents(ctx context.Context) {
	since := time.Now()
	backoff := time.Second
	for ctx.Err() == nil {
		err := c.docker.Events(ctx, since, func(e docker.Event) {
			since = e.Time
			backoff = time.Second
			c.event(ctx, e)
		})
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			slog.WarnContext(ctx, "docker events stopped", "err", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Minute)
	}
}

// event records a container starting, stopping or running out of memory,
// as a line of its own, and starts following it when it starts.
func (c *Collector) event(ctx context.Context, e docker.Event) {
	if e.Action == "destroy" {
		return
	}
	if e.Name == "" || c.excluded(e.Name, nil) {
		return
	}
	entry := parse.Entry{Level: parse.Info, Kind: "docker"}
	switch e.Action {
	case "start":
		entry.Msg = "container started"
	case "oom":
		entry.Level, entry.Msg = parse.Error, "container ran out of memory"
	case "die":
		entry.Msg = "container stopped"
		entry.Attrs = `{"exit_code":` + strconv.Quote(e.ExitCode) + `}`
		switch e.ExitCode {
		case "0", "143", "130": // clean, SIGTERM, SIGINT
		case "137":
			entry.Level, entry.Msg = parse.Warn, "container killed"
		default:
			entry.Level, entry.Msg = parse.Error, "container exited with code "+e.ExitCode
		}
	default:
		return
	}
	src, err := c.source(ctx, e.Name, "")
	if err != nil {
		slog.ErrorContext(ctx, "store source failed", "err", err)
		return
	}
	c.offer(ctx, src.ID, e.Time, entry, false, false)
	if e.Action == "start" {
		c.follow(ctx, e.ID)
	}
}

func (c *Collector) excluded(name string, labels map[string]string) bool {
	return c.cfg.Exclude[name] || strings.EqualFold(labels["logbook.exclude"], "true")
}

func (c *Collector) source(ctx context.Context, name, image string) (store.Source, error) {
	src, err := c.store.EnsureSource(ctx, name, image)
	if err != nil {
		return src, err
	}
	c.mu.Lock()
	c.names[src.ID] = name
	if c.gates[src.ID] == nil {
		id := src.ID
		c.gates[id] = newGate(c.cfg.Limits, func(day time.Time) int {
			n, err := c.store.LinesSince(context.Background(), day)
			if err != nil {
				slog.Warn("read today's counts failed", "err", err)
			}
			return n[id]
		})
	}
	c.mu.Unlock()
	return src, nil
}

// follow starts a follower for a container unless one is running.
func (c *Collector) follow(ctx context.Context, id string) {
	c.mu.Lock()
	if _, ok := c.following[id]; ok {
		c.mu.Unlock()
		return
	}
	fctx, cancel := context.WithCancel(ctx)
	c.following[id] = cancel
	c.mu.Unlock()
	go func() {
		defer func() {
			c.mu.Lock()
			delete(c.following, id)
			c.mu.Unlock()
			cancel()
		}()
		c.runFollower(fctx, id)
	}()
}

func (c *Collector) setStatus(name, s string) {
	c.mu.Lock()
	c.status[name] = s
	c.mu.Unlock()
}

// runFollower reads one container's logs while it runs, resuming after
// the last line stored.
func (c *Collector) runFollower(ctx context.Context, id string) {
	ctr, err := c.docker.Inspect(ctx, id)
	if err != nil {
		if ctx.Err() == nil && err != docker.ErrNotFound {
			slog.WarnContext(ctx, "inspect container failed", "id", id[:min(12, len(id))], "err", err)
		}
		return
	}
	if c.excluded(ctr.Name, ctr.Labels) {
		c.setStatus(ctr.Name, "excluded")
		return
	}
	src, err := c.source(ctx, ctr.Name, ctr.Image)
	if err != nil {
		slog.ErrorContext(ctx, "store source failed", "container", ctr.Name, "err", err)
		return
	}
	c.setStatus(ctr.Name, "following")
	cursor := src.Cursor
	if cursor.IsZero() || time.Since(cursor) > 30*24*time.Hour {
		cursor = time.Now().Add(-c.cfg.Backfill)
	}
	slog.DebugContext(ctx, "following container", "container", ctr.Name, "since", cursor)

	f := &follower{c: c, ctx: ctx, sourceID: src.ID}
	stop := make(chan struct{})
	go f.flushStale(stop)
	defer close(stop)

	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		sctx, cancel := context.WithCancel(ctx)
		f.setCancel(cancel)
		err := c.docker.Logs(sctx, ctr, cursor, func(l docker.Line) {
			f.line(l)
			cursor = l.Time
		})
		cancel()
		f.flush()
		if ctx.Err() != nil {
			return
		}
		if f.takeFlood() {
			// Sit out the rest of the minute, then start from the end of
			// the log: what the container wrote meanwhile is skipped,
			// unread.
			wait := time.Until(time.Now().Truncate(time.Minute).Add(time.Minute))
			slog.WarnContext(ctx, "container is flooding its log; skipping ahead", "container", ctr.Name, "for", wait.Round(time.Second).String())
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			now := time.Now()
			c.offer(ctx, src.ID, now, parse.Entry{
				Level: parse.Warn, Kind: "limit",
				Msg: "skipped this container's output until now: over " + strconv.Itoa(c.floodLimit()) + " lines a minute",
			}, false, false)
			c.send(ctx, record{sourceID: src.ID, at: now, read: true}) // don't reread the flood after a restart
			cursor = time.Time{}
			backoff = time.Second
			continue
		}
		if err != nil {
			slog.WarnContext(ctx, "reading logs failed", "container", ctr.Name, "err", err)
		}
		// The stream ends when the container stops; keep going only if
		// it's still running (a log rotation can end it too).
		now, err := c.docker.Inspect(ctx, id)
		if err != nil || !now.Running {
			c.setStatus(ctr.Name, "stopped")
			return
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

// follower merges a plain line's continuations (stack frames) into it
// before handing it on.
type follower struct {
	c        *Collector
	ctx      context.Context
	sourceID int64

	mu      sync.Mutex
	pending *pendingLine
	cancel  context.CancelFunc // ends the current log stream
	flooded bool
}

func (f *follower) setCancel(cancel context.CancelFunc) {
	f.mu.Lock()
	f.cancel, f.flooded = cancel, false
	f.mu.Unlock()
}

// takeFlood says whether the stream was stopped for flooding, and clears it.
func (f *follower) takeFlood() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	fl := f.flooded
	f.flooded = false
	return fl
}

type pendingLine struct {
	line    docker.Line
	lines   int
	arrived time.Time
}

const (
	maxMerged   = 200 // continuation lines merged into one
	mergeWindow = 500 * time.Millisecond
)

func (f *follower) line(l docker.Line) {
	f.mu.Lock()
	if p := f.pending; p != nil && parse.Continuation(p.line.Text, l.Text) && p.lines < maxMerged &&
		len(p.line.Text) < f.c.cfg.MaxLine && l.Time.Sub(p.line.Time) < 2*time.Second &&
		!strings.HasPrefix(p.line.Text, "{") {
		p.line.Text += "\n" + l.Text
		p.lines++
		p.arrived = time.Now()
		f.mu.Unlock()
		return
	}
	prev := f.pending
	f.pending = &pendingLine{line: l, lines: 1, arrived: time.Now()}
	f.mu.Unlock()
	if prev != nil {
		f.emit(prev.line)
	}
}

func (f *follower) flush() {
	f.mu.Lock()
	p := f.pending
	f.pending = nil
	f.mu.Unlock()
	if p != nil {
		f.emit(p.line)
	}
}

// flushStale hands on a pending line once nothing has joined it for a
// moment, and ends quiet minutes so their summaries are written.
func (f *follower) flushStale(stop chan struct{}) {
	t := time.NewTicker(mergeWindow)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			f.mu.Lock()
			p := f.pending
			if p != nil && time.Since(p.arrived) >= mergeWindow {
				f.pending = nil
			} else {
				p = nil
			}
			f.mu.Unlock()
			if p != nil {
				f.emit(p.line)
			}
			f.c.endQuietMinute(f.ctx, f.sourceID)
		}
	}
}

func (f *follower) emit(l docker.Line) {
	if f.c.offer(f.ctx, f.sourceID, l.Time, parse.Line(l.Text, f.c.cfg.MaxLine), l.Stderr, true) {
		f.mu.Lock()
		if !f.flooded && f.cancel != nil {
			f.flooded = true
			f.cancel()
		}
		f.mu.Unlock()
	}
}

func (c *Collector) floodLimit() int {
	g := gate{limits: c.cfg.Limits}
	return g.floodLimit()
}

// offer passes an entry through the source's gate and on to the writer. It
// reports whether the source is flooding.
func (c *Collector) offer(ctx context.Context, sourceID int64, at time.Time, e parse.Entry, stderr, read bool) bool {
	c.mu.Lock()
	g := c.gates[sourceID]
	name := c.names[sourceID]
	c.mu.Unlock()
	if e.Msg == "" && e.Attrs == "" && e.Err == "" {
		// An empty line: nothing to keep, but it moves the cursor.
		c.send(ctx, record{sourceID: sourceID, at: at, read: read})
		return false
	}
	g.mu.Lock()
	v := g.admit(at, e)
	g.mu.Unlock()
	for _, n := range v.notes {
		c.send(ctx, record{sourceID: sourceID, line: lineOf(n.at, n.entry, false), at: n.at})
		if n.quota && c.cfg.OnQuota != nil {
			c.cfg.OnQuota(name, c.cfg.Limits.DailyQuota)
		}
	}
	r := record{sourceID: sourceID, at: at, read: read}
	if v.keep {
		r.line = lineOf(at, e, stderr)
	} else {
		r.dropped = 1
	}
	c.send(ctx, r)
	return v.flood
}

// endQuietMinute writes the summaries of a minute no line has closed.
func (c *Collector) endQuietMinute(ctx context.Context, sourceID int64) {
	c.mu.Lock()
	g := c.gates[sourceID]
	c.mu.Unlock()
	if g == nil {
		return
	}
	// The window follows line timestamps; a minute and a bit of wall time
	// without lines means it's over (also when catching up on old lines).
	g.mu.Lock()
	var notes []note
	if !g.window.IsZero() && time.Since(g.lastSeen) >= time.Minute+5*time.Second {
		notes = g.roll(g.window.Add(time.Minute))
	}
	g.mu.Unlock()
	for _, n := range notes {
		c.send(ctx, record{sourceID: sourceID, line: lineOf(n.at, n.entry, false), at: n.at})
	}
}

func (c *Collector) send(ctx context.Context, r record) {
	select {
	case c.in <- r:
	case <-ctx.Done():
	}
}

func lineOf(at time.Time, e parse.Entry, stderr bool) *store.Line {
	l := &store.Line{
		Time: at, Level: e.Level, Kind: e.Kind, Msg: e.Msg, Trace: e.Trace, User: e.User,
		Err: e.Err, Attrs: e.Attrs, Stderr: stderr,
	}
	if e.DurMS != 0 || e.Kind == "request" || e.Kind == "job" {
		d := e.DurMS
		l.DurMS = &d
	}
	return l
}

// write stores records in batches: every half second, or sooner when a
// batch fills up.
func (c *Collector) write(ctx context.Context) {
	const maxBatch = 500
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	var batch []record
	flush := func() {
		if len(batch) == 0 {
			return
		}
		c.store1(batch)
		batch = batch[:0]
	}
	for {
		select {
		case <-ctx.Done():
			// Drain what's queued so the cursors match what was stored.
			for {
				select {
				case r := <-c.in:
					batch = append(batch, r)
				default:
					flush()
					return
				}
			}
		case r := <-c.in:
			batch = append(batch, r)
			if len(batch) >= maxBatch {
				flush()
			}
		case <-t.C:
			flush()
		}
	}
}

func (c *Collector) store1(batch []record) {
	var lines []store.NewLine
	type key struct {
		src  int64
		hour int64
	}
	counts := map[key]*store.Count{}
	cursors := map[int64]*store.Cursor{}
	for _, r := range batch {
		k := key{r.sourceID, r.at.Unix() / 3600}
		cnt := counts[k]
		if cnt == nil {
			cnt = &store.Count{SourceID: r.sourceID, Hour: k.hour}
			counts[k] = cnt
		}
		cnt.Dropped += r.dropped
		if r.line != nil {
			lines = append(lines, store.NewLine{Line: *r.line, SourceID: r.sourceID})
			cnt.Lines++
			switch {
			case r.line.Level >= parse.Error:
				cnt.Errors++
			case r.line.Level >= parse.Warn:
				cnt.Warns++
			}
		}
		if r.read {
			cur := cursors[r.sourceID]
			if cur == nil {
				cur = &store.Cursor{SourceID: r.sourceID}
				cursors[r.sourceID] = cur
			}
			if r.at.After(cur.At) {
				cur.At = r.at
			}
			if r.line != nil && r.at.After(cur.LastLine) {
				cur.LastLine = r.at
			}
		}
	}
	var cs []store.Count
	for _, cnt := range counts {
		if cnt.Lines+cnt.Dropped > 0 {
			cs = append(cs, *cnt)
		}
	}
	var curs []store.Cursor
	for _, cur := range cursors {
		curs = append(curs, *cur)
	}
	ctx, run := logging.QuietJob(context.Background(), "store.write", "lines", len(lines))
	ids, err := c.store.Write(ctx, lines, cs, curs)
	run.End(err)
	if err != nil {
		return
	}
	out := make([]store.Line, len(lines))
	c.mu.Lock()
	for i, l := range lines {
		l.ID = ids[i]
		l.Source = c.names[l.SourceID]
		out[i] = l.Line
	}
	c.mu.Unlock()
	c.Hub.Publish(out)
}
