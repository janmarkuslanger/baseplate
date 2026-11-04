// Package sqlite provides the SQLite storage adapter (pure Go, no CGO).
package sqlite

import (
	"database/sql"
	"fmt"

	"github.com/janmarkuslanger/baseplate/storage"
	_ "modernc.org/sqlite"
)

// Open opens (or creates) the database file at path. Use ":memory:" for tests.
func Open(path string) (*storage.SQLStore, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer; a single connection avoids SQLITE_BUSY and
	// keeps ":memory:" databases consistent across queries.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return storage.NewSQLStore(db, storage.SQLite), nil
}
