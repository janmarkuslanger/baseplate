// Package render executes frontend templates. Templates come from three
// layers, later ones overriding earlier ones: baseplate defaults, plugins,
// and the customer theme.
package render

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"maps"
	"path"
	"strings"
	ttemplate "text/template"
	"time"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/i18n"
	"github.com/janmarkuslanger/baseplate/internal/markup"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/storage"
)

//go:embed templates/*.html
var defaults embed.FS

// Page is the data passed to page templates.
type Page struct {
	Site        map[string]any
	Entry       storage.Entry
	Data        map[string]any
	Type        *content.Type
	URL         string // site-relative, e.g. "/about/"
	AbsURL      string
	MarkdownURL string
	Meta        Meta
	Nav         []Link
}

// Ref is a resolved relation in template data: {{with .Data.parent}}{{.Data.title}}{{end}}.
// Data is the target's own data; its relations are not resolved again.
// URL is set for published routable targets.
type Ref struct {
	ID   int64
	Type string
	URL  string
	Data map[string]any
}

// RefID lets plugins read the target ID without importing render.
func (r *Ref) RefID() int64 { return r.ID }

// Meta holds the computed SEO values for <head>.
type Meta struct {
	Title, Description, Canonical, Robots string
	OGTitle, OGDescription, OGImage       string
}

type Link struct {
	ID     int64
	Parent int64
	Title  string
	URL    string
}

// BlockData is passed to block templates.
type BlockData struct {
	Block map[string]any
	Page  *Page
}

type Renderer struct {
	t       *template.Template
	schema  *content.Schema
	entries EntriesFunc
	i18n    *i18n.Bundle
	lang    string
	loc     *time.Location
	outputs map[string]*ttemplate.Template
}

// SetLocale sets the site language and time zone used by "date".
func (r *Renderer) SetLocale(lang string, loc *time.Location) {
	r.lang, r.loc = lang, loc
}

// EntriesFunc implements the "entries" template function:
//
//	{{range entries "blog" "sort" "-publishedDate" "limit" 3}}<a href="{{.URL}}">{{.Data.title}}</a>{{end}}
type EntriesFunc func(typ string, opts ...any) ([]*Ref, error)

// SetEntries sets the data source of the "entries" template function.
// The build sets it once all URLs are known; without it, "entries" fails.
func (r *Renderer) SetEntries(f EntriesFunc) { r.entries = f }

// New builds the template set from the defaults, all plugin template FSs
// and the theme's "templates" directory (if theme is non-nil).
func New(reg *plugin.Registry, theme fs.FS) (*Renderer, error) {
	r := &Renderer{schema: reg.Schema, i18n: i18n.NewBundle(append(i18n.Core(), i18n.Catalogs.Items(reg)...)...), lang: i18n.Default, loc: time.UTC}
	funcs := template.FuncMap{
		"blocks": r.blocks,
		"date":   r.date,
		"entries": func(typ string, opts ...any) ([]*Ref, error) {
			if r.entries == nil {
				return nil, fmt.Errorf("entries %q: only available in the build", typ)
			}
			return r.entries(typ, opts...)
		},
		"richtext": func(s any) template.HTML { return template.HTML(markup.SanitizeHTML(fmt.Sprint(s))) },
		"json": func(v any) (template.JS, error) {
			b, err := json.Marshal(v)
			return template.JS(b), err
		},
		"str": func(v any) string {
			if v == nil {
				return ""
			}
			return fmt.Sprint(v)
		},
	}
	for k, v := range reg.TemplateFuncs() {
		funcs[k] = v
	}
	r.t = template.New("").Funcs(funcs)
	layers := []fs.FS{defaults}
	layers = append(layers, reg.TemplateFS()...)
	if theme != nil {
		sub, err := fs.Sub(theme, "templates")
		if err != nil {
			return nil, err
		}
		layers = append(layers, sub)
	}
	for _, l := range layers {
		if err := parseAll(r.t, l); err != nil {
			return nil, err
		}
	}
	outputs, _ := fs.Sub(outputDefaults, "outputs")
	if err := r.parseOutputs(append([]fs.FS{outputs}, layers...), maps.Clone(funcs)); err != nil {
		return nil, err
	}
	return r, nil
}

