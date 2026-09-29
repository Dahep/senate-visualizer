# Synthesized MVP design — Chilean Congress vote visualizer (Cámara only)

Synthesis: base = candidate A (the only candidate whose discovery strategy, XML field names, and API behavior claims survived live verification), with grafts from candidates B and C (see *Synthesis decision*).

## Problem

Build the MVP of a server-rendered web app that lets users browse Cámara de Diputados votations, see per-deputy and per-party breakdowns, and follow representatives, parties, and bills. Stack is fixed: Go + chi, SQLite (modernc.org/sqlite), sqlc, goose, html/template + Tailwind, HTMX (zero authored JS), go-chart SVG. The shape is non-obvious because the Cámara API has **no endpoint that enumerates votations**, no bill-detail endpoint, and no usable party affiliation — three of the app's core entities therefore have no canonical upstream source and must be derived. Phase A constraints honored here: sqlc queries only from `db/queries/*.sql`, goose migrations, compile-time SQL validation, and a schema that must not paint Phase 2 (Senate, cohesion analytics) into a corner.

Facts verified against the live API during this design (not assumed from docs):

- The WSDL lists 13 operations; none enumerates votations by session or legislature. `getSesionDetalle` returns session metadata only (no votation list); `getSesionBoletinXML(4746)` returns an **empty body**. Session-based enumeration is a dead end.
- `getVotacion_Detalle?prmVotacionID=N`: a miss returns `<Votacion xsi:nil="true"/>`; IDs are locally contiguous (87460 and 87461 both hit); ID 1 is a miss, so the historical floor must be discovered.
- Detail responses embed a `Sesion` block (`ID`, `Numero`, `Fecha`, `Tipo`), `Boletin`, `Articulo` (per-vote subject text, **can be empty**), `Tramite`, `Informe` (persist later or ignore), and a `Pareos` collection; the per-vote element is nested `<Voto><Diputado><DIPID>…</Diputado><Opcion Codigo="…">text</Opcion></Voto>` — pareos are not an `<Opcion>` value, they arrive as deputy pairs.
- `getDiputados_Vigentes` returns 155 deputies; `Militancia_Actual` / `Militancias_Periodos` fields exist in the schema but are **unpopulated in every record**. Party affiliation genuinely must be curated.
- API dates are sloppy (`FechaTermino>2030-10T23:59:59`): never let a malformed source date fail an ingest.

## Usage (caller's view)

### README quickstart

```sh
make setup            # install sqlc, goose, tailwindcss CLI
make generate migrate # sqlc codegen (validates SQL at build time) + goose up
make seed             # parties.json + SyncDeputies (1 request) + affiliations.json (validated)
make sync-full        # one-shot historical load: cmd/sync -full   (resumable; single-writer)
make run              # server on :8080; background incremental sync every 6h
```

`cmd/sync` and the server's background scheduler are two drivers over the *same* `ingest.Syncer`; `make sync-full` is `go run ./cmd/sync -full`, the scheduler is `Syncer` on a ticker. There is one write path into `votations`/`individual_votes`/`bills`/`sessions` and it lives in `internal/ingest`. Handlers are read-only.

### Call site 1 — the sync binary (full load and incremental tick share everything)

```go
// cmd/sync/main.go
func main() {
    full := flag.Bool("full", false, "historical scan from the ID floor")
    flag.Parse()

    cfg := config.Load()
    db, err := database.Open(cfg.DBPath)          // PRAGMAs: WAL, foreign_keys=ON, busy_timeout
    if err != nil { log.Fatal(err) }
    defer db.Close()
    if err := database.Migrate(db); err != nil { log.Fatal(err) }

    client := camara.NewClient(cfg.APIBaseURL, cfg.APIDelay) // rate-limited, bounded-retry
// Client contract: path-style invocation (<APIBaseURL>/<op>?prm...) — the
// `?op=` query form returns the ASMX HTML help page, not XML. Every response
// must be Content-Type text/xml (fallback check: first byte '<'); an HTML
// body is classified retryable, never decoded. Per-request timeout 30s
// (context-bound), max 5 retries with exponential backoff on 429/5xx/network;
// a permanently failing request is terminal for that unit of work.
    syncer := ingest.NewSyncer(database.New(db), db, client, cfg.Discovery)

    ctx := context.Background()
    if *full {
        // Cold start: probe UP from a seed ID to find the ceiling, then
        // walk DOWN from the ceiling to the bisect-probed floor.
        res, err := syncer.Scan(ctx, ingest.ScanDirectionDown)
        logResult(res, err)
    } else {
        res, err := syncer.Tick(ctx) // discover new IDs above the ceiling,
                                     // drain the candidates queue, refresh deputies
        logResult(res, err)
    }
    // Drivers are mutually exclusive by lease: the Syncer refuses to run
    // without owning the `sync_lock` row (owner + expiry, compare-and-swap);
    // the server's scheduler skips its tick while a CLI scan holds it. This
    // also keeps one process owning the shared 150ms rate limiter.
}
```

