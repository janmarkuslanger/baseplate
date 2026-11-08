package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/storage"
)

// startWith opens the database file like an app start: migrations, then the
// schema of the given content types.
func startWith(t *testing.T, path string, types ...content.Type) *storage.SQLStore {
	t.Helper()
	ctx := context.Background()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(ctx, storage.CoreMigrations); err != nil {
		t.Fatal(err)
	}
	if err := s.UseSchema(ctx, newRegistry(t, s, types...).Schema); err != nil {
		t.Fatal(err)
	}
	return s
}

// newRegistry registers the core, the core blocks and the given types.
func newRegistry(t *testing.T, s *storage.SQLStore, types ...content.Type) *plugin.Registry {
	t.Helper()
	reg := plugin.NewRegistry(plugin.Services{Store: s})
	if err := reg.Use(fields.Core{}, fields.CoreBlocks{}); err != nil {
		t.Fatal(err)
	}
	for _, ct := range types {
		if err := reg.ContentType(ct); err != nil {
			t.Fatal(err)
		}
	}
	return reg
}

var categoryType = content.Type{Name: "category", Fields: []content.Field{{Name: "title", Kind: fields.Text}}}

// Removing group, list, blocks and relation fields from the schema keeps
// their data, even when the entry is saved in between: re-adding the
// fields brings it back.
func TestRemovedFieldsKeepDataAcrossSave(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db.sqlite")
	full := content.Type{Name: "post", Fields: []content.Field{
		{Name: "title", Kind: fields.Text},
		{Name: "tags", Kind: fields.Relation, Ref: "category", Many: true},
		{Name: "meta", Kind: fields.Group, Fields: []content.Field{{Name: "author", Kind: fields.Text}}},
		{Name: "links", Kind: fields.List, Fields: []content.Field{
			{Name: "label", Kind: fields.Text},
			{Name: "ref", Kind: fields.Relation, Ref: "category"},
		}},
		// cb_heading stays in the schema through page.body.
		{Name: "body", Kind: fields.Blocks},
	}}
	reduced := content.Type{Name: "post", Fields: []content.Field{
		{Name: "title", Kind: fields.Text},
		{Name: "links", Kind: fields.List, Fields: []content.Field{{Name: "label", Kind: fields.Text}}},
	}}

	v1 := startWith(t, path, categoryType, full)
	cat := &storage.Entry{Type: "category", Data: map[string]any{"title": "Go"}}
	if err := v1.SaveDraft(ctx, cat); err != nil {
		t.Fatal(err)
	}
	post := &storage.Entry{Type: "post", Data: map[string]any{
		"title": "Alt",
		"tags":  []any{cat.ID},
		"meta":  map[string]any{"author": "Ada"},
		"links": []any{map[string]any{"label": "bleibt", "ref": cat.ID}},
		"body":  []any{map[string]any{"_type": "heading", "text": "H"}},
	}}
	if err := v1.SaveDraft(ctx, post); err != nil {
		t.Fatal(err)
	}
	v1.Close()

	v2 := startWith(t, path, categoryType, reduced)
	got, err := v2.GetEntry(ctx, "post", post.ID, "", storage.Draft)
	if err != nil {
		t.Fatal(err)
	}
	got.Data["title"] = "Neu"
	if err := v2.SaveDraft(ctx, got); err != nil {
		t.Fatal(err)
	}
	// A page edit must not touch the post's blocks in the shared cb_heading.
	page := &storage.Entry{Type: "page", Slug: "p", Data: map[string]any{"title": "P", "body": []any{map[string]any{"_type": "heading", "text": "Seite"}}}}
	if err := v2.SaveDraft(ctx, page); err != nil {
		t.Fatal(err)
	}
	v2.Close()

	v3 := startWith(t, path, categoryType, full)
	back, err := v3.GetEntry(ctx, "post", post.ID, "", storage.Draft)
	if err != nil {
		t.Fatal(err)
	}
	d := back.Data
	if d["title"] != "Neu" {
		t.Errorf("title = %v, want the value saved in between", d["title"])
	}
	if tags := d["tags"].([]any); len(tags) != 1 || tags[0] != cat.ID {
		t.Errorf("tags = %v, want [%d]", d["tags"], cat.ID)
	}
	if meta, _ := d["meta"].(map[string]any); meta["author"] != "Ada" {
		t.Errorf("meta = %v", d["meta"])
	}
	links := d["links"].([]any)
	if len(links) != 1 || links[0].(map[string]any)["label"] != "bleibt" || links[0].(map[string]any)["ref"] != cat.ID {
		t.Errorf("links = %v, want the item with its nested relation", links)
	}
	if body := d["body"].([]any); len(body) != 1 || body[0].(map[string]any)["text"] != "H" {
		t.Errorf("body = %v", d["body"])
	}
}

// Deleting a list item takes the data of its removed fields along, so
// no links or relations are left that no entry owns.
func TestRemovedItemTakesRemovedFieldData(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db.sqlite")
	full := content.Type{Name: "post", Fields: []content.Field{
		{Name: "links", Kind: fields.List, Fields: []content.Field{
			{Name: "label", Kind: fields.Text},
			{Name: "ref", Kind: fields.Relation, Ref: "category"},
			{Name: "extra", Kind: fields.Group, Fields: []content.Field{{Name: "note", Kind: fields.Text}}},
		}},
	}}
	reduced := content.Type{Name: "post", Fields: []content.Field{
		{Name: "links", Kind: fields.List, Fields: []content.Field{{Name: "label", Kind: fields.Text}}},
	}}

	v1 := startWith(t, path, categoryType, full)
	cat := &storage.Entry{Type: "category", Data: map[string]any{"title": "Go"}}
	v1.SaveDraft(ctx, cat)
	post := &storage.Entry{Type: "post", Data: map[string]any{"links": []any{
		map[string]any{"label": "weg", "ref": cat.ID, "extra": map[string]any{"note": "n"}},
	}}}
	if err := v1.SaveDraft(ctx, post); err != nil {
		t.Fatal(err)
	}
	v1.Close()

	v2 := startWith(t, path, categoryType, reduced)
	got, _ := v2.GetEntry(ctx, "post", post.ID, "", storage.Draft)
	got.Data["links"] = []any{}
	if err := v2.SaveDraft(ctx, got); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`SELECT COUNT(*) FROM relations`,
		`SELECT COUNT(*) FROM components`,
		`SELECT COUNT(*) FROM ct_post__links`,
	} {
		if n := count(t, v2, q); n != 0 {
			t.Errorf("%s = %d, want 0", q, n)
		}
	}
}
