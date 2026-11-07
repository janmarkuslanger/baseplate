package content

import (
	"strings"
	"testing"
)

func TestCheckRenames(t *testing.T) {
	text := func(name, from string) Field { return Field{Name: name, Kind: "text", RenamedFrom: from} }
	for _, c := range []struct {
		name   string
		fields []Field
		want   string // "" = valid
	}{
		{"plain rename", []Field{text("summary", "teaser")}, ""},
		{"renamed from itself", []Field{text("summary", "summary")}, ""},
		{"old name reused", []Field{text("summary", "teaser"), text("teaser", "")}, `renamed from "teaser", which is still a field`},
		{"two fields from one name", []Field{text("a", "old"), text("b", "old")}, "both renamed from"},
		{"nested", []Field{{Name: "links", Kind: "list", Fields: []Field{text("label", "title"), text("title", "")}}}, "news.links.label"},
		{"same old name in different lists", []Field{text("a", "x"), {Name: "links", Kind: "list", Fields: []Field{text("b", "x")}}}, ""},
	} {
		s := NewSchema()
		pass := func(a NormalizeArgs) (any, error) { return a.Raw, nil }
		s.AddKind(Kind{Name: "text", Storage: StoreText, Normalize: pass})
		s.AddKind(Kind{Name: "list", Storage: StoreList, Normalize: pass})
		if err := s.AddType(Type{Name: "news", Fields: c.fields}); err != nil {
			t.Fatal(err)
		}
		err := s.Check()
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: want %q, got %v", c.name, c.want, err)
		}
	}
}
