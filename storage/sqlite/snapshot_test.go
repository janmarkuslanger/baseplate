package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/storage"
)

// storeAt opens the database file with the test schema, like one app
// process does.
func storeAt(t *testing.T, path string) *storage.SQLStore {
	t.Helper()
	ctx := context.Background()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(ctx, storage.CoreMigrations); err != nil {
		t.Fatal(err)
	}
	reg := plugin.NewRegistry(plugin.Services{Store: s})
	if err := reg.Use(fields.Core{}, fields.CoreBlocks{}, testTypes{}); err != nil {
		t.Fatal(err)
	}
	if err := s.UseSchema(ctx, reg.Schema); err != nil {
		t.Fatal(err)
	}
	return s
}

func revision(n int) map[string]any {
	v := fmt.Sprint(n)
	return map[string]any{"title": v, "links": []any{map[string]any{"label": v}}}
}

// A read spans several statements (row, components, relations, publication
// state; count and page). Writes committed by another process in between
// must not show up in only part of the result.
func TestReadsSeeOneSnapshot(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db.sqlite")
	writer, reader := storeAt(t, path), storeAt(t, path)

	post := &storage.Entry{Type: "post", Data: revision(0)}
	if err := writer.SaveDraft(ctx, post); err != nil {
		t.Fatal(err)
	}
	if err := writer.Publish(ctx, "post", post.ID, ""); err != nil {
		t.Fatal(err)
	}

	const rounds = 300
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		defer close(done)
		for i := 1; i <= rounds; i++ {
			post.Data = revision(i)
			if err := writer.SaveDraft(ctx, post); err != nil {
				t.Error(err)
				return
			}
			if err := writer.Publish(ctx, "post", post.ID, ""); err != nil {
				t.Error(err)
				return
			}
			if err := writer.SaveDraft(ctx, &storage.Entry{Type: "post", Data: revision(i)}); err != nil {
				t.Error(err)
				return
			}
		}
	})
	wg.Go(func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			e, err := reader.GetEntry(ctx, "post", post.ID, "", storage.Published)
			if err != nil {
				t.Error(err)
				return
			}
			links := e.Data["links"].([]any)
			if len(links) != 1 || links[0].(map[string]any)["label"] != e.Data["title"] {
				t.Errorf("torn entry: title %v, links %v", e.Data["title"], links)
				return
			}
			list, total, err := reader.ListEntries(ctx, storage.ListQuery{Type: "post", Version: storage.Draft})
			if err != nil {
				t.Error(err)
				return
			}
			if total != len(list) {
				t.Errorf("torn list: total %d, %d entries", total, len(list))
				return
			}
		}
	})
	wg.Wait()
}
