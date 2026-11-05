// Package admin is the editor interface: server-rendered templates with
// Alpine.js for the field editor and htmx for partial updates.
package admin

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/janmarkuslanger/baseplate/auth"
	"github.com/janmarkuslanger/baseplate/build"
	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/media"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/storage"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

type Admin struct {
	Reg     *plugin.Registry
	Auth    *auth.Manager
	Media   media.Store
	Builder *build.Builder
	Logger  *slog.Logger

	base    *template.Template
	pages   map[string]*template.Template
	plugins map[string]*template.Template

	buildMu   sync.Mutex
	lastBuild *buildStatus
}

type buildStatus struct {
	Result build.Result
	Err    string
	At     time.Time
}

func (a *Admin) store() storage.Store { return a.Reg.Store }

// Init parses the admin templates. Call after all plugins are registered.
func (a *Admin) Init() error {
	base, err := template.New("").Funcs(a.funcs()).ParseFS(templateFS, "templates/layout.html", "templates/fields.html")
	if err != nil {
		return err
	}
	for _, k := range a.Reg.Schema.Kinds() {
		if k.AdminTemplate == "" {
			continue
		}
		if _, err := base.New("kind/" + k.Name).Parse(k.AdminTemplate); err != nil {
			return fmt.Errorf("admin template of kind %s: %w", k.Name, err)
		}
	}
	a.base = base
	a.pages = map[string]*template.Template{}
	for _, name := range []string{"login", "dashboard", "list", "edit", "media"} {
		t, err := template.Must(base.Clone()).ParseFS(templateFS, "templates/"+name+".html")
		if err != nil {
			return fmt.Errorf("admin template %s: %w", name, err)
		}
		a.pages[name] = t
	}
	a.plugins = map[string]*template.Template{}
	for _, fsys := range a.Reg.AdminTemplateFS() {
		err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || path.Ext(p) != ".html" {
				return err
			}
			b, err := fs.ReadFile(fsys, p)
			if err != nil {
				return err
			}
			t, err := template.Must(base.Clone()).Parse(string(b))
			if err != nil {
				return fmt.Errorf("admin template %s: %w", p, err)
			}
			a.plugins[p] = t
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// Mount registers all admin routes on mux under /admin/ and /media/.
func (a *Admin) Mount(mux *http.ServeMux) {
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /admin/static/", http.StripPrefix("/admin/static/", cacheStatic(http.FileServerFS(static))))
	mux.HandleFunc("GET /admin/login", a.loginPage)
	mux.HandleFunc("POST /admin/login", a.login)

	authed := http.NewServeMux()
	authed.HandleFunc("POST /admin/logout", a.logout)
	authed.HandleFunc("GET /admin/{$}", a.dashboard)
	authed.HandleFunc("GET /admin/c/{type}", a.list)
	authed.HandleFunc("GET /admin/c/{type}/new", a.edit)
	authed.HandleFunc("GET /admin/c/{type}/{id}", a.edit)
	authed.HandleFunc("POST /admin/c/{type}/save", a.save)
	authed.HandleFunc("POST /admin/c/{type}/{id}/delete", a.delete)
	authed.HandleFunc("GET /admin/media", a.mediaPage)
	authed.HandleFunc("GET /admin/media.json", a.mediaJSON)
	authed.HandleFunc("POST /admin/media", a.upload)
	authed.HandleFunc("POST /admin/build", a.build)
	for _, p := range a.Reg.AdminPages() {
		p := p
		authed.HandleFunc("/admin/p/"+strings.Trim(p.Path, "/"), func(w http.ResponseWriter, r *http.Request) {
			p.Handler(w, r, ui{a: a, active: "p/" + p.Path})
		})
	}
	mux.Handle("/admin/", a.Auth.Require("/admin/login", authed))
	mux.Handle("GET /media/", http.StripPrefix("/media/", http.FileServer(http.Dir(a.Media.Dir))))
}

func cacheStatic(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		h.ServeHTTP(w, r)
	})
}

// --- layout ---

type navItem struct {
	Label, URL, Key string
}

type view struct {
	Title  string
	Active string
	Nav    []navItem
	User   *storage.User
	CSRF   string
	Flash  string
	Page   any
}

