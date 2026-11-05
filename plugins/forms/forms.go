// Package forms is the forms plugin: editors build forms in the admin, place
// them on pages with the "form" block, and the CMS server receives the
// submissions of the static site, stores them and sends them by e-mail.
package forms

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/i18n"
	bpmail "github.com/janmarkuslanger/baseplate/mail"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/settings"
	"github.com/janmarkuslanger/baseplate/storage"
)

//go:embed templates
var frontendFS embed.FS

//go:embed admin
var adminFS embed.FS

//go:embed locales/*.json
var localesFS embed.FS

func catalogs() ([]i18n.Catalog, error) {
	entries, err := localesFS.ReadDir("locales")
	if err != nil {
		return nil, err
	}
	var out []i18n.Catalog
	for _, e := range entries {
		b, err := localesFS.ReadFile("locales/" + e.Name())
		if err != nil {
			return nil, err
		}
		c := i18n.Catalog{Lang: strings.TrimSuffix(e.Name(), ".json")}
		if err := json.Unmarshal(b, &c.Messages); err != nil {
			return nil, fmt.Errorf("forms: locale %s: %w", e.Name(), err)
		}
		out = append(out, c)
	}
	return out, nil
}

const (
	TypeName  = "form"
	BlockName = "form"
	// HoneypotField must stay empty; bots that fill it are silently ignored.
	HoneypotField = "_hp"
	maxBody       = 64 << 10
	maxValueLen   = 5000
)

// Field types offered in the form builder.
var fieldTypes = []content.Option{
	{Value: "text", Label: "forms.ft.text"},
	{Value: "textarea", Label: "forms.ft.textarea"},
	{Value: "email", Label: "forms.ft.email"},
	{Value: "tel", Label: "forms.ft.tel"},
	{Value: "checkbox", Label: "forms.ft.checkbox"},
}

type Plugin struct {
	// Limit is the number of submissions per IP per Window (default 5 / 10 min).
	Limit  int
	Window time.Duration

	reg     *plugin.Registry
	texts   *i18n.Bundle
	pages   *template.Template
	mu      sync.Mutex
	hits    map[string][]time.Time
	nowFunc func() time.Time
}

func New() *Plugin { return &Plugin{Limit: 5, Window: 10 * time.Minute} }

func (p *Plugin) Name() string { return "forms" }

