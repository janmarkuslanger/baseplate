package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/janmarkuslanger/baseplate/content"
)

func (s *SQLStore) CreateEntry(ctx context.Context, e *Entry) error {
	t, err := s.entryTable(e.Type)
	if err != nil {
		return err
	}
	if e.Status == "" {
		e.Status = StatusDraft
	}
	now := s.now()
	e.CreatedAt, e.UpdatedAt = now, now
	return s.tx(ctx, func(q querier) error {
		cols := []string{"slug", "status", "locale", "created_at", "updated_at"}
		args := []any{e.Slug, e.Status, e.Locale, now.Unix(), now.Unix()}
		for _, c := range t.Columns {
			cols = append(cols, s.col(c.Name))
			args = append(args, scalarArg(c, e.Data[c.Field.Name]))
		}
		res, err := q.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (%s) VALUES (%s)`, t.Name, strings.Join(cols, ", "), placeholders(len(cols))), args...)
		if err != nil {
			return err
		}
		if e.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		return s.writeChildren(ctx, q, t, e.ID, e.Data)
	})
}

func (s *SQLStore) UpdateEntry(ctx context.Context, e *Entry) error {
	t, err := s.entryTable(e.Type)
	if err != nil {
		return err
	}
	e.UpdatedAt = s.now()
	return s.tx(ctx, func(q querier) error {
		sets := []string{"slug = ?", "status = ?", "locale = ?", "updated_at = ?"}
		args := []any{e.Slug, e.Status, e.Locale, e.UpdatedAt.Unix()}
		for _, c := range t.Columns {
			sets = append(sets, s.col(c.Name)+" = ?")
			args = append(args, scalarArg(c, e.Data[c.Field.Name]))
		}
		res, err := q.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET %s WHERE id = ?`, t.Name, strings.Join(sets, ", ")), append(args, e.ID)...)
		if err != nil {
			return err
		}
		if err := mustAffect(res); err != nil {
			return err
		}
		// Components keep their IDs: items carrying the key of a component
		// of this entry are updated (wherever they moved), others are new,
		// and components no longer present are deleted.
		tree := map[compRef]bool{}
		if err := s.collectTree(ctx, q, t.Name, e.ID, tree); err != nil {
			return err
		}
		if err := s.unlink(ctx, q, t.Name, e.ID, tree); err != nil {
			return err
		}
		r := &reuse{tree: tree, used: map[compRef]bool{}}
		if err := s.writeChildrenReuse(ctx, q, t, e.ID, e.Data, r); err != nil {
			return err
		}
		for c := range tree {
			if !r.used[c] {
				// A removed component takes along what is still linked to
				// it: data of fields no longer in the schema.
				if err := s.deleteChildren(ctx, q, c.table, c.id); err != nil {
					return err
				}
				if _, err := q.ExecContext(ctx, `DELETE FROM `+c.table+` WHERE id = ?`, c.id); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (s *SQLStore) DeleteEntry(ctx context.Context, typ string, id int64) error {
	t, err := s.entryTable(typ)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(q querier) error {
		res, err := q.ExecContext(ctx, `DELETE FROM `+t.Name+` WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if err := mustAffect(res); err != nil {
			return err
		}
		if err := s.deleteChildren(ctx, q, t.Name, id); err != nil {
			return err
		}
		_, err = q.ExecContext(ctx, `DELETE FROM relations WHERE target_type = ? AND target_id = ?`, typ, id)
		return err
	})
}

// writeChildren stores the components and relations of one owner row.
func (s *SQLStore) writeChildren(ctx context.Context, q querier, t *table, ownerID int64, data map[string]any) error {
	return s.writeChildrenReuse(ctx, q, t, ownerID, data, nil)
}

func (s *SQLStore) writeChildrenReuse(ctx context.Context, q querier, t *table, ownerID int64, data map[string]any, r *reuse) error {
	for _, f := range t.Fields {
		v := data[f.Name]
		switch s.layout.fieldStorage(f) {
		case content.StoreGroup:
			if m, ok := v.(map[string]any); ok {
				if err := s.writeComponent(ctx, q, t.Name, ownerID, f.Name, t.children[f.Name], 0, m, r); err != nil {
					return err
				}
			}
		case content.StoreList:
			for i, it := range asItems(v) {
				if err := s.writeComponent(ctx, q, t.Name, ownerID, f.Name, t.children[f.Name], i, it, r); err != nil {
					return err
				}
			}
		case content.StoreBlocks:
			for i, it := range asItems(v) {
				name, _ := it[content.BlockTypeKey].(string)
				if _, ok := s.layout.tables[blockTable(name)]; !ok {
					return fmt.Errorf("storage: unknown block %q in %s.%s", name, t.Name, f.Name)
				}
				if err := s.writeComponent(ctx, q, t.Name, ownerID, f.Name, blockTable(name), i, it, r); err != nil {
					return err
				}
			}
		case content.StoreRelation:
			for i, target := range relationIDs(v) {
				var one int
				err := q.QueryRowContext(ctx, `SELECT 1 FROM `+entryTable(f.Ref)+` WHERE id = ?`, target).Scan(&one)
				if errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("%w (%s → %s #%d)", ErrInvalidRelation, f.Name, f.Ref, target)
				}
				if err != nil {
					return err
				}
				if _, err := q.ExecContext(ctx, `INSERT INTO relations (owner_table, owner_id, field, target_type, target_id, position) VALUES (?, ?, ?, ?, ?, ?)`,
					t.Name, ownerID, f.Name, f.Ref, target, i); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *SQLStore) writeComponent(ctx context.Context, q querier, ownerTable string, ownerID int64, field, tableName string, pos int, data map[string]any, r *reuse) error {
	ct := s.layout.tables[tableName]
	id := r.take(ct.Name, data)
	cols := make([]string, len(ct.Columns))
	args := make([]any, len(ct.Columns))
	for i, c := range ct.Columns {
		cols[i], args[i] = s.col(c.Name), scalarArg(c, data[c.Field.Name])
	}
	switch {
	case id != 0 && len(cols) > 0:
		sets := make([]string, len(cols))
		for i, c := range cols {
			sets[i] = c + " = ?"
		}
		if _, err := q.ExecContext(ctx, `UPDATE `+ct.Name+` SET `+strings.Join(sets, ", ")+` WHERE id = ?`, append(args, id)...); err != nil {
			return err
		}
	case id != 0:
		// nothing to update
	default:
		var res sql.Result
		var err error
		switch {
		case len(cols) == 0:
			res, err = q.ExecContext(ctx, `INSERT INTO `+ct.Name+` `+dialects[s.dialect].insertDefault)
		default:
			res, err = q.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (%s) VALUES (%s)`, ct.Name, strings.Join(cols, ", "), placeholders(len(cols))), args...)
		}
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO components (owner_table, owner_id, field, component_table, component_id, position) VALUES (?, ?, ?, ?, ?, ?)`,
		ownerTable, ownerID, field, ct.Name, id, pos); err != nil {
		return err
	}
	return s.writeChildrenReuse(ctx, q, ct, id, data, r)
}
