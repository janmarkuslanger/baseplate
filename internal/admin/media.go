package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/janmarkuslanger/baseplate/i18n"
	"github.com/janmarkuslanger/baseplate/internal/media"
)

func (a *Admin) mediaPage(w http.ResponseWriter, r *http.Request) {
	files, err := a.Media.List()
	if err != nil {
		a.fail(w, err)
		return
	}
	var flash string
	if k := i18n.Key(r.URL.Query().Get("error")); uploadErrors[k] {
		flash = a.tr(k)
	}
	a.render(w, r, http.StatusOK, a.pages["media"], view{Title: a.t("nav.media"), Active: "media", Flash: flash, Page: files})
}

// uploadErrors are the messages the upload redirect may show on the media
// page; the query carries the key, so links cannot show arbitrary text.
var uploadErrors = map[i18n.Key]bool{
	errNoFile: true, media.ErrType: true, media.ErrSize: true, media.ErrContent: true,
}

const errNoFile = i18n.Key("media.error.noFile")

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
	respondErr := func(k i18n.Key) {
		if wantsJSON {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": a.tr(k)})
			return
		}
		http.Redirect(w, r, "/admin/media?error="+urlQueryEscape(string(k)), http.StatusSeeOther)
	}
	file, hdr, err := r.FormFile("file")
	if r.MultipartForm != nil {
		// The server only cleans up the original request; this handler gets
		// a copy (context wrapper), so remove parsed temp files here.
		defer r.MultipartForm.RemoveAll()
	}
	if err != nil {
		respondErr(errNoFile)
		return
	}
	defer file.Close()
	url, err := a.Media.Save(file, hdr.Filename)
	if err != nil {
		var k i18n.Key
		if errors.As(err, &k) && uploadErrors[k] {
			respondErr(k)
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
