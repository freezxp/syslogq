// Package auth implements users, sessions, and the audit log on SQLite
// (ADR-0004: opaque sessions server-side; bcrypt password hashing). All
// other packages depend on this only through the Service type.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver (pure Go, no cgo)
)

// Roles (RBAC matrix, docs/api.md §3).
const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"
)

var ErrInvalidCredentials = errors.New("invalid credentials")
var ErrUserExists = errors.New("user exists")
var ErrNotFound = errors.New("not found")

// User is one account.
type User struct {
	ID           int64
	Username     string
	PasswordHash string
	Role         string
	CreatedAt    time.Time
}

// Session is a live session; Token is the raw bearer (only shown once at
// login; the DB stores only its SHA-256).
type Session struct {
	Token     string
	UserID    int64
	Username  string
	Role      string
	ExpiresAt time.Time
}

// AuditEvent is one security-relevant action (docs/security.md §10).
type AuditEvent struct {
	Time     time.Time
	Username string
	Action   string
	Detail   string
	RemoteIP string
}

// hashToken converts a raw session token to the stored form.
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// openDB opens (creating if needed) the SQLite database and applies the
// schema. One connection pool, WAL mode.
func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, err
	}
	// modernc/sqlite serializes writes; a single writer avoids SQLITE_BUSY.
	db.SetMaxOpenConns(1)
	schema := `
CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('admin','operator','viewer')),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE TABLE IF NOT EXISTS sessions (
    token_hash TEXT PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_sessions_expiry ON sessions(expires_at);
CREATE TABLE IF NOT EXISTS audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ts TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    username TEXT NOT NULL,
    action TEXT NOT NULL,
    detail TEXT NOT NULL DEFAULT '',
    remote_ip TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit_log(ts);`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
