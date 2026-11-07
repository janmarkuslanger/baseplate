package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/storage"
)

// Referrers lists every referring entry once, also when type names and IDs
// concatenate to the same text (ct_a #12 and ct_a1 #2).
func TestReferrersDistinguishSimilarOwners(t *testing.T) {
	ctx := context.Background()
	ref := []content.Field{{Name: "cat", Kind: fields.Relation, Ref: "category"}}
	s := startWith(t, filepath.Join(t.TempDir(), "db.sqlite"), categoryType,
		content.Type{Name: "a", Fields: ref}, content.Type{Name: "a1", Fields: ref})

	cat := &storage.Entry{Type: "category", Data: map[string]any{"title": "Go"}}
	if err := s.CreateEntry(ctx, cat); err != nil {
		t.Fatal(err)
	}
	var a12 *storage.Entry
	for i := 0; i < 12; i++ {
		a12 = &storage.Entry{Type: "a", Data: map[string]any{}}
		if i == 11 {
			a12.Data["cat"] = cat.ID
		}
		if err := s.CreateEntry(ctx, a12); err != nil {
			t.Fatal(err)
		}
	}
	s.CreateEntry(ctx, &storage.Entry{Type: "a1", Data: map[string]any{}})
	a1n2 := &storage.Entry{Type: "a1", Data: map[string]any{"cat": cat.ID}}
	if err := s.CreateEntry(ctx, a1n2); err != nil {
		t.Fatal(err)
	}
	if a12.ID != 12 || a1n2.ID != 2 {
		t.Fatalf("setup: IDs %d and %d, want 12 and 2", a12.ID, a1n2.ID)
	}

	refs, err := s.Referrers(ctx, "category", cat.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, r := range refs {
		got[r.Type] = r.ID
	}
	if len(refs) != 2 || got["a"] != 12 || got["a1"] != 2 {
		t.Errorf("referrers = %+v, want a #12 and a1 #2", refs)
	}
}
