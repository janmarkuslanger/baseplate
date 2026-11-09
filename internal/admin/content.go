package admin

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/janmarkuslanger/baseplate/build"
	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/entries"
	"github.com/janmarkuslanger/baseplate/fields"
	"github.com/janmarkuslanger/baseplate/i18n"
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
	URL   string // routable types only
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
	status := r.URL.Query().Get("status")
	q := storage.ListQuery{Type: t.Name, Version: storage.Draft, Search: r.URL.Query().Get("q"), Limit: perPage, Offset: (pageNo - 1) * perPage}
	switch status {
	case "published":
		q.Published = storage.OnlyPublished
	case "draft":
		q.Published = storage.OnlyUnpublished
	}
	entries, total, err := a.store().ListEntries(r.Context(), q)
	if err != nil {
		a.fail(w, err)
		return
	}
	rows := make([]row, len(entries))
	for i, e := range entries {
		rows[i] = row{Entry: e, Title: fmt.Sprint(a.orDash(e.Data[t.Title()]))}
		if t.Routable {
			rows[i].URL, _ = build.EntryURL(t, &e, a.publishedParent(r.Context(), t.Name))
		}
	}
	a.render(w, r, http.StatusOK, a.pages["list"], view{Title: a.t(t.LabelPlural), Active: "c/" + t.Name, Flash: a.flash(r), Page: map[string]any{
		"Type": t, "Rows": rows, "Total": total, "Query": q, "Status": status, "PageNo": pageNo,
		"HasNext": pageNo*perPage < total, "HasPrev": pageNo > 1, "Prev": pageNo - 1, "Next": pageNo + 1,
	}})
}

// publishedParent looks up parents like the build does: published only.
func (a *Admin) publishedParent(ctx context.Context, typ string) func(int64) *storage.Entry {
	return func(id int64) *storage.Entry {
		e, err := a.store().GetEntry(ctx, typ, id, "", storage.Published)
		if err != nil {
			return nil
		}
		return e
	}
}

func (a *Admin) orDash(v any) any {
	if v == nil || v == "" {
		return a.t("entry.untitled")
	}
	return v
}

func (a *Admin) flash(r *http.Request) string {
	switch f := r.URL.Query().Get("flash"); f {
	case "saved", "published", "unpublished", "deleted":
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
				return a.t(cmp.Or(f.Label, f.Name))
			}
		}
		return ref.Field
	}
	if ref.Block != "" {
		if b, ok := a.Reg.Schema.Block(ref.Block); ok {
			return a.t(b.Label) + " › " + label(b.Fields)
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
		u := usedIn{Type: a.t(rt.Label), Field: ref.Field, URL: fmt.Sprintf("/admin/c/%s/%d", rt.Name, ref.ID), Nested: ref.Nested}
		u.Field = a.relationLabel(rt, ref)
		if src, err := a.store().GetEntry(r.Context(), rt.Name, ref.ID, "", storage.Draft); err == nil {
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
	e := &storage.Entry{Type: t.Name, Version: storage.Draft, Data: map[string]any{}}
	switch {
	case t.Singleton:
		got, err := a.store().GetEntryBySlug(r.Context(), t.Name, t.Name, "", storage.Draft)
		if err == nil {
			e = got
		} else if !errors.Is(err, storage.ErrNotFound) {
			a.fail(w, err)
			return
		}
	case r.PathValue("id") != "":
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		got, err := a.store().GetEntry(r.Context(), t.Name, id, "", storage.Draft)
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
	// Skeletons of every block, also restricted ones: the fields that offer
	// them pick from this map.
	for _, b := range a.Reg.Schema.AllBlocks() {
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
	in := entries.Save{Type: t.Name, Data: raw, Publish: r.FormValue("publish") != ""}
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
		e := &storage.Entry{Type: t.Name, ID: in.ID, Version: storage.Draft, Data: raw}
		if in.ID > 0 {
			if got, err := a.store().GetEntry(r.Context(), t.Name, in.ID, "", storage.Draft); err == nil {
				e.PublishedAt, e.Changed, e.UpdatedAt = got.PublishedAt, got.Changed, got.UpdatedAt
			}
		}
		a.renderEdit(w, r, http.StatusUnprocessableEntity, t, e, raw, errs)
		return
	case err != nil:
		a.fail(w, err)
		return
	}
	fl := "saved"
	if in.Publish {
		fl = "published"
	}
	target := fmt.Sprintf("/admin/c/%s/%d?flash=%s", t.Name, res.Entry.ID, fl)
	if t.Singleton {
		target = "/admin/c/" + t.Name + "?flash=" + fl
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// unpublish takes an entry off the site; its draft stays.
func (a *Admin) unpublish(w http.ResponseWriter, r *http.Request) {
	t, ok := a.contentType(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_, err := a.Reg.Entries.Unpublish(r.Context(), t.Name, id, "")
	if errors.Is(err, entries.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/c/%s/%d?flash=unpublished", t.Name, id), http.StatusSeeOther)
}

func (a *Admin) delete(w http.ResponseWriter, r *http.Request) {
	t, ok := a.contentType(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_, err := a.Reg.Entries.Delete(r.Context(), t.Name, id)
	if errors.Is(err, entries.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if ref, ok := errors.AsType[*storage.ReferencedError](err); ok {
		// Still in use: show the editor with the referrers instead.
		e, gerr := a.store().GetEntry(r.Context(), t.Name, id, "", storage.Draft)
		if gerr != nil {
			a.fail(w, gerr)
			return
		}
		a.renderEdit(w, r, http.StatusConflict, t, e, e.Data, content.Errors{"_": i18n.M("edit.delete.usedIn", len(ref.Referrers))})
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	http.Redirect(w, r, "/admin/c/"+t.Name+"?flash=deleted", http.StatusSeeOther)
}
