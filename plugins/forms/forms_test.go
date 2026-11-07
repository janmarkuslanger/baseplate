package forms

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/janmarkuslanger/baseplate/build"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/mail"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/storage"
	"github.com/janmarkuslanger/baseplate/storage/sqlite"
)

type fakeMailer struct {
	sent []mail.Message
	fail bool
	// during runs while the mail is being sent, e.g. to cancel the request.
	during func()
}

func (f *fakeMailer) Send(ctx context.Context, m mail.Message) error {
	if f.fail {
		return errors.New("smtp down")
	}
	if f.during != nil {
		f.during()
	}
	if err := ctx.Err(); err != nil {
		return err // like SMTP, which closes the connection when ctx ends
	}
	f.sent = append(f.sent, m)
	return nil
}

func setup(t *testing.T) (*plugin.Registry, *Plugin, *fakeMailer, int64) {
	t.Helper()
	ctx := context.Background()
	st, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	m := &fakeMailer{}
	reg := plugin.NewRegistry(plugin.Services{Store: st, Mailer: m, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	p := New()
	if err := reg.Use(fields.Core{}, fields.CoreBlocks{}, p); err != nil {
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
	ft, _ := reg.Schema.Type(TypeName)
	data, err := reg.Schema.Validate(ft.Fields, map[string]any{
		"title": "Kontakt", "recipient": "team@acme.test",
		"fields": []any{
			map[string]any{"label": "Name", "name": "name", "type": "text", "required": true},
			map[string]any{"label": "E-Mail", "name": "email", "type": "email", "required": true},
			map[string]any{"label": "Nachricht", "name": "nachricht", "type": "textarea"},
			map[string]any{"label": "Datenschutz akzeptiert", "name": "dsgvo", "type": "checkbox", "required": true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	e := &storage.Entry{Type: TypeName, Status: storage.StatusPublished, Data: data}
	if err := st.CreateEntry(ctx, e); err != nil {
		t.Fatal(err)
	}
	return reg, p, m, e.ID
}

func post(t *testing.T, reg *plugin.Registry, id int64, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return postCtx(t, context.Background(), reg, id, form)
}

func postCtx(t *testing.T, ctx context.Context, reg *plugin.Registry, id int64, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	for _, rt := range reg.Routes() {
		mux.Handle(rt.Pattern, rt.Handler)
	}
	r := httptest.NewRequestWithContext(ctx, "POST", "/api/forms/"+itoa(id), strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Referer", "https://acme.test/kontakt/")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func count(t *testing.T, reg *plugin.Registry) int {
	var n int
	reg.Store.DB().QueryRow(`SELECT COUNT(*) FROM form_submissions`).Scan(&n)
	return n
}

func TestSubmit(t *testing.T) {
	reg, _, m, id := setup(t)

	valid := url.Values{"name": {"Ada"}, "email": {"ada@example.com"}, "nachricht": {"Hallo!"}, "dsgvo": {"1"}}
	w := post(t, reg, id, valid)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Vielen Dank") {
		t.Fatalf("valid submit: %d %s", w.Code, w.Body)
	}
	if len(m.sent) != 1 {
		t.Fatalf("mails sent = %d", len(m.sent))
	}
	msg := m.sent[0]
	if msg.To[0] != "team@acme.test" || msg.ReplyTo != "ada@example.com" || !strings.Contains(msg.Text, "Nachricht:\nHallo!") || !strings.Contains(msg.Text, "Datenschutz akzeptiert:\nja") {
		t.Errorf("mail: %+v", msg)
	}
	if count(t, reg) != 1 {
		t.Error("submission not stored")
	}

	w = post(t, reg, id, url.Values{"name": {"Ada"}, "email": {"kein-mail"}})
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "ungültige E-Mail") || !strings.Contains(w.Body.String(), "Datenschutz akzeptiert: Pflichtfeld") {
		t.Fatalf("invalid submit: %d %s", w.Code, w.Body)
	}

	bot := url.Values{"name": {"Bot"}, "email": {"b@b.de"}, "dsgvo": {"1"}, HoneypotField: {"spam"}}
	if w := post(t, reg, id, bot); w.Code != 200 {
		t.Fatalf("honeypot: %d", w.Code)
	}
	if count(t, reg) != 1 || len(m.sent) != 1 {
		t.Error("honeypot submission was processed")
	}

	if w := post(t, reg, 999, valid); w.Code != http.StatusNotFound {
		t.Errorf("unknown form: %d", w.Code)
	}
}

func TestSubmitMailFailureStillStores(t *testing.T) {
	reg, _, m, id := setup(t)
	m.fail = true
	w := post(t, reg, id, url.Values{"name": {"Ada"}, "email": {"ada@example.com"}, "dsgvo": {"1"}})
	if w.Code != 200 {
		t.Fatalf("submit: %d", w.Code)
	}
	var mailed int
	reg.Store.DB().QueryRow(`SELECT mailed FROM form_submissions`).Scan(&mailed)
	if count(t, reg) != 1 || mailed != 0 {
		t.Errorf("count=%d mailed=%d", count(t, reg), mailed)
	}
}

func TestSubmitMailOutlivesRequest(t *testing.T) {
	reg, _, m, id := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.during = cancel // the visitor closes the tab while the mail is sent
	postCtx(t, ctx, reg, id, url.Values{"name": {"Ada"}, "email": {"ada@example.com"}, "dsgvo": {"1"}})
	var mailed int
	reg.Store.DB().QueryRow(`SELECT mailed FROM form_submissions`).Scan(&mailed)
	if len(m.sent) != 1 || mailed != 1 {
		t.Errorf("sent=%d mailed=%d, want the mail sent and marked", len(m.sent), mailed)
	}
}

func TestSubmitMarkMailedFailureIsLogged(t *testing.T) {
	reg, _, m, id := setup(t)
	var logs strings.Builder
	reg.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	if _, err := reg.Store.DB().Exec(`CREATE TRIGGER no_update BEFORE UPDATE ON form_submissions BEGIN SELECT RAISE(FAIL, 'read-only'); END`); err != nil {
		t.Fatal(err)
	}
	w := post(t, reg, id, url.Values{"name": {"Ada"}, "email": {"ada@example.com"}, "dsgvo": {"1"}})
	if w.Code != 200 || len(m.sent) != 1 {
		t.Fatalf("submit: %d, mails sent = %d", w.Code, len(m.sent))
	}
	if !strings.Contains(logs.String(), "forms: mark submission as mailed") || !strings.Contains(logs.String(), "read-only") {
		t.Errorf("update failure not logged: %q", logs.String())
	}
}

func TestRateLimit(t *testing.T) {
	reg, p, _, id := setup(t)
	p.Limit = 2
	valid := url.Values{"name": {"Ada"}, "email": {"ada@example.com"}, "dsgvo": {"1"}}
	post(t, reg, id, valid)
	post(t, reg, id, valid)
	if w := post(t, reg, id, valid); w.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d", w.Code)
	}
}

func TestSuccessRedirect(t *testing.T) {
	reg, _, _, id := setup(t)
	e, _ := reg.Store.GetEntry(context.Background(), TypeName, id)
	e.Data["successURL"] = "/danke/"
	reg.Store.UpdateEntry(context.Background(), e)
	w := post(t, reg, id, url.Values{"name": {"Ada"}, "email": {"ada@example.com"}, "dsgvo": {"1"}})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/danke/" {
		t.Fatalf("redirect: %d %s", w.Code, w.Header().Get("Location"))
	}
}

func TestFormBlockInBuild(t *testing.T) {
	reg, _, _, id := setup(t)
	ctx := context.Background()
	reg.Store.CreateEntry(ctx, &storage.Entry{Type: fields.SettingsType, Slug: fields.SettingsType, Status: storage.StatusPublished,
		Data: map[string]any{"baseURL": "https://acme.test", "serverURL": "https://cms.acme.test"}})
	reg.Store.CreateEntry(ctx, &storage.Entry{Type: fields.PageType, Slug: "kontakt", Status: storage.StatusPublished, Data: map[string]any{
		"title": "Kontakt", "body": []any{map[string]any{"_type": BlockName, "form": float64(id)}},
	}})
	dist := filepath.Join(t.TempDir(), "dist")
	b := &build.Builder{Reg: reg, DistDir: dist, Theme: fstest.MapFS{
		"templates/page.html": {Data: []byte(`{{define "page"}}{{blocks .Data.body .}}{{end}}`)},
	}}
	if _, err := b.Build(ctx); err != nil {
		t.Fatal(err)
	}
	html, _ := os.ReadFile(filepath.Join(dist, "kontakt", "index.html"))
	for _, want := range []string{
		`action="https://cms.acme.test/api/forms/` + itoa(id) + `"`,
		`name="email"`, `type="email"`, `required`, `name="_hp"`,
	} {
		if !strings.Contains(string(html), want) {
			t.Errorf("form html missing %q:\n%s", want, html)
		}
	}
}

func TestRateLimitBehindProxy(t *testing.T) {
	reg, p, _, id := setup(t)
	reg.ClientIP = plugin.ProxyIP("X-Forwarded-For")
	p.Limit = 1
	send := func(ip string) int {
		mux := http.NewServeMux()
		for _, rt := range reg.Routes() {
			mux.Handle(rt.Pattern, rt.Handler)
		}
		r := httptest.NewRequest("POST", "/api/forms/"+itoa(id), strings.NewReader(url.Values{"name": {"A"}, "email": {"a@b.de"}, "dsgvo": {"1"}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		// The client-supplied part before the proxy's entry is ignored.
		r.Header.Set("X-Forwarded-For", "9.9.9.9, "+ip)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w.Code
	}
	if send("1.1.1.1") != 200 || send("2.2.2.2") != 200 {
		t.Fatal("different clients behind the same proxy must not share a limit")
	}
	if send("1.1.1.1") != http.StatusTooManyRequests {
		t.Fatal("same client must be limited")
	}
}

// unpublish turns the form into a draft, as the admin's "save as draft" does.
func unpublish(t *testing.T, reg *plugin.Registry, id int64) {
	t.Helper()
	ctx := context.Background()
	e, err := reg.Store.GetEntry(ctx, TypeName, id)
	if err != nil {
		t.Fatal(err)
	}
	e.Status = storage.StatusDraft
	if err := reg.Store.UpdateEntry(ctx, e); err != nil {
		t.Fatal(err)
	}
}

func TestDraftFormRejectsSubmissions(t *testing.T) {
	reg, _, m, id := setup(t)
	unpublish(t, reg, id)
	w := post(t, reg, id, url.Values{"name": {"Ada"}, "email": {"ada@example.com"}, "dsgvo": {"1"}})
	if w.Code != http.StatusNotFound {
		t.Fatalf("draft form: want 404, got %d", w.Code)
	}
	if count(t, reg) != 0 || len(m.sent) != 0 {
		t.Error("draft form submission was processed")
	}
}

func TestDraftFormNotRendered(t *testing.T) {
	reg, _, _, id := setup(t)
	unpublish(t, reg, id)
	ctx := context.Background()
	reg.Store.CreateEntry(ctx, &storage.Entry{Type: fields.SettingsType, Slug: fields.SettingsType, Status: storage.StatusPublished,
		Data: map[string]any{"baseURL": "https://acme.test"}})
	reg.Store.CreateEntry(ctx, &storage.Entry{Type: fields.PageType, Slug: "kontakt", Status: storage.StatusPublished, Data: map[string]any{
		"title": "Kontakt", "body": []any{map[string]any{"_type": BlockName, "form": float64(id)}},
	}})
	dist := filepath.Join(t.TempDir(), "dist")
	b := &build.Builder{Reg: reg, DistDir: dist, Theme: fstest.MapFS{
		"templates/page.html": {Data: []byte(`{{define "page"}}[{{blocks .Data.body .}}]{{end}}`)},
	}}
	if _, err := b.Build(ctx); err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile(filepath.Join(dist, "kontakt", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(html), "<form") {
		t.Errorf("draft form rendered:\n%s", html)
	}
}
