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

func TestTPlaceholders(t *testing.T) {
	b := NewBundle(Catalog{Lang: "en", Messages: map[string]string{
		"x.two":  "{0} and {1}",
		"x.miss": "{0} {2} {x} {+0} {",
	}})
	cases := []struct {
		key  string
		args []any
		want string
	}{
		{"x.two", []any{"a", "b"}, "a and b"},
		// an argument containing a placeholder stays literal
		{"x.two", []any{"{1}", "b"}, "{1} and b"},
		{"x.two", []any{"{0}{1}", "{0}"}, "{0}{1} and {0}"},
		{"x.miss", []any{"a"}, "a {2} {x} {+0} {"},
		{"x.two", nil, "{0} and {1}"},
	}
	for _, c := range cases {
		if got := b.T("en", c.key, c.args...); got != c.want {
			t.Errorf("T(%q, %v) = %q, want %q", c.key, c.args, got, c.want)
		}
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
