package media

import (
	"bytes"
	"errors"
	"io"
	"maps"
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
	big := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, DefaultMaxSize)...)
	if _, err := s.Save(bytes.NewReader(big), "big.png"); !errors.Is(err, ErrSize) {
		t.Errorf("size: %v", err)
	}
	files, _ := s.List()
	if len(files) != 2 {
		t.Errorf("list = %v", files)
	}
}

func TestSaveVideo(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	// ftyp box: size, "ftyp", major brand, minor version, compatible brands.
	mp4 := "\x00\x00\x00\x1cftypisom\x00\x00\x02\x00isomiso2avc1 rest"
	webm := "\x1a\x45\xdf\xa3\x9f\x42\x86\x81\x01 rest"
	for name, data := range map[string]string{"Clip.MP4": mp4, "clip.webm": webm} {
		u, err := s.Save(strings.NewReader(data), name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if ext := strings.ToLower(name[strings.LastIndex(name, "."):]); !strings.HasPrefix(u, "/media/clip-") || !strings.HasSuffix(u, ext) {
			t.Errorf("%s: url = %s", name, u)
		}
	}
	for name, data := range map[string]string{
		"html.mp4":  "<html><script>alert(1)</script>",
		"avif.mp4":  "\x00\x00\x00\x1cftypavif rest",
		"mp4.webm":  mp4,
		"webm.mp4":  webm,
		"mp4.png":   mp4,
		"short.mp4": "\x00\x00\x00\x1cftyp",
	} {
		if _, err := s.Save(strings.NewReader(data), name); !errors.Is(err, ErrContent) {
			t.Errorf("%s: %v, want ErrContent", name, err)
		}
	}
	if _, err := s.Save(strings.NewReader(mp4), "clip.mov"); !errors.Is(err, ErrType) {
		t.Errorf("mov: %v", err)
	}

	// Videos may be larger than images, up to DefaultMaxVideoSize.
	video := func(size int) io.Reader {
		return io.MultiReader(strings.NewReader(mp4), bytes.NewReader(make([]byte, size-len(mp4))))
	}
	if _, err := s.Save(video(DefaultMaxSize+1), "large.mp4"); err != nil {
		t.Errorf("video above the image limit: %v", err)
	}
	if _, err := s.Save(video(DefaultMaxVideoSize), "limit.mp4"); err != nil {
		t.Errorf("video at the limit: %v", err)
	}
	if _, err := s.Save(video(DefaultMaxVideoSize+1), "big.mp4"); !errors.Is(err, ErrSize) {
		t.Errorf("video above the limit: %v", err)
	}

	files, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 4 {
		t.Fatalf("list = %v", files)
	}
	for _, f := range files {
		if !f.IsVideo || f.IsImage {
			t.Errorf("%s: IsVideo %v, IsImage %v", f.Name, f.IsVideo, f.IsImage)
		}
	}
}

func TestListKinds(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	for name, data := range map[string]string{
		"a.png": "\x89PNG\r\n\x1a\n", "b.pdf": "%PDF-1.7\n", "c.webm": "\x1a\x45\xdf\xa3",
	} {
		if _, err := s.Save(strings.NewReader(data), name); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	files, _ := s.List()
	got := map[string][2]bool{}
	for _, f := range files {
		got[f.Name[strings.LastIndex(f.Name, "."):]] = [2]bool{f.IsImage, f.IsVideo}
	}
	want := map[string][2]bool{".png": {true, false}, ".pdf": {false, false}, ".webm": {false, true}}
	if !maps.Equal(got, want) {
		t.Errorf("kinds (IsImage, IsVideo) = %v, want %v", got, want)
	}
}

// A store's own limits replace the defaults, per kind of file.
func TestStoreLimits(t *testing.T) {
	s := Store{Dir: t.TempDir(), MaxSize: 1 << 10, MaxVideoSize: 2 << 10}
	if size, video := s.Limits(); size != 1<<10 || video != 2<<10 {
		t.Errorf("limits = %d, %d", size, video)
	}
	if size, video := (Store{}).Limits(); size != DefaultMaxSize || video != DefaultMaxVideoSize {
		t.Errorf("default limits = %d, %d", size, video)
	}
	file := func(sig string, size int) io.Reader {
		return io.MultiReader(strings.NewReader(sig), bytes.NewReader(make([]byte, size-len(sig))))
	}
	png, mp4 := "\x89PNG\r\n\x1a\n", "\x00\x00\x00\x1cftypisom"
	for _, c := range []struct {
		name, sig string
		size      int
		ok        bool
	}{
		{"a.png", png, 1 << 10, true},
		{"b.png", png, 1<<10 + 1, false},
		{"c.mp4", mp4, 2 << 10, true},
		{"d.mp4", mp4, 2<<10 + 1, false},
	} {
		_, err := s.Save(file(c.sig, c.size), c.name)
		if c.ok && err != nil || !c.ok && !errors.Is(err, ErrSize) {
			t.Errorf("%s (%d bytes): %v", c.name, c.size, err)
		}
	}
}
