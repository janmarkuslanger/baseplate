package fileserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestServer(t *testing.T) {
	h := New(fstest.MapFS{
		"index.html":       {Data: []byte("home")},
		"about/index.html": {Data: []byte("about")},
		"uploads/a.png":    {Data: []byte("png")},
		"uploads/b.png":    {Data: []byte("png")},
	})
	cases := []struct {
		path   string
		status int
		body   string
	}{
		{"/", http.StatusOK, "home"},
		{"/about/", http.StatusOK, "about"},
		{"/about", http.StatusMovedPermanently, ""},
		{"/uploads/a.png", http.StatusOK, "png"},
		{"/uploads/", http.StatusNotFound, ""},
		{"/uploads", http.StatusNotFound, ""},
		{"/missing.png", http.StatusNotFound, ""},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", c.path, nil))
		if w.Code != c.status {
			t.Errorf("GET %s: %d, want %d", c.path, w.Code, c.status)
		}
		if c.body != "" && w.Body.String() != c.body {
			t.Errorf("GET %s: body %q, want %q", c.path, w.Body.String(), c.body)
		}
		if strings.Contains(w.Body.String(), "a.png") {
			t.Errorf("GET %s lists directory contents:\n%s", c.path, w.Body.String())
		}
	}
}

// Videos get their content type even where Go's table and the system's
// mime.types lack it; other files keep the usual detection.
func TestContentTypes(t *testing.T) {
	h := New(fstest.MapFS{
		"clip.mp4":  {Data: []byte("\x00\x00\x00\x1cftypisom")},
		"clip.WEBM": {Data: []byte("\x1a\x45\xdf\xa3")},
		"a.png":     {Data: []byte("\x89PNG\r\n\x1a\n")},
	})
	for path, want := range map[string]string{
		"/clip.mp4":    "video/mp4",
		"/clip.WEBM":   "video/webm",
		"/a.png":       "image/png",
		"/missing.mp4": "text/plain; charset=utf-8",
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if got := w.Header().Get("Content-Type"); got != want {
			t.Errorf("GET %s: Content-Type %q, want %q", path, got, want)
		}
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/clip.mp4", nil)
	req.Header.Set("Range", "bytes=4-7")
	h.ServeHTTP(w, req)
	if w.Code != http.StatusPartialContent || w.Body.String() != "ftyp" {
		t.Errorf("range request: %d %q, want 206 \"ftyp\"", w.Code, w.Body.String())
	}
}
