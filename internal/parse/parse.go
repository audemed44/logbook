// Package parse turns one log line into an Entry: JSON lines (the homelab
// apps' internal/logging, and most other JSON loggers), logfmt (Go's slog
// text handler and friends), and plain text with a level guessed from it.
package parse

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// Levels, the same numbers as Go's slog.
const (
	Debug = -4
	Info  = 0
	Warn  = 4
	Error = 8
)

// LevelName is the label for a level number.
func LevelName(l int) string {
	switch {
	case l >= Error:
		return "error"
	case l >= Warn:
		return "warn"
	case l >= Info:
		return "info"
	}
	return "debug"
}

// Entry is what Logbook keeps of a line.
type Entry struct {
	Level int
	Msg   string
	Kind  string // request, job, audit, suppressed, docker; "" for plain lines
	Trace string
	User  string
	Err   string
	DurMS int64
	// Attrs holds the rest of a structured line, as a JSON object ("" when
	// there's nothing).
	Attrs string
}

// known are the fields that become columns, under the names apps use.
var (
	levelKeys = []string{"level", "lvl", "severity", "log.level"}
	msgKeys   = []string{"msg", "message", "event"}
	traceKeys = []string{"trace", "trace_id", "request_id", "req_id"}
	errKeys   = []string{"err", "error", "exception"}
	dropKeys  = []string{"time", "ts", "timestamp", "@timestamp", "app"}
)

// Line parses text. stderr only matters for plain lines: it doesn't make a
// line an error (most apps log everything to stderr).
func Line(text string, maxLen int) Entry {
	text = strings.TrimRight(text, "\r\n")
	if strings.HasPrefix(text, "{") && strings.HasSuffix(text, "}") {
		if e, ok := jsonLine(text, maxLen); ok {
			return e
		}
	}
	if strings.Contains(text, "msg=") || strings.Contains(text, "level=") {
		if e, ok := logfmtLine(text, maxLen); ok {
			return e
		}
	}
	return Entry{Level: guessLevel(text), Msg: cut(text, maxLen)}
}

func jsonLine(text string, maxLen int) (Entry, bool) {
	var m map[string]any
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		return Entry{}, false
	}
	e := Entry{Level: Info}
	if v, ok := take(m, levelKeys); ok {
		e.Level = levelOf(v)
	}
	if v, ok := take(m, msgKeys); ok {
		e.Msg = str(v)
	}
	if v, ok := take(m, traceKeys); ok {
		e.Trace = str(v)
	}
	if v, ok := take(m, errKeys); ok {
		e.Err = str(v)
	}
	if v, ok := take(m, []string{"kind"}); ok {
		e.Kind = str(v)
	}
	if v, ok := take(m, []string{"user"}); ok {
		e.User = str(v)
	}
	if v, ok := m["dur_ms"].(float64); ok {
		e.DurMS = int64(v)
		delete(m, "dur_ms")
	}
	for _, k := range dropKeys {
		delete(m, k)
	}
	if e.Msg == "" && e.Err != "" {
		e.Msg = e.Err
	}
	finish(&e, m, maxLen)
	return e, true
}

// logfmtPair matches key=value, key="quoted value" or key='single'.
var logfmtPair = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_.\-]*)=("(?:[^"\\]|\\.)*"|'[^']*'|\S*)`)

func logfmtLine(text string, maxLen int) (Entry, bool) {
	pairs := logfmtPair.FindAllStringSubmatch(text, -1)
	if len(pairs) < 2 {
		return Entry{}, false
	}
	m := make(map[string]any, len(pairs))
	for _, p := range pairs {
		v := p[2]
		if strings.HasPrefix(v, `"`) {
			if s, err := strconv.Unquote(v); err == nil {
				v = s
			}
		} else if strings.HasPrefix(v, "'") {
			v = strings.Trim(v, "'")
		}
		m[p[1]] = v
	}
	_, hasLevel := m["level"]
	_, hasMsg := m["msg"]
	if !hasLevel && !hasMsg {
		return Entry{}, false
	}
	e := Entry{Level: Info}
	if v, ok := take(m, levelKeys); ok {
		e.Level = levelOf(v)
	}
	if v, ok := take(m, msgKeys); ok {
		e.Msg = str(v)
	}
	if v, ok := take(m, traceKeys); ok {
		e.Trace = str(v)
	}
	if v, ok := take(m, errKeys); ok {
		e.Err = str(v)
	}
	if v, ok := take(m, []string{"kind"}); ok {
		e.Kind = str(v)
	}
	if v, ok := take(m, []string{"user"}); ok {
		e.User = str(v)
	}
	if v, ok := m["dur_ms"].(string); ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			e.DurMS = n
			delete(m, "dur_ms")
		}
	}
	for _, k := range dropKeys {
		delete(m, k)
	}
	if e.Msg == "" {
		e.Msg = text
	}
	finish(&e, m, maxLen)
	return e, true
}

