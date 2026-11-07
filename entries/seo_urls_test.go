package entries_test

import (
	"context"
	"errors"
	"testing"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/entries"
	"github.com/janmarkuslanger/baseplate/fields"
)

// fields.Core rejects URLs the build cannot turn into absolute ones.
func TestSaveRejectsNonAbsoluteSEOURLs(t *testing.T) {
	svc, _ := service(t)
	ctx := context.Background()
	for _, c := range []struct {
		name, typ string
		data      map[string]any
		ok        bool
	}{
		{"absolute base URL", fields.SettingsType, map[string]any{"siteName": "A", "baseURL": "https://a.test/"}, true},
		{"relative base URL", fields.SettingsType, map[string]any{"siteName": "A", "baseURL": "/site"}, false},
		{"root as base URL", fields.SettingsType, map[string]any{"siteName": "A", "baseURL": "/"}, false},
		{"mailto as base URL", fields.SettingsType, map[string]any{"siteName": "A", "baseURL": "mailto:a@a.test"}, false},
		{"site-relative canonical", fields.PageType, map[string]any{"title": "T", "slug": "a", "seo": map[string]any{"canonical": "/other/"}}, true},
		{"absolute canonical", fields.PageType, map[string]any{"title": "T", "slug": "b", "seo": map[string]any{"canonical": "https://x.test/b/"}}, true},
		{"mailto canonical", fields.PageType, map[string]any{"title": "T", "slug": "c", "seo": map[string]any{"canonical": "mailto:a@a.test"}}, false},
		{"protocol-relative canonical", fields.PageType, map[string]any{"title": "T", "slug": "d", "seo": map[string]any{"canonical": "//x.test/"}}, false},
	} {
		_, err := svc.Save(ctx, entries.Save{Type: c.typ, Data: c.data})
		var errs content.Errors
		switch {
		case c.ok && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case !c.ok && (!errors.As(err, &errs) || len(errs) == 0):
			t.Errorf("%s: want a validation error, got %v", c.name, err)
		}
	}
}
