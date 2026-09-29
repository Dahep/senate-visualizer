package ingest

import "strings"

// birthOnly converts the API's datetime (or "0001-01-01T00:00:00" zero value)
// to a date-only string when present; "" otherwise.
func birthOnly(fecha string) string {
	fs := strings.TrimSpace(fecha)
	if fs == "" || strings.HasPrefix(fs, "0001") {
		return ""
	}
	if len(fs) > 10 {
		return fs[:10]
	}
	return fs
}
