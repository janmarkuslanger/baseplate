package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/janmarkuslanger/baseplate/storage"
)

func mustPublish(t *testing.T, s *storage.SQLStore, e *storage.Entry) {
	t.Helper()
	if err := s.Publish(context.Background(), e.Type, e.ID, e.Locale); err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, s *storage.SQLStore, typ string, id int64, v storage.Version) *storage.Entry {
	t.Helper()
	e, err := s.GetEntry(context.Background(), typ, id, "", v)
	if err != nil {
		t.Fatalf("%s #%d %s: %v", typ, id, v, err)
	}
	return e
}

func links(e *storage.Entry) []string {
	var out []string
	for _, it := range e.Data["links"].([]any) {
		out = append(out, it.(map[string]any)["label"].(string))
	}
	return out
}

// Editing the draft of a published entry leaves the published version as
// it was until the next publish; both have their own components.
func TestDraftEditsDoNotTouchPublished(t *testing.T) {
	ctx := context.Background()
	s, _ := schemaStore(t)
	post := mustCreate(t, s, &storage.Entry{Type: "post", Data: map[string]any{
		"title": "Eins", "links": []any{map[string]any{"label": "a"}},
	}})
	if d := get(t, s, "post", post.ID, storage.Draft); d.Published() || !d.Changed {
		t.Errorf("new draft: published %v, changed %v", d.Published(), d.Changed)
	}
	if _, err := s.GetEntry(ctx, "post", post.ID, "", storage.Published); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("unpublished entry has a published version: %v", err)
	}

	mustPublish(t, s, post)
	if d := get(t, s, "post", post.ID, storage.Draft); !d.Published() || d.Changed {
		t.Errorf("published draft: published %v, changed %v", d.Published(), d.Changed)
	}

	d := get(t, s, "post", post.ID, storage.Draft)
	d.Data["title"] = "Zwei"
	d.Data["links"] = []any{d.Data["links"].([]any)[0], map[string]any{"label": "b"}}
	if err := s.SaveDraft(ctx, d); err != nil {
		t.Fatal(err)
	}
	pub := get(t, s, "post", post.ID, storage.Published)
	if pub.Data["title"] != "Eins" || strings.Join(links(pub), ",") != "a" {
		t.Errorf("published changed by a draft save: %v", pub.Data)
	}
	if pub.ID != post.ID || pub.Version != storage.Published {
		t.Errorf("published version: id %d, version %q", pub.ID, pub.Version)
	}
	if d := get(t, s, "post", post.ID, storage.Draft); !d.Changed || d.Data["title"] != "Zwei" {
		t.Errorf("edited draft: changed %v, %v", d.Changed, d.Data)
	}

	mustPublish(t, s, post)
	pub = get(t, s, "post", post.ID, storage.Published)
	if pub.Data["title"] != "Zwei" || strings.Join(links(pub), ",") != "a,b" {
		t.Errorf("republished: %v", pub.Data)
	}
	// Two versions with two items each; republishing does not pile up.
	if n := count(t, s, `SELECT COUNT(*) FROM ct_post__links`); n != 4 {
		t.Errorf("list rows: %d, want 4", n)
	}
}

