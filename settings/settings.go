// Package settings gives typed access to the site settings (a singleton
// content type registered by fields.Core). Templates see the same values
// as .Site.<field>.
package settings

import (
	"context"
	"errors"
	"strings"

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
)

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
	Data           map[string]any
}

// FromData reads settings from entry data.
func FromData(d map[string]any) Settings {
	if d == nil {
		d = map[string]any{}
	}
	str := func(k string) string { s, _ := d[k].(string); return strings.TrimSpace(s) }
	llms, _ := d[LLMSTxt].(bool)
	return Settings{
		SiteName:       str(SiteName),
		BaseURL:        strings.TrimRight(str(BaseURL), "/"),
		Language:       str(Language),
		ServerURL:      strings.TrimRight(str(ServerURL), "/"),
		DefaultOGImage: str(DefaultOGImage),
		LLMSTxt:        llms,
		LLMSContent:    str(LLMSContent),
		Data:           d,
	}
}

// Load returns the settings; zero values if none were saved yet.
func Load(ctx context.Context, st storage.Store) (Settings, error) {
	e, err := st.GetEntryBySlug(ctx, Type, Type)
	if errors.Is(err, storage.ErrNotFound) {
		return FromData(nil), nil
	}
	if err != nil {
		return Settings{}, err
	}
	return FromData(e.Data), nil
}
