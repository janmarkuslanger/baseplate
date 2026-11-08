package entries_test

import (
	"context"
	"errors"
	"testing"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/entries"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/storage"
	"github.com/janmarkuslanger/baseplate/storage/sqlite"
)

func service(t *testing.T) (*entries.Service, *plugin.Registry) {
	t.Helper()
	st, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	st.Migrate(ctx, storage.CoreMigrations)
	reg := plugin.NewRegistry(plugin.Services{Store: st})
	if err := reg.Use(fields.Core{}); err != nil {
		t.Fatal(err)
	}
	if err := st.UseSchema(ctx, reg.Schema); err != nil {
		t.Fatal(err)
	}
	return reg.Entries, reg
}

func page(slug string, parent int64) map[string]any {
	d := map[string]any{"title": "T " + slug, "slug": slug}
	if parent > 0 {
		d["parent"] = parent
	}
	return d
}

func TestSaveRules(t *testing.T) {
	svc, _ := service(t)
	ctx := context.Background()

	res, err := svc.Save(ctx, entries.Save{Type: "page", Data: page("team", 0)})
	if err != nil || res.Entry.Published() || res.Changed {
		t.Fatalf("draft: %+v %v", res, err)
	}
	draftID := res.Entry.ID

	about, err := svc.Save(ctx, entries.Save{Type: "page", Data: page("about", 0), Publish: true})
	if err != nil || !about.Changed || about.Entry.Slug != "about" || !about.Entry.Published() || about.Entry.Changed {
		t.Fatalf("publish: %+v %v", about, err)
	}

	var errs content.Errors
	_, err = svc.Save(ctx, entries.Save{Type: "page", Data: page("team", 0)})
	if !errors.As(err, &errs) || errs["slug"] == nil {
		t.Errorf("same slug, same parent must conflict: %v", err)
	}
	if _, err := svc.Save(ctx, entries.Save{Type: "page", Data: page("team", about.Entry.ID)}); err != nil {
		t.Errorf("same slug under another parent is fine: %v", err)
	}
	if _, err := svc.Save(ctx, entries.Save{Type: "page", ID: draftID, Data: page("team", 0)}); err != nil {
		t.Errorf("an entry does not conflict with itself: %v", err)
	}

	_, err = svc.Save(ctx, entries.Save{Type: "page", Data: map[string]any{"slug": "x"}})
	if !errors.As(err, &errs) || errs["title"] == nil {
		t.Errorf("validation errors: %v", err)
	}
	_, err = svc.Save(ctx, entries.Save{Type: "page", Data: page("orphan", 999)})
	if !errors.As(err, &errs) || !errors.Is(errs["_"], storage.ErrInvalidRelation) {
		t.Errorf("invalid relation: %v", err)
	}

	edit, err := svc.Save(ctx, entries.Save{Type: "page", ID: about.Entry.ID, Data: page("about", 0)})
	if err != nil || !edit.Entry.Published() || edit.Changed {
		t.Errorf("saving the draft of a published entry keeps it published: %+v %v", edit, err)
	}
	if _, err := svc.Save(ctx, entries.Save{Type: "nope"}); !errors.Is(err, entries.ErrNotFound) {
		t.Errorf("unknown type: %v", err)
	}
	if _, err := svc.Save(ctx, entries.Save{Type: "page", ID: 12345, Data: page("x", 0)}); !errors.Is(err, entries.ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}
}

func TestSingleton(t *testing.T) {
	svc, reg := service(t)
	ctx := context.Background()
	for _, name := range []string{"Erst", "Dann"} {
		if _, err := svc.Save(ctx, entries.Save{Type: "settings", Data: map[string]any{"siteName": name, "baseURL": "https://a.test"}, Publish: name == "Erst"}); err != nil {
			t.Fatal(err)
		}
	}
	list, total, _ := reg.Store.ListEntries(ctx, storage.ListQuery{Type: "settings", Version: storage.Draft})
	if total != 1 || list[0].Data["siteName"] != "Dann" || !list[0].Changed {
		t.Errorf("singleton draft: %d %+v", total, list)
	}
	pub, err := reg.Store.GetEntryBySlug(ctx, "settings", "settings", "", storage.Published)
	if err != nil || pub.Data["siteName"] != "Erst" {
		t.Errorf("singleton published: %+v %v", pub, err)
	}
}

