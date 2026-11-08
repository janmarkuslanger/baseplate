package storage

import (
	"context"
	"fmt"
)

// singletonIndex names the index that keeps a singleton table at one
// document: one row per locale and version.
func singletonIndex(table string) string { return table + "_single" }

// syncSingletons gives the table of every singleton type a unique index on
// (locale, version), so the database refuses a second document, and drops
// it from types that are no longer singletons. A singleton table that
// already holds several documents is an error naming them: which one is
// the real entry is the developer's call, not the store's.
func (s *SQLStore) syncSingletons(ctx context.Context, q querier, l *layout) error {
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

func documentIDs(ctx context.Context, q querier, table string) ([]int64, error) {
	return scanIDs(q.QueryContext(ctx, `SELECT DISTINCT document_id FROM `+table+` ORDER BY document_id`))
}
