package entries_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/janmarkuslanger/baseplate/entries"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/storage"
	"github.com/janmarkuslanger/baseplate/storage/sqlite"
)

// BenchmarkSave measures saving and publishing one page while N published
// pages exist. The cost should not grow with N.
// Run: go test ./entries -run '^$' -bench Save
func BenchmarkSave(b *testing.B) {
	for _, n := range []int{1_000, 10_000} {
		b.Run(fmt.Sprintf("pages=%d", n), func(b *testing.B) {
			if testing.Short() && n > 1_000 {
				b.Skip("short mode")
			}
			svc := seededService(b, n)
			ctx := context.Background()
			data := benchPage("edited")
			res, err := svc.Save(ctx, entries.Save{Type: fields.PageType, Data: data, Publish: true})
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := svc.Save(ctx, entries.Save{Type: fields.PageType, ID: res.Entry.ID, Data: data, Publish: true}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func benchPage(slug string) map[string]any {
	return map[string]any{
		"title": "Page " + slug,
		"slug":  slug,
		"body": []any{
			map[string]any{"_type": "heading", "text": "Heading"},
			map[string]any{"_type": "richtext", "html": "<p>Some <strong>text</strong> for the page.</p>"},
		},
	}
}

// seededService returns a service over a file database (as in production)
// with n published pages.
func seededService(b *testing.B, n int) *entries.Service {
	b.Helper()
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx, storage.CoreMigrations); err != nil {
		b.Fatal(err)
	}
	reg := plugin.NewRegistry(plugin.Services{Store: st})
	if err := reg.Use(fields.Core{}, fields.CoreBlocks{}); err != nil {
		b.Fatal(err)
	}
	if err := st.UseSchema(ctx, reg.Schema); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < n; i++ {
		e := &storage.Entry{Type: fields.PageType, Slug: fmt.Sprintf("page-%d", i), Data: benchPage(fmt.Sprintf("page-%d", i))}
		if err := st.SaveDraft(ctx, e); err != nil {
			b.Fatal(err)
		}
		if err := st.Publish(ctx, fields.PageType, e.ID, ""); err != nil {
			b.Fatal(err)
		}
	}
	return reg.Entries
}
