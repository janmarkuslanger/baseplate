package entries_test

import (
	"context"
	"errors"
	"testing"

	"github.com/janmarkuslanger/baseplate/entries"
	"github.com/janmarkuslanger/baseplate/storage"
)

// Saving an existing entry in a locale it has no draft in yet adds that
// locale to the document, as storage.Entries.SaveDraft does.
func TestSaveAddsLocale(t *testing.T) {
	svc, reg := service(t)
	ctx := context.Background()
	de, err := svc.Save(ctx, entries.Save{Type: "page", Data: page("ueber", 0), Publish: true})
	if err != nil {
		t.Fatal(err)
	}
	var hooked []*storage.Entry
	entries.BeforeSave.On(reg, func(_ context.Context, e *entries.Event) error {
		hooked = append(hooked, e.Entry)
		return nil
	})

	en, err := svc.Save(ctx, entries.Save{Type: "page", ID: de.Entry.ID, Locale: "en", Data: page("about", 0), Publish: true})
	if err != nil {
		t.Fatalf("add locale: %v", err)
	}
	if en.Entry.ID != de.Entry.ID || en.Entry.Locale != "en" || en.Entry.Slug != "about" || !en.Entry.Published() {
		t.Errorf("new locale: %+v", en.Entry)
	}
	if len(hooked) != 1 || hooked[0].ID != de.Entry.ID || hooked[0].Locale != "en" {
		t.Errorf("beforeSave saw %+v", hooked)
	}
	if !en.Entry.CreatedAt.Equal(de.Entry.CreatedAt) {
		t.Errorf("new locale created %v, document %v", en.Entry.CreatedAt, de.Entry.CreatedAt)
	}
	orig, err := reg.Store.GetEntry(ctx, "page", de.Entry.ID, "", storage.Published)
	if err != nil || orig.Slug != "ueber" {
		t.Errorf("site language changed: %+v %v", orig, err)
	}

	if _, err := svc.Save(ctx, entries.Save{Type: "page", ID: de.Entry.ID + 100, Locale: "en", Data: page("x", 0)}); !errors.Is(err, entries.ErrNotFound) {
		t.Errorf("locale of an unknown entry: %v", err)
	}
	if _, err := svc.Save(ctx, entries.Save{Type: "page", ID: de.Entry.ID + 100, Data: page("x", 0)}); !errors.Is(err, entries.ErrNotFound) {
		t.Errorf("unknown entry: %v", err)
	}
}
