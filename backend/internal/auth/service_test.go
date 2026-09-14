package auth

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "test.db"), "s3cret-pass", time.Hour)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestBootstrapAdmin(t *testing.T) {
	s := newTestService(t)
	has, err := s.HasUsers(context.Background())
	if err != nil || !has {
		t.Fatalf("bootstrap: %v %v", has, err)
	}
	sess, err := s.Authenticate(context.Background(), "admin", "s3cret-pass")
	if err != nil {
		t.Fatalf("admin login: %v", err)
	}
	if sess.Username != "admin" || sess.Role != RoleAdmin {
		t.Fatalf("session: %+v", sess)
	}
}

func TestBootstrapOnlyWhenEmpty(t *testing.T) {
	// A fresh DB bootstraps its own admin; an existing one is untouched.
	s2, err := New(filepath.Join(t.TempDir(), "fresh.db"), "other-pass", time.Hour)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s2.Close()
	if _, err := s2.Authenticate(context.Background(), "admin", "other-pass"); err != nil {
		t.Fatalf("fresh db login: %v", err)
	}
}

func TestWrongPassword(t *testing.T) {
	s := newTestService(t)
	if _, err := s.Authenticate(context.Background(), "admin", "wrong"); err != ErrInvalidCredentials {
		t.Fatalf("want ErrInvalidCredentials, got %v", err)
	}
	if _, err := s.Authenticate(context.Background(), "ghost", "whatever"); err != ErrInvalidCredentials {
		t.Fatalf("missing user: %v", err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	s := newTestService(t)
	sess, err := s.Authenticate(context.Background(), "admin", "s3cret-pass")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	got, err := s.Validate(context.Background(), sess.Token)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got.Username != "admin" || got.Role != RoleAdmin {
		t.Fatalf("validated session: %+v", got)
	}
	if err := s.DeleteSession(context.Background(), sess.Token); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := s.Validate(context.Background(), sess.Token); err != ErrInvalidCredentials {
		t.Fatalf("token must be dead after logout: %v", err)
	}
}

func TestSessionExpiry(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "exp.db"), "pw", time.Hour)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()
	sess, err := s.Authenticate(context.Background(), "admin", "pw")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	// Force the stored expiry into the past, then validate.
	if _, err := s.db.Exec(`UPDATE sessions SET expires_at = ? WHERE token_hash = ?`,
		time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano), hashToken(sess.Token)); err != nil {
		t.Fatalf("age session: %v", err)
	}
	if _, err := s.Validate(context.Background(), sess.Token); err != ErrInvalidCredentials {
		t.Fatalf("expired token must be rejected: %v", err)
	}
}

func TestAuditTrail(t *testing.T) {
	s := newTestService(t)
	s.Audit(context.Background(), "admin", "login", "", "10.0.0.1")
	s.Audit(context.Background(), "admin", "export", "5000 rows", "10.0.0.1")
	events, err := s.RecentAudit(context.Background(), 10)
	if err != nil {
		t.Fatalf("recent audit: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("audit events: %d", len(events))
	}
	// newest first
	if events[0].Action != "export" || events[0].Detail != "5000 rows" {
		t.Fatalf("newest event: %+v", events[0])
	}
	if events[1].RemoteIP != "10.0.0.1" {
		t.Fatalf("oldest event ip: %+v", events[1])
	}
}