func (p *Plugin) Register(r *plugin.Registry) error {
	p.reg = r
	cats, err := catalogs()
	if err != nil {
		return err
	}
	for _, c := range cats {
		i18n.Catalogs.Add(r, c)
	}
	p.texts = i18n.NewBundle(cats...)
	p.hits = map[string][]time.Time{}
	if p.nowFunc == nil {
		p.nowFunc = time.Now
	}
	p.pages, err = template.ParseFS(frontendFS, "templates/response.html")
	if err != nil {
		return err
	}
	if err := r.ContentType(content.Type{
		Name: TypeName, Label: "forms.type", LabelPlural: "forms.type.plural",
		Fields: []content.Field{
			{Name: "title", Label: "forms.title", Kind: fields.Text, Required: true, Help: "forms.title.help"},
			{Name: "fields", Label: "forms.fields", Kind: fields.List, Required: true, Fields: []content.Field{
				{Name: "label", Label: "forms.field.label", Kind: fields.Text, Required: true},
				{Name: "name", Label: "forms.field.name", Kind: fields.Slug, Required: true, Help: "forms.field.name.help"},
				{Name: "type", Label: "forms.field.type", Kind: fields.Select, Required: true, Options: fieldTypes},
				{Name: "required", Label: "forms.field.required", Kind: fields.Bool},
			}},
			{Name: "submitLabel", Label: "forms.submitLabel", Kind: fields.Text, Help: "forms.submitLabel.help"},
			{Name: "recipient", Label: "forms.recipient", Kind: fields.Email, Required: true, Tab: "forms.tab.delivery"},
			{Name: "subject", Label: "forms.subject", Kind: fields.Text, Tab: "forms.tab.delivery"},
			{Name: "successMessage", Label: "forms.successMessage", Kind: fields.Textarea, Tab: "forms.tab.delivery", Help: "forms.successMessage.help"},
			{Name: "successURL", Label: "forms.successURL", Kind: fields.URL, Tab: "forms.tab.delivery", Help: "forms.successURL.help"},
		},
	}); err != nil {
		return err
	}
	if err := r.Block(content.Block{
		Name: BlockName, Label: "forms.type",
		Fields: []content.Field{{Name: "form", Label: "forms.type", Kind: fields.Relation, Ref: TypeName, Required: true}},
	}); err != nil {
		return err
	}
	tmpl, _ := fs.Sub(frontendFS, "templates/blocks")
	r.Templates(tmpl)
	r.Funcs(plugin.TemplateFuncs{
		"formByID": p.formByID,
		// formsT translates visitor-facing texts: {{formsT $.Page.Site.language "forms.public.submit"}}
		"formsT": func(lang any, key string) string {
			l, _ := lang.(string)
			if l == "" {
				l = i18n.Default
			}
			return p.pt(l, key)
		},
	})
	adm, _ := fs.Sub(adminFS, "admin")
	r.AdminTemplates(adm)
	r.AdminPage(plugin.AdminPage{Path: "forms/submissions", Label: "forms.submissions", Handler: p.adminSubmissions})
	r.PublicRoute(plugin.Route{Pattern: "POST /api/forms/{id}", Handler: http.HandlerFunc(p.submit)})
	r.Migration(storage.Migration{
		Name: "forms_0001_submissions",
		SQL: map[storage.Dialect]string{
			storage.SQLite: `CREATE TABLE form_submissions (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				form_id INTEGER NOT NULL,
				data TEXT NOT NULL,
				mailed INTEGER NOT NULL DEFAULT 0,
				created_at INTEGER NOT NULL
			);
			CREATE INDEX form_submissions_form ON form_submissions(form_id, created_at)`,
			storage.MySQL: `CREATE TABLE form_submissions (
				id BIGINT AUTO_INCREMENT PRIMARY KEY,
				form_id BIGINT NOT NULL,
				data JSON NOT NULL,
				mailed TINYINT NOT NULL DEFAULT 0,
				created_at BIGINT NOT NULL,
				INDEX form_submissions_form (form_id, created_at)
			)`,
		},
	})
	return nil
}

// formDef is a form entry as used by the block template and the handler.
type formDef struct {
	ID     int64
	Data   map[string]any
	Fields []fieldDef
}

type fieldDef struct {
	Name, Label, Type string
	Required          bool
}

func (p *Plugin) load(ctx context.Context, id int64) (*formDef, error) {
	e, err := p.reg.Store.GetEntry(ctx, TypeName, id)
	if err != nil {
		return nil, err
	}
	if e.Type != TypeName {
		return nil, storage.ErrNotFound
	}
	d := &formDef{ID: e.ID, Data: e.Data}
	list, _ := e.Data["fields"].([]any)
	for _, it := range list {
		m, _ := it.(map[string]any)
		f := fieldDef{Label: str(m["label"]), Name: str(m["name"]), Type: str(m["type"])}
		f.Required, _ = m["required"].(bool)
		d.Fields = append(d.Fields, f)
	}
	return d, nil
}

// formByID is available in frontend templates: {{with formByID .Block.form}}.
func (p *Plugin) formByID(id any) (*formDef, error) {
	n, ok := content.RefID(id)
	if !ok {
		return nil, nil
	}
	d, err := p.load(context.Background(), n)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil // form was deleted; render nothing
	}
	return d, err
}

func (p *Plugin) allow(ip string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.nowFunc()
	recent := p.hits[ip][:0]
	for _, t := range p.hits[ip] {
		if now.Sub(t) < p.Window {
			recent = append(recent, t)
		}
	}
	if len(recent) >= p.Limit {
		p.hits[ip] = recent
		return false
	}
	p.hits[ip] = append(recent, now)
	return true
}

type response struct {
	Lang      string
	BackLabel string
	Title     string
	Message   string
	Errors    []string
	Back      string
}

func (p *Plugin) respond(w http.ResponseWriter, status int, res response) {
	if res.Lang == "" {
		res.Lang = i18n.Default
	}
	res.BackLabel = p.pt(res.Lang, "forms.public.back")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	p.pages.ExecuteTemplate(w, "response.html", res)
}

