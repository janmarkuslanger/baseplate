package tests

import (
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/janmarkuslanger/baseplate/internal/auth"
	"github.com/janmarkuslanger/baseplate/internal/media"
	"github.com/janmarkuslanger/baseplate/settings"
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

// sendUpload posts an upload of a file of size bytes (see fileUpload) to
// h as c's user: as a form, or as the image field's JSON request. Without
// length, the request announces no body length.
func sendUpload(c *client, h http.Handler, name string, size int64, asJSON, length bool) (*httptest.ResponseRecorder, *countingReader) {
	csrf := c.csrf
	if asJSON {
		csrf = ""
	}
	body, file, ctype, n := fileUpload(csrf, name, size)
	req := httptest.NewRequest("POST", "/admin/media", body)
	req.ContentLength = -1
	if length {
		req.ContentLength = n
	}
	req.Header.Set("Content-Type", ctype)
	if asJSON {
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	u, _ := url.Parse(c.srv.URL)
	for _, ck := range c.http.Jar.Cookies(u) {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, file
}

// redirect returns the target of a form upload's answer.
func redirect(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("got %d %q, want a redirect", rec.Code, rec.Body.String())
	}
	return rec.Header().Get("Location")
}

// jsonError returns the message of the image field's upload error.
func jsonError(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var out struct{ Error string }
	json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusBadRequest || out.Error == "" {
		t.Fatalf("got %d %q, want an upload error", rec.Code, rec.Body.String())
	}
	return out.Error
}

// An upload larger than the media limit is rejected with the size error
// before the server has read (and spooled) all of it.
func TestOversizedUploadIsRejectedEarly(t *testing.T) {
	_, c := newTestApp(t)
	c.login()
	h := c.srv.Config.Handler

	t.Run("form", func(t *testing.T) {
		rec, file := sendUpload(c, h, "big.mp4", 2*media.DefaultMaxVideoSize, false, true)
		if loc := redirect(t, rec); loc != "/admin/media?error=media.error.size" {
			t.Fatalf("redirect to %q, want the size error", loc)
		}
		if file.read > 0 {
			t.Errorf("read %d bytes of a body whose length already exceeds the limit", file.read)
		}
	})

	t.Run("image at the limit", func(t *testing.T) {
		if rec, _ := sendUpload(c, h, "limit.png", media.DefaultMaxSize, false, true); redirect(t, rec) != "/admin/media" {
			t.Fatalf("redirect to %q, want the upload accepted", redirect(t, rec))
		}
	})

	t.Run("image above its limit", func(t *testing.T) {
		if rec, _ := sendUpload(c, h, "large.png", media.DefaultMaxSize+1, false, true); redirect(t, rec) != "/admin/media?error=media.error.size" {
			t.Fatalf("redirect to %q, want the size error", redirect(t, rec))
		}
	})

	// A video may exceed the body limit of other admin requests.
	t.Run("video above the general body limit", func(t *testing.T) {
		if rec, _ := sendUpload(c, h, "clip.mp4", auth.MaxBodySize+1<<20, false, true); redirect(t, rec) != "/admin/media" {
			t.Fatalf("redirect to %q, want the upload accepted", redirect(t, rec))
		}
	})

	t.Run("json without length", func(t *testing.T) {
		rec, file := sendUpload(c, h, "big.mp4", 2*media.DefaultMaxVideoSize, true, false)
		if msg := jsonError(t, rec); msg != "Datei zu groß (Bilder und PDFs max. 20 MB, Videos max. 100 MB)" {
			t.Fatalf("error %q, want the size error", msg)
		}
		if limit := int64(media.DefaultMaxVideoSize + 1<<20); file.read > limit {
			t.Errorf("read %d bytes, want at most %d", file.read, limit)
		}
	})
}

// The published settings set the upload limits: below and above the
// defaults, in the checks, the messages and the media page.
func TestUploadLimitsFromSettings(t *testing.T) {
	_, c := newTestApp(t)
	c.login()
	h := c.srv.Config.Handler
	base := map[string]any{"siteName": "Acme", "baseURL": "https://acme.test"}
	with := func(limits map[string]any) map[string]any {
		out := maps.Clone(base)
		maps.Copy(out, limits)
		return out
	}

	for _, v := range []any{0, -5, 2.5, settings.MaxMediaMB + 1} {
		res, body := c.save("settings", "published", with(map[string]any{settings.MediaMaxVideoMB: v}))
		if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "Upload-Limits: ganze Zahl von 1 bis 1024 (MB)") {
			t.Errorf("video limit %v: %d, want the limit refused", v, res.StatusCode)
		}
	}

	// Lower limits: images 1 MB, videos 2 MB.
	if res, body := c.save("settings", "published", with(map[string]any{settings.MediaMaxMB: 1, settings.MediaMaxVideoMB: 2})); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("save settings: %d\n%s", res.StatusCode, body)
	}
	if _, html := c.do("GET", "/admin/media", nil); !strings.Contains(html, "pdf (max. 1 MB) sowie mp4, webm (max. 2 MB)") {
		t.Errorf("media page does not name the limits:\n%s", html)
	}
	const msg = "Datei zu groß (Bilder und PDFs max. 1 MB, Videos max. 2 MB)"
	if rec, _ := sendUpload(c, h, "a.png", 1<<20, false, true); redirect(t, rec) != "/admin/media" {
		t.Errorf("image at the limit: redirect to %q", redirect(t, rec))
	}
	rec, _ := sendUpload(c, h, "b.png", 1<<20+1, false, true)
	if loc := redirect(t, rec); loc != "/admin/media?error=media.error.size" {
		t.Errorf("image above the limit: redirect to %q", loc)
	} else if _, html := c.do("GET", loc, nil); !strings.Contains(html, msg) {
		t.Errorf("media page does not show %q", msg)
	}
	if rec, _ := sendUpload(c, h, "c.mp4", 2<<20, false, true); redirect(t, rec) != "/admin/media" {
		t.Errorf("video at the limit: redirect to %q", redirect(t, rec))
	}
	if rec, _ := sendUpload(c, h, "d.mp4", 2<<20+1, true, true); jsonError(t, rec) != msg {
		t.Errorf("video above the limit: %q", jsonError(t, rec))
	}
	// The cap of the request follows the settings too.
	if rec, file := sendUpload(c, h, "e.mp4", 4<<20, false, true); redirect(t, rec) != "/admin/media?error=media.error.size" || file.read > 0 {
		t.Errorf("announced body above the limit: redirect to %q after reading %d bytes", redirect(t, rec), file.read)
	}

	// Higher limit: an image above the default is accepted.
	c.save("settings", "published", with(map[string]any{settings.MediaMaxMB: 30}))
	if rec, _ := sendUpload(c, h, "f.png", media.DefaultMaxSize+1<<20, false, true); redirect(t, rec) != "/admin/media" {
		t.Errorf("image above the default: redirect to %q", redirect(t, rec))
	}
	if _, html := c.do("GET", "/admin/media", nil); !strings.Contains(html, "pdf (max. 30 MB) sowie mp4, webm (max. 100 MB)") {
		t.Errorf("media page does not name the limits:\n%s", html)
	}
}

