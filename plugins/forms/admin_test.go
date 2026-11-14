package forms

import (
	"context"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeUI renders the plugin's admin templates without the admin layout.
// Its "t" marks every lookup, so a test can tell translated keys from
// plain text.
type fakeUI struct{ t *testing.T }

func (u fakeUI) Render(w http.ResponseWriter, r *http.Request, name string, data any) {
	tmpl, err := template.New(name).Funcs(template.FuncMap{
		"t":    func(key string, _ ...any) string { return "T(" + key + ")" },
		"date": func(t time.Time) string { return t.Format(time.DateTime) },
		"json": func(v any) (string, error) {
			b, err := json.Marshal(v)
			return string(b), err
		},
	}).ParseFS(adminFS, "admin/"+name)
	if err != nil {
		u.t.Fatal(err)
	}
	if err := tmpl.ExecuteTemplate(w, "content", map[string]any{"Page": data}); err != nil {
		u.t.Fatal(err)
	}
}

func (fakeUI) CSRFToken(*http.Request) string { return "csrf" }

func getSubmissions(t *testing.T, p *Plugin, query string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	p.adminSubmissions(w, httptest.NewRequest("GET", "/admin/p/forms/submissions"+query, nil), fakeUI{t})
	return w
}

// A form title is written by an editor and must be shown as is, not looked
// up as a translation key.
func TestSubmissionsShowFormTitleAsText(t *testing.T) {
	reg, p, _, id := setup(t)
	post(t, reg, id, url.Values{"name": {"Ada"}, "email": {"ada@example.com"}, "dsgvo": {"1"}})
	var sid int64
	if err := reg.Store.DB().QueryRowContext(context.Background(), `SELECT id FROM form_submissions`).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"", "?id=" + itoa(sid)} {
		body := getSubmissions(t, p, query).Body.String()
		if !strings.Contains(body, "Kontakt") || strings.Contains(body, "T(Kontakt)") {
			t.Errorf("%q: form title not shown as plain text:\n%s", query, body)
		}
	}

	// A deleted form still gets the translated placeholder.
	if err := reg.Store.DeleteEntry(context.Background(), TypeName, id); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"", "?id=" + itoa(sid)} {
		if body := getSubmissions(t, p, query).Body.String(); !strings.Contains(body, "T(forms.deletedForm)") {
			t.Errorf("%q: deleted form placeholder missing:\n%s", query, body)
		}
	}
}

// Store errors are logged, not shown in the browser.
func TestSubmissionsErrorsStayInLog(t *testing.T) {
	reg, p, _, _ := setup(t)
	var logs strings.Builder
	reg.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	if _, err := reg.Store.DB().ExecContext(context.Background(), `DROP TABLE form_submissions`); err != nil {
		t.Fatal(err)
	}
	w := getSubmissions(t, p, "")
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "form_submissions") {
		t.Errorf("want 500 without details, got %d: %s", w.Code, w.Body)
	}
	if !strings.Contains(logs.String(), "no such table: form_submissions") {
		t.Errorf("error not logged: %q", logs.String())
	}
}

// A stored submission that cannot be decoded is an error, not an empty row.
func TestSubmissionsUndecodableDataFails(t *testing.T) {
	reg, p, _, id := setup(t)
	if _, err := reg.Store.DB().ExecContext(context.Background(), `INSERT INTO form_submissions (form_id, data, created_at) VALUES (?, 'not json', 0)`, id); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"", "?id=1"} {
		if w := getSubmissions(t, p, query); w.Code != http.StatusInternalServerError {
			t.Errorf("%q: want 500, got %d:\n%s", query, w.Code, w.Body)
		}
	}
}
