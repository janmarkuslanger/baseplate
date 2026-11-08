package build

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/janmarkuslanger/baseplate/storage"
)

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
