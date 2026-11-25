package tests

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/janmarkuslanger/baseplate/plugins/access"
)

func basicGet(t *testing.T, c *client, path, user, pw string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", c.srv.URL+path, nil)
	if user != "" || pw != "" {
		req.SetBasicAuth(user, pw)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res
}

func TestAccessProtectsSiteAndMedia(t *testing.T) {
	dist, c := newTestApp(t, access.New())
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("home"), 0o644); err != nil {
		t.Fatal(err)
	}
	status := func(path, user, pw string) int { return basicGet(t, c, path, user, pw).StatusCode }

	if got := status("/", "", ""); got != 200 {
		t.Fatalf("open site: %d", got)
	}

	c.login()
	save := func(enabled, user, pw string) int {
		form := url.Values{"csrf": {c.csrf}, "username": {user}, "password": {pw}}
		if enabled != "" {
			form.Set("enabled", enabled)
		}
		res, _ := c.do("POST", "/admin/p/access", form)
		return res.StatusCode
	}

	for name, args := range map[string][3]string{
		"enable without password": {"1", "gast", ""},
		"short password":          {"1", "gast", "kurz"},
		"colon in username":       {"1", "ga:st", "ein-langes-passwort"},
		"enable without username": {"1", "", "ein-langes-passwort"},
	} {
		if got := save(args[0], args[1], args[2]); got != http.StatusUnprocessableEntity {
			t.Errorf("%s: status %d, want 422", name, got)
		}
	}
	if got := status("/", "", ""); got != 200 {
		t.Fatalf("rejected form must not enable the protection: %d", got)
	}

	if got := save("1", "gast", "ein-langes-passwort"); got != http.StatusSeeOther {
		t.Fatalf("save: %d", got)
	}
	res := basicGet(t, c, "/", "", "")
	if res.StatusCode != 401 || res.Header.Get("WWW-Authenticate") == "" {
		t.Fatalf("no credentials: %d %q", res.StatusCode, res.Header.Get("WWW-Authenticate"))
	}
	for _, path := range []string{"/", "/index.html", "/media/missing.png"} {
		if got := status(path, "gast", "falsch-falsch-falsch"); got != 401 {
			t.Errorf("%s wrong password: %d", path, got)
		}
		if got := status(path, "andere", "ein-langes-passwort"); got != 401 {
			t.Errorf("%s wrong user: %d", path, got)
		}
	}
	if got := status("/", "gast", "ein-langes-passwort"); got != 200 {
		t.Fatalf("right credentials: %d", got)
	}
	if got := status("/", "gast", "ein-langes-passwort"); got != 200 { // cached path
		t.Fatalf("right credentials again: %d", got)
	}
	if got := status("/media/missing.png", "gast", "ein-langes-passwort"); got != 404 {
		t.Fatalf("media behind the login: %d, want 404 (file missing)", got)
	}
	if got := status("/admin/login", "", ""); got != 200 {
		t.Fatalf("admin login must stay open: %d", got)
	}

	// A new password replaces the old one at once, a blank one keeps it.
	if got := save("1", "gast", "ein-neues-passwort"); got != http.StatusSeeOther {
		t.Fatalf("change password: %d", got)
	}
	if got := status("/", "gast", "ein-langes-passwort"); got != 401 {
		t.Fatalf("old password still works: %d", got)
	}
	if got := save("1", "gast", ""); got != http.StatusSeeOther {
		t.Fatalf("keep password: %d", got)
	}
	if got := status("/", "gast", "ein-neues-passwort"); got != 200 {
		t.Fatalf("kept password: %d", got)
	}

	if got := save("", "gast", ""); got != http.StatusSeeOther {
		t.Fatalf("disable: %d", got)
	}
	if got := status("/", "", ""); got != 200 {
		t.Fatalf("disabled protection: %d", got)
	}
}

func TestAccessLimitsPasswordGuesses(t *testing.T) {
	p := access.New()
	p.Limit = 3
	_, c := newTestApp(t, p)
	c.login()
	form := url.Values{"csrf": {c.csrf}, "enabled": {"1"}, "username": {"gast"}, "password": {"ein-langes-passwort"}}
	if res, _ := c.do("POST", "/admin/p/access", form); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("save: %d", res.StatusCode)
	}
	var last int
	for range 5 {
		last = basicGet(t, c, "/", "gast", "falsch-falsch-falsch").StatusCode
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("after 5 wrong guesses: %d, want 429", last)
	}
}
