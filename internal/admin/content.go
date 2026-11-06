package admin

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/entries"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/internal/auth"
	"github.com/janmarkuslanger/baseplate/settings"
	"github.com/janmarkuslanger/baseplate/storage"
)

func (a *Admin) contentType(w http.ResponseWriter, r *http.Request) (*content.Type, bool) {
	t, ok := a.Reg.Schema.Type(r.PathValue("type"))
	if !ok {
		http.NotFound(w, r)
	}
	return t, ok
}

type row struct {
	Entry storage.Entry
	Title string
}

func (a *Admin) list(w http.ResponseWriter, r *http.Request) {
	t, ok := a.contentType(w, r)
	if !ok {
		return
	}
	if t.Singleton {
		a.edit(w, r)
		return
	}
	const perPage = 50
	pageNo, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if pageNo < 1 {
		pageNo = 1
	}
	q := storage.ListQuery{Type: t.Name, Status: r.URL.Query().Get("status"), Search: r.URL.Query().Get("q"), Limit: perPage, Offset: (pageNo - 1) * perPage}
	entries, total, err := a.store().ListEntries(r.Context(), q)
	if err != nil {
		a.fail(w, err)
		return
	}
	rows := make([]row, len(entries))
	for i, e := range entries {
		rows[i] = row{Entry: e, Title: fmt.Sprint(a.orDash(e.Data[t.Title()]))}
	}
	a.render(w, r, http.StatusOK, a.pages["list"], view{Title: a.t(t.LabelPlural), Active: "c/" + t.Name, Flash: a.flash(r), Page: map[string]any{
		"Type": t, "Rows": rows, "Total": total, "Query": q, "PageNo": pageNo,
		"HasNext": pageNo*perPage < total, "HasPrev": pageNo > 1, "Prev": pageNo - 1, "Next": pageNo + 1,
	}})
}

func (a *Admin) orDash(v any) any {
	if v == nil || v == "" {
		return a.t("entry.untitled")
	}
	return v
}

func (a *Admin) flash(r *http.Request) string {
	switch f := r.URL.Query().Get("flash"); f {
	case "saved", "published", "deleted":
		return a.t("flash." + f)
	}
	return ""
}

type editPage struct {
	Type     *content.Type
	Entry    *storage.Entry
	Data     map[string]any
	Errors   map[string]string // translated, by field path
	Blocks   map[string]any    // block name -> empty item, for the "add block" menu
	Labels   map[string]string
	Settings map[string]any
	IsNew    bool
	UsedIn   []usedIn
}

// relationLabel names the relation field of a referrer: its label, or
// "<block> › <field>" when the field sits inside a block.
func (a *Admin) relationLabel(rt *content.Type, ref storage.Referrer) string {
	label := func(fs []content.Field) string {
		for _, f := range fs {
			if f.Name == ref.Field {
				return cmp.Or(f.Label, f.Name)
			}
		}
		return ref.Field
	}
	if ref.Block != "" {
		if b, ok := a.Reg.Schema.Block(ref.Block); ok {
			return b.Label + " › " + label(b.Fields)
		}
	}
	return label(rt.Fields)
}

// usedIn is an entry whose relation points at the edited entry.
type usedIn struct {
	Type, Title, Field, URL string
	Nested                  bool
}

func (a *Admin) usedIn(r *http.Request, t *content.Type, e *storage.Entry) []usedIn {
	if e.ID == 0 {
		return nil
	}
	refs, err := a.store().Referrers(r.Context(), t.Name, e.ID)
	if err != nil {
		a.Logger.Error("referrers", "err", err)
		return nil
	}
	out := make([]usedIn, 0, len(refs))
	for _, ref := range refs {
		rt, ok := a.Reg.Schema.Type(ref.Type)
		if !ok {
			continue
		}
		u := usedIn{Type: rt.Label, Field: ref.Field, URL: fmt.Sprintf("/admin/c/%s/%d", rt.Name, ref.ID), Nested: ref.Nested}
		u.Field = a.relationLabel(rt, ref)
		if src, err := a.store().GetEntry(r.Context(), rt.Name, ref.ID); err == nil {
			u.Title = fmt.Sprint(a.orDash(src.Data[rt.Title()]))
		}
		out = append(out, u)
	}
	return out
}

func (a *Admin) edit(w http.ResponseWriter, r *http.Request) {
	t, ok := a.contentType(w, r)
	if !ok {
		return
	}
	e := &storage.Entry{Type: t.Name, Status: storage.StatusDraft, Data: map[string]any{}}
	switch {
	case t.Singleton:
		got, err := a.store().GetEntryBySlug(r.Context(), t.Name, t.Name)
		if err == nil {
			e = got
		} else if !errors.Is(err, storage.ErrNotFound) {
			a.fail(w, err)
			return
		}
	case r.PathValue("id") != "":
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		got, err := a.store().GetEntry(r.Context(), t.Name, id)
		if errors.Is(err, storage.ErrNotFound) || (err == nil && got.Type != t.Name) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			a.fail(w, err)
			return
		}
		e = got
	}
	a.renderEdit(w, r, http.StatusOK, t, e, e.Data, nil)
}

