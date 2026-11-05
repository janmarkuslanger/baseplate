// Package build generates the static site: one HTML file per published
// routable entry, Markdown variants, sitemap.xml, robots.txt and llms.txt.
package build

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/render"
	"github.com/janmarkuslanger/baseplate/settings"
	"github.com/janmarkuslanger/baseplate/storage"
)

type Builder struct {
	Reg      *plugin.Registry
	Theme    fs.FS // may be nil
	DistDir  string
	MediaDir string

	mu sync.Mutex
}

type Result struct {
	Pages    int
	Duration time.Duration
	Finished time.Time
}

// Build renders the site into a temporary directory and swaps it into
// DistDir only on success, so a failed build never leaves a broken site.
// Concurrent calls are serialized.
func (b *Builder) Build(ctx context.Context) (Result, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	start := time.Now()

	r, err := render.New(b.Reg, b.Theme)
	if err != nil {
		return Result{}, err
	}
	cfg, err := settings.Load(ctx, b.Reg.Store)
	if err != nil {
		return Result{}, err
	}
	base := cfg.BaseURL
	if base == "" {
		return Result{}, errors.New("Basis-URL fehlt – bitte in den Einstellungen eintragen")
	}

	// Clean first: with "dist/" the .tmp/.old paths would land inside dist.
	dist := filepath.Clean(b.DistDir)
	tmp := dist + ".tmp"
	os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(tmp)

	s := &site{ctx: ctx, b: b, r: r, out: tmp, cfg: cfg, settings: cfg.Data}
	pages, err := s.collect(base)
	if err != nil {
		return Result{}, err
	}
	for _, p := range pages {
		if err := s.renderPage(p); err != nil {
			return Result{}, err
		}
	}
	steps := []func([]*render.Page) error{s.sitemap, s.robots}
	if cfg.LLMSTxt {
		steps = append(steps, s.llms)
	}
	for _, step := range steps {
		if err := step(pages); err != nil {
			return Result{}, err
		}
	}
	if b.Theme != nil {
		if sub, err := fs.Sub(b.Theme, "assets"); err == nil {
			if err := copyFS(sub, filepath.Join(tmp, "assets")); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return Result{}, err
			}
		}
	}
	if b.MediaDir != "" {
		if err := copyFS(os.DirFS(b.MediaDir), filepath.Join(tmp, "media")); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return Result{}, err
		}
	}
	for _, g := range b.Reg.Generators() {
		if err := g(s); err != nil {
			return Result{}, fmt.Errorf("generator: %w", err)
		}
	}

	old := dist + ".old"
	os.RemoveAll(old)
	if err := os.Rename(dist, old); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Result{}, err
	}
	if err := os.Rename(tmp, dist); err != nil {
		os.Rename(old, dist)
		return Result{}, err
	}
	os.RemoveAll(old)

	if err := plugin.BuildAfter.Run(ctx, b.Reg, &plugin.BuildEvent{DistDir: dist, Pages: len(pages)}); err != nil && b.Reg.Logger != nil {
		b.Reg.Logger.Error("build hook", "err", err)
	}
	return Result{Pages: len(pages), Duration: time.Since(start), Finished: time.Now()}, nil
}

// site implements plugin.Site.
type site struct {
	ctx      context.Context
	b        *Builder
	r        *render.Renderer
	out      string
	cfg      settings.Settings
	settings map[string]any
}

func (s *site) Context() context.Context { return s.ctx }
func (s *site) Settings() map[string]any { return s.settings }

func (s *site) Published(typ string) ([]storage.Entry, error) {
	list, _, err := s.b.Reg.Store.ListEntries(s.ctx, storage.ListQuery{Type: typ, Status: storage.StatusPublished})
	return list, err
}

func (s *site) Render(p, tmpl string, data any) error {
	var buf bytes.Buffer
	if err := s.r.Execute(&buf, tmpl, data); err != nil {
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
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, data, 0o644)
}

// collect loads all published routable entries and computes their URLs
// and SEO metadata.
func (s *site) collect(base string) ([]*render.Page, error) {
	var pages []*render.Page
	byID := map[int64]*storage.Entry{}
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
			if t.Name == fields.PageType {
				byID[e.ID] = e // parent chains only link pages; IDs are per type
			}
			pages = append(pages, &render.Page{Site: s.settings, Entry: *e, Data: e.Data, Type: t})
		}
	}
	seen := map[string]string{}
	var nav []render.Link
	for _, p := range pages {
		u, err := entryURL(&p.Entry, p.Type, byID)
		if err != nil {
			return nil, err
		}
		if other, dup := seen[u]; dup {
			return nil, fmt.Errorf("URL %s ist doppelt vergeben (%s und %q)", u, other, str(p.Data["title"]))
		}
		seen[u] = fmt.Sprintf("%q", str(p.Data["title"]))
		p.URL, p.AbsURL = u, base+u
		p.MarkdownURL = markdownURL(u)
		p.Meta = meta(p, s.cfg, base)
		if p.Type.Name == fields.PageType {
			parent, _ := content.RefID(p.Data["parent"])
			nav = append(nav, render.Link{ID: p.Entry.ID, Parent: parent, Title: str(p.Data["title"]), URL: u})
		}
	}
	// Resolve relations once all URLs are known.
	res := &resolver{s: s, urls: map[refKey]string{}, cache: map[refKey]*render.Ref{}, lists: map[string][]storage.Entry{}}
	for _, p := range pages {
		res.urls[refKey{p.Type.Name, p.Entry.ID}] = p.URL
	}
	for _, p := range pages {
		p.Data = res.populate(p.Type.Fields, p.Entry.Data)
		res.inverse(p)
	}
	sort.Slice(nav, func(i, j int) bool { return nav[i].URL < nav[j].URL })
	sort.Slice(pages, func(i, j int) bool { return pages[i].URL < pages[j].URL })
	for _, p := range pages {
		p.Nav = nav
	}
	return pages, nil
}

