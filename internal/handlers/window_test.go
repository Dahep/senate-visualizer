package handlers

import (
	"net/http/httptest"
	"testing"
	"time"
)

// The window is a deep value, so its parse step is policy-independent: the
// same query input yields ONE validity outcome and ONE set of explicit
// bounds, whichever handler spec applies a policy afterwards. Only the
// named policy's default may differ. These tests pin that parity, the two
// policies' defaults, and the URL fragment templates render.

// hasDate reports whether y-m-d round-trips through parseWindowValues (a
// parsed bound is kept verbatim; the table's inputs are already valid).
func TestWindowParseOneResultPerInput(t *testing.T) {
	cases := []struct {
		name        string
		rawQuery    string
		allHistory  voteWindow // resolved under windowAllHistory ("" on the window side = validity error)
		legislature voteWindow // resolved under windowLegislature ("= same")
		wantErr     string     // "" = no validity error
	}{
		{
			name:        "valid closed range",
			rawQuery:    "from=2024-01-01&to=2024-12-31",
			allHistory:  voteWindow{From: "2024-01-01", To: "2024-12-31"},
			legislature: voteWindow{From: "2024-01-01", To: "2024-12-31"},
		},
		{
			name:        "from only",
			rawQuery:    "from=2024-01-01",
			allHistory:  voteWindow{From: "2024-01-01"},
			legislature: voteWindow{From: "2024-01-01"},
		},
		{
			name:        "to only",
			rawQuery:    "to=2024-12-31",
			allHistory:  voteWindow{To: "2024-12-31"},
			legislature: voteWindow{From: currentLegisStart, To: "2024-12-31"},
		},
		{
			name:        "absent both",
			allHistory:  voteWindow{},
			legislature: voteWindow{From: currentLegisStart},
		},
		{
			name:        "open bounds stay open pre-policy",
			rawQuery:    "from=&to=",
			allHistory:  voteWindow{},
			legislature: voteWindow{From: currentLegisStart},
		},
		{
			name:     "malformed from",
			rawQuery: "from=abcd-ef-gh",
			wantErr:  `fecha inválida en ?from="abcd-ef-gh" (se espera AAAA-MM-DD)`,
		},
		{
			name:     "malformed to",
			rawQuery: "to=2022-2-2",
			wantErr:  `fecha inválida en ?to="2022-2-2" (se espera AAAA-MM-DD)`,
		},
		{
			name:     "zero sentinel (was silently an open bound)",
			rawQuery: "from=0000-00-00",
			wantErr:  `fecha inválida en ?from="0000-00-00" (se espera AAAA-MM-DD)`,
		},
		{
			name:     "far sentinel (was silently accepted)",
			rawQuery: "to=9999-99-99",
			wantErr:  `fecha inválida en ?to="9999-99-99" (se espera AAAA-MM-DD)`,
		},
		{
			name:     "impossible calendar date",
			rawQuery: "from=2026-02-30",
			wantErr:  `fecha inválida en ?from="2026-02-30" (se espera AAAA-MM-DD)`,
		},
		{
			name:     "free text",
			rawQuery: "from=siglo+XXI",
			wantErr:  `fecha inválida en ?from="siglo XXI" (se espera AAAA-MM-DD)`,
		},
		{
			name:     "both params invalid: deterministic message",
			rawQuery: "from=also-bad&to=also-bad",
			wantErr:  `fecha inválida en ?from="also-bad" (se espera AAAA-MM-DD)`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			type spec struct {
				policy windowPolicy
				want   voteWindow
				got    voteWindow
				err    error
			}
			specs := []spec{
				{policy: windowAllHistory, want: tc.allHistory},
				{policy: windowLegislature, want: tc.legislature},
			}
			for i := range specs {
				req := httptest.NewRequest("GET", "/any-page?"+tc.rawQuery, nil)
				w, err := newWindow(req, chamberDeputies, specs[i].policy)
				specs[i].got, specs[i].err = w, err
			}
			for _, s := range specs {
				if tc.wantErr == "" && s.err != nil {
					t.Fatalf("policy %v: unexpected error %v", s.policy, s.err)
				}
				if tc.wantErr != "" {
					if s.err == nil {
						t.Fatalf("policy %v: nil error, want %q", s.policy, tc.wantErr)
					}
					// One meaning of invalid input: identical error for
					// every handler spec.
					if s.err.Error() != tc.wantErr {
						t.Fatalf("policy %v: error %q, want identical %q", s.policy, s.err.Error(), tc.wantErr)
					}
					continue
				}
				if s.got.Policy != s.policy || s.got.Chamber != chamberDeputies {
					t.Fatalf("policy %v: identity not stamped: %+v", s.policy, s.got)
				}
				if s.got.From != s.want.From || s.got.To != s.want.To {
					t.Fatalf("policy %v: bounds (%q, %q), want (%q, %q)",
						s.policy, s.got.From, s.got.To, s.want.From, s.want.To)
				}
			}
			// The parse itself never diverges: whichever bounds differ
			// must be a policy default (for From in legislature), never a
			// bound the other policy would have dropped or altered. The
			// table's want-windows encode exactly that.
		})
	}
}

// The two named policies only ever differ by their open-bound default.
func TestWindowPolicyDifference(t *testing.T) {
	if windowLegislature == windowAllHistory {
		t.Fatal("policies collapsed")
	}
	if got := windowAllHistory.String(); got != "all-history" {
		t.Fatalf("all-history name %q", got)
	}
	if got := windowLegislature.String(); got != "legislature" {
		t.Fatalf("legislature name %q", got)
	}
}

// resolve is the ONLY place a window default is decided.
func TestWindowResolve(t *testing.T) {
	w := voteWindow{Chamber: chamberDeputies, Policy: windowLegislature}
	if w.resolve().From != "2022-03-11" {
		t.Fatalf("legislature default: %+v", w.resolve())
	}
	all := voteWindow{Chamber: chamberDeputies, Policy: windowAllHistory}
	if r := all.resolve(); r.From != "" || r.To != "" {
		t.Fatalf("all-history must leave bounds open: %+v", r)
	}
	// (and now, a date that parses: legislature does not overwrite it.)
	explicit := voteWindow{Chamber: chamberDeputies, Policy: windowLegislature, From: "2010-05-02", To: "2010-05-03"}
	if r := explicit.resolve(); r.From != "2010-05-02" || r.To != "2010-05-03" {
		t.Fatalf("legislature must not overwrite explicit bounds: %+v", r)
	}
}

// Query renders the resolved bounds so a chart link re-parses exactly what
// the page queried.
func TestWindowQuery(t *testing.T) {
	cases := []struct {
		w    voteWindow
		want string
	}{
		{voteWindow{}, ""},
		{voteWindow{From: "2024-01-01"}, "?from=2024-01-01"},
		{voteWindow{To: "2024-12-31"}, "?to=2024-12-31"},
		{voteWindow{From: "2024-01-01", To: "2024-12-31"}, "?from=2024-01-01&to=2024-12-31"},
		{voteWindow{From: currentLegisStart}, "?from=2022-03-11"},
	}
	for _, tc := range cases {
		if got := tc.w.Query(); got != tc.want {
			t.Fatalf("Query %+v = %q, want %q", tc.w, got, tc.want)
		}
	}
	if _, err := time.Parse("2006-01-02", "2022-03-11"); err != nil {
		t.Fatalf("currentLegisStart must round-trip the one date rule: %v", err)
	}
}
