package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/storage"
)

var relatedPost = content.Type{Name: "post", Fields: []content.Field{
	{Name: "title", Kind: fields.Text},
	{Name: "cats", Kind: fields.Relation, Ref: "category", Many: true},
	{Name: "related", Kind: fields.Relation, Ref: "post", Many: true},
}}

func mustCreate(t *testing.T, s *storage.SQLStore, e *storage.Entry) *storage.Entry {
	t.Helper()
	if err := s.CreateEntry(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	return e
}

// wantReferenced asserts that deleting typ #id is refused with the given
// referrers and that the entry is still there.
func wantReferenced(t *testing.T, s *storage.SQLStore, typ string, id int64, want ...storage.Referrer) {
	t.Helper()
	ctx := context.Background()
	err := s.DeleteEntry(ctx, typ, id)
	var ref *storage.ReferencedError
	if !errors.As(err, &ref) {
		t.Fatalf("delete %s #%d: want *ReferencedError, got %v", typ, id, err)
	}
	if ref.Type != typ || ref.ID != id || len(ref.Referrers) != len(want) {
		t.Fatalf("delete %s #%d: error %+v, want referrers %+v", typ, id, ref, want)
	}
	for i, w := range want {
		if ref.Referrers[i] != w {
			t.Errorf("referrer %d = %+v, want %+v", i, ref.Referrers[i], w)
		}
	}
	if _, err := s.GetEntry(ctx, typ, id); err != nil {
		t.Errorf("refused delete removed the entry: %v", err)
	}
}

func TestDeleteReferencedEntryIsRefused(t *testing.T) {
	ctx := context.Background()
	s := startWith(t, filepath.Join(t.TempDir(), "db.sqlite"), categoryType, relatedPost)
	cat := mustCreate(t, s, &storage.Entry{Type: "category", Data: map[string]any{"title": "Go"}})
	post := mustCreate(t, s, &storage.Entry{Type: "post", Data: map[string]any{"cats": []any{cat.ID}}})

	wantReferenced(t, s, "category", cat.ID, storage.Referrer{Type: "post", ID: post.ID, Field: "cats"})
	got, _ := s.GetEntry(ctx, "post", post.ID)
	if cats := got.Data["cats"].([]any); len(cats) != 1 {
		t.Errorf("refused delete touched the relation: %v", cats)
	}

	// Removing the relation allows the delete.
	got.Data["cats"] = []any{}
	if err := s.UpdateEntry(ctx, got); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteEntry(ctx, "category", cat.ID); err != nil {
		t.Fatalf("delete after unlinking: %v", err)
	}
}

func TestDeleteReferencedFromBlockOrParent(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	reg := newRegistry(t, s, content.Type{Name: "form", Fields: []content.Field{{Name: "title", Kind: fields.Text}}})
	reg.Block(content.Block{Name: "form", Fields: []content.Field{{Name: "form", Kind: fields.Relation, Ref: "form"}}})
	if err := s.UseSchema(ctx, reg.Schema); err != nil {
		t.Fatal(err)
	}
	f := mustCreate(t, s, &storage.Entry{Type: "form", Data: map[string]any{"title": "Kontakt"}})
	parent := mustCreate(t, s, &storage.Entry{Type: "page", Slug: "ueber", Data: map[string]any{"title": "Über"}})
	child := mustCreate(t, s, &storage.Entry{Type: "page", Slug: "kontakt", Data: map[string]any{"title": "K", "parent": parent.ID, "body": []any{
		map[string]any{"_type": "article", "children": []any{map[string]any{"_type": "form", "form": f.ID}}},
	}}})

	wantReferenced(t, s, "form", f.ID, storage.Referrer{Type: "page", ID: child.ID, Field: "form", Nested: true, Block: "form"})
	wantReferenced(t, s, "page", parent.ID, storage.Referrer{Type: "page", ID: child.ID, Field: "parent"})

	if err := s.DeleteEntry(ctx, "page", child.ID); err != nil {
		t.Fatal(err)
	}
	for _, e := range []*storage.Entry{f, parent} {
		if err := s.DeleteEntry(ctx, e.Type, e.ID); err != nil {
			t.Errorf("delete %s after its referrer: %v", e.Type, err)
		}
	}
}

// An entry's relations to itself do not keep it alive.
func TestDeleteSelfReferencingEntry(t *testing.T) {
	ctx := context.Background()
	s := startWith(t, filepath.Join(t.TempDir(), "db.sqlite"), categoryType, relatedPost)
	post := mustCreate(t, s, &storage.Entry{Type: "post", Data: map[string]any{}})
	post.Data["related"] = []any{post.ID}
	if err := s.UpdateEntry(ctx, post); err != nil {
		t.Fatal(err)
	}
	if refs, err := s.Referrers(ctx, "post", post.ID); err != nil || len(refs) != 0 {
		t.Errorf("referrers of a self-reference: %+v %v", refs, err)
	}
	if err := s.DeleteEntry(ctx, "post", post.ID); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM relations`); n != 0 {
		t.Errorf("relations left: %d", n)
	}
}

// Relations of a field removed from the schema are kept data, but they do
// not block a delete: the editor could not remove them.
func TestRemovedRelationFieldDoesNotBlockDelete(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db.sqlite")
	s := startWith(t, path, categoryType, relatedPost)
	cat := mustCreate(t, s, &storage.Entry{Type: "category", Data: map[string]any{"title": "Go"}})
	post := mustCreate(t, s, &storage.Entry{Type: "post", Data: map[string]any{"cats": []any{cat.ID}}})
	s.Close()

	s = startWith(t, path, categoryType, content.Type{Name: "post", Fields: []content.Field{{Name: "title", Kind: fields.Text}}})
	if refs, err := s.Referrers(ctx, "category", cat.ID); err != nil || len(refs) != 0 {
		t.Errorf("referrers via a removed field: %+v %v", refs, err)
	}
	if err := s.DeleteEntry(ctx, "category", cat.ID); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s = startWith(t, path, categoryType, relatedPost)
	got, _ := s.GetEntry(ctx, "post", post.ID)
	if cats := got.Data["cats"].([]any); len(cats) != 0 {
		t.Errorf("relation to the deleted category survived: %v", cats)
	}
}
