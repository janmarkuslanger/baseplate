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
	err := s.Migrate(context.Background(), []storage.Migration{{Name: "x", SQL: map[storage.Dialect]string{"postgres": "SELECT 1"}}})
	if err == nil {
		t.Fatal("expected error for missing dialect")
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
