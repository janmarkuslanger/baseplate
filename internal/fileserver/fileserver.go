// Package fileserver serves static files without directory listings.
package fileserver

import (
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// New returns an http.FileServer for fsys that answers 404 for directories
// without an index.html instead of listing their contents.
func New(fsys fs.FS) http.Handler {
	h := http.FileServerFS(noListing{fsys})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if t, ok := types[strings.ToLower(path.Ext(r.URL.Path))]; ok {
			w.Header().Set("Content-Type", t)
		}
		h.ServeHTTP(w, r)
	})
}

// types are content types missing from Go's built-in table. Without a
// system mime.types file (e.g. in a scratch container) such files would get
// a sniffed type, which is application/octet-stream for many MP4 files.
var types = map[string]string{".mp4": "video/mp4", ".webm": "video/webm"}

type noListing struct{ fs.FS }

func (n noListing) Open(name string) (fs.File, error) {
	f, err := n.FS.Open(name)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !st.IsDir() {
		return f, nil
	}
	index, err := n.FS.Open(path.Join(name, "index.html"))
	if err != nil {
		f.Close()
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fs.ErrNotExist
		}
		return nil, err
	}
	index.Close()
	return f, nil
}
