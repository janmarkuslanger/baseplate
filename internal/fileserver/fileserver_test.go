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
