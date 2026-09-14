package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/freezxp/syslogq/internal/query"
	"github.com/freezxp/syslogq/internal/storage"
)

const (
	maxQueryLen   = 8 * 1024
	defaultWindow = time.Hour
	maxSearchHits = 1000
	maxOffset     = 100000
	maxBuckets    = 2000
)

// parseRange extracts the shared ?q&start&end parameters into
// storage.RangeParams. q is Advanced-mode syntax: parsed and recompiled so
// only escaped output ever reaches storage (docs/security.md §5).
func parseRange(r *http.Request) (storage.RangeParams, error) {
	var p storage.RangeParams
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > maxQueryLen {
		return p, fmt.Errorf("query too long (max %d bytes)", maxQueryLen)
	}
	if q != "" {
		e, err := query.Parse(q)
		if err != nil {
			return p, err
		}
		filter, err := query.Compile(e)
		if err != nil {
			return p, err
		}
		p.Filter = filter
	}
	start, end := r.URL.Query().Get("start"), r.URL.Query().Get("end")
	var err error
	if start != "" {
		if p.Start, err = time.Parse(time.RFC3339Nano, start); err != nil {
			return p, errors.New("start must be RFC3339")
		}
	}
	if end != "" {
		if p.End, err = time.Parse(time.RFC3339Nano, end); err != nil {
			return p, errors.New("end must be RFC3339")
		}
	}
	return p, nil
}

// defaultEnd returns the effective range, applying the default window when
// bounds are missing and rejecting inverted ranges.
func defaultRange(p storage.RangeParams) (storage.RangeParams, error) {
	if p.End.IsZero() {
		p.End = time.Now().UTC()
	}
	if p.Start.IsZero() {
		p.Start = p.End.Add(-defaultWindow)
	}
	if !p.Start.Before(p.End) {
		return p, errors.New("start must be before end")
	}
	return p, nil
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	p, err := parseRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	p, err = defaultRange(p)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	limit := clampInt(r, "limit", 100, 1, maxSearchHits)
	offset := clampInt(r, "offset", 0, 0, maxOffset)

	logs, err := s.deps.Reader.Search(r.Context(), storage.SearchParams{
		RangeParams: p,
		Offset:      offset,
		Limit:       limit,
	})
	if err != nil {
		s.writeStorageError(w, err)
		return
	}
	if logs == nil {
		logs = []map[string]string{}
	}
	resp := map[string]any{"logs": logs}
	if len(logs) == limit {
		resp["next_offset"] = offset + len(logs)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleCount(w http.ResponseWriter, r *http.Request) {
	p, err := parseRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	p, err = defaultRange(p)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	n, err := s.deps.Reader.Count(r.Context(), p)
	if err != nil {
		s.writeStorageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": n})
}

func (s *Server) handleVolume(w http.ResponseWriter, r *http.Request) {
	p, err := parseRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	p, err = defaultRange(p)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	step, err := parseBucket(r.URL.Query().Get("bucket"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	if p.End.Sub(p.Start)/step > maxBuckets {
		writeError(w, http.StatusBadRequest, "invalid_query",
			fmt.Sprintf("range/bucket yields more than %d buckets", maxBuckets))
		return
	}
	buckets, err := s.deps.Reader.CountByTime(r.Context(), p, step)
	if err != nil {
		s.writeStorageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"bucket": step.String(), "buckets": zeroFill(buckets, p.Start, p.End, step)})
}

// parseBucket accepts Go durations plus the "Nd" day form.
func parseBucket(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 5 * time.Minute, nil
	}
	if n, err := strconv.Atoi(strings.TrimSuffix(s, "d")); err == nil && strings.HasSuffix(s, "d") {
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, errors.New("bucket must be a positive duration like 5m, 1h, 1d")
	}
	return d, nil
}

// zeroFill expands sparse buckets into a contiguous, epoch-aligned series.
func zeroFill(in []storage.BucketCount, start, end time.Time, step time.Duration) []storage.BucketCount {
	counts := make(map[int64]int64, len(in))
	for _, b := range in {
		counts[b.Time.UnixNano()] = b.Count
	}
	out := make([]storage.BucketCount, 0, int(end.Sub(start)/step)+1)
	for t := start.Truncate(step); !t.After(end); t = t.Add(step) {
		out = append(out, storage.BucketCount{Time: t, Count: counts[t.UnixNano()]})
	}
	return out
}

func (s *Server) handleFields(w http.ResponseWriter, r *http.Request) {
	p, err := parseRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	p, err = defaultRange(p)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	fields, err := s.deps.Reader.FieldNames(r.Context(), p)
	if err != nil {
		s.writeStorageError(w, err)
		return
	}
	if fields == nil {
		fields = []storage.ValueCount{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"fields": fields})
}

func (s *Server) handleFieldValues(w http.ResponseWriter, r *http.Request) {
	field := r.PathValue("field")
	p, err := parseRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	p, err = defaultRange(p)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	limit := clampInt(r, "limit", 10, 1, 100)
	values, err := s.deps.Reader.FieldValues(r.Context(), p, field, limit)
	if err != nil {
		if errors.Is(err, storage.ErrBadRequest) {
			writeError(w, http.StatusBadRequest, "invalid_query", "invalid field name")
			return
		}
		s.writeStorageError(w, err)
		return
	}
	if values == nil {
		values = []storage.ValueCount{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"field": field, "values": values})
}

// writeStorageError maps typed storage errors to the stable API shape.
func (s *Server) writeStorageError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrBadRequest):
		writeError(w, http.StatusBadRequest, "invalid_query", "storage rejected the query")
	case errors.Is(err, storage.ErrTimeout):
		writeError(w, http.StatusGatewayTimeout, "storage_timeout", "storage timed out")
	default:
		writeError(w, http.StatusServiceUnavailable, "storage_unavailable", "storage backend unavailable")
	}
}

func clampInt(r *http.Request, name string, def, min, max int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}
