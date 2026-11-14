package tests

import (
	"bytes"
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
