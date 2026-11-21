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

// maxUploadBody is the request size of the largest upload media accepts
// (a video): the file plus room for the multipart framing and the CSRF
// field. Smaller limits per file type are checked by media.Store.Save.
const maxUploadBody = max(media.MaxSize, media.MaxVideoSize) + 1<<20

// limitUpload caps upload requests at maxUploadBody, which replaces the
// general auth.MaxBodySize on this route. A request that announces a larger
// body is answered without reading it; one without a length stops being
// read at the cap. It runs before authentication but neither reads nor
// changes anything.
func (a *Admin) limitUpload(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > maxUploadBody {
			a.uploadError(w, r, media.ErrSize)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxUploadBody)
		next.ServeHTTP(w, r)
	})
}

// uploadError answers a rejected upload: as JSON for the image field,
// otherwise by redirecting to the media page, which shows the message.
func (a *Admin) uploadError(w http.ResponseWriter, r *http.Request, k i18n.Key) {
	if wantsJSON(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": a.tr(k)})
		return
	}
	http.Redirect(w, r, "/admin/media?error="+urlQueryEscape(string(k)), http.StatusSeeOther)
}

func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

func (a *Admin) upload(w http.ResponseWriter, r *http.Request) {
	file, hdr, err := r.FormFile("file")
	if r.MultipartForm != nil {
		// The server only cleans up the original request; this handler gets
		// a copy (context wrapper), so remove parsed temp files here.
		defer r.MultipartForm.RemoveAll()
	}
	if tooLarge := new(http.MaxBytesError); errors.As(err, &tooLarge) {
		a.uploadError(w, r, media.ErrSize)
		return
	}
	if err != nil {
		a.uploadError(w, r, errNoFile)
		return
	}
	defer file.Close()
	url, err := a.Media.Save(file, hdr.Filename)
	if err != nil {
		var k i18n.Key
		if errors.As(err, &k) && uploadErrors[k] {
			a.uploadError(w, r, k)
			return
		}
		a.fail(w, err)
		return
	}
	if wantsJSON(r) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"url": url})
		return
	}
	http.Redirect(w, r, "/admin/media", http.StatusSeeOther)
}
