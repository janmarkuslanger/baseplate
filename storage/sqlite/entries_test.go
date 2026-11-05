package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/storage"
)

type testTypes struct{}

func (testTypes) Name() string { return "test" }
func (testTypes) Register(r *plugin.Registry) error {
	r.ContentType(content.Type{Name: "category", Fields: []content.Field{{Name: "title", Kind: fields.Text}}})
	return r.ContentType(content.Type{Name: "post", Fields: []content.Field{
		{Name: "title", Kind: fields.Text},
		{Name: "views", Kind: fields.Number},
		{Name: "featured", Kind: fields.Bool},
		{Name: "categories", Kind: fields.Relation, Ref: "category", Many: true, Inverse: "posts"},
		{Name: "links", Kind: fields.List, Fields: []content.Field{{Name: "label", Kind: fields.Text}}},
	}})
}

func schemaStore(t *testing.T) (*storage.SQLStore, *content.Schema) {
	t.Helper()
	s := openTest(t)
	reg := plugin.NewRegistry(plugin.Services{Store: s})
	if err := reg.Use(fields.Core{}, testTypes{}); err != nil {
		t.Fatal(err)
	}
	if err := s.UseSchema(context.Background(), reg.Schema); err != nil {
		t.Fatal(err)
	}
	return s, reg.Schema
}

