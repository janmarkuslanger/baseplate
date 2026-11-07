package build

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/i18n"
	"github.com/janmarkuslanger/baseplate/render"
	"github.com/janmarkuslanger/baseplate/settings"
	"github.com/janmarkuslanger/baseplate/storage"
)

// write renders the site into s.out. The order matters: public files come
// last, so any path written before is detected as a conflict.
func (s *site) write(pages []*render.Page) error {
	for _, p := range pages {
		if err := s.renderPage(p); err != nil {
			return err
		}
	}
	if err := s.outputs(pages); err != nil {
		return err
	}
	if err := s.b.copyAssets(s.out); err != nil {
		return err
	}
	for _, g := range s.b.Reg.Generators() {
		if err := g(s); err != nil {
			return fmt.Errorf("generator: %w", err)
		}
	}
	return s.b.copyPublic(s.out)
}

// site implements plugin.Site.
type site struct {
	ctx context.Context
	b   *Builder
	r   *render.Renderer
	out string
	cfg settings.Settings
	res *resolver // set by collect
}

func (s *site) Context() context.Context { return s.ctx }

func (s *site) Settings() map[string]any { return s.cfg.Data }

func (s *site) Published(typ string) ([]storage.Entry, error) {
	list, _, err := s.b.Reg.Store.ListEntries(s.ctx, storage.ListQuery{Type: typ, Status: storage.StatusPublished})
	return list, err
}

func (s *site) Render(p, tmpl string, data any) error {
	return s.RenderPage(p, tmpl, "-", data)
}

func (s *site) RenderPage(p, tmpl, layout string, data any) error {
	var buf bytes.Buffer
	if err := s.r.ExecutePage(&buf, tmpl, layout, data); err != nil {
		return err
	}
	return s.WriteFile(p, buf.Bytes())
}

func (s *site) WriteFile(p string, data []byte) error {
	clean := path.Clean("/" + p)
	if strings.HasSuffix(p, "/") {
		clean += "/index.html"
	}
	full := filepath.Join(s.out, filepath.FromSlash(clean))
	if _, err := os.Lstat(full); err == nil {
		return i18n.M(MsgPathConflict, clean)
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, data, 0o644)
}

// collect loads all published routable entries and computes their URLs
// and SEO metadata.
func (s *site) collect() ([]*render.Page, error) {
	base := s.cfg.BaseURL
	var pages []*render.Page
	byID := map[refKey]*storage.Entry{}
	for _, t := range s.b.Reg.Schema.Types() {
		if !t.Routable {
			continue
		}
		list, err := s.Published(t.Name)
		if err != nil {
			return nil, err
		}
		for i := range list {
			e := &list[i]
			byID[refKey{t.Name, e.ID}] = e
			pages = append(pages, &render.Page{Site: s.cfg.Data, Entry: *e, Data: e.Data, Type: t})
		}
	}
	seen := map[string]string{}
	var nav []render.Link
	for _, p := range pages {
		u, err := EntryURL(p.Type, &p.Entry, func(id int64) *storage.Entry { return byID[refKey{p.Type.Name, id}] })
		if err != nil {
			return nil, err
		}
		if other, dup := seen[u]; dup {
			return nil, i18n.M("build.duplicateURL", u, other, str(p.Data["title"]))
		}
		seen[u] = str(p.Data["title"])
		p.URL, p.AbsURL = u, base+u
		p.MarkdownURL = markdownURL(u)
		if p.Meta, err = meta(p, s.cfg); err != nil {
			return nil, err
		}
		if p.Type.Name == fields.PageType {
			parent, _ := content.RefID(p.Data["parent"])
			nav = append(nav, render.Link{ID: p.Entry.ID, Parent: parent, Title: str(p.Data["title"]), URL: u})
		}
	}
	// Resolve relations once all URLs are known.
	res := &resolver{s: s, urls: map[refKey]string{}, cache: map[refKey]*render.Ref{}, lists: map[string][]storage.Entry{}}
	s.res = res
	for _, p := range pages {
		res.urls[refKey{p.Type.Name, p.Entry.ID}] = p.URL
	}
	for _, p := range pages {
		p.Data = res.populate(p.Type.Fields, p.Entry.Data)
		res.inverse(p)
	}
	if res.err != nil {
		return nil, fmt.Errorf("resolve relations: %w", res.err)
	}
	slices.SortFunc(nav, func(a, b render.Link) int { return cmp.Compare(a.URL, b.URL) })
	slices.SortFunc(pages, func(a, b *render.Page) int { return cmp.Compare(a.URL, b.URL) })
	for _, p := range pages {
		p.Nav = nav
	}
	return pages, nil
}

func (s *site) renderPage(p *render.Page) error {
	name := p.Type.TemplateName()
	if !s.r.Has(name) && s.r.Has(name+".html") {
		name += ".html"
	}
	// The Markdown variant renders first: a page without one (noindex, or
	// an empty templates/markdown.md) must not link to it.
	var md []byte
	if indexable(p) {
		var err error
		if md, err = s.r.ExecuteOutput(render.PageOutput, p); err != nil {
			return fmt.Errorf("%s: %w", p.URL, err)
		}
	}
	if md == nil {
		p.MarkdownURL = ""
	}
	if err := s.RenderPage(p.URL, name, p.Type.Layout, p); err != nil {
		return fmt.Errorf("%s: %w", p.URL, err)
	}
	if md == nil {
		return nil
	}
	return s.WriteFile(p.MarkdownURL, md)
}

// outputs renders the root output templates (robots.txt, sitemap.xml,
// llms.txt and the theme's own) into the site root.
func (s *site) outputs(pages []*render.Page) error {
	data := render.Output{Site: s.cfg.Data, BaseURL: s.cfg.BaseURL, Pages: pages}
	for _, p := range pages {
		if !indexable(p) {
			continue
		}
		data.Indexable = append(data.Indexable, p)
		// A canonical pointing at the page itself keeps it in the sitemap.
		if seo, _ := p.Data["seo"].(map[string]any); seo["sitemapExclude"] != true && p.Meta.Canonical == p.AbsURL {
			data.Sitemap = append(data.Sitemap, p)
		}
	}
	for _, name := range s.r.Outputs() {
		b, err := s.r.ExecuteOutput(name, data)
		if err != nil {
			return fmt.Errorf("output %s: %w", name, err)
		}
		if b == nil {
			continue
		}
		if err := s.WriteFile(name, b); err != nil {
			return err
		}
	}
	return nil
}