// parseAll parses every .html file of fsys, recursively, except the
// output templates.
func parseAll(t *template.Template, fsys fs.FS) error {
	return fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && p == "output" {
			return fs.SkipDir
		}
		if err != nil || d.IsDir() || path.Ext(p) != ".html" {
			return err
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		if _, err := t.New(p).Parse(string(b)); err != nil {
			return fmt.Errorf("template %s: %w", p, err)
		}
		return nil
	})
}

func (r *Renderer) Has(name string) bool { return r.t.Lookup(name) != nil }

// DefaultLayout is the layout template used when a type names none.
const DefaultLayout = "layout"

// Layout is the data of layout templates:
//
//	{{define "layout"}}<html><head>{{template "baseplate/head" .Page}}</head><body>{{.Content}}</body></html>{{end}}
//
// Page is the *Page of entry pages, or the data a generator passes.
type Layout struct {
	Page    any
	Content template.HTML
}

// ExecutePage renders template name with data and wraps the result in
// layout ("" = DefaultLayout if defined, "-" = none). A named layout that
// does not exist is an error.
func (r *Renderer) ExecutePage(w io.Writer, name, layout string, data any) error {
	switch {
	case layout == "-", layout == "" && !r.Has(DefaultLayout):
		return r.Execute(w, name, data)
	case layout == "":
		layout = DefaultLayout
	}
	if !r.Has(layout) {
		return fmt.Errorf("render: layout %q not defined (add it to your theme)", layout)
	}
	var buf bytes.Buffer
	if err := r.Execute(&buf, name, data); err != nil {
		return err
	}
	return r.Execute(w, layout, Layout{Page: data, Content: template.HTML(buf.String())})
}

func (r *Renderer) Execute(w io.Writer, name string, data any) error {
	if !r.Has(name) {
		return fmt.Errorf("render: template %q not defined (add it to your theme)", name)
	}
	return r.t.ExecuteTemplate(w, name, data)
}

// blocks renders the items of a blocks field: {{blocks .Data.body .}}.
func (r *Renderer) blocks(items any, page *Page) (template.HTML, error) {
	list, _ := items.([]any)
	var buf bytes.Buffer
	for _, it := range list {
		m, _ := it.(map[string]any)
		name, _ := m[content.BlockTypeKey].(string)
		b, ok := r.schema.Block(name)
		if !ok {
			continue // block from a removed plugin
		}
		if err := r.Execute(&buf, b.TemplateName(), BlockData{Block: m, Page: page}); err != nil {
			return "", err
		}
		buf.WriteByte('\n')
	}
	return template.HTML(buf.String()), nil
}

// Markdown renders a page as Markdown for the .md variant.
func (r *Renderer) Markdown(p *Page) string {
	var b strings.Builder
	b.WriteString("# " + fmt.Sprint(p.Data["title"]) + "\n\n")
	if p.Meta.Description != "" {
		b.WriteString("> " + p.Meta.Description + "\n\n")
	}
	r.markdownFields(&b, p.Type.Fields, p.Data)
	return strings.TrimSpace(b.String()) + "\n"
}

// markdownFields finds blocks in fields, also inside groups and lists.
func (r *Renderer) markdownFields(b *strings.Builder, fs []content.Field, data map[string]any) {
	for _, f := range fs {
		k, _ := r.schema.Kind(f.Kind)
		switch k.Storage {
		case content.StoreBlocks:
			r.markdownBlocks(b, data[f.Name])
		case content.StoreGroup:
			if m, ok := data[f.Name].(map[string]any); ok {
				r.markdownFields(b, f.Fields, m)
			}
		case content.StoreList:
			list, _ := data[f.Name].([]any)
			for _, it := range list {
				if m, ok := it.(map[string]any); ok {
					r.markdownFields(b, f.Fields, m)
				}
			}
		}
	}
}

// markdownBlocks writes each block's Markdown, then the blocks nested in
// its fields.
func (r *Renderer) markdownBlocks(b *strings.Builder, items any) {
	list, _ := items.([]any)
	for _, it := range list {
		m, _ := it.(map[string]any)
		name, _ := m[content.BlockTypeKey].(string)
		blk, ok := r.schema.Block(name)
		if !ok {
			continue
		}
		if blk.Markdown != nil {
			if md := strings.TrimSpace(blk.Markdown(m)); md != "" {
				b.WriteString(md + "\n\n")
			}
		}
		r.markdownFields(b, blk.Fields, m)
	}
}
