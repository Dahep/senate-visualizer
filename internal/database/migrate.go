package database

import (
	"database/sql"

	"github.com/pressly/goose/v3"
)

func sqlOpen(driver, dsn string) (*OpenDB, error) {
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	return &OpenDB{DB: db}, nil
}

// OpenDB wraps *sql.DB so Open doesn't shadow sqlc's New/Queries API.
type OpenDB struct{ DB *sql.DB }

func (o *OpenDB) Close() error { return o.DB.Close() }

// Migrate applies goose migrations from a directory.
func (o *OpenDB) Migrate(dir string) error {
	goose.SetDialect("sqlite3")
	goose.SetVerbose(false)
	return goose.Up(o.DB, dir)
}

// DB exposes the raw handle for transactions (the Syncer's per-votation tx).
func (o *OpenDB) Raw() *sql.DB { return o.DB }
