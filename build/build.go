// Package build generates the static site: one HTML file per published
// routable entry, the output templates (Markdown variants, sitemap.xml,
// robots.txt, llms.txt, see render.Output) and the public files (theme
// "public/", plugin.Registry.Public).
package build

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/i18n"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/render"
	"github.com/janmarkuslanger/baseplate/settings"
	"github.com/janmarkuslanger/baseplate/storage"
)

// Builder renders the static site from published content into DistDir.
type Builder struct {
	Reg      *plugin.Registry
	Theme    fs.FS // may be nil
	DistDir  string
	MediaDir string

	mu sync.Mutex
}

// ErrBaseURLMissing means the settings have no base URL yet.
var ErrBaseURLMissing = i18n.Key("build.baseURLMissing")

// ErrBaseURLInvalid means the base URL is not an absolute http(s) URL
// (saved before the admin validated it).
var ErrBaseURLInvalid = i18n.Key("build.baseURLInvalid")

// MsgPublicConflict is the key of the error for a public file whose path the
// build already uses (argument: the path).
const MsgPublicConflict = "build.publicConflict"

// MsgPathConflict is the key of the error for a file the build writes
// twice, e.g. a generator writing robots.txt (argument: the path). Replace
// a core output with a template in templates/output/ instead.
const MsgPathConflict = "build.pathConflict"

// Result describes a successful build.
type Result struct {
	Pages    int
	Duration time.Duration
	Finished time.Time
}

// Build renders the site into a temporary directory and swaps it into
// DistDir only on success, so a failed build never leaves a broken site.
// Concurrent calls are serialized, across processes too (see lockDist).
func (b *Builder) Build(ctx context.Context) (Result, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	start := time.Now()

	dist, err := distTarget(b.DistDir)
	if err != nil {
		return Result{}, err
	}
	unlock, err := lockDist(ctx, dist)
	if err != nil {
		return Result{}, err
	}
	defer unlock()

	cfg, err := settings.Load(ctx, b.Reg.Store)
	if err != nil {
		return Result{}, err
	}
	if cfg.BaseURL == "" {
		return Result{}, ErrBaseURLMissing
	}
	if !settings.ValidBaseURL(cfg.BaseURL) {
		return Result{}, ErrBaseURLInvalid
	}

	tmp := dist + ".tmp"
	if err := os.RemoveAll(tmp); err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(tmp)

	s := &site{ctx: ctx, b: b, out: tmp, cfg: cfg}
	pages, err := s.collect()
	if err != nil {
		return Result{}, err
	}
	s.r, err = render.New(b.Reg, b.Theme, render.Options{Entries: s.entries, Lang: cfg.Language, Location: cfg.Location})
	if err != nil {
		return Result{}, err
	}
	if err := s.write(pages); err != nil {
		return Result{}, err
	}
	if err := swap(tmp, dist); err != nil {
		return Result{}, err
	}

	if err := plugin.BuildAfter.Run(ctx, b.Reg, &plugin.BuildEvent{DistDir: filepath.Clean(b.DistDir), Pages: len(pages)}); err != nil && b.Reg.Logger != nil {
		b.Reg.Logger.Error("build hook", "err", err)
	}
	return Result{Pages: len(pages), Duration: time.Since(start), Finished: time.Now()}, nil
}

// EntryURL builds an entry's URL from its type's path pattern. parent
// returns a published entry of the same type by ID, or nil; an unpublished
// or missing parent ends the {parents} chain.
func EntryURL(t *content.Type, e *storage.Entry, parent func(id int64) *storage.Entry) (string, error) {
	var parents []string
	if strings.Contains(t.PathPattern(), "{parents}") {
		seen := map[int64]bool{e.ID: true}
		for cur := e; ; {
			pid, _ := content.RefID(cur.Data["parent"])
			if pid == 0 {
				break
			}
			if seen[pid] {
				return "", i18n.M("build.parentCycle", str(e.Data[t.Title()]))
			}
			seen[pid] = true
			if cur = parent(pid); cur == nil {
				break
			}
			parents = append([]string{cur.Slug}, parents...)
		}
	}
	return t.URL(e.Slug, parents), nil
}

func str(v any) string { s, _ := v.(string); return s }
