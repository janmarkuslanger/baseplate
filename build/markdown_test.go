package build

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/storage"
)

func TestMarkdownOutputsEscapeText(t *testing.T) {
	b, st := setup(t)
	add(t, st, fields.SettingsType, fields.SettingsType, storage.Published, map[string]any{"siteName": "A*B", "baseURL": "https://a.test", "llmsTxt": true})
	add(t, st, fields.PageType, "x", storage.Published, map[string]any{
		"title": "About [x](y) <b>bold</b>",
		"seo":   map[string]any{"metaDescription": "eins\n# zwei"},
		"body":  []any{map[string]any{"_type": "heading", "text": "*wichtig*"}},
	})
	if _, err := b.Build(context.Background()); err != nil {
		t.Fatal(err)
	}

	md := read(t, b, "x.md")
	for _, want := range []string{"# About \\[x\\](y) \\<b\\>bold\\</b\\>\n", "> eins # zwei\n", "## \\*wichtig\\*"} {
		if !strings.Contains(md, want) {
			t.Errorf("x.md missing %q:\n%s", want, md)
		}
	}
	llms := read(t, b, "llms.txt")
	for _, want := range []string{"# A\\*B\n", "- [About \\[x\\](y) \\<b\\>bold\\</b\\>](https://a.test/x.md): eins # zwei\n"} {
		if !strings.Contains(llms, want) {
			t.Errorf("llms.txt missing %q:\n%s", want, llms)
		}
	}
}

// A noindex page gets no Markdown variant: a static host cannot send
// X-Robots-Tag for the .md file.
func TestNoindexPageHasNoMarkdownVariant(t *testing.T) {
	b, st := setup(t)
	add(t, st, fields.SettingsType, fields.SettingsType, storage.Published, map[string]any{"baseURL": "https://a.test"})
	add(t, st, fields.PageType, "geheim", storage.Published, map[string]any{"title": "Intern", "seo": map[string]any{"noindex": true}})
	if _, err := b.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(b.DistDir, "geheim.md")); !os.IsNotExist(err) {
		t.Error("noindex page has a Markdown variant")
	}
	if html := read(t, b, "geheim/index.html"); strings.Contains(html, "text/markdown") {
		t.Errorf("noindex page links to a Markdown variant:\n%s", html)
	}
}

func TestDisabledMarkdownVariantIsNotLinked(t *testing.T) {
	b, st := setup(t)
	b.Theme.(fstest.MapFS)["templates/markdown.md"] = &fstest.MapFile{}
	add(t, st, fields.SettingsType, fields.SettingsType, storage.Published, map[string]any{"baseURL": "https://a.test", "llmsTxt": true})
	add(t, st, fields.PageType, "about", storage.Published, map[string]any{"title": "About"})
	if _, err := b.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(b.DistDir, "about.md")); !os.IsNotExist(err) {
		t.Error("Markdown variant written although templates/markdown.md is empty")
	}
	if html := read(t, b, "about/index.html"); strings.Contains(html, "text/markdown") {
		t.Errorf("page links to a Markdown variant that does not exist:\n%s", html)
	}
	if llms := read(t, b, "llms.txt"); !strings.Contains(llms, "- [About](https://a.test/about/)") {
		t.Errorf("llms.txt must link the page itself without a Markdown variant:\n%s", llms)
	}
}
