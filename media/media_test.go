package media

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestSave(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	u1, err := s.Save(strings.NewReader("img"), "Mein Foto!.JPG")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u1, "/media/mein-foto-") || !strings.HasSuffix(u1, ".jpg") {
		t.Errorf("url = %s", u1)
	}
	u2, _ := s.Save(strings.NewReader("img"), "Mein Foto!.JPG")
	if u1 != u2 {
		t.Error("same content must yield same URL")
	}
	if _, err := s.Save(strings.NewReader("<svg/>"), "x.svg"); !errors.Is(err, ErrType) {
		t.Errorf("svg: %v", err)
	}
	if _, err := s.Save(bytes.NewReader(make([]byte, MaxSize+1)), "big.png"); !errors.Is(err, ErrSize) {
		t.Errorf("size: %v", err)
	}
	files, _ := s.List()
	if len(files) != 1 {
		t.Errorf("list = %v", files)
	}
}
