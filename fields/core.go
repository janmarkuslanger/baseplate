package fields

import (
	"context"
	"errors"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/storage"
)

// Built-in content type names.
const (
	PageType     = "page"
	SettingsType = "settings"
)

// Tabs used by the standard field groups.
const (
	TabSEO = "SEO"
	TabLLM = "LLM"
)

// SEOField is the standard SEO group added to every routable type.
func SEOField() content.Field {
	return content.Field{
		Name: "seo", Label: "SEO", Kind: Group, Tab: TabSEO,
		Fields: []content.Field{
			{Name: "metaTitle", Label: "Meta-Titel", Kind: Text, Max: 70, Help: "Leer = Seitentitel. Ideal 50–60 Zeichen."},
			{Name: "metaDescription", Label: "Meta-Beschreibung", Kind: Textarea, Max: 170, Help: "Ideal 120–160 Zeichen."},
			{Name: "canonical", Label: "Canonical-URL", Kind: URL, Help: "Nur setzen, wenn der Inhalt woanders das Original ist."},
			{Name: "noindex", Label: "Nicht indexieren (noindex)", Kind: Bool},
			{Name: "nofollow", Label: "Links nicht folgen (nofollow)", Kind: Bool},
			{Name: "ogTitle", Label: "Social-Titel", Kind: Text, Max: 90},
			{Name: "ogDescription", Label: "Social-Beschreibung", Kind: Textarea, Max: 200},
			{Name: "ogImage", Label: "Social-Bild", Kind: Image, Help: "Empfohlen 1200×630 px."},
			{Name: "sitemapExclude", Label: "Nicht in Sitemap aufnehmen", Kind: Bool},
			{Name: "sitemapPriority", Label: "Sitemap-Priorität", Kind: Select, Options: []content.Option{
				{Value: "", Label: "Standard (0.5)"}, {Value: "1.0", Label: "1.0 – sehr wichtig"},
				{Value: "0.8", Label: "0.8"}, {Value: "0.3", Label: "0.3 – unwichtig"},
			}},
		},
	}
}

// RoutableFields wraps the fields of a routable type with title, slug and SEO.
func RoutableFields(fs ...content.Field) []content.Field {
	out := []content.Field{
		{Name: "title", Label: "Titel", Kind: Text, Required: true, Max: 200},
		{Name: "slug", Label: "URL-Pfad", Kind: Slug, Required: true, Help: "„index“ ist die Startseite."},
	}
	out = append(out, fs...)
	return append(out, SEOField())
}

// Core registers the built-in kinds, blocks and types. The app registers it
// before any other plugin.
type Core struct{}

func (Core) Name() string { return "core" }

func (Core) Register(r *plugin.Registry) error {
	for _, k := range Kinds() {
		if err := r.Kind(k); err != nil {
			return err
		}
	}
	blocks := []content.Block{
		{Name: "heading", Label: "Überschrift", Fields: []content.Field{
			{Name: "text", Label: "Text", Kind: Text, Required: true},
			{Name: "level", Label: "Ebene", Kind: Select, Options: []content.Option{{Value: "h2", Label: "H2"}, {Value: "h3", Label: "H3"}}},
		}, Markdown: func(d map[string]any) string {
			prefix := "## "
			if d["level"] == "h3" {
				prefix = "### "
			}
			return prefix + str(d["text"])
		}},
		{Name: "richtext", Label: "Text", Fields: []content.Field{
			{Name: "html", Label: "Inhalt", Kind: RichText, Required: true},
		}, Markdown: func(d map[string]any) string { return HTMLToMarkdown(str(d["html"])) }},
		{Name: "image", Label: "Bild", Fields: []content.Field{
			{Name: "src", Label: "Bild", Kind: Image, Required: true},
			{Name: "alt", Label: "Alternativtext", Kind: Text, Help: "Beschreibt das Bild für Screenreader und Suchmaschinen."},
			{Name: "caption", Label: "Bildunterschrift", Kind: Text},
		}, Markdown: func(d map[string]any) string {
			return "![" + str(d["alt"]) + "](" + str(d["src"]) + ")"
		}},
		{Name: "cta", Label: "Call-to-Action", Fields: []content.Field{
			{Name: "text", Label: "Text", Kind: Textarea},
			{Name: "label", Label: "Button-Text", Kind: Text, Required: true},
			{Name: "href", Label: "Link", Kind: URL, Required: true},
		}, Markdown: func(d map[string]any) string {
			md := "[" + str(d["label"]) + "](" + str(d["href"]) + ")"
			if t := str(d["text"]); t != "" {
				md = t + "\n\n" + md
			}
			return md
		}},
	}
	// The article container nests any blocks, including articles (unbounded).
	blocks = append(blocks, content.Block{Name: "article", Label: "Artikel", Fields: []content.Field{
		{Name: "title", Label: "Überschrift", Kind: Text},
		{Name: "children", Label: "Inhalt", Kind: Blocks},
	}, Markdown: func(d map[string]any) string {
		if t := str(d["title"]); t != "" {
			return "## " + t
		}
		return ""
	}})
	for _, b := range blocks {
		if err := r.Block(b); err != nil {
			return err
		}
	}
	if err := r.ContentType(content.Type{
		Name: PageType, Label: "Seite", LabelPlural: "Seiten", Routable: true,
		Fields: RoutableFields(
			content.Field{Name: "parent", Label: "Übergeordnete Seite", Kind: Reference, Ref: PageType},
			content.Field{Name: "body", Label: "Inhalt", Kind: Blocks},
		),
	}); err != nil {
		return err
	}
	return r.ContentType(content.Type{
		Name: SettingsType, Label: "Einstellungen", LabelPlural: "Einstellungen", Singleton: true, TitleField: "siteName",
		Fields: []content.Field{
			{Name: "siteName", Label: "Name der Website", Kind: Text, Required: true},
			{Name: "baseURL", Label: "Basis-URL", Kind: URL, Required: true, Help: "z. B. https://www.example.com – für Canonicals, Sitemap und llms.txt."},
			{Name: "language", Label: "Sprache", Kind: Text, Help: "z. B. de"},
			{Name: "serverURL", Label: "CMS-Server-URL", Kind: URL, Help: "Öffentliche URL dieses CMS. Formulare senden dorthin."},
			{Name: "defaultOgImage", Label: "Standard-Social-Bild", Kind: Image},
			{Name: "llmsTxt", Label: "llms.txt erzeugen", Kind: Bool, Tab: TabLLM, Help: "Stellt KI-Systemen unter /llms.txt eine Übersicht aller indexierbaren Seiten bereit."},
			{Name: "llmsContent", Label: "Zusatzinhalt für llms.txt", Kind: Textarea, Max: 5000, Tab: TabLLM, Help: "Markdown, erscheint vor der Seitenliste – z. B. „> Kurzbeschreibung“, Leistungen, Kontakt."},
		},
	})
}

func str(v any) string { s, _ := v.(string); return s }

// LoadSettings returns the site settings, or an empty map if none were saved yet.
func LoadSettings(ctx context.Context, st storage.Store) (map[string]any, error) {
	e, err := st.GetEntryBySlug(ctx, SettingsType, SettingsType)
	if errors.Is(err, storage.ErrNotFound) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	return e.Data, nil
}
