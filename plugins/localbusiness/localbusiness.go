// Package localbusiness describes a business with a location in the site's
// structured data: address, phone, e-mail, opening hours and coordinates,
// edited in the settings (tab "Standort"). It refines the organization the
// core puts on the home page (render.JSONLD) to a schema.org LocalBusiness,
// or a subtype such as Physiotherapy.
//
//	app.Use(localbusiness.New())
//
// The values are in the settings as Group, so a theme can use them as well:
// {{.Site.localBusiness.phone}}.
package localbusiness

import (
	"context"
	"embed"
	"regexp"
	"strings"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/entries"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/i18n"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/render"
)

// Group is the name of the settings field that holds the business data.
const Group = "localBusiness"

// DefaultType is the schema.org type used when none is set.
const DefaultType = "LocalBusiness"

var (
	// typeRe is a schema.org type name.
	typeRe = regexp.MustCompile(`^[A-Z][A-Za-z]{1,59}$`)
	// hoursRe is one openingHours line: days (Mo-Fr, Mo,We) and a time range.
	hoursRe = regexp.MustCompile(`^(?:Mo|Tu|We|Th|Fr|Sa|Su)(?:[-,](?:Mo|Tu|We|Th|Fr|Sa|Su))* (?:[01]\d|2[0-3]):[0-5]\d-(?:[01]\d|2[0-3]):[0-5]\d$`)
)

//go:embed locales/*.json
var localesFS embed.FS

// Plugin adds the "Standort" settings and the LocalBusiness structured data.
type Plugin struct{}

// New returns the plugin.
func New() *Plugin { return &Plugin{} }

func (*Plugin) Name() string { return "localbusiness" }

func (*Plugin) Register(r *plugin.Registry) error {
	cats, err := i18n.Load(localesFS, "locales")
	if err != nil {
		return err
	}
	for _, c := range cats {
		i18n.Catalogs.Add(r, c)
	}
	if err := r.Extend(fields.SettingsType, content.Field{
		Name: Group, Label: "localbusiness.group", Kind: fields.Group, Tab: "localbusiness.tab", Help: "localbusiness.group.help",
		Fields: []content.Field{
			{Name: "type", Label: "localbusiness.type", Kind: fields.Text, Max: 60, Help: "localbusiness.type.help"},
			{Name: "phone", Label: "localbusiness.phone", Kind: fields.Text, Max: 40},
			{Name: "email", Label: "localbusiness.email", Kind: fields.Email},
			{Name: "street", Label: "localbusiness.street", Kind: fields.Text, Max: 120},
			{Name: "postalCode", Label: "localbusiness.postalCode", Kind: fields.Text, Max: 20},
			{Name: "city", Label: "localbusiness.city", Kind: fields.Text, Max: 120},
			{Name: "country", Label: "localbusiness.country", Kind: fields.Text, Max: 2, Help: "localbusiness.country.help"},
			{Name: "latitude", Label: "localbusiness.latitude", Kind: fields.Number},
			{Name: "longitude", Label: "localbusiness.longitude", Kind: fields.Number},
			{Name: "openingHours", Label: "localbusiness.openingHours", Kind: fields.Textarea, Max: 1000, Help: "localbusiness.openingHours.help"},
		},
	}); err != nil {
		return err
	}
	entries.BeforeSave.On(r, check)
	render.JSONLD.Filter(r, refine)
	return nil
}

// check validates what the field kinds cannot: the type name, the opening
// hours lines and the coordinates.
func check(_ context.Context, e *entries.Event) error {
	if e.Type.Name != fields.SettingsType {
		return nil
	}
	b, _ := e.Entry.Data[Group].(map[string]any)
	if t := str(b["type"]); t != "" && !typeRe.MatchString(t) {
		return i18n.M("localbusiness.typeInvalid")
	}
	for _, l := range lines(str(b["openingHours"])) {
		if !hoursRe.MatchString(l) {
			return i18n.M("localbusiness.hoursInvalid")
		}
	}
	lat, hasLat := b["latitude"].(float64)
	lng, hasLng := b["longitude"].(float64)
	if hasLat != hasLng || lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return i18n.M("localbusiness.geoInvalid")
	}
	return nil
}

// refine turns the organization of the home page into the business. Without
// an organization node (no site name, or a Person) or without any value it
// leaves the graph alone.
func refine(_ context.Context, g *render.Graph) (*render.Graph, error) {
	b, _ := g.Page.Site[Group].(map[string]any)
	if empty(b) {
		return g, nil
	}
	for _, n := range g.Nodes {
		if n["@type"] != "Organization" {
			continue
		}
		n["@type"] = DefaultType
		if t := str(b["type"]); typeRe.MatchString(t) {
			n["@type"] = t
		}
		if logo, ok := n["logo"]; ok {
			n["image"] = logo // Google asks for an image of a local business
		}
		set(n, "telephone", str(b["phone"]))
		set(n, "email", str(b["email"]))
		if addr := address(b); addr != nil {
			n["address"] = addr
		}
		if lat, ok := b["latitude"].(float64); ok {
			if lng, ok := b["longitude"].(float64); ok {
				n["geo"] = map[string]any{"@type": "GeoCoordinates", "latitude": lat, "longitude": lng}
			}
		}
		if hours := lines(str(b["openingHours"])); len(hours) > 0 {
			n["openingHours"] = hours
		}
		break
	}
	return g, nil
}

// empty reports whether the group holds no value. Stored groups carry
// internal keys ("_key"), and empty fields are left out.
func empty(b map[string]any) bool {
	for k, v := range b {
		if !strings.HasPrefix(k, "_") && v != nil && v != "" {
			return false
		}
	}
	return true
}

// address is the PostalAddress of the settings, nil if all parts are empty.
func address(b map[string]any) map[string]any {
	a := map[string]any{"@type": "PostalAddress"}
	set(a, "streetAddress", str(b["street"]))
	set(a, "postalCode", str(b["postalCode"]))
	set(a, "addressLocality", str(b["city"]))
	set(a, "addressCountry", strings.ToUpper(str(b["country"])))
	if len(a) == 1 {
		return nil
	}
	return a
}

func set(m map[string]any, k, v string) {
	if v != "" {
		m[k] = v
	}
}

func str(v any) string { s, _ := v.(string); return strings.TrimSpace(s) }

// lines splits s into its non-blank lines, trimmed.
func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}
