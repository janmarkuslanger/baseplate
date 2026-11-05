package build

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/plugin"
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
	if err := reg.Use(fields.Core{}); err != nil {
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
	if !strings.Contains(md, "# Willkommen") || !strings.Contains(md, "## Hallo <Welt>") || !strings.Contains(md, "Text **fett**") {
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
