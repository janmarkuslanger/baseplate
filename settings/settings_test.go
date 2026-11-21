package settings_test

import (
	"testing"

	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/settings"
)

// Every typed key must be a field of the registered settings type.
func TestKeysMatchSettingsType(t *testing.T) {
	r := plugin.NewRegistry(plugin.Services{})
	if err := r.Use(fields.Core{}); err != nil {
		t.Fatal(err)
	}
	typ, ok := r.Schema.Type(settings.Type)
	if !ok {
		t.Fatal("settings type missing")
	}
	have := map[string]bool{}
	for _, f := range typ.Fields {
		have[f.Name] = true
	}
	for _, k := range []string{settings.SiteName, settings.BaseURL, settings.Language, settings.ServerURL, settings.DefaultOGImage, settings.LLMSTxt, settings.LLMSContent, settings.Timezone, settings.MediaMaxMB, settings.MediaMaxVideoMB} {
		if !have[k] {
			t.Errorf("key %q is not a field of the settings type", k)
		}
	}
}

func TestFromData(t *testing.T) {
	s := settings.FromData(map[string]any{"siteName": " Acme ", "baseURL": "https://a.test/", "llmsTxt": true, "extra": 1})
	if s.SiteName != "Acme" || s.BaseURL != "https://a.test" || !s.LLMSTxt || s.Data["extra"] != 1 {
		t.Errorf("%+v", s)
	}
	if z := settings.FromData(nil); z.Data == nil || z.BaseURL != "" {
		t.Errorf("zero: %+v", z)
	}
}

func TestValidBaseURL(t *testing.T) {
	for in, want := range map[string]bool{
		"https://a.test": true, "http://a.test/sub": true, " https://a.test/ ": true,
		"": false, "/": false, "/site": false, "a.test": false, "mailto:a@a.test": false, "ftp://a.test": false,
		"https://": false, "https://a.test/?x=1": false, "https://a.test/#top": false, "https://u:p@a.test": false,
	} {
		if got := settings.ValidBaseURL(in); got != want {
			t.Errorf("%q: %v, want %v", in, got, want)
		}
	}
}

func TestLocation(t *testing.T) {
	for in, want := range map[string]string{"": "Europe/Berlin", "America/New_York": "America/New_York", "Mars/Olympus": "Europe/Berlin"} {
		if got := settings.FromData(map[string]any{"timezone": in}).Location.String(); got != want {
			t.Errorf("%q: %s, want %s", in, got, want)
		}
	}
}

// Upload limits are read in MB and returned in bytes; values the settings
// would not accept count as unset.
func TestFromDataMediaLimits(t *testing.T) {
	s := settings.FromData(map[string]any{settings.MediaMaxMB: 5.0, settings.MediaMaxVideoMB: float64(settings.MaxMediaMB)})
	if s.MediaMaxSize != 5<<20 || s.MediaMaxVideoSize != settings.MaxMediaMB<<20 {
		t.Errorf("limits = %d, %d", s.MediaMaxSize, s.MediaMaxVideoSize)
	}
	for _, v := range []any{nil, 0.0, -1.0, 2.5, float64(settings.MaxMediaMB + 1), 1e300, "5"} {
		if s := settings.FromData(map[string]any{settings.MediaMaxMB: v}); s.MediaMaxSize != 0 {
			t.Errorf("%v: limit %d, want 0", v, s.MediaMaxSize)
		}
	}
}
