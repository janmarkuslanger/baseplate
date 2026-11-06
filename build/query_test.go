package build

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/storage"
)

// listSetup registers posts (routable, under /blog/) with categories and
// returns a builder whose index page renders tmpl.
func listSetup(t *testing.T, tmpl string) (*Builder, storage.Store, map[string]int64) {
	t.Helper()
	b, st := setup(t)
	ctx := context.Background()
	b.Reg.ContentType(content.Type{Name: "category", Fields: []content.Field{{Name: "name", Kind: fields.Text}}})
	b.Reg.ContentType(content.Type{Name: "post", Routable: true, Path: "/blog/{slug}/", Fields: fields.RoutableFields(
		content.Field{Name: "date", Kind: fields.Text},
		content.Field{Name: "views", Kind: fields.Number},
		content.Field{Name: "featured", Kind: fields.Bool},
		content.Field{Name: "categories", Kind: fields.Relation, Ref: "category", Many: true},
	)})
	b.Reg.Extend(fields.PageType,
		content.Field{Name: "picked", Kind: fields.Relation, Ref: "category", Many: true},
		content.Field{Name: "none", Kind: fields.Relation, Ref: "category", Many: true})
	b.Reg.Block(content.Block{Name: "post_list", Fields: []content.Field{
		{Name: "limit", Kind: fields.Number},
		{Name: "categories", Kind: fields.Relation, Ref: "category", Many: true},
	}})
	if err := st.UseSchema(ctx, b.Reg.Schema); err != nil {
		t.Fatal(err)
	}
	b.Theme = fstest.MapFS{
		"templates/page.html": {Data: []byte(`{{define "page"}}` + tmpl + `{{end}}`)},
		"templates/post.html": {Data: []byte(`{{define "post"}}{{end}}`)},
	}
	add(t, st, fields.SettingsType, fields.SettingsType, storage.StatusPublished, map[string]any{"baseURL": "https://a.test"})
	ids := map[string]int64{
		"yoga": add(t, st, "category", "", storage.StatusPublished, map[string]any{"name": "Yoga"}),
		"food": add(t, st, "category", "", storage.StatusPublished, map[string]any{"name": "Food"}),
	}
	for _, p := range []struct {
		slug, date string
		views      int
		featured   bool
		cats       []any
		status     string
	}{
		{"b", "2024-02-01", 5, true, []any{ids["yoga"]}, storage.StatusPublished},
		{"a", "2024-03-01", 20, false, []any{ids["food"]}, storage.StatusPublished},
		{"c", "2024-01-01", 100, true, []any{ids["yoga"], ids["food"]}, storage.StatusPublished},
		{"d", "", 1, false, nil, storage.StatusPublished},
		{"x", "2025-01-01", 0, true, []any{ids["yoga"]}, storage.StatusDraft},
	} {
		ids[p.slug] = add(t, st, "post", p.slug, p.status, map[string]any{
			"title": strings.ToUpper(p.slug), "date": p.date, "views": p.views, "featured": p.featured, "categories": p.cats,
		})
	}
	return b, st, ids
}

func TestEntries(t *testing.T) {
	const item = `{{range $q}}{{.Data.title}}{{end}}`
	for _, c := range []struct{ name, query, want string }{
		{"default order: last updated first", `entries "post"`, "DCAB"},
		{"sort descending, empty last", `entries "post" "sort" "-date"`, "ABCD"},
		{"sort ascending", `entries "post" "sort" "date"`, "CBAD"},
		{"sort numbers", `entries "post" "sort" "views"`, "DBAC"},
		{"sort slug", `entries "post" "sort" "slug"`, "ABCD"},
		{"limit and offset", `entries "post" "sort" "slug" "limit" 2 "offset" 1`, "BC"},
		{"offset past end", `entries "post" "offset" 10`, ""},
		{"where bool", `entries "post" "where" "featured" true "sort" "slug"`, "BC"},
		{"where and where", `entries "post" "where" "featured" true "where" "views" 100`, "C"},
		{"has category", `entries "post" "has" "categories" (index $.Data.picked 0) "sort" "slug"`, "AC"},
		{"has from block data", `entries "post" "has" "categories" $.Data.picked "sort" "slug"`, "AC"},
		{"has empty = all", `entries "post" "has" "categories" $.Data.none "sort" "slug"`, "ABCD"},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, st, ids := listSetup(t, `{{$q := `+c.query+`}}`+item)
			add(t, st, fields.PageType, "index", storage.StatusPublished, map[string]any{"title": "S", "picked": []any{ids["food"]}})
			if _, err := b.Build(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := read(t, b, "index.html"); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestEntriesResult(t *testing.T) {
	b, st, _ := listSetup(t, `{{range entries "post" "where" "views" 100}}{{.URL}}|{{range .Data.categories}}{{.Data.name}},{{end}}{{end}}`)
	add(t, st, fields.PageType, "index", storage.StatusPublished, map[string]any{"title": "S"})
	if _, err := b.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := read(t, b, "index.html"); got != "/blog/c/|Yoga,Food," {
		t.Errorf("got %q", got)
	}
}

// A listing block: the editor picks categories and a limit.
func TestEntriesInBlock(t *testing.T) {
	b, st, ids := listSetup(t, `{{blocks .Data.body .}}`)
	b.Theme.(fstest.MapFS)["templates/blocks.html"] = &fstest.MapFile{Data: []byte(
		`{{define "block/post_list"}}{{range entries "post" "sort" "-date" "limit" .Block.limit "has" "categories" .Block.categories}}<a href="{{.URL}}">{{.Data.title}}</a>{{end}}{{end}}`)}
	b.Reg.AllowBlocks(fields.PageType, "body", "post_list")
	add(t, st, fields.PageType, "index", storage.StatusPublished, map[string]any{"title": "S", "body": []any{
		map[string]any{"_type": "post_list", "limit": 1, "categories": []any{ids["yoga"]}},
		map[string]any{"_type": "post_list"},
	}})
	if _, err := b.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := `<a href="/blog/b/">B</a>` + `<a href="/blog/a/">A</a><a href="/blog/b/">B</a><a href="/blog/c/">C</a><a href="/blog/d/">D</a>`
	if got := strings.ReplaceAll(read(t, b, "index.html"), "\n", ""); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestEntriesErrors(t *testing.T) {
	for _, c := range []struct{ query, want string }{
		{`entries "nope"`, `unknown type "nope"`},
		{`entries "post" "sort" "-nope"`, `sort: unknown field "nope"`},
		{`entries "post" "limit"`, `"limit" needs 1 value`},
		{`entries "post" "where" "featured"`, `"where" needs 2 value`},
		{`entries "post" "limit" -1`, `negative`},
		{`entries "post" "limit" "drei"`, `not a number`},
		{`entries "post" "filter" 1`, `unknown option filter`},
		{`entries "post" "where" "categories" 1`, `use "where" for plain fields`},
		{`entries "post" "has" "featured" true`, `use "where" for plain fields`},
	} {
		b, st, _ := listSetup(t, `{{range `+c.query+`}}{{end}}`)
		add(t, st, fields.PageType, "index", storage.StatusPublished, map[string]any{"title": "S"})
		if _, err := b.Build(context.Background()); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want %q, got %v", c.query, c.want, err)
		}
	}
}
