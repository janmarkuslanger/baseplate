package admin

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/janmarkuslanger/baseplate/storage"
)

// preview shows the saved draft of a routable entry as the site would
// with it published. Build errors (missing base URL, duplicate URL,
// template error) are shown as text: they are the editor's to fix.
func (a *Admin) preview(w http.ResponseWriter, r *http.Request) {
	t, ok := a.contentType(w, r)
	if !ok {
		return
	}
	if !t.Routable {
		http.NotFound(w, r)
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	e, err := a.store().GetEntry(r.Context(), t.Name, id, "", storage.Draft)
	if errors.Is(err, storage.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	html, err := a.Builder.Preview(r.Context(), e)
	if err != nil {
		a.Logger.Error("preview", "type", t.Name, "id", id, "err", err)
		http.Error(w, a.tr(err), http.StatusUnprocessableEntity)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	if a.DevThemeDir != "" {
		html = injectReload(html)
	}
	w.Write(html)
}

// reloadScript polls devTheme and reloads the page once the theme changed.
const reloadScript = `<script>(function(){var v;function p(){fetch("/admin/dev/theme",{cache:"no-store"}).then(function(r){return r.text()}).then(function(t){if(v===undefined)v=t;else if(t!==v)location.reload()}).catch(function(){}).finally(function(){setTimeout(p,500)})}p()})()</script>`

func injectReload(html []byte) []byte {
	if i := bytes.LastIndex(html, []byte("</body>")); i >= 0 {
		return append(html[:i:i], append([]byte(reloadScript), html[i:]...)...)
	}
	return append(html, reloadScript...)
}

// devTheme returns a version string of the theme directory (newest
// modification time and file count), used by the dev reload script.
func (a *Admin) devTheme(w http.ResponseWriter, r *http.Request) {
	if a.DevThemeDir == "" {
		http.NotFound(w, r)
		return
	}
	var newest int64
	var n int
	filepath.WalkDir(a.DevThemeDir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			n++
			newest = max(newest, fi.ModTime().UnixNano())
		}
		return nil
	})
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, "%d-%d", newest, n)
}
