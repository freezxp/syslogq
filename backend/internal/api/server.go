// Package api hosts the HTTP surface: health/readiness/metrics (Phase 1),
// auth + ingest (Phase 2), and the versioned REST API under /api/v1
// (Phase 3+). Handlers are thin; see docs/api.md.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/freezxp/syslogq/internal/auth"
	"github.com/freezxp/syslogq/internal/health"
	"github.com/freezxp/syslogq/internal/metrics"
	"github.com/freezxp/syslogq/internal/storage"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Config for the HTTP server.
type Config struct {
	Address string
	// ShutdownTimeout bounds graceful HTTP shutdown (default 5s).
	ShutdownTimeout time.Duration
}

// Deps are the server's collaborators.
type Deps struct {
	MetricsReg *prometheus.Registry
	Ingestion  *metrics.Ingestion
	Ready      *health.Registry
	Auth       *auth.Service
	Log        *slog.Logger
	// IngestFunc feeds one raw entry into the pipeline (HTTP ingest).
	IngestFunc func(raw []byte) bool
	// IngestRequireAuth mirrors ingestion.http.require_auth.
	IngestRequireAuth bool
	// Reader serves the query API (Phase 3); nil disables those routes.
	Reader storage.Reader
	// Web, when set, serves the embedded SPA at "/" (Phase 4).
	Web http.Handler
}

type Server struct {
	http    *http.Server
	cfg     Config
	deps    Deps
	handler http.Handler
}

func NewServer(cfg Config, deps Deps) *Server {
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 5 * time.Second
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	s := &Server{cfg: cfg, deps: deps}
	s.handler = s.routes()
	s.http = &http.Server{
		Addr:              cfg.Address,
		Handler:           s.handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

// Handler exposes the routed handler (for tests).
func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.Handle("/ready", s.deps.Ready.Handler())
	mux.Handle("/metrics", promhttp.HandlerFor(s.deps.MetricsReg, promhttp.HandlerOpts{}))

	if s.deps.Auth != nil {
		// Auth endpoints (no session required).
		mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
		mux.HandleFunc("POST /api/v1/auth/logout", s.requireAuth(s.handleLogout))
		mux.HandleFunc("GET /api/v1/auth/me", s.requireAuth(s.handleMe))
		mux.HandleFunc("GET /api/v1/system/info", s.requireAuth(s.handleSystemInfo))
	}

	if s.deps.IngestFunc != nil {
		if s.deps.IngestRequireAuth && s.deps.Auth != nil {
			mux.HandleFunc("POST /api/v1/ingest", s.requireAuth(s.handleIngest))
		} else {
			mux.HandleFunc("POST /api/v1/ingest", s.handleIngest)
		}
	}

	if s.deps.Reader != nil {
		mux.HandleFunc("GET /api/v1/logs/search", s.requireAuth(s.handleSearch))
		mux.HandleFunc("GET /api/v1/logs/count", s.requireAuth(s.handleCount))
		mux.HandleFunc("GET /api/v1/logs/volume", s.requireAuth(s.handleVolume))
		mux.HandleFunc("GET /api/v1/logs/export", s.requireRole(auth.RoleOperator, s.handleExport))
		mux.HandleFunc("GET /api/v1/fields", s.requireAuth(s.handleFields))
		mux.HandleFunc("GET /api/v1/fields/{field}/values", s.requireAuth(s.handleFieldValues))
	}

	// Embedded SPA last: "/" only matches paths no other route claims.
	if s.deps.Web != nil {
		mux.Handle("/", s.deps.Web)
	}

	return mux
}

// Start serves until Stop is called; it returns the listener error.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.cfg.Address)
	if err != nil {
		return err
	}
	s.deps.Log.Info("http server started", "addr", ln.Addr().String())
	if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Stop gracefully drains in-flight HTTP requests.
func (s *Server) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
	defer cancel()
	if err := s.http.Shutdown(ctx); err != nil {
		s.deps.Log.Warn("http shutdown", "error", err)
	}
}

// --- errors (docs/api.md §1: one stable shape) ---

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": apiError{Code: code, Message: message}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// --- middleware ---

type ctxKey int

const sessionKey ctxKey = 1

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
			return
		}
		sess, err := s.deps.Auth.Validate(r.Context(), token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid or expired session")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), sessionKey, sess)))
	}
}

// requireRole wraps requireAuth with an RBAC check (docs/api.md §3).
func (s *Server) requireRole(role string, next http.HandlerFunc) http.HandlerFunc {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		sess := r.Context().Value(sessionKey).(*auth.Session)
		if !roleAtLeast(sess.Role, role) {
			writeError(w, http.StatusForbidden, "forbidden", "insufficient role")
			return
		}
		next(w, r)
	})
}

// roleAtLeast: viewer < operator < admin.
func roleAtLeast(has, needs string) bool {
	rank := map[string]int{auth.RoleViewer: 0, auth.RoleOperator: 1, auth.RoleAdmin: 2}
	return rank[has] >= rank[needs]
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func sessionFrom(r *http.Request) *auth.Session {
	return r.Context().Value(sessionKey).(*auth.Session)
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// --- auth handlers ---

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*1024)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "username and password required")
		return
	}
	sess, err := s.deps.Auth.Authenticate(r.Context(), req.Username, req.Password)
	if err != nil {
		s.deps.Auth.Audit(context.Background(), req.Username, "login_failed", "", remoteIP(r))
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid credentials")
		return
	}
	s.deps.Auth.Audit(context.Background(), sess.Username, "login", "", remoteIP(r))
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      sess.Token,
		"expires_at": sess.ExpiresAt,
		"user":       map[string]any{"username": sess.Username, "role": sess.Role},
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r)
	if err := s.deps.Auth.DeleteSession(r.Context(), bearerToken(r)); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "logout failed")
		return
	}
	s.deps.Auth.Audit(context.Background(), sess.Username, "logout", "", remoteIP(r))
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"username":   sess.Username,
		"role":       sess.Role,
		"expires_at": sess.ExpiresAt,
	})
}

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version": Version,
	})
}
