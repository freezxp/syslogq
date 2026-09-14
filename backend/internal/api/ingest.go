package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

// Ingest limits (docs/ingestion.md §2): 1000 entries / 8 MiB per request.
const (
	ingestMaxEntries = 1000
	ingestMaxBytes   = 8 << 20
)

// handleIngest implements POST /api/v1/ingest (docs/api.md §2). Bodies:
// single JSON object, JSON array, or NDJSON — sniffed by first byte. Each
// entry is fed through the standard pipeline path (detect → parse →
// normalize → queue), so metrics, limits, and policies behave identically
// to syslog ingestion.
func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	if ct := r.Header.Get("Content-Type"); ct != "" && !jsonContentType(ct) {
		writeError(w, http.StatusUnsupportedMediaType, "bad_request",
			"content-type must be JSON or NDJSON")
		return
	}

	if r.ContentLength > ingestMaxBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "bad_request",
			"body exceeds 8 MiB limit")
		return
	}
	body := http.MaxBytesReader(w, r.Body, ingestMaxBytes)
	first, lines, err := splitLines(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "empty body")
		return
	}

	var rejected map[string]int
	accepted := 0
	switch first {
	case '[': // JSON array: marshal each element back to bytes for the pipeline
		var arr []json.RawMessage
		if err := json.Unmarshal(lines, &arr); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON array")
			return
		}
		if len(arr) > ingestMaxEntries {
			writeError(w, http.StatusRequestEntityTooLarge, "bad_request",
				"too many entries in one request (max 1000)")
			return
		}
		for _, raw := range arr {
			if s.ingestLine(raw) {
				accepted++
			} else {
				rejected = bump(rejected, "invalid_entry")
			}
		}
	default: // single object or NDJSON: lines are entries
		if len(lines) == 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "empty body")
			return
		}
		n := 0
		for _, line := range bytes.Split(lines, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			n++
			if n > ingestMaxEntries {
				writeError(w, http.StatusRequestEntityTooLarge, "bad_request",
					"too many entries in one request (max 1000)")
				return
			}
			if s.ingestLine(line) {
				accepted++
			} else {
				rejected = bump(rejected, "invalid_entry")
			}
		}
	}

	if rejected == nil {
		rejected = map[string]int{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"accepted": accepted,
		"rejected": rejected,
	})
}

// ingestLine runs one entry through the pipeline. Returns false when the
// entry was rejected (not JSON).
func (s *Server) ingestLine(raw []byte) bool {
	return s.deps.IngestFunc(raw)
}

func jsonContentType(ct string) bool {
	return ct == "application/json" || ct == "application/x-ndjson" ||
		bytes.HasPrefix([]byte(ct), []byte("application/json"))
}

// splitLines reads the whole body (bounded), returns the first
// non-whitespace byte and the trimmed body bytes.
func splitLines(body io.Reader) (byte, []byte, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return 0, nil, err
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return 0, nil, io.ErrUnexpectedEOF
	}
	return data[0], data, nil
}

func bump(m map[string]int, key string) map[string]int {
	if m == nil {
		m = map[string]int{}
	}
	m[key]++
	return m
}
