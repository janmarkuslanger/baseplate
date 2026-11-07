package build

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/storage"
)

func countPages(t *testing.T, dist string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(dist, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Name() == "index.html" {
			n++
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// Two Builders on one DistDir stand in for two processes ("serve" with
// auto-build and a cron "build"): neither may publish a partial site.
func TestConcurrentBuildersOnOneDistDir(t *testing.T) {
	b1, st := setup(t)
	add(t, st, fields.SettingsType, fields.SettingsType, storage.StatusPublished, map[string]any{"baseURL": "https://a.test"})
	const pages = 150
	for i := range pages {
		add(t, st, fields.PageType, fmt.Sprintf("p%d", i), storage.StatusPublished, map[string]any{"title": "P"})
	}
	b2 := &Builder{Reg: b1.Reg, Theme: b1.Theme, DistDir: b1.DistDir, MediaDir: b1.MediaDir}

	for round := range 5 {
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i, b := range []*Builder{b1, b2} {
			wg.Go(func() { _, errs[i] = b.Build(context.Background()) })
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				t.Fatalf("round %d: %v", round, err)
			}
		}
		if n := countPages(t, b1.DistDir); n != pages {
			t.Fatalf("round %d: %d pages published, want %d", round, n, pages)
		}
	}
}

func TestBuildWaitingForLockHonorsContext(t *testing.T) {
	b, st := setup(t)
	add(t, st, fields.SettingsType, fields.SettingsType, storage.StatusPublished, map[string]any{"baseURL": "https://a.test"})
	dist, err := distTarget(b.DistDir)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := lockDist(context.Background(), dist)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 4*lockRetry)
	defer cancel()
	if _, err := b.Build(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want to wait for the lock until the deadline, got %v", err)
	}
	if _, err := os.Stat(b.DistDir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("build wrote %s while another build held the lock", b.DistDir)
	}
}

func TestBuildIntoSymlinkedDistDir(t *testing.T) {
	b, st := setup(t)
	add(t, st, fields.SettingsType, fields.SettingsType, storage.StatusPublished, map[string]any{"baseURL": "https://a.test"})
	add(t, st, fields.PageType, "index", storage.StatusPublished, map[string]any{"title": "Eins"})
	www := filepath.Join(t.TempDir(), "www")
	if err := os.Mkdir(www, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(www, b.DistDir); err != nil {
		t.Fatal(err)
	}
	for range 2 { // the second build replaces the first one's site behind the link
		if _, err := b.Build(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if target, err := os.Readlink(b.DistDir); err != nil || target != www {
		t.Fatalf("dist dir is no longer a symlink to %s: %q %v", www, target, err)
	}
	if html, err := os.ReadFile(filepath.Join(www, "index.html")); err != nil || !strings.Contains(string(html), "Eins") {
		t.Fatalf("site not written into the link target: %q %v", html, err)
	}
}

func TestInvalidDistDirRejectedBeforeRendering(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(dir, "work")
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(work)
	for _, c := range []struct{ dist, want string }{
		{".", "must not be the working directory"},
		{"..", "must not be the working directory"},
		{"/", "must not be the working directory"},
		{file, "is not a directory"},
	} {
		b, st := setup(t)
		add(t, st, fields.SettingsType, fields.SettingsType, storage.StatusPublished, map[string]any{"baseURL": "https://a.test"})
		add(t, st, fields.PageType, "index", storage.StatusPublished, map[string]any{"title": "A"})
		b.DistDir = c.dist
		if _, err := b.Build(context.Background()); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("DistDir %q: want %q, got %v", c.dist, c.want, err)
		}
	}
	if entries, _ := os.ReadDir(work); len(entries) != 0 {
		t.Errorf("rejected builds wrote into the working directory: %v", entries)
	}
}
