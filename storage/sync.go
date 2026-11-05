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
}

var dialects = map[Dialect]dialectSQL{
	SQLite: {
		idPK:     "INTEGER PRIMARY KEY AUTOINCREMENT",
		slugType: "TEXT",
		quote:    func(n string) string { return `"` + n + `"` },
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
	MySQL: {
		idPK:     "BIGINT AUTO_INCREMENT PRIMARY KEY",
		slugType: "VARCHAR(255)",
		quote:    func(n string) string { return "`" + n + "`" },
		types: map[content.Storage]string{
			content.StoreText: "TEXT", content.StoreLongText: "LONGTEXT",
			content.StoreNumber: "DOUBLE", content.StoreBool: "TINYINT(1)",
		},
		columns: func(ctx context.Context, q querier, table string) (map[string]string, error) {
			rows, err := q.QueryContext(ctx, `SELECT column_name, column_type FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ?`, table)
			if err != nil {
				return nil, err
			}
			return scanColumns(rows)
		},
		tables: func(ctx context.Context, q querier) ([]string, error) {
			return scanNames(q.QueryContext(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE()`))
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

// sync creates missing tables and columns. It never drops or alters
// existing ones; a column whose type no longer matches the schema is an
// error, so data is never converted silently.
func (s *SQLStore) sync(ctx context.Context, l *layout) error {
	d := dialects[s.dialect]
	if err := s.renames(ctx, l); err != nil {
		return err
	}
	for _, name := range l.order {
		t := l.tables[name]
		existing, err := d.columns(ctx, s.db, t.Name)
		if err != nil {
			return fmt.Errorf("inspect %s: %w", t.Name, err)
		}
		if len(existing) == 0 {
			if _, err := s.db.ExecContext(ctx, createTableSQL(d, t)); err != nil {
				return fmt.Errorf("create %s: %w", t.Name, err)
			}
			continue
		}
		for _, c := range t.Columns {
			want := d.types[c.Storage]
			have, ok := existing[c.Name]
			if !ok {
				if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s NULL`, t.Name, d.quote(c.Name), want)); err != nil {
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
	norm := func(s string) string {
		s = strings.ToUpper(strings.TrimSpace(s))
		if s == "TINYINT" {
			return "TINYINT(1)"
		}
		return s
	}
	return norm(have) == norm(want)
}

// renames applies RenamedFrom: columns, component tables (with their
// nested tables) and link rows move from the old field name to the new one.
// A rename whose old name no longer exists has already happened.
func (s *SQLStore) renames(ctx context.Context, l *layout) error {
	d := dialects[s.dialect]
	for _, name := range l.order { // parents before children
		t := l.tables[name]
		for _, f := range t.Fields {
			if f.RenamedFrom == "" || f.RenamedFrom == f.Name {
				continue
			}
			switch l.fieldStorage(f) {
			case content.StoreGroup, content.StoreList:
				if err := s.renameTables(ctx, d, ComponentTable(t.Name, f.RenamedFrom), ComponentTable(t.Name, f.Name)); err != nil {
					return err
				}
				if _, err := s.db.ExecContext(ctx, `UPDATE components SET field = ? WHERE owner_table = ? AND field = ?`, f.Name, t.Name, f.RenamedFrom); err != nil {
					return err
				}
			case content.StoreBlocks:
				if _, err := s.db.ExecContext(ctx, `UPDATE components SET field = ? WHERE owner_table = ? AND field = ?`, f.Name, t.Name, f.RenamedFrom); err != nil {
					return err
				}
			case content.StoreRelation:
				if _, err := s.db.ExecContext(ctx, `UPDATE relations SET field = ? WHERE owner_table = ? AND field = ?`, f.Name, t.Name, f.RenamedFrom); err != nil {
					return err
				}
			default:
				cols, err := d.columns(ctx, s.db, t.Name)
				if err != nil {
					return err
				}
				oldCol, newCol := Column(f.RenamedFrom), Column(f.Name)
				if _, ok := cols[oldCol]; !ok {
					continue
				}
				if _, ok := cols[newCol]; ok {
					return fmt.Errorf("storage: cannot rename %s.%s to %s: both columns exist", t.Name, oldCol, newCol)
				}
				if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s RENAME COLUMN %s TO %s`, t.Name, d.quote(oldCol), d.quote(newCol))); err != nil {
					return fmt.Errorf("rename %s.%s: %w", t.Name, oldCol, err)
				}
			}
		}
	}
	return nil
}

// renameTables renames a component table and every table nested below it
// (same prefix), and updates the link tables.
func (s *SQLStore) renameTables(ctx context.Context, d dialectSQL, from, to string) error {
	all, err := d.tables(ctx, s.db)
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
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s RENAME TO %s`, old, renamed)); err != nil {
			return fmt.Errorf("rename table %s: %w", old, err)
		}
		for _, stmt := range []string{
			`UPDATE components SET component_table = ? WHERE component_table = ?`,
			`UPDATE components SET owner_table = ? WHERE owner_table = ?`,
			`UPDATE relations SET owner_table = ? WHERE owner_table = ?`,
		} {
			if _, err := s.db.ExecContext(ctx, stmt, renamed, old); err != nil {
				return err
			}
		}
	}
	return nil
}
