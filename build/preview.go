package build

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/janmarkuslanger/baseplate/render"
	"github.com/janmarkuslanger/baseplate/storage"
)

// ErrPageNotFound means no entry has the requested URL.
var ErrPageNotFound = errors.New("build: no page at this URL")

// ErrNotRoutable means the entry's type has no pages to preview.
var ErrNotRoutable = errors.New("build: content type has no pages")

// Preview renders the page of e, usually a draft, as the site would show
// it with e published: every other entry appears in its published
// version. Nothing is written to DistDir.
func (b *Builder) Preview(ctx context.Context, e *storage.Entry) ([]byte, error) {
	t, ok := b.Reg.Schema.Type(e.Type)
	if !ok || !t.Routable {
		return nil, ErrNotRoutable
	}
	cfg, err := b.settings(ctx)
	if err != nil {
		return nil, err
	}
	s := &site{ctx: ctx, b: b, cfg: cfg, overlay: e}
	pages, err := s.collect()
	if err != nil {
		return nil, err
	}
	if err := s.newRenderer(); err != nil {
		return nil, err
	}
	for _, p := range pages {
		if p.Type.Name != e.Type || p.Entry.ID != e.ID {
			continue
		}
		var buf bytes.Buffer
		if err := s.r.ExecutePage(&buf, s.pageTemplate(p), p.Type.Layout, p); err != nil {
			return nil, fmt.Errorf("%s: %w", p.URL, err)
		}
		return buf.Bytes(), nil
	}
	return nil, fmt.Errorf("build: preview of %s #%d: page missing", e.Type, e.ID)
}

// PreviewPath renders the page at urlPath (e.g. "/" or "/about/") from the
// saved drafts of all entries, for the dev server. Nothing is written to
// DistDir. It returns ErrPageNotFound when no entry has that URL.
func (b *Builder) PreviewPath(ctx context.Context, urlPath string) ([]byte, error) {
	if !strings.HasSuffix(urlPath, "/") {
		urlPath += "/"
	}
	cfg, err := b.settings(ctx)
	if err != nil {
		return nil, err
	}
	s := &site{ctx: ctx, b: b, cfg: cfg, drafts: true}
	pages, err := s.collect()
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(pages, func(p *render.Page) bool { return p.URL == urlPath })
	if i < 0 {
		return nil, ErrPageNotFound
	}
	if err := s.newRenderer(); err != nil {
		return nil, err
	}
	p := pages[i]
	var buf bytes.Buffer
	if err := s.r.ExecutePage(&buf, s.pageTemplate(p), p.Type.Layout, p); err != nil {
		return nil, fmt.Errorf("%s: %w", p.URL, err)
	}
	return buf.Bytes(), nil
}
