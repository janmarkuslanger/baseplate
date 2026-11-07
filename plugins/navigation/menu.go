package navigation

import (
	"context"
	"fmt"
	"strings"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/render"
)

// Link is one rendered menu link.
type Link struct {
	Title string
	// URL is the page URL (plus "#anchor"), the link's URL, or "" for a
	// heading without target.
	URL string
	// Current is set when the link points to the rendered page: a page or a
	// site-relative URL, without anchor.
	Current bool
	// Active is set on the current link, on links whose page contains the
	// rendered page (/kurse/ on /kurse/yoga/) and on all their ancestors.
	Active   bool
	Children []*Link
	// Data is the link's block data, for fields a project adds.
	Data map[string]any
}

// navigation implements {{navigation "key" (entries "navigation") .}}: the
// links of the navigation with that key, for the page being rendered.
// navs is the result of the entries function, whose page relations are
// resolved; a missing key gives no links. Links to pages that are not
// published are left out together with their children.
func (p *Plugin) navigation(key string, navs []*render.Ref, page any) ([]*Link, error) {
	for _, n := range navs {
		if n.Data["key"] != key {
			continue
		}
		// Resolution drops relations to unpublished pages; the stored entry
		// tells those links apart from links without a page.
		raw, err := p.reg.Store.GetEntry(context.Background(), TypeName, n.ID)
		if err != nil {
			return nil, fmt.Errorf("navigation %q: %w", key, err)
		}
		withPage := map[string]bool{}
		collectPageLinks(raw.Data["items"], withPage)
		cur := ""
		if pg, ok := page.(*render.Page); ok && pg != nil {
			cur = pg.URL
		}
		return links(n.Data["items"], withPage, cur), nil
	}
	return nil, nil
}

// collectPageLinks records the item keys of stored links that name a page.
func collectPageLinks(items any, out map[string]bool) {
	list, _ := items.([]any)
	for _, it := range list {
		m, _ := it.(map[string]any)
		if _, ok := content.RefID(m["page"]); ok {
			k, _ := m[content.ItemKey].(string)
			out[k] = true
		}
		collectPageLinks(m["children"], out)
	}
}

func links(items any, withPage map[string]bool, cur string) []*Link {
	list, _ := items.([]any)
	var out []*Link
	for _, it := range list {
		m, _ := it.(map[string]any)
		l := &Link{Data: m}
		l.Title, _ = m["label"].(string)
		ref, _ := m["page"].(*render.Ref)
		switch {
		case ref != nil && ref.URL != "":
			l.URL = ref.URL
		case withPage[str(m[content.ItemKey])]:
			continue // the page is not published
		default:
			l.URL, _ = m["url"].(string)
		}
		if a, _ := m["anchor"].(string); a != "" && l.URL != "" {
			l.URL += "#" + a
		} else if strings.HasPrefix(l.URL, "/") {
			l.Current = l.URL == cur
			l.Active = l.Current || l.URL != "/" && strings.HasPrefix(cur, l.URL)
		}
		l.Children = links(m["children"], withPage, cur)
		for _, c := range l.Children {
			l.Active = l.Active || c.Active
		}
		out = append(out, l)
	}
	return out
}

func str(v any) string { s, _ := v.(string); return s }