func count(t *testing.T, s *storage.SQLStore, q string) int {
	var n int
	if err := s.DB().QueryRow(q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestEntryRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, _ := schemaStore(t)

	cat := &storage.Entry{Type: "category", Data: map[string]any{"title": "Go"}}
	if err := s.CreateEntry(ctx, cat); err != nil {
		t.Fatal(err)
	}
	page := &storage.Entry{Type: "page", Slug: "index", Status: storage.StatusPublished, Data: map[string]any{
		"title": "Start", "slug": "index",
		"seo": map[string]any{"metaTitle": "Meta", "noindex": true},
		"body": []any{
			map[string]any{"_type": "heading", "text": "H"},
			map[string]any{"_type": "article", "title": "A", "children": []any{
				map[string]any{"_type": "richtext", "html": "<p>x</p>"},
				map[string]any{"_type": "article", "title": "B", "children": []any{}},
			}},
		},
	}}
	if err := s.CreateEntry(ctx, page); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetEntryBySlug(ctx, "page", "index")
	if err != nil {
		t.Fatal(err)
	}
	if got.Data["title"] != "Start" || got.Data["slug"] != "index" {
		t.Errorf("scalars: %v", got.Data)
	}
	seo := got.Data["seo"].(map[string]any)
	if seo["metaTitle"] != "Meta" || seo["noindex"] != true || seo["nofollow"] != false {
		t.Errorf("group: %v", seo)
	}
	body := got.Data["body"].([]any)
	art := body[1].(map[string]any)
	inner := art["children"].([]any)[1].(map[string]any)
	if len(body) != 2 || body[0].(map[string]any)["_type"] != "heading" || art["title"] != "A" || inner["title"] != "B" {
		t.Errorf("blocks: %#v", body)
	}
	if !strings.HasPrefix(art["_key"].(string), "c") {
		t.Error("block items need a stable key")
	}

	post := &storage.Entry{Type: "post", Data: map[string]any{
		"title": "Hallo Welt", "views": 3.5, "featured": true,
		"categories": []any{cat.ID},
		"links":      []any{map[string]any{"label": "eins"}, map[string]any{"label": "zwei"}},
	}}
	if err := s.CreateEntry(ctx, post); err != nil {
		t.Fatal(err)
	}
	p, _ := s.GetEntry(ctx, "post", post.ID)
	if p.Data["views"] != 3.5 || p.Data["featured"] != true || len(p.Data["links"].([]any)) != 2 {
		t.Errorf("post: %v", p.Data)
	}
	if ids := p.Data["categories"].([]any); len(ids) != 1 || ids[0] != cat.ID {
		t.Errorf("relation: %v", p.Data["categories"])
	}

	// Updates rewrite components instead of piling them up.
	before := count(t, s, `SELECT COUNT(*) FROM components`)
	p.Data["links"] = []any{map[string]any{"label": "nur eins"}}
	if err := s.UpdateEntry(ctx, p); err != nil {
		t.Fatal(err)
	}
	if after := count(t, s, `SELECT COUNT(*) FROM components`); after != before-1 {
		t.Errorf("components after update: %d, want %d", after, before-1)
	}

	list, total, err := s.ListEntries(ctx, storage.ListQuery{Type: "post", Search: "Welt"})
	if err != nil || total != 1 || list[0].Data["title"] != "Hallo Welt" {
		t.Errorf("search: %v %d %v", list, total, err)
	}
}

func TestRelationIntegrity(t *testing.T) {
	ctx := context.Background()
	s, _ := schemaStore(t)
	err := s.CreateEntry(ctx, &storage.Entry{Type: "post", Data: map[string]any{"categories": []any{int64(99)}}})
	if !errors.Is(err, storage.ErrInvalidRelation) {
		t.Fatalf("want ErrInvalidRelation, got %v", err)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM ct_post`); n != 0 {
		t.Error("failed create must roll back")
	}

	form := &storage.Entry{Type: "page", Slug: "ziel", Data: map[string]any{"title": "Ziel"}}
	s.CreateEntry(ctx, form)
	cat := &storage.Entry{Type: "category", Data: map[string]any{"title": "Go"}}
	s.CreateEntry(ctx, cat)
	post := &storage.Entry{Type: "post", Data: map[string]any{"categories": []any{cat.ID}}}
	s.CreateEntry(ctx, post)
	// A relation from inside a nested block: page → article → page "parent"
	// is not a block field, so use a child page whose parent is "ziel".
	child := &storage.Entry{Type: "page", Slug: "kind", Data: map[string]any{"title": "Kind", "parent": form.ID}}
	s.CreateEntry(ctx, child)

	refs, err := s.Referrers(ctx, "category", cat.ID)
	if err != nil || len(refs) != 1 || refs[0].Type != "post" || refs[0].ID != post.ID || refs[0].Field != "categories" {
		t.Fatalf("referrers: %+v %v", refs, err)
	}

	if err := s.DeleteEntry(ctx, "category", cat.ID); err != nil {
		t.Fatal(err)
	}
	p, _ := s.GetEntry(ctx, "post", post.ID)
	if len(p.Data["categories"].([]any)) != 0 {
		t.Error("relation to deleted entry must disappear")
	}
	if err := s.DeleteEntry(ctx, "page", form.ID); err != nil {
		t.Fatal(err)
	}
	c, _ := s.GetEntry(ctx, "page", child.ID)
	if c.Data["parent"] != nil {
		t.Errorf("parent of deleted page: %v", c.Data["parent"])
	}
	if n := count(t, s, `SELECT COUNT(*) FROM ct_page__seo`); n != 0 {
		// pages without SEO data own no seo component
		t.Logf("seo components: %d", n)
	}
}

func TestNestedReferrer(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	reg := plugin.NewRegistry(plugin.Services{Store: s})
	reg.Use(fields.Core{})
	reg.ContentType(content.Type{Name: "form", Fields: []content.Field{{Name: "title", Kind: fields.Text}}})
	reg.Block(content.Block{Name: "form", Fields: []content.Field{{Name: "form", Kind: fields.Relation, Ref: "form"}}})
	if err := s.UseSchema(ctx, reg.Schema); err != nil {
		t.Fatal(err)
	}
	f := &storage.Entry{Type: "form", Data: map[string]any{"title": "Kontakt"}}
	s.CreateEntry(ctx, f)
	page := &storage.Entry{Type: "page", Slug: "kontakt", Data: map[string]any{"title": "K", "body": []any{
		map[string]any{"_type": "article", "children": []any{map[string]any{"_type": "form", "form": f.ID}}},
	}}}
	if err := s.CreateEntry(ctx, page); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetEntry(ctx, "page", page.ID)
	block := got.Data["body"].([]any)[0].(map[string]any)["children"].([]any)[0].(map[string]any)
	if block["form"] != f.ID {
		t.Errorf("block relation: %v", block)
	}
	refs, _ := s.Referrers(ctx, "form", f.ID)
	if len(refs) != 1 || refs[0].Type != "page" || refs[0].ID != page.ID || !refs[0].Nested {
		t.Errorf("nested referrer: %+v", refs)
	}
	s.DeleteEntry(ctx, "page", page.ID)
	if n := count(t, s, `SELECT COUNT(*) FROM components`) + count(t, s, `SELECT COUNT(*) FROM cb_form`) + count(t, s, `SELECT COUNT(*) FROM relations`); n != 0 {
		t.Errorf("leftover rows after delete: %d", n)
	}
}

func TestSchemaSync(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	mk := func(kind string, extra ...content.Field) *content.Schema {
		reg := plugin.NewRegistry(plugin.Services{Store: s})
		reg.Use(fields.Core{})
		reg.ContentType(content.Type{Name: "news", Fields: append([]content.Field{{Name: "rating", Kind: kind}}, extra...)})
		return reg.Schema
	}
	if err := s.UseSchema(ctx, mk(fields.Number)); err != nil {
		t.Fatal(err)
	}
	s.CreateEntry(ctx, &storage.Entry{Type: "news", Data: map[string]any{"rating": 4.0}})
	// Adding a field adds a column; existing rows keep their data.
	if err := s.UseSchema(ctx, mk(fields.Number, content.Field{Name: "subTitle", Kind: fields.Text})); err != nil {
		t.Fatal(err)
	}
	list, _, _ := s.ListEntries(ctx, storage.ListQuery{Type: "news"})
	if list[0].Data["rating"] != 4.0 {
		t.Errorf("data lost after sync: %v", list[0].Data)
	}
	// Changing a field's kind is refused.
	if err := s.UseSchema(ctx, mk(fields.Text)); err == nil || !strings.Contains(err.Error(), "rating") {
		t.Fatalf("want type conflict error, got %v", err)
	}
}
