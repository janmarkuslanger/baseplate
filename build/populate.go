package build

import (
	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/render"
	"github.com/janmarkuslanger/baseplate/storage"
)

type refKey struct {
	typ string
	id  int64
}

// resolver turns relation IDs into render.Ref values, one level deep.
type resolver struct {
	s     *site
	urls  map[refKey]string // published routable entries
	cache map[refKey]*render.Ref
	lists map[string][]storage.Entry
}

func (r *resolver) published(typ string) []storage.Entry {
	if l, ok := r.lists[typ]; ok {
		return l
	}
	l, _ := r.s.Published(typ)
	r.lists[typ] = l
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
		// link to pages missing from the build.
		if u, ok := r.urls[k]; ok {
			if e, err := r.s.b.Reg.Store.GetEntry(r.s.ctx, typ, id); err == nil {
				ref = &render.Ref{ID: id, Type: typ, URL: u, Data: e.Data}
			}
		}
	} else if e, err := r.s.b.Reg.Store.GetEntry(r.s.ctx, typ, id); err == nil {
		ref = &render.Ref{ID: id, Type: typ, Data: e.Data}
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
			for _, e := range r.published(src.Name) {
				if points(e, f, p.Entry.ID) {
					if ref := r.ref(src.Name, e.ID); ref != nil {
						refs = append(refs, ref)
					}
				}
			}
			p.Data[f.Inverse] = refs
		}
	}
}

func points(e storage.Entry, f content.Field, id int64) bool {
	if n, ok := content.RefID(e.Data[f.Name]); ok {
		return n == id
	}
	list, _ := e.Data[f.Name].([]any)
	for _, it := range list {
		if n, _ := content.RefID(it); n == id {
			return true
		}
	}
	return false
}
