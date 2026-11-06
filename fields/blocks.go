package fields

import (
	"fmt"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/internal/markup"
	"github.com/janmarkuslanger/baseplate/plugin"
)

// CoreBlocks registers baseplate's generic content elements (heading,
// richtext, image, cta, article). Opt in with app.Use(fields.CoreBlocks{});
// Only restricts it to some of them.
type CoreBlocks struct {
	Only []string
}

func (CoreBlocks) Name() string { return "core-blocks" }

func (c CoreBlocks) Register(r *plugin.Registry) error {
	only := map[string]bool{}
	for _, n := range c.Only {
		only[n] = true
	}
	known := map[string]bool{}
	for _, b := range coreBlocks() {
		known[b.Name] = true
		if len(only) > 0 && !only[b.Name] {
			continue
		}
		if err := r.Block(b); err != nil {
			return err
		}
	}
	for _, n := range c.Only {
		if !known[n] {
			return fmt.Errorf("fields: CoreBlocks.Only: unknown block %q", n)
		}
	}
	return nil
}

func coreBlocks() []content.Block {
	return []content.Block{
		{Name: "heading", Label: "block.heading", Fields: []content.Field{
			{Name: "text", Label: "block.heading.text", Kind: Text, Required: true},
			{Name: "level", Label: "block.heading.level", Kind: Select, Options: []content.Option{{Value: "h2", Label: "H2"}, {Value: "h3", Label: "H3"}}},
		}, Markdown: func(d map[string]any) string {
			prefix := "## "
			if d["level"] == "h3" {
				prefix = "### "
			}
			return prefix + str(d["text"])
		}},
		{Name: "richtext", Label: "block.richtext", Fields: []content.Field{
			{Name: "html", Label: "block.richtext.html", Kind: RichText, Required: true},
		}, Markdown: func(d map[string]any) string { return markup.HTMLToMarkdown(str(d["html"])) }},
		{Name: "image", Label: "block.image", Fields: []content.Field{
			{Name: "src", Label: "block.image.src", Kind: Image, Required: true},
			{Name: "alt", Label: "block.image.alt", Kind: Text, Help: "block.image.alt.help"},
			{Name: "caption", Label: "block.image.caption", Kind: Text},
		}, Markdown: func(d map[string]any) string {
			return "![" + str(d["alt"]) + "](" + str(d["src"]) + ")"
		}},
		{Name: "cta", Label: "block.cta", Fields: []content.Field{
			{Name: "text", Label: "block.cta.text", Kind: Textarea},
			{Name: "label", Label: "block.cta.label", Kind: Text, Required: true},
			{Name: "href", Label: "block.cta.href", Kind: URL, Required: true},
		}, Markdown: func(d map[string]any) string {
			md := "[" + str(d["label"]) + "](" + str(d["href"]) + ")"
			if t := str(d["text"]); t != "" {
				md = t + "\n\n" + md
			}
			return md
		}},
		// The article container nests any blocks, including articles (unbounded).
		{Name: "article", Label: "block.article", Fields: []content.Field{
			{Name: "title", Label: "block.article.title", Kind: Text},
			{Name: "children", Label: "block.article.children", Kind: Blocks},
		}, Markdown: func(d map[string]any) string {
			if t := str(d["title"]); t != "" {
				return "## " + t
			}
			return ""
		}},
	}
}