// finish cuts the columns to size and stores what's left as JSON, dropping
// whole fields (largest first) when the rest would be too long.
func finish(e *Entry, rest map[string]any, maxLen int) {
	e.Msg = cut(e.Msg, maxLen)
	e.Err = cut(e.Err, maxLen)
	e.Trace = cut(e.Trace, 64)
	e.User = cut(e.User, 128)
	e.Kind = cut(e.Kind, 32)
	if len(rest) == 0 {
		return
	}
	for k, v := range rest {
		if s, ok := v.(string); ok && len(s) > maxLen {
			rest[k] = cut(s, maxLen)
		}
	}
	b, err := json.Marshal(rest)
	for err == nil && len(b) > maxLen && len(rest) > 0 {
		biggest, size := "", -1
		for k, v := range rest {
			vb, _ := json.Marshal(v)
			if len(vb) > size {
				biggest, size = k, len(vb)
			}
		}
		delete(rest, biggest)
		rest["logbook_dropped"] = "fields over the size limit"
		b, err = json.Marshal(rest)
		if len(rest) == 1 {
			break
		}
	}
	if err == nil && len(rest) > 0 {
		e.Attrs = string(b)
	}
}

func take(m map[string]any, keys []string) (any, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			delete(m, k)
			return v, true
		}
	}
	return nil, false
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func levelOf(v any) int {
	if f, ok := v.(float64); ok {
		// Numeric levels: slog's own (-4..8) or pino/bunyan's (10..60).
		switch {
		case f >= 50:
			return Error
		case f >= 40:
			return Warn
		case f >= 30:
			return Info
		case f >= 10:
			return Debug
		}
		return int(f)
	}
	return named(str(v), Info)
}

func named(s string, fallback int) int {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case s == "":
		return fallback
	case strings.HasPrefix(s, "debug"), strings.HasPrefix(s, "trace"), s == "dbg", s == "verbose":
		return Debug
	case strings.HasPrefix(s, "info"), s == "notice", s == "inf":
		return Info
	case strings.HasPrefix(s, "warn"), s == "wrn":
		return Warn
	case strings.HasPrefix(s, "err"), strings.HasPrefix(s, "fatal"), strings.HasPrefix(s, "crit"),
		strings.HasPrefix(s, "panic"), s == "alert", s == "emerg", s == "eror":
		return Error
	}
	return fallback
}

// plainLevel finds a level word near the start of a plain line:
// "ERROR: …", "[warn] …", "2026/10/13 10:00:00 [error] …", "W1013 …".
var plainLevel = regexp.MustCompile(`(?i)(?:^|[\s\[(|:])(debug|dbg|trace|info|inf|notice|warn|warning|wrn|error|err|eror|fatal|critical|crit|panic)(?:[\]):|\s]|$)`)

func guessLevel(text string) int {
	head := text
	if len(head) > 80 {
		head = head[:80]
	}
	if m := plainLevel.FindStringSubmatch(head); m != nil {
		return named(m[1], Info)
	}
	switch {
	case strings.HasPrefix(text, "panic: "), strings.HasPrefix(text, "Traceback ("),
		strings.HasPrefix(text, "goroutine "):
		return Error
	}
	return Info
}

// Continuation says whether a plain line belongs to the one before it
// (prev, as merged so far): an indented stack frame, "Caused by:", and the
// exception line that ends a Python traceback.
func Continuation(prev, text string) bool {
	if text == "" {
		return false
	}
	if text[0] == ' ' || text[0] == '\t' {
		return true
	}
	if strings.HasPrefix(text, "Caused by:") || strings.HasPrefix(text, "...") {
		return true
	}
	// Only one exception line ends a traceback.
	last := prev[strings.LastIndexByte(prev, '\n')+1:]
	return strings.HasPrefix(prev, "Traceback (") && !exceptionLine.MatchString(last) && exceptionLine.MatchString(text)
}

// exceptionLine is "ValueError: bad input", "requests.exceptions.ConnectionError: …".
var exceptionLine = regexp.MustCompile(`^[A-Za-z_][\w.]*(Error|Exception|Exit|Interrupt|Warning)\b`)

func cut(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	for n > 0 && n < len(s) && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n] + "… [cut]"
}
