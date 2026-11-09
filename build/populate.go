package build

import (
	"errors"
	"slices"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/render"
	"github.com/janmarkuslanger/baseplate/storage"
)

type refKey struct {
	typ string
	id  int64
}

// resolver turns relation IDs into render.Ref values, one level deep.
// The first storage error is kept in err and fails the build; a missing
// target is not an error (the relation simply resolves to nothing).
type resolver struct {
	s     *site
	urls  map[refKey]string // published routable entries
	cache map[refKey]*render.Ref
	lists map[string][]storage.Entry
	byID  map[refKey]*storage.Entry // index of lists
	inv   map[invKey]map[int64][]int64
	err   error
}

// invKey names one inverse relation: the source type and its field.
type invKey struct{ src, field string }

func (r *resolver) fail(err error) {
	if r.err == nil {
		r.err = err
	}
}

func (r *resolver) published(typ string) []storage.Entry {
	if l, ok := r.lists[typ]; ok {
		return l
	}
	l, err := r.s.Published(typ)
	if err != nil {
		r.fail(err)
	}
	r.lists[typ] = l
	for i := range l {
		r.byID[refKey{typ, l[i].ID}] = &l[i]
	}
	return l
}

func (r *resolver) ref(typ string, id int64) *render.Ref {
	k := refKey{typ, id}
	if ref, ok := r.cache[k]; ok {
		return ref
	}
	var ref *render.Ref
	t, _ := r.s.b.Reg.Schema.Type(typ)
	if t != nil && t.Routable {
		// Routable targets only resolve when published, so templates never
		// link to pages missing from the build. They are already loaded.
		if u, ok := r.urls[k]; ok {
			r.published(typ)
			if e := r.byID[k]; e != nil {
				ref = &render.Ref{ID: id, Type: typ, URL: u, Data: e.Data}
			}
		}
	} else {
		// Like routable targets, unpublished entries resolve to nothing.
		e, err := r.s.b.Reg.Store.GetEntry(r.s.ctx, typ, id, "", storage.Published)
		switch {
		case err == nil:
			ref = &render.Ref{ID: id, Type: typ, Data: e.Data}
		case !errors.Is(err, storage.ErrNotFound):
			r.fail(err)
		}
	}
	r.cache[k] = ref
	return ref
}

// populate returns a copy of data with relation fields resolved, recursing
// into groups, lists and blocks.
func (r *resolver) populate(fs []content.Field, data map[string]any) map[string]any {
	out := make(map[string]any, len(data))
	for k, v := range data {
		out[k] = v
	}
	for _, f := range fs {
		k, _ := r.s.b.Reg.Schema.Kind(f.Kind)
		v := data[f.Name]
		switch k.Storage {
		case content.StoreRelation:
			if f.Many {
				refs := []*render.Ref{}
				list, _ := v.([]any)
				for _, it := range list {
					if id, ok := content.RefID(it); ok {
						if ref := r.ref(f.Ref, id); ref != nil {
							refs = append(refs, ref)
						}
					}
				}
				out[f.Name] = refs
			} else if id, ok := content.RefID(v); ok {
				if ref := r.ref(f.Ref, id); ref != nil {
					out[f.Name] = ref
				} else {
					delete(out, f.Name)
				}
			}
		case content.StoreGroup:
			if m, ok := v.(map[string]any); ok {
				out[f.Name] = r.populate(f.Fields, m)
			}
		case content.StoreList, content.StoreBlocks:
			list, _ := v.([]any)
			items := make([]any, 0, len(list))
			for _, it := range list {
				m, _ := it.(map[string]any)
				sub := f.Fields
				if k.Storage == content.StoreBlocks {
					name, _ := m[content.BlockTypeKey].(string)
					b, _ := r.s.b.Reg.Schema.Block(name)
					sub = b.Fields
				}
				items = append(items, r.populate(sub, m))
			}
			out[f.Name] = items
		}
	}
	return out
}

// inverse adds read-only inverse fields: for every top-level relation field
// with Inverse set, the target gets the published sources pointing at it.
func (r *resolver) inverse(p *render.Page) {
	for _, src := range r.s.b.Reg.Schema.Types() {
		for _, f := range src.Fields {
			if f.Inverse == "" || f.Ref != p.Type.Name {
				continue
			}
			refs := []*render.Ref{}
			for _, id := range r.sources(src.Name, f)[p.Entry.ID] {
				if ref := r.ref(src.Name, id); ref != nil {
					refs = append(refs, ref)
				}
			}
			p.Data[f.Inverse] = refs
		}
	}
}

// sources maps each target ID to the IDs of the published entries of src
// whose field f points at it, in list order. Built once per field, so
// inverse stays linear in the number of entries.
func (r *resolver) sources(src string, f content.Field) map[int64][]int64 {
	k := invKey{src, f.Name}
	if m, ok := r.inv[k]; ok {
		return m
	}
	m := map[int64][]int64{}
	for _, e := range r.published(src) {
		for _, id := range targets(e, f) {
			m[id] = append(m[id], e.ID)
		}
	}
	r.inv[k] = m
	return m
}

// targets returns the distinct IDs the relation field f of e points at.
func targets(e storage.Entry, f content.Field) []int64 {
	if n, ok := content.RefID(e.Data[f.Name]); ok {
		return []int64{n}
	}
	list, _ := e.Data[f.Name].([]any)
	var ids []int64
	for _, it := range list {
		if n, _ := content.RefID(it); n != 0 && !slices.Contains(ids, n) {
			ids = append(ids, n)
		}
	}
	return ids
}

func points(e storage.Entry, f content.Field, id int64) bool {
	return slices.Contains(targets(e, f), id)
}
