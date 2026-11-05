// Package plugin is baseplate's extension API. Everything – including the
// built-in page type and fields – is registered through a Registry.
package plugin

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/janmarkuslanger/baseplate/content"
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
}

// Event names for hooks.
type Event string

const (
	// EntryBeforeSave runs after validation, before the entry is stored.
	// Returning an error aborts the save.
	EntryBeforeSave Event = "entry.beforeSave"
	EntryAfterSave  Event = "entry.afterSave"
	// BuildAfter runs after a successful SSG build; the entry is nil.
	BuildAfter Event = "build.after"
)

type Hook func(ctx context.Context, e *storage.Entry) error

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
	funcs          TemplateFuncs
	migrations     []storage.Migration
	dataMigrations []DataMigration
	hooks          map[Event][]Hook
}

func NewRegistry(svc Services) *Registry {
	return &Registry{
		Services: svc,
		Schema:   content.NewSchema(),
		funcs:    TemplateFuncs{},
		hooks:    map[Event][]Hook{},
	}
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

func (r *Registry) Kind(k content.Kind) error                   { return r.Schema.AddKind(k) }
func (r *Registry) ContentType(t content.Type) error            { return r.Schema.AddType(t) }
func (r *Registry) Extend(typ string, f ...content.Field) error { return r.Schema.Extend(typ, f...) }
func (r *Registry) Block(b content.Block) error                 { return r.Schema.AddBlock(b) }

func (r *Registry) Generator(g Generator)         { r.generators = append(r.generators, g) }
func (r *Registry) AdminPage(p AdminPage)         { r.adminPages = append(r.adminPages, p) }
func (r *Registry) PublicRoute(rt Route)          { r.routes = append(r.routes, rt) }
func (r *Registry) On(e Event, h Hook)            { r.hooks[e] = append(r.hooks[e], h) }
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

// AdminTemplates adds templates used by AdminPage handlers.
func (r *Registry) AdminTemplates(f fs.FS) { r.adminTemplates = append(r.adminTemplates, f) }

// Funcs adds frontend template functions.
func (r *Registry) Funcs(m TemplateFuncs) {
	for k, v := range m {
		r.funcs[k] = v
	}
}

// Accessors used by the core.

func (r *Registry) Plugins() []string               { return r.plugins }
func (r *Registry) Generators() []Generator         { return r.generators }
func (r *Registry) AdminPages() []AdminPage         { return r.adminPages }
func (r *Registry) Routes() []Route                 { return r.routes }
func (r *Registry) TemplateFS() []fs.FS             { return r.templates }
func (r *Registry) AdminTemplateFS() []fs.FS        { return r.adminTemplates }
func (r *Registry) TemplateFuncs() TemplateFuncs    { return r.funcs }
func (r *Registry) Migrations() []storage.Migration { return r.migrations }
func (r *Registry) DataMigrations() []DataMigration { return r.dataMigrations }

// Run calls all hooks for e in registration order and stops at the first error.
func (r *Registry) Run(ctx context.Context, e Event, entry *storage.Entry) error {
	for _, h := range r.hooks[e] {
		if err := h(ctx, entry); err != nil {
			return err
		}
	}
	return nil
}
