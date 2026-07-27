//go:build !nosqlite

package sqlstore

import (
	// modernc.org/sqlite rather than mattn/go-sqlite3: it is a pure-Go
	// translation of SQLite with no cgo, which is what keeps CGO_ENABLED=0
	// static builds and the scratch image working. It is the largest single
	// dependency in the binary, and `-tags nosqlite` removes it.
	_ "modernc.org/sqlite"
)

func init() { register("sqlite", "sqlite") }
