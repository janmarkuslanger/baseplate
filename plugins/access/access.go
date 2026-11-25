// Package access protects the public site the server serves (the static
// files and /media/) with one username and password, set in the admin and
// checked with HTTP Basic Auth. The admin and the endpoints of other
// plugins (e.g. form submissions) stay open. A static host in front of
// dist/ does not run the check: the protection applies where baseplate
// serves the site.
package access

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/janmarkuslanger/baseplate/i18n"
	"github.com/janmarkuslanger/baseplate/internal/auth"
	"github.com/janmarkuslanger/baseplate/internal/ratelimit"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/storage"
)

//go:embed admin
var adminFS embed.FS

//go:embed locales/*.json
var localesFS embed.FS

const (
	// AdminPath is the admin page where the access is set.
	AdminPath = "access"
	// MinPassword is the shortest password, like the admin users'.
	MinPassword = 10
	maxPassword = 1024
	maxUsername = 128
	// maxHashing bounds concurrent password checks; each needs 64 MiB.
	maxHashing = 4
)

// config is the stored access. verified is the digest of the last
// credentials that passed the (slow) password check, so the many requests
// of one page view only pay for it once; a save replaces the config.
type config struct {
	enabled  bool
	username string
	hash     string

	mu       sync.Mutex
	verified *[sha256.Size]byte
}

// Plugin is the access plugin; create it with New.
type Plugin struct {
	// Limit is the number of password checks per IP (per /64 for IPv6) per
	// Window (default 10 / 15 min); a success resets it.
	Limit  int
	Window time.Duration

	reg     *plugin.Registry
	limiter ratelimit.Limiter
	hashing chan struct{}

	mu  sync.Mutex
	cfg *config // nil: load from the store
}

// New returns the plugin with the default rate limit.
func New() *Plugin {
	return &Plugin{Limit: 10, Window: 15 * time.Minute, hashing: make(chan struct{}, maxHashing)}
}

func (p *Plugin) Name() string { return "access" }

func (p *Plugin) Register(r *plugin.Registry) error {
	p.reg = r
	cats, err := i18n.Load(localesFS, "locales")
	if err != nil {
		return err
	}
	for _, c := range cats {
		i18n.Catalogs.Add(r, c)
	}
	adm, _ := fs.Sub(adminFS, "admin")
	r.AdminTemplates(adm)
	r.AdminPage(plugin.AdminPage{Path: AdminPath, Label: "access.title", Handler: p.adminPage})
	r.Guard(p.guard)
	r.Migration(storage.Migration{
		Name: "access_0001_credentials",
		SQL: map[storage.Dialect]string{
			storage.SQLite: `CREATE TABLE access_credentials (
				id INTEGER PRIMARY KEY CHECK (id = 1),
				enabled INTEGER NOT NULL DEFAULT 0,
				username TEXT NOT NULL,
				password_hash TEXT NOT NULL
			)`,
		},
	})
	return nil
}

