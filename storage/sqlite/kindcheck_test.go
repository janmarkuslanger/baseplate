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

var tagType = content.Type{Name: "tag", Fields: []content.Field{{Name: "title", Kind: fields.Text}}}

// Changing how a field with stored data is persisted fails the start
// instead of reading the data wrongly and dropping it on the next save.
func TestKindChangeWithDataFailsStart(t *testing.T) {
	label := []content.Field{{Name: "label", Kind: fields.Text}}
	text := content.Field{Name: "f", Kind: fields.Text}
	rel := content.Field{Name: "f", Kind: fields.Relation, Ref: "category"}
	rels := content.Field{Name: "f", Kind: fields.Relation, Ref: "category", Many: true}
	list := content.Field{Name: "f", Kind: fields.List, Fields: label}
	group := content.Field{Name: "f", Kind: fields.Group, Fields: label}
	blocks := content.Field{Name: "f", Kind: fields.Blocks}
	item := func(s string) map[string]any { return map[string]any{"label": s} }

	for _, c := range []struct {
		name     string
		from, to content.Field
		value    func(cat int64) any
		want     string // "" = the start succeeds
	}{
		{"text → relation", text, rel, func(int64) any { return "x" }, "has a column"},
		{"relation → text", rel, text, func(id int64) any { return id }, "has components or relations stored"},
		{"relation → list", rels, list, func(id int64) any { return []any{id} }, "has relations stored"},
		{"list → relation", list, rel, func(int64) any { return []any{item("a")} }, "has components stored"},
		{"list → blocks", list, blocks, func(int64) any { return []any{item("a")} }, "has components in ct_post__f"},
		{"blocks → list", blocks, list, func(int64) any { return []any{map[string]any{"_type": "heading", "text": "H"}} }, "has components in cb_heading"},
		{"list with two items → group", list, group, func(int64) any { return []any{item("a"), item("b")} }, "up to 2 items"},
		{"relation Ref changed", rel, content.Field{Name: "f", Kind: fields.Relation, Ref: "tag"}, func(id int64) any { return id }, `relations to "category"`},
		{"Many → single with two targets", rels, rel, func(id int64) any { return []any{id, id + 1} }, "up to 2 targets"},

		{"list with one item → group", list, group, func(int64) any { return []any{item("a")} }, ""},
		{"group → list", group, list, func(int64) any { return item("a") }, ""},
		{"single → Many", rel, rels, func(id int64) any { return id }, ""},
		{"Many → single with one target", rels, rel, func(id int64) any { return []any{id} }, ""},
		{"no data stored", rel, text, func(int64) any { return nil }, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "db.sqlite")
			post := func(f content.Field) content.Type { return content.Type{Name: "post", Fields: []content.Field{f}} }

			s := startWith(t, path, categoryType, tagType, post(c.from))
			c1 := &storage.Entry{Type: "category", Data: map[string]any{"title": "eins"}}
			c2 := &storage.Entry{Type: "category", Data: map[string]any{"title": "zwei"}}
			s.CreateEntry(ctx, c1)
			s.CreateEntry(ctx, c2)
			e := &storage.Entry{Type: "post", Data: map[string]any{}}
			if v := c.value(c1.ID); v != nil {
				e.Data["f"] = v
			}
			if err := s.CreateEntry(ctx, e); err != nil {
				t.Fatal(err)
			}
			s.Close()

			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			err = s.UseSchema(ctx, newRegistry(t, s, categoryType, tagType, post(c.to)).Schema)
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("start failed: %v", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "ct_post.f")):
				t.Fatalf("want start error containing %q, got %v", c.want, err)
			}
		})
	}
}

// A refused start changes nothing: the old schema still starts and reads
// the data.
func TestRefusedKindChangeKeepsData(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db.sqlite")
	v1 := content.Type{Name: "post", Fields: []content.Field{{Name: "cats", Kind: fields.Relation, Ref: "category", Many: true}}}
	v2 := content.Type{Name: "post", Fields: []content.Field{
		{Name: "cats", Kind: fields.Text},
		{Name: "added", Kind: fields.Text},
	}}

	s := startWith(t, path, categoryType, v1)
	cat := &storage.Entry{Type: "category", Data: map[string]any{"title": "Go"}}
	s.CreateEntry(ctx, cat)
	post := &storage.Entry{Type: "post", Data: map[string]any{"cats": []any{cat.ID}}}
	s.CreateEntry(ctx, post)
	s.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UseSchema(ctx, newRegistry(t, s, categoryType, v2).Schema); err == nil {
		t.Fatal("kind change must fail the start")
	}
	if n := count(t, s, `SELECT COUNT(*) FROM pragma_table_info('ct_post') WHERE name IN ('cats', 'added')`); n != 0 {
		t.Errorf("refused start added %d columns", n)
	}
	s.Close()

	s = startWith(t, path, categoryType, v1)
	got, err := s.GetEntry(ctx, "post", post.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cats := got.Data["cats"].([]any); len(cats) != 1 || cats[0] != cat.ID {
		t.Errorf("cats = %v", got.Data["cats"])
	}
}
