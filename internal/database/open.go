// Package database owns the connection (PRAGMAs) and goose migrations.
package database

import (
	"fmt"

	_ "modernc.org/sqlite"
)

// Open opens a SQLite connection with the schema-governing PRAGMAs.
func Open(path string) (*OpenDB, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(normal)",
		path,
	)
	return sqlOpen("sqlite", dsn)
}
