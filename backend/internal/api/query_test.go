package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/freezxp/syslogq/internal/storage"
)

// fakeReader records calls and returns canned data; it stands in for the
// VictoriaLogs adapter.
type fakeReader struct {
	mu sync.Mutex

	gotFilter   string
	gotRange    storage.RangeParams
	gotSearch   storage.SearchParams
	gotField    string
	gotLimit    int
	gotStep     time.Duration
	searchCalls int

	searchRows []map[string]string
	count      int64
	buckets    []storage.BucketCount
	fields     []storage.ValueCount
	values     []storage.ValueCount
	err        error
}

func (f *fakeReader) record(p storage.RangeParams) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotFilter = p.Filter
	f.gotRange = p
}

func (f *fakeReader) Search(_ context.Context, p storage.SearchParams) ([]map[string]string, error) {
	f.record(p.RangeParams)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotSearch = p
	f.searchCalls++
	if f.err != nil {
		return nil, f.err
	}
	return f.searchRows, nil
}

func (f *fakeReader) Count(_ context.Context, p storage.RangeParams) (int64, error) {
	f.record(p)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, f.err
	}
	return f.count, nil
}

func (f *fakeReader) CountByTime(_ context.Context, p storage.RangeParams, step time.Duration) ([]storage.BucketCount, error) {
	f.record(p)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotStep = step
	if f.err != nil {
		return nil, f.err
	}
	return f.buckets, nil
}

func (f *fakeReader) FieldNames(_ context.Context, p storage.RangeParams) ([]storage.ValueCount, error) {
	f.record(p)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.fields, nil
}

func (f *fakeReader) FieldValues(_ context.Context, p storage.RangeParams, field string, limit int) ([]storage.ValueCount, error) {
	f.record(p)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotField = field
	f.gotLimit = limit
	if f.err != nil {
		return nil, f.err
	}
	return f.values, nil
}

func newQueryServer(t *testing.T, fr storage.Reader) (*Server, string) {
	t.Helper()
	s := newTestServerWithReader(t, false, &ingestRecorder{}, fr)
	return s, login(t, s)
}

