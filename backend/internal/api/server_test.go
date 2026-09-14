package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/freezxp/syslogq/internal/auth"
	"github.com/freezxp/syslogq/internal/health"
	"github.com/freezxp/syslogq/internal/storage"

	"github.com/prometheus/client_golang/prometheus"
)

type ingestRecorder struct {
	accepted [][]byte
}

func newTestServer(t *testing.T, requireAuth bool, rec *ingestRecorder) *Server {
	return newTestServerWithReader(t, requireAuth, rec, nil)
}

func newTestServerWithReader(t *testing.T, requireAuth bool, rec *ingestRecorder, reader storage.Reader) *Server {
	t.Helper()
	authSvc, err := auth.New(filepath.Join(t.TempDir(), "api.db"), "test-pass", time.Hour)
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	t.Cleanup(func() { authSvc.Close() })
	s := NewServer(Config{Address: ":0"}, Deps{
		MetricsReg: prometheus.NewRegistry(),
		Ready:      health.NewRegistry(),
		Auth:       authSvc,
		IngestFunc: func(raw []byte) bool {
			if !json.Valid(raw) || raw[0] != '{' {
				return false
			}
			rec.accepted = append(rec.accepted, raw)
			return true
		},
		IngestRequireAuth: requireAuth,
		Reader:            reader,
	})
	return s
}

func doJSON(t *testing.T, s *Server, method, path, token string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func login(t *testing.T, s *Server) string {
	t.Helper()
	rec := doJSON(t, s, "POST", "/api/v1/auth/login", "", `{"username":"admin","password":"test-pass"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("login body: %v", err)
	}
	return out.Token
}

func TestLoginFlow(t *testing.T) {
	s := newTestServer(t, false, &ingestRecorder{})

	bad := doJSON(t, s, "POST", "/api/v1/auth/login", "", `{"username":"admin","password":"wrong"}`)
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("bad password: %d", bad.Code)
	}
	var errBody map[string]map[string]string
	if err := json.Unmarshal(bad.Body.Bytes(), &errBody); err != nil {
		t.Fatalf("error shape: %v", err)
	}
	if errBody["error"]["code"] != "unauthorized" {
		t.Fatalf("error code: %v", errBody)
	}

	token := login(t, s)
	if token == "" {
		t.Fatal("empty token")
	}

	me := doJSON(t, s, "GET", "/api/v1/auth/me", token, "")
	if me.Code != http.StatusOK {
		t.Fatalf("me: %d", me.Code)
	}
	var user struct {
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	_ = json.Unmarshal(me.Body.Bytes(), &user)
	if user.Username != "admin" || user.Role != "admin" {
		t.Fatalf("me: %s", me.Body.String())
	}

	noAuth := doJSON(t, s, "GET", "/api/v1/auth/me", "", "")
	if noAuth.Code != http.StatusUnauthorized {
		t.Fatalf("me without token: %d", noAuth.Code)
	}
}

func TestLogoutInvalidatesToken(t *testing.T) {
	s := newTestServer(t, false, &ingestRecorder{})
	token := login(t, s)

	out := doJSON(t, s, "POST", "/api/v1/auth/logout", token, "")
	if out.Code != http.StatusOK {
		t.Fatalf("logout: %d", out.Code)
	}
	after := doJSON(t, s, "GET", "/api/v1/auth/me", token, "")
	if after.Code != http.StatusUnauthorized {
		t.Fatalf("token alive after logout: %d", after.Code)
	}
}

func TestIngestSingleObject(t *testing.T) {
	rec := &ingestRecorder{}
	s := newTestServer(t, false, rec)

	res := doJSON(t, s, "POST", "/api/v1/ingest", "", `{"message":"hello","level":"error","host":"web1"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("ingest: %d %s", res.Code, res.Body.String())
	}
	var out struct {
		Accepted int            `json:"accepted"`
		Rejected map[string]int `json:"rejected"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("body: %v", err)
	}
	if out.Accepted != 1 || len(out.Rejected) != 0 {
		t.Fatalf("accepted=%d rejected=%v", out.Accepted, out.Rejected)
	}
	if len(rec.accepted) != 1 {
		t.Fatalf("pipeline saw %d entries", len(rec.accepted))
	}
}

func TestIngestNDJSONWithRejects(t *testing.T) {
	rec := &ingestRecorder{}
	s := newTestServer(t, false, rec)

	body := `{"message":"one"}` + "\n" + `{"message":"two"}` + "\n" + `not json` + "\n\n"
	req := httptest.NewRequest("POST", "/api/v1/ingest", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-ndjson")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ingest: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Accepted int            `json:"accepted"`
		Rejected map[string]int `json:"rejected"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Accepted != 2 || out.Rejected["invalid_entry"] != 1 {
		t.Fatalf("accepted=%d rejected=%v", out.Accepted, out.Rejected)
	}
}

func TestIngestArray(t *testing.T) {
	rec := &ingestRecorder{}
	s := newTestServer(t, false, rec)

	res := doJSON(t, s, "POST", "/api/v1/ingest", "",
		`[{"message":"a"},{"message":"b"},{"message":"c"}]`)
	if res.Code != http.StatusOK {
		t.Fatalf("ingest: %d %s", res.Code, res.Body.String())
	}
	var out struct {
		Accepted int `json:"accepted"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	if out.Accepted != 3 {
		t.Fatalf("accepted: %d", out.Accepted)
	}
}

func TestIngestRejectsInvalidJSON(t *testing.T) {
	rec := &ingestRecorder{}
	s := newTestServer(t, false, rec)

	res := doJSON(t, s, "POST", "/api/v1/ingest", "", `{oops`)
	if res.Code != http.StatusOK { // per-entry rejection, not request failure
		t.Fatalf("invalid single: %d %s", res.Code, res.Body.String())
	}
	var out struct {
		Accepted int            `json:"accepted"`
		Rejected map[string]int `json:"rejected"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	if out.Accepted != 0 || out.Rejected["invalid_entry"] != 1 {
		t.Fatalf("accepted=%d rejected=%v", out.Accepted, out.Rejected)
	}
}

func TestIngestRequiresAuthWhenConfigured(t *testing.T) {
	rec := &ingestRecorder{}
	s := newTestServer(t, true, rec)

	denied := doJSON(t, s, "POST", "/api/v1/ingest", "", `{"message":"x"}`)
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated ingest: %d", denied.Code)
	}

	token := login(t, s)
	ok := doJSON(t, s, "POST", "/api/v1/ingest", token, `{"message":"x"}`)
	if ok.Code != http.StatusOK {
		t.Fatalf("authenticated ingest: %d %s", ok.Code, ok.Body.String())
	}
}

func TestIngestBadContentType(t *testing.T) {
	rec := &ingestRecorder{}
	s := newTestServer(t, false, rec)

	req := httptest.NewRequest("POST", "/api/v1/ingest",
		bytes.NewReader([]byte(`{"message":"x"}`)))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("content type: %d", w.Code)
	}
}
