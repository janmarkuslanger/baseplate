// Package sqlite provides the SQLite storage adapter (pure Go, no CGO).
package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"runtime"

	_ "modernc.org/sqlite"

	"github.com/janmarkuslanger/baseplate/storage"
)

// Open opens (or creates) the database file at path. Use ":memory:" for tests.
//
// A file database gets one write connection and a pool of read-only
// connections (storage.SQLStore.WithReader): in WAL mode readers see every
// committed write and neither wait for the writer nor block it, so admin
// pages and builds keep reading while a save holds the write connection.
// ":memory:" keeps a single connection, since every further connection
// would open a database of its own.
func Open(path string) (*storage.SQLStore, error) {
	// _txlock=immediate takes the write lock when a transaction begins, so
	// a check and the write depending on it (Store.Atomic) are serialized
	// across processes, and no transaction fails upgrading a read lock.
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer; a single write connection avoids
	// SQLITE_BUSY between this process's own writes.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	st := storage.NewSQLStore(db, storage.SQLite)
	if path == ":memory:" {
		return st, nil
	}
	// The write connection above created the file and switched it to WAL,
	// which is persistent. query_only refuses writes, so nothing bypasses
	// the write connection and its locking by accident.
	rdb, err := sql.Open("sqlite", readerDSN(path))
	if err != nil {
		db.Close()
		return nil, err
	}
	n := readConns()
	rdb.SetMaxOpenConns(n)
	rdb.SetMaxIdleConns(n)
	if err := rdb.Ping(); err != nil {
		return nil, errors.Join(err, rdb.Close(), db.Close())
	}
	return st.WithReader(rdb), nil
}

// readerDSN opens path for the read pool.
func readerDSN(path string) string {
	return fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=query_only(1)", path)
}

// readConns is the size of the read pool of a file database: one per CPU,
// at least 4.
func readConns() int { return max(4, runtime.GOMAXPROCS(0)) }
