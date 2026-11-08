package build

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/storage"
)

func draft(t *testing.T, st storage.Store, typ string, id int64) *storage.Entry {
	t.Helper()
	e, err := st.GetEntry(context.Background(), typ, id, "", storage.Draft)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// A preview shows the draft of one entry among the published versions of
// all others, and writes nothing.
func TestPreview(t *testing.T) {
	b, st := setup(t)
	ctx := context.Background()
	add(t, st, fields.SettingsType, fields.SettingsType, storage.Published, map[string]any{"baseURL": "https://a.test"})
	about := add(t, st, fields.PageType, "ueber-uns", storage.Published, map[string]any{"title": "Über uns"})
	add(t, st, fields.PageType, "team", storage.Published, map[string]any{"title": "Team", "parent": about})
	add(t, st, fields.PageType, "geheim", storage.Draft, map[string]any{"title": "Geheim"})

	// A published page with draft changes.
	e := draft(t, st, fields.PageType, about)
	e.Data["title"] = "Über uns (neu)"
	if err := st.SaveDraft(ctx, e); err != nil {
		t.Fatal(err)
	}
	html, err := b.Preview(ctx, draft(t, st, fields.PageType, about))
	if err != nil {
		t.Fatal(err)
	}
	got := string(html)
	for _, want := range []string{"<h1>Über uns (neu)</h1>", `href="/ueber-uns/team/"`, "Über uns (neu)</a>"} {
		if !strings.Contains(got, want) {
			t.Errorf("preview misses %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Geheim") {
		t.Error("another entry's draft shows in the preview")
	}
	if _, err := os.Stat(b.DistDir); !os.IsNotExist(err) {
		t.Errorf("preview wrote to the dist dir: %v", err)
	}

	// A draft that was never published.
	neu := add(t, st, fields.PageType, "neu", storage.Draft, map[string]any{"title": "Neu", "parent": about})
	html, err = b.Preview(ctx, draft(t, st, fields.PageType, neu))
	if err != nil || !strings.Contains(string(html), "<h1>Neu</h1>") || !strings.Contains(string(html), `href="/ueber-uns/neu/"`) {
		t.Errorf("unpublished draft: %v\n%s", err, html)
	}
}

// Template lists (entries) include the previewed draft.
func TestPreviewInEntryLists(t *testing.T) {
	b, st := setup(t)
	ctx := context.Background()
	b.Theme = fstest.MapFS{"templates/page.html": {Data: []byte(`{{define "page"}}{{range entries "page" "sort" "title"}}[{{.Data.title}}]{{end}}{{end}}`)}}
	add(t, st, fields.SettingsType, fields.SettingsType, storage.Published, map[string]any{"baseURL": "https://a.test"})
	add(t, st, fields.PageType, "a", storage.Published, map[string]any{"title": "A"})
	id := add(t, st, fields.PageType, "b", storage.Draft, map[string]any{"title": "B"})
	html, err := b.Preview(ctx, draft(t, st, fields.PageType, id))
	if err != nil || string(html) != "[A][B]" {
		t.Errorf("entries in preview: %q %v", html, err)
	}
}

func TestPreviewErrors(t *testing.T) {
	b, st := setup(t)
	ctx := context.Background()
	id := add(t, st, fields.PageType, "x", storage.Draft, map[string]any{"title": "X"})
	if _, err := b.Preview(ctx, draft(t, st, fields.PageType, id)); !errors.Is(err, ErrBaseURLMissing) {
		t.Errorf("without settings: %v", err)
	}
	add(t, st, fields.SettingsType, fields.SettingsType, storage.Published, map[string]any{"baseURL": "https://a.test"})
	if _, err := b.Preview(ctx, draft(t, st, fields.SettingsType, 1)); !errors.Is(err, ErrNotRoutable) {
		t.Errorf("settings: %v", err)
	}
	// The draft takes a URL another published page has.
	add(t, st, fields.PageType, "y", storage.Published, map[string]any{"title": "Y"})
	e := draft(t, st, fields.PageType, id)
	e.Slug, e.Data["slug"] = "y", "y"
	if _, err := b.Preview(ctx, e); err == nil || !strings.Contains(err.Error(), "/y/") {
		t.Errorf("duplicate URL: %v", err)
	}
}
