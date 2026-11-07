package storage

import (
	"context"
	"fmt"

	"github.com/janmarkuslanger/baseplate/content"
)

// storedLink summarizes the link rows of one field of an owner table: the
// component table (components) or target type (relations) they point at,
// and the most rows a single owner has.
type storedLink struct {
	field, to string
	most      int
}

// checkStoredKinds refuses a schema whose fields no longer match the data
// stored under their names: a kind change between column, component and
// relation storage, a group or blocks field pointing at other tables, a
// relation pointing at another type, and a single field (group, relation
// without Many) where an owner holds several rows. Data is never converted
// or dropped silently; the field must be renamed and the data migrated.
func (s *SQLStore) checkStoredKinds(ctx context.Context, q querier, l *layout) error {
	d := dialects[s.dialect]
	for _, name := range l.order {
		t := l.tables[name]
		cols, err := d.columns(ctx, q, t.Name)
		if err != nil {
			return err
		}
		comps, err := storedLinks(ctx, q, `SELECT field, component_table, MAX(n) FROM (SELECT owner_id, field, component_table, COUNT(*) AS n FROM components WHERE owner_table = ? GROUP BY owner_id, field, component_table) AS c GROUP BY field, component_table`, t.Name)
		if err != nil {
			return err
		}
		rels, err := storedLinks(ctx, q, `SELECT field, target_type, MAX(n) FROM (SELECT owner_id, field, target_type, COUNT(*) AS n FROM relations WHERE owner_table = ? GROUP BY owner_id, field, target_type) AS r GROUP BY field, target_type`, t.Name)
		if err != nil {
			return err
		}
		for _, f := range t.Fields {
			if err := checkFieldKind(l, t, f, cols, comps[f.Name], rels[f.Name]); err != nil {
				return fmt.Errorf("storage: %s.%s %w (field %q changed kind; migrate the data or rename the field)", t.Name, f.Name, err, f.Name)
			}
		}
	}
	return nil
}

func checkFieldKind(l *layout, t *table, f content.Field, cols map[string]string, comps, rels []storedLink) error {
	st := l.fieldStorage(f)
	scalar := !st.Container() && st != content.StoreRelation
	if _, ok := cols[Column(f.Name)]; ok && !scalar && !(t.Entry && Column(f.Name) == "slug") {
		return fmt.Errorf("has a column, but the schema declares kind %q", f.Kind)
	}
	if scalar && len(comps)+len(rels) > 0 {
		return fmt.Errorf("has components or relations stored, but the schema declares kind %q", f.Kind)
	}
	if st != content.StoreRelation && len(rels) > 0 {
		return fmt.Errorf("has relations stored, but the schema declares kind %q", f.Kind)
	}
	if st == content.StoreRelation && len(comps) > 0 {
		return fmt.Errorf("has components stored, but the schema declares kind %q", f.Kind)
	}
	for _, c := range comps {
		switch st {
		case content.StoreGroup, content.StoreList:
			if c.to != t.children[f.Name] {
				return fmt.Errorf("has components in %s, but the schema (kind %q) stores them in %s", c.to, f.Kind, t.children[f.Name])
			}
			if st == content.StoreGroup && c.most > 1 {
				return fmt.Errorf("has up to %d items stored, but the schema declares a group", c.most)
			}
		case content.StoreBlocks:
			if b := blockOf(c.to); b == "" || c.to != blockTable(b) {
				return fmt.Errorf("has components in %s, but the schema declares kind %q", c.to, f.Kind)
			}
		}
	}
	for _, r := range rels {
		if r.to != f.Ref {
			return fmt.Errorf("has relations to %q, but the schema relates to %q", r.to, f.Ref)
		}
		if !f.Many && r.most > 1 {
			return fmt.Errorf("has up to %d targets stored, but the schema declares a single relation (Many is false)", r.most)
		}
	}
	return nil
}

func storedLinks(ctx context.Context, q querier, query, ownerTable string) (map[string][]storedLink, error) {
	rows, err := q.QueryContext(ctx, query, ownerTable)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]storedLink{}
	for rows.Next() {
		var l storedLink
		if err := rows.Scan(&l.field, &l.to, &l.most); err != nil {
			return nil, err
		}
		out[l.field] = append(out[l.field], l)
	}
	return out, rows.Err()
}
