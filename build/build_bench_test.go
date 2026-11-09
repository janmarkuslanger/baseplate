package build

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/plugins/navigation"
	"github.com/janmarkuslanger/baseplate/storage"
	"github.com/janmarkuslanger/baseplate/storage/sqlite"
)

// benchTheme renders a navigation of a few items on every page, as a real
// theme does. It does not list every page: that would be quadratic by
// construction and measure the theme, not the build.
var benchTheme = fstest.MapFS{
	"templates/page.html": {Data: []byte(`{{define "page"}}<!doctype html><html><head>{{template "baseplate/head" .}}</head><body><nav>{{range navigation "main" .}}<a href="{{.URL}}">{{.Title}}</a>{{end}}</nav><h1>{{.Data.title}}</h1>{{blocks .Data.body .}}</body></html>{{end}}`)},
}

// BenchmarkBuild measures a full build (read from the database, render,
// write) of N published pages. Run: go test ./build -run '^$' -bench Build
func BenchmarkBuild(b *testing.B) {
	for _, n := range []int{1_000, 10_000} {
		b.Run(fmt.Sprintf("pages=%d", n), func(b *testing.B) {
			if testing.Short() && n > 1_000 {
				b.Skip("short mode")
			}
			bd := seededBuilder(b, n)
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				res, err := bd.Build(ctx)
				if err != nil {
					b.Fatal(err)
				}
				if res.Pages != n {
					b.Fatalf("pages = %d, want %d", res.Pages, n)
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(n), "ns/page")
		})
	}
}

// seededBuilder returns a Builder over a file database with n published
// pages (a file database, as in production, not :memory:).
func seededBuilder(b *testing.B, n int) *Builder {
	b.Helper()
	ctx := context.Background()
	dir := b.TempDir()
	st, err := sqlite.Open(filepath.Join(dir, "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { st.Close() })
	st.Migrate(ctx, storage.CoreMigrations)
	reg := plugin.NewRegistry(plugin.Services{Store: st})
	if err := reg.Use(fields.Core{}, fields.CoreBlocks{}, navigation.New()); err != nil {
		b.Fatal(err)
	}
	if err := st.UseSchema(ctx, reg.Schema); err != nil {
		b.Fatal(err)
	}
	seed := func(typ, slug string, data map[string]any) {
		e := &storage.Entry{Type: typ, Slug: slug, Data: data}
		if err := st.SaveDraft(ctx, e); err != nil {
			b.Fatal(err)
		}
		if err := st.Publish(ctx, typ, e.ID, ""); err != nil {
			b.Fatal(err)
		}
	}
	seed(fields.SettingsType, fields.SettingsType, map[string]any{"siteName": "Bench", "baseURL": "https://bench.test/"})
	var items []any
	for i := 0; i < 5; i++ {
		items = append(items, map[string]any{"_type": navigation.ItemBlock, "title": fmt.Sprintf("Item %d", i), "url": fmt.Sprintf("/item-%d/", i)})
	}
	seed(navigation.TypeName, "main", map[string]any{"name": "Main", "key": "main", "items": items})
	for i := 0; i < n; i++ {
		seed(fields.PageType, fmt.Sprintf("page-%d", i), map[string]any{
			"title": fmt.Sprintf("Page %d", i),
			"seo":   map[string]any{"metaDescription": fmt.Sprintf("Description of page %d", i)},
			"body": []any{
				map[string]any{"_type": "heading", "text": "Heading"},
				map[string]any{"_type": "richtext", "html": "<p>Some <strong>text</strong> for the page.</p>"},
			},
		})
	}
	return &Builder{Reg: reg, Theme: benchTheme, DistDir: filepath.Join(dir, "dist"), MediaDir: filepath.Join(dir, "media")}
}
