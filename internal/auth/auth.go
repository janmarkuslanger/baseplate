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
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/janmarkuslanger/baseplate/i18n"
	"github.com/janmarkuslanger/baseplate/internal/ratelimit"
	"github.com/janmarkuslanger/baseplate/storage"
)

// MaxBodySize limits request bodies of authenticated state-changing requests.
const MaxBodySize = 64 << 20

const (
	CookieName = "bp_session"
	// LoginCSRFCookie carries the login form's CSRF token (double submit:
	// the form field must match the cookie).
	LoginCSRFCookie = "bp_login_csrf"
	sessionTTL      = 14 * 24 * time.Hour
	argonTime       = 2
	argonMemory     = 64 * 1024
	argonPar        = 2
	argonKeyLen     = 32
	// maxHashing bounds concurrent password checks at login. Each needs
	// argonMemory (64 MiB), so a login flood cannot exhaust memory.
	maxHashing = 4
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
	// Logger records storage errors while checking a session
	// (slog.Default() if nil).
	Logger *slog.Logger

	limiter ratelimit.Limiter
	// dummyHash is checked for unknown users so timing does not reveal
	// which e-mails exist.
	dummyOnce sync.Once
	dummyHash string

	slotsOnce sync.Once
	slots     chan struct{}
}

// Login checks credentials and creates a session. key identifies the client
// for rate limiting (typically the remote IP).
func (m *Manager) Login(ctx context.Context, key, email, pw string) (*storage.Session, error) {
	if !m.limiter.Allow(key, limitMax, limitWindow) {
		return nil, ErrRateLimited
	}
	u, err := m.Store.GetUserByEmail(ctx, email)
	if errors.Is(err, storage.ErrNotFound) {
		u = nil // checked against a dummy hash, so timing does not reveal it
	} else if err != nil {
		return nil, err
	}
	release, err := m.acquire(ctx)
	if err != nil {
		return nil, err
	}
	var ok bool
	if u == nil {
		m.dummyOnce.Do(func() { m.dummyHash = HashPassword("dummy") })
		CheckPassword(m.dummyHash, pw)
	} else {
		ok = CheckPassword(u.PasswordHash, pw)
	}
	release()
	if !ok {
		return nil, ErrInvalidLogin
	}
	m.limiter.Reset(key)
	s := &storage.Session{Token: token(), UserID: u.ID, CSRF: token(), ExpiresAt: time.Now().Add(sessionTTL)}
	if err := m.Store.CreateSession(ctx, s); err != nil {
		return nil, err
	}
	return s, nil
}

// acquire waits for one of maxHashing slots, or until ctx ends.
func (m *Manager) acquire(ctx context.Context) (release func(), err error) {
	m.slotsOnce.Do(func() { m.slots = make(chan struct{}, maxHashing) })
	select {
	case m.slots <- struct{}{}:
		return func() { <-m.slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// LoginCSRF returns the CSRF token for the login form and sets it as a
// cookie. An existing token is kept, so several open login tabs work.
func (m *Manager) LoginCSRF(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(LoginCSRFCookie); err == nil && c.Value != "" {
		return c.Value
	}
	tok := token()
	http.SetCookie(w, &http.Cookie{
		Name: LoginCSRFCookie, Value: tok, Path: "/admin/login",
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: m.SecureCookies || r.TLS != nil,
	})
	return tok
}

// CheckLoginCSRF reports whether the login form's csrf field matches the
// cookie set by LoginCSRF. It stops other sites from logging a visitor
// into an attacker's account.
func (m *Manager) CheckLoginCSRF(r *http.Request) bool {
	c, err := r.Cookie(LoginCSRFCookie)
	if err != nil || c.Value == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(r.FormValue("csrf")), []byte(c.Value)) == 1
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
		cur, err := m.current(r)
		if err != nil {
			// Fail closed: the session could not be checked, which is
			// neither a login nor a reason to send the editor to it.
			m.logger().Error("auth: check session", "err", err)
			http.Error(w, i18n.Key("error.internal").Error(), http.StatusInternalServerError)
			return
		}
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

// current returns the session's user, nil without a valid session, and an
// error if the store fails to answer.
func (m *Manager) current(r *http.Request) (*Current, error) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return nil, nil
	}
	s, err := m.Store.GetSession(r.Context(), c.Value)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	u, err := m.Store.GetUser(r.Context(), s.UserID)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get user %d: %w", s.UserID, err)
	}
	return &Current{User: u, Session: s}, nil
}

func (m *Manager) logger() *slog.Logger {
	if m.Logger != nil {
		return m.Logger
	}
	return slog.Default()
}

// Login allows limitMax attempts per key per limitWindow.
const (
	limitWindow = 15 * time.Minute
	limitMax    = 10
)
