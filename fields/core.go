package fields

import (
	"context"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/settings"
	"github.com/janmarkuslanger/baseplate/storage"
)

// Built-in content type names.
const (
	PageType     = "page"
	SettingsType = settings.Type
)

// Tabs used by the standard field groups.
const (
	TabSEO = "SEO"
	TabLLM = "LLM"
)

// SEOField is the standard SEO group added to every routable type.
func SEOField() content.Field {
	return content.Field{
		Name: "seo", Label: "seo", Kind: Group, Tab: TabSEO,
		Fields: []content.Field{
			{Name: "metaTitle", Label: "seo.metaTitle", Kind: Text, Max: 70, Help: "seo.metaTitle.help"},
			{Name: "metaDescription", Label: "seo.metaDescription", Kind: Textarea, Max: 170, Help: "seo.metaDescription.help"},
			{Name: "canonical", Label: "seo.canonical", Kind: URL, Help: "seo.canonical.help"},
			{Name: "noindex", Label: "seo.noindex", Kind: Bool},
			{Name: "nofollow", Label: "seo.nofollow", Kind: Bool},
			{Name: "ogTitle", Label: "seo.ogTitle", Kind: Text, Max: 90},
			{Name: "ogDescription", Label: "seo.ogDescription", Kind: Textarea, Max: 200},
			{Name: "ogImage", Label: "seo.ogImage", Kind: Image, Help: "seo.ogImage.help"},
			{Name: "sitemapExclude", Label: "seo.sitemapExclude", Kind: Bool},
			{Name: "sitemapPriority", Label: "seo.sitemapPriority", Kind: Select, Options: []content.Option{
				{Value: "", Label: "seo.priority.default"}, {Value: "1.0", Label: "seo.priority.high"},
				{Value: "0.8", Label: "0.8"}, {Value: "0.3", Label: "seo.priority.low"},
			}},
		},
	}
}

// RoutableFields wraps the fields of a routable type with title, slug and SEO.
func RoutableFields(fs ...content.Field) []content.Field {
	out := []content.Field{
		{Name: "title", Label: "field.title", Kind: Text, Required: true, Max: 200},
		{Name: "slug", Label: "field.slug", Kind: Slug, Required: true, Help: "field.slug.help"},
	}
	out = append(out, fs...)
	return append(out, SEOField())
}

// Core registers the built-in kinds and types (page, settings). The app
// registers it before any other plugin. The built-in blocks are separate
// (CoreBlocks), so a site only offers the blocks it registers.
type Core struct{}

func (Core) Name() string { return "core" }

func (Core) Register(r *plugin.Registry) error {
	for _, k := range Kinds() {
		if err := r.Kind(k); err != nil {
			return err
		}
	}
	if err := r.ContentType(content.Type{
		Name: PageType, Label: "type.page", LabelPlural: "type.page.plural", Routable: true, Path: "/{parents}/{slug}/",
		Fields: RoutableFields(
			content.Field{Name: "parent", Label: "page.parent", Kind: Relation, Ref: PageType, Inverse: "children"},
			content.Field{Name: "body", Label: "page.body", Kind: Blocks},
		),
	}); err != nil {
		return err
	}
	return r.ContentType(content.Type{
		Name: SettingsType, Label: "type.settings", LabelPlural: "type.settings.plural", Singleton: true, TitleField: settings.SiteName,
		Fields: []content.Field{
			{Name: settings.SiteName, Label: "settings.siteName", Kind: Text, Required: true},
			{Name: settings.BaseURL, Label: "settings.baseURL", Kind: URL, Required: true, Help: "settings.baseURL.help"},
			{Name: settings.Language, Label: "settings.language", Kind: Text, Help: "settings.language.help"},
			{Name: settings.ServerURL, Label: "settings.serverURL", Kind: URL, Help: "settings.serverURL.help"},
			{Name: settings.Timezone, Label: "settings.timezone", Kind: Timezone, Help: "settings.timezone.help"},
			{Name: settings.DefaultOGImage, Label: "settings.defaultOgImage", Kind: Image},
			{Name: settings.LLMSTxt, Label: "settings.llmsTxt", Kind: Bool, Tab: TabLLM, Help: "settings.llmsTxt.help"},
			{Name: settings.LLMSContent, Label: "settings.llmsContent", Kind: Textarea, Max: 5000, Tab: TabLLM, Help: "settings.llmsContent.help"},
		},
	})
}

func str(v any) string { s, _ := v.(string); return s }

// LoadSettings returns the settings data.
//
// Deprecated: use settings.Load for typed access.
func LoadSettings(ctx context.Context, st storage.Entries) (map[string]any, error) {
	s, err := settings.Load(ctx, st)
	return s.Data, err
}
