package i18n

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
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

func TestLoad(t *testing.T) {
	cs, err := Load(fstest.MapFS{
		"l/de.json": {Data: []byte(`{"a": "A"}`)},
		"l/en.json": {Data: []byte(`{"a": "a"}`)},
		"l/README":  {Data: []byte("not a catalog")},
	}, "l")
	if err != nil || len(cs) != 2 || cs[0].Lang != "de" || cs[0].Messages["a"] != "A" {
		t.Fatalf("%v %+v", err, cs)
	}
	if _, err := Load(fstest.MapFS{"l/de.json": {Data: []byte(`{`)}}, "l"); err == nil || !strings.Contains(err.Error(), "de.json") {
		t.Errorf("broken file: %v", err)
	}
	if _, err := Load(fstest.MapFS{}, "missing"); err == nil {
		t.Error("missing dir must fail")
	}
}
