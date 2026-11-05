package media

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestSave(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	jpeg := "\xff\xd8\xff\xe0 fake jpeg"
	u1, err := s.Save(strings.NewReader(jpeg), "Mein Foto!.JPG")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u1, "/media/mein-foto-") || !strings.HasSuffix(u1, ".jpg") {
		t.Errorf("url = %s", u1)
	}
	u2, _ := s.Save(strings.NewReader(jpeg), "Mein Foto!.JPG")
	if u1 != u2 {
		t.Error("same content must yield same URL")
	}
	if _, err := s.Save(strings.NewReader("<svg/>"), "x.svg"); !errors.Is(err, ErrType) {
		t.Errorf("svg: %v", err)
	}
	if _, err := s.Save(strings.NewReader("<html><script>alert(1)</script>"), "x.png"); !errors.Is(err, ErrContent) {
		t.Errorf("html as png: %v", err)
	}
	if _, err := s.Save(strings.NewReader("\x00\x00\x00\x1cftypavif rest"), "bild.avif"); err != nil {
		t.Errorf("avif: %v", err)
	}
	big := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, MaxSize)...)
	if _, err := s.Save(bytes.NewReader(big), "big.png"); !errors.Is(err, ErrSize) {
		t.Errorf("size: %v", err)
	}
	files, _ := s.List()
	if len(files) != 2 {
		t.Errorf("list = %v", files)
	}
}
