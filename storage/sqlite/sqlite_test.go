package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/janmarkuslanger/baseplate/storage"
)

func openTest(t *testing.T) *storage.SQLStore {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(context.Background(), storage.CoreMigrations); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMigrateIsIdempotent(t *testing.T) {
	s := openTest(t)
	if err := s.Migrate(context.Background(), storage.CoreMigrations); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestMigrationMissingDialect(t *testing.T) {
	s := openTest(t)
	err := s.Migrate(context.Background(), []storage.Migration{{Name: "x", SQL: map[storage.Dialect]string{storage.MySQL: "SELECT 1"}}})
	if err == nil {
		t.Fatal("expected error for missing dialect")
	}
}

func TestEntryCRUD(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)

	e := &storage.Entry{Type: "page", Slug: "home", Data: map[string]any{"title": "Home"}}
	if err := s.CreateEntry(ctx, e); err != nil {
		t.Fatal(err)
	}
	if e.ID == 0 || e.Status != storage.StatusDraft {
		t.Fatalf("unexpected entry after create: %+v", e)
	}

	got, err := s.GetEntryBySlug(ctx, "page", "home")
	if err != nil {
		t.Fatal(err)
	}
	if got.Data["title"] != "Home" {
		t.Fatalf("data not round-tripped: %v", got.Data)
	}

	got.Status = storage.StatusPublished
	got.Data["title"] = "Start"
	if err := s.UpdateEntry(ctx, got); err != nil {
		t.Fatal(err)
	}
	s.CreateEntry(ctx, &storage.Entry{Type: "page", Slug: "about", Data: map[string]any{"title": "About us"}})
	s.CreateEntry(ctx, &storage.Entry{Type: "form", Slug: "contact"})

	list, total, err := s.ListEntries(ctx, storage.ListQuery{Type: "page"})
	if err != nil || total != 2 || len(list) != 2 {
		t.Fatalf("list pages: total=%d len=%d err=%v", total, len(list), err)
	}
	list, total, _ = s.ListEntries(ctx, storage.ListQuery{Type: "page", Status: storage.StatusPublished})
	if total != 1 || list[0].Data["title"] != "Start" {
		t.Fatalf("status filter: %+v", list)
	}
	_, total, _ = s.ListEntries(ctx, storage.ListQuery{Search: "About"})
	if total != 1 {
		t.Fatalf("search: total=%d", total)
	}
	list, total, _ = s.ListEntries(ctx, storage.ListQuery{Type: "page", Limit: 1})
	if total != 2 || len(list) != 1 {
		t.Fatalf("limit: total=%d len=%d", total, len(list))
	}

	if err := s.DeleteEntry(ctx, got.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetEntry(ctx, got.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err := s.DeleteEntry(ctx, got.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("delete twice: want ErrNotFound, got %v", err)
	}
}

func TestUsersAndSessions(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)

	u := &storage.User{Email: "Admin@Example.com", PasswordHash: "x"}
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateUser(ctx, &storage.User{Email: "admin@example.com", PasswordHash: "y"}); err == nil {
		t.Fatal("duplicate email (case-insensitive) must fail")
	}
	got, err := s.GetUserByEmail(ctx, "ADMIN@example.com")
	if err != nil || got.ID != u.ID {
		t.Fatalf("get by email: %v %v", got, err)
	}
	if n, _ := s.CountUsers(ctx); n != 1 {
		t.Fatalf("count = %d", n)
	}

	ses := &storage.Session{Token: "t1", UserID: u.ID, CSRF: "c", ExpiresAt: time.Now().Add(time.Hour)}
	if err := s.CreateSession(ctx, ses); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSession(ctx, "t1"); err != nil {
		t.Fatal(err)
	}
	s.CreateSession(ctx, &storage.Session{Token: "old", UserID: u.ID, CSRF: "c", ExpiresAt: time.Now().Add(-time.Minute)})
	if _, err := s.GetSession(ctx, "old"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expired session: want ErrNotFound, got %v", err)
	}
}
