// Package storage defines the persistence contract of baseplate.
//
// Content is stored as generic entries whose fields live in a JSON document;
// the schema is defined in code (see package content) and validated before
// an entry reaches the store.
package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("storage: not found")

// Dialect identifies the SQL flavour of a store.
type Dialect string

const (
	SQLite Dialect = "sqlite"
	MySQL  Dialect = "mysql"
)

// Migration is a named schema change. SQL holds one script per dialect;
// a store runs the script matching its own dialect.
type Migration struct {
	Name string
	SQL  map[Dialect]string
}

// Entry statuses.
const (
	StatusDraft     = "draft"
	StatusPublished = "published"
)

// Entry is one content item of a content type.
type Entry struct {
	ID        int64
	Type      string
	Slug      string
	Status    string
	Locale    string
	Data      map[string]any
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ListQuery filters ListEntries. Zero values mean "no filter".
type ListQuery struct {
	Type   string
	Status string
	Search string
	Limit  int
	Offset int
}

type User struct {
	ID           int64
	Email        string
	Name         string
	PasswordHash string
	CreatedAt    time.Time
}

type Session struct {
	Token     string
	UserID    int64
	CSRF      string
	ExpiresAt time.Time
}

// Store is implemented by every storage adapter.
type Store interface {
	Dialect() Dialect
	// DB exposes the connection for plugins that own tables.
	DB() *sql.DB
	// Migrate applies all migrations that have not run yet, in order.
	Migrate(ctx context.Context, ms []Migration) error
	Close() error

	CreateEntry(ctx context.Context, e *Entry) error
	UpdateEntry(ctx context.Context, e *Entry) error
	GetEntry(ctx context.Context, id int64) (*Entry, error)
	GetEntryBySlug(ctx context.Context, typ, slug string) (*Entry, error)
	// ListEntries returns one page of matching entries and the total count.
	ListEntries(ctx context.Context, q ListQuery) ([]Entry, int, error)
	DeleteEntry(ctx context.Context, id int64) error

	CreateUser(ctx context.Context, u *User) error
	GetUser(ctx context.Context, id int64) (*User, error)
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	CountUsers(ctx context.Context) (int, error)

	CreateSession(ctx context.Context, s *Session) error
	GetSession(ctx context.Context, token string) (*Session, error)
	DeleteSession(ctx context.Context, token string) error
}
