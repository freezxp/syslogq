package ingestion

import (
	"bytes"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// testConn is an in-memory net.Conn for driving a frameReader: buffered data
// is served even after Close, an empty buffer blocks until closed or the read
// deadline (mirroring a real socket).
type testConn struct {
	r        *bytes.Reader
	closed   chan struct{}
	deadline time.Time
}

func newTestConn(data []byte) *testConn {
	return &testConn{r: bytes.NewReader(data), closed: make(chan struct{})}
}

func (c *testConn) Read(b []byte) (int, error) {
	if c.r.Len() > 0 {
		return c.r.Read(b)
	}
	select {
	case <-c.closed:
		return 0, io.EOF
	default:
	}
	wait := 100 * time.Millisecond
	if !c.deadline.IsZero() {
		wait = time.Until(c.deadline)
		if wait <= 0 {
			return 0, &netTimeout{}
		}
	}
	select {
	case <-c.closed:
		return 0, io.EOF
	case <-time.After(wait):
		if !c.deadline.IsZero() {
			return 0, &netTimeout{}
		}
		return 0, io.EOF
	}
}

func (c *testConn) Write(b []byte) (int, error) { return len(b), nil }
func (c *testConn) Close() error {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return nil
}
func (c *testConn) LocalAddr() net.Addr                { return nil }
func (c *testConn) RemoteAddr() net.Addr               { return nil }
func (c *testConn) SetDeadline(t time.Time) error      { c.deadline = t; return nil }
func (c *testConn) SetReadDeadline(t time.Time) error  { c.deadline = t; return nil }
func (c *testConn) SetWriteDeadline(t time.Time) error { return nil }

type netTimeout struct{}

func (*netTimeout) Error() string   { return "i/o timeout" }
func (*netTimeout) Timeout() bool   { return true }
func (*netTimeout) Temporary() bool { return true }

// drainFrames reads frames until the stream ends. The conn is closed up front:
// buffered data is still delivered, then the reader sees EOF.
func drainFrames(t *testing.T, conn net.Conn, max int) ([][]byte, error) {
	t.Helper()
	conn.Close()
	fr := newFrameReader(conn, max, 0)
	var frames [][]byte
	for {
		msg, err := fr.next()
		if err != nil {
			return frames, err
		}
		frames = append(frames, msg)
	}
}

func assertStreamEnd(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("terminal error: %v", err)
	}
}

func TestFrameReaderDelimited(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "lf frames",
			in:   "<34>Oct 11 22:14:15 host su: root\n<34>Oct 11 22:14:16 host su: again\n",
			want: []string{"<34>Oct 11 22:14:15 host su: root", "<34>Oct 11 22:14:16 host su: again"},
		},
		{
			name: "nul frames",
			in:   "<34>a\x00<34>b\x00",
			want: []string{"<34>a", "<34>b"},
		},
		{
			name: "crlf stripped",
			in:   "msg1\r\nmsg2\r\n",
			want: []string{"msg1", "msg2"},
		},
		{
			name: "digits without space are a delimited frame",
			in:   "123\n",
			want: []string{"123"},
		},
		{
			name: "zero-padded octet count accepted",
			in:   "0007 <34>msg",
			want: []string{"<34>msg"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := drainFrames(t, newTestConn([]byte(tc.in)), 1024)
			assertStreamEnd(t, err)
			if strings.Join(toStrings(got), "|") != strings.Join(tc.want, "|") {
				t.Fatalf("got %q, want %q", toStrings(got), tc.want)
			}
		})
	}
}

func toStrings(b [][]byte) []string {
	out := make([]string, len(b))
	for i, v := range b {
		out[i] = string(v)
	}
	return out
}

func TestFrameReaderOctetCounted(t *testing.T) {
	frame := "<34>Oct 11 22:14:15 host app: message"
	in := strconv.Itoa(len(frame)) + " " + frame + "3 abc"
	got, err := drainFrames(t, newTestConn([]byte(in)), 1024)
	assertStreamEnd(t, err)
	if len(got) != 2 || string(got[0]) != frame || string(got[1]) != "abc" {
		t.Fatalf("got %q", toStrings(got))
	}
}

func TestFrameReaderOversizedDelimited(t *testing.T) {
	in := strings.Repeat("x", 100) + "\nok\n"
	conn := newTestConn([]byte(in))
	conn.Close()
	fr := newFrameReader(conn, 50, 0)

	if _, err := fr.next(); !errors.Is(err, errOversizedFrame) {
		t.Fatalf("first frame: want errOversizedFrame, got %v", err)
	}
	msg, err := fr.next()
	if err != nil || string(msg) != "ok" {
		t.Fatalf("second frame: got %q, %v", msg, err)
	}
}

func TestFrameReaderOversizedOctetCounted(t *testing.T) {
	payload := strings.Repeat("x", 100)
	in := "100 " + payload + "3 ok!"
	conn := newTestConn([]byte(in))
	conn.Close()
	fr := newFrameReader(conn, 50, 0)

	if _, err := fr.next(); !errors.Is(err, errOversizedFrame) {
		t.Fatalf("first frame: want errOversizedFrame, got %v", err)
	}
	msg, err := fr.next()
	if err != nil || string(msg) != "ok!" {
		t.Fatalf("second frame: got %q, %v", msg, err)
	}
}

func TestFrameReaderIdleTimeout(t *testing.T) {
	conn := newTestConn(nil)
	fr := newFrameReader(conn, 100, 5*time.Millisecond)
	defer conn.Close()
	if _, err := fr.next(); !errors.Is(err, ErrIdleTimeout) {
		t.Fatalf("want ErrIdleTimeout, got %v", err)
	}
}

func TestFrameReaderPartialAtEOF(t *testing.T) {
	// Trailing bytes without a delimiter are not a message.
	got, err := drainFrames(t, newTestConn([]byte("complete\npartial-without-delim")), 1024)
	assertStreamEnd(t, err)
	if len(got) != 1 || string(got[0]) != "complete" {
		t.Fatalf("got %q", toStrings(got))
	}
}

func TestIndexDelim(t *testing.T) {
	if i, b, ok := indexDelim([]byte("ab\x00cd")); !ok || i != 2 || b != 0 {
		t.Fatalf("nul: %d %d %v", i, b, ok)
	}
	if i, b, ok := indexDelim([]byte("ab\ncd")); !ok || i != 2 || b != '\n' {
		t.Fatalf("lf: %d %d %v", i, b, ok)
	}
	if _, _, ok := indexDelim([]byte("abcd")); ok {
		t.Fatal("no delim expected")
	}
	if i, _, ok := indexDelim([]byte("\x00a\n")); !ok || i != 0 {
		t.Fatalf("earliest: %d %v", i, ok)
	}
}
