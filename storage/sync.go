package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/janmarkuslanger/baseplate/content"
)

// dialectSQL holds the SQL that differs between databases.
type dialectSQL struct {
	idPK     string
	types    map[content.Storage]string
	slugType string
	// columns returns the existing columns of a table (lowercased name →
	// uppercased type); an empty map means the table does not exist.
	columns func(ctx context.Context, q querier, table string) (map[string]string, error)
	// tables lists all table names.
	tables func(ctx context.Context, q querier) ([]string, error)
	// quote quotes an identifier; column names come from field names and
	// may be SQL keywords ("limit", "order", …).
	quote func(name string) string
	// insertDefault is the INSERT tail for a row with only default values.
	insertDefault string
}

var dialects = map[Dialect]dialectSQL{
	SQLite: {
		idPK:          "INTEGER PRIMARY KEY AUTOINCREMENT",
		slugType:      "TEXT",
		quote:         func(n string) string { return `"` + n + `"` },
		insertDefault: "DEFAULT VALUES",
		types: map[content.Storage]string{
			content.StoreText: "TEXT", content.StoreLongText: "TEXT",
			content.StoreNumber: "REAL", content.StoreBool: "INTEGER",
		},
		columns: func(ctx context.Context, q querier, table string) (map[string]string, error) {
			rows, err := q.QueryContext(ctx, `SELECT name, type FROM pragma_table_info(?)`, table)
			if err != nil {
				return nil, err
			}
			return scanColumns(rows)
		},
		tables: func(ctx context.Context, q querier) ([]string, error) {
			return scanNames(q.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table'`))
		},
	},
}

func scanNames(rows *sql.Rows, err error) ([]string, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func scanColumns(rows *sql.Rows) (map[string]string, error) {
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			return nil, err
		}
		out[strings.ToLower(name)] = strings.ToUpper(typ)
	}
	return out, rows.Err()
}

// sync applies pending renames and creates missing tables and columns, in
// one transaction: a failed or interrupted start leaves the database as it
// was. It never drops or alters existing tables; a column whose type no
// longer matches the schema is an error, so data is never converted
// silently.
func (s *SQLStore) sync(ctx context.Context, l *layout) error {
	return s.tx(ctx, func(q querier) error {
		if err := s.renames(ctx, q, l); err != nil {
			return err
		}
		if err := s.createMissing(ctx, q, l); err != nil {
			return err
		}
		if err := s.checkStoredKinds(ctx, q, l); err != nil {
			return err
		}
		return s.syncSingletons(ctx, q, l)
	})
}

func (s *SQLStore) createMissing(ctx context.Context, q querier, l *layout) error {
	d := dialects[s.dialect]
	for _, name := range l.order {
		t := l.tables[name]
		existing, err := d.columns(ctx, q, t.Name)
		if err != nil {
			return fmt.Errorf("inspect %s: %w", t.Name, err)
		}
		if len(existing) == 0 {
			if _, err := q.ExecContext(ctx, createTableSQL(d, t)); err != nil {
				return fmt.Errorf("create %s: %w", t.Name, err)
			}
			continue
		}
		for _, c := range t.Columns {
			want := d.types[c.Storage]
			have, ok := existing[c.Name]
			if !ok {
				if _, err := q.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s NULL`, t.Name, d.quote(c.Name), want)); err != nil {
					return fmt.Errorf("add %s.%s: %w", t.Name, c.Name, err)
				}
				continue
			}
			if !sameType(have, want) {
				return fmt.Errorf("storage: column %s.%s has type %s, schema expects %s (field %q changed kind; migrate the data or rename the field)", t.Name, c.Name, have, want, c.Field.Name)
			}
		}
	}
	return nil
}

func createTableSQL(d dialectSQL, t *table) string {
	cols := []string{"id " + d.idPK}
	if t.Entry {
		cols = append(cols,
			"slug "+d.slugType+" NOT NULL DEFAULT ''",
			"status VARCHAR(20) NOT NULL DEFAULT 'draft'",
			"locale VARCHAR(20) NOT NULL DEFAULT ''",
			"created_at BIGINT NOT NULL",
			"updated_at BIGINT NOT NULL",
		)
	}
	for _, c := range t.Columns {
		cols = append(cols, d.quote(c.Name)+" "+d.types[c.Storage]+" NULL")
	}
	return fmt.Sprintf("CREATE TABLE %s (\n\t%s\n)", t.Name, strings.Join(cols, ",\n\t"))
}

func sameType(have, want string) bool {
	norm := func(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }
	return norm(have) == norm(want)
}

// renames applies RenamedFrom: columns, component tables (with their
// nested tables) and link rows move from the old field name to the new one.
// Each rename runs once; it is recorded in schema_migrations like a data
// migration, so a later field reusing the old name keeps its data.
func (s *SQLStore) renames(ctx context.Context, q querier, l *layout) error {
	d := dialects[s.dialect]
	for _, name := range l.order { // parents before children
		t := l.tables[name]
		for _, f := range t.Fields {
			if f.RenamedFrom == "" || f.RenamedFrom == f.Name {
				continue
			}
			key := fmt.Sprintf("rename:%s.%s>%s", t.Name, f.RenamedFrom, f.Name)
			var done int
			if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE name = ?`, key).Scan(&done); err != nil {
				return err
			}
			if done > 0 {
				continue
			}
			if err := s.rename(ctx, q, d, l, t, f); err != nil {
				return err
			}
			if _, err := q.ExecContext(ctx, `INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)`, key, s.now().Unix()); err != nil {
				return err
			}
		}
	}
	return nil
}

