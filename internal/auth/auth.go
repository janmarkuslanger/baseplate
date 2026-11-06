// Package auth handles admin users: password hashing, sessions, CSRF and
// login rate limiting.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/janmarkuslanger/baseplate/i18n"
	"github.com/janmarkuslanger/baseplate/storage"
)

// MaxBodySize limits request bodies of authenticated state-changing requests.
const MaxBodySize = 64 << 20

const (
	CookieName  = "bp_session"
	sessionTTL  = 14 * 24 * time.Hour
	argonTime   = 2
	argonMemory = 64 * 1024
	argonPar    = 2
	argonKeyLen = 32
)

var (
	ErrInvalidLogin = i18n.Key("login.invalid")
	ErrRateLimited  = i18n.Key("login.rateLimited")
)

// HashPassword returns an encoded argon2id hash.
func HashPassword(pw string) string {
	salt := randomBytes(16)
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonPar, argonKeyLen)
	return fmt.Sprintf("argon2id$%d$%d$%d$%s$%s", argonTime, argonMemory, argonPar,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

// CheckPassword verifies pw against an encoded hash in constant time.
func CheckPassword(encoded, pw string) bool {
	var t, m uint32
	var p uint8
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "argon2id" {
		return false
	}
	if _, err := fmt.Sscanf(parts[1]+" "+parts[2]+" "+parts[3], "%d %d %d", &t, &m, &p); err != nil {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return b
}

func token() string { return rand.Text() }

// Manager issues and checks sessions.
type Manager struct {
	Store interface {
		storage.Users
		storage.Sessions
	}
	// SecureCookies forces the Secure flag (set when served behind HTTPS).
	SecureCookies bool

	limiter limiter
	// dummyHash is checked for unknown users so timing does not reveal
	// which e-mails exist.
	dummyOnce sync.Once
	dummyHash string
}

// Login checks credentials and creates a session. key identifies the client
// for rate limiting (typically the remote IP).
func (m *Manager) Login(ctx context.Context, key, email, pw string) (*storage.Session, error) {
	if !m.limiter.allow(key) {
		return nil, ErrRateLimited
	}
	u, err := m.Store.GetUserByEmail(ctx, email)
	if errors.Is(err, storage.ErrNotFound) {
		m.dummyOnce.Do(func() { m.dummyHash = HashPassword("dummy") })
		CheckPassword(m.dummyHash, pw)
		return nil, ErrInvalidLogin
	}
	if err != nil {
		return nil, err
	}
	if !CheckPassword(u.PasswordHash, pw) {
		return nil, ErrInvalidLogin
	}
	m.limiter.reset(key)
	s := &storage.Session{Token: token(), UserID: u.ID, CSRF: token(), ExpiresAt: time.Now().Add(sessionTTL)}
	if err := m.Store.CreateSession(ctx, s); err != nil {
		return nil, err
	}
	return s, nil
}

// SetCookie sends the session cookie.
func (m *Manager) SetCookie(w http.ResponseWriter, r *http.Request, s *storage.Session) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: s.Token, Path: "/", Expires: s.ExpiresAt,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: m.SecureCookies || r.TLS != nil,
	})
}

// Logout deletes the current session and its cookie.
func (m *Manager) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(CookieName); err == nil {
		m.Store.DeleteSession(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
}

type ctxKey struct{}

// Current holds the authenticated user and session of a request.
type Current struct {
	User    *storage.User
	Session *storage.Session
}

// FromContext returns the current user set by Require, or nil.
func FromContext(ctx context.Context) *Current {
	c, _ := ctx.Value(ctxKey{}).(*Current)
	return c
}

// Require redirects unauthenticated requests to loginPath and rejects
// state-changing requests without a valid CSRF token (form field or
// X-CSRF-Token header).
func (m *Manager) Require(loginPath string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := m.current(r)
		if cur == nil {
			if r.Header.Get("HX-Request") != "" {
				w.Header().Set("HX-Redirect", loginPath)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, loginPath, http.StatusSeeOther)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			r.Body = http.MaxBytesReader(w, r.Body, MaxBodySize)
			tok := r.Header.Get("X-CSRF-Token")
			if tok == "" {
				tok = r.FormValue("csrf")
			}
			if subtle.ConstantTimeCompare([]byte(tok), []byte(cur.Session.CSRF)) != 1 {
				http.Error(w, i18n.Key("auth.csrf").Error(), http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, cur)))
	})
}

func (m *Manager) current(r *http.Request) *Current {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return nil
	}
	s, err := m.Store.GetSession(r.Context(), c.Value)
	if err != nil {
		return nil
	}
	u, err := m.Store.GetUser(r.Context(), s.UserID)
	if err != nil {
		return nil
	}
	return &Current{User: u, Session: s}
}

// limiter allows 10 failed attempts per key per 15 minutes.
type limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

const (
	limitWindow = 15 * time.Minute
	limitMax    = 10
)

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.hits == nil {
		l.hits = map[string][]time.Time{}
	}
	now := time.Now()
	recent := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < limitWindow {
			recent = append(recent, t)
		}
	}
	if len(recent) >= limitMax {
		l.hits[key] = recent
		return false
	}
	l.hits[key] = append(recent, now)
	return true
}

func (l *limiter) reset(key string) {
	l.mu.Lock()
	delete(l.hits, key)
	l.mu.Unlock()
}
