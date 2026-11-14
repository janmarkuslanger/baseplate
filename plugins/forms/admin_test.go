package forms

import (
	"context"
	"encoding/json"
	"html/template"
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
	if err := reg.Store.DB().QueryRow(`SELECT id FROM form_submissions`).Scan(&sid); err != nil {
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
