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
	return r.format(t, layout), nil
}

// dateNames are the month and weekday tokens of a Go layout, long form
// first. Their catalog keys are prefix + number: "date.month.5",
// "date.weekday.short.3".
type dateName struct {
	token, prefix string
	month         bool
}

var dateNames = []dateName{
	{"January", "date.month.", true},
	{"Jan", "date.month.short.", true},
	{"Monday", "date.weekday.", false},
	{"Mon", "date.weekday.short.", false},
}

// format is t.Format(layout) with the catalog's month and weekday names.
// The layout is cut at name tokens; the other parts are formatted by Go
// and the names are looked up, so a translation never depends on finding
// Go's English output text.
func (r *Renderer) format(t time.Time, layout string) string {
	var out strings.Builder
	start := 0
	for i := 0; i < len(layout); {
		// Go reads "PM"/"pm" as one token; its M never starts a name.
		if strings.HasPrefix(layout[i:], "PM") || strings.HasPrefix(layout[i:], "pm") {
			i += 2
			continue
		}
		n, ok := nameAt(layout[i:])
		if !ok {
			i++
			continue
		}
		out.WriteString(t.Format(layout[start:i]))
		num, english := int(t.Weekday()), t.Weekday().String()
		if n.month {
			num, english = int(t.Month()), t.Month().String()
		}
		if len(n.token) == 3 {
			english = english[:3]
		}
		name, _ := r.text(n.prefix+strconv.Itoa(num), english)
		out.WriteString(name)
		i += len(n.token)
		start = i
	}
	out.WriteString(t.Format(layout[start:]))
	return out.String()
}

// nameAt reports the name token at the start of s. Like Go's layout
// parser, a short token followed by a lowercase letter ("Mont") is text.
func nameAt(s string) (dateName, bool) {
	for _, n := range dateNames {
		if !strings.HasPrefix(s, n.token) {
			continue
		}
		if rest := s[len(n.token):]; len(n.token) == 3 && rest != "" && 'a' <= rest[0] && rest[0] <= 'z' {
			continue
		}
		return n, true
	}
	return dateName{}, false
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
