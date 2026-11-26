// Package settings gives typed access to the site settings (a singleton
// content type registered by fields.Core). Templates see the same values
// as .Site.<field>.
package settings

import (
	"cmp"
	"context"
	"errors"
	"math"
	"net/url"
	"strings"
	"time"
	_ "time/tzdata" // time zones also on hosts without zoneinfo

	"github.com/janmarkuslanger/baseplate/storage"
)

// Type is the content type (and slug) of the settings entry.
const Type = "settings"

// Field names of the settings type.
const (
	SiteName       = "siteName"
	BaseURL        = "baseURL"
	Language       = "language"
	ServerURL      = "serverURL"
	DefaultOGImage = "defaultOgImage"
	LLMSTxt        = "llmsTxt"
	LLMSContent    = "llmsContent"
	Timezone       = "timezone"
	// Logo, OrgType and SameAs describe the site's organization in the
	// structured data (JSON-LD) of the home page. OrgType is a schema.org
	// type, Organization when empty; SameAs lists profile URLs, one per line.
	Logo    = "logo"
	OrgType = "orgType"
	SameAs  = "sameAs"
	// MediaMaxMB and MediaMaxVideoMB limit uploads to the media library
	// (images and PDFs, videos) in MB; empty means the media library's
	// defaults of 20 and 100 MB.
	MediaMaxMB      = "mediaMaxMB"
	MediaMaxVideoMB = "mediaMaxVideoMB"
)

// MaxMediaMB is the largest upload limit the settings accept.
const MaxMediaMB = 1024

// ValidMediaMB reports whether v can be an upload limit: a whole number of
// MB from 1 to MaxMediaMB.
func ValidMediaMB(v float64) bool { return v == math.Trunc(v) && v >= 1 && v <= MaxMediaMB }

// ValidBaseURL reports whether s can be the site's base URL: an absolute
// http(s) URL without credentials, query or fragment. Canonicals, the
// sitemap and robots.txt are built from it and must be absolute.
func ValidBaseURL(s string) bool {
	u, err := url.Parse(strings.TrimSpace(s))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" &&
		u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == ""
}

// OrgTypes are the schema.org types the core offers for the site's
// organization. Plugins may refine the type (see render.JSONLD).
var OrgTypes = []string{"Organization", "Person"}

// SameAsURLs splits the sameAs setting into its URLs: one per line, blank
// lines ignored.
func SameAsURLs(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// ValidSameAs reports whether s is a list of absolute http(s) URLs, one per
// line.
func ValidSameAs(s string) bool {
	for _, l := range SameAsURLs(s) {
		u, err := url.Parse(l)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return false
		}
	}
	return true
}

// DefaultTimezone is used when the settings name none.
const DefaultTimezone = "Europe/Berlin"

// Settings of the site. Data holds all fields, including ones plugins add
// with Extend.
type Settings struct {
	SiteName       string
	BaseURL        string // without trailing slash
	Language       string
	ServerURL      string // without trailing slash
	DefaultOGImage string
	LLMSTxt        bool
	LLMSContent    string
	// Location is the site's time zone: the admin converts date input with
	// it and templates format dates in it.
	Location *time.Location
	// MediaMaxSize and MediaMaxVideoSize are the upload limits in bytes;
	// 0 if not set (or invalid), which means the media library's defaults.
	MediaMaxSize, MediaMaxVideoSize int64
	Data                            map[string]any
}

// FromData reads settings from entry data.
func FromData(d map[string]any) Settings {
	if d == nil {
		d = map[string]any{}
	}
	str := func(k string) string { s, _ := d[k].(string); return strings.TrimSpace(s) }
	llms, _ := d[LLMSTxt].(bool)
	mb := func(k string) int64 {
		if v, ok := d[k].(float64); ok && ValidMediaMB(v) {
			return int64(v) << 20
		}
		return 0
	}
	loc, err := time.LoadLocation(cmp.Or(str(Timezone), DefaultTimezone))
	if err != nil {
		loc, _ = time.LoadLocation(DefaultTimezone)
	}
	return Settings{
		SiteName:          str(SiteName),
		BaseURL:           strings.TrimRight(str(BaseURL), "/"),
		Language:          str(Language),
		ServerURL:         strings.TrimRight(str(ServerURL), "/"),
		DefaultOGImage:    str(DefaultOGImage),
		LLMSTxt:           llms,
		LLMSContent:       str(LLMSContent),
		Location:          loc,
		MediaMaxSize:      mb(MediaMaxMB),
		MediaMaxVideoSize: mb(MediaMaxVideoMB),
		Data:              d,
	}
}

// Load returns the published settings; zero values if none were
// published yet.
func Load(ctx context.Context, st storage.Entries) (Settings, error) {
	e, err := st.GetEntryBySlug(ctx, Type, Type, "", storage.Published)
	if errors.Is(err, storage.ErrNotFound) {
		return FromData(nil), nil
	}
	if err != nil {
		return Settings{}, err
	}
	return FromData(e.Data), nil
}
