package storage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/janmarkuslanger/baseplate/content"
)

func (s *SQLStore) GetEntry(ctx context.Context, typ string, id int64) (*Entry, error) {
	return s.getOne(ctx, typ, `id = ?`, id)
}

func (s *SQLStore) GetEntryBySlug(ctx context.Context, typ, slug string) (*Entry, error) {
	return s.getOne(ctx, typ, `slug = ?`, slug)
}

func (s *SQLStore) getOne(ctx context.Context, typ, cond string, arg any) (*Entry, error) {
	t, err := s.entryTable(typ)
	if err != nil {
		return nil, err
	}
	list, err := s.selectEntries(ctx, t, ` WHERE `+cond+` ORDER BY id LIMIT 1`, arg)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return &list[0], nil
}

func (s *SQLStore) ListEntries(ctx context.Context, lq ListQuery) ([]Entry, int, error) {
	t, err := s.entryTable(lq.Type)
	if err != nil {
		return nil, 0, err
	}
	var where []string
	var args []any
	if lq.Status != "" {
		where = append(where, "status = ?")
		args = append(args, lq.Status)
	}
	if lq.Search != "" {
		like := "%" + lq.Search + "%"
		ors := []string{"slug LIKE ?"}
		args = append(args, like)
		for _, c := range t.Columns {
			if c.Storage == content.StoreText || c.Storage == content.StoreLongText {
				ors = append(ors, s.col(c.Name)+" LIKE ?")
				args = append(args, like)
			}
		}
		where = append(where, "("+strings.Join(ors, " OR ")+")")
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+t.Name+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	tail := cond + ` ORDER BY updated_at DESC, id DESC`
	if lq.Limit > 0 {
		tail += fmt.Sprintf(" LIMIT %d OFFSET %d", lq.Limit, lq.Offset)
	}
	list, err := s.selectEntries(ctx, t, tail, args...)
	return list, total, err
}

// selectEntries loads entry rows and hydrates their components and
// relations with a fixed number of queries per nesting level.
func (s *SQLStore) selectEntries(ctx context.Context, t *table, tail string, args ...any) ([]Entry, error) {
	cols := []string{"id", "slug", "status", "locale", "created_at", "updated_at"}
	for _, c := range t.Columns {
		cols = append(cols, s.col(c.Name))
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+strings.Join(cols, ", ")+` FROM `+t.Name+tail, args...)
	if err != nil {
		return nil, err
	}
	var list []Entry
	for rows.Next() {
		var e Entry
		var created, updated int64
		dest := []any{&e.ID, &e.Slug, &e.Status, &e.Locale, &created, &updated}
		vals := scalarDests(t)
		if err := rows.Scan(append(dest, vals...)...); err != nil {
			rows.Close()
			return nil, err
		}
		e.Type = t.Type
		e.CreatedAt, e.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
		e.Data = scalarData(t, vals)
		for _, f := range t.Fields {
			if f.Name == "slug" {
				e.Data["slug"] = e.Slug
			}
		}
		list = append(list, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	owners := make(map[int64]map[string]any, len(list))
	for i := range list {
		owners[list[i].ID] = list[i].Data
	}
	if err := s.hydrate(ctx, t, owners); err != nil {
		return nil, err
	}
	return list, nil
}

// hydrate fills container and relation fields of the given rows of t.
func (s *SQLStore) hydrate(ctx context.Context, t *table, owners map[int64]map[string]any) error {
	if len(owners) == 0 {
		return nil
	}
	ids := make([]any, 0, len(owners))
	for id, data := range owners {
		ids = append(ids, id)
		for _, f := range t.Fields {
			switch st := s.layout.fieldStorage(f); {
			case st == content.StoreList || st == content.StoreBlocks || (st == content.StoreRelation && f.Many):
				data[f.Name] = []any{}
			}
		}
	}

	// Components: links first, then one query per component table.
	var links []link
	err := s.inChunks(ids, func(chunk []any) error {
		rows, err := s.db.QueryContext(ctx, `SELECT owner_id, field, component_table, component_id FROM components WHERE owner_table = ? AND owner_id IN (`+placeholders(len(chunk))+`) ORDER BY position`,
			append([]any{t.Name}, chunk...)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var l link
			if err := rows.Scan(&l.ownerID, &l.field, &l.table, &l.compID); err != nil {
				return err
			}
			links = append(links, l)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	byTable := map[string]map[int64]map[string]any{}
	for _, l := range links {
		if _, ok := s.layout.tables[l.table]; !ok {
			continue // block or field removed from the schema
		}
		if byTable[l.table] == nil {
			byTable[l.table] = map[int64]map[string]any{}
		}
		byTable[l.table][l.compID] = nil
	}
	for name, comps := range byTable {
		ct := s.layout.tables[name]
		if err := s.loadComponents(ctx, ct, comps); err != nil {
			return err
		}
		if err := s.hydrate(ctx, ct, comps); err != nil {
			return err
		}
	}
	for _, l := range links {
		comp := byTable[l.table][l.compID]
		owner := owners[l.ownerID]
		if comp == nil || owner == nil {
			continue
		}
		f, ok := fieldOf(t, l.field)
		if !ok {
			continue
		}
		switch s.layout.fieldStorage(f) {
		case content.StoreGroup:
			comp[content.ItemKey] = itemKeyOf(l.table, l.compID)
			owner[f.Name] = comp
		case content.StoreBlocks:
			comp[content.BlockTypeKey] = blockName(l.table)
			fallthrough
		case content.StoreList:
			comp[content.ItemKey] = itemKeyOf(l.table, l.compID)
			owner[f.Name] = append(owner[f.Name].([]any), comp)
		}
	}

	// Relations.
	return s.inChunks(ids, func(chunk []any) error {
		rows, err := s.db.QueryContext(ctx, `SELECT owner_id, field, target_id FROM relations WHERE owner_table = ? AND owner_id IN (`+placeholders(len(chunk))+`) ORDER BY position`,
			append([]any{t.Name}, chunk...)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var ownerID, target int64
			var field string
			if err := rows.Scan(&ownerID, &field, &target); err != nil {
				return err
			}
			f, ok := fieldOf(t, field)
			if !ok || owners[ownerID] == nil {
				continue
			}
			if f.Many {
				owners[ownerID][field] = append(owners[ownerID][field].([]any), target)
			} else {
				owners[ownerID][field] = target
			}
		}
		return rows.Err()
	})
}

// loadComponents reads the scalar columns of the given component rows.
func (s *SQLStore) loadComponents(ctx context.Context, ct *table, comps map[int64]map[string]any) error {
	ids := make([]any, 0, len(comps))
	for id := range comps {
		ids = append(ids, id)
		comps[id] = map[string]any{}
	}
	cols := []string{"id"}
	for _, c := range ct.Columns {
		cols = append(cols, s.col(c.Name))
	}
	return s.inChunks(ids, func(chunk []any) error {
		rows, err := s.db.QueryContext(ctx, `SELECT `+strings.Join(cols, ", ")+` FROM `+ct.Name+` WHERE id IN (`+placeholders(len(chunk))+`)`, chunk...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			vals := scalarDests(ct)
			if err := rows.Scan(append([]any{&id}, vals...)...); err != nil {
				return err
			}
			comps[id] = scalarData(ct, vals)
		}
		return rows.Err()
	})
}

// --- helpers ---
