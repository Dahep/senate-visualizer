package ingest

import (
	"database/sql"
	"strings"
)

func nullIfEmpty(v string) sql.NullString {
	if strings.TrimSpace(v) == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: strings.TrimSpace(v), Valid: true}
}
