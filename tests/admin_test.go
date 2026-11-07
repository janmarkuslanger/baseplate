package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	stdhtml "html"
	"io"
	"log/slog"
	"mime/multipart"
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

	"github.com/janmarkuslanger/baseplate"
	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/entries"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/plugin"
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

// newTestApp starts a server with an admin user; it returns the dist dir.
func newTestApp(t *testing.T, plugins ...plugin.Plugin) (string, *client) {
	t.Helper()
	_, dist, c := startApp(t, io.Discard, plugins...)
	return dist, c
}

// startApp is newTestApp that also returns the app and logs to logs.
func startApp(t *testing.T, logs io.Writer, plugins ...plugin.Plugin) (*baseplate.App, string, *client) {
	t.Helper()
	dir := t.TempDir()
	app := baseplate.New(baseplate.Config{DataDir: filepath.Join(dir, "data"), DistDir: filepath.Join(dir, "dist"), Theme: testTheme,
		Logger: slog.New(slog.NewTextHandler(logs, nil))})
	app.Use(fields.CoreBlocks{})
	app.Use(plugins...)
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
	return app, filepath.Join(dir, "dist"), c
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

var csrfRe = regexp.MustCompile(`data-csrf="([0-9A-Za-z]+)"`)

var loginCSRFRe = regexp.MustCompile(`name="csrf" value="([0-9A-Za-z]+)"`)

// credentials returns the fields of the login form.
func credentials(email, pw string) url.Values {
	return url.Values{"email": {email}, "password": {pw}}
}

// postLogin submits the login form like a browser: it loads the form first
// for its CSRF token and cookie.
func postLogin(t *testing.T, c *http.Client, base string, form url.Values) *http.Response {
	t.Helper()
	res, err := c.Get(base + "/admin/login")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	m := loginCSRFRe.FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("no csrf token in login form")
	}
	form.Set("csrf", m[1])
	res, err = c.PostForm(base+"/admin/login", form)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res
}

func (c *client) login() {
	c.t.Helper()
	res := postLogin(c.t, c.http, c.srv.URL, credentials("admin@example.com", "sehr-geheim-123"))
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
	dist, c := newTestApp(t)

	if res, _ := c.do("GET", "/admin/", nil); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("anonymous dashboard: %d", res.StatusCode)
	}
	if res := postLogin(t, c.http, c.srv.URL, credentials("admin@example.com", "falsch")); res.StatusCode != http.StatusUnauthorized {
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
	html, err := os.ReadFile(filepath.Join(dist, "index.html"))
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

func TestLoginCSRF(t *testing.T) {
	_, c := newTestApp(t)
	creds := credentials("admin@example.com", "sehr-geheim-123")
	loggedIn := func() bool {
		res, _ := c.do("GET", "/admin/", nil)
		return res.StatusCode == http.StatusOK
	}

	// A cross-site form post carries neither the cookie nor the token.
	if res, _ := c.do("POST", "/admin/login", creds); res.StatusCode != http.StatusForbidden || loggedIn() {
		t.Fatalf("login without token: %d, logged in %v", res.StatusCode, loggedIn())
	}

	// A token from the attacker's own visit does not match the victim's cookie.
	attacker, _ := cookiejar.New(nil)
	ac := &http.Client{Jar: attacker, CheckRedirect: c.http.CheckRedirect}
	res, _ := ac.Get(c.srv.URL + "/admin/login")
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	foreign := loginCSRFRe.FindStringSubmatch(string(b))[1]
	c.do("GET", "/admin/login", nil) // victim has a cookie of their own
	forged := credentials("admin@example.com", "sehr-geheim-123")
	forged.Set("csrf", foreign)
	if res, body := c.do("POST", "/admin/login", forged); res.StatusCode != http.StatusForbidden || loggedIn() {
		t.Fatalf("login with foreign token: %d, logged in %v", res.StatusCode, loggedIn())
	} else if !loginCSRFRe.MatchString(body) {
		t.Error("rejected login does not render a fresh form")
	}

	if res := postLogin(t, c.http, c.srv.URL, creds); res.StatusCode != http.StatusSeeOther || !loggedIn() {
		t.Fatalf("login with token: %d", res.StatusCode)
	}
}

func TestLoginBodyLimit(t *testing.T) {
	_, c := newTestApp(t)
	big := strings.Repeat("x", 1<<20)
	form := credentials("admin@example.com", "sehr-geheim-123")
	form.Set("pad", big)
	res := postLogin(t, c.http, c.srv.URL, form)
	if res.StatusCode == http.StatusSeeOther {
		t.Fatal("oversized login body must not be accepted")
	}
}

type blogPlugin struct{}

func (blogPlugin) Name() string { return "blog" }
func (blogPlugin) Register(r *plugin.Registry) error {
	return r.ContentType(content.Type{Name: "post", Label: "Post", LabelPlural: "Posts", Routable: true, Path: "/blog/{slug}/", Fields: fields.RoutableFields()})
}

func TestListShowsURL(t *testing.T) {
	_, c := newTestApp(t, blogPlugin{})
	c.login()
	idOf := func(res *http.Response) string {
		return strings.Split(strings.TrimPrefix(res.Header.Get("Location"), "/admin/c/page/"), "?")[0]
	}
	res, _ := c.save("page", "published", map[string]any{"title": "Über uns", "slug": "ueber-uns"})
	parent := idOf(res)
	c.save("page", "published", map[string]any{"title": "Team", "slug": "team", "parent": parent})
	res, _ = c.save("page", "draft", map[string]any{"title": "Entwurf", "slug": "entwurf"})
	c.save("page", "published", map[string]any{"title": "Kind", "slug": "kind", "parent": idOf(res)})
	c.save("post", "published", map[string]any{"title": "Hallo", "slug": "hallo"})

	_, pages := c.do("GET", "/admin/c/page", nil)
	for _, want := range []string{">/ueber-uns/<", ">/ueber-uns/team/<", ">/kind/<"} {
		if !strings.Contains(pages, want) {
			t.Errorf("page list: missing %s", want)
		}
	}
	if _, posts := c.do("GET", "/admin/c/post", nil); !strings.Contains(posts, ">/blog/hallo/<") {
		t.Error("post list: missing /blog/hallo/")
	}
}

func TestTimezoneSetting(t *testing.T) {
	_, c := newTestApp(t)
	c.login()
	if _, html := c.do("GET", "/admin/", nil); !strings.Contains(html, `data-tz="Europe/Berlin"`) {
		t.Error("default time zone missing in the admin")
	}
	if res, _ := c.save("settings", "", map[string]any{"siteName": "A", "baseURL": "https://a.test", "timezone": "Mars/Olympus"}); res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid time zone saved: %d", res.StatusCode)
	}
	c.save("settings", "", map[string]any{"siteName": "A", "baseURL": "https://a.test", "timezone": "America/New_York"})
	if _, html := c.do("GET", "/admin/", nil); !strings.Contains(html, `data-tz="America/New_York"`) {
		t.Error("saved time zone not passed to date inputs")
	}
}

