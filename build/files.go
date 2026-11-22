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
// source directory means there is nothing to copy. Media files are hard
// links to the library where the file system allows it (see linkTree).
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
		if err := linkTree(b.MediaDir, filepath.Join(out, "media"), isTemp); err != nil {
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

// linkTree puts the files of the directory src into dst as hard links,
// leaving out paths (slash-separated, relative to src) for which skip
// reports true. A file that cannot be linked (another file system, no
// hard links) is copied instead. Linking makes the media library cost
// almost nothing per build, whatever its size. The library never changes
// a file in place (uploads are renamed into it, deletes unlink), so a
// link keeps the content the build saw. A missing src links nothing; a
// file that disappears meanwhile is left out, as in copyFS.
func linkTree(src, dst string, skip func(p string) bool) error {
	if _, err := os.Stat(src); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p != src && errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if skip != nil && rel != "." && skip(filepath.ToSlash(rel)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		// A symlink is copied with the content it points to, as before.
		if d.Type().IsRegular() {
			if err := os.Link(p, target); err == nil {
				return nil
			} else if errors.Is(err, fs.ErrNotExist) {
				return nil // deleted meanwhile
			}
		}
		in, err := os.Open(p)
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
