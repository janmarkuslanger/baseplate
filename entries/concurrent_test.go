package entries_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/entries"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/storage"
	"github.com/janmarkuslanger/baseplate/storage/sqlite"
)

// serviceAt opens the database file like one app process does.
func serviceAt(t *testing.T, path string) *plugin.Registry {
	t.Helper()
	ctx := context.Background()
	st, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx, storage.CoreMigrations); err != nil {
		t.Fatal(err)
	}
	reg := plugin.NewRegistry(plugin.Services{Store: st})
	if err := reg.Use(fields.Core{}); err != nil {
		t.Fatal(err)
	}
	if err := st.UseSchema(ctx, reg.Schema); err != nil {
		t.Fatal(err)
	}
	return reg
}

// saveConcurrently runs n saves at once, alternating between the given
// services (each on its own connection, like separate processes).
func saveConcurrently(regs []*plugin.Registry, n int, in entries.Save) []error {
	errs := make([]error, n)
	var start, done sync.WaitGroup
	start.Add(1)
	for i := range n {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			_, errs[i] = regs[i%len(regs)].Entries.Save(context.Background(), in)
		}()
	}
	start.Done()
	done.Wait()
	return errs
}

func TestConcurrentSavesKeepSlugsUnique(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	regs := []*plugin.Registry{serviceAt(t, path), serviceAt(t, path)}

	errs := saveConcurrently(regs, 8, entries.Save{Type: "page", Data: page("team", 0)})

	ok := 0
	for _, err := range errs {
		var verrs content.Errors
		switch {
		case err == nil:
			ok++
		case errors.As(err, &verrs) && verrs["slug"] != nil:
		default:
			t.Errorf("want success or the slug error, got %v", err)
		}
	}
	if ok != 1 {
		t.Errorf("%d of 8 concurrent saves of slug %q succeeded, want 1", ok, "team")
	}
	list, _, err := regs[0].Store.ListEntries(context.Background(), storage.ListQuery{Type: "page", Version: storage.Draft})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Errorf("stored %d pages with slug team, want 1", len(list))
	}
}

func TestConcurrentFirstSavesOfSingletonStoreOneEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	regs := []*plugin.Registry{serviceAt(t, path), serviceAt(t, path)}

	errs := saveConcurrently(regs, 8, entries.Save{Type: "settings", Data: map[string]any{"siteName": "S", "baseURL": "https://a.test"}})

	for _, err := range errs {
		if err != nil {
			t.Errorf("every save of a singleton updates its one entry, got %v", err)
		}
	}
	_, total, err := regs[0].Store.ListEntries(context.Background(), storage.ListQuery{Type: "settings", Version: storage.Draft})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("singleton has %d entries, want 1", total)
	}
}
