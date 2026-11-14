package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/janmarkuslanger/baseplate/storage"
)

// A failed data migration leaves neither its writes nor a record, so the
// next start runs it again; a successful one runs once.
func TestRunOnceRollsBackFailedRun(t *testing.T) {
	s := openTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	boom := errors.New("boom")

	runs := 0
	fn := func(ctx context.Context, st storage.Store) error {
		runs++
		if err := st.CreateUser(ctx, &storage.User{Email: fmt.Sprintf("u%d@a.test", runs), PasswordHash: "x"}); err != nil {
			return err
		}
		if _, err := st.DB().ExecContext(ctx, `UPDATE users SET name = 'migrated'`); err != nil {
			return err
		}
		if runs == 1 {
			return boom
		}
		return nil
	}
	if err := s.RunOnce(ctx, "m", fn); !errors.Is(err, boom) {
		t.Fatalf("failing run: %v", err)
	}
	if n, err := s.CountUsers(ctx); err != nil || n != 0 {
		t.Fatalf("writes of the failed run kept: %d users, %v", n, err)
	}
	for range 2 {
		if err := s.RunOnce(ctx, "m", fn); err != nil {
			t.Fatal(err)
		}
	}
	if runs != 2 {
		t.Errorf("runs = %d, want 2 (failed + one successful)", runs)
	}
	u, err := s.GetUserByEmail(ctx, "u2@a.test")
	if err != nil || u.Name != "migrated" {
		t.Errorf("writes of the successful run: %+v %v", u, err)
	}
}

// Processes starting at the same time run a data migration once.
func TestRunOnceConcurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	stores := make([]*storage.SQLStore, 4)
	for i := range stores {
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		stores[i] = s
	}
	ctx := context.Background()
	if err := stores[0].Migrate(ctx, storage.CoreMigrations); err != nil {
		t.Fatal(err)
	}
	for round := range 10 {
		name := fmt.Sprintf("m%d", round)
		var wg sync.WaitGroup
		errs := make(chan error, len(stores))
		for _, s := range stores {
			wg.Go(func() {
				errs <- s.RunOnce(ctx, name, func(ctx context.Context, st storage.Store) error {
					return st.CreateUser(ctx, &storage.User{Email: name + "@a.test", PasswordHash: "x"})
				})
			})
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("round %d: %v", round, err)
			}
		}
	}
	if n, err := stores[0].CountUsers(ctx); err != nil || n != 10 {
		t.Errorf("users = %d, want 10 (one per migration): %v", n, err)
	}
}

// A view handed to a data migration must not close the shared database.
func TestRunOnceViewCannotClose(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	err := s.RunOnce(ctx, "close", func(ctx context.Context, st storage.Store) error { return st.Close() })
	if err == nil {
		t.Fatal("Close on the view succeeded")
	}
	if _, err := s.CountUsers(ctx); err != nil {
		t.Fatalf("store unusable after Close on a view: %v", err)
	}
}
