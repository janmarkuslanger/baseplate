package admin

import (
	"context"
	"net/http"
	"time"

	"github.com/janmarkuslanger/baseplate/build"
	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/settings"
	"github.com/janmarkuslanger/baseplate/storage"
)

type buildStatus struct {
	Result build.Result
	Err    string
	At     time.Time
}

type typeCount struct {
	Type             *content.Type
	Total, Published int
}

func (a *Admin) dashboard(w http.ResponseWriter, r *http.Request) {
	var counts []typeCount
	for _, t := range a.Reg.Schema.Types() {
		if t.Hidden || t.Singleton {
			continue
		}
		_, total, err := a.store().ListEntries(r.Context(), storage.ListQuery{Type: t.Name, Limit: 1})
		if err != nil {
			a.fail(w, err)
			return
		}
		_, pub, _ := a.store().ListEntries(r.Context(), storage.ListQuery{Type: t.Name, Status: storage.StatusPublished, Limit: 1})
		counts = append(counts, typeCount{Type: t, Total: total, Published: pub})
	}
	cfg, _ := settings.Load(r.Context(), a.store())
	a.buildMu.Lock()
	last := a.lastBuild
	a.buildMu.Unlock()
	a.render(w, r, http.StatusOK, a.pages["dashboard"], view{Title: a.t("nav.dashboard"), Active: "dashboard", Page: map[string]any{
		"Counts": counts, "Build": last, "SettingsMissing": cfg.BaseURL == "",
	}})
}

// contentChanged starts a background build if BuildOnPublish is set.
// Changes during a running build cause exactly one more build afterwards.
func (a *Admin) contentChanged() {
	if !a.BuildOnPublish {
		return
	}
	a.autoMu.Lock()
	if a.autoRunning {
		a.autoPending = true
		a.autoMu.Unlock()
		return
	}
	a.autoRunning = true
	a.autoMu.Unlock()
	go func() {
		for {
			res, err := a.Builder.Build(context.Background())
			st := &buildStatus{Result: res, At: time.Now()}
			if err != nil {
				st.Err = err.Error()
				a.Logger.Error("auto build", "err", err)
			}
			a.buildMu.Lock()
			a.lastBuild = st
			a.buildMu.Unlock()
			a.autoMu.Lock()
			if !a.autoPending {
				a.autoRunning = false
				a.autoMu.Unlock()
				return
			}
			a.autoPending = false
			a.autoMu.Unlock()
		}
	}()
}

func (a *Admin) build(w http.ResponseWriter, r *http.Request) {
	res, err := a.Builder.Build(r.Context())
	st := &buildStatus{Result: res, At: time.Now()}
	if err != nil {
		st.Err = err.Error()
	}
	a.buildMu.Lock()
	a.lastBuild = st
	a.buildMu.Unlock()
	if err := a.pages["dashboard"].ExecuteTemplate(w, "build-status", st); err != nil {
		a.Logger.Error("build status", "err", err)
	}
}
