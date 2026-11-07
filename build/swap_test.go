package build

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
)

func writeSite(t *testing.T, dir, version string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(version), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A web server serving dist (or the preview at /) must always find a
// site: replacing it may never leave a moment without dist.
func TestSwapNeverLeavesDistMissing(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("no atomic exchange on " + runtime.GOOS + "; renameSwap leaves a short gap")
	}
	root := t.TempDir()
	dist, tmp := filepath.Join(root, "dist"), filepath.Join(root, "dist.tmp")
	writeSite(t, dist, "v0")

	var (
		stop    atomic.Bool
		missing atomic.Int64
		wg      sync.WaitGroup
	)
	wg.Go(func() {
		for !stop.Load() {
			if _, err := os.Stat(filepath.Join(dist, "index.html")); err != nil {
				missing.Add(1)
			}
		}
	})
	const swaps = 2000
	var err error
	for i := 1; i <= swaps && err == nil; i++ {
		writeSite(t, tmp, "v"+strconv.Itoa(i))
		err = swap(tmp, dist)
		os.RemoveAll(tmp) // as Build does: tmp may now hold the previous site
	}
	stop.Store(true)
	wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if n := missing.Load(); n > 0 {
		t.Errorf("dist/index.html was missing %d times during %d swaps", n, swaps)
	}
	if b, err := os.ReadFile(filepath.Join(dist, "index.html")); err != nil || string(b) != "v"+strconv.Itoa(swaps) {
		t.Errorf("dist holds %q (%v) after the last swap", b, err)
	}
	if _, err := os.Stat(dist + ".old"); !os.IsNotExist(err) {
		t.Errorf("dist.old left behind (%v)", err)
	}
}

// The first build has no previous site to exchange with.
func TestSwapWithoutPreviousSite(t *testing.T) {
	root := t.TempDir()
	dist, tmp := filepath.Join(root, "dist"), filepath.Join(root, "dist.tmp")
	writeSite(t, tmp, "v1")
	if err := swap(tmp, dist); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dist, "index.html")); err != nil || string(b) != "v1" {
		t.Errorf("dist holds %q (%v)", b, err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("tmp still exists (%v)", err)
	}
}

// Without an atomic exchange (other systems, file systems without
// support), the two-rename fallback still replaces the site completely.
func TestRenameSwapFallback(t *testing.T) {
	root := t.TempDir()
	dist, tmp := filepath.Join(root, "dist"), filepath.Join(root, "dist.tmp")
	writeSite(t, dist, "v1")
	writeSite(t, tmp, "v2")
	if err := renameSwap(tmp, dist); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dist, "index.html")); err != nil || string(b) != "v2" {
		t.Errorf("dist holds %q (%v)", b, err)
	}
	for _, p := range []string{tmp, dist + ".old"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s left behind (%v)", p, err)
		}
	}
}