### Call site 2 — votation detail handler (page + HTMX partial + SVG chart share one query)

```go
// internal/handlers/votations.go
func VotationDetail(q *database.Queries, r *render.Renderer) http.HandlerFunc {
    return func(w http.ResponseWriter, req *http.Request) {
        id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
        if err != nil { http.NotFound(w, req); return }

        v, err := q.GetVotation(req.Context(), id)          // sqlc :one
        if errors.Is(err, sql.ErrNoRows) { http.NotFound(w, req); return }
        if err != nil { http.Error(w, "db error", 500); return }

        votes, err := q.GetVotesByVotation(req.Context(), id) // join rep + party-as-of-date
        if err != nil { http.Error(w, "db error", 500); return }
        breakdown := analytics.PartyBreakdown(votes)          // pure: []VoteRow -> []PartySlice

        r.Page(w, "votation_detail", VotationDetailData{Votation: v, Breakdown: breakdown})
        // Chart: standalone GET /charts/party-breakdown/{id} handler renders
        // the same Breakdown rows via go-chart as an image/svg+xml response
        // (referenced from templates with <img src>). No template.HTML cast,
        // no inline SVG, no safeHTML — API-derived labels never bypass the
        // template escaper. charts.* still xml-escape every label internally.
    }
}
```

### Call site 3 — affiliation seeding with validation at the boundary

```go
// cmd/sync (subcommand) — also run as `make seed-check` in CI
func seedAffiliations(ctx context.Context, q *database.Queries, path string, checkOnly bool) error {
    file, err := curation.LoadAffiliations(path)   // parse + schema checks
    if err != nil { return err }

    problems := curation.Validate(file)            // pure: dupes, overlaps, date order, unknown parties
    dbProblems, err := curation.ValidateAgainstDB(ctx, q, file) // rep exists? coverage gaps?
    problems = append(problems, dbProblems...)
    if err != nil || len(problems) > 0 {
        return &curation.ValidationError{Problems: problems} // prints ALL problems, exit 1
    }
    if checkOnly { return nil }
    return curation.Apply(ctx, q, file) // tx: DELETE WHERE source='curated'; INSERT file rows
}
```

## Shape

### Module map

```
cmd/server/main.go          # web server; embeds scheduler (SYNC_INTERVAL, default 6h)
cmd/sync/main.go            # -full | -once | -seed | -seed-check: every write-side driver
internal/config/            # env-driven Config struct, no globals
internal/database/          # Open (PRAGMAs), Migrate (goose), + sqlc-generated code
internal/ingest/
    sync.go                 # Syncer: SyncVotationID, SyncDeputies, Scan, Tick
    discovery.go            # VotationDiscovery iface; IDScanner; BoletinExpander
    scheduler.go            # ticker loop calling Syncer.Tick
    camara/
        client.go           # rate-limited net/http client, retries, sentinel ErrNotFound
        types.go            # xml structs matching the VERIFIED field names
        mapping.go          # pure: VotacionXML -> VotationData, NormalizeVoteOption
internal/curation/          # parties/affiliations file loading, validation, Apply
internal/handlers/          # chi handlers, one file per resource, read-only
internal/render/            # template loader + Renderer (see §7)
internal/analytics/         # pure functions over query rows: breakdown, cohesion, loyalty
internal/charts/            # go-chart -> SVG strings
db/migrations/001_initial_schema.sql   # = SCHEMA.sql shipped alongside this design
db/queries/*.sql            # votations, individual_votes, representatives, parties,
                            # bills, sessions, sync.sql; sqlc.yaml validates at build
data/parties.json           # party registry: short_name, name, color
data/affiliations.json      # curated memberships: rep external_id -> party + date range
web/templates/{layouts,pages,partials}/*.html
web/static/                 # tailwind output; js/ stays empty
```

