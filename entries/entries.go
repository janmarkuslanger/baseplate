// Package entries is the application service for writing content. It owns
// the rules of a save (validation, singleton and status handling, slug
// uniqueness, hooks) so the admin, plugins and data migrations behave the
// same. HTTP handlers only parse input, call the service and render.
package entries

import (
	"context"
	"errors"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/hook"
	"github.com/janmarkuslanger/baseplate/i18n"
	"github.com/janmarkuslanger/baseplate/storage"
)

// Event is the payload of the entry hooks.
type Event struct {
	Entry *storage.Entry
	Type  *content.Type
	// UserID is the admin user who saves (0 outside the admin).
	UserID int64
}

var (
	// BeforeSave runs after validation, before the entry is stored. Plugins
	// may change Entry.Data. An error aborts the save and is shown to the
	// editor.
	BeforeSave = hook.NewAction[*Event]("entry.beforeSave", "after validation, before an entry is stored; error aborts the save")
	// AfterSave runs after an entry was stored. Errors are logged.
	AfterSave = hook.NewAction[*Event]("entry.afterSave", "after an entry was stored; errors are logged")
)

// Logger is the subset of *slog.Logger the service uses.
type Logger interface {
	Error(msg string, args ...any)
}

// Service saves and deletes entries.
type Service struct {
	Store  storage.Entries
	Schema *content.Schema
	Hooks  hook.Host
	Logger Logger
}

// Save input. ID 0 creates (singletons update their single entry).
type Save struct {
	Type    string
	ID      int64
	Data    map[string]any
	Publish bool
	UserID  int64
}

// Result of a save. Changed reports whether published content changed
// (the entry is or was published), e.g. to trigger a site build.
type Result struct {
	Entry   *storage.Entry
	Changed bool
}

// ErrNotFound is returned for an unknown type or entry.
var ErrNotFound = errors.New("entries: not found")

// Save validates and stores an entry. Validation, slug conflicts, hook
// refusals and invalid relations are returned as content.Errors (path "_"
// for errors not tied to a field); other errors are internal.
func (s *Service) Save(ctx context.Context, in Save) (*Result, error) {
	t, ok := s.Schema.Type(in.Type)
	if !ok {
		return nil, ErrNotFound
	}
	e, err := s.load(ctx, t, in.ID)
	if err != nil {
		return nil, err
	}
	wasPublished := e.Status == storage.StatusPublished

	clean, err := s.Schema.Validate(t.Fields, in.Data)
	errs, isValidation := errors.AsType[content.Errors](err)
	if err != nil && !isValidation {
		return nil, err
	}
	if errs == nil {
		errs = content.Errors{}
	}

	switch {
	case t.Singleton:
		e.Slug, e.Status = t.Name, storage.StatusPublished
	default:
		e.Slug, _ = clean["slug"].(string)
		e.Status = storage.StatusDraft
		if in.Publish {
			e.Status = storage.StatusPublished
		}
	}
	if t.Routable && e.Slug != "" {
		taken, err := s.slugTaken(ctx, t, e, clean)
		if err != nil {
			return nil, err
		}
		if taken {
			errs["slug"] = i18n.M("validation.slugTaken")
		}
	}
	if len(errs) > 0 {
		return nil, errs
	}

	e.Data = clean
	ev := &Event{Entry: e, Type: t, UserID: in.UserID}
	if err := BeforeSave.Run(ctx, s.Hooks, ev); err != nil {
		return nil, content.Errors{"_": err}
	}
	if e.ID == 0 {
		err = s.Store.CreateEntry(ctx, e)
	} else {
		err = s.Store.UpdateEntry(ctx, e)
	}
	if errors.Is(err, storage.ErrInvalidRelation) {
		return nil, content.Errors{"_": err}
	}
	if err != nil {
		return nil, err
	}
	if err := AfterSave.Run(ctx, s.Hooks, ev); err != nil && s.Logger != nil {
		s.Logger.Error("entry hook", "err", err)
	}
	return &Result{Entry: e, Changed: wasPublished || e.Status == storage.StatusPublished}, nil
}

// Delete removes an entry. changed reports whether it was published.
func (s *Service) Delete(ctx context.Context, typ string, id int64) (changed bool, err error) {
	e, err := s.Store.GetEntry(ctx, typ, id)
	if errors.Is(err, storage.ErrNotFound) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if err := s.Store.DeleteEntry(ctx, typ, id); err != nil {
		return false, err
	}
	return e.Status == storage.StatusPublished, nil
}

// load returns the entry to update, or a new one.
func (s *Service) load(ctx context.Context, t *content.Type, id int64) (*storage.Entry, error) {
	switch {
	case id > 0:
		e, err := s.Store.GetEntry(ctx, t.Name, id)
		if errors.Is(err, storage.ErrNotFound) {
			return nil, ErrNotFound
		}
		return e, err
	case t.Singleton:
		e, err := s.Store.GetEntryBySlug(ctx, t.Name, t.Name)
		if err == nil {
			return e, nil
		}
		if !errors.Is(err, storage.ErrNotFound) {
			return nil, err
		}
	}
	return &storage.Entry{Type: t.Name, Status: storage.StatusDraft}, nil
}

// slugTaken reports a sibling (same type and parent) with the same slug.
func (s *Service) slugTaken(ctx context.Context, t *content.Type, e *storage.Entry, data map[string]any) (bool, error) {
	all, _, err := s.Store.ListEntries(ctx, storage.ListQuery{Type: t.Name})
	if err != nil {
		return false, err
	}
	parent, _ := content.RefID(data["parent"])
	for _, o := range all {
		p, _ := content.RefID(o.Data["parent"])
		if o.ID != e.ID && o.Slug == e.Slug && p == parent {
			return true, nil
		}
	}
	return false, nil
}
