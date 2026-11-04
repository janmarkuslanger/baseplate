package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// CoreMigrations create the tables baseplate itself needs.
var CoreMigrations = []Migration{
	{
		Name: "0001_core",
		SQL: map[Dialect]string{
			SQLite: `
CREATE TABLE entries (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	type TEXT NOT NULL,
	slug TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'draft',
	locale TEXT NOT NULL DEFAULT '',
	data TEXT NOT NULL DEFAULT '{}',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE INDEX entries_type_slug ON entries(type, slug);
CREATE TABLE users (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	email TEXT NOT NULL UNIQUE,
	name TEXT NOT NULL DEFAULT '',
	password_hash TEXT NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE TABLE sessions (
	token TEXT PRIMARY KEY,
	user_id INTEGER NOT NULL,
	csrf TEXT NOT NULL,
	expires_at INTEGER NOT NULL
);`,
			MySQL: `
CREATE TABLE entries (
	id BIGINT AUTO_INCREMENT PRIMARY KEY,
	type VARCHAR(100) NOT NULL,
	slug VARCHAR(255) NOT NULL DEFAULT '',
	status VARCHAR(20) NOT NULL DEFAULT 'draft',
	locale VARCHAR(20) NOT NULL DEFAULT '',
	data JSON NOT NULL,
	created_at BIGINT NOT NULL,
	updated_at BIGINT NOT NULL,
	INDEX entries_type_slug (type, slug)
);
CREATE TABLE users (
	id BIGINT AUTO_INCREMENT PRIMARY KEY,
	email VARCHAR(255) NOT NULL UNIQUE,
	name VARCHAR(255) NOT NULL DEFAULT '',
	password_hash VARCHAR(255) NOT NULL,
	created_at BIGINT NOT NULL
);
CREATE TABLE sessions (
	token VARCHAR(64) PRIMARY KEY,
	user_id BIGINT NOT NULL,
	csrf VARCHAR(64) NOT NULL,
	expires_at BIGINT NOT NULL
);`,
		},
	},
}

// SQLStore implements Store on top of database/sql. Adapters (sqlite, mysql)
// only open the connection and pick the dialect; queries are shared.
type SQLStore struct {
	db      *sql.DB
	dialect Dialect
	now     func() time.Time
}

func NewSQLStore(db *sql.DB, d Dialect) *SQLStore {
	return &SQLStore{db: db, dialect: d, now: time.Now}
}

func (s *SQLStore) Dialect() Dialect { return s.dialect }
func (s *SQLStore) DB() *sql.DB      { return s.db }
func (s *SQLStore) Close() error     { return s.db.Close() }

