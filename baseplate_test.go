package baseplate

import (
	"bytes"
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
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

var testTheme = fstest.MapFS{
	"templates/page.html": {Data: []byte(`{{define "page"}}<html><head>{{template "baseplate/head" .}}</head><body><h1>{{.Data.title}}</h1>{{blocks .Data.body .}}</body></html>{{end}}`)},
}

type client struct {
	t    *testing.T
	srv  *httptest.Server
	http *http.Client
	csrf string
}

func newTestApp(t *testing.T) (*App, *client) {
	t.Helper()
	dir := t.TempDir()
	app := New(Config{DataDir: filepath.Join(dir, "data"), DistDir: filepath.Join(dir, "dist"), Theme: testTheme,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	var out bytes.Buffer
	if err := app.Run(context.Background(), []string{"user", "add", "admin@example.com"}, strings.NewReader("sehr-geheim-123\n"), &out); err != nil {
		t.Fatal(err)
	}
	// Run closed the store; set up again for the server.
	if err := app.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Registry.Store.Close() })
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	c := &client{t: t, srv: srv, http: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	return app, c
}

func (c *client) do(method, path string, form url.Values) (*http.Response, string) {
	c.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, c.srv.URL+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res, string(b)
}

var csrfRe = regexp.MustCompile(`data-csrf="([0-9a-f]+)"`)

func (c *client) login() {
	c.t.Helper()
	res, _ := c.do("POST", "/admin/login", url.Values{"email": {"admin@example.com"}, "password": {"sehr-geheim-123"}})
	if res.StatusCode != http.StatusSeeOther {
		c.t.Fatalf("login: %d", res.StatusCode)
	}
	_, html := c.do("GET", "/admin/", nil)
	m := csrfRe.FindStringSubmatch(html)
	if m == nil {
		c.t.Fatal("no csrf token on dashboard")
	}
	c.csrf = m[1]
}

func (c *client) save(typ string, status string, data map[string]any) (*http.Response, string) {
	b, _ := json.Marshal(data)
	return c.do("POST", "/admin/c/"+typ+"/save", url.Values{"csrf": {c.csrf}, "status": {status}, "data": {string(b)}})
}

func TestAdminFlow(t *testing.T) {
	app, c := newTestApp(t)

	if res, _ := c.do("GET", "/admin/", nil); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("anonymous dashboard: %d", res.StatusCode)
	}
	if res, _ := c.do("POST", "/admin/login", url.Values{"email": {"admin@example.com"}, "password": {"falsch"}}); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad login: %d", res.StatusCode)
	}
	c.login()

	for _, p := range []string{"/admin/", "/admin/c/page", "/admin/c/page/new", "/admin/c/settings", "/admin/media"} {
		if res, body := c.do("GET", p, nil); res.StatusCode != 200 {
			t.Fatalf("GET %s: %d\n%s", p, res.StatusCode, body)
		}
	}

	// Validation errors re-render the form with 422.
	res, body := c.save("page", "published", map[string]any{"title": "", "slug": "x"})
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "Bitte prüfe") {
		t.Fatalf("invalid save: %d", res.StatusCode)
	}

	// Missing CSRF is rejected.
	b, _ := json.Marshal(map[string]any{"title": "x", "slug": "x"})
	if res, _ := c.do("POST", "/admin/c/page/save", url.Values{"data": {string(b)}}); res.StatusCode != http.StatusForbidden {
		t.Fatalf("no csrf: %d", res.StatusCode)
	}

	res, _ = c.save("settings", "", map[string]any{"siteName": "Acme", "baseURL": "https://acme.test"})
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("save settings: %d", res.StatusCode)
	}
	res, _ = c.save("page", "published", map[string]any{
		"title": "Start", "slug": "index",
		"body": []any{map[string]any{"_type": "richtext", "html": "<p>Hallo<script>x</script></p>", "_key": "abc"}},
	})
	if res.StatusCode != http.StatusSeeOther || !strings.Contains(res.Header.Get("Location"), "flash=published") {
		t.Fatalf("save page: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if res, body := c.do("GET", res.Header.Get("Location"), nil); res.StatusCode != 200 || !strings.Contains(body, "Veröffentlicht") {
		t.Fatalf("edit page after save: %d", res.StatusCode)
	} else if m := regexp.MustCompile(`name="status" value="(\w+)"`).FindStringSubmatch(body); m == nil || m[1] != "published" {
		// Enter submits the first submit button; it must keep the status.
		t.Errorf("default submit button of a published entry: %v", m)
	}
	res, body = c.save("page", "published", map[string]any{"title": "Doppelt", "slug": "index"})
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "bereits vergeben") {
		t.Fatalf("duplicate slug: %d", res.StatusCode)
	}

	res, body = c.do("POST", "/admin/build", url.Values{"csrf": {c.csrf}})
	if res.StatusCode != 200 || !strings.Contains(body, "1 Seiten") {
		t.Fatalf("build: %d %s", res.StatusCode, body)
	}
	html, err := os.ReadFile(filepath.Join(app.cfg.DistDir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), "<p>Hallo</p>") || strings.Contains(string(html), "script") {
		t.Errorf("built html: %s", html)
	}
	if _, body := c.do("GET", "/", nil); !strings.Contains(body, "<h1>Start</h1>") {
		t.Errorf("preview of dist not served")
	}
}
