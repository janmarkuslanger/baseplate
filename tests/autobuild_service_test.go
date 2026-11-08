package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/janmarkuslanger/baseplate"
	"github.com/janmarkuslanger/baseplate/entries"
	"github.com/janmarkuslanger/baseplate/fields"
)

// syncBuffer is a log sink the auto-build goroutine may write to.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func autoBuildApp(t *testing.T, logs *syncBuffer) (*baseplate.App, string) {
	t.Helper()
	t.Setenv("BASEPLATE_BUILD_ON_PUBLISH", "1")
	dir := t.TempDir()
	app := baseplate.New(baseplate.Config{DataDir: filepath.Join(dir, "data"), DistDir: filepath.Join(dir, "dist"), Theme: testTheme,
		Logger: slog.New(slog.NewTextHandler(logs, nil))})
	app.Use(fields.CoreBlocks{})
	if err := app.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	return app, filepath.Join(dir, "dist")
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// Plugins save through plugin.Services.Entries, not the admin handlers;
// their published changes must trigger the auto-build too.
func TestBuildOnPublishThroughEntriesService(t *testing.T) {
	app, dist := autoBuildApp(t, &syncBuffer{})
	t.Cleanup(func() { app.Close() })
	ctx := context.Background()
	svc := app.Registry.Entries

	if _, err := svc.Save(ctx, entries.Save{Type: fields.SettingsType, Data: map[string]any{"siteName": "Acme", "baseURL": "https://acme.test"}, Publish: true}); err != nil {
		t.Fatal(err)
	}
	res, err := svc.Save(ctx, entries.Save{Type: fields.PageType, Data: map[string]any{"title": "Live", "slug": "news"}, Publish: true})
	if err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(dist, "news", "index.html")
	waitFor(t, func() bool {
		b, err := os.ReadFile(page)
		return err == nil && strings.Contains(string(b), "<h1>Live</h1>")
	}, "the published page to be built")

	if _, err := svc.Delete(ctx, fields.PageType, res.Entry.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _, err := os.Stat(page); return os.IsNotExist(err) }, "the deleted page to leave the site")
}

// Close waits for a running auto-build, so it never runs against the
// closed store.
func TestCloseStopsAutoBuildBeforeStore(t *testing.T) {
	t.Setenv("BASEPLATE_BUILD_ON_PUBLISH", "1")
	logs := &syncBuffer{}
	app, _, c := startApp(t, logs)
	c.login()
	save := func(typ string, data map[string]any) {
		b, _ := json.Marshal(data)
		if res, _ := c.do("POST", "/admin/c/"+typ+"/save", url.Values{"csrf": {c.csrf}, "publish": {"1"}, "data": {string(b)}}); res.StatusCode != 303 {
			t.Fatalf("save %s: %d", typ, res.StatusCode)
		}
	}
	save(fields.SettingsType, map[string]any{"siteName": "Acme", "baseURL": "https://acme.test"})
	for i := range 30 {
		save(fields.PageType, map[string]any{"title": "P", "slug": fmt.Sprintf("p%d", i)})
	}
	if err := app.Close(); err != nil { // like SIGTERM while the last save's build runs
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // a build still running would log now
	if l := logs.String(); strings.Contains(l, "auto build") {
		t.Errorf("auto build ran after Close:\n%s", l)
	}
}
