package tests

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/janmarkuslanger/baseplate/internal/media"
)

// countingReader yields n zero bytes and counts how many were read.
type countingReader struct{ n, read int64 }

func (r *countingReader) Read(p []byte) (int, error) {
	if r.read >= r.n {
		return 0, io.EOF
	}
	p = p[:min(int64(len(p)), r.n-r.read)]
	clear(p)
	r.read += int64(len(p))
	return len(p), nil
}

// oversizedUpload returns a multipart upload of a PNG twice the media limit
// and the counter of its file bytes.
func oversizedUpload(csrf string) (io.Reader, *countingReader, string, int64) {
	return pngUpload(csrf, 2*media.MaxSize)
}

// pngUpload returns a multipart upload of a PNG of size bytes, the counter
// of its bytes after the signature, the content type and the body length.
func pngUpload(csrf string, size int64) (io.Reader, *countingReader, string, int64) {
	var head bytes.Buffer
	mw := multipart.NewWriter(&head)
	if csrf != "" {
		mw.WriteField("csrf", csrf)
	}
	fw, _ := mw.CreateFormFile("file", "big.png")
	sig := "\x89PNG\r\n\x1a\n"
	fw.Write([]byte(sig))
	tail := "\r\n--" + mw.Boundary() + "--\r\n"
	file := &countingReader{n: size - int64(len(sig))}
	total := int64(head.Len()) + file.n + int64(len(tail))
	return io.MultiReader(&head, file, strings.NewReader(tail)), file, mw.FormDataContentType(), total
}

// An upload larger than the media limit is rejected with the size error
// before the server has read (and spooled) all of it.
func TestOversizedUploadIsRejectedEarly(t *testing.T) {
	_, c := newTestApp(t)
	c.login()
	u, _ := url.Parse(c.srv.URL)
	cookies := c.http.Jar.Cookies(u)
	h := c.srv.Config.Handler

	t.Run("form", func(t *testing.T) {
		body, file, ctype, size := oversizedUpload(c.csrf)
		req := httptest.NewRequest("POST", "/admin/media", body)
		req.ContentLength = size
		req.Header.Set("Content-Type", ctype)
		for _, ck := range cookies {
			req.AddCookie(ck)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if loc := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || loc != "/admin/media?error=media.error.size" {
			t.Fatalf("got %d %q, want a redirect with the size error", rec.Code, loc)
		}
		if file.read > 0 {
			t.Errorf("read %d bytes of a body whose length already exceeds the limit", file.read)
		}
	})

	t.Run("file at the limit", func(t *testing.T) {
		body, _, ctype, size := pngUpload(c.csrf, media.MaxSize)
		req := httptest.NewRequest("POST", "/admin/media", body)
		req.ContentLength = size
		req.Header.Set("Content-Type", ctype)
		for _, ck := range cookies {
			req.AddCookie(ck)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if loc := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || loc != "/admin/media" {
			t.Fatalf("got %d %q, want the upload accepted", rec.Code, loc)
		}
	})

	t.Run("json without length", func(t *testing.T) {
		body, file, ctype, _ := oversizedUpload("")
		req := httptest.NewRequest("POST", "/admin/media", body)
		req.ContentLength = -1
		req.Header.Set("Content-Type", ctype)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-CSRF-Token", c.csrf)
		for _, ck := range cookies {
			req.AddCookie(ck)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out struct{ Error string }
		json.Unmarshal(rec.Body.Bytes(), &out)
		if rec.Code != http.StatusBadRequest || out.Error != "Datei größer als 20 MB" {
			t.Fatalf("got %d %q, want the size error", rec.Code, rec.Body.String())
		}
		if limit := int64(media.MaxSize + 1<<20); file.read > limit {
			t.Errorf("read %d bytes, want at most %d", file.read, limit)
		}
	})
}
