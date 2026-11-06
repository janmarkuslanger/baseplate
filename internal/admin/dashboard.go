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
	last := a.lastBuild.Load()
	a.render(w, r, http.StatusOK, a.pages["dashboard"], view{Title: a.t("nav.dashboard"), Active: "dashboard", Page: map[string]any{
		"Counts": counts, "Build": last, "SettingsMissing": cfg.BaseURL == "",
	}})
}

// contentChanged requests a background build if BuildOnPublish is set.
// Requests during a running build result in exactly one more build.
func (a *Admin) contentChanged() {
	if a.rebuild == nil {
		return
	}
	select {
	case a.rebuild <- struct{}{}:
	default: // a build is already pending
	}
}

// autoBuild runs one build per pending request, for the app's lifetime.
func (a *Admin) autoBuild() {
	for range a.rebuild {
		res, err := a.Builder.Build(context.Background())
		if err != nil {
			a.Logger.Error("auto build", "err", err)
		}
		a.recordBuild(res, err)
	}
}

func (a *Admin) build(w http.ResponseWriter, r *http.Request) {
	res, err := a.Builder.Build(r.Context())
	st := a.recordBuild(res, err)
	if err := a.pages["dashboard"].ExecuteTemplate(w, "build-status", st); err != nil {
		a.Logger.Error("build status", "err", err)
	}
}

// recordBuild stores the outcome shown on the dashboard.
func (a *Admin) recordBuild(res build.Result, err error) *buildStatus {
	st := &buildStatus{Result: res, At: time.Now()}
	if err != nil {
		st.Err = err.Error()
	}
	a.lastBuild.Store(st)
	return st
}
