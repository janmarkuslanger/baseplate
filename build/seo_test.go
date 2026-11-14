package build

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/storage"
)

func TestCanonicalURLs(t *testing.T) {
	b, st := setup(t)
	add(t, st, fields.SettingsType, fields.SettingsType, storage.Published, map[string]any{"baseURL": "https://a.test"})
	canon := func(c string) map[string]any { return map[string]any{"canonical": c} }
	add(t, st, fields.PageType, "plain", storage.Published, map[string]any{"title": "Plain"})
	add(t, st, fields.PageType, "rel", storage.Published, map[string]any{"title": "Rel", "seo": canon("/plain/")})
	add(t, st, fields.PageType, "self", storage.Published, map[string]any{"title": "Self", "seo": canon("https://a.test/self/")})
	add(t, st, fields.PageType, "self-rel", storage.Published, map[string]any{"title": "SelfRel", "seo": canon("/self-rel/")})
	add(t, st, fields.PageType, "foreign", storage.Published, map[string]any{"title": "Foreign", "seo": canon("https://x.test/orig/")})
	if _, err := b.Build(context.Background()); err != nil {
		t.Fatal(err)
	}

	for page, want := range map[string]string{
		"plain":    "https://a.test/plain/",
		"rel":      "https://a.test/plain/",
		"self":     "https://a.test/self/",
		"self-rel": "https://a.test/self-rel/",
		"foreign":  "https://x.test/orig/",
	} {
		html := read(t, b, page+"/index.html")
		if !strings.Contains(html, `<link rel="canonical" href="`+want+`">`) || !strings.Contains(html, `<meta property="og:url" content="`+want+`">`) {
			t.Errorf("%s: want canonical and og:url %s in\n%s", page, want, html)
		}
	}

	sitemap := read(t, b, "sitemap.xml")
	for page, in := range map[string]bool{"plain": true, "self": true, "self-rel": true, "rel": false, "foreign": false} {
		if got := strings.Contains(sitemap, "<loc>https://a.test/"+page+"/</loc>"); got != in {
			t.Errorf("%s in sitemap: %v, want %v\n%s", page, got, in, sitemap)
		}
	}
}

// Values saved before the admin validated them fail the build with a
// message instead of producing relative URLs.
func TestInvalidStoredSEOURLsFailTheBuild(t *testing.T) {
	b, st := setup(t)
	ctx := context.Background()
	settingsID := add(t, st, fields.SettingsType, fields.SettingsType, storage.Published, map[string]any{"baseURL": "/site"})
	if _, err := b.Build(ctx); !errors.Is(err, ErrBaseURLInvalid) {
		t.Fatalf("relative base URL: want ErrBaseURLInvalid, got %v", err)
	}

	e, err := st.GetEntry(ctx, fields.SettingsType, settingsID, "", storage.Draft)
	if err != nil {
		t.Fatal(err)
	}
	e.Data["baseURL"] = "https://a.test"
	if err := st.SaveDraft(ctx, e); err != nil {
		t.Fatal(err)
	}
	if err := st.Publish(ctx, fields.SettingsType, settingsID, ""); err != nil {
		t.Fatal(err)
	}
	add(t, st, fields.PageType, "x", storage.Published, map[string]any{"title": "X", "seo": map[string]any{"canonical": "mailto:a@a.test"}})
	if _, err := b.Build(ctx); err == nil || !strings.Contains(err.Error(), "/x/") || !strings.Contains(err.Error(), "mailto:a@a.test") {
		t.Fatalf("invalid canonical: want an error naming page and value, got %v", err)
	}
}

// A type with a custom TitleField uses that field as the page title.
func TestCustomTitleField(t *testing.T) {
	b, st := setup(t)
	ctx := context.Background()
	b.Reg.ContentType(content.Type{Name: "person", Routable: true, Path: "/people/{slug}/", TitleField: "name", Fields: []content.Field{
		{Name: "name", Kind: fields.Text},
		{Name: "slug", Kind: fields.Slug},
		fields.SEOField(),
	}})
	if err := st.UseSchema(ctx, b.Reg.Schema); err != nil {
		t.Fatal(err)
	}
	b.Theme = fstest.MapFS{"templates/person.html": {Data: []byte(`{{define "person"}}{{template "baseplate/head" .}}{{end}}`)}}
	add(t, st, fields.SettingsType, fields.SettingsType, storage.Published, map[string]any{"baseURL": "https://a.test", "siteName": "Site"})
	add(t, st, "person", "ada", storage.Published, map[string]any{"name": "Ada"})
	if _, err := b.Build(ctx); err != nil {
		t.Fatal(err)
	}
	if html := read(t, b, "people/ada/index.html"); !strings.Contains(html, "<title>Ada | Site</title>") {
		t.Errorf("want the name field as title in\n%s", html)
	}

	add(t, st, "person", "ada", storage.Published, map[string]any{"name": "Grace"})
	if _, err := b.Build(ctx); err == nil || !strings.Contains(err.Error(), "Ada") || !strings.Contains(err.Error(), "Grace") {
		t.Fatalf("duplicate URL error must name both entries, got %v", err)
	}
}