### Core types and signatures

```go
package ingest

// VotationDiscovery yields candidate external votation IDs. The Syncer does
// not care which strategy produced them (per boundary-discipline).
//
// Graft from candidate C: every discovered ID is persisted in the durable
// `votation_candidates` queue (INSERT OR IGNORE, fetched_at cursor), so a
// crashed drain resumes for free and future discovery sources (Senate, manual
// adds) plug in behind the same queue.
type VotationDiscovery interface {
    // Next returns the next batch of IDs to try. done=true means exhausted.
    Next(ctx context.Context) (ids []int, done bool, err error)
    // RecordMiss/RecordHit feed back so scanners can self-terminate.
    RecordHit(id int)
    RecordMiss(id int)
}

// IDScanner is the primary strategy: getVotacion_Detalle must be called once
// per votation anyway, so probing the ID space discovers votations for FREE.
// NOTE: the space is SPARSE and NON-MONOTONIC (live-verified: 42338 hits with
// nil neighbors; large empty runs between 1 and ~42338) — never assume
// contiguity; gap runs are recorded and tolerance sweeps widen geometrically.
type IDScanner struct {
    HighWater, Floor, ScanCursor int // sync_metadata: high_water / floor / camara.scan_cursor
    GapTolerance  int // consecutive misses that END the current sweep and
                      // trigger geometric probe-ahead (10x, then 100x the
                      // tolerance; a hit continues the sweep) — default 50
    // ... not implemented
}
// ScanResult carries EndReason ∈ {Floor, ToleranceWidened, Error} so the
// operator can never confuse "reached the floor" with "gave up in a gap".
// Success additionally asserts depth: min(vote_date) is surfaced in the sync
// result — a "completed" scan that only spans recent days is a loud failure.

// BoletinExpander is the fallback (graph BFS, still API-only): from boletins
// already in the DB, call getVotaciones_Boletin to harvest votation IDs
// outside the scanned range; repeat with newly-seen boletins.
type BoletinExpander struct{ /* q *database.Queries, client *camara.Client; not implemented */ }

type Syncer struct {
    q      *database.Queries
    db     *sql.DB          // for the one-transaction-per-votation boundary
    client *camara.Client
    disc   VotationDiscovery // IDScanner primary, BoletinExpander fallback
}

func NewSyncer(q *database.Queries, db *sql.DB, c *camara.Client, d VotationDiscovery) *Syncer {
    panic("not implemented")
}

// SyncVotationID is THE unit of work. Idempotent: safe to re-run, safe to
// crash mid-way (one tx per votation). Returns what happened.
func (s *Syncer) SyncVotationID(ctx context.Context, externalID int) (Outcome, error) {
    panic("not implemented")
    // 1. det, err := client.GetVotacionDetalle(externalID)
    //    - errors.Is(err, camara.ErrNotFound)  -> Outcome{Miss: true}   (xsi:nil body)
    // 2. data := camara.MapVotacion(det)      // pure; NormalizeVoteOption total
    // 3. tx:
    //    a. UpsertSession(data.Session)                       (free linkage, verified in XML)
    //    b. if data.Boletin != "": UpsertBillStub(boletin, data.Articulo)
    //    c. UpsertVotation(...) ON CONFLICT(chamber_id, external_id) DO UPDATE ... RETURNING id
    //    d. delete-and-replace vote rows for this votation (one DELETE +
    //       batched INSERT), so re-syncing applies upstream corrections
    //       (graft B instead of per-row INSERT OR REPLACE)
    //    e. for each pareo pair: UPSERT as 'paired' with
    //       ON CONFLICT DO UPDATE — an override must be an update, not a
    //       WHERE NOT EXISTS insert (live-verified: every pareo member also
    //       carries an explicit `No Vota` row; NOT EXISTS would lose 100% of
    //       pareos; see gap 4). vote_raw keeps the original `No Vota` text.
    // 4. cursors: the HIGH-WATER is advanced ONLY by the sequential scanner
    //    (drained queue candidates may sit far above the frontier — advancing
    //    from them would mark ranges as scanned that were never probed).
    //    The DURABLE scan cursor (camara.scan_cursor) is updated inside this
    //    transaction on every completed direct-scan ID, so a crashed 4h scan
    //    resumes exactly where it stopped.
}

func (s *Syncer) Scan(ctx context.Context, dir ScanDirection) (ScanResult, error) { panic("not implemented") }
func (s *Syncer) Tick(ctx context.Context) (ScanResult, error) { panic("not implemented") }
// Tick: requires initialized cursors (bootstrapped by the first Scan); walks
// up from high_water, overlapping -(overlap window) to self-heal, DRAINS the
// candidates queue, then SyncDeputies.
func (s *Syncer) Drain(ctx context.Context, limit int) (ScanResult, error)  { panic("not implemented") }
// Drain: SyncVotationID for each pending votation_candidates row; on miss the
// candidate is stamped fetched_at; on error attempts++/last_error, and
// attempts >= 5 quarantines the row (excluded from the pending query,
// reported in the sync result) — no infinite retry.
// Drain NEVER advances high_water (see SyncVotationID step 4).
func (s *Syncer) SyncDeputies(ctx context.Context) (int, error) { panic("not implemented") }
// SyncDeputies: fetches getDiputados_Vigentes AND getDiputados. ACTIVE is
// granted by membership in the vigentes set (getDiputados has no per-record
// active marker — verified); getDiputados enriches stubs with gender /
// birthdate (Fecha_Nacimiento can be absent). Stubs start active=0.
```