func (p *Plugin) submit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	lang := p.siteLang(ctx)
	back := r.Referer()
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	def, err := p.load(ctx, id)
	if err != nil {
		p.respond(w, http.StatusNotFound, response{Lang: lang, Title: p.pt(lang, "forms.public.notFound"), Back: back})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := r.ParseForm(); err != nil {
		p.respond(w, http.StatusBadRequest, response{Lang: lang, Title: p.pt(lang, "forms.public.badRequest"), Back: back})
		return
	}
	success := func() {
		if u := str(def.Data["successURL"]); u != "" {
			http.Redirect(w, r, u, http.StatusSeeOther)
			return
		}
		p.respond(w, http.StatusOK, response{Lang: lang, Title: p.pt(lang, "forms.public.thanks"), Message: firstNonEmpty(str(def.Data["successMessage"]), p.pt(lang, "forms.public.sent")), Back: back})
	}
	if r.PostFormValue(HoneypotField) != "" {
		success() // pretend success so bots learn nothing
		return
	}
	if !p.allow(p.reg.ClientIP(r)) {
		p.respond(w, http.StatusTooManyRequests, response{Lang: lang, Title: p.pt(lang, "forms.public.tooMany"), Message: p.pt(lang, "forms.public.tooMany.hint"), Back: back})
		return
	}

	values := map[string]string{}
	var errs []string
	var replyTo string
	for _, f := range def.Fields {
		v := strings.TrimSpace(r.PostFormValue(f.Name))
		if f.Type == "checkbox" {
			if v != "" {
				v = p.pt(lang, "forms.public.yes")
			}
		}
		switch {
		case f.Required && v == "":
			errs = append(errs, p.pt(lang, "forms.public.required", f.Label))
		case utf8.RuneCountInString(v) > maxValueLen:
			errs = append(errs, p.pt(lang, "forms.public.tooLong", f.Label, maxValueLen))
		case f.Type == "email" && v != "":
			if a, err := mail.ParseAddress(v); err != nil || a.Address != v {
				errs = append(errs, p.pt(lang, "forms.public.email", f.Label))
			} else if replyTo == "" {
				replyTo = v
			}
		}
		if v == "" && f.Type == "checkbox" {
			v = p.pt(lang, "forms.public.no")
		}
		values[f.Name] = v
	}
	if len(errs) > 0 {
		p.respond(w, http.StatusUnprocessableEntity, response{Lang: lang, Title: p.pt(lang, "forms.public.check"), Errors: errs, Back: back})
		return
	}

	data, _ := json.Marshal(values)
	res, err := p.reg.Store.DB().ExecContext(ctx, `INSERT INTO form_submissions (form_id, data, created_at) VALUES (?, ?, ?)`, def.ID, string(data), p.nowFunc().Unix())
	if err != nil {
		p.reg.Logger.Error("forms: store submission", "err", err)
		p.respond(w, http.StatusInternalServerError, response{Lang: lang, Title: p.pt(lang, "forms.public.error"), Message: p.pt(lang, "forms.public.storeFailed"), Back: back})
		return
	}
	subID, _ := res.LastInsertId()

	var text strings.Builder
	for _, f := range def.Fields {
		fmt.Fprintf(&text, "%s:\n%s\n\n", f.Label, values[f.Name])
	}
	msg := bpmail.Message{
		To:      []string{str(def.Data["recipient"])},
		ReplyTo: replyTo,
		Subject: firstNonEmpty(str(def.Data["subject"]), p.pt(lang, "forms.public.mailSubject", str(def.Data["title"]))),
		Text:    text.String(),
	}
	// The submission is stored either way; a mail failure is logged and
	// visible in the admin (not mailed), but not shown to the visitor.
	if err := p.reg.Mailer.Send(ctx, msg); err != nil {
		p.reg.Logger.Error("forms: send mail", "err", err, "submission", subID)
	} else {
		p.reg.Store.DB().ExecContext(ctx, `UPDATE form_submissions SET mailed = 1 WHERE id = ?`, subID)
	}
	success()
}

// pt translates a visitor-facing text into the site language.
func (p *Plugin) pt(lang, key string, args ...any) string { return p.texts.T(lang, key, args...) }

// siteLang is the website language from the settings (default i18n.Default).
func (p *Plugin) siteLang(ctx context.Context) string {
	if s, err := settings.Load(ctx, p.reg.Store); err == nil && s.Language != "" {
		return s.Language
	}
	return i18n.Default
}

func str(v any) string { s, _ := v.(string); return s }

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
