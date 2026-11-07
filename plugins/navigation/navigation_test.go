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

// link saves a published link entry and returns its ID.
func link(t *testing.T, reg *plugin.Registry, label string, target map[string]any, children ...int64) int64 {
	t.Helper()
	data := map[string]any{"label": label, "children": ids(children...)}
	for k, v := range target {
		data[k] = v
	}
	return save(t, reg, LinkType, data, true)
}

func ids(in ...int64) []any {
	out := []any{}
	for _, id := range in {
		out = append(out, id)
	}
	return out
}

const theme = `{{define "page"}}{{template "menu" (navigation "main" .)}}|missing:{{len (navigation "missing" .)}}{{end}}
{{define "menu"}}<ul>{{range .}}<li{{if .Active}} class="active"{{end}}><a href="{{.URL}}"{{if .Current}} aria-current="page"{{end}}>{{.Title}}</a>{{if .Children}}{{template "menu" .Children}}{{end}}</li>{{end}}</ul>{{end}}`

func TestNavigationInBuild(t *testing.T) {
	reg := setup(t)
	ctx := context.Background()
	home := save(t, reg, fields.PageType, map[string]any{"title": "Start", "slug": "index"}, true)
	kurse := save(t, reg, fields.PageType, map[string]any{"title": "Kurse", "slug": "kurse"}, true)
	yoga := save(t, reg, fields.PageType, map[string]any{"title": "Yoga", "slug": "yoga", "parent": kurse}, true)
	draftPage := save(t, reg, fields.PageType, map[string]any{"title": "Geheim", "slug": "geheim"}, false)

	termine := link(t, reg, "Termine", map[string]any{"url": "/kurse/yoga/#termine"})
	extern := link(t, reg, "Extern", map[string]any{"url": "https://extern.test/"})
	yogaLink := link(t, reg, "Yoga", map[string]any{"page": yoga}, termine, extern)
	kind := link(t, reg, "Kind", map[string]any{"url": "/kind/"})
	geheim := link(t, reg, "Geheim", map[string]any{"page": draftPage}, kind)
	entwurf := save(t, reg, LinkType, map[string]any{"label": "Entwurf", "url": "/entwurf/"}, false)
	save(t, reg, TypeName, map[string]any{"name": "Hauptmenü", "key": "main", "items": ids(
		link(t, reg, "Start", map[string]any{"page": home}),
		link(t, reg, "Kurse", map[string]any{"page": kurse}, yogaLink),
		link(t, reg, "Mehr", nil, geheim),
		entwurf,
	)}, true)

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
		`<li><a href="/">Start</a></li>`,                 // home is no prefix of every page
		`<li class="active"><a href="/kurse/">Kurse</a>`, // ancestor of the current link
		`<li class="active"><a href="/kurse/yoga/" aria-current="page">Yoga</a>`,
		`<li><a href="/kurse/yoga/#termine">Termine</a></li>`, // anchors are never current
		`<li><a href="https://extern.test/">Extern</a></li>`,  // third level
		`<li><a href="">Mehr</a></li>`,                        // heading whose only child is hidden
		`|missing:0`,                                          // unknown key: no links
	} {
		if !strings.Contains(html, want) {
			t.Errorf("menu lacks %s\n%s", want, html)
		}
	}
	for _, hidden := range []string{"Geheim", "Kind", "Entwurf"} {
		if strings.Contains(html, hidden) {
			t.Errorf("%q is rendered: links to unpublished pages, their children and draft links must be left out", hidden)
		}
	}
}

// A link that contains itself further down must not loop.
func TestNavigationCycle(t *testing.T) {
	reg := setup(t)
	a := link(t, reg, "A", map[string]any{"url": "/a/"})
	bID := link(t, reg, "B", map[string]any{"url": "/b/"}, a)
	if _, err := reg.Entries.Save(context.Background(), entries.Save{Type: LinkType, ID: a, Publish: true,
		Data: map[string]any{"label": "A", "url": "/a/", "children": ids(bID)}}); err != nil {
		t.Fatal(err)
	}
	save(t, reg, TypeName, map[string]any{"name": "N", "key": "main", "items": ids(a)}, true)
	navigation := reg.TemplateFuncs()["navigation"].(func(string, any) ([]*Link, error))
	links, err := navigation("main", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || len(links[0].Children) != 1 || len(links[0].Children[0].Children) != 0 {
		t.Errorf("cycle A → B → A: got %d links, want A → B only", len(links))
	}
}

func TestLinkURLOrPage(t *testing.T) {
	reg := setup(t)
	page := save(t, reg, fields.PageType, map[string]any{"title": "A", "slug": "a"}, true)
	_, err := reg.Entries.Save(context.Background(), entries.Save{Type: LinkType, Data: map[string]any{"label": "x", "url": "/x/", "page": page}})
	var errs content.Errors
	if !errors.As(err, &errs) {
		t.Fatalf("link with URL and page: err = %v, want a validation error", err)
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
