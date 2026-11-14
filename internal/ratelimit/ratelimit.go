// Package ratelimit counts attempts per key in a sliding window, in memory.
package ratelimit

import (
	"net/netip"
	"sync"
	"time"
)

// DefaultMaxKeys is the number of keys a Limiter tracks if MaxKeys is 0.
const DefaultMaxKeys = 10_000

// Limiter is safe for concurrent use; the zero value is ready.
//
// A key that is an IPv6 address counts for its whole /64: one client
// usually controls all of it, so per-address keys would let it pick a fresh
// limit for every attempt. IPv4 and IPv4-mapped addresses and other keys
// are used as given.
type Limiter struct {
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// MaxKeys bounds the keys kept in memory (DefaultMaxKeys if 0). When
	// the table is full even after dropping expired keys, attempts of new
	// keys are rejected until old keys expire; known keys are unaffected.
	// Failing closed keeps a flood of addresses from both exhausting memory
	// and bypassing the limit.
	MaxKeys int

	mu        sync.Mutex
	hits      map[string][]time.Time
	lastSweep time.Time
}

// Allow records an attempt for key and reports whether it is within max
// attempts per window. A rejected attempt is not recorded.
func (l *Limiter) Allow(key string, max int, window time.Duration) bool {
	key = bucket(key)
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if l.hits == nil {
		l.hits = map[string][]time.Time{}
	}
	l.sweep(now, window, false)
	ts, known := l.hits[key]
	if !known && len(l.hits) >= l.maxKeys() {
		l.sweep(now, window, true)
		if len(l.hits) >= l.maxKeys() {
			return false
		}
	}
	recent := expire(ts, now, window)
	if len(recent) >= max {
		l.hits[key] = recent
		return false
	}
	l.hits[key] = append(recent, now)
	return true
}

// Reset forgets all attempts of key.
func (l *Limiter) Reset(key string) {
	key = bucket(key)
	l.mu.Lock()
	delete(l.hits, key)
	l.mu.Unlock()
}

// sweep drops keys without attempts in the window, at most once per window
// (once per second if forced, as the table is full), so keys of clients
// that never come back do not pile up.
func (l *Limiter) sweep(now time.Time, window time.Duration, force bool) {
	every := window
	if force {
		every = min(window, time.Second)
	}
	if now.Sub(l.lastSweep) < every {
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

func (l *Limiter) maxKeys() int {
	if l.MaxKeys > 0 {
		return l.MaxKeys
	}
	return DefaultMaxKeys
}

func (l *Limiter) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// bucket returns the key attempts are counted under: the /64 prefix of an
// IPv6 address, key itself otherwise.
func bucket(key string) string {
	addr, err := netip.ParseAddr(key)
	if err != nil || !addr.Is6() || addr.Is4In6() {
		return key
	}
	p, err := addr.WithZone("").Prefix(64)
	if err != nil {
		return key // unreachable: 64 is a valid IPv6 prefix length
	}
	return p.String()
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
