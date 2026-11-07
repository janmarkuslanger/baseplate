package build

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// distTarget returns the absolute directory a build replaces. A symlinked
// DistDir is followed, so the link keeps pointing at the new site. A path
// whose replacement would move the working directory or the file system
// root, or that is not a directory, is rejected before anything renders.
func distTarget(dir string) (string, error) {
	dist := filepath.Clean(dir)
	fi, err := os.Lstat(dist)
	switch {
	case err == nil && fi.Mode()&fs.ModeSymlink != 0:
		if dist, err = filepath.EvalSymlinks(dist); err != nil {
			return "", fmt.Errorf("build: dist dir %s: %w", dir, err)
		}
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return "", fmt.Errorf("build: dist dir %s: %w", dir, err)
	}
	abs, err := filepath.Abs(dist)
	if err != nil {
		return "", err
	}
	if fi, err := os.Stat(abs); err == nil && !fi.IsDir() {
		return "", fmt.Errorf("build: dist dir %s is not a directory", dir)
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if filepath.Dir(abs) == abs || within(wd, abs) {
		return "", fmt.Errorf("build: dist dir %s must not be the working directory, one of its parents or the file system root", dir)
	}
	return abs, nil
}

// within reports whether p is dir or below it.
func within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// lockRetry is how often a build waiting for another one checks again.
const lockRetry = 50 * time.Millisecond

// lockDist serializes builds of one dist dir across Builders and processes
// (e.g. "serve" with BASEPLATE_BUILD_ON_PUBLISH and a cron "build"), which
// would otherwise replace each other's temporary output. The lock file
// "<dist>.lock" stays next to dist; the lock itself ends with the process.
func lockDist(ctx context.Context, dist string) (unlock func(), err error) {
	if err := os.MkdirAll(filepath.Dir(dist), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(dist+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	for {
		ok, err := tryLock(f)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("build: lock %s: %w", f.Name(), err)
		}
		if ok {
			return func() { unlockFile(f); f.Close() }, nil
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(lockRetry):
		}
	}
}

// swap replaces dist with tmp. If that fails, the previous site is put back.
func swap(tmp, dist string) error {
	old := dist + ".old"
	if err := os.RemoveAll(old); err != nil {
		return err
	}
	if err := os.Rename(dist, old); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Rename(tmp, dist); err != nil {
		if rerr := os.Rename(old, dist); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			return errors.Join(err, fmt.Errorf("restore previous site: %w", rerr))
		}
		return err
	}
	os.RemoveAll(old) // the new site is live; a leftover is removed by the next build
	return nil
}
