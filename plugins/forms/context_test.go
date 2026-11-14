package forms

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/janmarkuslanger/baseplate/build"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/storage"
)

// A form block loads its form with the build's context: a build cancelled
// while rendering fails at the lookup instead of finishing.
func TestFormBlockUsesBuildContext(t *testing.T) {
	reg, _, _, id := setup(t)
	publish(t, reg.Store, &storage.Entry{Type: fields.SettingsType, Slug: fields.SettingsType,
		Data: map[string]any{"baseURL": "https://acme.test"}})
	publish(t, reg.Store, &storage.Entry{Type: fields.PageType, Slug: "kontakt", Data: map[string]any{
		"title": "Kontakt", "body": []any{map[string]any{"_type": BlockName, "form": float64(id)}},
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := reg.Funcs(plugin.TemplateFuncs{"cancelBuild": func() string { cancel(); return "" }}); err != nil {
		t.Fatal(err)
	}
	b := &build.Builder{Reg: reg, DistDir: filepath.Join(t.TempDir(), "dist"), Theme: fstest.MapFS{
		"templates/page.html": {Data: []byte(`{{define "page"}}{{cancelBuild}}{{blocks .Data.body .}}{{end}}`)},
	}}
	if _, err := b.Build(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Build = %v, want the form lookup to fail with %v", err, context.Canceled)
	}
}
