// Package devreload reloads pages in the browser when the theme directory
// changes (`serve --dev`).
package devreload

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"io/fs"
	"net/http"
	"path/filepath"
)

// Path is where Handler is mounted.
const Path = "/__dev/theme"

// script polls Path and reloads the page once the theme version changed.
const script = `<script>(function(){var v;function p(){fetch("` + Path + `",{cache:"no-store"}).then(function(r){return r.text()}).then(function(t){if(v===undefined)v=t;else if(t!==v)location.reload()}).catch(function(){}).finally(function(){setTimeout(p,500)})}p()})()</script>`

// Inject adds the reload script before </body>, or at the end.
func Inject(html []byte) []byte {
	if i := bytes.LastIndex(html, []byte("</body>")); i >= 0 {
		return append(html[:i:i], append([]byte(script), html[i:]...)...)
	}
	return append(html, script...)
}

// Handler returns a version string of dir that changes when a file is
// added, removed, renamed or modified.
func Handler(dir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := fnv.New64a()
		filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				return nil
			}
			fmt.Fprintf(h, "%s|%d|%d\n", p, fi.Size(), fi.ModTime().UnixNano())
			return nil
		})
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprintf(w, "%x", h.Sum64())
	})
}
