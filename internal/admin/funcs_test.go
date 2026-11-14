package admin

import (
	"testing"
	"unicode/utf8"
)

func TestInitial(t *testing.T) {
	cases := map[string]string{
		"":            "?",
		"anna@x.de":   "A",
		"über@x.de":   "Ü",
		"日本@x.jp":     "日",
		"\xff@broken": "?",
	}
	for in, want := range cases {
		got := initial(in)
		if got != want || !utf8.ValidString(got) {
			t.Errorf("initial(%q) = %q, want %q", in, got, want)
		}
	}
}
