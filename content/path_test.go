package content

import (
	"strings"
	"testing"
)

func TestTypeURL(t *testing.T) {
	page := Type{Name: "page", Path: "/{parents}/{slug}/"}
	for _, c := range []struct {
		typ     Type
		slug    string
		parents []string
		want    string
	}{
		{Type{Name: "blog"}, "hallo", nil, "/blog/hallo/"},
		{Type{Name: "post", Path: "/blog/{slug}/"}, "hallo", nil, "/blog/hallo/"},
		{Type{Name: "landing", Path: "/{slug}/"}, "angebot", nil, "/angebot/"},
		{page, "index", nil, "/"},
		{page, "ueber-uns", nil, "/ueber-uns/"},
		{page, "team", []string{"ueber-uns"}, "/ueber-uns/team/"},
		{page, "c", []string{"a", "b"}, "/a/b/c/"},
		{page, "index", []string{"ueber-uns"}, "/ueber-uns/"},
	} {
		if got := c.typ.URL(c.slug, c.parents); got != c.want {
			t.Errorf("%s %q %v = %q, want %q", c.typ.PathPattern(), c.slug, c.parents, got, c.want)
		}
	}
}

func TestPathCheck(t *testing.T) {
	parent := Field{Name: "parent", Kind: "relation", Ref: "a"}
	for _, c := range []struct {
		typ  Type
		want string // "" = valid
	}{
		{Type{Name: "a"}, ""},
		{Type{Name: "a", Path: "/{slug}/"}, ""},
		{Type{Name: "a", Path: "/x/{parents}/{slug}/", Fields: []Field{parent}}, ""},
		{Type{Name: "a", Path: "blog/{slug}/"}, "start and end"},
		{Type{Name: "a", Path: "/blog/{slug}"}, "start and end"},
		{Type{Name: "a", Path: "/blog/"}, "needs {slug}"},
		{Type{Name: "a", Path: "/{year}/{slug}/"}, "unknown placeholder"},
		{Type{Name: "a", Path: "/{parents}/{slug}/"}, "no field \"parent\""},
		{Type{Name: "a", Path: "/{parents}/{slug}/", Fields: []Field{{Name: "parent", Kind: "relation", Ref: "b"}}}, "no field \"parent\""},
	} {
		c.typ.Routable = true
		s := NewSchema()
		s.AddKind(Kind{Name: "relation", Storage: StoreRelation, Normalize: func(a NormalizeArgs) (any, error) { return a.Raw, nil }})
		s.AddType(Type{Name: "b"})
		if err := s.AddType(c.typ); err != nil {
			t.Fatal(err)
		}
		err := s.Check()
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%q: %v", c.typ.Path, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%q: want %q, got %v", c.typ.Path, c.want, err)
		}
	}

	s := NewSchema()
	s.AddType(Type{Name: "a", Path: "/oops/"})
	if err := s.Check(); err != nil {
		t.Errorf("paths of non-routable types are not checked: %v", err)
	}
}
