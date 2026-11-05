// Package media stores uploaded files in a local directory. Files are
// served under /media/ by the CMS server and copied into the static build.
package media

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const MaxSize = 20 << 20

// allowed lists accepted extensions. SVG is excluded because it can carry
// scripts.
var allowed = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".avif": true, ".pdf": true}

var ErrType = errors.New("Dateityp nicht erlaubt (jpg, png, gif, webp, avif, pdf)")
var ErrContent = errors.New("Dateiinhalt passt nicht zur Dateiendung")

// sniffed maps extensions to the content type http.DetectContentType
// reports for them. AVIF is not detected by Go and checked by signature.
var sniffed = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png",
	".gif": "image/gif", ".webp": "image/webp", ".pdf": "application/pdf",
}

// matchesExt reports whether the first bytes of a file fit its extension.
func matchesExt(ext string, head []byte) bool {
	if ext == ".avif" {
		return len(head) >= 12 && string(head[4:8]) == "ftyp" && (string(head[8:12]) == "avif" || string(head[8:12]) == "avis")
	}
	return http.DetectContentType(head) == sniffed[ext]
}

var ErrSize = fmt.Errorf("Datei größer als %d MB", MaxSize>>20)

type Store struct{ Dir string }

type File struct {
	Name     string
	URL      string
	Size     int64
	Modified time.Time
	IsImage  bool
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
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, MaxSize+1))
	tmp.Close()
	if err != nil {
		return "", err
	}
	if n > MaxSize {
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
		out = append(out, File{Name: e.Name(), URL: "/media/" + e.Name(), Size: info.Size(), Modified: info.ModTime(), IsImage: ext != ".pdf"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
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