func (a *Admin) nav() []navItem {
	var items []navItem
	for _, t := range a.Reg.Schema.Types() {
		if t.Hidden || t.Singleton {
			continue
		}
		items = append(items, navItem{Label: t.LabelPlural, URL: "/admin/c/" + t.Name, Key: "c/" + t.Name})
	}
	for _, p := range a.Reg.AdminPages() {
		if p.Label != "" {
			items = append(items, navItem{Label: p.Label, URL: "/admin/p/" + p.Path, Key: "p/" + p.Path})
		}
	}
	items = append(items, navItem{Label: "Medien", URL: "/admin/media", Key: "media"})
	for _, t := range a.Reg.Schema.Types() {
		if t.Singleton && !t.Hidden {
			items = append(items, navItem{Label: t.Label, URL: "/admin/c/" + t.Name, Key: "c/" + t.Name})
		}
	}
	return items
}

func (a *Admin) render(w http.ResponseWriter, r *http.Request, status int, tmpl *template.Template, v view) {
	if cur := auth.FromContext(r.Context()); cur != nil {
		v.User, v.CSRF = cur.User, cur.Session.CSRF
		v.Nav = a.nav()
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "layout", v); err != nil {
		a.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}

func (a *Admin) fail(w http.ResponseWriter, err error) {
	a.Logger.Error("admin", "err", err)
	http.Error(w, "Interner Fehler", http.StatusInternalServerError)
}

// ui implements plugin.AdminUI.
type ui struct {
	a      *Admin
	active string
}

func (u ui) Render(w http.ResponseWriter, r *http.Request, name string, data any) {
	t, ok := u.a.plugins[name]
	if !ok {
		u.a.fail(w, fmt.Errorf("admin template %q not registered", name))
		return
	}
	u.a.render(w, r, http.StatusOK, t, view{Active: u.active, Page: data})
}

func (u ui) CSRFToken(r *http.Request) string {
	if cur := auth.FromContext(r.Context()); cur != nil {
		return cur.Session.CSRF
	}
	return ""
}

// --- auth ---

func (a *Admin) loginPage(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, http.StatusOK, a.pages["login"], view{Title: "Anmelden"})
}

