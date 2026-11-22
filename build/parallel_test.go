package build

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/i18n"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/storage"
)

// Pages render in parallel; "entries" shares the build's resolver, which
// loads and caches lazily. Run with -race.
func TestParallelPagesShareEntries(t *testing.T) {
	b, st := setup(t)
	b.Theme = fstest.MapFS{"templates/page.html": {Data: []byte(`{{define "page"}}<h1>{{.Data.title}}</h1>{{range entries "page" "sort" "title" "limit" 2}}<a href="{{.URL}}">{{.Data.title}}</a>{{end}}{{end}}`)}}
	add(t, st, fields.SettingsType, fields.SettingsType, storage.Published, map[string]any{"baseURL": "https://a.test"})
	for i := range 40 {
		add(t, st, fields.PageType, fmt.Sprintf("p-%02d", i), storage.Published, map[string]any{"title": fmt.Sprintf("P %02d", i)})
	}
	res, err := b.Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Pages != 40 {
		t.Fatalf("pages = %d, want 40", res.Pages)
	}
	for i := range 40 {
		html, err := os.ReadFile(filepath.Join(b.DistDir, fmt.Sprintf("p-%02d", i), "index.html"))
		if err != nil {
			t.Fatal(err)
		}
		if want := `<a href="/p-00/">P 00</a><a href="/p-01/">P 01</a>`; !strings.Contains(string(html), want) {
			t.Fatalf("p-%02d: %s, want the list %s", i, html, want)
		}
	}
}

// With several failing pages the build reports the first in URL order, as
// a build rendering one page after another does.
func TestFirstFailingPageReported(t *testing.T) {
	b, st := setup(t)
	b.Theme = fstest.MapFS{"templates/page.html": {Data: []byte(`{{define "page"}}{{if eq .Data.title "bad"}}{{entries "nope"}}{{end}}{{end}}`)}}
	add(t, st, fields.SettingsType, fields.SettingsType, storage.Published, map[string]any{"baseURL": "https://a.test"})
	for i := range 40 {
		title := "ok"
		if i == 7 || i == 31 {
			title = "bad"
		}
		add(t, st, fields.PageType, fmt.Sprintf("p-%02d", i), storage.Published, map[string]any{"title": title})
	}
	for range 10 {
		_, err := b.Build(context.Background())
		if err == nil || !strings.HasPrefix(err.Error(), "/p-07/: ") {
			t.Fatalf("want the error of /p-07/, got %v", err)
		}
	}
	if _, err := os.Stat(b.DistDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a failed build must not create the site: %v", err)
	}
}

// A generator must not overwrite a copied asset or media file, nor write a
// file where a page's directory is.
func TestGeneratorsDoNotOverwriteCopies(t *testing.T) {
	for _, p := range []string{"assets/site.css", "media/photo.png", "a"} {
		t.Run(p, func(t *testing.T) {
			b, st := setup(t)
			if err := os.MkdirAll(b.MediaDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(b.MediaDir, "photo.png"), []byte("png"), 0o644); err != nil {
				t.Fatal(err)
			}
			b.Reg.Generator(func(s plugin.Site) error { return s.WriteFile(p, []byte("mine")) })
			add(t, st, fields.SettingsType, fields.SettingsType, storage.Published, map[string]any{"baseURL": "https://a.test"})
			add(t, st, fields.PageType, "a", storage.Published, map[string]any{"title": "A"})
			_, err := b.Build(context.Background())
			var m i18n.Msg
			if !errors.As(err, &m) || m.Key != MsgPathConflict || m.Args[0] != "/"+p {
				t.Fatalf("want path conflict for /%s, got %v", p, err)
			}
			if got, _ := os.ReadFile(filepath.Join(b.MediaDir, "photo.png")); string(got) != "png" {
				t.Errorf("media original changed to %q", got)
			}
		})
	}
}
