// Package navigation lets editors build menus in the admin: any number of
// navigations, each with a key the theme refers to and its items. An item
// has a title, a URL (external targets), a page (internal targets) and
// items of its own, nested to any depth and edited in place. An item with
// neither URL nor page is a heading for its items.
//
// Templates render a navigation by key for the page being rendered:
//
//	{{range navigation "main" .}}
//	  <a href="{{.URL}}"{{if .Current}} aria-current="page"{{end}}>{{.Title}}</a>
//	  {{range .Items}}…{{end}}
//	{{end}}
package navigation

import (
	"context"
	"embed"
	"regexp"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/entries"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/i18n"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/storage"
)

// TypeName is the content type of navigations, ItemBlock the block of one
// item, which nests itself for the item's items.
const (
	TypeName  = "navigation"
	ItemBlock = "navitem"
)

// keyRe is the form of a navigation key: lowercase words joined by "-".
var keyRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

//go:embed locales/*.json
var localesFS embed.FS

// Plugin adds the navigation type, its item block and the "navigation"
// template function.
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
		Name: ItemBlock, Label: "navigation.item", Restricted: true,
		Fields: []content.Field{
			{Name: "title", Label: "navigation.label", Kind: fields.Text, Required: true},
			{Name: "url", Label: "navigation.url", Kind: fields.URL, Help: "navigation.url.help"},
			{Name: "page", Label: "navigation.page", Kind: fields.Relation, Ref: fields.PageType, Help: "navigation.page.help"},
			{Name: "items", Label: "navigation.items", Kind: fields.Blocks, Blocks: []string{ItemBlock}},
		},
	}); err != nil {
		return err
	}
	if err := r.ContentType(content.Type{
		Name: TypeName, Label: "navigation.type", LabelPlural: "navigation.type.plural", TitleField: "name",
		Fields: []content.Field{
			{Name: "name", Label: "navigation.name", Kind: fields.Text, Required: true},
			{Name: "key", Label: "navigation.key", Kind: fields.Text, Required: true, Help: "navigation.key.help"},
			{Name: "items", Label: "navigation.items", Kind: fields.Blocks, Blocks: []string{ItemBlock}},
		},
	}); err != nil {
		return err
	}
	entries.BeforeSave.On(r, p.check)
	return r.Funcs(plugin.TemplateFuncs{"navigation": p.navigation})
}

// check enforces what single fields cannot: an item has a URL or a page,
// not both, and a navigation key has the form of keyRe and names one
// navigation only (drafts included). Hooks run before the save transaction,
// so two editors saving the same new key at the same moment are not caught;
// for menus this is accepted.
func (p *Plugin) check(ctx context.Context, e *entries.Event) error {
	if e.Type.Name != TypeName {
		return nil
	}
	if label, ok := urlAndPage(e.Entry.Data["items"]); ok {
		return i18n.M("navigation.urlOrPage", label)
	}
	key, _ := e.Entry.Data["key"].(string)
	if !keyRe.MatchString(key) {
		return i18n.M("navigation.keyInvalid")
	}
	all, _, err := p.reg.Store.ListEntries(ctx, storage.ListQuery{Type: TypeName, Version: storage.Draft})
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

// urlAndPage returns the title of the first item (at any depth) that has
// both a URL and a page.
func urlAndPage(items any) (string, bool) {
	list, _ := items.([]any)
	for _, it := range list {
		m, _ := it.(map[string]any)
		_, hasPage := content.RefID(m["page"])
		if url, _ := m["url"].(string); url != "" && hasPage {
			title, _ := m["title"].(string)
			return title, true
		}
		if label, ok := urlAndPage(m["items"]); ok {
			return label, true
		}
	}
	return "", false
}
