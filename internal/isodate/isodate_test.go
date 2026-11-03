package isodate

import "testing"

// The one date rule both boundaries now share: a real YYYY-MM-DD calendar
// date. Anything else — the 0000-00-00 / 9999-99-99 sentinels the old
// shape-only rules accepted, impossible calendar dates, wrong shapes —
// fails along with it.
func TestValid(t *testing.T) {
	for _, s := range []string{"2022-03-11", "2026-03-11", "0000-01-01"} {
		if !Valid(s) {
			t.Fatalf("%s must be valid", s)
		}
	}
	for _, s := range []string{
		"", "2022-3-11", "2022-03-1", "abcd-ef-gh",
		"0000-00-00", "9999-99-99", "2026-02-30", "2026-13-01", "2022-03-11T14:00:00",
	} {
		if Valid(s) {
			t.Fatalf("%s must not be valid", s)
		}
	}
}
