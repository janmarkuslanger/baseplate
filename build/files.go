package build

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/janmarkuslanger/baseplate/i18n"
)

// copyAssets copies the theme's "assets/" and the media library.
func (b *Builder) copyAssets(out string) error {
	if b.Theme != nil {
		if sub, err := fs.Sub(b.Theme, "assets"); err == nil {
			if err := copyFS(sub, filepath.Join(out, "assets")); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	if b.MediaDir != "" {
		if err := copyFS(os.DirFS(b.MediaDir), filepath.Join(out, "media")); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
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
		return copyFile(src, p, target)
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