// slowReader delays its second half, as a slow connection would.
type slowReader struct {
	r     io.Reader
	after int64
	pause time.Duration
	read  int64
}

func (s *slowReader) Read(p []byte) (int, error) {
	if s.read < s.after {
		p = p[:min(int64(len(p)), s.after-s.read)]
	} else if s.pause > 0 {
		time.Sleep(s.pause)
		s.pause = 0
	}
	n, err := s.r.Read(p)
	s.read += int64(n)
	return n, err
}

// An upload may take longer than the server's read and write timeouts.
func TestSlowUploadOutlastsServerTimeouts(t *testing.T) {
	_, c := newTestApp(t)
	c.login()
	srv := httptest.NewUnstartedServer(c.srv.Config.Handler)
	srv.Config.ReadTimeout = 200 * time.Millisecond
	srv.Config.WriteTimeout = 200 * time.Millisecond
	srv.Start()
	defer srv.Close()

	body, _, ctype, n := fileUpload(c.csrf, "slow.mp4", 64<<10)
	req, _ := http.NewRequest("POST", srv.URL+"/admin/media", &slowReader{r: body, after: 32 << 10, pause: 500 * time.Millisecond})
	req.ContentLength = n
	req.Header.Set("Content-Type", ctype)
	u, _ := url.Parse(c.srv.URL)
	for _, ck := range c.http.Jar.Cookies(u) {
		req.AddCookie(ck)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if loc := res.Header.Get("Location"); res.StatusCode != http.StatusSeeOther || loc != "/admin/media" {
		t.Fatalf("got %d %q, want the upload accepted", res.StatusCode, loc)
	}

	// Other requests keep the server's timeouts.
	form := url.Values{"csrf": {c.csrf}, "data": {`{"siteName":"A","baseURL":"https://a.test"}`}}.Encode()
	req, _ = http.NewRequest("POST", srv.URL+"/admin/c/settings/save", &slowReader{r: strings.NewReader(form), after: 10, pause: 500 * time.Millisecond})
	req.ContentLength = int64(len(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, ck := range c.http.Jar.Cookies(u) {
		req.AddCookie(ck)
	}
	if res, err := client.Do(req); err == nil {
		res.Body.Close()
		if res.StatusCode == http.StatusSeeOther {
			t.Errorf("slow save answered %d, want the server's read timeout", res.StatusCode)
		}
	}
}