func (s *SQLStore) Migrate(ctx context.Context, ms []Migration) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (name VARCHAR(255) PRIMARY KEY, applied_at BIGINT NOT NULL)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	for _, m := range ms {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE name = ?`, m.Name).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		script, ok := m.SQL[s.dialect]
		if !ok {
			return fmt.Errorf("migration %s has no SQL for dialect %s", m.Name, s.dialect)
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for _, stmt := range splitStatements(script) {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %s: %w", m.Name, err)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)`, m.Name, s.now().Unix()); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// splitStatements splits a script on ";" at line ends. Migration scripts
// must not contain semicolons inside string literals.
func splitStatements(script string) []string {
	var out []string
	for _, part := range strings.Split(script, ";") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// --- entries ---

const entryCols = `id, type, slug, status, locale, data, created_at, updated_at`

func (s *SQLStore) CreateEntry(ctx context.Context, e *Entry) error {
	data, err := json.Marshal(nonNil(e.Data))
	if err != nil {
		return err
	}
	now := s.now()
	e.CreatedAt, e.UpdatedAt = now, now
	if e.Status == "" {
		e.Status = StatusDraft
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO entries (type, slug, status, locale, data, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.Type, e.Slug, e.Status, e.Locale, string(data), now.Unix(), now.Unix())
	if err != nil {
		return err
	}
	e.ID, err = res.LastInsertId()
	return err
}

func (s *SQLStore) UpdateEntry(ctx context.Context, e *Entry) error {
	data, err := json.Marshal(nonNil(e.Data))
	if err != nil {
		return err
	}
	e.UpdatedAt = s.now()
	res, err := s.db.ExecContext(ctx,
		`UPDATE entries SET slug = ?, status = ?, locale = ?, data = ?, updated_at = ? WHERE id = ?`,
		e.Slug, e.Status, e.Locale, string(data), e.UpdatedAt.Unix(), e.ID)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

func (s *SQLStore) GetEntry(ctx context.Context, id int64) (*Entry, error) {
	return scanEntry(s.db.QueryRowContext(ctx, `SELECT `+entryCols+` FROM entries WHERE id = ?`, id))
}

func (s *SQLStore) GetEntryBySlug(ctx context.Context, typ, slug string) (*Entry, error) {
	return scanEntry(s.db.QueryRowContext(ctx, `SELECT `+entryCols+` FROM entries WHERE type = ? AND slug = ? ORDER BY id LIMIT 1`, typ, slug))
}

func (s *SQLStore) ListEntries(ctx context.Context, q ListQuery) ([]Entry, int, error) {
	var where []string
	var args []any
	if q.Type != "" {
		where = append(where, "type = ?")
		args = append(args, q.Type)
	}
	if q.Status != "" {
		where = append(where, "status = ?")
		args = append(args, q.Status)
	}
	if q.Search != "" {
		where = append(where, "(slug LIKE ? OR data LIKE ?)")
		like := "%" + q.Search + "%"
		args = append(args, like, like)
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries`+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	query := `SELECT ` + entryCols + ` FROM entries` + cond + ` ORDER BY updated_at DESC, id DESC`
	if q.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d OFFSET %d", q.Limit, q.Offset)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *e)
	}
	return out, total, rows.Err()
}

func (s *SQLStore) DeleteEntry(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM entries WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

type scanner interface{ Scan(...any) error }

func scanEntry(r scanner) (*Entry, error) {
	var e Entry
	var data string
	var created, updated int64
	err := r.Scan(&e.ID, &e.Type, &e.Slug, &e.Status, &e.Locale, &data, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(data), &e.Data); err != nil {
		return nil, fmt.Errorf("entry %d: decode data: %w", e.ID, err)
	}
	e.Data = nonNil(e.Data)
	e.CreatedAt, e.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
	return &e, nil
}

// --- users ---

func (s *SQLStore) CreateUser(ctx context.Context, u *User) error {
	u.CreatedAt = s.now()
	res, err := s.db.ExecContext(ctx, `INSERT INTO users (email, name, password_hash, created_at) VALUES (?, ?, ?, ?)`,
		strings.ToLower(u.Email), u.Name, u.PasswordHash, u.CreatedAt.Unix())
	if err != nil {
		return err
	}
	u.ID, err = res.LastInsertId()
	return err
}

func (s *SQLStore) GetUser(ctx context.Context, id int64) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT id, email, name, password_hash, created_at FROM users WHERE id = ?`, id))
}

func (s *SQLStore) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT id, email, name, password_hash, created_at FROM users WHERE email = ?`, strings.ToLower(email)))
}

func (s *SQLStore) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func scanUser(r scanner) (*User, error) {
	var u User
	var created int64
	err := r.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.CreatedAt = time.Unix(created, 0)
	return &u, nil
}

// --- sessions ---

func (s *SQLStore) CreateSession(ctx context.Context, ses *Session) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (token, user_id, csrf, expires_at) VALUES (?, ?, ?, ?)`,
		ses.Token, ses.UserID, ses.CSRF, ses.ExpiresAt.Unix())
	return err
}

// GetSession returns ErrNotFound for unknown and expired sessions alike.
func (s *SQLStore) GetSession(ctx context.Context, token string) (*Session, error) {
	var ses Session
	var exp int64
	err := s.db.QueryRowContext(ctx, `SELECT token, user_id, csrf, expires_at FROM sessions WHERE token = ?`, token).
		Scan(&ses.Token, &ses.UserID, &ses.CSRF, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	ses.ExpiresAt = time.Unix(exp, 0)
	if s.now().After(ses.ExpiresAt) {
		s.DeleteSession(ctx, token)
		return nil, ErrNotFound
	}
	return &ses, nil
}

func (s *SQLStore) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, token)
	return err
}

func mustAffect(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func nonNil(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
