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
	"fmt"
	"time"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/i18n"
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("storage: not found")

// ErrInvalidRelation is returned when a relation points to a missing entry.
var ErrInvalidRelation = i18n.Key("validation.relationTarget")

// ReferencedError is returned by DeleteEntry while relations of other
// entries point at the entry. Referrers lists them; removing those
// relations allows the delete.
type ReferencedError struct {
	Type      string
	ID        int64
	Referrers []Referrer
}

func (e *ReferencedError) Error() string {
	return fmt.Sprintf("storage: %s #%d is referenced by %d relation(s)", e.Type, e.ID, len(e.Referrers))
}

// Dialect identifies the SQL flavour of a store. baseplate ships SQLite; an
// adapter for another database adds its own value (see ARCHITECTURE.md,
// "Databases").
type Dialect string

const SQLite Dialect = "sqlite"

// Migration is a named schema change. SQL holds one script per dialect;
// a store runs the script matching its own dialect.
type Migration struct {
	Name string
	SQL  map[Dialect]string
}

// Version is one of the two rows an entry can have per locale: the draft
// editors work on and the published copy the site is built from.
type Version string

const (
	Draft     Version = "draft"
	Published Version = "published"
)

// Entry is one version of a content item in one locale. ID is the
// document ID, shared by all locales and versions of the item; relations
// point at it. Data holds field values: scalars, maps for groups, lists of
// maps for lists and blocks (block items carry "_type"), int64 document
// IDs for single relations and []any of them for many.
type Entry struct {
	ID      int64
	Type    string
	Slug    string
	Locale  string
	Version Version
	// PublishedAt is when the entry was last published in its locale; zero
	// if it is not published.
	PublishedAt time.Time
	// Changed is set on drafts saved since they were last published (or
	// never published).
	Changed   bool
	Data      map[string]any
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Published reports whether the entry has a published version.
func (e *Entry) Published() bool { return !e.PublishedAt.IsZero() }

// ListQuery filters ListEntries. Type and Version are required; Locale is
// matched exactly ("" is the site language). Other zero values mean "no
// filter".
type ListQuery struct {
	Type      string
	Version   Version
	Locale    string
	Published PublishedFilter
	Search    string
	Limit     int
	Offset    int
}

// PublishedFilter keeps entries by whether they have a published version
// in their locale; useful on drafts.
type PublishedFilter uint8

const (
	AnyPublished PublishedFilter = iota
	OnlyPublished
	OnlyUnpublished
)

// Referrer is a document whose relation field points at another one. Field
// is the relation field; Nested is true when the field sits inside a
// component (group, list item or block) of the entry; Block names the block
// that holds the field, if any.
type Referrer struct {
	Type   string
	ID     int64
	Field  string
	Nested bool
	Block  string
}

// User is an admin user.
type User struct {
	ID           int64
	Email        string
	Name         string
	PasswordHash string
	CreatedAt    time.Time
}

// Session is a logged-in admin session.
type Session struct {
	Token     string
	UserID    int64
	CSRF      string
	ExpiresAt time.Time
}

// Store is implemented by every storage adapter (SQLStore). Consumers that
// need only part of it should depend on the smaller interfaces.
type Store interface {
	Tables
	Entries
	Users
	Sessions
}

// Tables manages tables and the connection.
type Tables interface {
	Dialect() Dialect
	// DB exposes the connection, for plugins that own tables
	// (see Registry.Migration). Content goes through Entries.
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
}

// Entries stores content. Plugins write through entries.Service, which
// adds validation and hooks on top. Editing happens on the draft; Publish
// copies it to the published version the site is built from.
type Entries interface {
	// SaveDraft creates or updates the draft of e.Locale. ID 0 creates a
	// new document and sets e.ID; an ID of an existing document without a
	// draft in e.Locale adds that locale.
	SaveDraft(ctx context.Context, e *Entry) error
	// Publish copies the draft to the published version, replacing it.
	Publish(ctx context.Context, typ string, id int64, locale string) error
	// Unpublish removes the published version; the draft stays.
	Unpublish(ctx context.Context, typ string, id int64, locale string) error
	GetEntry(ctx context.Context, typ string, id int64, locale string, v Version) (*Entry, error)
	GetEntryBySlug(ctx context.Context, typ, slug, locale string, v Version) (*Entry, error)
	// ListEntries returns one page of matching entries and the total count.
	ListEntries(ctx context.Context, q ListQuery) ([]Entry, int, error)
	// DeleteEntry removes the document (all locales and versions), its
	// components and its relations. It fails with *ReferencedError while
	// other entries point at it.
	DeleteEntry(ctx context.Context, typ string, id int64) error
	// Referrers lists other documents whose relation fields (as the schema
	// declares them, in any version) point at the given document.
	Referrers(ctx context.Context, typ string, id int64) ([]Referrer, error)
	// Atomic runs fn in one write transaction on a view of the store bound
	// to it, committed if fn returns nil. Atomic calls must be serialized
	// (also across processes sharing the database): what fn reads stays
	// true until its writes are committed. entries.Service relies on this
	// for slug and singleton uniqueness. fn must use only the given view.
	Atomic(ctx context.Context, fn func(Entries) error) error
}

// Users stores admin users.
type Users interface {
	CreateUser(ctx context.Context, u *User) error
	GetUser(ctx context.Context, id int64) (*User, error)
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	CountUsers(ctx context.Context) (int, error)
}

// Sessions stores admin sessions.
type Sessions interface {
	CreateSession(ctx context.Context, s *Session) error
	GetSession(ctx context.Context, token string) (*Session, error)
	DeleteSession(ctx context.Context, token string) error
}

var _ Store = (*SQLStore)(nil)
