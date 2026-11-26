package build

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/render"
	"github.com/janmarkuslanger/baseplate/storage"
)

var ldRe = regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)

// graph returns the nodes of the page's JSON-LD by @type, nil without any.
func graph(t *testing.T, b *Builder, file string) map[string]map[string]any {
	t.Helper()
	html := read(t, b, file)
	m := ldRe.FindStringSubmatch(html)
	if m == nil {
		return nil
	}
	var doc struct {
		Context string           `json:"@context"`
		Graph   []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal([]byte(m[1]), &doc); err != nil {
		t.Fatalf("%s: invalid JSON-LD: %v\n%s", file, err, m[1])
	}
	if doc.Context != "https://schema.org" {
		t.Errorf("%s: @context = %q", file, doc.Context)
	}
	out := map[string]map[string]any{}
	for _, n := range doc.Graph {
		out[n["@type"].(string)] = n
	}
	return out
}

func TestJSONLDHomeAndBreadcrumbs(t *testing.T) {
	b, st := setup(t)
	add(t, st, fields.SettingsType, fields.SettingsType, storage.Published, map[string]any{
		"siteName": "Acme", "baseURL": "https://acme.test", "language": "de",
		"logo": "/media/logo.png", "sameAs": "https://instagram.com/acme\n\n https://linkedin.com/acme ",
	})
	add(t, st, fields.PageType, "index", storage.Published, map[string]any{"title": "Start"})
	about := add(t, st, fields.PageType, "about", storage.Published, map[string]any{"title": "Über uns"})
	add(t, st, fields.PageType, "team", storage.Published, map[string]any{"title": "Team", "parent": about})
	if _, err := b.Build(context.Background()); err != nil {
		t.Fatal(err)
	}

	home := graph(t, b, "index.html")
	org, web := home["Organization"], home["WebSite"]
	if org == nil || web == nil || home["BreadcrumbList"] != nil {
		t.Fatalf("home: want Organization and WebSite only, got %v", home)
	}
	if org["name"] != "Acme" || org["url"] != "https://acme.test/" || org["logo"] != "https://acme.test/media/logo.png" {
		t.Errorf("organization: %v", org)
	}
	if got := org["sameAs"].([]any); len(got) != 2 || got[1] != "https://linkedin.com/acme" {
		t.Errorf("sameAs: %v", got)
	}
	if web["inLanguage"] != "de" || web["publisher"].(map[string]any)["@id"] != org["@id"] {
		t.Errorf("website: %v", web)
	}

	top := graph(t, b, "about/index.html")
	if top["Organization"] != nil || top["WebSite"] != nil {
		t.Errorf("only the home page carries the organization: %v", top)
	}
	var names []string
	for _, it := range graph(t, b, "about/team/index.html")["BreadcrumbList"]["itemListElement"].([]any) {
		it := it.(map[string]any)
		names = append(names, it["name"].(string)+" "+it["item"].(string))
	}
	want := "Acme https://acme.test/,Über uns https://acme.test/about/,Team https://acme.test/about/team/"
	if got := strings.Join(names, ","); got != want {
		t.Errorf("breadcrumbs\n got %s\nwant %s", got, want)
	}
	if n := graph(t, b, "about/index.html")["BreadcrumbList"]; n == nil || len(n["itemListElement"].([]any)) != 2 {
		t.Errorf("a page without parent still has Home > Page: %v", n)
	}
}

func TestJSONLDOrgTypeAndNoSiteName(t *testing.T) {
	b, st := setup(t)
	add(t, st, fields.SettingsType, fields.SettingsType, storage.Published, map[string]any{
		"siteName": "Max Mustermann", "baseURL": "https://m.test", "orgType": "Person",
	})
	add(t, st, fields.PageType, "index", storage.Published, map[string]any{"title": "Start"})
	if _, err := b.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if g := graph(t, b, "index.html"); g["Person"] == nil || g["Organization"] != nil {
		t.Errorf("orgType Person: %v", g)
	}

	b, st = setup(t)
	add(t, st, fields.SettingsType, fields.SettingsType, storage.Published, map[string]any{"baseURL": "https://m.test"})
	add(t, st, fields.PageType, "index", storage.Published, map[string]any{"title": "Start"})
	if _, err := b.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(read(t, b, "index.html"), "ld+json") {
		t.Error("no site name, no structured data")
	}
}

func TestJSONLDHook(t *testing.T) {
	b, st := setup(t)
	add(t, st, fields.SettingsType, fields.SettingsType, storage.Published, map[string]any{
		"siteName": `Acme </script><script>alert(1)</script>`, "baseURL": "https://acme.test",
	})
	add(t, st, fields.PageType, "index", storage.Published, map[string]any{"title": "Start"})
	add(t, st, fields.PageType, "other", storage.Published, map[string]any{"title": "Other"})
	render.JSONLD.Filter(b.Reg, func(_ context.Context, g *render.Graph) (*render.Graph, error) {
		for _, n := range g.Nodes {
			if n["@type"] == "Organization" {
				n["@type"] = "LocalBusiness"
			}
		}
		if g.Page.URL == "/other/" {
			g.Nodes = append(g.Nodes, map[string]any{"@type": "FAQPage"})
		}
		return g, nil
	})
	if _, err := b.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if g := graph(t, b, "index.html"); g["LocalBusiness"] == nil || g["Organization"] != nil {
		t.Errorf("hook changes the organization: %v", g)
	}
	if g := graph(t, b, "other/index.html"); g["FAQPage"] == nil {
		t.Errorf("hook adds a node to any page: %v", g)
	}
	html := read(t, b, "index.html")
	if strings.Contains(html, "</script><script>alert") || !strings.Contains(html, `</script>`) {
		t.Errorf("a site name must not end the script element:\n%s", html)
	}
}

// A failing hook function is logged; the page keeps the graph as it was.
func TestJSONLDHookError(t *testing.T) {
	b, st := setup(t)
	add(t, st, fields.SettingsType, fields.SettingsType, storage.Published, map[string]any{"siteName": "Acme", "baseURL": "https://acme.test"})
	add(t, st, fields.PageType, "index", storage.Published, map[string]any{"title": "Start"})
	render.JSONLD.Filter(b.Reg, func(context.Context, *render.Graph) (*render.Graph, error) {
		return nil, errors.New("boom")
	})
	if _, err := b.Build(context.Background()); err != nil {
		t.Fatalf("a failing hook must not fail the build: %v", err)
	}
	if g := graph(t, b, "index.html"); g["Organization"] == nil {
		t.Errorf("graph before the failing function stays: %v", g)
	}
}
