package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Line is one stored log line.
type Line struct {
	ID     int64     `json:"id"`
	Time   time.Time `json:"time"`
	Source string    `json:"source"`
	Level  int       `json:"level"`
	Kind   string    `json:"kind,omitempty"`
	Msg    string    `json:"msg"`
	Trace  string    `json:"trace,omitempty"`
	User   string    `json:"user,omitempty"`
	Err    string    `json:"err,omitempty"`
	DurMS  *int64    `json:"dur_ms,omitempty"`
	Attrs  string    `json:"attrs,omitempty"` // a JSON object
	Stderr bool      `json:"stderr,omitempty"`
}

// NewLine is a line on its way in, with the source already resolved.
type NewLine struct {
	Line
	SourceID int64
}

// Source is a container Logbook has seen.
type Source struct {
	ID        int64
	Name      string
	Image     string
	FirstSeen time.Time
	LastLine  time.Time
	Cursor    time.Time
}

// EnsureSource returns the source called name, adding it the first time.
func (s *Store) EnsureSource(ctx context.Context, name, image string) (Source, error) {
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sources (name, image, first_seen) VALUES (?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET image = CASE WHEN excluded.image <> '' THEN excluded.image ELSE image END`,
		name, image, now)
	if err != nil {
		return Source{}, err
	}
	var src Source
	var first, last, cursor int64
	err = s.db.QueryRowContext(ctx, `SELECT id, name, image, first_seen, last_line, cursor_ns FROM sources WHERE name = ?`, name).
		Scan(&src.ID, &src.Name, &src.Image, &first, &last, &cursor)
	src.FirstSeen, src.LastLine = fromMS(first), fromMS(last)
	if cursor > 0 {
		src.Cursor = time.Unix(0, cursor)
	}
	return src, err
}

// Count is what a batch adds to one source's hourly counts.
type Count struct {
	SourceID int64
	Hour     int64 // unix hour
	Lines    int
	Warns    int
	Errors   int
	Dropped  int
}

// Cursor is how far a source has been read.
type Cursor struct {
	SourceID int64
	At       time.Time // Docker's timestamp of the last line read
	LastLine time.Time
}

// Write stores a batch of lines, counts and cursors in one transaction.
func (s *Store) Write(ctx context.Context, lines []NewLine, counts []Count, cursors []Cursor) ([]int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	ins, err := tx.PrepareContext(ctx, `INSERT INTO lines (ts, source, level, kind, msg, trace, user, err, dur_ms, attrs, stderr)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return nil, err
	}
	defer ins.Close()
	ids := make([]int64, len(lines))
	for i, l := range lines {
		var dur any
		if l.DurMS != nil {
			dur = *l.DurMS
		}
		res, err := ins.ExecContext(ctx, l.Time.UnixMilli(), l.SourceID, l.Level, l.Kind, l.Msg, l.Trace, l.User, l.Err, dur, l.Attrs, l.Stderr)
		if err != nil {
			return nil, err
		}
		ids[i], _ = res.LastInsertId()
	}
	for _, c := range counts {
		_, err := tx.ExecContext(ctx, `INSERT INTO counts (hour, source, lines, warns, errors, dropped) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(hour, source) DO UPDATE SET lines = lines + excluded.lines, warns = warns + excluded.warns,
			errors = errors + excluded.errors, dropped = dropped + excluded.dropped`,
			c.Hour, c.SourceID, c.Lines, c.Warns, c.Errors, c.Dropped)
		if err != nil {
			return nil, err
		}
	}
	for _, c := range cursors {
		_, err := tx.ExecContext(ctx, `UPDATE sources SET cursor_ns = MAX(cursor_ns, ?), last_line = MAX(last_line, ?) WHERE id = ?`,
			c.At.UnixNano(), ms(c.LastLine), c.SourceID)
		if err != nil {
			return nil, err
		}
	}
	return ids, tx.Commit()
}