func (a *Admin) login(w http.ResponseWriter, r *http.Request) {
	// Unauthenticated: bound the body before the form is parsed.
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	ses, err := a.Auth.Login(r.Context(), clientIP(r), r.FormValue("email"), r.FormValue("password"))
	if err != nil {
		msg := err.Error()
		if !errors.Is(err, auth.ErrInvalidLogin) && !errors.Is(err, auth.ErrRateLimited) {
			a.Logger.Error("login", "err", err)
			msg = "Anmeldung fehlgeschlagen"
		}
		a.render(w, r, http.StatusUnauthorized, a.pages["login"], view{Title: "Anmelden", Flash: msg, Page: r.FormValue("email")})
		return
	}
	a.Auth.SetCookie(w, r, ses)
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

func (a *Admin) logout(w http.ResponseWriter, r *http.Request) {
	a.Auth.Logout(w, r)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// --- dashboard & build ---

type typeCount struct {
	Type             *content.Type
	Total, Published int
}

func (a *Admin) dashboard(w http.ResponseWriter, r *http.Request) {
	var counts []typeCount
	for _, t := range a.Reg.Schema.Types() {
		if t.Hidden || t.Singleton {
			continue
		}
		_, total, err := a.store().ListEntries(r.Context(), storage.ListQuery{Type: t.Name, Limit: 1})
		if err != nil {
			a.fail(w, err)
			return
		}
		_, pub, _ := a.store().ListEntries(r.Context(), storage.ListQuery{Type: t.Name, Status: storage.StatusPublished, Limit: 1})
		counts = append(counts, typeCount{Type: t, Total: total, Published: pub})
	}
	settings, _ := fields.LoadSettings(r.Context(), a.store())
	a.buildMu.Lock()
	last := a.lastBuild
	a.buildMu.Unlock()
	a.render(w, r, http.StatusOK, a.pages["dashboard"], view{Title: "Übersicht", Active: "dashboard", Page: map[string]any{
		"Counts": counts, "Build": last, "SettingsMissing": settings["baseURL"] == nil,
	}})
}

func (a *Admin) build(w http.ResponseWriter, r *http.Request) {
	res, err := a.Builder.Build(r.Context())
	st := &buildStatus{Result: res, At: time.Now()}
	if err != nil {
		st.Err = err.Error()
	}
	a.buildMu.Lock()
	a.lastBuild = st
	a.buildMu.Unlock()
	if err := a.pages["dashboard"].ExecuteTemplate(w, "build-status", st); err != nil {
		a.Logger.Error("build status", "err", err)
	}
}

// --- content ---

func (a *Admin) contentType(w http.ResponseWriter, r *http.Request) (*content.Type, bool) {
	t, ok := a.Reg.Schema.Type(r.PathValue("type"))
	if !ok {
		http.NotFound(w, r)
	}
	return t, ok
}

type row struct {
	Entry storage.Entry
	Title string
}

func (a *Admin) list(w http.ResponseWriter, r *http.Request) {
	t, ok := a.contentType(w, r)
	if !ok {
		return
	}
	if t.Singleton {
		a.edit(w, r)
		return
	}
	const perPage = 50
	pageNo, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if pageNo < 1 {
		pageNo = 1
	}
	q := storage.ListQuery{Type: t.Name, Status: r.URL.Query().Get("status"), Search: r.URL.Query().Get("q"), Limit: perPage, Offset: (pageNo - 1) * perPage}
	entries, total, err := a.store().ListEntries(r.Context(), q)
	if err != nil {
		a.fail(w, err)
		return
	}
	rows := make([]row, len(entries))
	for i, e := range entries {
		rows[i] = row{Entry: e, Title: fmt.Sprint(orDash(e.Data[t.Title()]))}
	}
	a.render(w, r, http.StatusOK, a.pages["list"], view{Title: t.LabelPlural, Active: "c/" + t.Name, Flash: flash(r), Page: map[string]any{
		"Type": t, "Rows": rows, "Total": total, "Query": q, "PageNo": pageNo,
		"HasNext": pageNo*perPage < total, "HasPrev": pageNo > 1, "Prev": pageNo - 1, "Next": pageNo + 1,
	}})
}

func orDash(v any) any {
	if v == nil || v == "" {
		return "(ohne Titel)"
	}
	return v
}

func flash(r *http.Request) string {
	switch r.URL.Query().Get("flash") {
	case "saved":
		return "Gespeichert."
	case "published":
		return "Veröffentlicht. Klicke in der Übersicht auf „Website bauen“, um die Änderung live zu bringen."
	case "deleted":
		return "Gelöscht."
	}
	return ""
}

type editPage struct {
	Type     *content.Type
	Entry    *storage.Entry
	Data     map[string]any
	Errors   content.Errors
	Blocks   map[string]any // block name -> empty item, for the "add block" menu
	Labels   map[string]string
	Settings map[string]any
	IsNew    bool
	UsedIn   []usedIn
}

// relationLabel names the relation field of a referrer: its label, or
// "<block> › <field>" when the field sits inside a block.
func (a *Admin) relationLabel(rt *content.Type, ref storage.Referrer, target string) string {
	if !ref.Nested {
		for _, f := range rt.Fields {
			if f.Name == ref.Field && f.Label != "" {
				return f.Label
			}
		}
		return ref.Field
	}
	for _, b := range a.Reg.Schema.AllBlocks() {
		for _, f := range b.Fields {
			if f.Name == ref.Field && f.Ref == target {
				return b.Label + " › " + firstNonEmpty(f.Label, f.Name)
			}
		}
	}
	return ref.Field
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// usedIn is an entry whose relation points at the edited entry.
type usedIn struct {
	Type, Title, Field, URL string
	Nested                  bool
}

func (a *Admin) usedIn(r *http.Request, t *content.Type, e *storage.Entry) []usedIn {
	if e.ID == 0 {
		return nil
	}
	refs, err := a.store().Referrers(r.Context(), t.Name, e.ID)
	if err != nil {
		a.Logger.Error("referrers", "err", err)
		return nil
	}
	out := make([]usedIn, 0, len(refs))
	for _, ref := range refs {
		rt, ok := a.Reg.Schema.Type(ref.Type)
		if !ok {
			continue
		}
		u := usedIn{Type: rt.Label, Field: ref.Field, URL: fmt.Sprintf("/admin/c/%s/%d", rt.Name, ref.ID), Nested: ref.Nested}
		u.Field = a.relationLabel(rt, ref, t.Name)
		if src, err := a.store().GetEntry(r.Context(), rt.Name, ref.ID); err == nil {
			u.Title = fmt.Sprint(orDash(src.Data[rt.Title()]))
		}
		out = append(out, u)
	}
	return out
}

func (a *Admin) edit(w http.ResponseWriter, r *http.Request) {
	t, ok := a.contentType(w, r)
	if !ok {
		return
	}
	e := &storage.Entry{Type: t.Name, Status: storage.StatusDraft, Data: map[string]any{}}
	switch {
	case t.Singleton:
		got, err := a.store().GetEntryBySlug(r.Context(), t.Name, t.Name)
		if err == nil {
			e = got
		} else if !errors.Is(err, storage.ErrNotFound) {
			a.fail(w, err)
			return
		}
	case r.PathValue("id") != "":
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		got, err := a.store().GetEntry(r.Context(), t.Name, id)
		if errors.Is(err, storage.ErrNotFound) || (err == nil && got.Type != t.Name) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			a.fail(w, err)
			return
		}
		e = got
	}
	a.renderEdit(w, r, http.StatusOK, t, e, e.Data, nil)
}

func (a *Admin) renderEdit(w http.ResponseWriter, r *http.Request, status int, t *content.Type, e *storage.Entry, data map[string]any, errs content.Errors) {
	blocks := map[string]any{}
	labels := map[string]string{}
	for _, b := range a.Reg.Schema.Blocks(content.Field{}) {
		item := skeleton(b.Fields, map[string]any{})
		item[fields.BlockTypeKey] = b.Name
		blocks[b.Name] = item
		labels[b.Name] = b.Label
	}
	settings, _ := fields.LoadSettings(r.Context(), a.store())
	title := t.Label
	if e.ID != 0 && !t.Singleton {
		title = fmt.Sprint(orDash(e.Data[t.Title()]))
	}
	a.render(w, r, status, a.pages["edit"], view{Title: title, Active: "c/" + t.Name, Flash: flash(r), Page: editPage{
		Type: t, Entry: e, Data: skeleton(t.Fields, data), Errors: errs, Blocks: blocks, Labels: labels,
		Settings: settings, IsNew: e.ID == 0, UsedIn: a.usedIn(r, t, e),
	}})
}

// skeleton fills in empty groups and lists so the Alpine editor can bind
// nested fields without null checks.
func skeleton(fs []content.Field, data map[string]any) map[string]any {
	if data == nil {
		data = map[string]any{}
	}
	for _, f := range fs {
		switch f.Kind {
		case fields.Group:
			m, _ := data[f.Name].(map[string]any)
			data[f.Name] = skeleton(f.Fields, m)
		case fields.List, fields.Blocks:
			if _, ok := data[f.Name].([]any); !ok {
				data[f.Name] = []any{}
			}
		case fields.Bool:
			if _, ok := data[f.Name].(bool); !ok {
				data[f.Name] = false
			}
		default:
			if _, ok := data[f.Name]; !ok {
				data[f.Name] = ""
			}
		}
	}
	return data
}

func (a *Admin) save(w http.ResponseWriter, r *http.Request) {
	t, ok := a.contentType(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	e := &storage.Entry{Type: t.Name}
	if id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64); id > 0 {
		got, err := a.store().GetEntry(ctx, t.Name, id)
		if err != nil || got.Type != t.Name {
			http.NotFound(w, r)
			return
		}
		e = got
	} else if t.Singleton {
		if got, err := a.store().GetEntryBySlug(ctx, t.Name, t.Name); err == nil {
			e = got
		}
	}

	var raw map[string]any
	if err := json.Unmarshal([]byte(r.FormValue("data")), &raw); err != nil {
		http.Error(w, "ungültige Daten", http.StatusBadRequest)
		return
	}
	clean, err := a.Reg.Schema.Validate(t.Fields, raw)
	errs, _ := err.(content.Errors)
	if err != nil && errs == nil {
		a.fail(w, err)
		return
	}
	if errs == nil {
		errs = content.Errors{}
	}

	switch {
	case t.Singleton:
		e.Slug, e.Status = t.Name, storage.StatusPublished
	default:
		e.Slug, _ = clean["slug"].(string)
		e.Status = storage.StatusDraft
		if r.FormValue("status") == storage.StatusPublished {
			e.Status = storage.StatusPublished
		}
	}
	if t.Routable && e.Slug != "" {
		if msg := a.slugConflict(r, t, e, clean); msg != "" {
			errs["slug"] = msg
		}
	}
	if len(errs) > 0 {
		e.Data = raw
		a.renderEdit(w, r, http.StatusUnprocessableEntity, t, e, raw, errs)
		return
	}

	e.Data = clean
	if err := a.Reg.Run(ctx, plugin.EntryBeforeSave, e); err != nil {
		a.renderEdit(w, r, http.StatusUnprocessableEntity, t, e, raw, content.Errors{"_": err.Error()})
		return
	}
	if e.ID == 0 {
		err = a.store().CreateEntry(ctx, e)
	} else {
		err = a.store().UpdateEntry(ctx, e)
	}
	if errors.Is(err, storage.ErrInvalidRelation) {
		a.renderEdit(w, r, http.StatusUnprocessableEntity, t, e, raw, content.Errors{"_": err.Error()})
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	if err := a.Reg.Run(ctx, plugin.EntryAfterSave, e); err != nil {
		a.Logger.Error("afterSave hook", "err", err)
	}
	fl := "saved"
	if e.Status == storage.StatusPublished && !t.Singleton {
		fl = "published"
	}
	target := fmt.Sprintf("/admin/c/%s/%d?flash=%s", t.Name, e.ID, fl)
	if t.Singleton {
		target = "/admin/c/" + t.Name + "?flash=saved"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// slugConflict reports a sibling with the same slug (same type and parent).
func (a *Admin) slugConflict(r *http.Request, t *content.Type, e *storage.Entry, data map[string]any) string {
	all, _, err := a.store().ListEntries(r.Context(), storage.ListQuery{Type: t.Name})
	if err != nil {
		return ""
	}
	parent := parentID(data["parent"])
	for _, o := range all {
		if o.ID != e.ID && o.Slug == e.Slug && parentID(o.Data["parent"]) == parent {
			return "dieser URL-Pfad ist bereits vergeben"
		}
	}
	return ""
}

// parentID reads a relation value (int64; float64 when it comes from JSON).
func parentID(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}

func (a *Admin) delete(w http.ResponseWriter, r *http.Request) {
	t, ok := a.contentType(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	e, err := a.store().GetEntry(r.Context(), t.Name, id)
	if err != nil || e.Type != t.Name {
		http.NotFound(w, r)
		return
	}
	if err := a.store().DeleteEntry(r.Context(), t.Name, id); err != nil {
		a.fail(w, err)
		return
	}
	http.Redirect(w, r, "/admin/c/"+t.Name+"?flash=deleted", http.StatusSeeOther)
}

// refOptions lists entries of a type for relation pickers.
func (a *Admin) refOptions(typ string) []content.Option {
	t, ok := a.Reg.Schema.Type(typ)
	if !ok {
		return nil
	}
	list, _, err := a.store().ListEntries(contextBackground(), storage.ListQuery{Type: typ})
	if err != nil {
		return nil
	}
	out := make([]content.Option, 0, len(list))
	for _, e := range list {
		out = append(out, content.Option{Value: strconv.FormatInt(e.ID, 10), Label: fmt.Sprint(orDash(e.Data[t.Title()]))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// --- media ---

func (a *Admin) mediaPage(w http.ResponseWriter, r *http.Request) {
	files, err := a.Media.List()
	if err != nil {
		a.fail(w, err)
		return
	}
	a.render(w, r, http.StatusOK, a.pages["media"], view{Title: "Medien", Active: "media", Flash: r.URL.Query().Get("error"), Page: files})
}

func (a *Admin) mediaJSON(w http.ResponseWriter, r *http.Request) {
	files, err := a.Media.List()
	if err != nil {
		a.fail(w, err)
		return
	}
	if files == nil {
		files = []media.File{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(files)
}

func (a *Admin) upload(w http.ResponseWriter, r *http.Request) {
	wantsJSON := strings.Contains(r.Header.Get("Accept"), "application/json")
	respondErr := func(msg string) {
		if wantsJSON {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": msg})
			return
		}
		http.Redirect(w, r, "/admin/media?error="+urlQueryEscape(msg), http.StatusSeeOther)
	}
	file, hdr, err := r.FormFile("file")
	if r.MultipartForm != nil {
		// The server only cleans up the original request; this handler gets
		// a copy (context wrapper), so remove parsed temp files here.
		defer r.MultipartForm.RemoveAll()
	}
	if err != nil {
		respondErr("Keine Datei erhalten")
		return
	}
	defer file.Close()
	url, err := a.Media.Save(file, hdr.Filename)
	if err != nil {
		if errors.Is(err, media.ErrType) || errors.Is(err, media.ErrSize) || errors.Is(err, media.ErrContent) {
			respondErr(err.Error())
			return
		}
		a.fail(w, err)
		return
	}
	if wantsJSON {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"url": url})
		return
	}
	http.Redirect(w, r, "/admin/media", http.StatusSeeOther)
}
