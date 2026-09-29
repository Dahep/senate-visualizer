# Phase 2 — milestone 1: cohesion analytics (landed 2026-09-29)

The local half of Phase 2 (per DISTILLED_PLAN.md): cohesion scores, loyalty,
rebels view, alignment matrix, and trend charts over the already-ingested
Cámara history (2002-2026). The remaining Phase 2 half is Senate intake —
its discovery spike is described at the bottom.

## What exists now

- `db/queries/analytics.sql` — three windowed queries (cast votes only,
  party-at-date join, `?from`/`?to` + optional party filter):
  `GetCohesionRows` (per party-votation vote counts), `GetLoyaltyRows`
  (rep-level rows for loyalty/rebels), `GetPairAgreementRows` (pairwise
  self-join for alignment).
- `internal/analytics` — pure arithmetic: aggregate Rice index
  (Σ max(yes,no,abstain) / Σ cast, vote-weighted), monthly Rice series,
  per-representative loyalty vs the party's modal vote that day. Unit-tested.
- Pages: party detail (cohesion + monthly SVG trend line via
  `/charts/party-cohesion/{id}`), representative detail (loyalty %),
  `/rebels` (loyalty ascending, ≥10 attributed votations), `/alignment`
  (pairwise same-vote rate; ≥10 joint cast votes; capped at 200 rows shown,
  most/least order toggle; 10-minute in-process cache for the ~5s self-join).
- Window defaults: rebels/alignment default to the 2022-03-11+ legislature;
  party cohesion defaults to all history.

## Verification snapshot (live DB)

- UDI cohesion over 2002-2026: 96.2% (183k cast votes, 7276 votations).
- Rebels (2022+): all-IND catch-all dominates the low-loyalty band (expected:
  independents share no real bench — indicator is only meaningful within a
  real party).
- Alignment: most-aligned pair lists truncated from 12,235 pairs to 200.

## Caveats accepted

- `IND` is a catch-all label: its "modal vote" is not a bench position, so
  loyalty for independents is weak signal (noted on the page).
- Parties with ~1 cast vote per votation get "n/a": Rice with one voter is
  definitionally 100%.
- Rice counts abstain as a position (per DESIGN.md gap 8); a party voting
  mostly abstain can score 100%.
- Modal-vote ties (e.g. a 50/50 yes/no bench split) resolve yes > no >
  abstain; only bites tiny or evenly split benches.
- Cross-chamber and Senate views are still pending the Senate spike.

## Milestone 2 (next): Senate intake

Source ladder (from DISTILLED_PLAN §2.2): 1) XHR discovery on
`senado.cl/actividad-legislativa/sala/votaciones`; 2) "Datos Abiertos
Legislativos" datasets; 3) HTML scraper with goquery feeding the same
`ingest` write path (schema already chamber-namespaced; boletín is the
cross-chamber bill key). Open with a discovery spike, exactly like the
affiliations SERVEL spike: pin exact URLs/shapes with fixtures before writing
the scraper.
