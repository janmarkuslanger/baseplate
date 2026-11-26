package build

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

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
	if err := s.renderPages(pages); err != nil {
		return err
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
	// overlay is an entry shown in place of its published version (a
	// previewed draft); nil in a build.
	overlay *storage.Entry
	// drafts shows the draft of every entry instead of its published
	// version (PreviewPath).
	drafts bool

	// resMu guards res while pages render in parallel ("entries").
	resMu sync.Mutex
	// mu guards dirs, the directories created below out.
	mu   sync.Mutex
	dirs map[string]bool
}

func (s *site) newRenderer() error {
	r, err := render.New(s.b.Reg, s.b.Theme, render.Options{Context: s.ctx, Entries: s.entries, Lang: s.cfg.Language, Location: s.cfg.Location})
	s.r = r
	return err
}

func (s *site) Context() context.Context { return s.ctx }

func (s *site) Settings() map[string]any { return s.cfg.Data }

func (s *site) Published(typ string) ([]storage.Entry, error) {
	version := storage.Published
	if s.drafts {
		version = storage.Draft
	}
	list, _, err := s.b.Reg.Store.ListEntries(s.ctx, storage.ListQuery{Type: typ, Version: version})
	if err != nil || s.overlay == nil || s.overlay.Type != typ {
		return list, err
	}
	if i := slices.IndexFunc(list, func(e storage.Entry) bool { return e.ID == s.overlay.ID }); i >= 0 {
		list[i] = *s.overlay
	} else {
		list = append(list, *s.overlay)
	}
	return list, nil
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

// WriteFile is safe for concurrent use. Every path is written once: the
// file is created exclusively (O_EXCL), so the file system decides what
// counts as the same path, including case-insensitive ones (macOS,
// Windows) and files copied before (assets, media).
func (s *site) WriteFile(p string, data []byte) error {
	clean := path.Clean("/" + p)
	if strings.HasSuffix(p, "/") {
		clean = path.Join(clean, "index.html")
	}
	full := filepath.Join(s.out, filepath.FromSlash(clean))
	if err := s.mkdir(filepath.Dir(full)); err != nil {
		return err
	}
	f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return i18n.M(MsgPathConflict, clean)
	}
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	return errors.Join(err, f.Close())
}

// mkdir creates dir below out once per build; the pages of a build share
// most of their directories.
func (s *site) mkdir(dir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dirs[dir] {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if s.dirs == nil {
		s.dirs = map[string]bool{}
	}
	for d := dir; !s.dirs[d]; d = filepath.Dir(d) {
		s.dirs[d] = true
		if d == s.out {
			break
		}
	}
	return nil
}

// renderPages renders the pages in parallel, one worker per CPU. The error
// returned is the one of the first failing page in order, as if they had
// rendered one after another: workers take pages in order and stop taking
// new ones after a failure, so every page before it has rendered.
func (s *site) renderPages(pages []*render.Page) error {
	errs := make([]error, len(pages))
	var next atomic.Int64
	var failed atomic.Bool
	var wg sync.WaitGroup
	for range min(runtime.GOMAXPROCS(0), len(pages)) {
		wg.Go(func() {
			for !failed.Load() {
				i := int(next.Add(1) - 1)
				if i >= len(pages) {
					return
				}
				err := s.ctx.Err()
				if err == nil {
					err = s.renderPage(pages[i])
				}
				if err != nil {
					errs[i] = err
					failed.Store(true)
				}
			}
		})
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
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
		title := str(p.Data[p.Type.Title()])
		if other, dup := seen[u]; dup {
			return nil, i18n.M("build.duplicateURL", u, other, title)
		}
		seen[u] = title
		p.URL, p.AbsURL = u, base+u
		p.MarkdownURL = markdownURL(u)
		if p.Meta, err = meta(p, s.cfg); err != nil {
			return nil, err
		}
		if p.Type.Name == fields.PageType {
			parent, _ := content.RefID(p.Data["parent"])
			nav = append(nav, render.Link{ID: p.Entry.ID, Parent: parent, Title: title, URL: u})
		}
	}
	// Resolve relations once all URLs are known.
	res := &resolver{s: s, urls: map[refKey]string{}, cache: map[refKey]*render.Ref{}, lists: map[string][]storage.Entry{}, byID: map[refKey]*storage.Entry{}, inv: map[invKey]map[int64][]int64{}}
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

// pageTemplate names the template of a page's type.
func (s *site) pageTemplate(p *render.Page) string {
	name := p.Type.TemplateName()
	if !s.r.Has(name) && s.r.Has(name+".html") {
		name += ".html"
	}
	return name
}

func (s *site) renderPage(p *render.Page) error {
	name := s.pageTemplate(p)
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