// LinesSince is how many lines each source has stored since t (for the
// daily quota after a restart).
func (s *Store) LinesSince(ctx context.Context, t time.Time) (map[int64]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT source, SUM(lines) FROM counts WHERE hour >= ? GROUP BY source`, t.Unix()/3600)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// Filter narrows a query. Zero values match everything.
type Filter struct {
	Sources  []string
	MinLevel int // only when HasLevel
	HasLevel bool
	Kind     string
	Q        string // words, each matched as a prefix, all required
	Trace    string
	Since    time.Time
	Until    time.Time
	// Before pages back: lines older than this cursor ("<ms>:<id>").
	Before string
	// After is for following: lines with an id above it, oldest first.
	After int64
	Limit int
}

const lineCols = `l.id, l.ts, s.name, l.level, l.kind, l.msg, l.trace, l.user, l.err, l.dur_ms, l.attrs, l.stderr`

func scanLines(rows *sql.Rows) ([]Line, error) {
	defer rows.Close()
	out := []Line{}
	for rows.Next() {
		var l Line
		var ts int64
		var dur sql.NullInt64
		if err := rows.Scan(&l.ID, &ts, &l.Source, &l.Level, &l.Kind, &l.Msg, &l.Trace, &l.User, &l.Err, &dur, &l.Attrs, &l.Stderr); err != nil {
			return nil, err
		}
		l.Time = time.UnixMilli(ts)
		if dur.Valid {
			l.DurMS = &dur.Int64
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ErrBadQuery is a filter that can't be run (a malformed cursor).
var ErrBadQuery = errors.New("bad query")

// Lines finds lines, newest first (or oldest first with After).
func (s *Store) Lines(ctx context.Context, f Filter) ([]Line, error) {
	var where []string
	var args []any
	if len(f.Sources) > 0 {
		where = append(where, `s.name IN (?`+strings.Repeat(`, ?`, len(f.Sources)-1)+`)`)
		for _, n := range f.Sources {
			args = append(args, n)
		}
	}
	if f.HasLevel {
		where = append(where, `l.level >= ?`)
		args = append(args, f.MinLevel)
	}
	if f.Kind != "" {
		where = append(where, `l.kind = ?`)
		args = append(args, f.Kind)
	}
	if f.Trace != "" {
		where = append(where, `l.trace = ?`)
		args = append(args, f.Trace)
	}
	if !f.Since.IsZero() {
		where = append(where, `l.ts >= ?`)
		args = append(args, f.Since.UnixMilli())
	}
	if !f.Until.IsZero() {
		where = append(where, `l.ts < ?`)
		args = append(args, f.Until.UnixMilli())
	}
	if q := ftsQuery(f.Q); q != "" {
		where = append(where, `l.id IN (SELECT rowid FROM lines_fts WHERE lines_fts MATCH ?)`)
		args = append(args, q)
	}
	order := `l.ts DESC, l.id DESC`
	if f.After > 0 {
		where = append(where, `l.id > ?`)
		args = append(args, f.After)
		order = `l.id ASC`
	} else if f.Before != "" {
		tsStr, idStr, ok := strings.Cut(f.Before, ":")
		ts, err1 := strconv.ParseInt(tsStr, 10, 64)
		id, err2 := strconv.ParseInt(idStr, 10, 64)
		if !ok || err1 != nil || err2 != nil {
			return nil, ErrBadQuery
		}
		where = append(where, `(l.ts < ? OR (l.ts = ? AND l.id < ?))`)
		args = append(args, ts, ts, id)
	}
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	q := `SELECT ` + lineCols + ` FROM lines l JOIN sources s ON s.id = l.source`
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	q += ` ORDER BY ` + order + ` LIMIT ` + strconv.Itoa(limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return scanLines(rows)
}

// CursorOf is the Before cursor that continues after l.
func CursorOf(l Line) string {
	return strconv.FormatInt(l.Time.UnixMilli(), 10) + ":" + strconv.FormatInt(l.ID, 10)
}

// ftsQuery makes typed words safe for FTS5: each one quoted and matched as
// a prefix, all of them required.
func ftsQuery(q string) string {
	var terms []string
	for _, w := range strings.FieldsFunc(q, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		terms = append(terms, `"`+w+`"*`)
		if len(terms) == 8 {
			break
		}
	}
	return strings.Join(terms, " AND ")
}

// Line reads one line.
func (s *Store) Line(ctx context.Context, id int64) (Line, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+lineCols+` FROM lines l JOIN sources s ON s.id = l.source WHERE l.id = ?`, id)
	if err != nil {
		return Line{}, err
	}
	ls, err := scanLines(rows)
	if err != nil {
		return Line{}, err
	}
	if len(ls) == 0 {
		return Line{}, ErrNotFound
	}
	return ls[0], nil
}

// Around is the source's lines just before and after a line, oldest first.
func (s *Store) Around(ctx context.Context, id int64, n int) ([]Line, error) {
	l, err := s.Line(ctx, id)
	if err != nil {
		return nil, err
	}
	q := `SELECT * FROM (SELECT ` + lineCols + ` FROM lines l JOIN sources s ON s.id = l.source
		WHERE s.name = ? AND (l.ts < ? OR (l.ts = ? AND l.id < ?)) ORDER BY l.ts DESC, l.id DESC LIMIT ?)
		UNION ALL
		SELECT * FROM (SELECT ` + lineCols + ` FROM lines l JOIN sources s ON s.id = l.source
		WHERE s.name = ? AND (l.ts > ? OR (l.ts = ? AND l.id >= ?)) ORDER BY l.ts ASC, l.id ASC LIMIT ?)`
	ts := l.Time.UnixMilli()
	rows, err := s.db.QueryContext(ctx, `SELECT * FROM (`+q+`) ORDER BY 2, 1`,
		l.Source, ts, ts, l.ID, n, l.Source, ts, ts, l.ID, n+1)
	if err != nil {
		return nil, err
	}
	return scanLines(rows)
}

// Problem is a message that keeps coming up at warn or error.
type Problem struct {
	Source string    `json:"source"`
	Level  int       `json:"level"`
	Msg    string    `json:"msg"`
	Count  int       `json:"count"`
	Last   time.Time `json:"last"`
	LastID int64     `json:"last_id"`
}

// Problems groups warnings and errors since t by source and message, most
// frequent first.
func (s *Store) Problems(ctx context.Context, since time.Time, minLevel, limit int) ([]Problem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.name, MAX(l.level), l.msg, COUNT(*), MAX(l.ts), MAX(l.id)
		FROM lines l JOIN sources s ON s.id = l.source
		WHERE l.level >= ? AND l.level >= 4 AND l.ts >= ? AND l.kind <> 'suppressed'
		GROUP BY l.source, l.msg ORDER BY MAX(l.level) DESC, COUNT(*) DESC LIMIT ?`, minLevel, since.UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Problem{}
	for rows.Next() {
		var p Problem
		var last int64
		if err := rows.Scan(&p.Source, &p.Level, &p.Msg, &p.Count, &last, &p.LastID); err != nil {
			return nil, err
		}
		p.Last = time.UnixMilli(last)
		out = append(out, p)
	}
	return out, rows.Err()
}

