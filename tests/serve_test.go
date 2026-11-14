package tests

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/janmarkuslanger/baseplate"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/storage"
)

// failingUserCount fails CountUsers and then stops the server via stop.
type failingUserCount struct {
	storage.Store
	stop context.CancelFunc
}

func (s failingUserCount) CountUsers(context.Context) (int, error) {
	s.stop()
	return 0, errors.New("users table locked")
}

// breakUserCount installs failingUserCount as the store of the registry.
type breakUserCount struct{ stop context.CancelFunc }

func (breakUserCount) Name() string { return "break-user-count" }
func (p breakUserCount) Register(r *plugin.Registry) error {
	r.Store = failingUserCount{Store: r.Store, stop: p.stop}
	return nil
}

// TestServeLogsUserCountError checks that serve reports a failing user
// count instead of skipping the "no admin user" check silently.
func TestServeLogsUserCountError(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var logs bytes.Buffer
	dir := t.TempDir()
	app := baseplate.New(baseplate.Config{DataDir: filepath.Join(dir, "data"), DistDir: filepath.Join(dir, "dist"), Theme: testTheme,
		Addr: "127.0.0.1:0", Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	app.Use(breakUserCount{stop: stop})
	if err := app.Run(ctx, []string{"serve"}, strings.NewReader(""), io.Discard); err != nil {
		t.Fatal(err)
	}
	if out := logs.String(); !strings.Contains(out, "count users") || !strings.Contains(out, "users table locked") {
		t.Errorf("user count error not logged: %q", out)
	}
	if strings.Contains(logs.String(), "no admin user yet") {
		t.Error("a failed count was reported as zero users")
	}
}
