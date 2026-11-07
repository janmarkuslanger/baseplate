package auth

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/janmarkuslanger/baseplate/storage"
)

// requestWith runs a GET through Require with the session cookie and
// reports the response and whether the protected handler ran.
func requestWith(m *Manager, token string) (*httptest.ResponseRecorder, bool) {
	reached := false
	h := m.Require("/admin/login", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	}))
	req := httptest.NewRequest("GET", "/admin/", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: token})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, reached
}

func TestRequireFailsClosedWhenSessionStoreFails(t *testing.T) {
	m := setup(t)
	var logs bytes.Buffer
	m.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	ses, err := m.Login(context.Background(), "ip", "a@b.de", "pw")
	if err != nil {
		t.Fatal(err)
	}
	db := m.Store.(storage.Tables).DB()
	if _, err := db.Exec(`DROP TABLE sessions`); err != nil {
		t.Fatal(err)
	}

	rec, reached := requestWith(m, ses.Token)

	if reached {
		t.Fatal("protected handler ran although the session could not be checked")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status %d, want 500 (not a redirect to the login)", rec.Code)
	}
	if !strings.Contains(logs.String(), "sessions") {
		t.Errorf("store error not logged: %q", logs.String())
	}
}

func TestRequireFailsClosedWhenUserStoreFails(t *testing.T) {
	m := setup(t)
	m.Logger = slog.New(slog.NewTextHandler(new(bytes.Buffer), nil))
	ses, err := m.Login(context.Background(), "ip", "a@b.de", "pw")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Store.(storage.Tables).DB().Exec(`DROP TABLE users`); err != nil {
		t.Fatal(err)
	}

	rec, reached := requestWith(m, ses.Token)

	if reached || rec.Code != http.StatusInternalServerError {
		t.Errorf("reached=%v status=%d, want the handler skipped and 500", reached, rec.Code)
	}
}

func TestRequireRedirectsWithoutValidSession(t *testing.T) {
	m := setup(t)
	ses, err := m.Login(context.Background(), "ip", "a@b.de", "pw")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Store.(storage.Tables).DB().Exec(`DELETE FROM users`); err != nil {
		t.Fatal(err)
	}

	for name, token := range map[string]string{"unknown token": "nope", "user deleted": ses.Token} {
		rec, reached := requestWith(m, token)
		if reached || rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/login" {
			t.Errorf("%s: reached=%v status=%d location=%q, want a redirect to the login", name, reached, rec.Code, rec.Header().Get("Location"))
		}
	}
}
