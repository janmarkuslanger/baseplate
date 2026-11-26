package localbusiness

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/janmarkuslanger/baseplate/build"
	"github.com/janmarkuslanger/baseplate/entries"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/i18n"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/storage"
	"github.com/janmarkuslanger/baseplate/storage/sqlite"
)

func setup(t *testing.T) *plugin.Registry {
	t.Helper()
	ctx := context.Background()
	st, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	reg := plugin.NewRegistry(plugin.Services{Store: st, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := reg.Use(fields.Core{}, fields.CoreBlocks{}, New()); err != nil {
		t.Fatal(err)
	}
	if err := reg.Schema.Check(); err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx, append(storage.CoreMigrations, reg.Migrations()...)); err != nil {
		t.Fatal(err)
	}
	if err := st.UseSchema(ctx, reg.Schema); err != nil {
		t.Fatal(err)
	}
	return reg
}

func saveSettings(reg *plugin.Registry, business map[string]any) error {
	data := map[string]any{"siteName": "Praxis Beispiel", "baseURL": "https://praxis.test", "logo": "/media/logo.png"}
	if business != nil {
		data[Group] = business
	}
	_, err := reg.Entries.Save(context.Background(), entries.Save{Type: fields.SettingsType, Data: data, Publish: true})
	return err
}

var ldRe = regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)

// buildNodes builds the site and returns the JSON-LD nodes of a page by @type.
func buildNodes(t *testing.T, reg *plugin.Registry, file string) map[string]map[string]any {
	t.Helper()
	if _, err := reg.Entries.Save(context.Background(), entries.Save{Type: fields.PageType, Data: map[string]any{"title": "Start", "slug": "index"}, Publish: true}); err != nil {
		t.Fatal(err)
	}
	dist := filepath.Join(t.TempDir(), "dist")
	theme := fstest.MapFS{"templates/page.html": {Data: []byte(`{{define "page"}}<html><head>{{template "baseplate/head" .}}</head><body></body></html>{{end}}`)}}
	if _, err := (&build.Builder{Reg: reg, DistDir: dist, Theme: theme}).Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile(filepath.Join(dist, file))
	if err != nil {
		t.Fatal(err)
	}
	m := ldRe.FindSubmatch(html)
	if m == nil {
		t.Fatalf("no JSON-LD in\n%s", html)
	}
	var doc struct {
		Graph []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal(m[1], &doc); err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]any{}
	for _, n := range doc.Graph {
		out[n["@type"].(string)] = n
	}
	return out
}

func TestRefinesTheOrganization(t *testing.T) {
	reg := setup(t)
	if err := saveSettings(reg, map[string]any{
		"type": "Physiotherapy", "phone": "+49 2263 1234", "email": "praxis@praxis.test",
		"street": "Hauptstr. 1", "postalCode": "51766", "city": "Engelskirchen", "country": "de",
		"latitude": 50.99, "longitude": 7.4,
		"openingHours": "Mo-Fr 08:00-12:00\n\n Mo,We 14:00-18:00 ",
	}); err != nil {
		t.Fatal(err)
	}
	g := buildNodes(t, reg, "index.html")
	if g["Organization"] != nil || g["WebSite"] == nil {
		t.Fatalf("the organization becomes the business, WebSite stays: %v", g)
	}
	n := g["Physiotherapy"]
	if n == nil {
		t.Fatalf("want a Physiotherapy node: %v", g)
	}
	if n["telephone"] != "+49 2263 1234" || n["email"] != "praxis@praxis.test" || n["image"] != "https://praxis.test/media/logo.png" || n["logo"] != n["image"] {
		t.Errorf("contact and image: %v", n)
	}
	addr := n["address"].(map[string]any)
	if addr["@type"] != "PostalAddress" || addr["streetAddress"] != "Hauptstr. 1" || addr["postalCode"] != "51766" || addr["addressLocality"] != "Engelskirchen" || addr["addressCountry"] != "DE" {
		t.Errorf("address: %v", addr)
	}
	if geo := n["geo"].(map[string]any); geo["latitude"] != 50.99 || geo["longitude"] != 7.4 {
		t.Errorf("geo: %v", geo)
	}
	if h := n["openingHours"].([]any); len(h) != 2 || h[0] != "Mo-Fr 08:00-12:00" || h[1] != "Mo,We 14:00-18:00" {
		t.Errorf("openingHours: %v", h)
	}
	if n["@id"] == nil || n["name"] != "Praxis Beispiel" {
		t.Errorf("the node keeps what the core wrote: %v", n)
	}
}

func TestDefaultTypeAndEmptyParts(t *testing.T) {
	reg := setup(t)
	if err := saveSettings(reg, map[string]any{"phone": "123"}); err != nil {
		t.Fatal(err)
	}
	n := buildNodes(t, reg, "index.html")[DefaultType]
	if n == nil {
		t.Fatal("want the default type LocalBusiness")
	}
	for _, k := range []string{"address", "geo", "openingHours", "email"} {
		if _, ok := n[k]; ok {
			t.Errorf("%s is set without a value: %v", k, n[k])
		}
	}
}

// Without any value the core's organization stays as it is.
func TestNoValuesNoChange(t *testing.T) {
	reg := setup(t)
	if err := saveSettings(reg, nil); err != nil {
		t.Fatal(err)
	}
	if g := buildNodes(t, reg, "index.html"); g["Organization"] == nil || g[DefaultType] != nil {
		t.Errorf("want the plain Organization: %v", g)
	}
}

func TestValidation(t *testing.T) {
	reg := setup(t)
	for name, business := range map[string]map[string]any{
		"type with a space":    {"type": "Local Business"},
		"type in lower case":   {"type": "physiotherapy"},
		"hours without days":   {"openingHours": "08:00-12:00"},
		"hours with a bad day": {"openingHours": "Mon-Fri 08:00-12:00"},
		"hours out of range":   {"openingHours": "Mo 25:00-26:00"},
		"latitude alone":       {"latitude": 50.0},
		"latitude too large":   {"latitude": 91.0, "longitude": 7.0},
		"longitude too large":  {"latitude": 50.0, "longitude": 181.0},
	} {
		if err := saveSettings(reg, business); err == nil {
			t.Errorf("%s: saved", name)
		}
	}
	for name, business := range map[string]map[string]any{
		"empty":     {},
		"type":      {"type": "Dentist"},
		"hours":     {"openingHours": "Mo-Fr 08:00-12:00\nSa 09:00-11:30"},
		"both geos": {"latitude": -33.9, "longitude": 151.2},
	} {
		if err := saveSettings(reg, business); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Every key the plugin uses is in its German catalog.
func TestCatalog(t *testing.T) {
	cats, err := i18n.Load(localesFS, "locales")
	if err != nil || len(cats) == 0 {
		t.Fatalf("catalogs: %v %v", cats, err)
	}
	de := cats[0].Messages
	src, _ := os.ReadFile("localbusiness.go")
	for _, m := range regexp.MustCompile(`"(localbusiness\.[\w.]+)"`).FindAllStringSubmatch(string(src), -1) {
		if _, ok := de[m[1]]; !ok {
			t.Errorf("key %s is not translated", m[1])
		}
	}
	if strings.TrimSpace(de["localbusiness.tab"]) == "" {
		t.Error("tab title missing")
	}
}
