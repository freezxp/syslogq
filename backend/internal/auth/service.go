package auth

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Service is the auth store: users, sessions, audit. All methods are safe
// for concurrent use (single SQLite writer).
type Service struct {
	db         *sql.DB
	sessionTTL time.Duration
}

// New opens the database at path (created if missing) and returns the
// service. bootstrap, when non-empty, creates the admin user with that
// password if no users exist.
func New(path, bootstrapAdminPassword string, sessionTTL time.Duration) (*Service, error) {
	if sessionTTL <= 0 {
		sessionTTL = 24 * time.Hour
	}
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}
	s := &Service{db: db, sessionTTL: sessionTTL}
	if bootstrapAdminPassword != "" {
		if err := s.bootstrapAdmin(bootstrapAdminPassword); err != nil {
			db.Close()
			return nil, err
		}
	}
	return s, nil
}

func (s *Service) Close() error { return s.db.Close() }

// bootstrapAdmin creates the initial admin account when the users table is
// empty (first boot; password always supplied via env per security.md §7).
func (s *Service) bootstrapAdmin(password string) error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO users (username, password_hash, role) VALUES ('admin', ?, 'admin')`, string(hash))
	return err
}

// Authenticate verifies username+password and issues a session.
func (s *Service) Authenticate(ctx context.Context, username, password string) (*Session, error) {
	var u User
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash, role FROM users WHERE username = ?`, username,
	).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Burn comparable time so missing users aren't distinguishable.
			_ = bcrypt.CompareHashAndPassword(
				[]byte("$2a$10$7EqJtq98hPqEX7fNZaFWoOhi5B0X0PYmVlSkC0pR0uW9Qy3mO0Fpi"), []byte(password))
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return nil, ErrInvalidCredentials
	}
	return s.createSession(ctx, u)
}

func (s *Service) createSession(ctx context.Context, u User) (*Session, error) {
	token, err := newToken()
	if err != nil {
		return nil, err
	}
	expires := time.Now().UTC().Add(s.sessionTTL)
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES (?, ?, ?)`,
		hashToken(token), u.ID, expires.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	return &Session{Token: token, UserID: u.ID, Username: u.Username, Role: u.Role, ExpiresAt: expires}, nil
}

// Validate resolves a bearer token to its session, checking expiry.
func (s *Service) Validate(ctx context.Context, token string) (*Session, error) {
	var sess Session
	var expires string
	err := s.db.QueryRowContext(ctx, `
SELECT s.token_hash, s.expires_at, u.id, u.username, u.role
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.token_hash = ?`, hashToken(token),
	).Scan(&sess.Token, &expires, &sess.UserID, &sess.Username, &sess.Role)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	sess.Token = token
	exp, err := time.Parse(time.RFC3339Nano, expires)
	if err != nil || time.Now().UTC().After(exp) {
		_ = s.DeleteSession(context.Background(), token)
		return nil, ErrInvalidCredentials
	}
	sess.ExpiresAt = exp
	return &sess, nil
}

// DeleteSession removes one session (logout).
func (s *Service) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hashToken(token))
	return err
}

// Audit records a security-relevant event.
func (s *Service) Audit(ctx context.Context, username, action, detail, remoteIP string) {
	_, _ = s.db.ExecContext(ctx,
		`INSERT INTO audit_log (username, action, detail, remote_ip) VALUES (?, ?, ?, ?)`,
		username, action, detail, remoteIP)
}

// RecentAudit returns the newest n audit events.
func (s *Service) RecentAudit(ctx context.Context, n int) ([]AuditEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT ts, username, action, detail, remote_ip FROM audit_log ORDER BY id DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEvent
	for rows.Next() {
		var e AuditEvent
		var ts string
		if err := rows.Scan(&ts, &e.Username, &e.Action, &e.Detail, &e.RemoteIP); err != nil {
			return nil, err
		}
		if e.Time, err = time.Parse(time.RFC3339Nano, ts); err != nil {
			e.Time = time.Time{}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// HasUsers reports whether any account exists (login setup state).
func (s *Service) HasUsers(ctx context.Context) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n > 0, err
}

// Usernames lists accounts for the admin settings page.
func (s *Service) Usernames(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, username, role, created_at FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		var created string
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &created); err != nil {
			return nil, err
		}
		u.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, u)
	}
	return out, rows.Err()
}
