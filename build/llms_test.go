package build

import (
	"context"
	"strings"
	"testing"

	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/storage"
)

// The llms.txt section heading follows the site language, not German only.
func TestLLMsTxtHeadingInSiteLanguage(t *testing.T) {
	for lang, want := range map[string]string{"": "## Seiten\n", "de-AT": "## Seiten\n", "en": "## Pages\n"} {
		b, st := setup(t)
		add(t, st, fields.SettingsType, fields.SettingsType, storage.Published, map[string]any{"baseURL": "https://a.test", "llmsTxt": true, "language": lang})
		add(t, st, fields.PageType, "a", storage.Published, map[string]any{"title": "A"})
		if _, err := b.Build(context.Background()); err != nil {
			t.Fatal(err)
		}
		if llms := read(t, b, "llms.txt"); !strings.Contains(llms, want) {
			t.Errorf("language %q: want %q in\n%s", lang, want, llms)
		}
	}
}
