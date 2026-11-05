// Package storage defines the persistence contract of baseplate.
//
// Content is stored relationally: one table per content type, component
// tables for groups, lists and blocks, and link tables for components and
// relations (see schema.go). Tables follow the schema defined in code; they
// are created and extended automatically, never dropped.
package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/janmarkuslanger/baseplate/content"
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("storage: not found")

// ErrInvalidRelation is returned when a relation points to a missing entry.
var ErrInvalidRelation = errors.New("Verknüpfung zeigt auf einen nicht vorhandenen Eintrag")

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

// Entry is one content item of a content type. Data holds field values:
// scalars, maps for groups, lists of maps for lists and blocks (block items
// carry "_type"), int64 for single relations and []any of int64 for many.
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

// ListQuery filters ListEntries. Type is required; other zero values mean
// "no filter".
type ListQuery struct {
	Type   string
	Status string
	Search string
	Limit  int
	Offset int
}

// Referrer is an entry whose relation field points at another entry. Field
// is the relation field; Nested is true when the field sits inside a
// component (group, list item or block) of the entry.
type Referrer struct {
	Type   string
	ID     int64
	Field  string
	Nested bool
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
	// UseSchema creates missing tables and columns for the content schema.
	// It must be called before any entry method.
	UseSchema(ctx context.Context, s *content.Schema) error
	// RunOnce runs fn unless a run with this name was recorded before; the
	// run is recorded only if fn succeeds. Used for data migrations.
	RunOnce(ctx context.Context, name string, fn func(ctx context.Context) error) error
	Close() error

	CreateEntry(ctx context.Context, e *Entry) error
	UpdateEntry(ctx context.Context, e *Entry) error
	GetEntry(ctx context.Context, typ string, id int64) (*Entry, error)
	GetEntryBySlug(ctx context.Context, typ, slug string) (*Entry, error)
	// ListEntries returns one page of matching entries and the total count.
	ListEntries(ctx context.Context, q ListQuery) ([]Entry, int, error)
	// DeleteEntry removes the entry, its components and every relation from
	// or to it.
	DeleteEntry(ctx context.Context, typ string, id int64) error
	// Referrers lists entries with relations pointing at the given entry.
	Referrers(ctx context.Context, typ string, id int64) ([]Referrer, error)

	CreateUser(ctx context.Context, u *User) error
	GetUser(ctx context.Context, id int64) (*User, error)
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	CountUsers(ctx context.Context) (int, error)

	CreateSession(ctx context.Context, s *Session) error
	GetSession(ctx context.Context, token string) (*Session, error)
	DeleteSession(ctx context.Context, token string) error
}
