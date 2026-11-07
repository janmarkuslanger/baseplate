// Package navigation lets editors build menus in the admin: any number of
// navigations, each with a key the theme refers to and a tree of links of
// any depth. A link points to a page (optionally to an anchor on it), to a
// URL, or nowhere – then it is a heading for its children.
//
// Templates render a navigation by key:
//
//	{{range navigation "main" (entries "navigation") .}}
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

// TypeName is the content type of navigations; LinkBlock is the block of
// one link, which nests itself for the children.
const (
	TypeName  = "navigation"
	LinkBlock = "navlink"
)

//go:embed locales/*.json
var localesFS embed.FS

// Plugin adds the navigation type and the "navigation" template function.
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
	if err := r.Block(content.Block{
		Name: LinkBlock, Label: "navigation.link", Restricted: true,
		Fields: []content.Field{
			{Name: "label", Label: "navigation.label", Kind: fields.Text, Required: true},
			{Name: "page", Label: "navigation.page", Kind: fields.Relation, Ref: fields.PageType},
			{Name: "anchor", Label: "navigation.anchor", Kind: fields.Slug, Help: "navigation.anchor.help"},
			{Name: "url", Label: "navigation.url", Kind: fields.URL, Help: "navigation.url.help"},
			{Name: "children", Label: "navigation.children", Kind: fields.Blocks, Blocks: []string{LinkBlock}},
		},
	}); err != nil {
		return err
	}
	if err := r.ContentType(content.Type{
		Name: TypeName, Label: "navigation.type", LabelPlural: "navigation.type.plural", TitleField: "name",
		Fields: []content.Field{
			{Name: "name", Label: "navigation.name", Kind: fields.Text, Required: true},
			{Name: "key", Label: "navigation.key", Kind: fields.Slug, Required: true, Help: "navigation.key.help"},
			{Name: "items", Label: "navigation.items", Kind: fields.Blocks, Blocks: []string{LinkBlock}},
		},
	}); err != nil {
		return err
	}
	entries.BeforeSave.On(r, p.uniqueKey)
	return r.Funcs(plugin.TemplateFuncs{"navigation": p.navigation})
}

// uniqueKey refuses a navigation whose key another navigation uses, drafts
// included, so a key always names exactly one navigation. Hooks run before
// the save transaction, so two editors saving the same new key at the same
// moment are not caught; for menus edited by a few people this is accepted.
func (p *Plugin) uniqueKey(ctx context.Context, e *entries.Event) error {
	if e.Type.Name != TypeName {
		return nil
	}
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
	return nil
}
