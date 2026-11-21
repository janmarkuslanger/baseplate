// Package media stores uploaded files in a local directory. Files are
// served under /media/ by the CMS server and copied into the static build.
package media

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/janmarkuslanger/baseplate/i18n"
)

// Default size limits of images and PDFs, and of videos; Store.MaxSize and
// Store.MaxVideoSize replace them.
const (
	DefaultMaxSize      = 20 << 20
	DefaultMaxVideoSize = 100 << 20
)

// allowed lists accepted extensions. SVG is excluded because it can carry
// scripts.
var allowed = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".avif": true,
	".pdf": true, ".mp4": true, ".webm": true,
}

// videos lists the extensions that are videos (limit Store.MaxVideoSize).
var videos = map[string]bool{".mp4": true, ".webm": true}

var (
	ErrType    = i18n.Key("media.error.type")
	ErrContent = i18n.Key("media.error.content")
)

// sniffed maps extensions to the content type http.DetectContentType
// reports for them. AVIF and MP4 are checked by signature: Go does not
// detect AVIF and reports MP4 only for some brands.
var sniffed = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png",
	".gif": "image/gif", ".webp": "image/webp", ".pdf": "application/pdf",
	".webm": "video/webm",
}

// matchesExt reports whether the first bytes of a file fit its extension.
func matchesExt(ext string, head []byte) bool {
	switch ext {
	case ".avif":
		return isoBrand(head) == "avif" || isoBrand(head) == "avis"
	case ".mp4":
		// Any ISO base media file (brands isom, mp41, mp42, avc1, M4V , …)
		// except the AVIF image brands.
		b := isoBrand(head)
		return b != "" && b != "avif" && b != "avis"
	}
	return http.DetectContentType(head) == sniffed[ext]
}

// isoBrand returns the major brand of an ISO base media file (MP4, AVIF):
// a leading "ftyp" box. It is empty for other files.
func isoBrand(head []byte) string {
	if len(head) < 12 || string(head[4:8]) != "ftyp" {
		return ""
	}
	return string(head[8:12])
}

var ErrSize = i18n.Key("media.error.size")

// Store keeps uploads as files in Dir.
type Store struct {
	Dir string
	// MaxSize limits images and PDFs, MaxVideoSize videos, in bytes; zero
	// means DefaultMaxSize and DefaultMaxVideoSize.
	MaxSize, MaxVideoSize int64
}

// Limits returns the size limits of images and PDFs, and of videos.
func (s Store) Limits() (size, video int64) {
	return cmp.Or(s.MaxSize, DefaultMaxSize), cmp.Or(s.MaxVideoSize, DefaultMaxVideoSize)
}

// maxSize returns the size limit of files with extension ext.
func (s Store) maxSize(ext string) int64 {
	size, video := s.Limits()
	if videos[ext] {
		return video
	}
	return size
}

// File is one upload in the media library.
type File struct {
	Name     string
	URL      string
	Size     int64
	Modified time.Time
	IsImage  bool
	IsVideo  bool
}

// Save stores r under a content-addressed name and returns its URL.
// Uploading the same file twice yields the same URL.
func (s Store) Save(r io.Reader, filename string) (string, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	if !allowed[ext] {
		return "", ErrType
	}
	head := make([]byte, 512)
	hn, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", err
	}
	head = head[:hn]
	if !matchesExt(ext, head) {
		return "", ErrContent
	}
	r = io.MultiReader(bytes.NewReader(head), r)
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(s.Dir, ".upload-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	limit := s.maxSize(ext)
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, limit+1))
	tmp.Close()
	if err != nil {
		return "", err
	}
	if n > limit {
		return "", ErrSize
	}
	base := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	name := slugify(base) + "-" + hex.EncodeToString(h.Sum(nil))[:10] + ext
	if err := os.Rename(tmp.Name(), filepath.Join(s.Dir, name)); err != nil {
		return "", err
	}
	return "/media/" + name, nil
}

// List returns all files, newest first.
func (s Store) List() ([]File, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []File
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		out = append(out, File{Name: e.Name(), URL: "/media/" + e.Name(), Size: info.Size(), Modified: info.ModTime(),
			IsImage: ext != ".pdf" && !videos[ext], IsVideo: videos[ext]})
	}
	slices.SortFunc(out, func(a, b File) int { return b.Modified.Compare(a.Modified) }) // newest first
	return out, nil
}

func slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "file"
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}
