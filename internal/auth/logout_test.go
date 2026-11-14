package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/janmarkuslanger/baseplate/storage"
)

// logoutRequest returns a logout request carrying the session cookie.
func logoutRequest(token string) *http.Request {
	req := httptest.NewRequest("POST", "/admin/logout", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: token})
	return req
}

func TestLogoutDeletesSessionAndCookie(t *testing.T) {
	m := setup(t)
	m.SecureCookies = true
	ctx := context.Background()
	ses, err := m.Login(ctx, "ip", "a@b.de", "pw")
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	if err := m.Logout(rec, logoutRequest(ses.Token)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Store.GetSession(ctx, ses.Token); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("session still stored after logout: %v", err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want the deleted session cookie", len(cookies))
	}
	c := cookies[0]
	if c.Name != CookieName || c.MaxAge >= 0 {
		t.Errorf("cookie %s max-age %d, want %s deleted", c.Name, c.MaxAge, CookieName)
	}
	// The deletion must carry the attributes the session cookie was set with.
	if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
		t.Errorf("deletion cookie secure=%v httpOnly=%v sameSite=%v path=%q, want the session cookie's attributes", c.Secure, c.HttpOnly, c.SameSite, c.Path)
	}
}

func TestLogoutKeepsCookieWhenDeleteFails(t *testing.T) {
	m := setup(t)
	ses, err := m.Login(context.Background(), "ip", "a@b.de", "pw")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Store.(storage.Tables).DB().Exec(`DROP TABLE sessions`); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	if err := m.Logout(rec, logoutRequest(ses.Token)); err == nil {
		t.Error("Logout reported success although the session was not deleted")
	}
	if got := rec.Result().Cookies(); len(got) != 0 {
		t.Errorf("cookie cleared although the session is still valid: %v", got)
	}
}
