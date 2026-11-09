package navigation

import (
	"context"
	"fmt"
	"strings"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/render"
	"github.com/janmarkuslanger/baseplate/storage"
)

// Item is one rendered menu item.
type Item struct {
	Title string
	// URL is the page URL, the item's URL, or "" for a heading.
	URL string
	// Current is set when the item points to the rendered page (a page or a
	// site-relative URL equal to the page URL).
	Current bool
	// Active is set on the current item, on items whose URL contains the
	// rendered page (/kurse/ on /kurse/yoga/) and on all their ancestors.
	Active bool
	Items  []*Item
	// Data is the item's block data, for fields a project adds.
	Data map[string]any
}

// navigation implements {{navigation "key" .}}: the items of the published
// navigation with that key, for the rendered page. An unknown key gives no
// items. Items pointing to unpublished pages are left out together with
// their items. Page URLs come from the page's .Nav, so pass the page (in a
// layout: .Page).
func (p *Plugin) navigation(key string, page any) ([]*Item, error) {
	pg, _ := page.(*render.Page)
	snap, err := p.snapshot(pg)
	if err != nil {
		return nil, fmt.Errorf("navigation %q: %w", key, err)
	}
	items, ok := snap.navs[key]
	if !ok {
		return nil, nil
	}
	b := builder{pages: snap.pages}
	if pg != nil {
		b.cur = pg.URL
	}
	return b.build(items), nil
}

// snapshot is what navigation reads besides the rendered page: the items of
// the published navigations by key and the URLs of the published pages.
type snapshot struct {
	navs  map[string]any
	pages map[int64]string
}

// snapshot returns the snapshot for the build that rendered pg. All pages of
// a build share one .Nav slice, so the last snapshot is reused while that
// slice is the same; loading the navigations and indexing every page once
// per rendered page would make a build quadratic in its page count.
func (p *Plugin) snapshot(pg *render.Page) (*snapshot, error) {
	var nav []render.Link
	if pg != nil {
		nav = pg.Nav
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(nav) > 0 && p.last != nil && len(p.lastNav) == len(nav) && &p.lastNav[0] == &nav[0] {
		return p.last, nil
	}
	list, _, err := p.reg.Store.ListEntries(context.Background(), storage.ListQuery{Type: TypeName, Version: storage.Published})
	if err != nil {
		return nil, err
	}
	s := &snapshot{navs: map[string]any{}, pages: make(map[int64]string, len(nav))}
	for _, n := range list {
		if k, ok := n.Data["key"].(string); ok {
			if _, dup := s.navs[k]; !dup {
				s.navs[k] = n.Data["items"]
			}
		}
	}
	for _, l := range nav {
		s.pages[l.ID] = l.URL
	}
	if len(nav) > 0 {
		p.last, p.lastNav = s, nav
	}
	return s, nil
}

type builder struct {
	pages map[int64]string // URLs of published pages by ID
	cur   string           // URL of the rendered page
}

func (b builder) build(items any) []*Item {
	list, _ := items.([]any)
	var out []*Item
	for _, it := range list {
		data, _ := it.(map[string]any)
		item := &Item{Data: data}
		item.Title, _ = data["title"].(string)
		if pageID, hasPage := content.RefID(data["page"]); hasPage {
			url, published := b.pages[pageID]
			if !published {
				continue
			}
			item.URL = url
		} else {
			item.URL, _ = data["url"].(string)
		}
		if strings.HasPrefix(item.URL, "/") && !strings.Contains(item.URL, "#") {
			item.Current = item.URL == b.cur
			item.Active = item.Current || item.URL != "/" && strings.HasPrefix(b.cur, item.URL)
		}
		item.Items = b.build(data["items"])
		for _, c := range item.Items {
			item.Active = item.Active || c.Active
		}
		out = append(out, item)
	}
	return out
}
