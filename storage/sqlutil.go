package storage

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	"github.com/janmarkuslanger/baseplate/content"
)

// Querier runs SQL; *sql.DB and *sql.Tx satisfy it. With SQLite's single
// connection, every query inside a transaction must use the transaction,
// and a query must never run while another result set is still open.
type Querier interface {
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
func (s *SQLStore) conn() Querier {
	if s.q != nil {
		return s.q
	}
	return s.db
}

// reader is the connection single-statement reads run on: the transaction
// inside Atomic, the reader pool (WithReader) or the database otherwise.
func (s *SQLStore) reader() Querier {
	if s.q != nil {
		return s.q
	}
	return s.readDB()
}

func (s *SQLStore) readDB() *sql.DB {
	if s.rdb != nil {
		return s.rdb
	}
	return s.db
}

// tx runs fn in a transaction, or in the surrounding one inside Atomic.
func (s *SQLStore) tx(ctx context.Context, fn func(q Querier) error) error {
	if s.q != nil {
		return fn(s.q)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// Rolls back on error and on panic, which would otherwise hold the
	// single connection forever; a no-op after Commit.
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// read runs fn on a view of the store whose queries share one transaction,
// so a read spanning several statements sees a single snapshot instead of
// writes committed between them; inside Atomic it joins the surrounding
// transaction. The transaction is begun read-only, which the SQLite adapter
// begins DEFERRED despite _txlock=immediate: it takes no write lock, and in
// WAL mode it neither blocks writers nor waits for them. It runs on the
// reader pool (WithReader), so it does not queue behind a write
// transaction holding the write connection either.
func (s *SQLStore) read(ctx context.Context, fn func(v *SQLStore) error) error {
	if s.q != nil {
		return fn(s)
	}
	tx, err := s.readDB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback() // no-op after Commit
	view := *s
	view.q = tx
	if err := fn(&view); err != nil {
		return err
	}
	return tx.Commit()
}

// Atomic runs fn in one transaction on a view of the store bound to it.
// The SQLite adapter begins transactions IMMEDIATE, so writers queue up
// before their first read, across connections and processes.
func (s *SQLStore) Atomic(ctx context.Context, fn func(Entries) error) error {
	return s.inView(ctx, func(v *SQLStore, _ Querier) error { return fn(v) })
}

// inView runs fn in a transaction (or the surrounding one) with a view of
// the store whose queries all use it.
func (s *SQLStore) inView(ctx context.Context, fn func(v *SQLStore, q Querier) error) error {
	return s.tx(ctx, func(q Querier) error {
		view := *s
		view.q = q
		return fn(&view, q)
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
