package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/freezxp/syslogq/internal/storage"
)

// pagedReader serves pages like the real adapter: limit rows per call.
type pagedReader struct {
	fakeReader
	rows     []map[string]string
	pageSize int
	calls    []storage.SearchParams
}

func (p *pagedReader) Search(_ context.Context, sp storage.SearchParams) ([]map[string]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, sp)
	if len(p.rows) <= sp.Offset {
		return nil, nil
	}
	end := sp.Offset + sp.Limit
	if end > len(p.rows) {
		end = len(p.rows)
	}
	out := make([]map[string]string, end-sp.Offset)
	copy(out, p.rows[sp.Offset:end])
	return out, nil
}

func exportRows(n int) []map[string]string {
	rows := make([]map[string]string, n)
	for i := range rows {
		rows[i] = map[string]string{"_msg": fmt.Sprintf("m%d", i), "hostname": "fw01"}
	}
	return rows
}

func TestExportNDJSON(t *testing.T) {
	fr := &pagedReader{rows: exportRows(5), pageSize: 1000}
	s, token := newQueryServer(t, fr)

	rec := doJSON(t, s, "GET", "/api/v1/logs/export", token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Errorf("content-type = %q", ct)
	}
	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("lines = %d, want 5", len(lines))
	}
	if !strings.Contains(lines[0], `"m0"`) {
		t.Errorf("first line = %q", lines[0])
	}
}

func TestExportJSON(t *testing.T) {
	fr := &pagedReader{rows: exportRows(3), pageSize: 2}
	s, token := newQueryServer(t, fr)

	rec := doJSON(t, s, "GET", "/api/v1/logs/export?format=json", token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.HasPrefix(body, "[\n") || !strings.HasSuffix(body, "\n]\n") {
		t.Errorf("body not a JSON array: %q", body[:min(30, len(body))])
	}
	if got := strings.Count(body, `"m`); got != 3 {
		t.Errorf("row count = %d, want 3", got)
	}
	// One page request: the fake returns all 3 rows (fewer than the
	// requested page), which correctly signals exhaustion.
	if len(fr.calls) != 1 {
		t.Errorf("search calls = %d, want 1", len(fr.calls))
	}
}

func TestExportCSV(t *testing.T) {
	fr := &pagedReader{rows: []map[string]string{
		{"_msg": "hello, world", "hostname": "fw01"},
		{"_msg": "second", "hostname": "fw02", "severity": "6"},
	}, pageSize: 1000}
	s, token := newQueryServer(t, fr)

	rec := doJSON(t, s, "GET", "/api/v1/logs/export?format=csv", token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want header + 2 rows: %v", len(lines), lines)
	}
	// Columns follow the first row's keys (documented CSV behavior).
	if lines[0] != "_msg,hostname" {
		t.Errorf("header = %q", lines[0])
	}
	// The comma inside the message must be quoted per CSV rules.
	if lines[1] != `"hello, world",fw01` {
		t.Errorf("csv row 1 = %q", lines[1])
	}
}

func TestExportEmpty(t *testing.T) {
	fr := &pagedReader{rows: nil, pageSize: 1000}
	s, token := newQueryServer(t, fr)

	for _, format := range []string{"ndjson", "json", "csv"} {
		rec := doJSON(t, s, "GET", "/api/v1/logs/export?format="+format, token, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", format, rec.Code)
		}
		if format == "json" && rec.Body.String() != "[]" {
			t.Errorf("json empty = %q", rec.Body.String())
		}
		if format == "csv" && rec.Body.String() != "" {
			t.Errorf("csv empty = %q", rec.Body.String())
		}
	}
}

func TestExportLimitCapAndBadFormat(t *testing.T) {
	fr := &pagedReader{rows: exportRows(20000), pageSize: 1000}
	s, token := newQueryServer(t, fr)

	rec := doJSON(t, s, "GET", "/api/v1/logs/export?format=bogus", token, "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad format: %d, want 400", rec.Code)
	}

	rec = doJSON(t, s, "GET", "/api/v1/logs/export?limit=999999", token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("big limit: %d", rec.Code)
	}
	if got := strings.Count(rec.Body.String(), "\n"); got != 10000 {
		t.Errorf("rows = %d, want cap 10000", got)
	}
}

func TestExportInjectsNothing(t *testing.T) {
	fr := &pagedReader{rows: exportRows(1), pageSize: 1000}
	s, token := newQueryServer(t, fr)

	q := url.QueryEscape(`* | stats count()`)
	rec := doJSON(t, s, "GET", "/api/v1/logs/export?q="+q, token, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("pipe query: %d %s, want 400 (unbalanced parens are a parse error)", rec.Code, rec.Body.String())
	}
}
