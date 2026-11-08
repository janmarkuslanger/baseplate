package tests

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/janmarkuslanger/baseplate"
	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/storage"
)

// newsV1 has "teaser"; newsV2 renames it to "summary" and migrates the data.
type newsV1 struct{}

func (newsV1) Name() string { return "news" }
func (newsV1) Register(r *plugin.Registry) error {
	return r.ContentType(content.Type{Name: "news", Fields: []content.Field{{Name: "teaser", Kind: fields.Text}}})
}

type newsV2 struct{ runs *int }

func (newsV2) Name() string { return "news" }
func (p newsV2) Register(r *plugin.Registry) error {
	r.DataMigration(plugin.DataMigration{Name: "news: teaser → summary", Run: func(ctx context.Context, st storage.Store) error {
		*p.runs++
		// The old column is no longer in the schema, so it is read via SQL.
		_, err := st.DB().ExecContext(ctx, `UPDATE `+storage.EntryTable("news")+` SET `+storage.Column("summary")+` = teaser WHERE summary IS NULL`)
		return err
	}})
	return r.ContentType(content.Type{Name: "news", Fields: []content.Field{{Name: "summary", Kind: fields.Text}}})
}

func TestDataMigration(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cfg := baseplate.Config{DataDir: filepath.Join(dir, "data"), DistDir: filepath.Join(dir, "dist"), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	v1 := baseplate.New(cfg).Use(newsV1{})
	if err := v1.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	v1.Registry.Store.SaveDraft(ctx, &storage.Entry{Type: "news", Data: map[string]any{"teaser": "Kurz"}})
	v1.Close()

	runs := 0
	for i := 0; i < 2; i++ { // second start must not run the migration again
		v2 := baseplate.New(cfg).Use(newsV2{runs: &runs})
		if err := v2.Setup(ctx); err != nil {
			t.Fatal(err)
		}
		list, _, _ := v2.Registry.Store.ListEntries(ctx, storage.ListQuery{Type: "news", Version: storage.Draft})
		if len(list) != 1 || list[0].Data["summary"] != "Kurz" {
			t.Fatalf("start %d: %v", i, list)
		}
		v2.Close()
	}
	if runs != 1 {
		t.Errorf("migration ran %d times, want 1", runs)
	}
}
