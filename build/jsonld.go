package build

import (
	"encoding/json"
	"html/template"
	"slices"

	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/render"
	"github.com/janmarkuslanger/baseplate/settings"
)

// jsonLD returns the structured data of p as JSON, or "" if there is none.
// The home page carries the WebSite and the site's organization, pages
// below another page a BreadcrumbList; render.JSONLD may add to both.
func (s *site) jsonLD(p *render.Page) (template.JS, error) {
	g := &render.Graph{Page: p}
	if p.URL == "/" {
		g.Nodes = append(g.Nodes, s.siteNodes()...)
	}
	if n := s.breadcrumbs(p); n != nil {
		g.Nodes = append(g.Nodes, n)
	}
	g, err := render.JSONLD.Apply(s.ctx, s.b.Reg, g)
	if err != nil && s.b.Reg.Logger != nil {
		s.b.Reg.Logger.Error("structured data", "page", p.URL, "err", err)
	}
	if len(g.Nodes) == 0 {
		return "", nil
	}
	// Marshal escapes <, > and &, so the document cannot end the script element.
	b, err := json.Marshal(map[string]any{"@context": "https://schema.org", "@graph": g.Nodes})
	return template.JS(b), err
}

// siteNodes are the WebSite and the organization of the settings. Without a
// site name there is nothing worth saying.
func (s *site) siteNodes() []map[string]any {
	cfg := s.cfg
	if cfg.SiteName == "" {
		return nil
	}
	orgID, siteID := cfg.BaseURL+"/#organization", cfg.BaseURL+"/#website"
	org := map[string]any{"@type": "Organization", "@id": orgID, "name": cfg.SiteName, "url": cfg.BaseURL + "/"}
	if t := str(cfg.Data[settings.OrgType]); slices.Contains(settings.OrgTypes, t) {
		org["@type"] = t
	}
	if logo := absolute(cfg.BaseURL, str(cfg.Data[settings.Logo])); logo != "" {
		org["logo"] = logo
	}
	if urls := settings.SameAsURLs(str(cfg.Data[settings.SameAs])); len(urls) > 0 {
		org["sameAs"] = urls
	}
	web := map[string]any{"@type": "WebSite", "@id": siteID, "url": cfg.BaseURL + "/", "name": cfg.SiteName, "publisher": map[string]any{"@id": orgID}}
	if cfg.Language != "" {
		web["inLanguage"] = cfg.Language
	}
	return []map[string]any{web, org}
}

// breadcrumbs is the BreadcrumbList of a page: the home page, the parents
// and the page itself. Nil for the home page, for other types than pages and
// when the list would have fewer than two items.
func (s *site) breadcrumbs(p *render.Page) map[string]any {
	if p.Type.Name != fields.PageType || p.URL == "/" {
		return nil
	}
	byID := make(map[int64]render.Link, len(p.Nav))
	for _, l := range p.Nav {
		byID[l.ID] = l
	}
	var chain []render.Link
	seen := map[int64]bool{}
	for id := p.Entry.ID; id != 0 && !seen[id]; {
		seen[id] = true
		l, ok := byID[id]
		if !ok {
			break
		}
		chain = append(chain, l)
		id = l.Parent
	}
	slices.Reverse(chain)
	var items []map[string]any
	add := func(name, url string) {
		items = append(items, map[string]any{"@type": "ListItem", "position": len(items) + 1, "name": name, "item": s.cfg.BaseURL + url})
	}
	if s.cfg.SiteName != "" {
		add(s.cfg.SiteName, "/")
	}
	for _, l := range chain {
		add(l.Title, l.URL)
	}
	if len(items) < 2 {
		return nil
	}
	return map[string]any{"@type": "BreadcrumbList", "itemListElement": items}
}
