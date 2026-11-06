// Package plugin is baseplate's extension API. Everything – including the
// built-in page type and fields – is registered through a Registry.
package plugin

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/entries"
	"github.com/janmarkuslanger/baseplate/hook"
	"github.com/janmarkuslanger/baseplate/mail"
	"github.com/janmarkuslanger/baseplate/storage"
)

// Plugin is implemented by every extension. Register is called once at
// startup, after storage and mail are available.
type Plugin interface {
	Name() string
	Register(r *Registry) error
}

// Services are the runtime dependencies available to plugins.
type Services struct {
	Store  storage.Store
	Mailer mail.Mailer
	Logger *slog.Logger
	// Media stores uploads in the media library (the admin's uploads).
	Media Media
	// Entries saves and deletes content with the same rules and hooks as the
	// admin. Use it instead of Store.CreateEntry/UpdateEntry.
	Entries *entries.Service
	// ClientIP returns the visitor's IP, honouring Config.TrustedProxyHeader.
	// Use it for rate limits instead of r.RemoteAddr.
	ClientIP func(r *http.Request) string
}

// Media is the media library as seen by plugins.
type Media interface {
	// Save stores a file and returns its URL (/media/…).
	Save(r io.Reader, filename string) (string, error)
}

// RemoteIP returns the host part of r.RemoteAddr.
func RemoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ProxyIP returns a ClientIP func that reads the first address of header
// (e.g. "X-Forwarded-For") and falls back to RemoteIP. Only use it behind a
// proxy that overwrites the header; otherwise clients can spoof it.
func ProxyIP(header string) func(r *http.Request) string {
	return func(r *http.Request) string {
		if v := r.Header.Get(header); v != "" {
			first, _, _ := strings.Cut(v, ",")
			if ip := strings.TrimSpace(first); ip != "" {
				return ip
			}
		}
		return RemoteIP(r)
	}
}

// BuildEvent is the payload of BuildAfter.
type BuildEvent struct {
	DistDir string
	Pages   int
}

// BuildAfter runs after a successful build. Errors are logged. The entry
// hooks are entries.BeforeSave and entries.AfterSave.
var BuildAfter = hook.NewAction[*BuildEvent]("build.after", "after a successful static build; errors are logged")

// AdminUI lets plugin admin pages render inside the admin layout.
type AdminUI interface {
	// Render executes an admin template (registered via AdminTemplates)
	// inside the admin layout.
	Render(w http.ResponseWriter, r *http.Request, name string, data any)
	// CSRFToken returns the token to embed in POST forms.
	CSRFToken(r *http.Request) string
}

// AdminPage is an extra page in the admin. Path is relative to /admin/.
type AdminPage struct {
	Path    string // e.g. "forms/submissions"
	Label   string // nav label; empty hides it from the navigation
	Handler func(w http.ResponseWriter, r *http.Request, ui AdminUI)
}

// Route is a public endpoint served by the CMS server (not part of the
// static build), e.g. form submissions.
type Route struct {
	Pattern string // net/http pattern, e.g. "POST /api/forms/{id}"
	Handler http.Handler
}

// Site gives generators access to content and output during a build.
type Site interface {
	Context() context.Context
	Settings() map[string]any
	Published(typ string) ([]storage.Entry, error)
	// Render executes a frontend template and writes it to path in dist/.
	Render(path, template string, data any) error
	// RenderPage is Render wrapped in a layout like entry pages: layout ""
	// is the theme's "layout" (if defined), "-" none. The layout gets
	// render.Layout{Page: data, Content: …}.
	RenderPage(path, template, layout string, data any) error
	WriteFile(path string, data []byte) error
}

// Generator produces extra output during a build (archives, feeds, ...).
type Generator func(s Site) error

// TemplateFuncs are extra functions available in frontend templates.
type TemplateFuncs map[string]any

// Registry collects everything plugins contribute.
type Registry struct {
	Services
	Schema *content.Schema

	plugins        []string
	generators     []Generator
	adminPages     []AdminPage
	routes         []Route
	templates      []fs.FS
	adminTemplates []fs.FS
	public         []fs.FS
	funcs          TemplateFuncs
	migrations     []storage.Migration
	dataMigrations []DataMigration
	hooks          hook.Set
}

