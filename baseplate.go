// Package baseplate is a monolithic CMS that customer projects embed as a
// Go module:
//
//	app := baseplate.New(baseplate.Config{Theme: theme})
//	app.Use(forms.New())
//	app.Main()
//
// The resulting binary serves the admin, builds the static site and
// handles dynamic endpoints such as form submissions.
package baseplate

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	netmail "net/mail"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/janmarkuslanger/baseplate/admin"
	"github.com/janmarkuslanger/baseplate/auth"
	"github.com/janmarkuslanger/baseplate/build"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/mail"
	"github.com/janmarkuslanger/baseplate/media"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/storage"
	"github.com/janmarkuslanger/baseplate/storage/sqlite"
)

// Config configures an App. Every field can be overridden by an
// environment variable (see LoadEnv), so one binary runs on any host.
type Config struct {
	// Addr is the listen address of `serve` (BASEPLATE_ADDR, default ":8080").
	Addr string
	// DataDir holds the SQLite database and uploaded media
	// (BASEPLATE_DATA_DIR, default "./data").
	DataDir string
	// DistDir receives the static build (BASEPLATE_DIST_DIR, default "./dist").
	DistDir string
	// Theme contains "templates/" and optionally "assets/".
	Theme fs.FS
	// SMTP configures outgoing mail (BASEPLATE_SMTP_HOST, _PORT, _USER,
	// _PASSWORD, _FROM). Without a host, mails are only logged.
	SMTP mail.SMTPConfig
	// SecureCookies sets the Secure flag on the admin session cookie; enable
	// it behind an HTTPS proxy (BASEPLATE_SECURE_COOKIES=1).
	SecureCookies bool
	// AdminLocale is the admin language, e.g. "de" or "en"
	// (BASEPLATE_ADMIN_LOCALE, default "de").
	AdminLocale string
	// TrustedProxyHeader names a header carrying the client IP, e.g.
	// "X-Forwarded-For" (BASEPLATE_TRUSTED_PROXY_HEADER). Rate limits use it
	// instead of the connection address. Only set it behind a proxy that
	// overwrites the header. Default: off.
	TrustedProxyHeader string
	// Store overrides the storage adapter (e.g. MySQL). Default: SQLite in DataDir.
	Store storage.Store
	// Mailer overrides the mailer built from SMTP.
	Mailer mail.Mailer
	Logger *slog.Logger
}

// LoadEnv applies BASEPLATE_* environment variables on top of c.
func (c *Config) LoadEnv() {
	str := func(key string, dst *string) {
		if v := os.Getenv(key); v != "" {
			*dst = v
		}
	}
	str("BASEPLATE_ADDR", &c.Addr)
	str("BASEPLATE_DATA_DIR", &c.DataDir)
	str("BASEPLATE_DIST_DIR", &c.DistDir)
	str("BASEPLATE_SMTP_HOST", &c.SMTP.Host)
	str("BASEPLATE_SMTP_USER", &c.SMTP.Username)
	str("BASEPLATE_SMTP_PASSWORD", &c.SMTP.Password)
	str("BASEPLATE_SMTP_FROM", &c.SMTP.From)
	str("BASEPLATE_TRUSTED_PROXY_HEADER", &c.TrustedProxyHeader)
	str("BASEPLATE_ADMIN_LOCALE", &c.AdminLocale)
	if v, err := strconv.Atoi(os.Getenv("BASEPLATE_SMTP_PORT")); err == nil {
		c.SMTP.Port = v
	}
	if v := os.Getenv("BASEPLATE_SECURE_COOKIES"); v != "" {
		c.SecureCookies = v == "1" || v == "true"
	}
}

