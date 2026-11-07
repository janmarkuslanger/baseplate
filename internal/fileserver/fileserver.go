// Package fileserver serves static files without directory listings.
package fileserver

import (
	"errors"
	"io/fs"
	"net/http"
	"path"
)

// New returns an http.FileServer for fsys that answers 404 for directories
// without an index.html instead of listing their contents.
func New(fsys fs.FS) http.Handler {
	return http.FileServerFS(noListing{fsys})
}

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
