package build

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

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
		"siteName": "Acme", "baseURL": "https://acme.test/", "llmsIntro": "Acme baut Dinge.",
	})
	add(t, st, fields.PageType, "index", storage.StatusPublished, map[string]any{
		"title": "Willkommen", "seo": map[string]any{"metaDescription": "Startseite von Acme"},
		"body": []any{
			map[string]any{"_type": "heading", "text": "Hallo <Welt>"},
			map[string]any{"_type": "richtext", "html": "<p>Text <strong>fett</strong></p>"},
		},
	})
	about := add(t, st, fields.PageType, "ueber-uns", storage.StatusPublished, map[string]any{
		"title": "Über uns", "llm": map[string]any{"summary": "Team und Geschichte"},
	})
	add(t, st, fields.PageType, "team", storage.StatusPublished, map[string]any{"title": "Team", "parent": float64(about)})
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
