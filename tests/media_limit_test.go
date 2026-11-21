package tests

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"testing"

	"github.com/janmarkuslanger/baseplate/internal/auth"
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

// oversizedUpload returns a multipart upload of a video twice the largest
// media limit and the counter of its file bytes.
func oversizedUpload(csrf string) (io.Reader, *countingReader, string, int64) {
	return fileUpload(csrf, "big.mp4", 2*media.MaxVideoSize)
}

// signatures are the first bytes of the files fileUpload sends.
var signatures = map[string]string{
	".png": "\x89PNG\r\n\x1a\n",
	".mp4": "\x00\x00\x00\x1cftypisom\x00\x00\x02\x00isomiso2avc1mp41",
}

// fileUpload returns a multipart upload of a file of size bytes (PNG or
// MP4, by name), the counter of its bytes after the signature, the content
// type and the body length.
func fileUpload(csrf, name string, size int64) (io.Reader, *countingReader, string, int64) {
	var head bytes.Buffer
	mw := multipart.NewWriter(&head)
	if csrf != "" {
		mw.WriteField("csrf", csrf)
	}
	fw, _ := mw.CreateFormFile("file", name)
	sig := signatures[path.Ext(name)]
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

	// post sends a form upload and returns the redirect target.
	post := func(t *testing.T, name string, size int64) string {
		t.Helper()
		body, _, ctype, length := fileUpload(c.csrf, name, size)
		req := httptest.NewRequest("POST", "/admin/media", body)
		req.ContentLength = length
		req.Header.Set("Content-Type", ctype)
		for _, ck := range cookies {
			req.AddCookie(ck)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("got %d %q, want a redirect", rec.Code, rec.Body.String())
		}
		return rec.Header().Get("Location")
	}

	t.Run("image at the limit", func(t *testing.T) {
		if loc := post(t, "limit.png", media.MaxSize); loc != "/admin/media" {
			t.Fatalf("redirect to %q, want the upload accepted", loc)
		}
	})

	t.Run("image above its limit", func(t *testing.T) {
		if loc := post(t, "large.png", media.MaxSize+1); loc != "/admin/media?error=media.error.size" {
			t.Fatalf("redirect to %q, want the size error", loc)
		}
	})

	// A video may exceed the body limit of other admin requests.
	t.Run("video above the general body limit", func(t *testing.T) {
		if loc := post(t, "clip.mp4", auth.MaxBodySize+1<<20); loc != "/admin/media" {
			t.Fatalf("redirect to %q, want the upload accepted", loc)
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
		if rec.Code != http.StatusBadRequest || out.Error != "Datei zu groß (Bilder und PDFs max. 20 MB, Videos max. 100 MB)" {
			t.Fatalf("got %d %q, want the size error", rec.Code, rec.Body.String())
		}
		if limit := int64(media.MaxVideoSize + 1<<20); file.read > limit {
			t.Errorf("read %d bytes, want at most %d", file.read, limit)
		}
	})
}
