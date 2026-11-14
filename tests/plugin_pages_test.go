package tests

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/janmarkuslanger/baseplate"
	"github.com/janmarkuslanger/baseplate/plugin"
)

// pagePlugin registers one admin page rendering template tmpl.
type pagePlugin struct {
	name, path, tmpl, text string
}

func (p pagePlugin) Name() string { return p.name }
func (p pagePlugin) Register(r *plugin.Registry) error {
	r.AdminTemplates(fstest.MapFS{p.tmpl: {Data: []byte(`{{define "content"}}` + p.text + `{{end}}`)}})
	r.AdminPage(plugin.AdminPage{Path: p.path, Label: p.name, Handler: func(w http.ResponseWriter, r *http.Request, ui plugin.AdminUI) {
		ui.Render(w, r, p.tmpl, nil)
	}})
	return nil
}

// A page path with surrounding slashes gets the same URL in the route, the
// nav link and the active marker.
func TestAdminPagePathIsNormalized(t *testing.T) {
	_, c := newTestApp(t, pagePlugin{name: "demo", path: "/demo/", tmpl: "demo.html", text: "DEMO PAGE"})
	c.login()
	res, html := c.do("GET", "/admin/p/demo", nil)
	if res.StatusCode != http.StatusOK || !strings.Contains(html, "DEMO PAGE") {
		t.Fatalf("page: status %d", res.StatusCode)
	}
	if want := `<a class="nav__item is-active" href="/admin/p/demo">`; !strings.Contains(html, want) {
		t.Errorf("nav lacks the active link %s", want)
	}
}

func TestAdminPageConflictsFailSetup(t *testing.T) {
	cases := map[string][]plugin.Plugin{
		"duplicate path": {
			pagePlugin{name: "a", path: "same", tmpl: "a.html"},
			pagePlugin{name: "b", path: "/same/", tmpl: "b.html"},
		},
		"empty path":          {pagePlugin{name: "a", path: "/", tmpl: "a.html"}},
		"space in path":       {pagePlugin{name: "a", path: "my page", tmpl: "a.html"}},
		"method prefix":       {pagePlugin{name: "a", path: "GET x", tmpl: "a.html"}},
		"wildcard":            {pagePlugin{name: "a", path: "x/{id}", tmpl: "a.html"}},
		"empty segment":       {pagePlugin{name: "a", path: "x//y", tmpl: "a.html"}},
		"dot segment":         {pagePlugin{name: "a", path: "x/../y", tmpl: "a.html"}},
		"non-ascii character": {pagePlugin{name: "a", path: "über", tmpl: "a.html"}},
		"duplicate template": {
			pagePlugin{name: "a", path: "a", tmpl: "page.html", text: "A"},
			pagePlugin{name: "b", path: "b", tmpl: "page.html", text: "B"},
		},
	}
	for name, plugins := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			app := baseplate.New(baseplate.Config{DataDir: filepath.Join(dir, "data"), DistDir: filepath.Join(dir, "dist"), Theme: testTheme,
				Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
			app.Use(plugins...)
			err := app.Setup(context.Background())
			if err == nil {
				defer app.Close()
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("Setup succeeded, then Handler panicked: %v", r)
					}
				}()
				app.Handler()
				t.Fatal("Setup succeeded despite the conflict")
			}
		})
	}
}
