package tests

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/janmarkuslanger/baseplate/plugin"
)

// auditPlugin uppercases titles before saving, refuses "verboten" and
// records saved entries.
type auditPlugin struct{ saved *[]string }

func (auditPlugin) Name() string { return "audit" }
func (p auditPlugin) Register(r *plugin.Registry) error {
	plugin.EntryBeforeSave.On(r, func(_ context.Context, e *plugin.EntryEvent) error {
		title, _ := e.Entry.Data["title"].(string)
		if title == "verboten" {
			return errors.New("Titel nicht erlaubt")
		}
		e.Entry.Data["title"] = strings.ToUpper(title)
		return nil
	})
	plugin.EntryAfterSave.On(r, func(_ context.Context, e *plugin.EntryEvent) error {
		*p.saved = append(*p.saved, e.Type.Name+":"+e.Entry.Data["title"].(string))
		if e.UserID == 0 {
			return errors.New("user missing")
		}
		return nil
	})
	return nil
}

func TestEntryHooks(t *testing.T) {
	var saved []string
	_, c := newTestApp(t, auditPlugin{saved: &saved})
	c.login()
	save := func(title string) (int, string) {
		b, _ := json.Marshal(map[string]any{"title": title, "slug": strings.ToLower(title)})
		res, body := c.do("POST", "/admin/c/page/save", url.Values{"csrf": {c.csrf}, "status": {"draft"}, "data": {string(b)}})
		return res.StatusCode, body
	}
	if code, _ := save("hallo"); code != 303 {
		t.Fatalf("save: %d", code)
	}
	if len(saved) != 1 || saved[0] != "page:HALLO" {
		t.Errorf("afterSave saw %v", saved)
	}
	if code, body := save("verboten"); code != 422 || !strings.Contains(body, "Titel nicht erlaubt") {
		t.Errorf("beforeSave error must abort with a message: %d", code)
	}
	if len(saved) != 1 {
		t.Error("aborted save must not run afterSave")
	}
}