// entryURL builds "/parent/child/" for pages (slug "index" is "/") and
// "/<type>/<slug>/" for other routable types.
func entryURL(e *storage.Entry, t *content.Type, byID map[int64]*storage.Entry) (string, error) {
	if t.Name != fields.PageType {
		return "/" + t.Name + "/" + e.Slug + "/", nil
	}
	var parts []string
	cur := e
	for depth := 0; cur != nil; depth++ {
		if depth > 20 {
			return "", fmt.Errorf("Seite %q: zyklische Eltern-Beziehung", str(e.Data["title"]))
		}
		if !(cur == e && cur.Slug == "index") {
			parts = append([]string{cur.Slug}, parts...)
		}
		pid, _ := content.RefID(cur.Data["parent"])
		if pid == 0 {
			break
		}
		// An unpublished parent leaves the child at its own level.
		cur = byID[pid]
	}
	if len(parts) == 0 {
		return "/", nil
	}
	return "/" + strings.Join(parts, "/") + "/", nil
}

func markdownURL(u string) string {
	if u == "/" {
		return "/index.md"
	}
	return strings.TrimSuffix(u, "/") + ".md"
}

func meta(p *render.Page, cfg settings.Settings, base string) render.Meta {
	seo, _ := p.Data["seo"].(map[string]any)
	title := str(p.Data["title"])
	m := render.Meta{
		Title:       firstNonEmpty(str(seo["metaTitle"]), title),
		Description: str(seo["metaDescription"]),
		Canonical:   firstNonEmpty(str(seo["canonical"]), p.AbsURL),
	}
	if site := cfg.SiteName; site != "" && str(seo["metaTitle"]) == "" && title != site {
		m.Title = title + " | " + site
	}
	robots := []string{"index", "follow"}
	if seo["noindex"] == true {
		robots[0] = "noindex"
	}
	if seo["nofollow"] == true {
		robots[1] = "nofollow"
	}
	m.Robots = strings.Join(robots, ", ")
	m.OGTitle = firstNonEmpty(str(seo["ogTitle"]), m.Title)
	m.OGDescription = firstNonEmpty(str(seo["ogDescription"]), m.Description)
	m.OGImage = absolute(base, firstNonEmpty(str(seo["ogImage"]), cfg.DefaultOGImage))
	return m
}

func (s *site) renderPage(p *render.Page) error {
	name := p.Type.TemplateName()
	if !s.r.Has(name) && s.r.Has(name+".html") {
		name += ".html"
	}
	if err := s.Render(p.URL, name, p); err != nil {
		return fmt.Errorf("%s: %w", p.URL, err)
	}
	return s.WriteFile(p.MarkdownURL, []byte(s.r.Markdown(p)))
}

func indexable(p *render.Page) bool {
	seo, _ := p.Data["seo"].(map[string]any)
	return seo["noindex"] != true
}

func (s *site) sitemap(pages []*render.Page) error {
	type url struct {
		Loc      string `xml:"loc"`
		LastMod  string `xml:"lastmod"`
		Priority string `xml:"priority,omitempty"`
	}
	type urlset struct {
		XMLName xml.Name `xml:"urlset"`
		NS      string   `xml:"xmlns,attr"`
		URLs    []url    `xml:"url"`
	}
	set := urlset{NS: "http://www.sitemaps.org/schemas/sitemap/0.9"}
	for _, p := range pages {
		seo, _ := p.Data["seo"].(map[string]any)
		if !indexable(p) || seo["sitemapExclude"] == true || str(seo["canonical"]) != "" {
			continue
		}
		set.URLs = append(set.URLs, url{Loc: p.AbsURL, LastMod: p.Entry.UpdatedAt.UTC().Format("2006-01-02"), Priority: str(seo["sitemapPriority"])})
	}
	out, err := xml.MarshalIndent(set, "", "  ")
	if err != nil {
		return err
	}
	return s.WriteFile("sitemap.xml", append([]byte(xml.Header), out...))
}

func (s *site) robots(_ []*render.Page) error {
	base := s.cfg.BaseURL
	return s.WriteFile("robots.txt", []byte("User-agent: *\nAllow: /\n\nSitemap: "+base+"/sitemap.xml\n"))
}

// llms writes /llms.txt following https://llmstxt.org.
func (s *site) llms(pages []*render.Page) error {
	var b strings.Builder
	b.WriteString("# " + firstNonEmpty(s.cfg.SiteName, "Website") + "\n\n")
	if extra := s.cfg.LLMSContent; extra != "" {
		b.WriteString(extra + "\n\n")
	}
	b.WriteString("## Seiten\n\n")
	for _, p := range pages {
		if !indexable(p) {
			continue
		}
		line := "- [" + str(p.Data["title"]) + "](" + absolute(s.cfg.BaseURL, p.MarkdownURL) + ")"
		if sum := p.Meta.Description; sum != "" {
			line += ": " + strings.ReplaceAll(sum, "\n", " ")
		}
		b.WriteString(line + "\n")
	}
	return s.WriteFile("llms.txt", []byte(b.String()))
}

func copyFS(src fs.FS, dst string) error {
	return fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dst, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := src.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}

func absolute(base, u string) string {
	if u == "" || strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		return u
	}
	return base + u
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func str(v any) string { s, _ := v.(string); return s }
