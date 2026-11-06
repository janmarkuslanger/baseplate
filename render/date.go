package render

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Date formats without translation. Named formats come from the catalogs
// ("date.format.<name>", Go layouts), so a project can add its own:
// i18n.Catalog{Lang: "de", Messages: {"date.format.card": "02. January 2006"}}.
var dateFixed = map[string]string{
	"iso":     time.RFC3339, // JSON-LD, <time datetime>
	"isodate": time.DateOnly,
}

// dateFallback is used for languages without a catalog entry.
var dateFallback = map[string]string{
	"short":    "01/02/2006",
	"long":     "January 2, 2006",
	"time":     "3:04 PM",
	"datetime": "January 2, 2006, 3:04 PM",
}

// date formats a date field (Unix seconds) in the site's time zone, or in
// zone if given: {{date .Data.publishedDate "long"}}. Empty values give "".
func (r *Renderer) date(v any, format string, zone ...string) (string, error) {
	var sec int64
	switch x := v.(type) {
	case nil:
		return "", nil
	case int64:
		sec = x
	case int:
		sec = int64(x)
	case float64:
		sec = int64(x)
	default:
		return "", fmt.Errorf("date: %v is not a date field value", v)
	}
	loc := r.loc
	if len(zone) > 0 {
		l, err := time.LoadLocation(zone[0])
		if err != nil {
			return "", fmt.Errorf("date: %w", err)
		}
		loc = l
	}
	t := time.Unix(sec, 0).In(loc)
	if layout, ok := dateFixed[format]; ok {
		return t.Format(layout), nil
	}
	layout, ok := r.text("date.format."+format, dateFallback[format])
	if !ok {
		return "", fmt.Errorf("date: unknown format %q", format)
	}
	out := t.Format(layout)
	if strings.Contains(layout, "January") {
		m, _ := r.text("date.month."+strconv.Itoa(int(t.Month())), t.Month().String())
		out = strings.Replace(out, t.Month().String(), m, 1)
	}
	if strings.Contains(layout, "Monday") {
		d, _ := r.text("date.weekday."+strconv.Itoa(int(t.Weekday())), t.Weekday().String())
		out = strings.Replace(out, t.Weekday().String(), d, 1)
	}
	return out, nil
}

// text returns the site language's catalog text for key ("de-DE" also
// finds "de"), else fallback. ok is false if neither exists.
func (r *Renderer) text(key, fallback string) (string, bool) {
	for _, l := range []string{r.lang, strings.SplitN(r.lang, "-", 2)[0]} {
		if r.i18n.Has(l, key) {
			return r.i18n.T(l, key), true
		}
	}
	return fallback, fallback != ""
}
