package storage

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/janmarkuslanger/baseplate/content"
)

// Table layout derived from the content schema:
//
//	ct_<type>            one row per entry; one column per scalar field
//	cb_<block>           one row per block instance
//	<owner>__<field>     one row per group/list item of <owner>
//	components           ordered links owner row → component row
//	relations            ordered links owner row → target entry
//
// Entry and component rows can both own components and relations.

// baseColumns exist on every entry table; fields must not reuse them,
// except "slug", which maps onto the slug column.
var baseColumns = map[string]bool{"id": true, "slug": true, "status": true, "locale": true, "created_at": true, "updated_at": true}

var identRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,59}$`)

type column struct {
	Name    string
	Field   content.Field
	Storage content.Storage
}

type table struct {
	Name    string
	Entry   bool
	Type    string // content type name (entry tables only)
	Fields  []content.Field
	Columns []column
	// children maps a group/list field name to its component table.
	children map[string]string
}

// layout is the complete table plan of a schema.
type layout struct {
	schema *content.Schema
	tables map[string]*table
	order  []string
}

func entryTable(typ string) string  { return "ct_" + typ }
func blockTable(name string) string { return "cb_" + name }

// snake converts a field name like "metaTitle" to "meta_title".
func snake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}

func planLayout(s *content.Schema) (*layout, error) {
	l := &layout{schema: s, tables: map[string]*table{}}
	for _, t := range s.Types() {
		if err := l.add(&table{Name: entryTable(t.Name), Entry: true, Type: t.Name, Fields: t.Fields}); err != nil {
			return nil, err
		}
	}
	for _, b := range s.AllBlocks() {
		if err := l.add(&table{Name: blockTable(b.Name), Fields: b.Fields}); err != nil {
			return nil, err
		}
	}
	return l, nil
}

func (l *layout) add(t *table) error {
	if !identRe.MatchString(t.Name) {
		return fmt.Errorf("storage: table name %q is invalid or too long (rename the type/field)", t.Name)
	}
	if _, dup := l.tables[t.Name]; dup {
		return fmt.Errorf("storage: table %q defined twice", t.Name)
	}
	t.children = map[string]string{}
	l.tables[t.Name] = t
	l.order = append(l.order, t.Name)
	seen := map[string]bool{}
	for _, f := range t.Fields {
		k, ok := l.schema.Kind(f.Kind)
		if !ok {
			return fmt.Errorf("storage: %s.%s: unknown kind %q", t.Name, f.Name, f.Kind)
		}
		col := snake(f.Name)
		if !identRe.MatchString(col) {
			return fmt.Errorf("storage: field name %q in %s is invalid", f.Name, t.Name)
		}
		if seen[col] {
			return fmt.Errorf("storage: fields in %s collide on column %q", t.Name, col)
		}
		seen[col] = true
		switch {
		case k.Storage == content.StoreGroup || k.Storage == content.StoreList:
			child := &table{Name: t.Name + "__" + col, Fields: f.Fields}
			t.children[f.Name] = child.Name
			if err := l.add(child); err != nil {
				return err
			}
		case k.Storage == content.StoreBlocks || k.Storage == content.StoreRelation:
			// stored in the link tables
		default:
			if t.Entry && col == "slug" {
				continue // uses the slug base column
			}
			if baseColumns[col] || (!t.Entry && col == "id") {
				return fmt.Errorf("storage: field %q in %s uses a reserved name", f.Name, t.Name)
			}
			t.Columns = append(t.Columns, column{Name: col, Field: f, Storage: k.Storage})
		}
	}
	return nil
}

func (l *layout) entry(typ string) (*table, error) {
	t, ok := l.tables[entryTable(typ)]
	if !ok {
		return nil, fmt.Errorf("storage: unknown content type %q", typ)
	}
	return t, nil
}

// fieldStorage returns the storage of a field of t.
func (l *layout) fieldStorage(f content.Field) content.Storage {
	k, _ := l.schema.Kind(f.Kind)
	return k.Storage
}
