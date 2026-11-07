package navigation

import (
	"context"
	"fmt"
	"strings"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/render"
	"github.com/janmarkuslanger/baseplate/storage"
)

// Link is one rendered menu link.
type Link struct {
	Title string
	// URL is the page URL, the link's URL, or "" for a heading.
	URL string
	// Current is set when the link points to the rendered page (a page or a
	// site-relative URL equal to the page URL).
	Current bool
	// Active is set on the current link, on links whose URL contains the
	// rendered page (/kurse/ on /kurse/yoga/) and on all their ancestors.
	Active   bool
	Children []*Link
	// Data is the link entry's data, for fields a project adds.
	Data map[string]any
}

// navigation implements {{navigation "key" .}}: the links of the published
// navigation with that key, for the rendered page. An unknown key gives no
// links. Unpublished links and links to unpublished pages are left out
// together with their children. Page URLs come from the page's .Nav, so
// pass the page (in a layout: .Page).
func (p *Plugin) navigation(key string, page any) ([]*Link, error) {
	ctx := context.Background()
	navs, err := p.published(ctx, TypeName)
	if err != nil {
		return nil, fmt.Errorf("navigation %q: %w", key, err)
	}
	for _, n := range navs {
		if n.Data["key"] != key {
			continue
		}
		links, err := p.published(ctx, LinkType)
		if err != nil {
			return nil, fmt.Errorf("navigation %q: %w", key, err)
		}
		b := builder{links: map[int64]map[string]any{}, pages: map[int64]string{}}
		for _, l := range links {
			b.links[l.ID] = l.Data
		}
		if pg, ok := page.(*render.Page); ok && pg != nil {
			b.cur = pg.URL
			for _, l := range pg.Nav {
				b.pages[l.ID] = l.URL
			}
		}
		return b.build(n.Data["items"], map[int64]bool{}), nil
	}
	return nil, nil
}

func (p *Plugin) published(ctx context.Context, typ string) ([]storage.Entry, error) {
	list, _, err := p.reg.Store.ListEntries(ctx, storage.ListQuery{Type: typ, Status: storage.StatusPublished})
	return list, err
}

type builder struct {
	links map[int64]map[string]any // published links by ID
	pages map[int64]string         // URLs of published pages by ID
	cur   string                   // URL of the rendered page
}

// build turns a list of link IDs into links. path holds the links above,
// so a link that contains itself (A → B → A) stops instead of looping.
func (b builder) build(ids any, path map[int64]bool) []*Link {
	list, _ := ids.([]any)
	var out []*Link
	for _, it := range list {
		id, ok := content.RefID(it)
		data, published := b.links[id]
		if !ok || !published || path[id] {
			continue
		}
		l := &Link{Data: data}
		l.Title, _ = data["label"].(string)
		if pageID, hasPage := content.RefID(data["page"]); hasPage {
			if l.URL, ok = b.pages[pageID]; !ok {
				continue // the page is not published
			}
		} else {
			l.URL, _ = data["url"].(string)
		}
		if strings.HasPrefix(l.URL, "/") && !strings.Contains(l.URL, "#") {
			l.Current = l.URL == b.cur
			l.Active = l.Current || l.URL != "/" && strings.HasPrefix(b.cur, l.URL)
		}
		path[id] = true
		l.Children = b.build(data["children"], path)
		delete(path, id)
		for _, c := range l.Children {
			l.Active = l.Active || c.Active
		}
		out = append(out, l)
	}
	return out
}