// ErrorsSince counts error lines per source since t (for burst alerts).
func (s *Store) ErrorsSince(ctx context.Context, since time.Time) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.name, COUNT(*) FROM lines l JOIN sources s ON s.id = l.source
		WHERE l.level >= 8 AND l.ts >= ? GROUP BY l.source`, since.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var name string
		var n int
		if err := rows.Scan(&name, &n); err != nil {
			return nil, err
		}
		out[name] = n
	}
	return out, rows.Err()
}

// Bucket is one hour of counts.
type Bucket struct {
	Hour    time.Time `json:"hour"`
	Lines   int       `json:"lines"`
	Warns   int       `json:"warns"`
	Errors  int       `json:"errors"`
	Dropped int       `json:"dropped"`
}

// Hourly is counts per hour from since to now, every hour present; source
// "" means all of them.
func (s *Store) Hourly(ctx context.Context, since time.Time, source string) ([]Bucket, error) {
	first := since.Unix() / 3600
	q := `SELECT c.hour, SUM(c.lines), SUM(c.warns), SUM(c.errors), SUM(c.dropped) FROM counts c`
	args := []any{first}
	if source != "" {
		q += ` JOIN sources s ON s.id = c.source WHERE c.hour >= ? AND s.name = ?`
		args = append(args, source)
	} else {
		q += ` WHERE c.hour >= ?`
	}
	rows, err := s.db.QueryContext(ctx, q+` GROUP BY c.hour`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byHour := map[int64]Bucket{}
	for rows.Next() {
		var h int64
		var b Bucket
		if err := rows.Scan(&h, &b.Lines, &b.Warns, &b.Errors, &b.Dropped); err != nil {
			return nil, err
		}
		byHour[h] = b
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	last := time.Now().Unix() / 3600
	out := make([]Bucket, 0, last-first+1)
	for h := first; h <= last; h++ {
		b := byHour[h]
		b.Hour = time.Unix(h*3600, 0)
		out = append(out, b)
	}
	return out, nil
}

// SourceStats is a source with its recent numbers.
type SourceStats struct {
	Name      string    `json:"name"`
	Image     string    `json:"image,omitempty"`
	FirstSeen time.Time `json:"first_seen"`
	LastLine  time.Time `json:"last_line,omitzero"`
	Lines24h  int       `json:"lines_24h"`
	Warns24h  int       `json:"warns_24h"`
	Errors24h int       `json:"errors_24h"`
	Dropped   int       `json:"dropped_24h"`
	Today     int       `json:"today"` // lines since midnight, against the quota
	Stored    int       `json:"stored"`
}

// Sources lists every source with its counts since dayStart (today) and
// for the last 24 hours.
func (s *Store) Sources(ctx context.Context, dayStart time.Time) ([]SourceStats, error) {
	h24 := time.Now().Add(-24*time.Hour).Unix() / 3600
	rows, err := s.db.QueryContext(ctx, `SELECT s.name, s.image, s.first_seen, s.last_line,
		COALESCE(SUM(CASE WHEN c.hour >= ? THEN c.lines END), 0),
		COALESCE(SUM(CASE WHEN c.hour >= ? THEN c.warns END), 0),
		COALESCE(SUM(CASE WHEN c.hour >= ? THEN c.errors END), 0),
		COALESCE(SUM(CASE WHEN c.hour >= ? THEN c.dropped END), 0),
		COALESCE(SUM(CASE WHEN c.hour >= ? THEN c.lines END), 0),
		(SELECT COUNT(*) FROM lines l WHERE l.source = s.id)
		FROM sources s LEFT JOIN counts c ON c.source = s.id AND c.hour >= ?
		GROUP BY s.id ORDER BY s.name`,
		h24, h24, h24, h24, dayStart.Unix()/3600, min(h24, dayStart.Unix()/3600))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SourceStats{}
	for rows.Next() {
		var st SourceStats
		var first, last int64
		if err := rows.Scan(&st.Name, &st.Image, &first, &last, &st.Lines24h, &st.Warns24h, &st.Errors24h, &st.Dropped, &st.Today, &st.Stored); err != nil {
			return nil, err
		}
		st.FirstSeen, st.LastLine = fromMS(first), fromMS(last)
		out = append(out, st)
	}
	return out, rows.Err()
}

// Usage is how much room the logs take.
type Usage struct {
	Bytes  int64     `json:"bytes"` // pages in use, not free ones
	Lines  int64     `json:"lines"`
	Oldest time.Time `json:"oldest,omitzero"`
}

func (s *Store) Usage(ctx context.Context) (Usage, error) {
	var u Usage
	used, err := s.usedBytes(ctx)
	if err != nil {
		return u, err
	}
	u.Bytes = used
	var oldest sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), MIN(ts) FROM lines`).Scan(&u.Lines, &oldest); err != nil {
		return u, err
	}
	if oldest.Valid {
		u.Oldest = time.UnixMilli(oldest.Int64)
	}
	return u, nil
}

