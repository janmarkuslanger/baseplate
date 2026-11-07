package tests

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
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
// (routable, with the standard SEO fields) and a new block.
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
	postLogin(t, c, srv.URL, credentials("a@b.de", "sehr-geheim-123"))
	dash := get("/admin/")
	csrf := csrfRe.FindStringSubmatch(dash)[1]

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
	c.PostForm(srv.URL+"/api/forms/1", url.Values{"name": {"Ada"}})
	if !strings.Contains(get("/admin/p/forms/submissions"), "Ada") {
		t.Error("submission not listed")
	}

	res, _ := c.PostForm(srv.URL+"/admin/build", url.Values{"csrf": {csrf}})
	res.Body.Close()
	if body := get("/news/launch/"); !strings.Contains(body, "NEWS Launch: Wir sind live") {
		t.Errorf("news page not built: %q", body)
	}
}

// colorPlugin adds a field kind with its own admin input.
type colorPlugin struct{}

func (colorPlugin) Name() string { return "color" }
func (colorPlugin) Register(r *plugin.Registry) error {
	if err := r.Kind(content.Kind{
		Name: "color", Storage: content.StoreText,
		Normalize: func(a content.NormalizeArgs) (any, error) {
			raw := a.Raw
			s, _ := raw.(string)
			if s == "" {
				return nil, nil
			}
			return s, nil
		},
		AdminTemplate: `<input type="color" class="color-input" x-model="{{.M}}">`,
	}); err != nil {
		return err
	}
	return r.Extend("page", content.Field{Name: "accent", Label: "Akzentfarbe", Kind: "color"})
}

func TestPluginKindAdminTemplate(t *testing.T) {
	dir := t.TempDir()
	app := baseplate.New(baseplate.Config{DataDir: filepath.Join(dir, "data"), DistDir: filepath.Join(dir, "dist"), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	app.Use(colorPlugin{})
	if err := app.Run(context.Background(), []string{"user", "add", "a@b.de"}, strings.NewReader("sehr-geheim-123\n"), io.Discard); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(appHandler(t, app))
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	postLogin(t, c, srv.URL, credentials("a@b.de", "sehr-geheim-123"))
	res, err := c.Get(srv.URL + "/admin/c/page/new")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(b), `<input type="color" class="color-input" x-model="data.accent">`) || !strings.Contains(string(b), "Akzentfarbe") {
		t.Errorf("plugin kind input missing")
	}
}

func TestPluginMediaService(t *testing.T) {
	dir := t.TempDir()
	var saved string
	app := baseplate.New(baseplate.Config{DataDir: filepath.Join(dir, "data"), DistDir: filepath.Join(dir, "dist"), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	app.Use(mediaPlugin{url: &saved})
	if err := app.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if !strings.HasPrefix(saved, "/media/logo-") || !strings.HasSuffix(saved, ".png") {
		t.Errorf("url = %q", saved)
	}
	if _, err := os.Stat(filepath.Join(dir, "data", "media", strings.TrimPrefix(saved, "/media/"))); err != nil {
		t.Errorf("file not in the media library: %v", err)
	}
}

// mediaPlugin uploads a file through Services.Media while registering.
type mediaPlugin struct{ url *string }

func (mediaPlugin) Name() string { return "media-user" }
func (p mediaPlugin) Register(r *plugin.Registry) error {
	u, err := r.Media.Save(strings.NewReader("\x89PNG\r\n\x1a\nrest"), "Logo.png")
	*p.url = u
	return err
}
