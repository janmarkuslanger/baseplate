package build

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/i18n"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/render"
	"github.com/janmarkuslanger/baseplate/storage"
	"github.com/janmarkuslanger/baseplate/storage/sqlite"
)

var theme = fstest.MapFS{
	"templates/page.html": {Data: []byte(`{{define "page"}}<!doctype html><html><head>{{template "baseplate/head" .}}</head><body><nav>{{range .Nav}}<a href="{{.URL}}">{{.Title}}</a>{{end}}</nav><h1>{{.Data.title}}</h1>{{blocks .Data.body .}}</body></html>{{end}}`)},
	"assets/site.css":     {Data: []byte(`body{}`)},
}

func setup(t *testing.T) (*Builder, storage.Store) {
	t.Helper()
	ctx := context.Background()
	st, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	st.Migrate(ctx, storage.CoreMigrations)
	reg := plugin.NewRegistry(plugin.Services{Store: st})
	if err := reg.Use(fields.Core{}, fields.CoreBlocks{}); err != nil {
		t.Fatal(err)
	}
	if err := st.UseSchema(ctx, reg.Schema); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	return &Builder{Reg: reg, Theme: theme, DistDir: filepath.Join(dir, "dist"), MediaDir: filepath.Join(dir, "media")}, st
}

func add(t *testing.T, st storage.Store, typ, slug, status string, data map[string]any) int64 {
	t.Helper()
	e := &storage.Entry{Type: typ, Slug: slug, Status: status, Data: data}
	if err := st.CreateEntry(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	return e.ID
}

func read(t *testing.T, b *Builder, p string) string {
	t.Helper()
	out, err := os.ReadFile(filepath.Join(b.DistDir, p))
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestBuild(t *testing.T) {
	b, st := setup(t)
	ctx := context.Background()

	if _, err := b.Build(ctx); err == nil || !strings.Contains(err.Error(), "Basis-URL") {
		t.Fatalf("want missing baseURL error, got %v", err)
	}

	add(t, st, fields.SettingsType, fields.SettingsType, storage.StatusPublished, map[string]any{
		"siteName": "Acme", "baseURL": "https://acme.test/", "llmsTxt": true, "llmsContent": "> Acme baut Dinge.",
	})
	add(t, st, fields.PageType, "index", storage.StatusPublished, map[string]any{
		"title": "Willkommen", "seo": map[string]any{"metaDescription": "Startseite von Acme"},
		"body": []any{
			map[string]any{"_type": "heading", "text": "Hallo <Welt>"},
			map[string]any{"_type": "richtext", "html": "<p>Text <strong>fett</strong></p>"},
		},
	})
	about := add(t, st, fields.PageType, "ueber-uns", storage.StatusPublished, map[string]any{
		"title": "Über uns", "seo": map[string]any{"metaDescription": "Team und Geschichte"},
	})
	add(t, st, fields.PageType, "team", storage.StatusPublished, map[string]any{"title": "Team", "parent": about})
	add(t, st, fields.PageType, "geheim", storage.StatusPublished, map[string]any{"title": "Intern", "seo": map[string]any{"noindex": true}})
	add(t, st, fields.PageType, "entwurf", storage.StatusDraft, map[string]any{"title": "Entwurf"})

	res, err := b.Build(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pages != 4 {
		t.Errorf("pages = %d, want 4", res.Pages)
	}

	home := read(t, b, "index.html")
	for _, want := range []string{
		`<title>Willkommen | Acme</title>`,
		`<meta name="description" content="Startseite von Acme">`,
		`<link rel="canonical" href="https://acme.test/">`,
		`<h2>Hallo &lt;Welt&gt;</h2>`,
		`<p>Text <strong>fett</strong></p>`,
		`<a href="/ueber-uns/team/">Team</a>`,
		`href="/index.md"`,
	} {
		if !strings.Contains(home, want) {
			t.Errorf("index.html missing %q\n%s", want, home)
		}
	}
	read(t, b, "ueber-uns/team/index.html")
	if _, err := os.Stat(filepath.Join(b.DistDir, "entwurf")); !os.IsNotExist(err) {
		t.Error("draft page was built")
	}
	if !strings.Contains(read(t, b, "geheim/index.html"), `content="noindex, follow"`) {
		t.Error("noindex not rendered")
	}

	md := read(t, b, "index.md")
	if !strings.Contains(md, "# Willkommen") || !strings.Contains(md, `## Hallo \<Welt\>`) || !strings.Contains(md, "Text **fett**") {
		t.Errorf("markdown:\n%s", md)
	}

	sitemap := read(t, b, "sitemap.xml")
	if !strings.Contains(sitemap, "<loc>https://acme.test/ueber-uns/team/</loc>") || strings.Contains(sitemap, "geheim") {
		t.Errorf("sitemap:\n%s", sitemap)
	}

	llms := read(t, b, "llms.txt")
	for _, want := range []string{"# Acme", "> Acme baut Dinge.", "- [Über uns](https://acme.test/ueber-uns.md): Team und Geschichte"} {
		if !strings.Contains(llms, want) {
			t.Errorf("llms.txt missing %q:\n%s", want, llms)
		}
	}
	if strings.Contains(llms, "Intern") {
		t.Error("noindex page listed in llms.txt")
	}
	if !strings.Contains(read(t, b, "robots.txt"), "Sitemap: https://acme.test/sitemap.xml") {
		t.Error("robots.txt")
	}
	read(t, b, "assets/site.css")
}

func TestLLMsTxtDisabled(t *testing.T) {
	b, st := setup(t)
	add(t, st, fields.SettingsType, fields.SettingsType, storage.StatusPublished, map[string]any{"baseURL": "https://a.test"})
	add(t, st, fields.PageType, "index", storage.StatusPublished, map[string]any{"title": "A"})
	if _, err := b.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(b.DistDir, "llms.txt")); !os.IsNotExist(err) {
		t.Error("llms.txt built although disabled")
	}
	read(t, b, "index.md") // Markdown variants stay
}

func TestBuildFailureKeepsOldSite(t *testing.T) {
	b, st := setup(t)
	ctx := context.Background()
	add(t, st, fields.SettingsType, fields.SettingsType, storage.StatusPublished, map[string]any{"baseURL": "https://a.test"})
	add(t, st, fields.PageType, "index", storage.StatusPublished, map[string]any{"title": "A"})
	if _, err := b.Build(ctx); err != nil {
		t.Fatal(err)
	}
	add(t, st, fields.PageType, "index", storage.StatusPublished, map[string]any{"title": "Duplicate"})
	if _, err := b.Build(ctx); err == nil || !strings.Contains(err.Error(), "doppelt") {
		t.Fatalf("want duplicate URL error, got %v", err)
	}
	if !strings.Contains(read(t, b, "index.html"), "<h1>A</h1>") {
		t.Error("previous build was not kept")
	}
}

func TestNestedArticles(t *testing.T) {
	b, st := setup(t)
	page, _ := b.Reg.Schema.Type(fields.PageType)
	data, err := b.Reg.Schema.Validate(page.Fields, map[string]any{"title": "Blog", "slug": "index", "body": []any{
		map[string]any{"_type": "article", "title": "Außen", "children": []any{
			map[string]any{"_type": "richtext", "html": "<p>Text außen</p>"},
			map[string]any{"_type": "article", "title": "Innen", "children": []any{
				map[string]any{"_type": "heading", "text": "Tief"},
			}},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	add(t, st, fields.SettingsType, fields.SettingsType, storage.StatusPublished, map[string]any{"baseURL": "https://a.test"})
	add(t, st, fields.PageType, "index", storage.StatusPublished, data)
	if _, err := b.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	html := strings.Join(strings.Fields(read(t, b, "index.html")), " ")
	if !strings.Contains(html, `<article class="bp-article"><h2>Außen</h2> <div class="bp-richtext"><p>Text außen</p></div> <article class="bp-article"><h2>Innen</h2> <h2>Tief</h2> </article> </article>`) {
		t.Errorf("nested html: %s", html)
	}
	md := read(t, b, "index.md")
	if !strings.Contains(md, "## Außen\n\nText außen\n\n## Innen\n\n## Tief") {
		t.Errorf("nested markdown: %q", md)
	}
}

func TestRelationsResolved(t *testing.T) {
	b, st := setup(t)
	b.Theme = fstest.MapFS{"templates/page.html": {Data: []byte(`{{define "page"}}[{{with .Data.parent}}parent={{.Data.title}}@{{.URL}}{{end}}][{{range .Data.children}}child={{.Data.title}}@{{.URL}};{{end}}]{{end}}`)}}
	add(t, st, fields.SettingsType, fields.SettingsType, storage.StatusPublished, map[string]any{"baseURL": "https://a.test"})
	about := add(t, st, fields.PageType, "ueber-uns", storage.StatusPublished, map[string]any{"title": "Über uns"})
	add(t, st, fields.PageType, "team", storage.StatusPublished, map[string]any{"title": "Team", "parent": about})
	add(t, st, fields.PageType, "entwurf", storage.StatusDraft, map[string]any{"title": "Entwurf", "parent": about})
	if _, err := b.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := read(t, b, "ueber-uns/team/index.html"); got != "[parent=Über uns@/ueber-uns/][]" {
		t.Errorf("child page: %q", got)
	}
	if got := read(t, b, "ueber-uns/index.html"); got != "[][child=Team@/ueber-uns/team/;]" {
		t.Errorf("parent page (inverse, drafts excluded): %q", got)
	}
}

func TestBuildDistDirTrailingSlash(t *testing.T) {
	b, st := setup(t)
	b.DistDir += "/"
	add(t, st, fields.SettingsType, fields.SettingsType, storage.StatusPublished, map[string]any{"baseURL": "https://a.test"})
	add(t, st, fields.PageType, "index", storage.StatusPublished, map[string]any{"title": "A"})
	for i := 0; i < 2; i++ { // second build swaps an existing dist
		if _, err := b.Build(context.Background()); err != nil {
			t.Fatalf("build %d: %v", i, err)
		}
	}
	read(t, b, "index.html")
}

func TestMarkdownBlocksInGroupsAndLists(t *testing.T) {
	b, st := setup(t)
	b.Reg.ContentType(content.Type{Name: "landing", Routable: true, Fields: fields.RoutableFields(
		content.Field{Name: "hero", Kind: fields.Group, Fields: []content.Field{{Name: "blocks", Kind: fields.Blocks}}},
		content.Field{Name: "sections", Kind: fields.List, Fields: []content.Field{{Name: "blocks", Kind: fields.Blocks}}},
	)})
	b.Reg.Store.UseSchema(context.Background(), b.Reg.Schema)
	b.Theme = fstest.MapFS{"templates/landing.html": {Data: []byte(`{{define "landing"}}x{{end}}`)}}
	add(t, st, fields.SettingsType, fields.SettingsType, storage.StatusPublished, map[string]any{"baseURL": "https://a.test"})
	add(t, st, "landing", "start", storage.StatusPublished, map[string]any{"title": "L", "slug": "start",
		"hero":     map[string]any{"blocks": []any{map[string]any{"_type": "heading", "text": "Im Hero"}}},
		"sections": []any{map[string]any{"blocks": []any{map[string]any{"_type": "heading", "text": "In Liste"}}}},
	})
	if _, err := b.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	md := read(t, b, "landing/start.md")
	if !strings.Contains(md, "## Im Hero") || !strings.Contains(md, "## In Liste") {
		t.Errorf("markdown: %q", md)
	}
}

// failingStore fails reads of one type, to simulate a broken database.
type failingStore struct {
	storage.Store
	typ string
}

func (f failingStore) GetEntry(ctx context.Context, typ string, id int64) (*storage.Entry, error) {
	if typ == f.typ {
		return nil, errors.New("disk on fire")
	}
	return f.Store.GetEntry(ctx, typ, id)
}

func TestRelationErrorsFailTheBuild(t *testing.T) {
	b, st := setup(t)
	ctx := context.Background()
	b.Reg.ContentType(content.Type{Name: "author", Fields: []content.Field{{Name: "name", Kind: fields.Text}}})
	b.Reg.Extend(fields.PageType, content.Field{Name: "author", Kind: fields.Relation, Ref: "author"})
	st.UseSchema(ctx, b.Reg.Schema)
	b.Theme = fstest.MapFS{"templates/page.html": {Data: []byte(`{{define "page"}}[{{with .Data.author}}{{.Data.name}}{{end}}]{{end}}`)}}
	add(t, st, fields.SettingsType, fields.SettingsType, storage.StatusPublished, map[string]any{"baseURL": "https://a.test"})
	ada := add(t, st, "author", "", storage.StatusPublished, map[string]any{"name": "Ada"})
	add(t, st, fields.PageType, "index", storage.StatusPublished, map[string]any{"title": "A", "author": ada})

	if _, err := b.Build(ctx); err != nil {
		t.Fatal(err)
	}
	if got := read(t, b, "index.html"); got != "[Ada]" {
		t.Errorf("resolved: %q", got)
	}

	// A deleted target is not an error.
	if err := st.DeleteEntry(ctx, "author", ada); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Build(ctx); err != nil {
		t.Fatalf("deleted target: %v", err)
	}

	// A storage error is.
	ada2 := add(t, st, "author", "", storage.StatusPublished, map[string]any{"name": "Ada"})
	add(t, st, fields.PageType, "zwei", storage.StatusPublished, map[string]any{"title": "B", "author": ada2})
	b.Reg.Store = failingStore{Store: st, typ: "author"}
	if _, err := b.Build(ctx); err == nil || !strings.Contains(err.Error(), "disk on fire") {
		t.Fatalf("want storage error, got %v", err)
	}
	if read(t, b, "index.html") != "[]" {
		t.Error("a failed build must keep the previous site")
	}
}

func TestDraftRelationTargetsNotRendered(t *testing.T) {
	b, st := setup(t)
	ctx := context.Background()
	b.Reg.ContentType(content.Type{Name: "category", Fields: []content.Field{{Name: "name", Kind: fields.Text}}})
	b.Reg.Extend(fields.PageType,
		content.Field{Name: "category", Kind: fields.Relation, Ref: "category"},
		content.Field{Name: "categories", Kind: fields.Relation, Ref: "category", Many: true})
	if err := st.UseSchema(ctx, b.Reg.Schema); err != nil {
		t.Fatal(err)
	}
	b.Theme = fstest.MapFS{"templates/page.html": {Data: []byte(`{{define "page"}}[{{with .Data.category}}{{.Data.name}}{{end}}][{{range .Data.categories}}{{.Data.name}};{{end}}]{{end}}`)}}
	add(t, st, fields.SettingsType, fields.SettingsType, storage.StatusPublished, map[string]any{"baseURL": "https://a.test"})
	draft := add(t, st, "category", "", storage.StatusDraft, map[string]any{"name": "Geheim"})
	pub := add(t, st, "category", "", storage.StatusPublished, map[string]any{"name": "News"})
	add(t, st, fields.PageType, "index", storage.StatusPublished, map[string]any{"title": "A", "category": draft, "categories": []any{draft, pub}})

	if _, err := b.Build(ctx); err != nil {
		t.Fatal(err)
	}
	if got := read(t, b, "index.html"); got != "[][News;]" {
		t.Errorf("draft relation target rendered: %q", got)
	}
}

func TestPublicFiles(t *testing.T) {
	b, st := setup(t)
	ctx := context.Background()
	tmpl := theme["templates/page.html"]
	b.Theme = fstest.MapFS{
		"templates/page.html":             tmpl,
		"public/favicon.ico":              {Data: []byte("ico")},
		"public/.well-known/security.txt": {Data: []byte("Contact: x")},
	}
	b.Reg.Public(fstest.MapFS{"google123.html": {Data: []byte("verify")}})
	add(t, st, fields.SettingsType, fields.SettingsType, storage.StatusPublished, map[string]any{"baseURL": "https://a.test"})
	add(t, st, fields.PageType, "index", storage.StatusPublished, map[string]any{"title": "A"})

	if _, err := b.Build(ctx); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]string{"favicon.ico": "ico", ".well-known/security.txt": "Contact: x", "google123.html": "verify"} {
		if got := read(t, b, p); got != want {
			t.Errorf("%s = %q, want %q", p, got, want)
		}
	}

	// A path the build already uses fails the build instead of being overwritten.
	for name, f := range map[string]fstest.MapFS{
		"generated file":  {"robots.txt": {Data: []byte("mine")}},
		"page":            {"index.html": {Data: []byte("mine")}},
		"page directory":  {"ueber-uns": {Data: []byte("mine")}},
		"plugin vs theme": {"favicon.ico": {Data: []byte("other")}},
	} {
		t.Run(name, func(t *testing.T) {
			b, st := setup(t)
			b.Theme = fstest.MapFS{"templates/page.html": tmpl, "public/favicon.ico": {Data: []byte("ico")}}
			b.Reg.Public(f)
			add(t, st, fields.SettingsType, fields.SettingsType, storage.StatusPublished, map[string]any{"baseURL": "https://a.test"})
			add(t, st, fields.PageType, "index", storage.StatusPublished, map[string]any{"title": "A"})
			add(t, st, fields.PageType, "ueber-uns", storage.StatusPublished, map[string]any{"title": "B"})
			_, err := b.Build(ctx)
			var m i18n.Msg
			if !errors.As(err, &m) || m.Key != MsgPublicConflict {
				t.Fatalf("want conflict, got %v", err)
			}
			if _, err := os.Stat(b.DistDir); !errors.Is(err, fs.ErrNotExist) {
				t.Error("a failed build must not publish a site")
			}
		})
	}
}

func TestNoPublicDir(t *testing.T) {
	b, st := setup(t) // theme without public/
	add(t, st, fields.SettingsType, fields.SettingsType, storage.StatusPublished, map[string]any{"baseURL": "https://a.test"})
	if _, err := b.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestTypePath(t *testing.T) {
	b, st := setup(t)
	ctx := context.Background()
	b.Reg.ContentType(content.Type{Name: "post", Routable: true, Path: "/blog/{slug}/", Fields: fields.RoutableFields()})
	b.Reg.ContentType(content.Type{Name: "doc", Routable: true, Path: "/docs/{parents}/{slug}/", Fields: fields.RoutableFields(
		content.Field{Name: "parent", Kind: fields.Relation, Ref: "doc"},
	)})
	b.Reg.Extend(fields.PageType, content.Field{Name: "featured", Kind: fields.Relation, Ref: "post"})
	if err := st.UseSchema(ctx, b.Reg.Schema); err != nil {
		t.Fatal(err)
	}
	tmpl := []byte(`{{define "x"}}{{with .Data.featured}}{{.URL}}{{end}}{{end}}`)
	b.Theme = fstest.MapFS{
		"templates/page.html": {Data: []byte(`{{define "page"}}{{with .Data.featured}}{{.URL}}{{end}}{{end}}`)},
		"templates/post.html": {Data: tmpl},
		"templates/doc.html":  {Data: tmpl},
	}
	add(t, st, fields.SettingsType, fields.SettingsType, storage.StatusPublished, map[string]any{"baseURL": "https://a.test"})
	post := add(t, st, "post", "hallo", storage.StatusPublished, map[string]any{"title": "Hallo"})
	add(t, st, fields.PageType, "index", storage.StatusPublished, map[string]any{"title": "Start", "featured": post})
	guide := add(t, st, "doc", "guide", storage.StatusPublished, map[string]any{"title": "Guide"})
	draft := add(t, st, "doc", "entwurf", storage.StatusDraft, map[string]any{"title": "Entwurf", "parent": guide})
	add(t, st, "doc", "install", storage.StatusPublished, map[string]any{"title": "Install", "parent": guide})
	add(t, st, "doc", "linux", storage.StatusPublished, map[string]any{"title": "Linux", "parent": draft})

	if _, err := b.Build(ctx); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"blog/hallo/index.html", "blog/hallo.md", "docs/guide/install/index.html", "docs/linux/index.html"} {
		read(t, b, p)
	}
	if got := read(t, b, "index.html"); got != "/blog/hallo/" {
		t.Errorf("relation URL: %q", got)
	}
	if sm := read(t, b, "sitemap.xml"); !strings.Contains(sm, "https://a.test/blog/hallo/") || !strings.Contains(sm, "https://a.test/docs/guide/install/") {
		t.Errorf("sitemap: %s", sm)
	}

	// A parent cycle fails the build.
	a := add(t, st, "doc", "a", storage.StatusPublished, map[string]any{"title": "A"})
	c := add(t, st, "doc", "c", storage.StatusPublished, map[string]any{"title": "C", "parent": a})
	e, _ := st.GetEntry(ctx, "doc", a)
	e.Data["parent"] = c
	if err := st.UpdateEntry(ctx, e); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Build(ctx); err == nil || !strings.Contains(err.Error(), "zyklisch") {
		t.Fatalf("want cycle error, got %v", err)
	}
}

func TestDatesUseSiteTimezone(t *testing.T) {
	b, st := setup(t)
	ctx := context.Background()
	b.Reg.ContentType(content.Type{Name: "event", Fields: []content.Field{{Name: "name", Kind: fields.Text}, {Name: "when", Kind: fields.DateTime}}})
	st.UseSchema(ctx, b.Reg.Schema)
	b.Theme = fstest.MapFS{"templates/page.html": {Data: []byte(`{{define "page"}}{{range entries "event" "sort" "when"}}{{.Data.name}} {{date .Data.when "datetime"}};{{end}}{{end}}`)}}
	add(t, st, fields.SettingsType, fields.SettingsType, storage.StatusPublished, map[string]any{"baseURL": "https://a.test", "language": "de", "timezone": "America/New_York"})
	add(t, st, "event", "", storage.StatusPublished, map[string]any{"name": "B", "when": int64(1714600800)}) // 2024-05-01 22:00 UTC
	add(t, st, "event", "", storage.StatusPublished, map[string]any{"name": "A", "when": int64(1714514400)}) // 2024-04-30 22:00 UTC
	add(t, st, fields.PageType, "index", storage.StatusPublished, map[string]any{"title": "S"})
	if _, err := b.Build(ctx); err != nil {
		t.Fatal(err)
	}
	if got := read(t, b, "index.html"); got != "A 30. April 2024, 18:00;B 1. Mai 2024, 18:00;" {
		t.Errorf("got %q", got)
	}
}

func TestOutputTemplates(t *testing.T) {
	b, st := setup(t)
	ctx := context.Background()
	b.Theme = fstest.MapFS{
		"templates/page.html":              theme["templates/page.html"],
		"templates/output/robots.txt":      {Data: []byte("User-agent: *\nDisallow: /\n")},
		"templates/output/sitemap.xml":     {Data: []byte("  \n")}, // empty: no file
		"templates/output/feed/titles.txt": {Data: []byte(`{{range entries "page" "sort" "slug"}}{{.Data.title}};{{end}}`)},
		"templates/output/plugin.txt":      {Data: []byte("theme")},
		"templates/markdown.md":            {Data: []byte(`{{if ne .URL "/intern/"}}# {{.Data.title}}{{end}}`)},
	}
	b.Reg.Templates(fstest.MapFS{
		"output/plugin.txt": {Data: []byte("plugin")},
		"output/extra.txt":  {Data: []byte("from {{.Site.siteName}}")},
	})
	add(t, st, fields.SettingsType, fields.SettingsType, storage.StatusPublished, map[string]any{"siteName": "A & B", "baseURL": "https://a.test"})
	add(t, st, fields.PageType, "index", storage.StatusPublished, map[string]any{"title": "Start & Ziel"})
	add(t, st, fields.PageType, "intern", storage.StatusPublished, map[string]any{"title": "Intern"})
	if _, err := b.Build(ctx); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]string{
		"robots.txt":      "User-agent: *\nDisallow: /\n",
		"feed/titles.txt": "Start & Ziel;Intern;", // text, not HTML-escaped
		"plugin.txt":      "theme",
		"extra.txt":       "from A & B",
		"index.md":        "# Start & Ziel",
	} {
		if got := read(t, b, p); got != want {
			t.Errorf("%s = %q, want %q", p, got, want)
		}
	}
	for _, p := range []string{"sitemap.xml", "intern.md", "llms.txt"} {
		if _, err := os.Stat(filepath.Join(b.DistDir, p)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s must not be written", p)
		}
	}
	if _, err := os.Stat(filepath.Join(b.DistDir, "intern/index.html")); err != nil {
		t.Error("an empty Markdown variant must not drop the page")
	}
}

func TestOutputTemplateError(t *testing.T) {
	b, st := setup(t)
	b.Theme = fstest.MapFS{"templates/page.html": theme["templates/page.html"], "templates/output/x.txt": {Data: []byte(`{{entries "nope"}}`)}}
	add(t, st, fields.SettingsType, fields.SettingsType, storage.StatusPublished, map[string]any{"baseURL": "https://a.test"})
	if _, err := b.Build(context.Background()); err == nil || !strings.Contains(err.Error(), "output x.txt") {
		t.Fatalf("want output error, got %v", err)
	}
}

func TestGeneratorsDoNotOverwrite(t *testing.T) {
	b, st := setup(t)
	b.Reg.Generator(func(s plugin.Site) error { return s.WriteFile("feed.xml", []byte("ok")) })
	b.Reg.Generator(func(s plugin.Site) error { return s.WriteFile("robots.txt", []byte("mine")) })
	add(t, st, fields.SettingsType, fields.SettingsType, storage.StatusPublished, map[string]any{"baseURL": "https://a.test"})
	_, err := b.Build(context.Background())
	var m i18n.Msg
	if !errors.As(err, &m) || m.Key != MsgPathConflict || m.Args[0] != "/robots.txt" {
		t.Fatalf("want path conflict for /robots.txt, got %v", err)
	}
}

func TestLayouts(t *testing.T) {
	b, st := setup(t)
	ctx := context.Background()
	b.Reg.ContentType(content.Type{Name: "landing", Routable: true, Layout: "bare", Fields: fields.RoutableFields()})
	b.Reg.ContentType(content.Type{Name: "raw", Routable: true, Layout: "-", Fields: fields.RoutableFields()})
	b.Reg.ContentType(content.Type{Name: "broken", Routable: true, Layout: "nope", Fields: fields.RoutableFields()})
	st.UseSchema(ctx, b.Reg.Schema)
	b.Theme = fstest.MapFS{
		"templates/layout.html":  {Data: []byte(`{{define "layout"}}<title>{{.Page.Meta.Title}}</title><main>{{.Content}}</main>{{end}}`)},
		"templates/bare.html":    {Data: []byte(`{{define "bare"}}<body class="bare">{{.Content}}</body>{{end}}`)},
		"templates/page.html":    {Data: []byte(`{{define "page"}}<h1>{{.Data.title}}</h1>{{end}}`)},
		"templates/landing.html": {Data: []byte(`{{define "landing"}}<h1>{{.Data.title}}</h1>{{end}}`)},
		"templates/raw.html":     {Data: []byte(`{{define "raw"}}raw{{end}}`)},
		"templates/broken.html":  {Data: []byte(`{{define "broken"}}x{{end}}`)},
		"templates/404.html":     {Data: []byte(`{{define "404"}}<p>{{.Data.text}}</p>{{end}}`)},
	}
	b.Reg.Generator(func(s plugin.Site) error {
		return s.RenderPage("404.html", "404", "", &render.Page{Meta: render.Meta{Title: "Weg"}, Data: map[string]any{"text": "weg"}})
	})
	b.Reg.Generator(func(s plugin.Site) error {
		return s.Render("plain.html", "404", &render.Page{Data: map[string]any{"text": "ohne"}})
	})
	add(t, st, fields.SettingsType, fields.SettingsType, storage.StatusPublished, map[string]any{"siteName": "Acme", "baseURL": "https://a.test"})
	add(t, st, fields.PageType, "index", storage.StatusPublished, map[string]any{"title": "A & B"})
	add(t, st, "landing", "l", storage.StatusPublished, map[string]any{"title": "L"})
	add(t, st, "raw", "r", storage.StatusPublished, map[string]any{"title": "R"})
	if _, err := b.Build(ctx); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]string{
		"index.html":           "<title>A &amp; B | Acme</title><main><h1>A &amp; B</h1></main>", // content not escaped twice
		"landing/l/index.html": `<body class="bare"><h1>L</h1></body>`,
		"raw/r/index.html":     "raw",
		"404.html":             "<title>Weg</title><main><p>weg</p></main>",
		"plain.html":           "<p>ohne</p>",
	} {
		if got := read(t, b, p); got != want {
			t.Errorf("%s = %q, want %q", p, got, want)
		}
	}

	add(t, st, "broken", "x", storage.StatusPublished, map[string]any{"title": "X"})
	if _, err := b.Build(ctx); err == nil || !strings.Contains(err.Error(), `layout "nope" not defined`) {
		t.Fatalf("want missing layout error, got %v", err)
	}
}

func TestSwapRestoresOnFailure(t *testing.T) {
	dir := t.TempDir()
	dist := filepath.Join(dir, "dist")
	os.MkdirAll(dist, 0o755)
	os.WriteFile(filepath.Join(dist, "index.html"), []byte("old"), 0o644)
	if err := swap(filepath.Join(dir, "missing.tmp"), dist); err == nil {
		t.Fatal("want error for a missing build")
	}
	if b, err := os.ReadFile(filepath.Join(dist, "index.html")); err != nil || string(b) != "old" {
		t.Errorf("previous site not restored: %q %v", b, err)
	}

	tmp := filepath.Join(dir, "new.tmp")
	os.MkdirAll(tmp, 0o755)
	os.WriteFile(filepath.Join(tmp, "index.html"), []byte("new"), 0o644)
	if err := swap(tmp, dist); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dist, "index.html")); string(b) != "new" {
		t.Errorf("swap: %q", b)
	}
	if _, err := os.Stat(dist + ".old"); !errors.Is(err, fs.ErrNotExist) {
		t.Error("old site left behind")
	}
}
