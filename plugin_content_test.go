package baseplate_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/janmarkuslanger/baseplate"
	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/plugins/forms"
)

// newsPlugin is what a customer project would write: a new content type
// (routable, with the standard SEO/LLM fields) and a new block.
type newsPlugin struct{}

func (newsPlugin) Name() string { return "news" }
func (newsPlugin) Register(r *plugin.Registry) error {
	if err := r.ContentType(content.Type{
		Name: "news", Label: "News", LabelPlural: "News", Routable: true,
		Fields: fields.RoutableFields(content.Field{Name: "teaser", Label: "Teaser", Kind: fields.Textarea}),
	}); err != nil {
		return err
	}
	return r.Block(content.Block{Name: "quote", Label: "Zitat", Fields: []content.Field{{Name: "text", Label: "Zitat", Kind: fields.Text}}})
}

func appHandler(t *testing.T, app *baseplate.App) http.Handler {
	t.Helper()
	if err := app.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })
	return app.Handler()
}

func TestPluginContentInAdmin(t *testing.T) {
	dir := t.TempDir()
	app := baseplate.New(baseplate.Config{
		DataDir: filepath.Join(dir, "data"), DistDir: filepath.Join(dir, "dist"),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Theme: fstest.MapFS{
			"templates/page.html": {Data: []byte(`{{define "page"}}{{.Data.title}}{{end}}`)},
			"templates/news.html": {Data: []byte(`{{define "news"}}NEWS {{.Data.title}}: {{.Data.teaser}}{{end}}`)},
		},
	})
	app.Use(forms.New(), newsPlugin{})
	ctx := context.Background()
	if err := app.Run(ctx, []string{"user", "add", "a@b.de"}, strings.NewReader("sehr-geheim-123\n"), io.Discard); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(appHandler(t, app))
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(p string) string {
		res, err := c.Get(srv.URL + p)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 {
			t.Fatalf("GET %s: %d", p, res.StatusCode)
		}
		return string(b)
	}
	c.PostForm(srv.URL+"/admin/login", url.Values{"email": {"a@b.de"}, "password": {"sehr-geheim-123"}})
	dash := get("/admin/")
	csrf := regexp.MustCompile(`data-csrf="([0-9a-f]+)"`).FindStringSubmatch(dash)[1]

	// Every plugin content type gets its own navigation entry.
	for _, want := range []string{`href="/admin/c/page">Seiten`, `href="/admin/c/form">Formulare`, `href="/admin/c/news">News`, `href="/admin/p/forms/submissions">Anfragen`} {
		if !strings.Contains(dash, want) {
			t.Errorf("navigation missing %s", want)
		}
	}
	edit := get("/admin/c/news/new")
	if !strings.Contains(edit, "Teaser") || !strings.Contains(edit, "SEO") {
		t.Error("news editor lacks plugin field or SEO tab")
	}
	if !strings.Contains(get("/admin/c/page/new"), "Zitat") {
		t.Error("plugin block not offered in page editor")
	}

	save := func(typ string, data map[string]any) {
		b, _ := json.Marshal(data)
		res, err := c.PostForm(srv.URL+"/admin/c/"+typ+"/save", url.Values{"csrf": {csrf}, "status": {"published"}, "data": {string(b)}})
		if err != nil || res.StatusCode != http.StatusSeeOther {
			t.Fatalf("save %s: %v %v", typ, res.StatusCode, err)
		}
	}
	save("settings", map[string]any{"siteName": "Acme", "baseURL": "https://acme.test"})
	save("news", map[string]any{"title": "Launch", "slug": "launch", "teaser": "Wir sind live"})
	save("form", map[string]any{"title": "Kontakt", "recipient": "t@acme.test", "fields": []any{map[string]any{"label": "Name", "name": "name", "type": "text"}}})
	if !strings.Contains(get("/admin/c/news"), "Launch") {
		t.Error("news list does not show the entry")
	}

	// A submission makes the submissions page query the store per row;
	// this used to deadlock on SQLite's single connection.
	c.PostForm(srv.URL+"/api/forms/3", url.Values{"name": {"Ada"}})
	if !strings.Contains(get("/admin/p/forms/submissions"), "Ada") {
		t.Error("submission not listed")
	}

	res, _ := c.PostForm(srv.URL+"/admin/build", url.Values{"csrf": {csrf}})
	res.Body.Close()
	if body := get("/news/launch/"); !strings.Contains(body, "NEWS Launch: Wir sind live") {
		t.Errorf("news page not built: %q", body)
	}
}
