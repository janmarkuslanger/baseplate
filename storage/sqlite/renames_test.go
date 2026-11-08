package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/storage"
)

// A rename runs once: a later field with the old name keeps its own data
// across restarts. Schema.Check rejects that combination; the store must
// still never move data a second time.
func TestRenameRunsOnce(t *testing.T) {
	for _, c := range []struct {
		name     string
		field    func(name, from string) content.Field
		old, new func(c1, c2 *storage.Entry) any
	}{
		{"relation",
			func(name, from string) content.Field {
				return content.Field{Name: name, RenamedFrom: from, Kind: fields.Relation, Ref: "category"}
			},
			func(c1, _ *storage.Entry) any { return c1.ID },
			func(_, c2 *storage.Entry) any { return c2.ID }},
		{"scalar",
			func(name, from string) content.Field {
				return content.Field{Name: name, RenamedFrom: from, Kind: fields.Text}
			},
			func(*storage.Entry, *storage.Entry) any { return "alt" },
			func(*storage.Entry, *storage.Entry) any { return "neu" }},
		{"list",
			func(name, from string) content.Field {
				return content.Field{Name: name, RenamedFrom: from, Kind: fields.List, Fields: []content.Field{{Name: "label", Kind: fields.Text}}}
			},
			func(*storage.Entry, *storage.Entry) any { return []any{map[string]any{"label": "alt"}} },
			func(*storage.Entry, *storage.Entry) any { return []any{map[string]any{"label": "neu"}} }},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "db.sqlite")
			post := func(fs ...content.Field) content.Type { return content.Type{Name: "post", Fields: fs} }

			s := startWith(t, path, categoryType, post(c.field("a", "")))
			c1 := &storage.Entry{Type: "category", Data: map[string]any{"title": "eins"}}
			c2 := &storage.Entry{Type: "category", Data: map[string]any{"title": "zwei"}}
			s.SaveDraft(ctx, c1)
			s.SaveDraft(ctx, c2)
			e := &storage.Entry{Type: "post", Data: map[string]any{"a": c.old(c1, c2)}}
			if err := s.SaveDraft(ctx, e); err != nil {
				t.Fatal(err)
			}
			s.Close()

			s = startWith(t, path, categoryType, post(c.field("b", "a")))
			s.Close()

			reused := post(c.field("b", "a"), c.field("a", ""))
			s = startWith(t, path, categoryType, reused)
			got, _ := s.GetEntry(ctx, "post", e.ID, "", storage.Draft)
			got.Data["a"] = c.new(c1, c2)
			if err := s.SaveDraft(ctx, got); err != nil {
				t.Fatal(err)
			}
			s.Close()

			for i := 0; i < 2; i++ {
				s = startWith(t, path, categoryType, reused)
				got, err := s.GetEntry(ctx, "post", e.ID, "", storage.Draft)
				if err != nil {
					t.Fatal(err)
				}
				if !sameValue(got.Data["b"], c.old(c1, c2)) || !sameValue(got.Data["a"], c.new(c1, c2)) {
					t.Errorf("restart %d: b = %v (want %v), a = %v (want %v)", i, got.Data["b"], c.old(c1, c2), got.Data["a"], c.new(c1, c2))
				}
				s.Close()
			}
		})
	}
}

// sameValue compares field values, ignoring the component keys of items.
func sameValue(got, want any) bool {
	if items, ok := got.([]any); ok {
		var labels []any
		for _, it := range items {
			labels = append(labels, it.(map[string]any)["label"])
		}
		wantItems, _ := want.([]any)
		if len(labels) != len(wantItems) {
			return false
		}
		for i, w := range wantItems {
			if labels[i] != w.(map[string]any)["label"] {
				return false
			}
		}
		return true
	}
	return got == want
}

// A start that fails while syncing the schema leaves the database as it
// was: a rename applied before the failure is rolled back and runs again
// on the next successful start.
func TestFailedSyncRollsBackRenames(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db.sqlite")
	v1 := content.Type{Name: "news", Fields: []content.Field{
		{Name: "teaser", Kind: fields.Text},
		{Name: "links", Kind: fields.List, Fields: []content.Field{{Name: "label", Kind: fields.Text}}},
		{Name: "rating", Kind: fields.Number},
	}}
	v2 := func(ratingKind string) content.Type {
		return content.Type{Name: "news", Fields: []content.Field{
			{Name: "summary", RenamedFrom: "teaser", Kind: fields.Text},
			{Name: "refs", RenamedFrom: "links", Kind: fields.List, Fields: []content.Field{{Name: "label", Kind: fields.Text}}},
			{Name: "rating", Kind: ratingKind},
		}}
	}

	s := startWith(t, path, v1)
	n := &storage.Entry{Type: "news", Data: map[string]any{"teaser": "Kurz", "links": []any{map[string]any{"label": "L"}}, "rating": 3.0}}
	if err := s.SaveDraft(ctx, n); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// rating changes kind: the start fails after the renames ran.
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	reg := newRegistry(t, s, v2(fields.Text))
	if err := s.UseSchema(ctx, reg.Schema); err == nil || !strings.Contains(err.Error(), "rating") {
		t.Fatalf("want kind conflict on rating, got %v", err)
	}
	for _, q := range []string{
		`SELECT COUNT(*) FROM pragma_table_info('ct_news') WHERE name = 'teaser'`,
		`SELECT COUNT(*) FROM sqlite_master WHERE name = 'ct_news__links'`,
		`SELECT COUNT(*) FROM components WHERE field = 'links'`,
	} {
		if got := count(t, s, q); got != 1 {
			t.Errorf("after failed start: %s = %d, want 1 (rename rolled back)", q, got)
		}
	}
	if got := count(t, s, `SELECT COUNT(*) FROM schema_migrations WHERE name LIKE 'rename:%'`); got != 0 {
		t.Errorf("failed start recorded %d renames", got)
	}
	s.Close()

	s = startWith(t, path, v2(fields.Number))
	got, err := s.GetEntry(ctx, "news", n.ID, "", storage.Draft)
	if err != nil {
		t.Fatal(err)
	}
	refs := got.Data["refs"].([]any)
	if got.Data["summary"] != "Kurz" || len(refs) != 1 || refs[0].(map[string]any)["label"] != "L" {
		t.Errorf("data after successful start: %v", got.Data)
	}
}
