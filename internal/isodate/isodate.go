// Package isodate is the ONE ISO-date rule shared by every date boundary:
// page windows (?from/?to) and the curation file validator alike accept
// exactly a real calendar date in YYYY-MM-DD. time.Parse does the work, so
// an impossible date (2026-02-30) and any wrong shape fail together — there
// is no second, more permissive copy of this rule anymore (the digit/dash
// scan in handlers and the ^\d{4}-\d{2}-\d{2}$ regex in curation both
// accepted the 0000-00-00 / 9999-99-99 sentinels).
package isodate

import "time"

// layout is the only accepted date shape.
const layout = "2006-01-02"

// Parse parses s as a YYYY-MM-DD calendar date.
func Parse(s string) (time.Time, error) {
	return time.Parse(layout, s)
}

// Valid reports whether s is a YYYY-MM-DD calendar date.
func Valid(s string) bool {
	_, err := Parse(s)
	return err == nil
}
