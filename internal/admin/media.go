package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/janmarkuslanger/baseplate/i18n"
	"github.com/janmarkuslanger/baseplate/internal/media"
	"github.com/janmarkuslanger/baseplate/settings"
)

// mediaView is the media page's data.
type mediaView struct {
	Files []media.File
	// SizeMB and VideoMB are the upload limits of images and PDFs, and of
	// videos.
	SizeMB, VideoMB int64
}

func (a *Admin) mediaPage(w http.ResponseWriter, r *http.Request) {
	st, err := a.mediaStore(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	files, err := st.List()
	if err != nil {
		a.fail(w, err)
		return
	}
	var flash string
	if k := i18n.Key(r.URL.Query().Get("error")); uploadErrors[k] {
		flash = a.uploadText(st, k)
	}
	size, video := st.Limits()
	page := mediaView{Files: files, SizeMB: size >> 20, VideoMB: video >> 20}
	a.render(w, r, http.StatusOK, a.pages["media"], view{Title: a.t("nav.media"), Active: "media", Flash: flash, Page: page})
}

// mediaStore returns the media library with the upload limits of the
// published settings.
func (a *Admin) mediaStore(ctx context.Context) (media.Store, error) {
	cfg, err := settings.Load(ctx, a.store())
	if err != nil {
		return media.Store{}, fmt.Errorf("load settings: %w", err)
	}
	st := a.Media
	st.MaxSize, st.MaxVideoSize = cfg.MediaMaxSize, cfg.MediaMaxVideoSize
	return st, nil
}

// uploadErrors are the messages the upload redirect may show on the media
// page; the query carries the key, so links cannot show arbitrary text.
var uploadErrors = map[i18n.Key]bool{
	errNoFile: true, media.ErrType: true, media.ErrSize: true, media.ErrContent: true,
}

const errNoFile = i18n.Key("media.error.noFile")

// uploadText is the message of the upload error k; the size error names
// st's limits.
func (a *Admin) uploadText(st media.Store, k i18n.Key) string {
	size, video := st.Limits()
	return a.t(string(k), size>>20, video>>20)
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

// uploadOverhead is room for the multipart framing and the CSRF field on
// top of the uploaded file.
const uploadOverhead = 1 << 20

// maxUploadBody is the request size of the largest upload the settings can
// allow. It replaces auth.MaxBodySize on the upload route; limitUpload
// applies the limit of the current settings.
const maxUploadBody = settings.MaxMediaMB<<20 + uploadOverhead

// limitUpload caps upload requests at the largest file the settings allow
// plus uploadOverhead. A request that announces a larger body is answered
// without reading it; one without a length stops being read at the cap.
// Reading and answering may take uploadTime of the cap. It runs before
// authentication and reads only the settings.
func (a *Admin) limitUpload(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st, err := a.mediaStore(r.Context())
		if err != nil {
			a.fail(w, err)
			return
		}
		size, video := st.Limits()
		limit := max(size, video) + uploadOverhead
		if r.ContentLength > limit {
			a.uploadError(w, r, st, media.ErrSize)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		// Not every ResponseWriter supports deadlines (httptest); the
		// server's timeouts apply then.
		rc := http.NewResponseController(w)
		deadline := time.Now().Add(uploadTime(limit))
		rc.SetReadDeadline(deadline)
		rc.SetWriteDeadline(deadline)
		next.ServeHTTP(w, r)
	})
}

// minUploadRate is the slowest connection, in bytes per second, that still
// gets a file of the limit uploaded: 256 KB/s, about 2 Mbit/s.
const minUploadRate = 256 << 10

// uploadTime is how long reading and answering an upload of n bytes may
// take: n at minUploadRate, but at least the 5 minutes the server allows
// other requests. It replaces the server's read and write timeouts.
func uploadTime(n int64) time.Duration {
	return max(5*time.Minute, time.Duration(n/minUploadRate)*time.Second)
}

// uploadError answers a rejected upload: as JSON for the image field,
// otherwise by redirecting to the media page, which shows the message.
func (a *Admin) uploadError(w http.ResponseWriter, r *http.Request, st media.Store, k i18n.Key) {
	if wantsJSON(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": a.uploadText(st, k)})
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
	if err == nil {
		defer file.Close()
	}
	st, serr := a.mediaStore(r.Context())
	if serr != nil {
		a.fail(w, serr)
		return
	}
	if tooLarge := new(http.MaxBytesError); errors.As(err, &tooLarge) {
		a.uploadError(w, r, st, media.ErrSize)
		return
	}
	if err != nil {
		a.uploadError(w, r, st, errNoFile)
		return
	}
	url, err := st.Save(file, hdr.Filename)
	if err != nil {
		var k i18n.Key
		if errors.As(err, &k) && uploadErrors[k] {
			a.uploadError(w, r, st, k)
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
