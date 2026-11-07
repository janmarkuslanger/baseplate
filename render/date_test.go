package render

import (
	"strings"
	"testing"
	"time"

	"github.com/janmarkuslanger/baseplate/i18n"
	"github.com/janmarkuslanger/baseplate/plugin"
)

type catalogPlugin struct{}

func (catalogPlugin) Name() string { return "cat" }
func (catalogPlugin) Register(r *plugin.Registry) error {
	i18n.Catalogs.Add(r, i18n.Catalog{Lang: "de", Messages: map[string]string{"date.format.card": "02. January 2006"}})
	return nil
}

func TestDate(t *testing.T) {
	reg := plugin.NewRegistry(plugin.Services{})
	if err := reg.Use(catalogPlugin{}); err != nil {
		t.Fatal(err)
	}
	berlin, _ := time.LoadLocation("Europe/Berlin")
	renderer := func(lang string) *Renderer {
		r, err := New(reg, nil, Options{Lang: lang, Location: berlin})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	// 2024-05-01 00:30 in Berlin = 2024-04-30 22:30 UTC, a Wednesday.
	const ts = int64(1714516200)
	for _, c := range []struct {
		lang   string
		v      any
		format string
		zone   []string
		want   string
	}{
		{"de", ts, "short", nil, "01.05.2024"},
		{"de", ts, "long", nil, "1. Mai 2024"},
		{"de", float64(ts), "datetime", nil, "1. Mai 2024, 00:30"},
		{"de", ts, "time", nil, "00:30"},
		{"de", ts, "card", nil, "01. Mai 2024"},
		{"de-DE", ts, "long", nil, "1. Mai 2024"},
		{"en", ts, "long", nil, "May 1, 2024"},
		{"en", ts, "datetime", nil, "May 1, 2024, 12:30 AM"},
		{"fr", ts, "short", nil, "05/01/2024"}, // no catalog: English, not the admin's German
		{"de", ts, "iso", nil, "2024-05-01T00:30:00+02:00"},
		{"de", ts, "isodate", nil, "2024-05-01"},
		{"de", ts, "short", []string{"UTC"}, "30.04.2024"},
		{"de", nil, "long", nil, ""},
	} {
		got, err := renderer(c.lang).date(c.v, c.format, c.zone...)
		if err != nil || got != c.want {
			t.Errorf("%s %s %v: %q (%v), want %q", c.lang, c.format, c.zone, got, err, c.want)
		}
	}

	r := renderer("de")
	r.i18n.Add(i18n.Catalog{Lang: "de", Messages: map[string]string{"date.format.weekday": "Monday, 2. January"}})
	if got, _ := r.date(ts, "weekday"); got != "Mittwoch, 1. Mai" {
		t.Errorf("weekday: %q", got)
	}

	for _, c := range []struct {
		v      any
		format string
		zone   []string
		want   string
	}{
		{ts, "nope", nil, "unknown format"},
		{"2024-05-01", "long", nil, "not a date field value"},
		{ts, "long", []string{"Mars/Olympus"}, "unknown time zone"},
	} {
		if _, err := r.date(c.v, c.format, c.zone...); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v %s: want %q, got %v", c.v, c.format, c.want, err)
		}
	}
}

func TestDateNames(t *testing.T) {
	reg := plugin.NewRegistry(plugin.Services{})
	berlin, _ := time.LoadLocation("Europe/Berlin")
	layouts := map[string]string{
		"rfc":     "Mon, 02 Jan 2006",
		"all":     "Monday Mon January Jan",
		"literal": "Mont Jan",        // "Mont" is text in Go layouts
		"ampm":    "3PM Mon",         // the M of PM starts no name
		"text":    "May: January",    // English names in literal text stay
		"repeat":  "Jan Jan Mon Mon", // every occurrence is translated
	}
	renderer := func(lang string) *Renderer {
		r, err := New(reg, nil, Options{Lang: lang, Location: berlin})
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]string{}
		for name, layout := range layouts {
			m["date.format."+name] = layout
		}
		r.i18n.Add(i18n.Catalog{Lang: lang, Messages: m})
		return r
	}
	// 2024-05-01 00:30 in Berlin is a Wednesday, 2024-09-01 00:00 a Sunday.
	const may, sep = int64(1714516200), int64(1725141600)
	for _, c := range []struct {
		lang, format string
		ts           int64
		want         string
	}{
		{"de", "rfc", may, "Mi., 01 Mai 2024"},
		{"de", "rfc", sep, "So., 01 Sept. 2024"},
		{"de", "all", may, "Mittwoch Mi. Mai Mai"},
		{"de", "literal", may, "Mont Mai"},
		{"de", "ampm", may, "12AM Mi."},
		{"de", "text", may, "May: Mai"},
		{"de", "repeat", sep, "Sept. Sept. So. So."},
		{"de-AT", "rfc", may, "Mi., 01 Mai 2024"},
		{"fr", "rfc", may, "Wed, 01 May 2024"}, // no catalog: Go's English names
		{"fr", "all", sep, "Sunday Sun September Sep"},
	} {
		got, err := renderer(c.lang).date(c.ts, c.format)
		if err != nil || got != c.want {
			t.Errorf("%s %s: %q (%v), want %q", c.lang, c.format, got, err, c.want)
		}
	}
}