func (a *Admin) renderEdit(w http.ResponseWriter, r *http.Request, status int, t *content.Type, e *storage.Entry, data map[string]any, errs content.Errors) {
	blocks := map[string]any{}
	labels := map[string]string{}
	for _, b := range a.Reg.Schema.Blocks(content.Field{}) {
		item := skeleton(b.Fields, map[string]any{})
		item[content.BlockTypeKey] = b.Name
		blocks[b.Name] = item
		labels[b.Name] = a.t(b.Label)
	}
	cfg, _ := settings.Load(r.Context(), a.store())
	title := a.t(t.Label)
	if e.ID != 0 && !t.Singleton {
		title = fmt.Sprint(a.orDash(e.Data[t.Title()]))
	}
	a.render(w, r, status, a.pages["edit"], view{Title: title, Active: "c/" + t.Name, Flash: a.flash(r), Page: editPage{
		Type: t, Entry: e, Data: skeleton(t.Fields, data), Errors: a.translateErrors(errs), Blocks: blocks, Labels: labels,
		Settings: cfg.Data, IsNew: e.ID == 0, UsedIn: a.usedIn(r, t, e),
	}})
}

func (a *Admin) translateErrors(errs content.Errors) map[string]string {
	if len(errs) == 0 {
		return nil
	}
	out := make(map[string]string, len(errs))
	for path, err := range errs {
		out[path] = a.tr(err)
	}
	return out
}

// skeleton fills in empty groups and lists so the Alpine editor can bind
// nested fields without null checks.
func skeleton(fs []content.Field, data map[string]any) map[string]any {
	if data == nil {
		data = map[string]any{}
	}
	for _, f := range fs {
		switch f.Kind {
		case fields.Group:
			m, _ := data[f.Name].(map[string]any)
			data[f.Name] = skeleton(f.Fields, m)
		case fields.List, fields.Blocks:
			if _, ok := data[f.Name].([]any); !ok {
				data[f.Name] = []any{}
			}
		case fields.Bool:
			if _, ok := data[f.Name].(bool); !ok {
				data[f.Name] = false
			}
		default:
			if _, ok := data[f.Name]; !ok {
				data[f.Name] = ""
			}
		}
	}
	return data
}

func (a *Admin) save(w http.ResponseWriter, r *http.Request) {
	t, ok := a.contentType(w, r)
	if !ok {
		return
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(r.FormValue("data")), &raw); err != nil {
		http.Error(w, a.t("error.badData"), http.StatusBadRequest)
		return
	}
	in := entries.Save{Type: t.Name, Data: raw, Publish: r.FormValue("status") == storage.StatusPublished}
	in.ID, _ = strconv.ParseInt(r.FormValue("id"), 10, 64)
	if cur := auth.FromContext(r.Context()); cur != nil {
		in.UserID = cur.User.ID
	}
	res, err := a.Reg.Entries.Save(r.Context(), in)
	var errs content.Errors
	switch {
	case errors.Is(err, entries.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.As(err, &errs):
		// Re-render with what the editor typed.
		e := &storage.Entry{Type: t.Name, ID: in.ID, Status: storage.StatusDraft, Data: raw}
		if in.ID > 0 {
			if got, err := a.store().GetEntry(r.Context(), t.Name, in.ID); err == nil {
				e.Status, e.UpdatedAt = got.Status, got.UpdatedAt
			}
		}
		a.renderEdit(w, r, http.StatusUnprocessableEntity, t, e, raw, errs)
		return
	case err != nil:
		a.fail(w, err)
		return
	}
	if res.Changed {
		a.contentChanged()
	}
	e := res.Entry
	fl := "saved"
	if e.Status == storage.StatusPublished && !t.Singleton {
		fl = "published"
	}
	target := fmt.Sprintf("/admin/c/%s/%d?flash=%s", t.Name, e.ID, fl)
	if t.Singleton {
		target = "/admin/c/" + t.Name + "?flash=saved"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (a *Admin) delete(w http.ResponseWriter, r *http.Request) {
	t, ok := a.contentType(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	changed, err := a.Reg.Entries.Delete(r.Context(), t.Name, id)
	if errors.Is(err, entries.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	if changed {
		a.contentChanged()
	}
	http.Redirect(w, r, "/admin/c/"+t.Name+"?flash=deleted", http.StatusSeeOther)
}

// refOptions lists entries of a type for relation pickers.
func (a *Admin) refOptions(typ string) []content.Option {
	t, ok := a.Reg.Schema.Type(typ)
	if !ok {
		return nil
	}
	list, _, err := a.store().ListEntries(contextBackground(), storage.ListQuery{Type: typ})
	if err != nil {
		return nil
	}
	out := make([]content.Option, 0, len(list))
	for _, e := range list {
		out = append(out, content.Option{Value: strconv.FormatInt(e.ID, 10), Label: fmt.Sprint(a.orDash(e.Data[t.Title()]))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}
