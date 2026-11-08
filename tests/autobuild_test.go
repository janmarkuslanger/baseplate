package tests

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildOnPublish(t *testing.T) {
	t.Setenv("BASEPLATE_BUILD_ON_PUBLISH", "1")
	dist, c := newTestApp(t)
	c.login()
	save := func(typ, status string, data map[string]any) {
		b, _ := json.Marshal(data)
		form := url.Values{"csrf": {c.csrf}, "data": {string(b)}}
		if status == "published" {
			form.Set("publish", "1")
		}
		if res, _ := c.do("POST", "/admin/c/"+typ+"/save", form); res.StatusCode != 303 {
			t.Fatalf("save %s: %d", typ, res.StatusCode)
		}
	}
	wait := func(want string) {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if b, err := os.ReadFile(filepath.Join(dist, "index.html")); err == nil && strings.Contains(string(b), want) {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("site was not rebuilt with %q", want)
	}
	save("settings", "published", map[string]any{"siteName": "Acme", "baseURL": "https://acme.test"})
	save("page", "draft", map[string]any{"title": "Entwurf", "slug": "index"})
	if _, err := os.Stat(filepath.Join(dist, "index.html")); err == nil {
		t.Fatal("a draft must not be built")
	}
	save("page", "published", map[string]any{"title": "Erste Fassung", "slug": "neu"})
	b, _ := json.Marshal(map[string]any{"title": "Live", "slug": "index"})
	c.do("POST", "/admin/c/page/save", url.Values{"csrf": {c.csrf}, "publish": {"1"}, "id": {"1"}, "data": {string(b)}})
	wait("<h1>Live</h1>")
}
