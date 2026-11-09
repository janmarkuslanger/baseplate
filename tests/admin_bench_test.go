package tests

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/storage"
)

// BenchmarkAdmin measures the admin pages an editor opens while the
// database holds N pages: the list, the editor of a page (with its parent
// picker) and a picker search. None may grow with N.
// Run: go test ./tests -run '^$' -bench Admin
func BenchmarkAdmin(b *testing.B) {
	for _, n := range []int{1_000, 10_000} {
		b.Run(fmt.Sprintf("pages=%d", n), func(b *testing.B) {
			if testing.Short() && n > 1_000 {
				b.Skip("short mode")
			}
			app, _, c := startApp(b, io.Discard)
			c.login()
			ctx := context.Background()
			var last int64
			for i := 0; i < n; i++ {
				e := &storage.Entry{Type: fields.PageType, Slug: fmt.Sprintf("page-%d", i), Data: map[string]any{"title": fmt.Sprintf("Page %d", i)}}
				if err := app.Registry.Store.SaveDraft(ctx, e); err != nil {
					b.Fatal(err)
				}
				last = e.ID
			}
			for name, path := range map[string]string{
				"list":   "/admin/c/page",
				"search": "/admin/c/page/options?q=page+5",
				"edit":   fmt.Sprintf("/admin/c/page/%d", last),
			} {
				b.Run(name, func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						if res, _ := c.do("GET", path, nil); res.StatusCode != http.StatusOK {
							b.Fatalf("GET %s: %d", path, res.StatusCode)
						}
					}
				})
			}
		})
	}
}
