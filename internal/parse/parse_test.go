package parse

import (
	"strings"
	"testing"
)

func TestJSONFromInternalLogging(t *testing.T) {
	e := Line(`{"time":"2026-10-13T10:47:01Z","level":"WARN","msg":"DELETE /api/items/{id}","app":"skeleton","kind":"request","method":"DELETE","path":"/api/items/99","status":404,"dur_ms":3,"trace":"628b1711e050faed","user":"aakash"}`, 8192)
	if e.Level != Warn || e.Msg != "DELETE /api/items/{id}" || e.Kind != "request" || e.Trace != "628b1711e050faed" ||
		e.User != "aakash" || e.DurMS != 3 {
		t.Fatalf("%+v", e)
	}
	if e.Attrs != `{"method":"DELETE","path":"/api/items/99","status":404}` {
		t.Fatalf("attrs: %s", e.Attrs)
	}
}

func TestJSONOtherLoggers(t *testing.T) {
	e := Line(`{"level":50,"time":1,"msg":"boom","error":{"code":1}}`, 8192)
	if e.Level != Error || e.Err != `{"code":1}` {
		t.Fatalf("pino: %+v", e)
	}
	e = Line(`{"severity":"warning","message":"slow"}`, 8192)
	if e.Level != Warn || e.Msg != "slow" || e.Attrs != "" {
		t.Fatalf("severity: %+v", e)
	}
	e = Line(`{"not closed"`, 8192)
	if e.Msg != `{"not closed"` {
		t.Fatalf("broken JSON: %+v", e)
	}
}

func TestLogfmt(t *testing.T) {
	e := Line(`time=2026-10-13T10:00:00Z level=ERROR msg="sync failed" err="dial tcp: timeout" n=3`, 8192)
	if e.Level != Error || e.Msg != "sync failed" || e.Err != "dial tcp: timeout" || e.Attrs != `{"n":"3"}` {
		t.Fatalf("%+v", e)
	}
}

func TestPlain(t *testing.T) {
	for text, want := range map[string]int{
		"2026/10/13 10:00:00 [error] 12#12: upstream timed out": Error,
		"WARNING: something odd":                                Warn,
		"[INFO] started":                                        Info,
		"Traceback (most recent call last):":                    Error,
		"just a line about an errand":                           Info,
		"DEBUG fetching":                                        Debug,
	} {
		if got := Line(text, 8192).Level; got != want {
			t.Errorf("%q: level %d, want %d", text, got, want)
		}
	}
}

func TestLimits(t *testing.T) {
	long := strings.Repeat("é", 3000) // 6000 bytes
	e := Line(long, 1000)
	if len(e.Msg) > 1000+len("… [cut]") || !strings.HasSuffix(e.Msg, "… [cut]") {
		t.Fatalf("msg %d bytes", len(e.Msg))
	}
	e = Line(`{"msg":"x","a":"`+strings.Repeat("a", 900)+`","b":"`+strings.Repeat("b", 900)+`"}`, 1000)
	if len(e.Attrs) > 1000 || !strings.Contains(e.Attrs, "logbook_dropped") {
		t.Fatalf("attrs %d bytes: %.80s", len(e.Attrs), e.Attrs)
	}
}

func TestContinuation(t *testing.T) {
	if !Continuation("x", "\tat foo.bar(Foo.java:1)") || !Continuation("x", "  File \"x.py\"") || Continuation("x", "next line") {
		t.Fatal("continuation")
	}
	tb := "Traceback (most recent call last):\n  File \"app.py\", line 3"
	if !Continuation(tb, "ValueError: bad input") || Continuation("plain line", "ValueError: bad input") ||
		Continuation(tb+"\nValueError: bad input", "KeyError: next one") {
		t.Fatal("python exception line")
	}
}
