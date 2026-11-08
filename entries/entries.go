// Package entries is the application service for writing content. It owns
// the rules of a save (validation, singletons, publishing, slug
// uniqueness, hooks) so the admin, plugins and data migrations behave the
// same. HTTP handlers only parse input, call the service and render.
//
// Saves edit the draft; publishing copies it to the published version the
// site is built from, so a published entry can be edited without changing
// the site until it is published again.
package entries

import (
	"context"
	"errors"
	"time"

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
	// PublishedChanged runs after published content changed: a publish, an
	// unpublish or the delete of a published entry, whoever made it: the
	// admin or a plugin through plugin.Services.Entries. Errors are logged.
	PublishedChanged = hook.NewAction[*Event]("entry.publishedChanged", "after a publish, unpublish or delete of a published entry; errors are logged")
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

// Save input. ID 0 creates (singletons update their single entry). Locale
// "" is the site language. Publish also publishes the saved draft.
type Save struct {
	Type    string
	ID      int64
	Locale  string
	Data    map[string]any
	Publish bool
	UserID  int64
}

// Result of a save. Entry is the saved draft. Changed reports whether
// published content changed (the save published), e.g. to trigger a site
// build.
type Result struct {
	Entry   *storage.Entry
	Changed bool
}

// ErrNotFound is returned for an unknown type or entry.
var ErrNotFound = errors.New("entries: not found")

// Save validates and stores the draft of an entry and, with Publish,
// publishes it. Validation, slug conflicts, hook refusals and invalid
// relations are returned as content.Errors (path "_" for errors not tied
// to a field); other errors are internal.
func (s *Service) Save(ctx context.Context, in Save) (*Result, error) {
	t, ok := s.Schema.Type(in.Type)
	if !ok {
		return nil, ErrNotFound
	}
	e, err := s.load(ctx, t, in.ID, in.Locale)
	if err != nil {
		return nil, err
	}

	clean, err := s.Schema.Validate(t.Fields, in.Data)
	errs, isValidation := errors.AsType[content.Errors](err)
	if err != nil && !isValidation {
		return nil, err
	}
	if len(errs) > 0 {
		return nil, errs
	}

	if t.Singleton {
		e.Slug = t.Name
	} else {
		e.Slug, _ = clean["slug"].(string)
	}
	e.Data = clean
	ev := &Event{Entry: e, Type: t, UserID: in.UserID}
	if err := BeforeSave.Run(ctx, s.Hooks, ev); err != nil {
		return nil, content.Errors{"_": err}
	}
	// The uniqueness checks and the writes share one transaction, so
	// concurrent saves cannot both pass the check. Hooks run before it:
	// they may use the store, which the transaction holds.
	err = s.Store.Atomic(ctx, func(st storage.Entries) error {
		if t.Singleton && e.ID == 0 {
			cur, err := st.GetEntryBySlug(ctx, t.Name, t.Name, e.Locale, storage.Draft)
			switch {
			case err == nil:
				e.ID, e.CreatedAt = cur.ID, cur.CreatedAt
			case !errors.Is(err, storage.ErrNotFound):
				return err
			}
		}
		versions := []storage.Version{storage.Draft}
		if in.Publish {
			versions = append(versions, storage.Published)
		}
		for _, v := range versions {
			if err := checkSlug(ctx, st, t, e, v); err != nil {
				return err
			}
		}
		if err := st.SaveDraft(ctx, e); err != nil {
			return err
		}
		if in.Publish {
			return st.Publish(ctx, t.Name, e.ID, e.Locale)
		}
		return nil
	})
	if _, ok := errors.AsType[content.Errors](err); ok {
		return nil, err
	}
	if errors.Is(err, storage.ErrInvalidRelation) {
		return nil, content.Errors{"_": err}
	}
	if err != nil {
		return nil, err
	}
	// Reload for what the store sets: PublishedAt and Changed.
	saved, err := s.Store.GetEntry(ctx, t.Name, e.ID, e.Locale, storage.Draft)
	if err != nil {
		return nil, err
	}
	ev.Entry = saved
	if err := AfterSave.Run(ctx, s.Hooks, ev); err != nil && s.Logger != nil {
		s.Logger.Error("entry hook", "err", err)
	}
	if in.Publish {
		s.publishedChanged(ctx, ev)
	}
	return &Result{Entry: saved, Changed: in.Publish}, nil
}

// Unpublish removes the published version of an entry; the draft stays.
// changed reports whether it was published.
func (s *Service) Unpublish(ctx context.Context, typ string, id int64, locale string) (changed bool, err error) {
	t, ok := s.Schema.Type(typ)
	if !ok {
		return false, ErrNotFound
	}
	e, err := s.Store.GetEntry(ctx, typ, id, locale, storage.Draft)
	if errors.Is(err, storage.ErrNotFound) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if !e.Published() {
		return false, nil
	}
	if err := s.Store.Unpublish(ctx, typ, id, locale); err != nil {
		return false, err
	}
	e.PublishedAt = time.Time{}
	s.publishedChanged(ctx, &Event{Entry: e, Type: t})
	return true, nil
}

func (s *Service) publishedChanged(ctx context.Context, ev *Event) {
	if err := PublishedChanged.Run(ctx, s.Hooks, ev); err != nil && s.Logger != nil {
		s.Logger.Error("entry hook", "err", err)
	}
}

// Delete removes an entry with all its versions. changed reports whether
// it was published. The event carries the draft of the site language. An
// entry other entries still point at is not deleted; the error is a
// *storage.ReferencedError listing them.
func (s *Service) Delete(ctx context.Context, typ string, id int64) (changed bool, err error) {
	e, err := s.Store.GetEntry(ctx, typ, id, "", storage.Draft)
	if errors.Is(err, storage.ErrNotFound) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if err := s.Store.DeleteEntry(ctx, typ, id); err != nil {
		return false, err
	}
	changed = e.Published()
	if changed {
		t, _ := s.Schema.Type(typ)
		s.publishedChanged(ctx, &Event{Entry: e, Type: t})
	}
	return changed, nil
}

// load returns the draft to update, or a new one.
func (s *Service) load(ctx context.Context, t *content.Type, id int64, locale string) (*storage.Entry, error) {
	switch {
	case id > 0:
		e, err := s.Store.GetEntry(ctx, t.Name, id, locale, storage.Draft)
		if errors.Is(err, storage.ErrNotFound) {
			return nil, ErrNotFound
		}
		return e, err
	case t.Singleton:
		e, err := s.Store.GetEntryBySlug(ctx, t.Name, t.Name, locale, storage.Draft)
		if err == nil {
			return e, nil
		}
		if !errors.Is(err, storage.ErrNotFound) {
			return nil, err
		}
	}
	return &storage.Entry{Type: t.Name, Locale: locale, Version: storage.Draft}, nil
}

// checkSlug refuses a slug a sibling (same type, locale and parent) uses
// in version v.
func checkSlug(ctx context.Context, st storage.Entries, t *content.Type, e *storage.Entry, v storage.Version) error {
	if !t.Routable || e.Slug == "" {
		return nil
	}
	all, _, err := st.ListEntries(ctx, storage.ListQuery{Type: t.Name, Version: v, Locale: e.Locale})
	if err != nil {
		return err
	}
	parent, _ := content.RefID(e.Data["parent"])
	for _, o := range all {
		p, _ := content.RefID(o.Data["parent"])
		if o.ID != e.ID && o.Slug == e.Slug && p == parent {
			return content.Errors{"slug": i18n.M("validation.slugTaken")}
		}
	}
	return nil
}