```go
package camara

// Field names below are the ones returned by the live HTTP-GET binding,
// verified 2026-09 — not the names in PLAN_GO.md's simplified example.
type VotacionXML struct {
    XMLName           xml.Name `xml:"Votacion"`
    Nil               string   `xml:"nil,attr"`        // "true" => sentinel miss
    ID                int      `xml:"ID"`
    Fecha             string   `xml:"Fecha"`           // keep as string; source dates are sloppy
    Tipo              CodedText `xml:"Tipo"`           // <Tipo Codigo="6">Única</Tipo>
    Resultado         CodedText `xml:"Resultado"`
    Quorum            CodedText `xml:"Quorum"`
    Sesion            *SesionXML `xml:"Sesion"`
    Boletin           string   `xml:"Boletin"`
    Articulo          string   `xml:"Articulo"`        // may be empty
    Tramite           CodedText `xml:"Tramite"`
    TotalAfirmativos  int      `xml:"TotalAfirmativos"`
    TotalNegativos    int      `xml:"TotalNegativos"`
    TotalAbstenciones int      `xml:"TotalAbstenciones"`
    TotalDispensados  int      `xml:"TotalDispensados"`
    Votos             []VotoXML `xml:"Votos>Voto"`
    Pareos            []PareoXML `xml:"Pareos>Pareo"`
}

// Vote is the internal closed enum; storage CHECK mirrors it exactly.
type Vote string
const (
    VoteYes Vote = "yes"; VoteNo = "no"; VoteAbstain = "abstain"; VoteAbsent = "absent"
    VoteDispensed Vote = "dispensed"; VotePaired = "paired"; VoteOther = "other"
)

// NormalizeVoteOption is TOTAL: unknown source values become VoteOther, never
// an error, never dropped. The raw string is always stored in vote_raw.
// Observed codes: 0=En Contra, 1=Afirmativo, 2=Abstencion, 4=No Vota
// (3 = Dispensado, unconfirmed). Map on text, not undocumented codes.
// Live-verified shape (2026-09-15): the vote element is nested <Voto>
// <Diputado><DIPID>…</DIPID><Nombre>…</Nombre><Apellido_Paterno>…</Apellido_Paterno></Diputado>
// <Opcion Codigo="0">En Contra</Opcion></Voto> — there is NO 'OpcionVoto'
// element; a struct written with that tag decodes silently to zero votes.
// encoding/xml ignores unknown elements by design, so the ingest-side totals
// cross-check (hard-fail when ZERO vote rows decode) is the tripwire.
func NormalizeVoteOption(opcion string) Vote {
    switch strings.ToLower(strings.TrimSpace(opcion)) {
    case "afirmativo":  return VoteYes
    case "en contra":   return VoteNo
    case "abstencion", "abstención": return VoteAbstain
    case "no vota":     return VoteAbsent
    case "dispensado":  return VoteDispensed
    case "pareo":       return VotePaired
    default:            return VoteOther // + slog.Warn at call site
    }
}
```

