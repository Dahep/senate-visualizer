package handlers

import (
	"database/sql"
	"fmt"
	"html/template"
)

// FuncMap supplies the shared template helpers. Nothing here bypasses the
// auto-escaper (no safeHTML — set through the typed fields only).
func FuncMap() template.FuncMap {
	return template.FuncMap{
		// formatDate renders the raw ISO string's date segment; anything
		// malformed comes back as-is (dates are stored raw by design).
		"day": func(iso string) string {
			if len(iso) >= 10 {
				return iso[:10]
			}
			return iso
		},
		"pct": func(n, total int64) string {
			if total == 0 {
				return "0%"
			}
			return fmt.Sprintf("%.0f%%", 100*float64(n)/float64(total))
		},
		// pctf formats an already-computed fraction [0..1] as a percentage.
		"pctf": func(f float64) string {
			return fmt.Sprintf("%.0f%%", 100*f)
		},
		// pctf1 formats an already-computed fraction with one decimal.
		"pctf1": func(f float64) string {
			return fmt.Sprintf("%.1f%%", 100*f)
		},
		// ns unwraps a nullable DB string for display.
		"ns": func(v sql.NullString) string {
			if !v.Valid {
				return ""
			}
			return v.String
		},
		// votelabel renders the Spanish display label of a stored vote value
		// (the closed enum of docs/DESIGN.md gap 4).
		"votelabel": func(v string) string {
			switch v {
			case "yes":
				return "Sí"
			case "no":
				return "No"
			case "abstain":
				return "Abstención"
			case "absent":
				return "No vota"
			case "dispensed":
				return "Dispensado"
			case "paired":
				return "Pareo"
			default:
				return "Otro"
			}
		},
	}
}
