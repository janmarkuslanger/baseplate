package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/janmarkuslanger/baseplate/storage"
	"github.com/janmarkuslanger/baseplate/storage/sqlite"
)

func TestPasswordHash(t *testing.T) {
	h := HashPassword("geheim")
	if !CheckPassword(h, "geheim") {
		t.Fatal("correct password rejected")
	}
	if CheckPassword(h, "falsch") || CheckPassword("garbage", "geheim") {
		t.Fatal("wrong password accepted")
	}
	if HashPassword("geheim") == h {
		t.Fatal("hash must be salted")
	}
}

func setup(t *testing.T) *Manager {
	t.Helper()
	s, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	s.Migrate(context.Background(), storage.CoreMigrations)
	s.CreateUser(context.Background(), &storage.User{Email: "a@b.de", PasswordHash: HashPassword("pw")})
	return &Manager{Store: s}
}

func TestLoginAndRequire(t *testing.T) {
	m := setup(t)
	ctx := context.Background()
	if _, err := m.Login(ctx, "ip", "a@b.de", "nope"); !errors.Is(err, ErrInvalidLogin) {
		t.Fatalf("want ErrInvalidLogin, got %v", err)
	}
	if _, err := m.Login(ctx, "ip", "x@b.de", "pw"); !errors.Is(err, ErrInvalidLogin) {
		t.Fatalf("unknown user: want ErrInvalidLogin, got %v", err)
	}
	ses, err := m.Login(ctx, "ip", "A@B.de", "pw")
	if err != nil {
		t.Fatal(err)
	}

	h := m.Require("/admin/login", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if FromContext(r.Context()).User.Email != "a@b.de" {
			t.Error("user not in context")
		}
		w.WriteHeader(204)
	}))
	do := func(method, csrf string, withCookie bool) int {
		body := url.Values{"csrf": {csrf}}.Encode()
		r := httptest.NewRequest(method, "/admin/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if withCookie {
			r.AddCookie(&http.Cookie{Name: CookieName, Value: ses.Token})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if c := do("GET", "", false); c != http.StatusSeeOther {
		t.Errorf("anonymous GET: %d", c)
	}
	if c := do("GET", "", true); c != 204 {
		t.Errorf("authed GET: %d", c)
	}
	if c := do("POST", "wrong", true); c != http.StatusForbidden {
		t.Errorf("POST bad csrf: %d", c)
	}
	if c := do("POST", ses.CSRF, true); c != 204 {
		t.Errorf("POST good csrf: %d", c)
	}
}

func TestLoginRateLimit(t *testing.T) {
	m := setup(t)
	ctx := context.Background()
	for i := 0; i < limitMax; i++ {
		m.Login(ctx, "ip", "a@b.de", "nope")
	}
	if _, err := m.Login(ctx, "ip", "a@b.de", "pw"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("want ErrRateLimited, got %v", err)
	}
	if _, err := m.Login(ctx, "other-ip", "a@b.de", "pw"); err != nil {
		t.Fatalf("other key must not be limited: %v", err)
	}
}
