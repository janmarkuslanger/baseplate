package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/storage"
)

const (
	optionLimit = 20  // matches per search
	maxOptionID = 200 // IDs per label lookup
)

// relationOptions feeds the relation pickers, which never hold a whole
// type in the page. With ids it returns the labels of those entries; with
// q (possibly empty) the first optionLimit entries matching the search.
func (a *Admin) relationOptions(w http.ResponseWriter, r *http.Request) {
	t, ok := a.contentType(w, r)
	if !ok {
		return
	}
	label := func(e storage.Entry) content.Option {
		return content.Option{Value: strconv.FormatInt(e.ID, 10), Label: fmt.Sprint(a.orDash(e.Data[t.Title()]))}
	}
	out := []content.Option{}
	if raw := r.URL.Query().Get("ids"); raw != "" {
		for i, s := range strings.Split(raw, ",") {
			id, err := strconv.ParseInt(s, 10, 64)
			if err != nil || id <= 0 || i >= maxOptionID {
				continue
			}
			e, err := a.store().GetEntry(r.Context(), t.Name, id, "", storage.Draft)
			if errors.Is(err, storage.ErrNotFound) {
				continue
			}
			if err != nil {
				a.fail(w, err)
				return
			}
			out = append(out, label(*e))
		}
	} else {
		list, _, err := a.store().ListEntries(r.Context(), storage.ListQuery{Type: t.Name, Version: storage.Draft, Search: r.URL.Query().Get("q"), Limit: optionLimit})
		if err != nil {
			a.fail(w, err)
			return
		}
		for _, e := range list {
			out = append(out, label(e))
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
