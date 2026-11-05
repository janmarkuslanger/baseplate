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
	"path"
	"strings"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/fields"
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
	t      *template.Template
	schema *content.Schema
}

// New builds the template set from the defaults, all plugin template FSs
// and the theme's "templates" directory (if theme is non-nil).
func New(reg *plugin.Registry, theme fs.FS) (*Renderer, error) {
	r := &Renderer{schema: reg.Schema}
	funcs := template.FuncMap{
		"blocks":   r.blocks,
		"richtext": func(s any) template.HTML { return template.HTML(fields.SanitizeHTML(fmt.Sprint(s))) },
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
	return r, nil
}

// parseAll parses every .html file of fsys, recursively.
func parseAll(t *template.Template, fsys fs.FS) error {
	return fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
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
		name, _ := m[fields.BlockTypeKey].(string)
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
	for _, f := range p.Type.Fields {
		if f.Kind == fields.Blocks {
			r.markdownBlocks(&b, p.Data[f.Name])
		}
	}
	return strings.TrimSpace(b.String()) + "\n"
}

// markdownBlocks writes each block's Markdown, then recurses into the
// block's own blocks fields (containers such as articles).
func (r *Renderer) markdownBlocks(b *strings.Builder, items any) {
	list, _ := items.([]any)
	for _, it := range list {
		m, _ := it.(map[string]any)
		name, _ := m[fields.BlockTypeKey].(string)
		blk, ok := r.schema.Block(name)
		if !ok {
			continue
		}
		if blk.Markdown != nil {
			if md := strings.TrimSpace(blk.Markdown(m)); md != "" {
				b.WriteString(md + "\n\n")
			}
		}
		for _, f := range blk.Fields {
			if f.Kind == fields.Blocks {
				r.markdownBlocks(b, m[f.Name])
			}
		}
	}
}
