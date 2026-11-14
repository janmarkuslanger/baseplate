package admin

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/janmarkuslanger/baseplate/internal/auth"
	"github.com/janmarkuslanger/baseplate/plugin"
)

// adminPages returns the plugin pages with their paths trimmed of slashes,
// so routes, nav links and active keys agree. An empty path would catch all
// of /admin/p/; a duplicate one, or one with a space or braces, makes
// ServeMux panic in Mount.
func adminPages(in []plugin.AdminPage) ([]plugin.AdminPage, error) {
	out := make([]plugin.AdminPage, 0, len(in))
	seen := map[string]bool{}
	for _, p := range in {
		p.Path = strings.Trim(p.Path, "/")
		if p.Path == "" {
			return nil, fmt.Errorf("admin page %q: empty path", p.Label)
		}
		if err := checkPagePath(p.Path); err != nil {
			return nil, fmt.Errorf("admin page %q: %w", p.Path, err)
		}
		if seen[p.Path] {
			return nil, fmt.Errorf("admin page %s registered twice", p.Path)
		}
		seen[p.Path] = true
		out = append(out, p)
	}
	return out, nil
}

// checkPagePath accepts slash-separated segments of ASCII letters, digits,
// '-', '_' and '.', so the path is a literal ServeMux pattern and a clean
// URL ("." and ".." segments would be redirected away).
func checkPagePath(path string) error {
	for seg := range strings.SplitSeq(path, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("empty, %q or %q path segment", ".", "..")
		}
		for _, c := range seg {
			if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.ContainsRune("-_.", c)) {
				return fmt.Errorf("character %q not allowed (use letters, digits, '-', '_', '.' and '/')", c)
			}
		}
	}
	return nil
}

// ui implements plugin.AdminUI.
type ui struct {
	a      *Admin
	active string
}

func (u ui) Render(w http.ResponseWriter, r *http.Request, name string, data any) {
	t, ok := u.a.plugins[name]
	if !ok {
		u.a.fail(w, fmt.Errorf("admin template %q not registered", name))
		return
	}
	u.a.render(w, r, http.StatusOK, t, view{Active: u.active, Page: data})
}

func (u ui) CSRFToken(r *http.Request) string {
	if cur := auth.FromContext(r.Context()); cur != nil {
		return cur.Session.CSRF
	}
	return ""
}
