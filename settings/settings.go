// Package settings gives typed access to the site settings (a singleton
// content type registered by fields.Core). Templates see the same values
// as .Site.<field>.
package settings

import (
	"cmp"
	"context"
	"errors"
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
)

// ValidBaseURL reports whether s can be the site's base URL: an absolute
// http(s) URL without credentials, query or fragment. Canonicals, the
// sitemap and robots.txt are built from it and must be absolute.
func ValidBaseURL(s string) bool {
	u, err := url.Parse(strings.TrimSpace(s))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" &&
		u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == ""
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
	Data     map[string]any
}

// FromData reads settings from entry data.
func FromData(d map[string]any) Settings {
	if d == nil {
		d = map[string]any{}
	}
	str := func(k string) string { s, _ := d[k].(string); return strings.TrimSpace(s) }
	llms, _ := d[LLMSTxt].(bool)
	loc, err := time.LoadLocation(cmp.Or(str(Timezone), DefaultTimezone))
	if err != nil {
		loc, _ = time.LoadLocation(DefaultTimezone)
	}
	return Settings{
		SiteName:       str(SiteName),
		BaseURL:        strings.TrimRight(str(BaseURL), "/"),
		Language:       str(Language),
		ServerURL:      strings.TrimRight(str(ServerURL), "/"),
		DefaultOGImage: str(DefaultOGImage),
		LLMSTxt:        llms,
		LLMSContent:    str(LLMSContent),
		Location:       loc,
		Data:           d,
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
