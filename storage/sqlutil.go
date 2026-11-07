package storage

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	"github.com/janmarkuslanger/baseplate/content"
)

// querier is satisfied by *sql.DB and *sql.Tx. With SQLite's single
// connection, every query inside a transaction must use the transaction,
// and a query must never run while another result set is still open.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *SQLStore) UseSchema(ctx context.Context, sc *content.Schema) error {
	l, err := planLayout(sc)
	if err != nil {
		return err
	}
	if err := s.sync(ctx, l); err != nil {
		return err
	}
	s.layout = l
	return nil
}

// col quotes a column name for the store's dialect.
func (s *SQLStore) col(name string) string { return dialects[s.dialect].quote(name) }

// conn is the connection entry queries run on: the transaction inside
// Atomic, the database otherwise.
func (s *SQLStore) conn() querier {
	if s.q != nil {
		return s.q
	}
	return s.db
}

// tx runs fn in a transaction, or in the surrounding one inside Atomic.
func (s *SQLStore) tx(ctx context.Context, fn func(q querier) error) error {
	if s.q != nil {
		return fn(s.q)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Atomic runs fn in one transaction on a view of the store bound to it.
// The SQLite adapter begins transactions IMMEDIATE, so writers queue up
// before their first read, across connections and processes.
func (s *SQLStore) Atomic(ctx context.Context, fn func(Entries) error) error {
	return s.tx(ctx, func(q querier) error {
		view := *s
		view.q = q
		return fn(&view)
	})
}

func (s *SQLStore) entryTable(typ string) (*table, error) {
	if s.layout == nil {
		return nil, errors.New("storage: UseSchema was not called")
	}
	return s.layout.entry(typ)
}

// --- write ---

func (s *SQLStore) inChunks(ids []any, fn func([]any) error) error {
	for chunk := range slices.Chunk(ids, 500) {
		if err := fn(chunk); err != nil {
			return err
		}
	}
	return nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

func fieldOf(t *table, name string) (content.Field, bool) {
	for _, f := range t.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return content.Field{}, false
}

func asItems(v any) []map[string]any {
	list, _ := v.([]any)
	out := make([]map[string]any, 0, len(list))
	for _, it := range list {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func relationIDs(v any) []int64 {
	switch x := v.(type) {
	case int64:
		return []int64{x}
	case float64:
		return []int64{int64(x)}
	case []any:
		var out []int64
		for _, it := range x {
			out = append(out, relationIDs(it)...)
		}
		return out
	}
	return nil
}

func scalarArg(c column, v any) any {
	if v == nil {
		return nil
	}
	if c.Storage == content.StoreBool {
		if b, _ := v.(bool); b {
			return 1
		}
		return 0
	}
	return v
}

func scalarDests(t *table) []any {
	out := make([]any, len(t.Columns))
	for i, c := range t.Columns {
		switch c.Storage {
		case content.StoreNumber:
			out[i] = new(sql.NullFloat64)
		case content.StoreBool:
			out[i] = new(sql.NullInt64)
		default:
			out[i] = new(sql.NullString)
		}
	}
	return out
}

func scalarData(t *table, vals []any) map[string]any {
	m := map[string]any{}
	for i, c := range t.Columns {
		switch v := vals[i].(type) {
		case *sql.NullString:
			if v.Valid {
				m[c.Field.Name] = v.String
			}
		case *sql.NullFloat64:
			if v.Valid {
				m[c.Field.Name] = v.Float64
			}
		case *sql.NullInt64:
			m[c.Field.Name] = v.Valid && v.Int64 != 0
		}
	}
	return m
}