// A data migration or plugin saving through the service gets the hooks.
func TestHooksRunForEveryCaller(t *testing.T) {
	svc, reg := service(t)
	ctx := context.Background()
	var after []int64
	entries.BeforeSave.On(reg, func(_ context.Context, e *entries.Event) error {
		if e.Entry.Data["title"] == "verboten" {
			return errors.New("nein")
		}
		e.Entry.Data["title"] = "geprüft"
		return nil
	})
	entries.AfterSave.On(reg, func(_ context.Context, e *entries.Event) error {
		after = append(after, e.Entry.ID)
		return nil
	})
	res, err := svc.Save(ctx, entries.Save{Type: "page", Data: page("a", 0), UserID: 7})
	if err != nil || res.Entry.Data["title"] != "geprüft" || len(after) != 1 || after[0] != res.Entry.ID {
		t.Fatalf("hooks: %+v %v %v", res, err, after)
	}
	var errs content.Errors
	if _, err := svc.Save(ctx, entries.Save{Type: "page", Data: map[string]any{"title": "verboten", "slug": "b"}}); !errors.As(err, &errs) || errs["_"] == nil {
		t.Errorf("beforeSave refusal: %v", err)
	}
	if len(after) != 1 {
		t.Error("a refused save must not run afterSave")
	}
}

func TestDelete(t *testing.T) {
	svc, _ := service(t)
	ctx := context.Background()
	pub, _ := svc.Save(ctx, entries.Save{Type: "page", Data: page("p", 0), Publish: true})
	draft, _ := svc.Save(ctx, entries.Save{Type: "page", Data: page("d", 0)})
	if changed, err := svc.Delete(ctx, "page", pub.Entry.ID); err != nil || !changed {
		t.Errorf("delete published: %v %v", changed, err)
	}
	if changed, err := svc.Delete(ctx, "page", draft.Entry.ID); err != nil || changed {
		t.Errorf("delete draft: %v %v", changed, err)
	}
	if _, err := svc.Delete(ctx, "page", draft.Entry.ID); !errors.Is(err, entries.ErrNotFound) {
		t.Errorf("delete twice: %v", err)
	}
}

func TestDeleteReferencedPage(t *testing.T) {
	svc, _ := service(t)
	ctx := context.Background()
	parent, _ := svc.Save(ctx, entries.Save{Type: "page", Data: page("ueber", 0), Publish: true})
	child, _ := svc.Save(ctx, entries.Save{Type: "page", Data: page("team", parent.Entry.ID), Publish: true})

	changed, err := svc.Delete(ctx, "page", parent.Entry.ID)
	var ref *storage.ReferencedError
	if !errors.As(err, &ref) || changed || len(ref.Referrers) != 1 || ref.Referrers[0].ID != child.Entry.ID {
		t.Fatalf("delete parent: changed=%v err=%v", changed, err)
	}
}

// Slugs are unique per version: a draft may take the slug the published
// version of another entry gives up in its draft, but publishing it waits
// until that entry is published under its new slug.
func TestSlugUniquePerVersion(t *testing.T) {
	svc, _ := service(t)
	ctx := context.Background()
	a, err := svc.Save(ctx, entries.Save{Type: "page", Data: page("x", 0), Publish: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Save(ctx, entries.Save{Type: "page", ID: a.Entry.ID, Data: page("y", 0)}); err != nil {
		t.Fatal(err)
	}
	b, err := svc.Save(ctx, entries.Save{Type: "page", Data: page("x", 0)})
	if err != nil {
		t.Fatalf("slug free among drafts: %v", err)
	}
	var errs content.Errors
	if _, err := svc.Save(ctx, entries.Save{Type: "page", ID: b.Entry.ID, Data: page("x", 0), Publish: true}); !errors.As(err, &errs) || errs["slug"] == nil {
		t.Errorf("slug still published elsewhere: %v", err)
	}
	if _, err := svc.Save(ctx, entries.Save{Type: "page", ID: a.Entry.ID, Data: page("y", 0), Publish: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Save(ctx, entries.Save{Type: "page", ID: b.Entry.ID, Data: page("x", 0), Publish: true}); err != nil {
		t.Errorf("slug free after the other entry moved: %v", err)
	}
}