func (c *Config) defaults() {
	if c.Addr == "" {
		c.Addr = ":8080"
	}
	if c.DataDir == "" {
		c.DataDir = "./data"
	}
	if c.DistDir == "" {
		c.DistDir = "./dist"
	}
	if c.SMTP.Port == 0 {
		c.SMTP.Port = 587
	}
	if c.Logger == nil {
		c.Logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
}

type App struct {
	cfg     Config
	plugins []plugin.Plugin

	// Set by setup.
	Registry *plugin.Registry
	Builder  *build.Builder
	auth     *auth.Manager
	admin    *admin.Admin
}

func New(cfg Config) *App { return &App{cfg: cfg} }

// Use adds plugins. They are registered in order after the core.
func (a *App) Use(ps ...plugin.Plugin) *App {
	a.plugins = append(a.plugins, ps...)
	return a
}

// Main runs the CLI with os.Args and exits on error.
func (a *App) Main() {
	if err := a.Run(context.Background(), os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

const usage = `usage: <binary> <command>

commands:
  serve            start the admin and API server
  build            build the static site into the dist directory
  migrate          apply database migrations
  user add EMAIL   create an admin user (password is read from stdin)
`

// Run executes one CLI command.
func (a *App) Run(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage)
		return nil
	}
	if err := a.Setup(ctx); err != nil {
		return err
	}
	defer a.Close()

	switch args[0] {
	case "serve":
		return a.serve(ctx)
	case "build":
		res, err := a.Builder.Build(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "built %d pages into %s in %s\n", res.Pages, a.cfg.DistDir, res.Duration.Round(time.Millisecond))
		return nil
	case "migrate":
		fmt.Fprintln(stdout, "migrations applied")
		return nil
	case "user":
		return a.userCmd(ctx, args[1:], stdin, stdout)
	}
	fmt.Fprint(stdout, usage)
	return fmt.Errorf("unknown command %q", args[0])
}

// Setup opens storage, registers plugins, runs migrations and wires the
// admin. Run calls it; call it yourself only to use Handler directly (custom
// servers, tests), and Close afterwards. Migrations run on every start so
// updates apply automatically.
func (a *App) Setup(ctx context.Context) error {
	a.cfg.LoadEnv()
	a.cfg.defaults()
	c := &a.cfg
	if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
		return err
	}
	st := c.Store
	if st == nil {
		var err error
		st, err = sqlite.Open(filepath.Join(c.DataDir, "baseplate.db"))
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
	}
	mailer := c.Mailer
	if mailer == nil {
		if c.SMTP.Host != "" {
			mailer = mail.SMTP{Config: c.SMTP}
		} else {
			mailer = mail.LogMailer{Logger: c.Logger}
		}
	}
	clientIP := plugin.RemoteIP
	if c.TrustedProxyHeader != "" {
		clientIP = plugin.ProxyIP(c.TrustedProxyHeader)
	}
	reg := plugin.NewRegistry(plugin.Services{Store: st, Mailer: mailer, Logger: c.Logger, ClientIP: clientIP})
	if err := reg.Use(fields.Core{}); err != nil {
		return err
	}
	if err := reg.Use(a.plugins...); err != nil {
		return err
	}
	if err := reg.Schema.Check(); err != nil {
		return err
	}
	if err := st.Migrate(ctx, append(append([]storage.Migration{}, storage.CoreMigrations...), reg.Migrations()...)); err != nil {
		return err
	}
	if err := st.UseSchema(ctx, reg.Schema); err != nil {
		return err
	}
	for _, m := range reg.DataMigrations() {
		m := m
		if err := st.RunOnce(ctx, m.Name, func(ctx context.Context) error { return m.Run(ctx, st) }); err != nil {
			return err
		}
	}
	mediaStore := media.Store{Dir: filepath.Join(c.DataDir, "media")}
	a.Registry = reg
	a.Builder = &build.Builder{Reg: reg, Theme: c.Theme, DistDir: c.DistDir, MediaDir: mediaStore.Dir}
	a.auth = &auth.Manager{Store: st, SecureCookies: c.SecureCookies}
	a.admin = &admin.Admin{Reg: reg, Auth: a.auth, Media: mediaStore, Builder: a.Builder, Logger: c.Logger, Lang: c.AdminLocale}
	return a.admin.Init()
}

// Close releases the storage opened by Setup.
func (a *App) Close() error { return a.Registry.Store.Close() }

// Handler returns the HTTP handler of the server: admin, media, plugin
// routes, and a preview of the last build at /.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	a.admin.Mount(mux)
	for _, rt := range a.Registry.Routes() {
		mux.Handle(rt.Pattern, rt.Handler)
	}
	mux.Handle("/", http.FileServer(http.Dir(a.cfg.DistDir)))
	return securityHeaders(mux)
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		if strings.HasPrefix(r.URL.Path, "/admin") {
			w.Header().Set("X-Frame-Options", "DENY")
		}
		h.ServeHTTP(w, r)
	})
}

func (a *App) serve(ctx context.Context) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv := &http.Server{
		Addr:              a.cfg.Addr,
		Handler:           a.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute, // large uploads
		WriteTimeout:      5 * time.Minute, // builds run in the request
		IdleTimeout:       2 * time.Minute,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	n, _ := a.Registry.Store.CountUsers(ctx)
	a.cfg.Logger.Info("baseplate running", "addr", a.cfg.Addr, "admin", "/admin/", "plugins", a.Registry.Plugins())
	if n == 0 {
		a.cfg.Logger.Warn("no admin user yet – create one with: <binary> user add you@example.com")
	}
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}

func (a *App) userCmd(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer) error {
	fsys := flag.NewFlagSet("user add", flag.ContinueOnError)
	name := fsys.String("name", "", "display name")
	if len(args) < 1 || args[0] != "add" {
		return errors.New("usage: user add [-name NAME] EMAIL")
	}
	if err := fsys.Parse(args[1:]); err != nil {
		return err
	}
	email := fsys.Arg(0)
	if addr, err := netmail.ParseAddress(email); err != nil || addr.Address != email {
		return fmt.Errorf("invalid e-mail address %q – usage: user add [-name NAME] EMAIL", email)
	}
	fmt.Fprint(stdout, "password (min. 10 characters): ")
	pw, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	pw = strings.TrimRight(pw, "\r\n")
	if len([]rune(pw)) < 10 {
		return errors.New("password must have at least 10 characters")
	}
	u := &storage.User{Email: email, Name: *name, PasswordHash: auth.HashPassword(pw)}
	if err := a.Registry.Store.CreateUser(ctx, u); err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	fmt.Fprintf(stdout, "\nuser %s created\n", u.Email)
	return nil
}