func TestSearch(t *testing.T) {
	fr := &fakeReader{
		searchRows: []map[string]string{
			{"_time": "2026-09-14T12:00:00Z", "_msg": "hello", "hostname": "fw01"},
		},
	}
	s, token := newQueryServer(t, fr)

	start := "2026-09-14T11:00:00Z"
	end := "2026-09-14T12:00:00Z"
	rec := doJSON(t, s, "GET", "/api/v1/logs/search?q=hello&start="+start+"&end="+end+"&limit=100", token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("search: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Logs       []map[string]string `json:"logs"`
		NextOffset *int                `json:"next_offset"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body: %v", err)
	}
	if len(out.Logs) != 1 || out.Logs[0]["_msg"] != "hello" {
		t.Errorf("logs = %+v", out.Logs)
	}
	if out.NextOffset != nil {
		t.Errorf("next_offset = %v, want absent on partial page", *out.NextOffset)
	}
	if fr.gotFilter != "hello" {
		t.Errorf("filter passed to reader = %q, want compiled phrase", fr.gotFilter)
	}
	if fr.gotSearch.Limit != 100 || fr.gotSearch.Offset != 0 {
		t.Errorf("search params = %+v", fr.gotSearch)
	}
}

func TestSearchEmptyReturnsEmptyArray(t *testing.T) {
	// A nil slice from storage must serialize as [], not null: the UI
	// dereferences logs.length unconditionally.
	fr := &fakeReader{}
	s, token := newQueryServer(t, fr)

	rec := doJSON(t, s, "GET",
		"/api/v1/logs/search?start=2026-09-14T11:00:00Z&end=2026-09-14T12:00:00Z", token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("search: %d %s", rec.Code, rec.Body.String())
	}
	if !json.Valid(rec.Body.Bytes()) || !strings.Contains(rec.Body.String(), `"logs":[]`) {
		t.Errorf("empty search body = %s, want logs:[]", rec.Body.String())
	}

	rec = doJSON(t, s, "GET",
		"/api/v1/fields?start=2026-09-14T11:00:00Z&end=2026-09-14T12:00:00Z", token, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"fields":[]`) {
		t.Errorf("empty fields body = %s, want fields:[]", rec.Body.String())
	}

	rec = doJSON(t, s, "GET",
		"/api/v1/fields/hostname/values?start=2026-09-14T11:00:00Z&end=2026-09-14T12:00:00Z", token, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"values":[]`) {
		t.Errorf("empty values body = %s, want values:[]", rec.Body.String())
	}
}

func TestSearchPagination(t *testing.T) {
	rows := make([]map[string]string, 100)
	for i := range rows {
		rows[i] = map[string]string{"_msg": fmt.Sprintf("m%d", i)}
	}
	fr := &fakeReader{searchRows: rows}
	s, token := newQueryServer(t, fr)

	rec := doJSON(t, s, "GET", "/api/v1/logs/search?limit=100&offset=200", token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("search: %d", rec.Code)
	}
	var out struct {
		NextOffset int `json:"next_offset"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.NextOffset != 300 {
		t.Errorf("next_offset = %d, want 300", out.NextOffset)
	}
	if fr.gotSearch.Offset != 200 {
		t.Errorf("offset = %d", fr.gotSearch.Offset)
	}
}

func TestSearchRejectsBadQuery(t *testing.T) {
	fr := &fakeReader{}
	s, token := newQueryServer(t, fr)

	for _, q := range []string{"host:", "a AND", "(x"} {
		rec := doJSON(t, s, "GET", "/api/v1/logs/search?q="+url.QueryEscape(q), token, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("q=%q: status %d, want 400", q, rec.Code)
		}
	}
}

func TestSearchRejectsBadRange(t *testing.T) {
	fr := &fakeReader{}
	s, token := newQueryServer(t, fr)

	cases := []string{
		"start=notatime",
		"end=notatime",
		"start=2026-09-14T12:00:00Z&end=2026-09-14T11:00:00Z",
		"start=2026-09-14T12:00:00Z&end=2026-09-14T12:00:00Z",
	}
	for _, c := range cases {
		rec := doJSON(t, s, "GET", "/api/v1/logs/search?"+c, token, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", c, rec.Code)
		}
	}
}

func TestSearchInjectionCompilesSafe(t *testing.T) {
	fr := &fakeReader{}
	s, token := newQueryServer(t, fr)

	// A value that tries to break out of its quotes and become live OR
	// syntax must survive as one inert quoted value.
	q := `host:"a\") OR (severity:1"`
	rec := doJSON(t, s, "GET", "/api/v1/logs/search?q="+url.QueryEscape(q), token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	if fr.gotFilter != `host:"a\") OR (severity:1"` {
		t.Errorf("filter = %q", fr.gotFilter)
	}
}

func TestSearchRequiresAuth(t *testing.T) {
	fr := &fakeReader{}
	s, _ := newQueryServer(t, fr)
	rec := doJSON(t, s, "GET", "/api/v1/logs/search", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", rec.Code)
	}
}

func TestCount(t *testing.T) {
	fr := &fakeReader{count: 42}
	s, token := newQueryServer(t, fr)
	rec := doJSON(t, s, "GET", "/api/v1/logs/count?q=severity:>=3", token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("count: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Count int64 `json:"count"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Count != 42 {
		t.Errorf("count = %d", out.Count)
	}
	if fr.gotFilter != "severity:>=3" {
		t.Errorf("filter = %q", fr.gotFilter)
	}
}

func TestVolumeZeroFill(t *testing.T) {
	// Sparse buckets: 11:05 and 11:15 present, 11:00/11:10/11:20 missing.
	fr := &fakeReader{buckets: []storage.BucketCount{
		{Time: time.Date(2026, 9, 14, 11, 5, 0, 0, time.UTC), Count: 3},
		{Time: time.Date(2026, 9, 14, 11, 15, 0, 0, time.UTC), Count: 7},
	}}
	s, token := newQueryServer(t, fr)

	start := "2026-09-14T11:00:00Z"
	end := "2026-09-14T11:25:00Z"
	rec := doJSON(t, s, "GET", "/api/v1/logs/volume?bucket=5m&start="+start+"&end="+end, token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("volume: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Bucket  string                `json:"bucket"`
		Buckets []storage.BucketCount `json:"buckets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body: %v", err)
	}
	if len(out.Buckets) != 6 { // 11:00..11:25 inclusive
		t.Fatalf("buckets = %d, want 6", len(out.Buckets))
	}
	want := []int64{0, 3, 0, 7, 0, 0}
	for i, b := range out.Buckets {
		if b.Count != want[i] {
			t.Errorf("bucket %d (%s) count = %d, want %d", i, b.Time, b.Count, want[i])
		}
	}
	if fr.gotStep != 5*time.Minute {
		t.Errorf("step = %v", fr.gotStep)
	}
}

func TestVolumeBucketForms(t *testing.T) {
	fr := &fakeReader{}
	s, token := newQueryServer(t, fr)

	rec := doJSON(t, s, "GET", "/api/v1/logs/volume?bucket=1d&start=2026-09-14T00:00:00Z&end=2026-09-15T00:00:00Z", token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("1d: %d %s", rec.Code, rec.Body.String())
	}
	if fr.gotStep != 24*time.Hour {
		t.Errorf("step = %v, want 24h", fr.gotStep)
	}

	rec = doJSON(t, s, "GET", "/api/v1/logs/volume?bucket=bogus&start=2026-09-14T00:00:00Z&end=2026-09-15T00:00:00Z", token, "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bogus bucket: %d, want 400", rec.Code)
	}

	// 25h/1s exceeds the bucket cap.
	rec = doJSON(t, s, "GET", "/api/v1/logs/volume?bucket=1s&start=2026-09-14T00:00:00Z&end=2026-09-14T01:00:00Z", token, "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("too many buckets: %d, want 400", rec.Code)
	}
}

func TestFields(t *testing.T) {
	fr := &fakeReader{fields: []storage.ValueCount{
		{Value: "_msg", Count: 14}, {Value: "hostname", Count: 14},
	}}
	s, token := newQueryServer(t, fr)

	rec := doJSON(t, s, "GET", "/api/v1/fields", token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("fields: %d", rec.Code)
	}
	var out struct {
		Fields []storage.ValueCount `json:"fields"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out.Fields) != 2 || out.Fields[1].Value != "hostname" {
		t.Errorf("fields = %+v", out.Fields)
	}
}

func TestFieldValues(t *testing.T) {
	fr := &fakeReader{values: []storage.ValueCount{
		{Value: "fw01", Count: 9}, {Value: "", Count: 5},
	}}
	s, token := newQueryServer(t, fr)

	rec := doJSON(t, s, "GET", "/api/v1/fields/hostname/values?limit=20", token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("values: %d %s", rec.Code, rec.Body.String())
	}
	if fr.gotField != "hostname" || fr.gotLimit != 20 {
		t.Errorf("got field=%q limit=%d", fr.gotField, fr.gotLimit)
	}
	var out struct {
		Field  string               `json:"field"`
		Values []storage.ValueCount `json:"values"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Field != "hostname" || len(out.Values) != 2 || out.Values[0].Value != "fw01" {
		t.Errorf("values = %+v", out.Values)
	}
}

func TestStorageErrorMapping(t *testing.T) {
	fr := &fakeReader{err: storage.ErrBackendUnavailable}
	s, token := newQueryServer(t, fr)
	rec := doJSON(t, s, "GET", "/api/v1/logs/search", token, "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("unavailable: %d, want 503", rec.Code)
	}

	fr.err = storage.ErrTimeout
	rec = doJSON(t, s, "GET", "/api/v1/logs/search", token, "")
	if rec.Code != http.StatusGatewayTimeout {
		t.Errorf("timeout: %d, want 504", rec.Code)
	}

	fr.err = storage.ErrBadRequest
	rec = doJSON(t, s, "GET", "/api/v1/logs/search", token, "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad request: %d, want 400", rec.Code)
	}
}
