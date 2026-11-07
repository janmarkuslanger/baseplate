package tests

import (
	"encoding/json"
	stdhtml "html"
	"regexp"
	"strings"
	"testing"

	"github.com/janmarkuslanger/baseplate/plugins/navigation"
)

// The admin edits links like other entries: the link editor shows its
// fields and the navigation editor offers saved links to pick.
func TestNavigationAdmin(t *testing.T) {
	_, c := newTestApp(t, navigation.New())
	c.login()
	if res, body := c.save(navigation.LinkType, "published", map[string]any{"label": "Kontakt", "url": "/kontakt/"}); res.StatusCode >= 400 {
		t.Fatalf("save link: %d %s", res.StatusCode, body)
	}

	_, html := c.do("GET", "/admin/c/"+navigation.LinkType+"/new", nil)
	for _, label := range []string{"Beschriftung", "URL", "Seite", "Unterpunkte"} {
		if !strings.Contains(html, label) {
			t.Errorf("link editor lacks field %q", label)
		}
	}

	_, html = c.do("GET", "/admin/c/"+navigation.TypeName+"/new", nil)
	m := regexp.MustCompile(`data-refs="([^"]*)"`).FindStringSubmatch(html)
	if m == nil {
		t.Fatal("no data-refs on the navigation editor")
	}
	var refs map[string][]struct{ Value, Label string }
	if err := json.Unmarshal([]byte(stdhtml.UnescapeString(m[1])), &refs); err != nil {
		t.Fatal(err)
	}
	if got := refs[navigation.LinkType]; len(got) != 1 || got[0].Label != "Kontakt" {
		t.Errorf("navigation editor offers links %+v, want the saved link", got)
	}
}
