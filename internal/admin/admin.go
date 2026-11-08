// Package admin is the editor interface: server-rendered templates with
// Alpine.js for the field editor and htmx for partial updates.
package admin

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/janmarkuslanger/baseplate/build"
	"github.com/janmarkuslanger/baseplate/entries"
	"github.com/janmarkuslanger/baseplate/i18n"
	"github.com/janmarkuslanger/baseplate/internal/auth"
	"github.com/janmarkuslanger/baseplate/internal/fileserver"
	"github.com/janmarkuslanger/baseplate/internal/media"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/settings"
	"github.com/janmarkuslanger/baseplate/storage"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Admin serves the editor interface under /admin/.
type Admin struct {
	Reg     *plugin.Registry
	Auth    *auth.Manager
	Media   media.Store
	Builder *build.Builder
	Logger  *slog.Logger
	// Lang is the admin language (default i18n.Default).
	Lang string
	// BuildOnPublish rebuilds the site in the background whenever published
	// content changes (publish, update, unpublish, delete), in the admin or
	// through plugin.Services.Entries (entries.PublishedChanged).
	BuildOnPublish bool

	// rebuild holds one pending background build request (BuildOnPublish);
	// stopBuilds ends autoBuild, which closes buildsDone.
	rebuild    chan struct{}
	stopBuilds context.CancelFunc
	buildsDone chan struct{}
	closeOnce  sync.Once

	i18n    *i18n.Bundle
	base    *template.Template
	pages   map[string]*template.Template
	plugins map[string]*template.Template

	lastBuild atomic.Pointer[buildStatus]
}

func (a *Admin) store() storage.Store { return a.Reg.Store }

// t translates a key (or returns a plain text unchanged).
func (a *Admin) t(key string, args ...any) string { return a.i18n.T(a.Lang, key, args...) }

// tr translates an error for display.
func (a *Admin) tr(err error) string { return a.i18n.Tr(a.Lang, err) }

// Init parses the admin templates. Call after all plugins are registered.
func (a *Admin) Init() error {
	if a.Lang == "" {
		a.Lang = i18n.Default
	}
	a.i18n = i18n.NewBundle(append(i18n.Core(), i18n.Catalogs.Items(a.Reg)...)...)
	if a.BuildOnPublish {
		ctx, cancel := context.WithCancel(context.Background())
		a.rebuild, a.stopBuilds, a.buildsDone = make(chan struct{}, 1), cancel, make(chan struct{})
		entries.PublishedChanged.On(a.Reg, func(context.Context, *entries.Event) error {
			a.contentChanged()
			return nil
		})
		go a.autoBuild(ctx)
	}
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
	mux.Handle("GET /admin/static/", http.StripPrefix("/admin/static/", cacheStatic(fileserver.New(static))))
	mux.HandleFunc("GET /admin/login", a.loginPage)
	mux.HandleFunc("POST /admin/login", a.login)

	authed := http.NewServeMux()
	authed.HandleFunc("POST /admin/logout", a.logout)
	authed.HandleFunc("GET /admin/{$}", a.dashboard)
	authed.HandleFunc("GET /admin/c/{type}", a.list)
	authed.HandleFunc("GET /admin/c/{type}/new", a.edit)
	authed.HandleFunc("GET /admin/c/{type}/{id}", a.edit)
	authed.HandleFunc("POST /admin/c/{type}/save", a.save)
	authed.HandleFunc("POST /admin/c/{type}/{id}/unpublish", a.unpublish)
	authed.HandleFunc("POST /admin/c/{type}/{id}/delete", a.delete)
	authed.HandleFunc("GET /admin/media", a.mediaPage)
	authed.HandleFunc("GET /admin/media.json", a.mediaJSON)
	authed.HandleFunc("POST /admin/media", a.upload)
	authed.HandleFunc("POST /admin/build", a.build)
	for _, p := range a.Reg.AdminPages() {
		authed.HandleFunc("/admin/p/"+strings.Trim(p.Path, "/"), func(w http.ResponseWriter, r *http.Request) {
			p.Handler(w, r, ui{a: a, active: "p/" + p.Path})
		})
	}
	mux.Handle("/admin/", a.Auth.Require("/admin/login", authed))
	mux.Handle("GET /media/", http.StripPrefix("/media/", fileserver.New(os.DirFS(a.Media.Dir))))
}

func cacheStatic(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		h.ServeHTTP(w, r)
	})
}

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
	// TZ is the site's time zone; date inputs convert with it.
	TZ   string
	Page any
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
	items = append(items, navItem{Label: "nav.media", URL: "/admin/media", Key: "media"})
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
		if cfg, err := settings.Load(r.Context(), a.store()); err == nil {
			v.TZ = cfg.Location.String()
		}
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
	http.Error(w, a.t("error.internal"), http.StatusInternalServerError)
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