### The eight gaps, resolved

**1. Votation discovery — sequential ID scan (primary), boletín graph expansion (out-of-band source).** Verified live: no enumeration endpoint exists; `getSesionDetalle` carries no votations; `getSesionBoletinXML` returns empty; misses are cleanly detectable (`xsi:nil`). Because every votation requires one `getVotacion_Detall` call regardless, scanning the ID space discovers votations at zero extra request cost — enumeration IS the detail fetch. **The space is sparse and non-monotonic (live-verified 2026-09-15: ID 42338 = 2024-11-14, nils immediately adjacent; the populated range spans at least ~42.3k–87.4k with large holes).** Therefore: `Scan(Down)` cold-starts by probing UP from a seed to establish the ceiling, then walks DOWN to a bisect-probed floor; a sweep never terminates on miss count alone — hitting `GapTolerance` triggers geometric probe-ahead (10×, 100×) before declaring an edge, and `ScanResult.EndReason ∈ {Floor, Tolerance, Error}` makes "stopped in a gap" loud. Cursors (`high_water`, `floor`, `camara.scan_cursor`) live in `sync_metadata`, updated per batch — a crashed scan resumes at `scan_cursor`. The BoletinExpander runs through the same queue but only as an operator-invoked completeness check: after a scan, sample known boletins from the DB and confirm each's votation IDs were scanned; surfaced differences are investigated rather than silently trusted.

**2. Bill metadata — provisional, honest, upgradeable.** No bill endpoint exists. `bills.id` is the boletín (cross-chamber natural key, ready for Phase 2). Title = the non-empty `Articulo` from the MOST RECENT votation ingested for that bill (deterministic across ingest orders; `bills.title_date` tracks it), else placeholder `'Boletín 15351-07'`; `title_source ∈ {votation_articulo, manual, enriched}` records provenance so a Phase 2 enrichment job (scrape tramitación/LeyChile) can tell which rows to upgrade. `MapVotacion` normalizes the boletín boundary (trim, must match `^\d+-\d+$`; a legit-but-unusual format or junk string → `bill_id IS NULL` + warn — never fails the votation; the schema CHECK is only a backstop). Empty `Boletin` in a votation → `bill_id IS NULL` (never a bogus empty-key bill). `status`/`law_number`/`filed_date` stay NULL in the MVP and are simply not rendered.

**3. Representative identity — internal PK + namespaced natural key.** `representatives.id` is an internal autoincrement used by every FK, URL, and join; `UNIQUE(chamber_id, external_id)` is the boundary key. `external_id` is TEXT so Senate slugs or ints both fit, and the chamber namespace makes collisions structurally impossible when Senate arrives. The same rule applies to `votations` and `sessions` — one invariant, three tables. Ingest resolves natural→internal via `INSERT ... ON CONFLICT(chamber_id, external_id) DO UPDATE ... RETURNING id` (sqlc `:one`). Vote rows create stub representatives (DIPID + names, which the vote XML carries); `SyncDeputies` fetches BOTH `getDiputados_Vigentes` (members of the vigentes set are `active=1`; the endpoint has no per-record active field) and `getDiputados` (all-history shape/census: gender, birthdate — `Fecha_Nacimiento` can be absent), so historical deputies absent from the vigentes list still exist. Stubs default `active=0`; the vigentes list grants it.