func TestUnpublishKeepsDraft(t *testing.T) {
	ctx := context.Background()
	s, _ := schemaStore(t)
	post := mustCreate(t, s, &storage.Entry{Type: "post", Data: map[string]any{"title": "T", "links": []any{map[string]any{"label": "a"}}}})
	mustPublish(t, s, post)
	if err := s.Unpublish(ctx, "post", post.ID, ""); err != nil {
		t.Fatal(err)
	}
	if d := get(t, s, "post", post.ID, storage.Draft); d.Published() || d.Data["title"] != "T" {
		t.Errorf("draft after unpublish: %+v", d)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM ct_post__links`); n != 1 {
		t.Errorf("components of the published version left: %d rows, want 1", n)
	}
	if err := s.Unpublish(ctx, "post", post.ID, ""); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("unpublish twice: %v", err)
	}
	if err := s.Publish(ctx, "post", 999, ""); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("publish unknown: %v", err)
	}
}

// Relations point at documents: they hold for every version, and a
// referrer with both versions counts once.
func TestRelationsPointAtDocuments(t *testing.T) {
	ctx := context.Background()
	s, _ := schemaStore(t)
	cat := mustCreate(t, s, &storage.Entry{Type: "category", Data: map[string]any{"title": "Go"}})
	post := mustCreate(t, s, &storage.Entry{Type: "post", Data: map[string]any{"categories": []any{cat.ID}}})
	mustPublish(t, s, cat)
	mustPublish(t, s, post)

	if ids := get(t, s, "post", post.ID, storage.Published).Data["categories"].([]any); len(ids) != 1 || ids[0] != cat.ID {
		t.Errorf("published relation: %v", ids)
	}
	refs, err := s.Referrers(ctx, "category", cat.ID)
	if err != nil || len(refs) != 1 || refs[0].ID != post.ID {
		t.Errorf("referrers: %+v %v", refs, err)
	}

	// Removing the relation from the draft still leaves the published one.
	d := get(t, s, "post", post.ID, storage.Draft)
	d.Data["categories"] = []any{}
	if err := s.SaveDraft(ctx, d); err != nil {
		t.Fatal(err)
	}
	wantReferenced(t, s, "category", cat.ID, storage.Referrer{Type: "post", ID: post.ID, Field: "categories"})
	if err := s.Unpublish(ctx, "post", post.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteEntry(ctx, "category", cat.ID); err != nil {
		t.Fatalf("delete after the last relation left: %v", err)
	}
}

func TestDeleteRemovesAllVersions(t *testing.T) {
	ctx := context.Background()
	s, _ := schemaStore(t)
	post := mustCreate(t, s, &storage.Entry{Type: "post", Data: map[string]any{"links": []any{map[string]any{"label": "a"}}}})
	mustPublish(t, s, post)
	if err := s.DeleteEntry(ctx, "post", post.ID); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`SELECT COUNT(*) FROM ct_post`, `SELECT COUNT(*) FROM ct_post__links`, `SELECT COUNT(*) FROM components`} {
		if n := count(t, s, q); n != 0 {
			t.Errorf("%s: %d", q, n)
		}
	}
	if err := s.DeleteEntry(ctx, "post", post.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("delete twice: %v", err)
	}
}

func TestListByVersion(t *testing.T) {
	ctx := context.Background()
	s, _ := schemaStore(t)
	live := mustCreate(t, s, &storage.Entry{Type: "post", Data: map[string]any{"title": "live"}})
	mustCreate(t, s, &storage.Entry{Type: "post", Data: map[string]any{"title": "draft"}})
	mustPublish(t, s, live)

	titles := func(q storage.ListQuery) string {
		t.Helper()
		q.Type = "post"
		list, total, err := s.ListEntries(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range list {
			out = append(out, e.Data["title"].(string))
		}
		if total != len(out) {
			t.Errorf("total %d, listed %d", total, len(out))
		}
		return strings.Join(out, ",")
	}
	for _, c := range []struct {
		q    storage.ListQuery
		want string
	}{
		{storage.ListQuery{Version: storage.Draft}, "draft,live"},
		{storage.ListQuery{Version: storage.Published}, "live"},
		{storage.ListQuery{Version: storage.Draft, Published: storage.OnlyPublished}, "live"},
		{storage.ListQuery{Version: storage.Draft, Published: storage.OnlyUnpublished}, "draft"},
		{storage.ListQuery{Version: storage.Draft, Locale: "en"}, ""},
	} {
		if got := titles(c.q); got != c.want {
			t.Errorf("%+v: %q, want %q", c.q, got, c.want)
		}
	}
	if _, _, err := s.ListEntries(ctx, storage.ListQuery{Type: "post"}); err == nil {
		t.Error("a list without version must fail")
	}
}

// Locales are versions of the same document: they share its ID and are
// published on their own.
func TestLocalesShareTheDocument(t *testing.T) {
	ctx := context.Background()
	s, _ := schemaStore(t)
	de := mustCreate(t, s, &storage.Entry{Type: "post", Data: map[string]any{"title": "Hallo"}})
	en := mustCreate(t, s, &storage.Entry{Type: "post", ID: de.ID, Locale: "en", Data: map[string]any{"title": "Hello"}})
	if en.ID != de.ID {
		t.Fatalf("locale got its own document: %d vs %d", en.ID, de.ID)
	}
	mustPublish(t, s, en)
	if _, err := s.GetEntry(ctx, "post", de.ID, "", storage.Published); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("publishing one locale published another: %v", err)
	}
	if e, err := s.GetEntry(ctx, "post", de.ID, "en", storage.Published); err != nil || e.Data["title"] != "Hello" {
		t.Errorf("published locale: %+v %v", e, err)
	}
	if err := s.SaveDraft(ctx, &storage.Entry{Type: "post", ID: 999, Locale: "en", Data: map[string]any{}}); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("locale of an unknown document: %v", err)
	}
}

func TestSingletonKeepsBothVersions(t *testing.T) {
	s := startWith(t, filepath.Join(t.TempDir(), "db.sqlite"), siteType(true))
	site := mustCreate(t, s, &storage.Entry{Type: "site", Slug: "site", Data: map[string]any{"title": "a"}})
	mustPublish(t, s, site)
	if get(t, s, "site", site.ID, storage.Published).Data["title"] != "a" {
		t.Error("singleton not published")
	}
}

// A database from before draft/published versions is refused instead of
// being read with a different meaning.
func TestOldLayoutFailsStart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx, storage.CoreMigrations); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, `CREATE TABLE ct_site (id INTEGER PRIMARY KEY AUTOINCREMENT, slug TEXT NOT NULL DEFAULT '', status VARCHAR(20) NOT NULL DEFAULT 'draft', locale VARCHAR(20) NOT NULL DEFAULT '', created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL, title TEXT NULL)`); err != nil {
		t.Fatal(err)
	}
	err = s.UseSchema(ctx, newRegistry(t, s, siteType(false)).Schema)
	s.Close()
	if err == nil || !strings.Contains(err.Error(), "ct_site") || !strings.Contains(err.Error(), "v0.3") {
		t.Fatalf("want an error naming the table and version, got %v", err)
	}
}