// NewRegistry returns an empty registry with the given services; the
// entries service is created when svc has none.
func NewRegistry(svc Services) *Registry {
	if svc.ClientIP == nil {
		svc.ClientIP = RemoteIP
	}
	r := &Registry{
		Services: svc,
		Schema:   content.NewSchema(),
		funcs:    TemplateFuncs{},
	}
	if r.Entries == nil {
		r.Entries = &entries.Service{Store: svc.Store, Schema: r.Schema, Hooks: r}
		if svc.Logger != nil {
			r.Entries.Logger = svc.Logger
		}
	}
	return r
}

// Use registers plugins in order.
func (r *Registry) Use(ps ...Plugin) error {
	for _, p := range ps {
		for _, n := range r.plugins {
			if n == p.Name() {
				return fmt.Errorf("plugin %q registered twice", n)
			}
		}
		if err := p.Register(r); err != nil {
			return fmt.Errorf("plugin %s: %w", p.Name(), err)
		}
		r.plugins = append(r.plugins, p.Name())
	}
	return nil
}

// Kind adds a field kind.
func (r *Registry) Kind(k content.Kind) error { return r.Schema.AddKind(k) }

// ContentType adds a content type; it appears in the admin automatically.
func (r *Registry) ContentType(t content.Type) error { return r.Schema.AddType(t) }

// Extend adds fields to an existing type, also one of the core or another plugin.
func (r *Registry) Extend(typ string, f ...content.Field) error { return r.Schema.Extend(typ, f...) }

// Block adds a content element for blocks fields.
func (r *Registry) Block(b content.Block) error { return r.Schema.AddBlock(b) }

// AllowBlocks restricts a blocks field of any type (also one defined by
// another plugin or the core) to the named blocks; path uses dots for
// fields inside groups. Empty Field.Blocks means: all registered blocks.
func (r *Registry) AllowBlocks(typ, path string, blocks ...string) error {
	return r.Schema.AllowBlocks(typ, path, blocks...)
}

// Generator adds a build step for extra pages (archives, 404, …).
func (r *Registry) Generator(g Generator) { r.generators = append(r.generators, g) }

// AdminPage adds a screen to the admin.
func (r *Registry) AdminPage(p AdminPage) { r.adminPages = append(r.adminPages, p) }

// PublicRoute adds an endpoint to the CMS server (not part of the static build).
func (r *Registry) PublicRoute(rt Route) { r.routes = append(r.routes, rt) }

// Migration adds a schema change for plugin-owned tables.
func (r *Registry) Migration(m storage.Migration) { r.migrations = append(r.migrations, m) }

// DataMigration changes stored content after a schema change, e.g. copying
// values of a renamed field. It runs once, after tables are synced, using the
// regular entry methods of the store. Name must be unique and stable.
// Write migrations so they can be re-run safely: a failed run is retried on
// the next start.
type DataMigration struct {
	Name string
	Run  func(ctx context.Context, st storage.Store) error
}

// DataMigration registers a data migration (see DataMigration).
func (r *Registry) DataMigration(m DataMigration) { r.dataMigrations = append(r.dataMigrations, m) }

// Templates adds frontend templates. Later registrations override earlier
// ones with the same name, so the customer theme (registered last) wins.
func (r *Registry) Templates(f fs.FS) { r.templates = append(r.templates, f) }

// Public adds files copied 1:1 to the root of the static site (favicon,
// verification files, …). A path that another file of the build already
// uses fails the build.
func (r *Registry) Public(f fs.FS) { r.public = append(r.public, f) }

// AdminTemplates adds templates used by AdminPage handlers.
func (r *Registry) AdminTemplates(f fs.FS) { r.adminTemplates = append(r.adminTemplates, f) }

// Funcs adds frontend template functions.
func (r *Registry) Funcs(m TemplateFuncs) {
	for k, v := range m {
		r.funcs[k] = v
	}
}

// Accessors used by the core.

// Plugins lists the registered plugin names in order.
func (r *Registry) Plugins() []string               { return r.plugins }
func (r *Registry) Generators() []Generator         { return r.generators }
func (r *Registry) AdminPages() []AdminPage         { return r.adminPages }
func (r *Registry) Routes() []Route                 { return r.routes }
func (r *Registry) TemplateFS() []fs.FS             { return r.templates }
func (r *Registry) AdminTemplateFS() []fs.FS        { return r.adminTemplates }
func (r *Registry) PublicFS() []fs.FS               { return r.public }
func (r *Registry) TemplateFuncs() TemplateFuncs    { return r.funcs }
func (r *Registry) Migrations() []storage.Migration { return r.migrations }
func (r *Registry) DataMigrations() []DataMigration { return r.dataMigrations }

// Hooks returns the app's hook registrations (see package hook).
func (r *Registry) Hooks() *hook.Set { return &r.hooks }
