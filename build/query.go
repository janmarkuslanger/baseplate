package build

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/janmarkuslanger/baseplate/content"
	"github.com/janmarkuslanger/baseplate/render"
	"github.com/janmarkuslanger/baseplate/storage"
)

// query is a parsed call of the "entries" template function.
type query struct {
	sort          string
	desc          bool
	limit, offset int
	where         []cond    // field equals value
	has           []hasCond // relation field points to one of the IDs
}

type cond struct {
	field string
	value any
}

type hasCond struct {
	field string
	ids   []int64
}

// entries implements the "entries" template function: published entries
// of a type, with relations resolved one level deep and URLs set for
// routable types. Options (pairs, "where" and "has" take two values):
//
//	"sort" "field" | "-field"   ascending / descending; default: last updated first
//	"limit" n, "offset" n       0 or empty = no limit
//	"where" "field" value       field equals value
//	"has" "relation" refs       relation contains one of refs (entry IDs or refs);
//	                            empty refs = no filter, anything else fails
func (s *site) entries(typ string, opts ...any) ([]*render.Ref, error) {
	t, ok := s.b.Reg.Schema.Type(typ)
	if !ok {
		return nil, fmt.Errorf("entries: unknown type %q", typ)
	}
	q, err := s.parseQuery(t, opts)
	if err != nil {
		return nil, fmt.Errorf("entries %q: %w", typ, err)
	}
	list := slices.Clone(s.res.published(typ))
	if s.res.err != nil {
		return nil, s.res.err
	}
	list = slices.DeleteFunc(list, func(e storage.Entry) bool { return !q.match(e) })
	if q.sort != "" {
		slices.SortStableFunc(list, func(a, b storage.Entry) int {
			x, y := sortValue(a, q.sort), sortValue(b, q.sort)
			if empty(x) || empty(y) { // last in both directions
				return cmp.Compare(btoi(empty(x)), btoi(empty(y)))
			}
			if q.desc {
				return compare(y, x)
			}
			return compare(x, y)
		})
	}
	list = list[min(q.offset, len(list)):]
	if q.limit > 0 && q.limit < len(list) {
		list = list[:q.limit]
	}
	out := make([]*render.Ref, len(list))
	for i, e := range list {
		out[i] = &render.Ref{ID: e.ID, Type: typ, URL: s.res.urls[refKey{typ, e.ID}], Data: s.res.populate(t.Fields, e.Data)}
	}
	return out, s.res.err
}

func (s *site) parseQuery(t *content.Type, opts []any) (query, error) {
	var q query
	for i := 0; i < len(opts); {
		key, _ := opts[i].(string)
		n := 1
		if key == "where" || key == "has" {
			n = 2
		}
		if i+n >= len(opts) {
			return q, fmt.Errorf("option %q needs %d value(s)", key, n)
		}
		v := opts[i+1]
		switch key {
		case "sort":
			name, _ := v.(string)
			q.sort, q.desc = strings.TrimPrefix(name, "-"), strings.HasPrefix(name, "-")
			if q.sort != "slug" && t.Field(q.sort) == nil {
				return q, fmt.Errorf("sort: unknown field %q", q.sort)
			}
		case "limit", "offset":
			n, err := count(v)
			if err != nil {
				return q, fmt.Errorf("%s: %w", key, err)
			}
			if key == "limit" {
				q.limit = n
			} else {
				q.offset = n
			}
		case "where", "has":
			name, _ := v.(string)
			f := t.Field(name)
			if f == nil {
				return q, fmt.Errorf("%s: unknown field %q", key, name)
			}
			k, _ := s.b.Reg.Schema.Kind(f.Kind)
			if rel := k.Storage == content.StoreRelation; rel != (key == "has") || k.Storage.Container() {
				return q, fmt.Errorf("%s: field %q has kind %s (use \"where\" for plain fields, \"has\" for relations)", key, name, f.Kind)
			}
			if key == "where" {
				q.where = append(q.where, cond{name, opts[i+2]})
				break
			}
			ids, err := refIDs(opts[i+2])
			if err != nil {
				return q, fmt.Errorf("has %q: %w", name, err)
			}
			if len(ids) > 0 {
				q.has = append(q.has, hasCond{name, ids})
			}
		default:
			return q, fmt.Errorf("unknown option %v", opts[i])
		}
		i += n + 1
	}
	return q, nil
}

func (q query) match(e storage.Entry) bool {
	for _, c := range q.where {
		if compare(e.Data[c.field], c.value) != 0 {
			return false
		}
	}
	for _, c := range q.has {
		f := content.Field{Name: c.field}
		if !slices.ContainsFunc(c.ids, func(id int64) bool { return points(e, f, id) }) {
			return false
		}
	}
	return true
}

// refIDs reads one or many relation values (IDs, resolved refs). Empty
// values (nil, "") are skipped; anything else that is not an entry ID, such
// as a slug, is an error rather than a filter that silently matches all.
func refIDs(v any) ([]int64, error) {
	var ids []int64
	add := func(x any) error {
		if empty(x) {
			return nil
		}
		if r, ok := x.(*render.Ref); ok && r == nil {
			return nil
		}
		id, ok := content.RefID(x)
		if !ok {
			return fmt.Errorf("not an entry ID: %#v", x)
		}
		ids = append(ids, id)
		return nil
	}
	var err error
	switch l := v.(type) {
	case []*render.Ref:
		for _, r := range l {
			err = errors.Join(err, add(r))
		}
	case []any:
		for _, x := range l {
			err = errors.Join(err, add(x))
		}
	case []int64:
		for _, x := range l {
			err = errors.Join(err, add(x))
		}
	default:
		err = add(v)
	}
	return ids, err
}

// count reads a limit/offset: a template int or a number from entry data.
func count(v any) (int, error) {
	var n int
	switch x := v.(type) {
	case nil:
	case int:
		n = x
	case int64:
		n = int(x)
	case float64:
		if x != float64(int(x)) {
			return 0, fmt.Errorf("not a whole number: %v", x)
		}
		n = int(x)
	case string:
		if x != "" {
			var err error
			if n, err = strconv.Atoi(x); err != nil {
				return 0, fmt.Errorf("not a number: %q", x)
			}
		}
	default:
		return 0, fmt.Errorf("not a number: %v", v)
	}
	if n < 0 {
		return 0, fmt.Errorf("negative: %d", n)
	}
	return n, nil
}

func sortValue(e storage.Entry, field string) any {
	if field == "slug" {
		return e.Slug
	}
	return e.Data[field]
}

// compare orders values of entry data: numbers numerically, strings
// lexically (ISO dates sort correctly), false before true; empty values last.
func compare(a, b any) int {
	if empty(a) || empty(b) {
		return cmp.Compare(btoi(empty(a)), btoi(empty(b)))
	}
	if x, ok := number(a); ok {
		if y, ok := number(b); ok {
			return cmp.Compare(x, y)
		}
	}
	if x, ok := a.(bool); ok {
		if y, ok := b.(bool); ok {
			return cmp.Compare(btoi(x), btoi(y))
		}
	}
	return cmp.Compare(fmt.Sprint(a), fmt.Sprint(b))
}

func empty(v any) bool { return v == nil || v == "" }

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func number(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}
