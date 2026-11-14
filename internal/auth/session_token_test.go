package auth

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/janmarkuslanger/baseplate/storage"
)

// storedTokens returns the token column of all stored sessions.
func storedTokens(t *testing.T, m *Manager) []string {
	t.Helper()
	rows, err := m.Store.(storage.Tables).DB().QueryContext(context.Background(), `SELECT token FROM sessions`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var tok string
		if err := rows.Scan(&tok); err != nil {
			t.Fatal(err)
		}
		out = append(out, tok)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// The store keeps a hash of the session token, so a leaked database does
// not contain usable session cookies.
func TestSessionTokenIsStoredHashed(t *testing.T) {
	m := setup(t)
	ses, err := m.Login(context.Background(), "ip", "a@b.de", "pw")
	if err != nil {
		t.Fatal(err)
	}
	stored := storedTokens(t, m)
	if len(stored) != 1 {
		t.Fatalf("%d sessions stored, want 1", len(stored))
	}
	if stored[0] == ses.Token {
		t.Fatal("the session cookie value is stored as is")
	}
	if _, reached := requestWith(m, ses.Token); !reached {
		t.Error("the session cookie is not accepted")
	}
	if _, reached := requestWith(m, stored[0]); reached {
		t.Error("the stored value is accepted as a session cookie")
	}
	if err := m.Logout(httptest.NewRecorder(), logoutRequest(ses.Token)); err != nil {
		t.Fatal(err)
	}
	if n := len(storedTokens(t, m)); n != 0 {
		t.Errorf("%d sessions stored after logout, want 0", n)
	}
}
