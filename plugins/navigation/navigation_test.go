package navigation

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/janmarkuslanger/baseplate/build"
	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/entries"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/storage"
	"github.com/janmarkuslanger/baseplate/storage/sqlite"
)

func setup(t *testing.T) *plugin.Registry {
	t.Helper()
	ctx := context.Background()
	st, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	reg := plugin.NewRegistry(plugin.Services{Store: st, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := reg.Use(fields.Core{}, fields.CoreBlocks{}, New()); err != nil {
		t.Fatal(err)
	}
	if err := reg.Schema.Check(); err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx, append(storage.CoreMigrations, reg.Migrations()...)); err != nil {
		t.Fatal(err)
	}
	if err := st.UseSchema(ctx, reg.Schema); err != nil {
		t.Fatal(err)
	}
	save(t, reg, fields.SettingsType, map[string]any{"siteName": "Acme", "baseURL": "https://acme.test"}, true)
	return reg
}

func save(t *testing.T, reg *plugin.Registry, typ string, data map[string]any, publish bool) int64 {
	t.Helper()
	res, err := reg.Entries.Save(context.Background(), entries.Save{Type: typ, Data: data, Publish: publish})
	if err != nil {
		t.Fatalf("save %s: %v", typ, err)
	}
	return res.Entry.ID
}

func link(label string, target map[string]any, children ...any) map[string]any {
	m := map[string]any{"_type": LinkBlock, "label": label, "children": children}
	for k, v := range target {
		m[k] = v
	}
	return m
}

const theme = `{{define "page"}}{{template "menu" (navigation "main" (entries "navigation") .)}}|footer:{{len (navigation "missing" (entries "navigation") .)}}{{end}}
{{define "menu"}}<ul>{{range .}}<li{{if .Active}} class="active"{{end}}><a href="{{.URL}}"{{if .Current}} aria-current="page"{{end}}>{{.Title}}</a>{{if .Children}}{{template "menu" .Children}}{{end}}</li>{{end}}</ul>{{end}}`

func TestNavigationInBuild(t *testing.T) {
	reg := setup(t)
	ctx := context.Background()
	home := save(t, reg, fields.PageType, map[string]any{"title": "Start", "slug": "index"}, true)
	kurse := save(t, reg, fields.PageType, map[string]any{"title": "Kurse", "slug": "kurse"}, true)
	yoga := save(t, reg, fields.PageType, map[string]any{"title": "Yoga", "slug": "yoga", "parent": kurse}, true)
	draft := save(t, reg, fields.PageType, map[string]any{"title": "Geheim", "slug": "geheim"}, false)

	save(t, reg, TypeName, map[string]any{"name": "Hauptmenü", "key": "main", "items": []any{
		link("Start", map[string]any{"page": home}),
		link("Kurse", map[string]any{"page": kurse},
			link("Yoga", map[string]any{"page": yoga},
				link("Termine", map[string]any{"page": yoga, "anchor": "termine"}),
				link("Extern", map[string]any{"url": "https://extern.test/"}))),
		link("Mehr", nil,
			link("Geheim", map[string]any{"page": draft}, link("Kind", map[string]any{"url": "/kind/"}))),
	}}, true)

	dist := filepath.Join(t.TempDir(), "dist")
	b := &build.Builder{Reg: reg, DistDir: dist, Theme: fstest.MapFS{"templates/page.html": {Data: []byte(theme)}}}
	if _, err := b.Build(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dist, "kurse", "yoga", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	html := string(got)
	for _, want := range []string{
		`<li><a href="/">Start</a></li>`,                                         // home is no prefix of every page
		`<li class="active"><a href="/kurse/">Kurse</a>`,                         // ancestor of the current link
		`<li class="active"><a href="/kurse/yoga/" aria-current="page">Yoga</a>`, // the page itself
		`<li><a href="/kurse/yoga/#termine">Termine</a></li>`,                    // anchors are never current
		`<li><a href="https://extern.test/">Extern</a></li>`,                     // third level
		`<li><a href="">Mehr</a></li>`,                                           // heading whose only child is hidden
		`|footer:0`,                                                              // unknown key: no links
	} {
		if !strings.Contains(html, want) {
			t.Errorf("menu lacks %s\n%s", want, html)
		}
	}
	for _, hidden := range []string{"Geheim", "Kind"} {
		if strings.Contains(html, hidden) {
			t.Errorf("link %q to an unpublished page (or its child) is rendered", hidden)
		}
	}
}

func TestKeyUnique(t *testing.T) {
	reg := setup(t)
	id := save(t, reg, TypeName, map[string]any{"name": "Haupt", "key": "main"}, true)
	_, err := reg.Entries.Save(context.Background(), entries.Save{Type: TypeName, Data: map[string]any{"name": "Zweite", "key": "main"}})
	var errs content.Errors
	if !errors.As(err, &errs) || errs["_"] == nil {
		t.Fatalf("second navigation with key main: err = %v, want a validation error", err)
	}
	// Saving the navigation itself again keeps its key.
	if _, err := reg.Entries.Save(context.Background(), entries.Save{Type: TypeName, ID: id, Data: map[string]any{"name": "Haupt", "key": "main"}, Publish: true}); err != nil {
		t.Fatalf("resave: %v", err)
	}
}

func TestLinkBlockOnlyInNavigations(t *testing.T) {
	reg := setup(t)
	_, err := reg.Entries.Save(context.Background(), entries.Save{Type: fields.PageType, Data: map[string]any{
		"title": "A", "slug": "a", "body": []any{link("x", map[string]any{"url": "/x/"})},
	}})
	var errs content.Errors
	if !errors.As(err, &errs) {
		t.Fatalf("navlink in a page body: err = %v, want a validation error", err)
	}
}
