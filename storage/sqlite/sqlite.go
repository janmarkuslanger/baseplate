// Package sqlite provides the SQLite storage adapter (pure Go, no CGO).
package sqlite

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"

	"github.com/janmarkuslanger/baseplate/storage"
)

// Open opens (or creates) the database file at path. Use ":memory:" for tests.
func Open(path string) (*storage.SQLStore, error) {
	// _txlock=immediate takes the write lock when a transaction begins, so
	// a check and the write depending on it (Store.Atomic) are serialized
	// across processes, and no transaction fails upgrading a read lock.
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate", path)
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
