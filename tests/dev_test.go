package tests

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/janmarkuslanger/baseplate"
	"github.com/janmarkuslanger/baseplate/fields"
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
