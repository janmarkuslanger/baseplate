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
}

var dialects = map[Dialect]dialectSQL{
	SQLite: {
		idPK:     "INTEGER PRIMARY KEY AUTOINCREMENT",
		slugType: "TEXT",
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
	},
	MySQL: {
		idPK:     "BIGINT AUTO_INCREMENT PRIMARY KEY",
		slugType: "VARCHAR(255)",
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
	},
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
				if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s NULL`, t.Name, c.Name, want)); err != nil {
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
		cols = append(cols, c.Name+" "+d.types[c.Storage]+" NULL")
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
