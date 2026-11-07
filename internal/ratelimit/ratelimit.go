// Package ratelimit counts attempts per key in a sliding window, in memory.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter is safe for concurrent use; the zero value is ready.
type Limiter struct {
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	mu        sync.Mutex
	hits      map[string][]time.Time
	lastSweep time.Time
}

// Allow records an attempt for key and reports whether it is within max
// attempts per window. A rejected attempt is not recorded.
func (l *Limiter) Allow(key string, max int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if l.hits == nil {
		l.hits = map[string][]time.Time{}
	}
	l.sweep(now, window)
	recent := expire(l.hits[key], now, window)
	if len(recent) >= max {
		l.hits[key] = recent
		return false
	}
	l.hits[key] = append(recent, now)
	return true
}

// Reset forgets all attempts of key.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	delete(l.hits, key)
	l.mu.Unlock()
}

// sweep drops keys without attempts in the window, at most once per window,
// so keys of clients that never come back do not pile up.
func (l *Limiter) sweep(now time.Time, window time.Duration) {
	if now.Sub(l.lastSweep) < window {
		return
	}
	l.lastSweep = now
	for k, ts := range l.hits {
		if recent := expire(ts, now, window); len(recent) == 0 {
			delete(l.hits, k)
		} else {
			l.hits[k] = recent
		}
	}
}

func (l *Limiter) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// expire returns the attempts of ts inside the window, reusing ts.
func expire(ts []time.Time, now time.Time, window time.Duration) []time.Time {
	recent := ts[:0]
	for _, t := range ts {
		if now.Sub(t) < window {
			recent = append(recent, t)
		}
	}
	return recent
}
