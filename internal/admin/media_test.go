package admin

import (
	"testing"
	"time"
)

// Uploads get time for the limit at 256 KB/s, at least 5 minutes.
func TestUploadTime(t *testing.T) {
	for n, want := range map[int64]time.Duration{
		0:         5 * time.Minute,
		20 << 20:  5 * time.Minute,
		100 << 20: 400 * time.Second,
		1 << 30:   4096 * time.Second,
	} {
		if got := uploadTime(n); got != want {
			t.Errorf("uploadTime(%d) = %v, want %v", n, got, want)
		}
	}
}
