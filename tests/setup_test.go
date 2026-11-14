package tests

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/janmarkuslanger/baseplate"
	"github.com/janmarkuslanger/baseplate/plugin"
)

type failingPlugin struct{}

func (failingPlugin) Name() string                    { return "failing" }
func (failingPlugin) Register(*plugin.Registry) error { return errors.New("register failed") }

// TestSetupClosesDatabaseOnError checks that a failed Setup does not leave
// the database it opened open, counting this process's open files.
func TestSetupClosesDatabaseOnError(t *testing.T) {
	openFiles := func() int {
		d, err := os.Open("/dev/fd")
		if err != nil {
			t.Skip("cannot list open files:", err)
		}
		defer d.Close()
		fds, err := d.Readdirnames(-1)
		if err != nil {
			t.Skip("cannot list open files:", err)
		}
		return len(fds)
	}
	before := openFiles()
	for range 5 {
		dir := t.TempDir()
		app := baseplate.New(baseplate.Config{DataDir: filepath.Join(dir, "data"), DistDir: filepath.Join(dir, "dist"), Theme: testTheme,
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
		app.Use(failingPlugin{})
		if err := app.Setup(context.Background()); err == nil {
			t.Fatal("Setup succeeded with a failing plugin")
		}
	}
	if after := openFiles(); after >= before+5 {
		t.Errorf("open files grew from %d to %d over 5 failed setups: the database was not closed", before, after)
	}
}
