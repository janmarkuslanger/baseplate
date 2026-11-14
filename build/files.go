package build

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/janmarkuslanger/baseplate/i18n"
)

// uploadTempPrefix names the media store's in-progress uploads
// (internal/media Store.Save); they are not part of the library yet.
const uploadTempPrefix = ".upload-"

// copyAssets copies the theme's "assets/" and the media library. A missing
// source directory means there is nothing to copy.
func (b *Builder) copyAssets(out string) error {
	if b.Theme != nil {
		if sub, err := fs.Sub(b.Theme, "assets"); err == nil {
			if err := copyFS(sub, filepath.Join(out, "assets"), nil); err != nil {
				return err
			}
		}
	}
	if b.MediaDir != "" {
		isTemp := func(p string) bool { return strings.HasPrefix(path.Base(p), uploadTempPrefix) }
		if err := copyFS(os.DirFS(b.MediaDir), filepath.Join(out, "media"), isTemp); err != nil {
			return err
		}
	}
	return nil
}

// copyFS copies src to dst, leaving out paths for which skip reports true.
// A missing src copies nothing. A file that disappears while copying (a
// media file deleted or an upload renamed meanwhile) is left out rather
// than aborting the build; any other error fails it.
func copyFS(src fs.FS, dst string, skip func(p string) bool) error {
	if _, err := fs.Stat(src, "."); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p != "." && errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if skip != nil && skip(p) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := src.Open(p)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		defer in.Close()
		return writeFile(in, target)
	})
}

// copyPublic copies the theme's "public/" and the plugins' public files 1:1
// to the site root. It runs after everything else, so a path that another
// file already uses is caught instead of being overwritten.
func (b *Builder) copyPublic(out string) error {
	srcs := b.Reg.PublicFS()
	if b.Theme != nil {
		if sub, err := fs.Sub(b.Theme, "public"); err == nil {
			srcs = append(srcs, sub)
		}
	}
	for _, src := range srcs {
		if _, err := fs.Stat(src, "."); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		err := fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			target := filepath.Join(out, filepath.FromSlash(p))
			if _, err := os.Lstat(target); err == nil {
				return i18n.M(MsgPublicConflict, "/"+p)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			return copyFile(src, p, target)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src fs.FS, p, target string) error {
	in, err := src.Open(p)
	if err != nil {
		return err
	}
	defer in.Close()
	return writeFile(in, target)
}

func writeFile(in io.Reader, target string) error {
	out, err := os.Create(target)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
