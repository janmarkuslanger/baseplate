package tests

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/janmarkuslanger/baseplate/storage"
)

// TestAdminFailsWhenSettingsCannotBeLoaded checks that a broken settings
// table surfaces as an error instead of pages rendered with zero settings.
func TestAdminFailsWhenSettingsCannotBeLoaded(t *testing.T) {
	var logs bytes.Buffer
	app, _, c := startApp(t, &logs, blogPlugin{})
	c.login()
	db := app.Registry.Store.(storage.Tables).DB()
	if _, err := db.Exec(`DROP TABLE ` + storage.EntryTable("settings")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/admin/", "/admin/c/post/new", "/admin/media"} {
		if res, _ := c.do("GET", path, nil); res.StatusCode != http.StatusInternalServerError {
			t.Errorf("GET %s: status %d, want 500", path, res.StatusCode)
		}
	}
	if !strings.Contains(logs.String(), "load settings") {
		t.Errorf("settings error not logged: %q", logs.String())
	}
}

// failingPublished fails every lookup of a published entry.
type failingPublished struct{ storage.Store }

func (s failingPublished) GetEntry(ctx context.Context, typ string, id int64, locale string, v storage.Version) (*storage.Entry, error) {
	if v == storage.Published {
		return nil, errors.New("disk on fire")
	}
	return s.Store.GetEntry(ctx, typ, id, locale, v)
}

// TestListFailsWhenParentLookupFails checks that a storage error while
// building {parents} URLs is reported instead of showing wrong URLs.
func TestListFailsWhenParentLookupFails(t *testing.T) {
	var logs bytes.Buffer
	app, _, c := startApp(t, &logs)
	c.login()
	res, _ := c.save("page", "published", map[string]any{"title": "Parent", "slug": "parent"})
	id := strings.Split(strings.TrimPrefix(res.Header.Get("Location"), "/admin/c/page/"), "?")[0]
	c.save("page", "published", map[string]any{"title": "Child", "slug": "child", "parent": id})
	app.Registry.Store = failingPublished{app.Registry.Store}
	if res, _ := c.do("GET", "/admin/c/page", nil); res.StatusCode != http.StatusInternalServerError {
		t.Errorf("status %d, want 500", res.StatusCode)
	}
	if !strings.Contains(logs.String(), "disk on fire") {
		t.Errorf("error not logged: %q", logs.String())
	}
}
