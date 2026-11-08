package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/storage"
)

func siteType(singleton bool) content.Type {
	return content.Type{Name: "site", Singleton: singleton, Fields: []content.Field{{Name: "title", Kind: fields.Text}}}
}

func TestSingletonTableRefusesSecondRow(t *testing.T) {
	ctx := context.Background()
	s := startWith(t, filepath.Join(t.TempDir(), "db.sqlite"), siteType(true))
	mustCreate(t, s, &storage.Entry{Type: "site", Slug: "site", Data: map[string]any{"title": "a"}})

	if err := s.SaveDraft(ctx, &storage.Entry{Type: "site", Slug: "site", Data: map[string]any{"title": "b"}}); err == nil {
		t.Fatal("a second row in a singleton table was stored")
	}
	if _, total, _ := s.ListEntries(ctx, storage.ListQuery{Type: "site", Version: storage.Draft}); total != 1 {
		t.Errorf("singleton has %d entries, want 1", total)
	}
}

func TestSingletonWithSeveralEntriesFailsStart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db.sqlite")
	s := startWith(t, path, siteType(false))
	a := mustCreate(t, s, &storage.Entry{Type: "site", Slug: "site", Data: map[string]any{}})
	b := mustCreate(t, s, &storage.Entry{Type: "site", Slug: "site", Data: map[string]any{}})
	s.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	err = s2.UseSchema(ctx, newRegistry(t, s2, siteType(true)).Schema)
	if err == nil {
		t.Fatal("start with two entries of a singleton type succeeded")
	}
	for _, id := range []int64{a.ID, b.ID} {
		if !strings.Contains(err.Error(), "site") || !strings.Contains(err.Error(), strconv.FormatInt(id, 10)) {
			t.Errorf("error %q does not name type site and entry %d", err, id)
		}
	}
}

func TestFormerSingletonAcceptsMoreRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	s := startWith(t, path, siteType(true))
	mustCreate(t, s, &storage.Entry{Type: "site", Slug: "a", Data: map[string]any{}})
	s.Close()

	s2 := startWith(t, path, siteType(false))
	mustCreate(t, s2, &storage.Entry{Type: "site", Slug: "b", Data: map[string]any{}})
}

func TestAtomicRollsBackOnError(t *testing.T) {
	ctx := context.Background()
	s := startWith(t, filepath.Join(t.TempDir(), "db.sqlite"), siteType(false))
	boom := errors.New("boom")

	err := s.Atomic(ctx, func(st storage.Entries) error {
		if err := st.SaveDraft(ctx, &storage.Entry{Type: "site", Slug: "x", Data: map[string]any{}}); err != nil {
			return err
		}
		if _, total, err := st.ListEntries(ctx, storage.ListQuery{Type: "site", Version: storage.Draft}); err != nil || total != 1 {
			t.Errorf("the view does not see its own write: %d %v", total, err)
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Atomic returned %v, want fn's error", err)
	}
	if _, total, _ := s.ListEntries(ctx, storage.ListQuery{Type: "site", Version: storage.Draft}); total != 0 {
		t.Errorf("%d entries survived the failed Atomic, want 0", total)
	}
}
