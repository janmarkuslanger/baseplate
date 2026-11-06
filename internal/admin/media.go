package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/janmarkuslanger/baseplate/internal/media"
)

func (a *Admin) mediaPage(w http.ResponseWriter, r *http.Request) {
	files, err := a.Media.List()
	if err != nil {
		a.fail(w, err)
		return
	}
	a.render(w, r, http.StatusOK, a.pages["media"], view{Title: a.t("nav.media"), Active: "media", Flash: r.URL.Query().Get("error"), Page: files})
}

func (a *Admin) mediaJSON(w http.ResponseWriter, r *http.Request) {
	files, err := a.Media.List()
	if err != nil {
		a.fail(w, err)
		return
	}
	if files == nil {
		files = []media.File{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(files)
}

func (a *Admin) upload(w http.ResponseWriter, r *http.Request) {
	wantsJSON := strings.Contains(r.Header.Get("Accept"), "application/json")
	respondErr := func(msg string) {
		if wantsJSON {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": msg})
			return
		}
		http.Redirect(w, r, "/admin/media?error="+urlQueryEscape(msg), http.StatusSeeOther)
	}
	file, hdr, err := r.FormFile("file")
	if r.MultipartForm != nil {
		// The server only cleans up the original request; this handler gets
		// a copy (context wrapper), so remove parsed temp files here.
		defer r.MultipartForm.RemoveAll()
	}
	if err != nil {
		respondErr(a.t("media.error.noFile"))
		return
	}
	defer file.Close()
	url, err := a.Media.Save(file, hdr.Filename)
	if err != nil {
		if errors.Is(err, media.ErrType) || errors.Is(err, media.ErrSize) || errors.Is(err, media.ErrContent) {
			respondErr(a.tr(err))
			return
		}
		a.fail(w, err)
		return
	}
	if wantsJSON {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"url": url})
		return
	}
	http.Redirect(w, r, "/admin/media", http.StatusSeeOther)
}
