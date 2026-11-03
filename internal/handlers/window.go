package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"congress-visualizer/internal/isodate"
)

// chamberDeputies is the chamber_id every analytics query runs under
// (Senate views pending the Phase-2 Senate spike). It is part of the
// window's identity, not a handler sibling: the window policy is
// per-chamber, so Senate intake picks chamber AND defaults here.
const chamberDeputies = "camara"

// currentLegisStart is the single named legislature default (2022-2026
// legislature per docs/AFFILIATIONS_PLAN.md boundaries). Both Chilean
// chambers seat a legislature on 11 March, so one constant serves both;
// a chamber with its own schedule gains a per-chamber branch in resolve()
// — the only place a window default is decided. Do NOT add a second
// constant at a call site — it will go stale.
//
// PRODUCT QUESTION: after the 2026-03-11 changeover the 2022-2026
// legislature is closed, but "current" is still hard-coded to its start.
// Pointing it at the 2026-2030 legislature is a data/doc decision (which
// intake actually has votes per chamber?), not a code reflex.
const currentLegisStart = "2022-03-11"

// windowPolicy names the open-bound default of a window. Policies are
// explicit values instead of decisions scattered at call sites, so the
// same input cannot resolve differently on different pages.
type windowPolicy int8

const (
	// windowAllHistory keeps both bounds open: every ingested votation
	// of the chamber. Party cohesion and the rep-profile loyalty also run
	// over all history — the latter as a named policy, not a zero-value
	// accident (see RepresentativeLoyalty).
	windowAllHistory windowPolicy = iota
	// windowLegislature fills an open lower bound with currentLegisStart:
	// rank pages (/rebels, /alignment) want the current legislature, not
	// 20+ years of noise.
	windowLegislature
)

// String is the policy's display name (error/debug text, not a UI term).
func (p windowPolicy) String() string {
	switch p {
	case windowAllHistory:
		return "all-history"
	case windowLegislature:
		return "legislature"
	default:
		return fmt.Sprintf("windowPolicy(%d)", int8(p))
	}
}

// voteWindow is the deep value for vote-date ranges: a chamber-qualified
// window resolved under ONE named policy. The query is parsed exactly once
// per request into it; pages, charts and links render their from/to from
// the same value, so a chart can never describe a window other than the
// summary above it. Sent as a value (never a pointer): a window, once
// resolved, does not mutate.
type voteWindow struct {
	Chamber  string // queries are chamber-scoped; per-chamber defaults start here
	Policy   windowPolicy
	From, To string // resolved ISO bounds; "" = open
}

// windowError is one malformed ?from/?to value. It is the explicit
// validity channel of the window: malformed input is REPORTED, never
// coerced into an open bound — one meaning of invalid input on every page.
type windowError struct {
	Param, Value string
}

func (e windowError) Error() string {
	return fmt.Sprintf("fecha inválida en ?%s=%q (se espera AAAA-MM-DD)", e.Param, e.Value)
}

// windowErr answers a malformed window with ONE uniform 400 across every
// page and chart, so identical input produces an identical response no
// matter the route.
func (s *Server) windowErr(w http.ResponseWriter, req *http.Request, err error) {
	s.Log.Warn("window", "path", req.URL.Path, "err", err)
	http.Error(w, "400 — "+err.Error(), http.StatusBadRequest)
}

// newWindow parses one request's ?from/?to once and resolves the result
// under its policy.
func newWindow(req *http.Request, chamber string, policy windowPolicy) (voteWindow, error) {
	w, err := parseWindowValues(req.URL.Query())
	if err != nil {
		return voteWindow{}, err
	}
	w.Chamber = chamber
	w.Policy = policy
	return w.resolve(), nil
}

// parseWindowValues is the query's raw parse step: bounds and validity
// channel only, policy-independent, so every handler spec over the same
// input sees ONE result. Empty params are open bounds; anything present
// must parse as a YYYY-MM-DD calendar date.
func parseWindowValues(q url.Values) (voteWindow, error) {
	var w voteWindow
	// ?from is validated before ?to — deterministic error, deterministic
	// message (map order would make both-params-invalid flaky).
	for _, b := range []struct {
		param string
		value *string
	}{{"from", &w.From}, {"to", &w.To}} {
		raw := q.Get(b.param)
		if raw != "" && !isodate.Valid(raw) {
			return voteWindow{}, windowError{Param: b.param, Value: raw}
		}
		*b.value = raw
	}
	return w, nil
}

// allHistoryWindow is the explicit full-history window (no request input):
// the rep-profile loyalty policy over the deputies chamber. Named so the
// "open window by accident" variant of voteWindow{} can never reappear.
func allHistoryWindow() voteWindow {
	return voteWindow{Chamber: chamberDeputies, Policy: windowAllHistory}
}

// resolve applies the policy's default to open bounds. This is the only
// place a window default is decided; call sites name a policy, nothing
// else.
func (w voteWindow) resolve() voteWindow {
	if w.Policy == windowLegislature && w.From == "" {
		w.From = currentLegisStart
	}
	return w
}

// Query renders the URL query fragment of the RESOLVED window — with the
// leading "?" — so a chart (or any link) re-parses exactly the bounds the
// page queried: "" when the window is fully open, "?from=..&to=.."
// otherwise. Templates interpolate it right after the path segment:
// html/template URL-escapes the value once, and the literal "?" must be
// part of it (a bare "from=.." interpolated next to the path is
// percent-escaped into "?from%3d..").
func (w voteWindow) Query() string {
	var b strings.Builder
	sep := "?"
	for _, bnd := range [2]struct {
		param string
		value string
	}{{"from", w.From}, {"to", w.To}} {
		if bnd.value == "" {
			continue
		}
		b.WriteString(sep)
		sep = "&"
		b.WriteString(bnd.param)
		b.WriteString("=")
		b.WriteString(url.QueryEscape(bnd.value))
	}
	return b.String()
}
