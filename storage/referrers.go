package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/janmarkuslanger/baseplate/content"
)

func (s *SQLStore) Referrers(ctx context.Context, typ string, id int64) ([]Referrer, error) {
	if _, err := s.entryTable(typ); err != nil {
		return nil, err
	}
	return s.referrers(ctx, s.conn(), typ, id)
}

// referrers resolves relation rows pointing at an entry to the entries
// owning them. Rows of fields the schema no longer declares and the
// entry's relations to itself are skipped.
func (s *SQLStore) referrers(ctx context.Context, q querier, typ string, id int64) ([]Referrer, error) {
	rows, err := q.QueryContext(ctx, `SELECT owner_table, owner_id, field FROM relations WHERE target_type = ? AND target_id = ? ORDER BY owner_table, owner_id`, typ, id)
	if err != nil {
		return nil, err
	}
	type ref struct {
		table string
		id    int64
		field string
	}
	var refs []ref
	for rows.Next() {
		var r ref
		if err := rows.Scan(&r.table, &r.id, &r.field); err != nil {
			rows.Close()
			return nil, err
		}
		refs = append(refs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []Referrer
	seen := map[ref]bool{}
	for _, r := range refs {
		if !s.declaresRelation(r.table, r.field) {
			continue
		}
		table, ownerID, nested := r.table, r.id, false
		// Walk up from components to the owning entry.
		for depth := 0; ; depth++ {
			if t, ok := s.layout.tables[table]; ok && t.Entry {
				break
			}
			if depth > 100 {
				return nil, fmt.Errorf("storage: component chain too deep at %s #%d", table, ownerID)
			}
			if err := q.QueryRowContext(ctx, `SELECT owner_table, owner_id FROM components WHERE component_table = ? AND component_id = ?`, table, ownerID).Scan(&table, &ownerID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					table = ""
					break
				}
				return nil, err
			}
			nested = true
		}
		t, ok := s.layout.tables[table]
		if !ok || (t.Type == typ && ownerID == id) {
			continue
		}
		key := ref{table, ownerID, r.field}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, Referrer{Type: t.Type, ID: ownerID, Field: r.field, Nested: nested, Block: blockOf(r.table)})
	}
	return out, nil
}

// declaresRelation reports whether field is a relation field of table in
// the current schema.
func (s *SQLStore) declaresRelation(table, field string) bool {
	t, ok := s.layout.tables[table]
	if !ok {
		return false
	}
	f, ok := fieldOf(t, field)
	return ok && s.layout.fieldStorage(f) == content.StoreRelation
}