// rename moves the data of one field from f.RenamedFrom to f.Name. Old
// names that no longer exist count as renamed (databases renamed before
// renames were recorded).
func (s *SQLStore) rename(ctx context.Context, q querier, d dialectSQL, l *layout, t *table, f content.Field) error {
	switch l.fieldStorage(f) {
	case content.StoreGroup, content.StoreList:
		if err := s.renameTables(ctx, q, d, ComponentTable(t.Name, f.RenamedFrom), ComponentTable(t.Name, f.Name)); err != nil {
			return err
		}
		_, err := q.ExecContext(ctx, `UPDATE components SET field = ? WHERE owner_table = ? AND field = ?`, f.Name, t.Name, f.RenamedFrom)
		return err
	case content.StoreBlocks:
		_, err := q.ExecContext(ctx, `UPDATE components SET field = ? WHERE owner_table = ? AND field = ?`, f.Name, t.Name, f.RenamedFrom)
		return err
	case content.StoreRelation:
		_, err := q.ExecContext(ctx, `UPDATE relations SET field = ? WHERE owner_table = ? AND field = ?`, f.Name, t.Name, f.RenamedFrom)
		return err
	}
	cols, err := d.columns(ctx, q, t.Name)
	if err != nil {
		return err
	}
	oldCol, newCol := Column(f.RenamedFrom), Column(f.Name)
	if _, ok := cols[oldCol]; !ok {
		return nil
	}
	if _, ok := cols[newCol]; ok {
		return fmt.Errorf("storage: cannot rename %s.%s to %s: both columns exist", t.Name, oldCol, newCol)
	}
	if _, err := q.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s RENAME COLUMN %s TO %s`, t.Name, d.quote(oldCol), d.quote(newCol))); err != nil {
		return fmt.Errorf("rename %s.%s: %w", t.Name, oldCol, err)
	}
	return nil
}

// renameTables renames a component table and every table nested below it
// (same prefix), and updates the link tables.
func (s *SQLStore) renameTables(ctx context.Context, q querier, d dialectSQL, from, to string) error {
	all, err := d.tables(ctx, q)
	if err != nil {
		return err
	}
	exists := map[string]bool{}
	for _, n := range all {
		exists[n] = true
	}
	if exists[to] {
		if exists[from] {
			return fmt.Errorf("storage: cannot rename %s to %s: both tables exist", from, to)
		}
		return nil // already renamed
	}
	for _, old := range all {
		if old != from && !strings.HasPrefix(old, from+"__") {
			continue
		}
		renamed := to + strings.TrimPrefix(old, from)
		if !identRe.MatchString(renamed) {
			return fmt.Errorf("storage: renamed table name %q is invalid or too long", renamed)
		}
		if _, err := q.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s RENAME TO %s`, old, renamed)); err != nil {
			return fmt.Errorf("rename table %s: %w", old, err)
		}
		for _, stmt := range []string{
			`UPDATE components SET component_table = ? WHERE component_table = ?`,
			`UPDATE components SET owner_table = ? WHERE owner_table = ?`,
			`UPDATE relations SET owner_table = ? WHERE owner_table = ?`,
		} {
			if _, err := q.ExecContext(ctx, stmt, renamed, old); err != nil {
				return err
			}
		}
	}
	return nil
}
