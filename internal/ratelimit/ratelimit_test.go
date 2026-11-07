package ratelimit

import (
	"fmt"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newLimiter() (*Limiter, *clock) {
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	return &Limiter{Now: c.now}, c
}

func TestAllowLimitsPerKey(t *testing.T) {
	l, _ := newLimiter()
	for i := range 3 {
		if !l.Allow("a", 3, time.Minute) {
			t.Fatalf("attempt %d rejected", i+1)
		}
	}
	if l.Allow("a", 3, time.Minute) {
		t.Fatal("4th attempt allowed")
	}
	if !l.Allow("b", 3, time.Minute) {
		t.Fatal("other key limited")
	}
}

func TestAllowSlidesWindow(t *testing.T) {
	l, c := newLimiter()
	l.Allow("a", 2, time.Minute)
	c.t = c.t.Add(30 * time.Second)
	l.Allow("a", 2, time.Minute)
	if l.Allow("a", 2, time.Minute) {
		t.Fatal("limit not reached")
	}
	c.t = c.t.Add(31 * time.Second) // first attempt left the window
	if !l.Allow("a", 2, time.Minute) {
		t.Fatal("expired attempt still counted")
	}
	if l.Allow("a", 2, time.Minute) {
		t.Fatal("window must still hold two attempts")
	}
}

func TestRejectedAttemptsAreNotRecorded(t *testing.T) {
	l, c := newLimiter()
	l.Allow("a", 1, time.Minute)
	for range 5 {
		c.t = c.t.Add(10 * time.Second)
		l.Allow("a", 1, time.Minute)
	}
	c.t = c.t.Add(11 * time.Second) // 61s after the only recorded attempt
	if !l.Allow("a", 1, time.Minute) {
		t.Fatal("rejected attempts kept the key locked")
	}
}

func TestReset(t *testing.T) {
	l, _ := newLimiter()
	l.Allow("a", 1, time.Minute)
	l.Reset("a")
	if !l.Allow("a", 1, time.Minute) {
		t.Fatal("reset key still limited")
	}
}

func TestExpiredKeysAreDropped(t *testing.T) {
	l, c := newLimiter()
	for i := range 1000 {
		l.Allow(fmt.Sprint("client-", i), 5, time.Minute)
	}
	c.t = c.t.Add(2 * time.Minute)
	l.Allow("new", 5, time.Minute)
	if n := len(l.hits); n != 1 {
		t.Fatalf("%d keys kept after their window, want 1", n)
	}
}

func TestActiveKeysSurviveSweep(t *testing.T) {
	l, c := newLimiter()
	l.Allow("old", 1, time.Minute)
	c.t = c.t.Add(50 * time.Second)
	l.Allow("active", 1, time.Minute)
	c.t = c.t.Add(20 * time.Second) // sweep: "old" expired, "active" not
	l.Allow("x", 1, time.Minute)
	if _, ok := l.hits["old"]; ok {
		t.Error("expired key kept")
	}
	if l.Allow("active", 1, time.Minute) {
		t.Error("sweep dropped an active key's attempts")
	}
}
