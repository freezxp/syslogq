package victorialogs

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/freezxp/syslogq/internal/model"
	"github.com/freezxp/syslogq/internal/storage"
)

func entry() model.LogEntry {
	fac, sev, pri := 20, 5, 165
	now := time.Date(2026, 9, 14, 14, 30, 0, 0, time.UTC)
	return model.LogEntry{
		Timestamp:    now,
		ReceivedAt:   now.Add(41 * time.Millisecond),
		Message:      "VPN tunnel disconnected",
		Hostname:     "fw01",
		SourceIP:     "10.10.1.1",
		SourcePort:   41022,
		Facility:     &fac,
		FacilityName: "local4",
		Severity:     &sev,
		SeverityName: "notice",
		Priority:     &pri,
		Protocol:     "udp",
		Format:       "rfc5424",
		AppName:      "vpn",
		SourceID:     "syslog-udp-514",
		SourceType:   "syslog",
		TenantID:     "default",
		Fields:       map[string]string{"vendor": "fortinet"},
		Labels:       map[string]string{"env": "prod"},
		RawMessage:   "<165>1 ... raw ...",
	}
}

func TestWriteLogsMapping(t *testing.T) {
	var gotPath, gotCT string
	var lines []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotCT = r.URL.Path, r.Header.Get("Content-Type")
		sc := bufio.NewScanner(r.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			if sc.Text() == "" {
				continue
			}
			var m map[string]any
			if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
				t.Errorf("bad json line: %v", err)
			}
			lines = append(lines, m)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.WriteLogs(context.Background(), []model.LogEntry{entry()}); err != nil {
		t.Fatal(err)
	}

	if gotPath != "/insert/jsonline" {
		t.Errorf("path: %q", gotPath)
	}
	if gotCT != "application/x-ndjson" {
		t.Errorf("content type: %q", gotCT)
	}
	if len(lines) != 1 {
		t.Fatalf("lines: %d", len(lines))
	}
	m := lines[0]
	if m["_time"] != "2026-09-14T14:30:00Z" || m["_msg"] != "VPN tunnel disconnected" {
		t.Errorf("reserved keys: %v", m)
	}
	for k, want := range map[string]any{
		"hostname": "fw01", "source_ip": "10.10.1.1", "source_port": float64(41022),
		"facility": float64(20), "severity": float64(5), "severity_name": "notice",
		"priority": float64(165), "protocol": "udp", "format": "rfc5424",
		"app_name": "vpn", "vendor": "fortinet", "labels.env": "prod",
		"raw_message": "<165>1 ... raw ...", "tenant_id": "default",
	} {
		if m[k] != want {
			t.Errorf("field %q: got %v want %v", k, m[k], want)
		}
	}
	// Absent optionals stay absent (omitempty wire shape).
	if _, ok := m["process_id"]; ok {
		t.Errorf("process_id should be omitted")
	}
}

func TestWriteLogsAccountID(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL, AccountID: 7})
	if err := c.WriteLogs(context.Background(), []model.LogEntry{entry()}); err != nil {
		t.Fatal(err)
	}
	if query != "account_id=7" {
		t.Errorf("query: %q", query)
	}
}

func TestWriteLogsErrorMapping(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusInternalServerError, storage.ErrBackendUnavailable},
		{http.StatusServiceUnavailable, storage.ErrBackendUnavailable},
		{http.StatusBadRequest, storage.ErrBadRequest},
		{http.StatusForbidden, storage.ErrBackendUnavailable},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(c.status)
			w.Write([]byte("boom")) //nolint:errcheck
		}))
		cl, _ := New(Config{BaseURL: srv.URL})
		err := cl.WriteLogs(context.Background(), []model.LogEntry{entry()})
		if !errors.Is(err, c.want) {
			t.Errorf("status %d: got %v, want %v", c.status, err, c.want)
		}
		srv.Close()
	}
}

func TestWriteLogsNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	cl, _ := New(Config{BaseURL: srv.URL})
	srv.Close() // server gone: transport error
	err := cl.WriteLogs(context.Background(), []model.LogEntry{entry()})
	if !errors.Is(err, storage.ErrBackendUnavailable) {
		t.Errorf("got %v, want ErrBackendUnavailable", err)
	}
}

func TestWriteLogsContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()
	cl, _ := New(Config{BaseURL: srv.URL, Timeout: 5 * time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := cl.WriteLogs(ctx, []model.LogEntry{entry()})
	if !errors.Is(err, storage.ErrTimeout) {
		t.Errorf("got %v, want ErrTimeout", err)
	}
}

func TestHealth(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer ok.Close()
	c, _ := New(Config{BaseURL: ok.URL})
	if err := c.Health(context.Background()); err != nil {
		t.Errorf("health: %v", err)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	c2, _ := New(Config{BaseURL: bad.URL})
	if err := c2.Health(context.Background()); !errors.Is(err, storage.ErrBackendUnavailable) {
		t.Errorf("health failure: %v", err)
	}
}

func TestNewValidation(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Error("empty BaseURL should fail")
	}
	if _, err := New(Config{BaseURL: "not a url"}); err == nil {
		t.Error("invalid BaseURL should fail")
	}
}

func TestWriteLogsEmptyBatch(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	defer srv.Close()
	c, _ := New(Config{BaseURL: srv.URL})
	if err := c.WriteLogs(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("empty batch must not hit the wire")
	}
}
