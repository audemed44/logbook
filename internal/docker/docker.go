// Package docker is a minimal, read-only client for the Docker Engine API
// over its unix socket: list containers, follow their logs, and watch
// their start/stop events.
package docker

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var ErrNotFound = errors.New("no such container")

type Client struct {
	http   *http.Client
	stream *http.Client // logs and events run until the caller's context ends
}

func New(socket string) *Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
		MaxIdleConns:    4,
		IdleConnTimeout: 30 * time.Second,
	}
	return &Client{
		http:   &http.Client{Transport: transport, Timeout: 30 * time.Second},
		stream: &http.Client{Transport: transport},
	}
}

func (c *Client) get(ctx context.Context, client *http.Client, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker"+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, ErrNotFound
	}
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		var e struct{ Message string }
		if json.Unmarshal(msg, &e) == nil && e.Message != "" {
			return nil, fmt.Errorf("docker: %s", e.Message)
		}
		return nil, fmt.Errorf("docker: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return resp, nil
}

// Container is a container as Logbook needs it.
type Container struct {
	ID      string
	Name    string
	Image   string
	Running bool
	TTY     bool // logs come raw, not in stdout/stderr frames
	Labels  map[string]string
}

// List lists every container, running or not.
func (c *Client) List(ctx context.Context) ([]Container, error) {
	resp, err := c.get(ctx, c.http, "/containers/json?all=1")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var raw []struct {
		Id     string
		Names  []string
		Image  string
		State  string
		Labels map[string]string
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]Container, 0, len(raw))
	for _, r := range raw {
		name := r.Id
		if len(r.Names) > 0 {
			name = strings.TrimPrefix(r.Names[0], "/")
		}
		out = append(out, Container{ID: r.Id, Name: name, Image: r.Image, Running: r.State == "running", Labels: r.Labels})
	}
	return out, nil
}

// Inspect reads one container, including whether it has a TTY.
func (c *Client) Inspect(ctx context.Context, id string) (Container, error) {
	resp, err := c.get(ctx, c.http, "/containers/"+url.PathEscape(id)+"/json")
	if err != nil {
		return Container{}, err
	}
	defer resp.Body.Close()
	var raw struct {
		Id     string
		Name   string
		Config struct {
			Image  string
			Tty    bool
			Labels map[string]string
		}
		State struct{ Running bool }
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return Container{}, err
	}
	return Container{
		ID: raw.Id, Name: strings.TrimPrefix(raw.Name, "/"), Image: raw.Config.Image,
		Running: raw.State.Running, TTY: raw.Config.Tty, Labels: raw.Config.Labels,
	}, nil
}

// Line is one log line as Docker stored it.
type Line struct {
	Time   time.Time
	Stderr bool
	Text   string
}

// maxLine bounds one line read from Docker; anything longer is cut (Docker
// itself splits lines at 16 KB).
const maxLine = 64 << 10

// Logs follows a container's logs from since (exclusive) until ctx ends or
// the container stops, calling fn for each line. A zero since starts at the
// end of the log: only new lines, without Docker reading the file through.
func (c *Client) Logs(ctx context.Context, ctr Container, since time.Time, fn func(Line)) error {
	q := url.Values{"follow": {"1"}, "stdout": {"1"}, "stderr": {"1"}, "timestamps": {"1"}}
	if since.IsZero() {
		q.Set("tail", "0")
	} else {
		// since is inclusive, to the nanosecond: start just after the last line.
		s := since.Add(time.Nanosecond)
		q.Set("since", strconv.FormatInt(s.Unix(), 10)+"."+fmt.Sprintf("%09d", s.Nanosecond()))
	}
	resp, err := c.get(ctx, c.stream, "/containers/"+url.PathEscape(ctr.ID)+"/logs?"+q.Encode())
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if ctr.TTY {
		return readLines(resp.Body, false, fn)
	}
	return readFrames(resp.Body, fn)
}

// readFrames splits Docker's multiplexed stream: an 8-byte header (stream
// type, then the payload size) before each chunk.
func readFrames(r io.Reader, fn func(Line)) error {
	br := bufio.NewReaderSize(r, 32<<10)
	hdr := make([]byte, 8)
	var partial [3]strings.Builder // per stream, a line split across frames
	for {
		if _, err := io.ReadFull(br, hdr); err != nil {
			return eof(err)
		}
		stream := hdr[0]
		size := binary.BigEndian.Uint32(hdr[4:])
		payload := make([]byte, size)
		if _, err := io.ReadFull(br, payload); err != nil {
			return eof(err)
		}
		if stream > 2 {
			continue
		}
		buf := &partial[stream]
		for len(payload) > 0 {
			i := indexByte(payload, '\n')
			if i < 0 {
				if buf.Len() < maxLine {
					buf.Write(payload[:min(len(payload), maxLine-buf.Len())])
				}
				break
			}
			if buf.Len() < maxLine {
				buf.Write(payload[:min(i, maxLine-buf.Len())])
			}
			emit(buf.String(), stream == 2, fn)
			buf.Reset()
			payload = payload[i+1:]
		}
	}
}

func readLines(r io.Reader, stderr bool, fn func(Line)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 32<<10), maxLine)
	for sc.Scan() {
		emit(strings.TrimSuffix(sc.Text(), "\r"), stderr, fn)
	}
	if errors.Is(sc.Err(), bufio.ErrTooLong) {
		return nil // the next follow picks up after the last line
	}
	return eof(sc.Err())
}

// emit splits off the RFC 3339 timestamp Docker puts first.
func emit(s string, stderr bool, fn func(Line)) {
	ts, text, ok := strings.Cut(s, " ")
	t, err := time.Parse(time.RFC3339Nano, ts)
	if !ok || err != nil {
		// A line that ends exactly at the timestamp ("<ts>" alone) is empty.
		if t2, err2 := time.Parse(time.RFC3339Nano, s); err2 == nil {
			fn(Line{Time: t2, Stderr: stderr})
			return
		}
		t, text = time.Now(), s
	}
	fn(Line{Time: t, Stderr: stderr, Text: text})
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

func eof(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return nil
	}
	return err
}

// Event is a container lifecycle event.
type Event struct {
	Time     time.Time
	ID       string
	Name     string
	Action   string // start, die, oom, kill, restart, destroy, …
	ExitCode string // for die
}

// Events streams container events from since until ctx ends.
func (c *Client) Events(ctx context.Context, since time.Time, fn func(Event)) error {
	filters := `{"type":["container"],"event":["start","die","oom","destroy"]}`
	q := url.Values{"filters": {filters}}
	if !since.IsZero() {
		q.Set("since", strconv.FormatInt(since.Unix(), 10))
	}
	resp, err := c.get(ctx, c.stream, "/events?"+q.Encode())
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	for {
		var raw struct {
			Action   string
			TimeNano int64 `json:"timeNano"`
			Actor    struct {
				ID         string
				Attributes map[string]string
			}
		}
		if err := dec.Decode(&raw); err != nil {
			return eof(err)
		}
		fn(Event{
			Time: time.Unix(0, raw.TimeNano), ID: raw.Actor.ID, Name: raw.Actor.Attributes["name"],
			Action: raw.Action, ExitCode: raw.Actor.Attributes["exitCode"],
		})
	}
}
