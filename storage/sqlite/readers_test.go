package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/janmarkuslanger/baseplate/storage"
)

// Reads of a file database do not queue behind a write transaction of the
// same process: entries, referrers, users and sessions are read while
// Atomic holds the write connection.
func TestReadsDoNotWaitForWriter(t *testing.T) {
	ctx := context.Background()
	s := storeAt(t, filepath.Join(t.TempDir(), "app.db"))
	e := &storage.Entry{Type: "post", Slug: "a", Data: revision(1)}
	if err := s.SaveDraft(ctx, e); err != nil {
		t.Fatal(err)
	}
	u := &storage.User{Email: "a@example.com", PasswordHash: "x"}
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(ctx, &storage.Session{Token: "tok", UserID: u.ID, CSRF: "c", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}

	inTx, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- s.Atomic(ctx, func(st storage.Entries) error {
			e.Data = revision(2)
			if err := st.SaveDraft(ctx, e); err != nil {
				return err
			}
			close(inTx)
			<-release
			return nil
		})
	}()
	<-inTx

	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	got, err := s.GetEntry(rctx, "post", e.ID, "", storage.Draft)
	if err != nil {
		t.Fatalf("GetEntry during a write transaction: %v", err)
	}
	if got.Data["title"] != "1" {
		t.Errorf("read saw the uncommitted write: title %v", got.Data["title"])
	}
	if _, _, err := s.ListEntries(rctx, storage.ListQuery{Type: "post", Version: storage.Draft}); err != nil {
		t.Errorf("ListEntries during a write transaction: %v", err)
	}
	if _, err := s.Referrers(rctx, "post", e.ID); err != nil {
		t.Errorf("Referrers during a write transaction: %v", err)
	}
	if _, err := s.GetUser(rctx, u.ID); err != nil {
		t.Errorf("GetUser during a write transaction: %v", err)
	}
	if _, err := s.GetSession(rctx, "tok"); err != nil {
		t.Errorf("GetSession during a write transaction: %v", err)
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// A read after the commit sees it.
	got, err = s.GetEntry(ctx, "post", e.ID, "", storage.Draft)
	if err != nil {
		t.Fatal(err)
	}
	if got.Data["title"] != "2" {
		t.Errorf("read after commit: title %v, want 2", got.Data["title"])
	}
}

// The read pool refuses writes: they must go through the write connection
// and its locking.
func TestReadPoolIsQueryOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	s := storeAt(t, path)
	ctx := context.Background()
	rdb, err := sql.Open("sqlite", readerDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer rdb.Close()
	if _, err := rdb.ExecContext(ctx, `INSERT INTO users (email, password_hash, created_at) VALUES ('x@example.com', 'x', 0)`); err == nil {
		t.Fatal("insert on a read pool connection succeeded")
	}
	if n, err := s.CountUsers(ctx); err != nil || n != 0 {
		t.Fatalf("users = %d (%v), want 0", n, err)
	}
}
