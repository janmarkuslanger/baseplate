package i18n

import (
	"errors"
	"fmt"
	"testing"
)

func TestTr(t *testing.T) {
	b := NewBundle(
		Catalog{Lang: "de", Messages: map[string]string{"x.missing": "fehlt: {0}", "x.denied": "verboten"}},
		Catalog{Lang: "en", Messages: map[string]string{"x.denied": "denied"}},
	)
	const denied = Key("x.denied")
	wrapped := fmt.Errorf("saving: %w", denied)
	cases := []struct {
		err  error
		lang string
		want string
	}{
		{denied, "en", "denied"},
		{wrapped, "en", "denied"},   // found in the chain
		{wrapped, "fr", "verboten"}, // falls back to German
		{fmt.Errorf("a: %w", M("x.missing", 3)), "de", "fehlt: 3"},
		{errors.New("plain"), "de", "plain"},
	}
	for _, c := range cases {
		if got := b.Tr(c.lang, c.err); got != c.want {
			t.Errorf("Tr(%q, %v) = %q, want %q", c.lang, c.err, got, c.want)
		}
	}
	if !errors.Is(wrapped, denied) {
		t.Error("Key must work with errors.Is")
	}
}

func TestCoreKeysHaveGermanTexts(t *testing.T) {
	for _, k := range []Key{"login.invalid", "login.rateLimited", "validation.relationTarget", "media.error.type",
		"media.error.size", "media.error.content", "auth.csrf", "build.baseURLMissing"} {
		if k.Error() == string(k) {
			t.Errorf("%s has no German text", k)
		}
	}
}
