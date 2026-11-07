package storage

import (
	"context"
	"strconv"
	"strings"

	"github.com/janmarkuslanger/baseplate/content"
)

// itemKeyOf is the item key of a component row: "<table>:<id>".
func itemKeyOf(table string, id int64) string { return table + ":" + strconv.FormatInt(id, 10) }

type compRef struct {
	table string
	id    int64
}

// reuse tracks existing components of an entry during an update.
type reuse struct {
	tree map[compRef]bool
	used map[compRef]bool
}

// take returns the ID of an existing component the item refers to, or 0.
func (r *reuse) take(table string, data map[string]any) int64 {
	if r == nil {
		return 0
	}
	k, _ := data[content.ItemKey].(string)
	tbl, num, ok := strings.Cut(k, ":")
	if !ok || tbl != table {
		return 0
	}
	id, err := strconv.ParseInt(num, 10, 64)
	c := compRef{table, id}
	if err != nil || !r.tree[c] || r.used[c] {
		return 0
	}
	r.used[c] = true
	return id
}

// collectTree adds every component below an owner row to tree. Only links
// the current schema describes are followed (see ownsLink).
func (s *SQLStore) collectTree(ctx context.Context, q querier, ownerTable string, ownerID int64, tree map[compRef]bool) error {
	links, err := s.childLinks(ctx, q, ownerTable, ownerID)
	if err != nil {
		return err
	}
	for _, l := range links {
		if !s.ownsLink(ownerTable, l) {
			continue
		}
		tree[compRef{l.table, l.compID}] = true
		if err := s.collectTree(ctx, q, l.table, l.compID, tree); err != nil {
			return err
		}
	}
	return nil
}

// ownsLink reports whether the schema describes a component link of an
// owner table: its field is a group, list or blocks field of the owner and
// the component table is known. Links of removed fields or blocks are left
// alone on save, so their data stays (see "remove field" in the README).
func (s *SQLStore) ownsLink(ownerTable string, l link) bool {
	t, ok := s.layout.tables[ownerTable]
	if !ok {
		return false
	}
	if _, ok := s.layout.tables[l.table]; !ok {
		return false
	}
	f, ok := fieldOf(t, l.field)
	if !ok {
		return false
	}
	switch s.layout.fieldStorage(f) {
	case content.StoreGroup, content.StoreList:
		return l.table == t.children[f.Name]
	case content.StoreBlocks:
		return blockOf(l.table) != "" && l.table == blockTable(blockOf(l.table))
	}
	return false
}

// unlink removes the component links and relations the schema describes
// from the root row and its components, keeping the component rows for
// reuse. Links and relations of fields no longer in the schema stay.
func (s *SQLStore) unlink(ctx context.Context, q querier, rootTable string, rootID int64, tree map[compRef]bool) error {
	owners := append([]compRef{{rootTable, rootID}}, keys(tree)...)
	for _, o := range owners {
		links, err := s.childLinks(ctx, q, o.table, o.id)
		if err != nil {
			return err
		}
		for _, l := range links {
			if !s.ownsLink(o.table, l) {
				continue
			}
			if _, err := q.ExecContext(ctx, `DELETE FROM components WHERE owner_table = ? AND owner_id = ? AND field = ? AND component_table = ? AND component_id = ?`,
				o.table, o.id, l.field, l.table, l.compID); err != nil {
				return err
			}
		}
		for _, f := range s.layout.tables[o.table].Fields {
			if s.layout.fieldStorage(f) != content.StoreRelation {
				continue
			}
			if _, err := q.ExecContext(ctx, `DELETE FROM relations WHERE owner_table = ? AND owner_id = ? AND field = ?`, o.table, o.id, f.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

func keys(m map[compRef]bool) []compRef {
	out := make([]compRef, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func (s *SQLStore) childLinks(ctx context.Context, q querier, ownerTable string, ownerID int64) ([]link, error) {
	rows, err := q.QueryContext(ctx, `SELECT field, component_table, component_id FROM components WHERE owner_table = ? AND owner_id = ?`, ownerTable, ownerID)
	if err != nil {
		return nil, err
	}
	var links []link
	for rows.Next() {
		var l link
		if err := rows.Scan(&l.field, &l.table, &l.compID); err != nil {
			rows.Close()
			return nil, err
		}
		links = append(links, l)
	}
	rows.Close()
	return links, rows.Err()
}

type link struct {
	ownerID, compID int64
	field, table    string
}

// deleteChildren removes all components (recursively) and relations owned
// by one row.
func (s *SQLStore) deleteChildren(ctx context.Context, q querier, ownerTable string, ownerID int64) error {
	rows, err := q.QueryContext(ctx, `SELECT component_table, component_id FROM components WHERE owner_table = ? AND owner_id = ?`, ownerTable, ownerID)
	if err != nil {
		return err
	}
	var links []link
	for rows.Next() {
		var l link
		if err := rows.Scan(&l.table, &l.compID); err != nil {
			rows.Close()
			return err
		}
		links = append(links, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, l := range links {
		if err := s.deleteChildren(ctx, q, l.table, l.compID); err != nil {
			return err
		}
		if _, ok := s.layout.tables[l.table]; ok {
			if _, err := q.ExecContext(ctx, `DELETE FROM `+l.table+` WHERE id = ?`, l.compID); err != nil {
				return err
			}
		}
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM components WHERE owner_table = ? AND owner_id = ?`, ownerTable, ownerID); err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `DELETE FROM relations WHERE owner_table = ? AND owner_id = ?`, ownerTable, ownerID)
	return err
}

// --- read ---
