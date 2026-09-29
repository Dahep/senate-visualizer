# Multi-period party affiliations — dated plan

Goal: cover `representative_parties` for votations the ingest already holds
(2011 → 2026) so party-at-date joins, breakdowns, and Phase 2 cohesion work
across the full history — not just the current legislature.

## Current state (validated live, 2026-09-16)

| Source | Covers | Status |
| ------ | ------ | ------ |
| camara.cl ficha parlamentaria (`Partido:` field) | 2026-2030 only | ✅ used by the bot drafter; 155/155 mapped |
| camara.cl ficha "Periodos parlamentarios" year list | years only — **no per-period party exposed** | ✅ usable as coverage checklist |
| Cámara open-data API (`getDiputados*`) | `Militancias_Periodos` verified empty in GET and SOAP bindings, all periods | ❌ dead end, confirmed twice |
| SERVEL `archivo.servel.cl` + "Centro de datos" | candidate records per election incl. party (elected ⇒ deputy of that period) | ⚠️ file URLs behind a JS-driven portal; needs a discovery spike |
| BCN biografías parlamentarias (bcn.cl/siit) | per-deputy, per-period party, whole history | ⚠️ site reshuffle; needs scrape probing |

File workflow (`data/affiliations.json` (single source of truth; bot traces live in `data/traces/`), `curation.Validate/Apply`) is already
built and period-agnostic — the plan only changes the FILE, not the pipeline.
`data/affiliations.draft.json` remains the current-period draft; the reviewed
file becomes `data/affiliations.json`.

## Period boundaries (hard-coded, verified against API periods)

| Legislature | start | end |
| ----------- | ----- | --- |
| 2026-2030 | 2026-03-11 | (open) |
| 2022-2026 | 2022-03-11 | 2026-03-10 |
| 2018-2022 | 2018-03-11 | 2022-03-10 |
| 2014-2018 | 2014-03-11 | 2018-03-10 |
| 2010-2014 | 2010-03-11 | 2014-03-10 |

(older periods only if the DB's `min(vote_date)` reaches them)

## Schema of a multi-period row (no pipeline change)

```json
{ "chamber": "camara", "external_id": "843",
  "party": "RN", "start": "2010-03-11", "end": "2014-03-10" }
```

Invariants enforced by `curation.Validate` (already implemented):
non-overlapping ranges per deputy — background continuity means a new
legislature row STARTS exactly at the previous row's end+1 day or later;
mid-period switches get their own row inside the period.

## Stage 1 — SERVEL candidate records (primary, authoritative)

SERVEL publishes elected-candidate listings per parliamentary election
(2009, 2013, 2017, 2021, 2025) with the declared party per candidate. The
elected become that period's deputies; the party row spans the full period
unless a mid-period switch row overrides it later.

Spike (1 session): find the direct file URLs behind the "Centro de datos" +
archivo.servel.cl results pages (XLSX/CSV "Resultados (senadores y
diputados)"), extract `DIPID ↔ party@period` — DIPID matching happens via
name (`Nombre Apellido_Paterno Apellido_Materno`) because SERVEL has no
DIPID; the aligner falls back to normalized-name matching and reports every
unmatched deputy loudly rather than guessing.

Output: one `draft_affiliations --period <YYYY-YYYY>` run mode; same review
CSV format as today, one file per period, then concatenated.

## Stage 2 — BCN biografías (fallback + mid-period switches)

BCN's biografías parlamentarias per deputy are the only public source with
switches WITHIN a period (the well-known frelections: Fuenzalida, Jiles,
Kast, Naranjo...). Each bio lists militancias with dates; the same
normalized-name aligner emits rows. Runs only for deputies whose SERVEL row
leaves coverage gaps (keeps it cheap).

## Stage 3 — mid-period deltas

After both sources: the existing seed `ValidateAgainstDB` + `AffiliationGaps`
count isolates votations where a deputy voted without party-at-date coverage
— each gap is one manual decision (true independent, late switch, or OCR
error). Party switches within a period are expected to be few dozens.

## Reviews and rollout

1. Extend `scripts/draft_affiliations.go` with `--period 2022-2026` runs;
   per-period CSV lands in `data/affiliations.review.<period>.csv`.
2. Human review: party-mapping renames land ONLY in `parties.json` (name →
   short_name); deputies flagged `??<raw>` are resolved by hand.
3. Concatenate accepted per-period rows into `data/affiliations.json`
   (`jq -s` concat of drafts, or manual), run `make seed-check` (CI
   validation), then `go run ./cmd/sync -seed-parties data/parties.json -seed data/affiliations.json`.
4. Verify: the votation-detail breakdown stops saying "sin partido" for any
   vote from a covered period; `AffiliationGaps` in the sync result trends
   to ~0 for the ingested date range.

## Decision table (what we accept)

- We accept coarse period-boundary dates (March 11) as membership starts/ends
  in exchange for 90% correct party-at-date attribution from mechanical
  sources; mid-period switch rows are the human-curation exception, not the
  default shape.
- We accept SERVEL's "party at candidacy" (not "party at vote") as the
  default row semantics — a deputy who legitimately switched parties
  mid-period is a reviewed edge, not silently misattributed.
- We do NOT try to rescue the Cámara API's empty militancia fields.

## Next step

Stage-1 discovery spike: locate SERVEL's direct candidate-list file URLs for
the 2021 and 2017 elections; write `scripts/draft_affiliations --period` ON THE
EXACTLY-FOUND URLs; run, review, apply.

## Final state (2026-09-16)

- `data/affiliations.json` — the only curated source truth (903 rows: 2002-2026 wiki + 2022-2026 SERVEL roster +/- current ficha).
- `data/traces/` — the bot trail: per-period review CSVs, draft JSONs, wiki report (zero actionable items remaining).
- `data/parties.json` — 30-party registry with historical parties (inc[PRI/IC/AMP], current PSC, CS, COM).
- Status: applied. Party-at-date coverage:

| Band | Coverage |
|------|----------|
| 2002-2009 | 94.5-98.1% |
| 2010-2017 | 96.0-99.1% |
| 2018-2021 | 78.6-80.7% (mid-period switches, unassigned by design) |
| 2022-2025 | 98.4-99.5% |
| 2026 (current) | 100.0% |
