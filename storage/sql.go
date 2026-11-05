package storage

import (
	"context"
	"database/sql"
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
	{
		Name: "0002_links",
		SQL: map[Dialect]string{
			SQLite: `
CREATE TABLE components (
	owner_table TEXT NOT NULL,
	owner_id INTEGER NOT NULL,
	field TEXT NOT NULL,
	component_table TEXT NOT NULL,
	component_id INTEGER NOT NULL,
	position INTEGER NOT NULL
);
CREATE INDEX components_owner ON components(owner_table, owner_id);
CREATE INDEX components_child ON components(component_table, component_id);
CREATE TABLE relations (
	owner_table TEXT NOT NULL,
	owner_id INTEGER NOT NULL,
	field TEXT NOT NULL,
	target_type TEXT NOT NULL,
	target_id INTEGER NOT NULL,
	position INTEGER NOT NULL
);
CREATE INDEX relations_owner ON relations(owner_table, owner_id);
CREATE INDEX relations_target ON relations(target_type, target_id)`,
			MySQL: `
CREATE TABLE components (
	owner_table VARCHAR(64) NOT NULL,
	owner_id BIGINT NOT NULL,
	field VARCHAR(64) NOT NULL,
	component_table VARCHAR(64) NOT NULL,
	component_id BIGINT NOT NULL,
	position INT NOT NULL,
	INDEX components_owner (owner_table, owner_id),
	INDEX components_child (component_table, component_id)
);
CREATE TABLE relations (
	owner_table VARCHAR(64) NOT NULL,
	owner_id BIGINT NOT NULL,
	field VARCHAR(64) NOT NULL,
	target_type VARCHAR(64) NOT NULL,
	target_id BIGINT NOT NULL,
	position INT NOT NULL,
	INDEX relations_owner (owner_table, owner_id),
	INDEX relations_target (target_type, target_id)
)`,
		},
	},
}

// SQLStore implements Store on top of database/sql. Adapters (sqlite, mysql)
// only open the connection and pick the dialect; queries are shared.
type SQLStore struct {
	db      *sql.DB
	dialect Dialect
	now     func() time.Time
	layout  *layout
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

func (s *SQLStore) RunOnce(ctx context.Context, name string, fn func(ctx context.Context) error) error {
	key := "data:" + name
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE name = ?`, key).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if err := fn(ctx); err != nil {
		return fmt.Errorf("data migration %s: %w", name, err)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)`, key, s.now().Unix())
	return err
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

type scanner interface{ Scan(...any) error }
