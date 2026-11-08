package admin

import (
	"errors"
	"net/http"
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
	w.Write(html)
}
