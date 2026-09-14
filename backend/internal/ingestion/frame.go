package ingestion

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"strconv"
	"time"
)

// RFC6587 framing (docs/ingestion.md §1): TCP/TLS streams carry either
// octet-counted frames ("123 <message>") or non-transparent frames
// delimited by LF or NUL. A stream SHOULD NOT mix the two styles; the
// framing style is decided per frame at a clean boundary.
type frameReader struct {
	conn net.Conn
	r    *bufio.Reader
	max  int
	idle time.Duration
	// pending holds bytes read for the current delimited frame that has not
	// seen its delimiter yet (plus any complete-but-unreturned bytes after
	// the delimiter of the frame currently being returned).
	pending []byte
	// discarding flushes an oversized delimited frame up to its delimiter.
	discarding bool
}

// errOversizedFrame is recoverable: the frame was skipped and counted by
// the caller; call next() again for the following frame.
var errOversizedFrame = errors.New("frame exceeds max message size")

// ErrIdleTimeout marks a TCP/TLS connection that went silent.
var ErrIdleTimeout = errors.New("connection idle timeout")

func newFrameReader(conn net.Conn, max int, idle time.Duration) *frameReader {
	return &frameReader{
		conn: conn,
		r:    bufio.NewReaderSize(conn, 64*1024),
		max:  max,
		idle: idle,
	}
}

// next returns the next complete frame. io.EOF means the peer closed;
// ErrIdleTimeout and other errors are fatal for the connection.
func (f *frameReader) next() ([]byte, error) {
	for {
		// A complete delimited frame is already buffered.
		if idx, _, ok := indexDelim(f.pending); ok {
			msg := f.pending[:idx]
			f.pending = f.pending[idx+1:]
			if len(msg) > 0 && msg[len(msg)-1] == '\r' {
				msg = msg[:len(msg)-1]
			}
			if len(msg) > f.max {
				return nil, errOversizedFrame
			}
			return msg, nil
		}

		// Flush an oversized delimited frame up to its delimiter.
		if f.discarding {
			b, err := f.readByte()
			if err != nil {
				f.discarding = false
				return nil, asTimeout(err)
			}
			if b == '\n' || b == 0 {
				f.discarding = false
				return nil, errOversizedFrame
			}
			continue
		}
		if len(f.pending) > f.max {
			f.discarding = true
			f.pending = f.pending[:0]
			continue
		}

		// A fresh frame that starts with a digit may be octet-counted.
		if len(f.pending) == 0 {
			if head, err := f.r.Peek(1); err == nil && isDigit(head[0]) {
				msg, err, probed := f.tryOctetCounted()
				if probed {
					return msg, err
				}
				// Not octet-counted: the probe's bytes now sit in pending
				// and belong to a delimited frame.
			}
		}

		f.setDeadline()
		chunk, err := f.r.ReadSlice('\n') // 64KiB chunks + ErrBufferFull
		f.pending = append(f.pending, chunk...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			// The stream ended, but pending may still hold complete
			// frames (NUL-delimited data carries no '\n'; probe
			// leftovers may hold one). Deliver them before the error.
			if _, _, ok := indexDelim(f.pending); ok {
				continue
			}
			// EOF mid-frame: the partial bytes are not a message.
			f.pending = nil
			return nil, asTimeout(err)
		}
	}
}

// tryOctetCounted consumes leading digits looking for "NNN SP". probed=true
// means the frame was decided here (msg/err carry the outcome); probed=false
// means the bytes were not an octet count and were moved to pending.
func (f *frameReader) tryOctetCounted() (msg []byte, err error, probed bool) {
	var digits []byte
	for {
		b, rerr := f.readByte()
		if rerr != nil {
			return nil, asTimeout(rerr), true
		}
		if isDigit(b) {
			digits = append(digits, b)
			if len(digits) > 10 {
				f.pending = append(f.pending, digits...)
				return nil, nil, false
			}
			continue
		}
		if b == ' ' && len(digits) > 0 {
			n, perr := strconv.Atoi(string(digits))
			if perr != nil {
				f.pending = append(f.pending, digits...)
				f.pending = append(f.pending, b)
				return nil, nil, false
			}
			if n > f.max {
				// Drain the announced payload, then report the frame as
				// oversized so the connection stays usable.
				f.setDeadline()
				if _, cerr := io.CopyN(io.Discard, f.r, int64(n)); cerr != nil {
					return nil, asTimeout(cerr), true
				}
				return nil, errOversizedFrame, true
			}
			if n == 0 {
				return []byte{}, nil, true
			}
			buf := make([]byte, n)
			f.setDeadline()
			if _, ferr := io.ReadFull(f.r, buf); ferr != nil {
				return nil, asTimeout(ferr), true
			}
			return buf, nil, true
		}
		// Not "digits SP": a delimited message that happens to start with
		// digits; give the bytes back to the delimited path.
		f.pending = append(f.pending, digits...)
		f.pending = append(f.pending, b)
		return nil, nil, false
	}
}

// indexDelim finds the earliest LF or NUL delimiter in b.
func indexDelim(b []byte) (int, byte, bool) {
	lf := bytes.IndexByte(b, '\n')
	nul := bytes.IndexByte(b, 0)
	switch {
	case lf >= 0 && (nul < 0 || lf < nul):
		return lf, '\n', true
	case nul >= 0:
		return nul, 0, true
	default:
		return 0, 0, false
	}
}

func (f *frameReader) readByte() (byte, error) {
	f.setDeadline()
	return f.r.ReadByte()
}

// asTimeout converts an OS read-deadline error into ErrIdleTimeout so
// callers see one sentinel for silent connections.
func asTimeout(err error) error {
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return ErrIdleTimeout
		}
	}
	return err
}

func (f *frameReader) setDeadline() {
	if f.idle > 0 {
		f.conn.SetReadDeadline(time.Now().Add(f.idle)) //nolint:errcheck
	}
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }
