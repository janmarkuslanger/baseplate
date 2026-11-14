package build

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

// vanishingFS lists every file of MapFS but reports gone as deleted on
// Open, like a file removed between reading a directory and copying it.
type vanishingFS struct {
	fstest.MapFS
	gone string
}

func (v vanishingFS) Open(name string) (fs.File, error) {
	if name == v.gone {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return v.MapFS.Open(name)
}

func TestCopyFSSkipsVanishedFile(t *testing.T) {
	src := vanishingFS{MapFS: fstest.MapFS{
		"a.png":     {Data: []byte("a")},
		"b.png":     {Data: []byte("b")},
		"sub/c.png": {Data: []byte("c")},
	}, gone: "a.png"}
	dst := t.TempDir()
	if err := copyFS(src, dst, nil); err != nil {
		t.Fatalf("a file vanishing mid-copy must not abort the copy: %v", err)
	}
	for _, p := range []string{"b.png", "sub/c.png"} {
		if _, err := os.Stat(filepath.Join(dst, p)); err != nil {
			t.Errorf("%s not copied: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "a.png")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("vanished file must not be copied: %v", err)
	}
}

func TestCopyAssetsMedia(t *testing.T) {
	b, _ := setup(t)
	if err := os.MkdirAll(b.MediaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"photo-0123456789.png", ".upload-42"} {
		if err := os.WriteFile(filepath.Join(b.MediaDir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := t.TempDir()
	if err := b.copyAssets(out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "media", "photo-0123456789.png")); err != nil {
		t.Errorf("media file not copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "media", ".upload-42")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("in-progress upload must not be copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "assets", "site.css")); err != nil {
		t.Errorf("theme asset not copied: %v", err)
	}
}

func TestCopyAssetsMissingDirs(t *testing.T) {
	b, _ := setup(t) // MediaDir does not exist
	b.Theme = fstest.MapFS{"templates/page.html": {Data: []byte(`x`)}}
	if err := b.copyAssets(t.TempDir()); err != nil {
		t.Fatalf("missing assets/ and media dir mean nothing to copy: %v", err)
	}
}
