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
	bpmail "github.com/janmarkuslanger/baseplate/mail"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/storage"
)

//go:embed templates
var frontendFS embed.FS

//go:embed admin
var adminFS embed.FS

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
	{Value: "text", Label: "Text (einzeilig)"},
	{Value: "textarea", Label: "Text (mehrzeilig)"},
	{Value: "email", Label: "E-Mail"},
	{Value: "tel", Label: "Telefon"},
	{Value: "checkbox", Label: "Checkbox (z. B. Datenschutz)"},
}

type Plugin struct {
	// Limit is the number of submissions per IP per Window (default 5 / 10 min).
	Limit  int
	Window time.Duration

	reg     *plugin.Registry
	pages   *template.Template
	mu      sync.Mutex
	hits    map[string][]time.Time
	nowFunc func() time.Time
}

func New() *Plugin { return &Plugin{Limit: 5, Window: 10 * time.Minute} }

func (p *Plugin) Name() string { return "forms" }

func (p *Plugin) Register(r *plugin.Registry) error {
	p.reg = r
	p.hits = map[string][]time.Time{}
	if p.nowFunc == nil {
		p.nowFunc = time.Now
	}
	var err error
	p.pages, err = template.ParseFS(frontendFS, "templates/response.html")
	if err != nil {
		return err
	}
	if err := r.ContentType(content.Type{
		Name: TypeName, Label: "Formular", LabelPlural: "Formulare",
		Fields: []content.Field{
			{Name: "title", Label: "Name des Formulars", Kind: fields.Text, Required: true, Help: "Intern, z. B. „Kontakt“."},
			{Name: "fields", Label: "Felder", Kind: fields.List, Required: true, Fields: []content.Field{
				{Name: "label", Label: "Beschriftung", Kind: fields.Text, Required: true},
				{Name: "name", Label: "Technischer Name", Kind: fields.Slug, Required: true, Help: "z. B. nachricht"},
				{Name: "type", Label: "Typ", Kind: fields.Select, Required: true, Options: fieldTypes},
				{Name: "required", Label: "Pflichtfeld", Kind: fields.Bool},
			}},
			{Name: "submitLabel", Label: "Button-Text", Kind: fields.Text, Help: "Standard: Absenden"},
			{Name: "recipient", Label: "Empfänger (E-Mail)", Kind: fields.Email, Required: true, Tab: "Versand"},
			{Name: "subject", Label: "Betreff der E-Mail", Kind: fields.Text, Tab: "Versand"},
			{Name: "successMessage", Label: "Danke-Text", Kind: fields.Textarea, Tab: "Versand", Help: "Wird nach dem Absenden angezeigt."},
			{Name: "successURL", Label: "Weiterleitung nach dem Absenden", Kind: fields.URL, Tab: "Versand", Help: "Optional, z. B. /danke/ – ersetzt den Danke-Text."},
		},
	}); err != nil {
		return err
	}
	if err := r.Block(content.Block{
		Name: BlockName, Label: "Formular",
		Fields: []content.Field{{Name: "form", Label: "Formular", Kind: fields.Relation, Ref: TypeName, Required: true}},
	}); err != nil {
		return err
	}
	tmpl, _ := fs.Sub(frontendFS, "templates/blocks")
	r.Templates(tmpl)
	r.Funcs(plugin.TemplateFuncs{"formByID": p.formByID})
	adm, _ := fs.Sub(adminFS, "admin")
	r.AdminTemplates(adm)
	r.AdminPage(plugin.AdminPage{Path: "forms/submissions", Label: "Anfragen", Handler: p.adminSubmissions})
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
	var n int64
	switch v := id.(type) {
	case interface{ RefID() int64 }: // resolved relation in build data
		n = v.RefID()
	case float64:
		n = int64(v)
	case int64:
		n = v
	default:
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
	Title   string
	Message string
	Errors  []string
	Back    string
}

func (p *Plugin) respond(w http.ResponseWriter, status int, res response) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	p.pages.ExecuteTemplate(w, "response.html", res)
}

func (p *Plugin) submit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	back := r.Referer()
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	def, err := p.load(ctx, id)
	if err != nil {
		p.respond(w, http.StatusNotFound, response{Title: "Formular nicht gefunden", Back: back})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := r.ParseForm(); err != nil {
		p.respond(w, http.StatusBadRequest, response{Title: "Ungültige Anfrage", Back: back})
		return
	}
	success := func() {
		if u := str(def.Data["successURL"]); u != "" {
			http.Redirect(w, r, u, http.StatusSeeOther)
			return
		}
		p.respond(w, http.StatusOK, response{Title: "Vielen Dank!", Message: firstNonEmpty(str(def.Data["successMessage"]), "Ihre Nachricht wurde gesendet."), Back: back})
	}
	if r.PostFormValue(HoneypotField) != "" {
		success() // pretend success so bots learn nothing
		return
	}
	if !p.allow(p.reg.ClientIP(r)) {
		p.respond(w, http.StatusTooManyRequests, response{Title: "Zu viele Anfragen", Message: "Bitte versuchen Sie es in einigen Minuten erneut.", Back: back})
		return
	}

	values := map[string]string{}
	var errs []string
	var replyTo string
	for _, f := range def.Fields {
		v := strings.TrimSpace(r.PostFormValue(f.Name))
		if f.Type == "checkbox" {
			if v != "" {
				v = "ja"
			}
		}
		switch {
		case f.Required && v == "":
			errs = append(errs, f.Label+": Pflichtfeld")
		case utf8.RuneCountInString(v) > maxValueLen:
			errs = append(errs, fmt.Sprintf("%s: maximal %d Zeichen", f.Label, maxValueLen))
		case f.Type == "email" && v != "":
			if a, err := mail.ParseAddress(v); err != nil || a.Address != v {
				errs = append(errs, f.Label+": ungültige E-Mail-Adresse")
			} else if replyTo == "" {
				replyTo = v
			}
		}
		if v == "" && f.Type == "checkbox" {
			v = "nein"
		}
		values[f.Name] = v
	}
	if len(errs) > 0 {
		p.respond(w, http.StatusUnprocessableEntity, response{Title: "Bitte prüfen Sie Ihre Eingaben", Errors: errs, Back: back})
		return
	}

	data, _ := json.Marshal(values)
	res, err := p.reg.Store.DB().ExecContext(ctx, `INSERT INTO form_submissions (form_id, data, created_at) VALUES (?, ?, ?)`, def.ID, string(data), p.nowFunc().Unix())
	if err != nil {
		p.reg.Logger.Error("forms: store submission", "err", err)
		p.respond(w, http.StatusInternalServerError, response{Title: "Fehler", Message: "Ihre Nachricht konnte nicht gespeichert werden. Bitte versuchen Sie es später erneut.", Back: back})
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
		Subject: firstNonEmpty(str(def.Data["subject"]), "Neue Anfrage: "+str(def.Data["title"])),
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

func str(v any) string { s, _ := v.(string); return s }

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
