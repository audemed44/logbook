package docker

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
	"time"
)

func frame(stream byte, s string) []byte {
	b := make([]byte, 8, 8+len(s))
	b[0] = stream
	binary.BigEndian.PutUint32(b[4:], uint32(len(s)))
	return append(b, s...)
}

func TestReadFrames(t *testing.T) {
	var in bytes.Buffer
	in.Write(frame(1, "2026-10-13T10:00:00.000000001Z hello\n2026-10-13T10:00:01Z sec"))
	in.Write(frame(2, "2026-10-13T10:00:02Z oops\n"))
	in.Write(frame(1, "ond\n"))
	in.Write(frame(1, "2026-10-13T10:00:03Z\n"))
	var got []Line
	if err := readFrames(&in, func(l Line) { got = append(got, l) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("lines: %+v", got)
	}
	if got[0].Text != "hello" || got[0].Time.Nanosecond() != 1 || got[0].Stderr {
		t.Fatalf("first: %+v", got[0])
	}
	if got[1].Text != "oops" || !got[1].Stderr {
		t.Fatalf("stderr line: %+v", got[1])
	}
	if got[2].Text != "second" || !got[2].Time.Equal(time.Date(2026, 10, 13, 10, 0, 1, 0, time.UTC)) {
		t.Fatalf("split line: %+v", got[2])
	}
	if got[3].Text != "" {
		t.Fatalf("empty line: %+v", got[3])
	}
}

func TestReadFramesCapsLongLines(t *testing.T) {
	var in bytes.Buffer
	in.Write(frame(1, "2026-10-13T10:00:00Z "+strings.Repeat("x", maxLine*2)+"\n"))
	var got []Line
	if err := readFrames(&in, func(l Line) { got = append(got, l) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Text) > maxLine {
		t.Fatalf("got %d lines, first %d bytes", len(got), len(got[0].Text))
	}
}

func TestReadLinesTTY(t *testing.T) {
	in := strings.NewReader("2026-10-13T10:00:00Z one\r\n2026-10-13T10:00:01Z two\n")
	var got []string
	if err := readLines(in, false, func(l Line) { got = append(got, l.Text) }); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "one,two" {
		t.Fatalf("got %q", got)
	}
}