type tagPlugin struct{}

func (tagPlugin) Name() string { return "tags" }
func (tagPlugin) Register(r *plugin.Registry) error {
	if err := r.ContentType(content.Type{Name: "tag", Fields: []content.Field{{Name: "title", Kind: fields.Text}}}); err != nil {
		return err
	}
	if err := r.ContentType(content.Type{Name: "author", Fields: []content.Field{{Name: "title", Kind: fields.Text}}}); err != nil {
		return err
	}
	return r.Block(content.Block{Name: "teaser", Fields: []content.Field{{Name: "author", Kind: fields.Relation, Ref: "author"}}})
}

// The editor gets the choices of all relation pickers, also inside blocks.
func TestRelationOptions(t *testing.T) {
	_, c := newTestApp(t, tagPlugin{}, extendPage{content.Field{Name: "tags", Kind: fields.Relation, Ref: "tag", Many: true}})
	c.login()
	c.save("tag", "published", map[string]any{"title": "Yoga"})
	c.save("author", "published", map[string]any{"title": "Julia"})
	_, html := c.do("GET", "/admin/c/page/new", nil)
	m := regexp.MustCompile(`data-refs="([^"]*)"`).FindStringSubmatch(html)
	if m == nil {
		t.Fatal("no data-refs on the editor")
	}
	var refs map[string][]struct{ Value, Label string }
	if err := json.Unmarshal([]byte(stdhtml.UnescapeString(m[1])), &refs); err != nil {
		t.Fatal(err)
	}
	if len(refs["tag"]) != 1 || refs["tag"][0].Label != "Yoga" || len(refs["author"]) != 1 || refs["author"][0].Label != "Julia" {
		t.Errorf("refs: %+v", refs)
	}
	if _, ok := refs["page"]; !ok {
		t.Error("core relation (parent page) missing")
	}
}

// The media page shows only known upload errors, never text from the URL.
func TestMediaUploadError(t *testing.T) {
	_, c := newTestApp(t)
	c.login()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("csrf", c.csrf)
	fw, _ := mw.CreateFormFile("file", "tool.exe")
	fw.Write([]byte("MZ"))
	mw.Close()
	req, _ := http.NewRequest("POST", c.srv.URL+"/admin/media", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	loc := res.Header.Get("Location")
	if res.StatusCode != http.StatusSeeOther || !strings.HasPrefix(loc, "/admin/media?error=") {
		t.Fatalf("rejected upload: %d %s", res.StatusCode, loc)
	}
	if _, html := c.do("GET", loc, nil); !strings.Contains(html, "Dateityp nicht erlaubt") {
		t.Errorf("upload error not shown:\n%s", html)
	}

	_, html := c.do("GET", "/admin/media?error="+url.QueryEscape("Ihr Konto ist gesperrt, rufen Sie 0900 an"), nil)
	if strings.Contains(html, "gesperrt") {
		t.Errorf("arbitrary text from the URL shown as a message:\n%s", html)
	}
}

type extendPage []content.Field

func (extendPage) Name() string { return "extend-page" }
func (e extendPage) Register(r *plugin.Registry) error {
	return r.Extend(fields.PageType, e...)
}

// "Verwendet in" names the referring type and field in the admin language,
// not by their catalog keys.
func TestUsedInLabels(t *testing.T) {
	app, _, c := startApp(t, io.Discard)
	c.login()
	ctx := context.Background()
	parent, err := app.Registry.Entries.Save(ctx, entries.Save{Type: fields.PageType, Data: map[string]any{"title": "Eltern", "slug": "eltern"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.Registry.Entries.Save(ctx, entries.Save{Type: fields.PageType, Data: map[string]any{"title": "Kind", "slug": "kind", "parent": parent.Entry.ID}}); err != nil {
		t.Fatal(err)
	}
	_, html := c.do("GET", fmt.Sprintf("/admin/c/page/%d", parent.Entry.ID), nil)
	m := regexp.MustCompile(`(?s)<ul class="used-in">.*?</ul>`).FindString(html)
	if !strings.Contains(m, "· Seite · Übergeordnete Seite") {
		t.Errorf("used-in list lacks translated labels: %q", m)
	}
}
