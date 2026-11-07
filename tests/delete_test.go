package tests

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The admin refuses to delete an entry that is still in use: the editor
// offers no delete button, and a delete request is answered with the
// editor, the reason and the referrers.
func TestAdminDeleteReferencedEntry(t *testing.T) {
	_, c := newTestApp(t)
	c.login()
	id := func(res *http.Response) string {
		t.Helper()
		if res.StatusCode != http.StatusSeeOther {
			t.Fatalf("save: %d", res.StatusCode)
		}
		return strings.Split(strings.TrimPrefix(res.Header.Get("Location"), "/admin/c/page/"), "?")[0]
	}
	res, _ := c.save("page", "draft", map[string]any{"title": "Über uns", "slug": "ueber"})
	parent := id(res)
	res, _ = c.save("page", "draft", map[string]any{"title": "Das Team", "slug": "team", "parent": parent})
	child := id(res)

	_, body := c.do("GET", "/admin/c/page/"+parent, nil)
	if strings.Contains(body, "/delete") || !strings.Contains(body, "an 1 Stelle(n) verwendet") {
		t.Errorf("editor of a referenced entry offers delete or lacks the reason")
	}

	res, body = c.do("POST", "/admin/c/page/"+parent+"/delete", url.Values{"csrf": {c.csrf}})
	if res.StatusCode != http.StatusConflict || !strings.Contains(body, "an 1 Stelle(n) verwendet") || !strings.Contains(body, "Das Team") {
		t.Fatalf("delete referenced: %d", res.StatusCode)
	}
	if res, _ := c.do("GET", "/admin/c/page/"+parent, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("refused delete removed the page: %d", res.StatusCode)
	}

	for _, p := range []string{child, parent} {
		if res, _ := c.do("POST", "/admin/c/page/"+p+"/delete", url.Values{"csrf": {c.csrf}}); res.StatusCode != http.StatusSeeOther {
			t.Fatalf("delete page %s: %d", p, res.StatusCode)
		}
	}
}