func (s *Store) usedBytes(ctx context.Context) (int64, error) {
	var pages, free, size int64
	if err := s.db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages); err != nil {
		return 0, err
	}
	if err := s.db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&free); err != nil {
		return 0, err
	}
	if err := s.db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&size); err != nil {
		return 0, err
	}
	return (pages - free) * size, nil
}

// Pruned says what Prune removed.
type Pruned struct {
	Expired int64 // older than the retention
	ForSize int64 // removed, oldest first, to get under the size cap
}

// pruneBatch is how many lines one delete takes, so the writer isn't held
// up for long.
const pruneBatch = 5000

// Prune deletes lines older than retain, then the oldest lines until the
// database is under maxBytes (down to 90% of it, so it doesn't run every
// minute), and gives the space back to the disk.
func (s *Store) Prune(ctx context.Context, retain time.Duration, maxBytes int64) (Pruned, error) {
	var p Pruned
	cutoff := time.Now().Add(-retain).UnixMilli()
	for {
		res, err := s.db.ExecContext(ctx, `DELETE FROM lines WHERE id IN (SELECT id FROM lines WHERE ts < ? LIMIT ?)`, cutoff, pruneBatch)
		if err != nil {
			return p, err
		}
		n, _ := res.RowsAffected()
		p.Expired += n
		if n < pruneBatch {
			break
		}
	}
	// Counts are tiny; keep them for the charts a while longer.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM counts WHERE hour < ?`, time.Now().Add(-retain-30*24*time.Hour).Unix()/3600); err != nil {
		return p, err
	}
	used, err := s.usedBytes(ctx)
	if err != nil {
		return p, err
	}
	if maxBytes > 0 && used > maxBytes {
		target := maxBytes * 9 / 10
		for used > target {
			res, err := s.db.ExecContext(ctx, `DELETE FROM lines WHERE id IN (SELECT id FROM lines ORDER BY ts, id LIMIT ?)`, pruneBatch)
			if err != nil {
				return p, err
			}
			n, _ := res.RowsAffected()
			p.ForSize += n
			if n == 0 {
				break
			}
			if used, err = s.usedBytes(ctx); err != nil {
				return p, err
			}
		}
	}
	if p.Expired+p.ForSize > 0 {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO lines_fts (lines_fts) VALUES ('optimize')`); err != nil {
			return p, fmt.Errorf("optimize the search index: %w", err)
		}
		if _, err := s.db.ExecContext(ctx, `PRAGMA incremental_vacuum`); err != nil {
			return p, err
		}
		if _, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
			return p, err
		}
	}
	return p, nil
}

// FileBytes is the size of the database file (pages in use and free).
func (s *Store) FileBytes(ctx context.Context) (int64, error) {
	var pages, size int64
	if err := s.db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages); err != nil {
		return 0, err
	}
	if err := s.db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&size); err != nil {
		return 0, err
	}
	return pages * size, nil
}

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMS(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v)
}
