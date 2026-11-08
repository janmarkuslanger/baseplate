package forms

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/plugin"
	"github.com/janmarkuslanger/baseplate/storage"
)

type submission struct {
	ID        int64
	FormID    int64
	FormTitle string
	Mailed    bool
	CreatedAt time.Time
	Preview   string
	Values    []labeled
}

type labeled struct{ Label, Value string }

func (p *Plugin) adminSubmissions(w http.ResponseWriter, r *http.Request, ui plugin.AdminUI) {
	ctx := r.Context()
	db := p.reg.Store.DB()

	if r.Method == http.MethodPost {
		id, _ := strconv.ParseInt(r.FormValue("delete"), 10, 64)
		if _, err := db.ExecContext(ctx, `DELETE FROM form_submissions WHERE id = ?`, id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/admin/p/forms/submissions", http.StatusSeeOther)
		return
	}

	forms, _, err := p.reg.Store.ListEntries(ctx, storage.ListQuery{Type: TypeName, Version: storage.Draft})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	titles := map[int64]string{}
	var options []content.Option
	for _, f := range forms {
		titles[f.ID] = str(f.Data["title"])
		options = append(options, content.Option{Value: strconv.FormatInt(f.ID, 10), Label: str(f.Data["title"])})
	}
	page := map[string]any{"CSRF": ui.CSRFToken(r), "Forms": options, "FormFilter": r.URL.Query().Get("form")}

	if id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64); id > 0 {
		var rs rawSubmission
		err := db.QueryRowContext(ctx, `SELECT id, form_id, data, mailed, created_at FROM form_submissions WHERE id = ?`, id).
			Scan(&rs.id, &rs.formID, &rs.data, &rs.mailed, &rs.created)
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		page["Detail"] = p.label(rs, titles)
		ui.Render(w, r, "submissions.html", page)
		return
	}

	query := `SELECT id, form_id, data, mailed, created_at FROM form_submissions`
	var args []any
	if f, _ := strconv.ParseInt(r.URL.Query().Get("form"), 10, 64); f > 0 {
		query += ` WHERE form_id = ?`
		args = append(args, f)
	}
	rows, err := db.QueryContext(ctx, query+` ORDER BY created_at DESC, id DESC LIMIT 200`, args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Read all rows before labeling: labeling queries the store, and SQLite
	// runs on a single connection that an open cursor would block.
	var raws []rawSubmission
	for rows.Next() {
		var rs rawSubmission
		if err := rows.Scan(&rs.id, &rs.formID, &rs.data, &rs.mailed, &rs.created); err != nil {
			rows.Close()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		raws = append(raws, rs)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var list []submission
	for _, rs := range raws {
		list = append(list, *p.label(rs, titles))
	}
	page["Rows"] = list
	ui.Render(w, r, "submissions.html", page)
}

type rawSubmission struct {
	id, formID, created int64
	data                string
	mailed              int
}

// label turns a stored row into a view, using the current form definition
// for field labels.
func (p *Plugin) label(rs rawSubmission, titles map[int64]string) *submission {
	s := submission{ID: rs.id, FormID: rs.formID, Mailed: rs.mailed == 1, CreatedAt: time.Unix(rs.created, 0)}
	data := rs.data
	s.FormTitle = titles[s.FormID]
	if s.FormTitle == "" {
		s.FormTitle = "forms.deletedForm"
	}
	values := map[string]string{}
	json.Unmarshal([]byte(data), &values)

	// Label values with the current form definition; keep unknown keys.
	// Checkbox values (stored as yes/no in the site language) are left out
	// of the preview.
	seen := map[string]bool{}
	checkbox := map[string]bool{}
	if def, err := p.load(context.Background(), s.FormID, storage.Draft); err == nil {
		for _, f := range def.Fields {
			if v, ok := values[f.Name]; ok {
				s.Values = append(s.Values, labeled{f.Label, v})
				seen[f.Name] = true
				checkbox[f.Label] = f.Type == "checkbox"
			}
		}
	}
	for k, v := range values {
		if !seen[k] {
			s.Values = append(s.Values, labeled{k, v})
		}
	}
	var parts []string
	for _, v := range s.Values {
		if v.Value != "" && !checkbox[v.Label] {
			parts = append(parts, v.Value)
		}
	}
	s.Preview = strings.Join(parts, " · ")
	if utf8.RuneCountInString(s.Preview) > 90 {
		s.Preview = string([]rune(s.Preview)[:90]) + "…"
	}
	return &s
}
