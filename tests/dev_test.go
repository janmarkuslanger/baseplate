package tests

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/janmarkuslanger/baseplate"
	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/internal/devreload"
	"github.com/janmarkuslanger/baseplate/plugin"
)

func TestDevThemeReload(t *testing.T) {
	dir := t.TempDir()
	theme := filepath.Join(dir, "theme")
	tpl := filepath.Join(theme, "templates", "page.html")
	if err := os.MkdirAll(filepath.Dir(tpl), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(title string) {
		src := `{{define "page"}}<html><head>{{template "baseplate/head" .}}</head><body><h1>` + title + `{{.Data.title}}</h1></body></html>{{end}}`
		if err := os.WriteFile(tpl, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("v1 ")

	app := baseplate.New(baseplate.Config{DataDir: filepath.Join(dir, "data"), DistDir: filepath.Join(dir, "dist"),
		Theme: testTheme, ThemeDir: theme, Dev: true, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	app.Use(fields.CoreBlocks{})
	if err := app.Run(context.Background(), []string{"user", "add", "admin@example.com"}, strings.NewReader("sehr-geheim-123\n"), io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := app.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	c := &client{t: t, srv: srv, http: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	c.login()

	c.save("settings", "published", map[string]any{"siteName": "Acme", "baseURL": "https://acme.test"})
	res, _ := c.save("page", "draft", map[string]any{"title": "Start", "slug": "index"})
	pageURL, _, _ := strings.Cut(res.Header.Get("Location"), "?")
	_, html := c.do("GET", pageURL+"/preview", nil)
	if !strings.Contains(html, "v1 ") || !strings.Contains(html, "/__dev/theme") {
		t.Fatalf("preview must use the theme dir and carry the reload script:\n%s", html)
	}
	_, v1 := c.do("GET", "/__dev/theme", nil)

	// Every page renders live from the draft: no build, no publish.
	if res, home := c.do("GET", "/", nil); res.StatusCode != 200 || !strings.Contains(home, "v1 Start") || !strings.Contains(home, "/__dev/theme") {
		t.Errorf("home from draft: %d\n%s", res.StatusCode, home)
	}
	if res, _ := c.do("GET", "/missing/", nil); res.StatusCode != 404 {
		t.Errorf("unknown page: %d", res.StatusCode)
	}
	if err := os.MkdirAll(filepath.Join(theme, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(theme, "assets", "a.css"), []byte("body{}"), 0o644)
	if res, css := c.do("GET", "/assets/a.css", nil); res.StatusCode != 200 || css != "body{}" {
		t.Errorf("theme asset: %d %q", res.StatusCode, css)
	}

	write("v2 ")
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(tpl, later, later); err != nil {
		t.Fatal(err)
	}
	_, html = c.do("GET", pageURL+"/preview", nil)
	if !strings.Contains(html, "v2 ") {
		t.Errorf("theme change must apply without a restart:\n%s", html)
	}
	if _, v2 := c.do("GET", "/__dev/theme", nil); v1 == v2 {
		t.Errorf("theme version unchanged after edit: %s", v1)
	}
}

type writerPlugin struct{}

func (writerPlugin) Name() string { return "writers" }
func (writerPlugin) Register(r *plugin.Registry) error {
	if err := r.ContentType(content.Type{Name: "author", Fields: []content.Field{{Name: "title", Kind: fields.Text}}}); err != nil {
		return err
	}
	return r.ContentType(content.Type{Name: "article", Routable: true, Path: "/a/{slug}/",
		Fields: fields.RoutableFields(content.Field{Name: "writer", Kind: fields.Relation, Ref: "author"})})
}

// startDev starts a dev server around theme and returns its dist dir.
func startDev(t *testing.T, cfg baseplate.Config, plugins ...plugin.Plugin) (string, *client) {
	t.Helper()
	dir := t.TempDir()
	cfg.DataDir, cfg.DistDir, cfg.Dev = filepath.Join(dir, "data"), filepath.Join(dir, "dist"), true
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	app := baseplate.New(cfg)
	app.Use(fields.CoreBlocks{})
	app.Use(plugins...)
	if err := app.Run(context.Background(), []string{"user", "add", "admin@example.com"}, strings.NewReader("sehr-geheim-123\n"), io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := app.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	c := &client{t: t, srv: srv, http: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	c.login()
	c.save("settings", "published", map[string]any{"siteName": "Acme", "baseURL": "https://acme.test"})
	return cfg.DistDir, c
}

// A file missing from the theme's assets/ comes from the last build.
func TestDevAssetFallsBackToDist(t *testing.T) {
	theme := fstest.MapFS{
		"templates/page.html": testTheme["templates/page.html"],
		"assets/a.css":        {Data: []byte("theme")},
	}
	dist, c := startDev(t, baseplate.Config{Theme: theme})
	if err := os.MkdirAll(filepath.Join(dist, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dist, "assets", "gen.css"), []byte("built"), 0o644)
	if _, css := c.do("GET", "/assets/gen.css", nil); css != "built" {
		t.Errorf("built asset: %q", css)
	}
	if _, css := c.do("GET", "/assets/a.css", nil); css != "theme" {
		t.Errorf("theme asset: %q", css)
	}
}

// Without a theme directory there is no reload endpoint, so no script.
func TestDevEmbeddedThemeHasNoReloadScript(t *testing.T) {
	_, c := startDev(t, baseplate.Config{Theme: testTheme})
	c.save("page", "draft", map[string]any{"title": "Start", "slug": "index"})
	res, html := c.do("GET", "/", nil)
	if res.StatusCode != 200 || !strings.Contains(html, "<h1>Start</h1>") {
		t.Fatalf("home: %d\n%s", res.StatusCode, html)
	}
	if strings.Contains(html, "/__dev/theme") {
		t.Error("reload script without endpoint")
	}
}

// A relation to an entry without a page resolves from the draft, like the
// routable entries do.
func TestDevRelationUsesDraft(t *testing.T) {
	theme := fstest.MapFS{
		"templates/page.html":    testTheme["templates/page.html"],
		"templates/article.html": {Data: []byte(`{{define "article"}}<html><body>W={{with .Data.writer}}{{.Data.title}}{{end}}</body></html>{{end}}`)},
	}
	_, c := startDev(t, baseplate.Config{Theme: theme}, writerPlugin{})
	res, _ := c.save("author", "published", map[string]any{"title": "Old"})
	authorURL, _, _ := strings.Cut(res.Header.Get("Location"), "?")
	id := strings.TrimPrefix(authorURL, "/admin/c/author/")
	c.do("POST", "/admin/c/author/save", url.Values{"csrf": {c.csrf}, "id": {id}, "data": {`{"title":"New"}`}})
	n, _ := strconv.Atoi(id)
	c.save("article", "draft", map[string]any{"title": "A", "slug": "x", "writer": n})
	if _, html := c.do("GET", "/a/x/", nil); !strings.Contains(html, "W=New") {
		t.Errorf("relation must show the draft:\n%s", html)
	}
}

// Renaming a theme file changes the reload version.
func TestDevVersionSeesRename(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.html")
	os.WriteFile(f, []byte("x"), 0o644)
	version := func() string {
		w := httptest.NewRecorder()
		devreload.Handler(dir).ServeHTTP(w, httptest.NewRequest("GET", devreload.Path, nil))
		return w.Body.String()
	}
	before := version()
	if err := os.Rename(f, filepath.Join(dir, "b.html")); err != nil {
		t.Fatal(err)
	}
	if before == version() {
		t.Error("rename not detected")
	}
}
