// Package navigation lets editors build menus in the admin: any number of
// navigations, each with a key the theme refers to and its links. A link is
// an entry of its own with a URL (external targets), a page (internal
// targets) and child links, which nest to any depth. A link with neither URL
// nor page is a heading for its children.
//
// Templates render a navigation by key for the page being rendered:
//
//	{{range navigation "main" .}}
//	  <a href="{{.URL}}"{{if .Current}} aria-current="page"{{end}}>{{.Title}}</a>
//	  {{range .Children}}…{{end}}
//	{{end}}
package navigation

import (
	"context"
	"embed"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/entries"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/i18n"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/storage"
)

// TypeName is the content type of navigations, LinkType the one of links.
const (
	TypeName = "navigation"
	LinkType = "navlink"
)

//go:embed locales/*.json
var localesFS embed.FS

// Plugin adds the navigation and link types and the "navigation" template
// function.
type Plugin struct {
	reg *plugin.Registry
}

// New returns the plugin.
func New() *Plugin { return &Plugin{} }

func (p *Plugin) Name() string { return "navigation" }

func (p *Plugin) Register(r *plugin.Registry) error {
	p.reg = r
	cats, err := i18n.Load(localesFS, "locales")
	if err != nil {
		return err
	}
	for _, c := range cats {
		i18n.Catalogs.Add(r, c)
	}
	if err := r.ContentType(content.Type{
		Name: LinkType, Label: "navigation.link", LabelPlural: "navigation.link.plural", TitleField: "label",
		Fields: []content.Field{
			{Name: "label", Label: "navigation.label", Kind: fields.Text, Required: true},
			{Name: "url", Label: "navigation.url", Kind: fields.URL, Help: "navigation.url.help"},
			{Name: "page", Label: "navigation.page", Kind: fields.Relation, Ref: fields.PageType, Help: "navigation.page.help"},
			{Name: "children", Label: "navigation.children", Kind: fields.Relation, Ref: LinkType, Many: true},
		},
	}); err != nil {
		return err
	}
	if err := r.ContentType(content.Type{
		Name: TypeName, Label: "navigation.type", LabelPlural: "navigation.type.plural", TitleField: "name",
		Fields: []content.Field{
			{Name: "name", Label: "navigation.name", Kind: fields.Text, Required: true},
			{Name: "key", Label: "navigation.key", Kind: fields.Slug, Required: true, Help: "navigation.key.help"},
			{Name: "items", Label: "navigation.items", Kind: fields.Relation, Ref: LinkType, Many: true},
		},
	}); err != nil {
		return err
	}
	entries.BeforeSave.On(r, p.check)
	return r.Funcs(plugin.TemplateFuncs{"navigation": p.navigation})
}

// check enforces what single fields cannot: a link has a URL or a page, not
// both, and a navigation key names one navigation only (drafts included).
// Hooks run before the save transaction, so two editors saving the same new
// key at the same moment are not caught; for menus this is accepted.
func (p *Plugin) check(ctx context.Context, e *entries.Event) error {
	switch e.Type.Name {
	case LinkType:
		_, hasPage := content.RefID(e.Entry.Data["page"])
		if url, _ := e.Entry.Data["url"].(string); url != "" && hasPage {
			return i18n.M("navigation.urlOrPage")
		}
	case TypeName:
		key, _ := e.Entry.Data["key"].(string)
		all, _, err := p.reg.Store.ListEntries(ctx, storage.ListQuery{Type: TypeName})
		if err != nil {
			return err
		}
		for _, other := range all {
			if other.ID != e.Entry.ID && other.Data["key"] == key {
				return i18n.M("navigation.keyTaken")
			}
		}
	}
	return nil
}
