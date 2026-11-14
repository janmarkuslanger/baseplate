package storage

import (
	"context"
	"fmt"
	"slices"
)

// singletonIndex names the index that keeps a singleton table at one
// document: one row per locale and version.
func singletonIndex(table string) string { return table + "_single" }

// syncSingletons gives the table of every singleton type a unique index on
// (locale, version), so the database refuses a second document, and drops
// it from types that are no longer singletons. A singleton table that
// already holds several documents is an error naming them: which one is
// the real entry is the developer's call, not the store's.
func (s *SQLStore) syncSingletons(ctx context.Context, q Querier, l *layout) error {
	for _, name := range l.order {
		t := l.tables[name]
		if !t.Entry {
			continue
		}
		ct, ok := l.schema.Type(t.Type)
		if !ok {
			continue
		}
		idx := singletonIndex(t.Name)
		if !ct.Singleton {
			if _, err := q.ExecContext(ctx, `DROP INDEX IF EXISTS `+idx); err != nil {
				return fmt.Errorf("drop %s: %w", idx, err)
			}
			continue
		}
		ids, err := documentIDs(ctx, q, t.Name)
		if err != nil {
			return err
		}
		if len(ids) > 1 {
			return fmt.Errorf("storage: singleton type %q has %d entries (IDs %v); delete all but one before starting", t.Type, len(ids), ids)
		}
		if _, err := q.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS `+idx+` ON `+t.Name+` (locale, version)`); err != nil {
			return fmt.Errorf("create %s: %w", idx, err)
		}
	}
	return nil
}

func documentIDs(ctx context.Context, q Querier, table string) ([]int64, error) {
	return scanIDs(q.QueryContext(ctx, `SELECT DISTINCT document_id FROM `+table+` ORDER BY document_id`))
}

func (s *SQLStore) SingletonID(ctx context.Context, typ string) (int64, error) {
	t, err := s.entryTable(typ)
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.read(ctx, func(v *SQLStore) error {
		ids, err := documentIDs(ctx, v.conn(), t.Name)
		switch {
		case err != nil:
			return err
		case len(ids) == 0:
			return ErrNotFound
		case len(ids) > 1:
			return fmt.Errorf("storage: singleton type %q has %d entries (IDs %v)", typ, len(ids), ids)
		}
		id = ids[0]
		return nil
	})
	return id, err
}

// checkSingleton keeps a singleton table at one document in all locales;
// the unique index covers one locale only. A save with ID 0 must not add a
// second document, one with an ID must target the existing one.
func (s *SQLStore) checkSingleton(ctx context.Context, q Querier, t *table, id int64) error {
	ct, ok := s.layout.schema.Type(t.Type)
	if !ok || !ct.Singleton {
		return nil
	}
	ids, err := documentIDs(ctx, q, t.Name)
	switch {
	case err != nil:
		return err
	case id == 0 && len(ids) > 0:
		return fmt.Errorf("storage: singleton type %q already has entry %d", t.Type, ids[0])
	case id != 0 && !slices.Contains(ids, id):
		return ErrNotFound
	}
	return nil
}