// config returns the stored access; no row means the site is open.
func (p *Plugin) config(ctx context.Context) (*config, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cfg != nil {
		return p.cfg, nil
	}
	c := &config{}
	var enabled int
	err := p.reg.Store.DB().QueryRowContext(ctx, `SELECT enabled, username, password_hash FROM access_credentials WHERE id = 1`).
		Scan(&enabled, &c.username, &c.hash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	c.enabled = enabled == 1
	p.cfg = c
	return c, nil
}

// guard asks for the credentials while the access is enabled. A store error
// fails closed.
func (p *Plugin) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg, err := p.config(r.Context())
		if err != nil {
			p.reg.Logger.Error("access: load", "err", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		if !cfg.enabled {
			next.ServeHTTP(w, r)
			return
		}
		if user, pw, ok := r.BasicAuth(); ok {
			switch p.check(r, cfg, user, pw) {
			case allowed:
				w.Header().Set("Cache-Control", "private")
				next.ServeHTTP(w, r)
				return
			case limited:
				w.Header().Set("Retry-After", "900")
				http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
				return
			}
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="Site", charset="UTF-8"`)
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
	})
}

type verdict int

const (
	denied verdict = iota
	allowed
	limited
)

func (p *Plugin) check(r *http.Request, cfg *config, user, pw string) verdict {
	sum := sha256.Sum256([]byte(user + "\x00" + pw))
	cfg.mu.Lock()
	ok := cfg.verified != nil && subtle.ConstantTimeCompare(cfg.verified[:], sum[:]) == 1
	cfg.mu.Unlock()
	if ok {
		return allowed
	}
	ip := p.reg.ClientIP(r)
	if !p.limiter.Allow(ip, p.Limit, p.Window) {
		return limited
	}
	select {
	case p.hashing <- struct{}{}:
		defer func() { <-p.hashing }()
	case <-r.Context().Done():
		return denied
	}
	// Check the password even if the username is wrong: no timing difference.
	pwOK := auth.CheckPassword(cfg.hash, pw)
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(cfg.username)) == 1
	if !pwOK || !userOK {
		return denied
	}
	p.limiter.Reset(ip)
	cfg.mu.Lock()
	cfg.verified = &sum
	cfg.mu.Unlock()
	return allowed
}

// Admin page error codes, each with an "access.err.<code>" text.
const (
	errUsername = "username"
	errPassword = "password"
	errLength   = "length"
)

func (p *Plugin) adminPage(w http.ResponseWriter, r *http.Request, ui plugin.AdminUI) {
	cfg, err := p.config(r.Context())
	if err != nil {
		p.fail(w, err)
		return
	}
	page := map[string]any{
		"CSRF": ui.CSRFToken(r), "Enabled": cfg.enabled, "Username": cfg.username,
		"HasPassword": cfg.hash != "", "Saved": r.URL.Query().Get("saved") == "1",
	}
	if r.Method == http.MethodPost {
		code, err := p.save(r, cfg)
		if err != nil {
			p.fail(w, err)
			return
		}
		if code == "" {
			http.Redirect(w, r, "/admin/p/"+AdminPath+"?saved=1", http.StatusSeeOther)
			return
		}
		page["Error"] = code
		page["Enabled"] = r.FormValue("enabled") != ""
		page["Username"] = strings.TrimSpace(r.FormValue("username"))
		w.WriteHeader(http.StatusUnprocessableEntity)
	}
	ui.Render(w, r, "access.html", page)
}

// save stores the form; a non-empty code means it was rejected.
func (p *Plugin) save(r *http.Request, cur *config) (code string, err error) {
	enabled := r.FormValue("enabled") != ""
	username := strings.TrimSpace(r.FormValue("username"))
	pw := r.FormValue("password")
	hash := cur.hash
	switch {
	case username == "" && enabled, strings.ContainsRune(username, ':'),
		utf8.RuneCountInString(username) > maxUsername, !utf8.ValidString(username):
		return errUsername, nil
	case pw != "" && (utf8.RuneCountInString(pw) < MinPassword || len(pw) > maxPassword):
		return errLength, nil
	case pw == "" && enabled && hash == "":
		return errPassword, nil
	}
	if pw != "" {
		hash = auth.HashPassword(pw)
	}
	_, err = p.reg.Store.DB().ExecContext(r.Context(), `INSERT INTO access_credentials (id, enabled, username, password_hash)
		VALUES (1, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET enabled = excluded.enabled,
		username = excluded.username, password_hash = excluded.password_hash`, boolInt(enabled), username, hash)
	if err != nil {
		return "", err
	}
	p.mu.Lock()
	p.cfg = nil // the next request loads it and starts without a verified login
	p.mu.Unlock()
	return "", nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (p *Plugin) fail(w http.ResponseWriter, err error) {
	p.reg.Logger.Error("access: admin", "err", err)
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}