**4. Vote value set — closed enum, lossless storage.** `individual_votes.vote` CHECK-constrained to `{yes,no,abstain,absent,dispensed,paired,other}`; `vote_raw` keeps the verbatim `<Opcion>` text (for pareo overrides of a `No Vota` row, it keeps the original `No Vota` — the override is visible in `vote`; `vote_raw='Pareo'` only for a row created by the pareo pass). `Dispensado` maps to `dispensed`; pareos are ingested from the separate `Pareos` collection as `paired` rows — **Pareos membership overrides an explicit `No Vota` row for the same deputy** (live-verified in votation 87461: all pareo members also carry `No Vota` rows, so an explicit-row-wins rule silently loses every pareo). `VoteOther` + warning log is the escape hatch: ingest never fails and never silently drops because the API invented a value. `votations.total_*` mirror the source totals (`total_dispensed`, not the plan's `total_absent` — absent is not reported, it is derived from rows); display counts always derive from `individual_votes`, and the stored totals are audit copies cross-checked at ingest as four equations — `total_yes == count('yes')`, `total_no == count('no')`, `total_abstain == count('abstain')`, `total_dispensed == count('dispensed')` — with absent and pareo-derived rows excluded from every equation (live data puts them in no reported total). A mismatch increments a `TotalsMismatch` result counter and logs at warn; true powerlessness is reserved for zero decoded rows, which hard-fails as decode breakage. Composite PK `(votation_id, representative_id)` replaces surrogate id + UNIQUE; the per-votation write is a **delete-and-replace of that votation's vote rows in one transaction** (graft B), so upstream corrections/retractions propagate on re-sync.

**5. Party affiliations — curated files, validated hard, applied idempotently.** Verified that the API's militancia fields are universally empty. Two files: `data/parties.json` (registry: short_name/name/color, keyed by `short_name`) and `data/affiliations.json`:

```json
[{ "chamber": "camara", "external_id": "1009", "party": "UDI",
   "start": "2022-03-11", "end": null }]
```

`curation.Validate` is a pure function (dupe keys, overlapping ranges per rep, `end > start`, unknown party short_names); `ValidateAgainstDB` checks rep existence and reports deputies with votes but no covering membership (the human extends the file — single source of truth stays the file). `Apply` is a reconcile in one transaction: delete `source='curated'` rows, insert file rows — re-running converges (idempotent). `source` column keeps the door open for future auto-sourced rows. `make seed-check` runs validation only, for CI. Each sync result reports `AffiliationGaps` (graft C): deputies with votes but no party-at-date coverage — surfaced as a count for the operator to extend the file, never an ingest failure.

**6. One sync core, two drivers.** The only write path into ingested tables is `ingest.Syncer`. `cmd/sync -full` calls `Syncer.Scan(ctx, Down)`; the server's embedded scheduler and `cmd/sync -once` call `Syncer.Tick(ctx)`, which also drains the `votation_candidates` queue (`Syncer.Drain`); both additionally call `Syncer.SyncDeputies`. Divergence between full and incremental is impossible because there is nothing to diverge — same constructor, same unit of work, same upserts. Progress and cursors are in `sync_metadata`, so the drivers are interchangeable and resumable.

**7. Template loader — walk, don't glob.** `ParseGlob` can't do `**`, so `render.Load(fsys fs.FS, root string)` walks with `fs.WalkDir` (works identically on `embed.FS` in prod and `os.DirFS` in dev). Layout: one base template set parsed from `layouts/*.html` + all `partials/*.html` + shared FuncMap; each `pages/*.html` is then parsed into a `Clone` of that base, keyed by filename, and must define `{{define "content"}}` (checked at startup — fail fast, per boundary-discipline). Handlers reference pages via typed `render.Page` constants (graft C), so a template-name typo is a compile error, not a 500. Cloning gives layout inheritance without `define` collisions and lets HTMX partials be executed from the base set by name. Dev mode reloads from disk per request behind a config flag.

**8. Cohesion formula — Rice index over cast votes, computed in Go.** For party *p* on votation *v*: `cohesion(p,v) = max(yes,no,abstain) / (yes+no+abstain)`. Absent/dispensed/paired are excluded (they are not positions); `other` excluded. Party score over a period is the aggregate Rice index `Σ max(...) / Σ cast` (vote-weighted, resistant to small-sample noise), computable from one sqlc query returning `(party, votation, vote, count)` rows restricted by date range, with the arithmetic in a pure `analytics.Cohesion(rows)` function — trivially testable, no derived tables (derive, don't sync). Representative loyalty uses the same rows: fraction of votations where the rep's cast vote equals their party's modal cast vote that day; the "rebels" view is loyalty ascending. The schema needs nothing beyond `individual_votes` + date-ranged `representative_parties` — the as-of party join compares date-ONLY strings on both sides (`rp.start_date <= v.vote_date AND (rp.end_date IS NULL OR rp.end_date >= v.vote_date)`), because `votations.date` is a raw timestamp and lexicographically `'2022-03-11' >= '2022-03-11T14:22:00'` is false — deputies would silently lose party attribution on their membership end day if raw dates were joined directly (`vote_date` is the date-only prefix derived in `MapVotacion`; unparseable → NULL → renders as "sin partido" rather than misattributed). This join is the same one the MVP party-breakdown query uses. Cursors are int64 arithmetic on parsed external IDs — TEXT external_id is never compared lexicographically.

### Query layer (db/queries/*.sql, sketch)

- `votations.sql`: ListVotations (chamber filter + date DESC pagination via `sqlc.narg`), CountVotations, GetVotation, UpsertVotation (`RETURNING id`), HighWater.
- `individual_votes.sql`: InsertVote (plain INSERT — the per-votation DELETE already happened), UpsertPareoVote (`INSERT ... ON CONFLICT(votation_id, representative_id) DO UPDATE SET vote='paired', vote_raw=excluded.vote_raw` where excluded raw is the pre-override value; the API's `<Opcion>` is never 'Pareo' — pareos come from the separate collection), GetVotesByVotation (join rep + party-as-of), GetRepresentativeVotingHistory.
- `representatives.sql`: ListRepresentatives (current party via `end_date IS NULL`), GetRepresentative, UpsertRepresentative (natural-key conflict), DeactivateMissing (active flag).
- `parties.sql`, `bills.sql` (UpsertBillStub with the "upgrade placeholder title only" CASE), `sessions.sql` (UpsertSession), `sync.sql` (GetSyncValue/UpsertSyncValue), `analytics.sql` (GetPartyVoteCounts for §8).

## Synthesis decision

**Base: candidate A.** It was the only candidate whose factual foundation survived contact with the live API — its ID-scan discovery (miss = `xsi:nil`), schema field names, WSDL surface, empty-session-endpoint findings, and empty militancia fields were all re-verified live, and it won every rubric criterion vs alternatives that rested discovery on a session-detail votation list that does not exist in the API. A on its own, however, had three defects; the grafts fix them:

- **Graft C (discovery queue):** candidate C's `votation_candidates` table becomes the persistent layer under A's discovery — `INSERT OR IGNORE` candidates plus a `fetched_at` drain makes the ID scan, the boletín BFS fallback, and future Senate sources feed one resumable loop. Rejected C's core: its *primary* discovery (session-detail votation lists) is empirically false, its fallback trigger can't fire on a cold start (it needs votations to exist to detect discovery gaps), and deriving `bills.status` from a votation's `Resultado` conflates a vote outcome with bill status.
- **Graft B (delete-and-replace votes):** B's transactional per-votation vote replacement replaces A's per-row `INSERT OR REPLACE`, so upstream corrections/retractions propagate on re-sync. B's source interface and repository port were rejected as unnecessary indirection over sqlc (and its primary discovery was empirically false, as was C's).
- **Graft C (typed Page constants + AffiliationGaps):** compile-checked template names and an affiliation-gap count in the sync result, replacing string-keyed page names and log-only gap reporting.

Pareo precedence (A's open question #3) was resolved during synthesis with live evidence: **the Pareos collection overrides an explicit `No Vota` row** — votation 87461 shows all 10 pareo pairs' members also carry `No Vota` rows, so A's original explicit-wins rule would silently lose every pareo.

## Tradeoffs accepted

- We accept up to ~87k requests (~3.6 h at 150 ms) for the full historical scan in exchange for discovery with zero dependence on undocumented endpoints or scraping — and those requests are the detail fetches we'd make anyway.
- We accept occasional wasted requests on missing IDs in exchange for never silently missing a votation that exists.
- We accept provisional bill titles (a per-vote `Articulo` is not a bill title) in exchange for shipping bills browsing in the MVP without a scraping dependency.
- We accept `external_id TEXT` (with a slight storage/type-affinity smell) in exchange for one identity rule that survives Senate slugs.
- We accept storing API dates as raw TEXT (lexicographic order holds for well-formed ISO) in exchange for never failing ingest on sloppy source data like `2030-10T23:59:59`.
- We accept the `other` vote value weakening analytics purity in exchange for an ingest that can never be broken by a new `OpcionVoto` string.
- We accept dropping `legislative_periods`/`legislatures` from the MVP schema in exchange for a smaller surface; sessions are kept because they arrive free inside every detail response.
- We accept a reconcile-delete-and-reinsert seed (visible in `updated_at`-style churn) in exchange for a trivially idempotent, file-is-truth affiliation workflow.

## Alternatives considered

- **Session-driven enumeration (getLegislaturas → getSesiones → details).** Verified dead: session details carry no votation references and `getSesionBoletinXML` returned an empty body. Was the most "documented" path; eliminated by evidence.
- **Scraping a Cámara votation index page with goquery.** Viable but couples MVP correctness to HTML structure, duplicates the reservation of colly/goquery for Phase 2 Senate, and still ends with the same per-ID detail fetches. Kept as last resort only.
- **Composite natural PKs everywhere (`PRIMARY KEY (chamber_id, external_id)`).** Collision-proof without a synthetic id, but every FK and join becomes two columns, URLs leak the namespace, and sqlc row types get noisier. Internal PK + UNIQUE natural key wins on join ergonomics.
- **Prefixed string IDs (`camara:1009`) as the PK.** Simplest collision story, worst join/index ergonomics and leaks source names into every URL. Rejected.
- **templ instead of html/template.** Type-safe templates are attractive, but the stack is fixed and the loader problem (§7) is the only real pain; solved inside stdlib.

## Decisions (Phase C, user sign-off 2026-09-15)

- **Ingest scope:** full historical scan in the first version (resumable; same arithmetic as incremental either way).
- **Anchor strategy:** auto-probe + bisect to find the ID floor; freeze the discovered value as default config afterward.
- **Affiliation data:** bot-generated draft from chamber/SERVEL sources, human reviews diffs before seed-apply.
- **Rate limit:** 150 ms steady + exponential backoff on 429/5xx.

## Open questions and risks

- What miss rate does the ID space actually have far back in history? If it exceeds ~20% over a 5k window, should `DISCOVERY=boletin` become the default instead of the fallback? (First full scan will answer this; telemetry is logged either way.)
- The ID floor is unknown (ID 1 is nil). Is exponential-probe-then-bisect acceptable, or do we ship a hard-coded anchor verified once by hand?
- Is the "pareo members always also carry `No Vota` rows" pattern general across history? Ingest handles both (override pass over the separate `Pareos` collection plus a defensive `<Opcion>` text mapping); the schema takes either.
- `getVotaciones_Boletin` responses may carry fields (e.g. `Informe`) the detail endpoint lacks — worth persisting later, or YAGNI?
- Rate-limit risk: is 150 ms actually polite enough for an 87k-request scan, or should the full load run at night with backoff on 429/5xx?
- Party colors and the affiliation file: who owns initial curation, and do we want `start` dates per-legislative-period (coarse, easy) rather than exact switch dates (precise, harder)?

## Next implementation step

Write `internal/ingest/camara/{types,mapping}.go` against the verified XML shapes, with table-driven tests over captured fixtures: a detail hit (87460), an empty-Articulo hit with pareos (87461), an `xsi:nil` miss (1), `getDiputados_Vigentes`, and `getDiputados` (including a record without `Fecha_Nacimiento`, e.g. DIPID 485) — pinning every boundary contract with a live fixture before database or HTTP code exists. A pre-code probe of the ID space's depth/holes (~50 probes) recalibrates the scan model and sets expectations for `EndReason` sweeps.
