-- ============================================================================
-- SCHEMA.sql — synthesized MVP schema (goose, SQLite)
-- On disk this is a single migration: db/migrations/001_initial_schema.sql
-- Chambers are seeded inline: they are reference data required by FKs.
-- Parties are NOT seeded here — they come from data/parties.json via the
-- seed workflow (mutable curation does not belong in migrations).
-- ============================================================================

-- +goose Up

CREATE TABLE chambers (
    id   TEXT PRIMARY KEY CHECK (id IN ('camara', 'senado')),
    name TEXT NOT NULL
);

INSERT INTO chambers (id, name) VALUES
    ('camara', 'Cámara de Diputados'),
    ('senado', 'Senado');

CREATE TABLE parties (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    short_name TEXT NOT NULL UNIQUE,              -- 'RN', 'UDI', 'IND' — stable join key used by the affiliations file
    name       TEXT NOT NULL UNIQUE,
    color      TEXT NOT NULL DEFAULT '#808080'
               CHECK (length(color) = 7 AND color GLOB '#*')
);

-- Identity rule (uniform for every externally-sourced entity):
--   internal INTEGER PK used by all FKs and URLs;
--   natural key UNIQUE(chamber_id, external_id) used only at the ingest boundary.
-- external_id is TEXT so a future Senate scraper can store slugs as well as ints.
CREATE TABLE representatives (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    chamber_id       TEXT NOT NULL REFERENCES chambers(id),
    external_id      TEXT NOT NULL CHECK (external_id <> ''),
    first_name       TEXT NOT NULL,
    last_name        TEXT NOT NULL,
    second_last_name TEXT NOT NULL DEFAULT '',
    gender           TEXT,
    birth_date       TEXT,
    active           INTEGER NOT NULL DEFAULT 0,  -- 1 = currently serving. Granted ONLY by getDiputados_Vigentes
                                                  -- membership (verified: no endpoint field carries an
                                                  -- active marker); stubs default 0 so historical figures
                                                  -- are not mislabeled as serving
    UNIQUE (chamber_id, external_id)
);

-- Party membership with date ranges. Dates are OUR curated input, so they are
-- strictly checked (unlike API-sourced dates, which we store as received).
CREATE TABLE representative_parties (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    representative_id INTEGER NOT NULL REFERENCES representatives(id) ON DELETE CASCADE,
    party_id          INTEGER NOT NULL REFERENCES parties(id),
    start_date        TEXT NOT NULL CHECK (start_date GLOB '????-??-??'),
    end_date          TEXT CHECK (end_date IS NULL OR end_date GLOB '????-??-??'),
    source            TEXT NOT NULL DEFAULT 'curated'
                      CHECK (source IN ('curated', 'api', 'scraped')),
    UNIQUE (representative_id, start_date),
    CHECK (end_date IS NULL OR end_date > start_date)
);
-- Invariant "at most one party per representative at any date" cannot be a
-- CHECK; it is enforced by seed-file validation at ingest (see DESIGN.md §5).

-- Populated lazily and for free from the Sesion block embedded in every
-- getVotacion_Detalle response (verified against the live API). No extra
-- requests; no session pages in the MVP, but the linkage future-proofs
-- Phase 2 (attendance, session context) and the discovery fallback.
CREATE TABLE sessions (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    chamber_id  TEXT NOT NULL REFERENCES chambers(id),
    external_id TEXT NOT NULL CHECK (external_id <> ''),
    number      INTEGER NOT NULL,
    date        TEXT NOT NULL,
    type        TEXT,
    UNIQUE (chamber_id, external_id)
);

-- Boletín is the natural PK and is intentionally NOT chamber-scoped:
-- the same bill passes through both chambers (Phase 2 cross-chamber view
-- joins on this row). No bill-detail endpoint exists; title is provisional.
CREATE TABLE bills (
    id           TEXT PRIMARY KEY CHECK (id GLOB '[0-9]*-[0-9]*'),  -- boletín, e.g. '18036-05'
    title        TEXT NOT NULL,          -- non-empty Articulo from the most recent votation ingested
                 -- for this bill (title_date tracks it; deterministic across
                 -- ingest orders) else 'Boletín <id>'
    title_source TEXT NOT NULL DEFAULT 'votation_articulo'
                 CHECK (title_source IN ('votation_articulo', 'manual', 'enriched')),
    title_date   TEXT,        -- votation date the title came from
    status       TEXT,                    -- NULL until Phase 2 enrichment
    law_number   TEXT,
    filed_date   TEXT
);

CREATE TABLE votations (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    chamber_id       TEXT NOT NULL REFERENCES chambers(id),
    external_id      TEXT NOT NULL CHECK (external_id <> ''),
    session_id       INTEGER REFERENCES sessions(id),
    bill_id          TEXT REFERENCES bills(id),          -- NULL when Boletin is empty (e.g. votación de oficio)
    date             TEXT NOT NULL,                      -- ISO 8601 timestamp as received (raw)
    vote_date        TEXT NOT NULL CHECK (vote_date GLOB '????-??-??'),
                         -- date-only prefix derived in MapVotacion. ALL
                         -- temporal joins/ordering use vote_date: the raw
                         -- timestamp is never directly compared against
                         -- date-only membership bounds ('2022-03-11' >=
                         -- '2022-03-11T14:00:00' is false lexicographically
                         -- and would silently strip party attribution on a
                         -- membership's own end day). Unparseable raw date ->
                         -- vote_date '0000-00-00' (sorts last, join misses
                         -- party, surfaced — never misattributed)
    subject          TEXT,                               -- Articulo text (may itself be empty -> NULL)
    vote_type        TEXT,                               -- Tipo: 'General', 'Única', ...
    result           TEXT,                               -- Resultado: 'Aprobado', 'Rechazado', ...
    quorum_type      TEXT,                               -- Quorum display text
    legislative_step TEXT,                               -- Tramite: 'PRIMER TRÁMITE', ...
    -- Totals mirror the source XML exactly (TotalAfirmativos/...). Per-vote
    -- rows are the source of truth; totals are for display and cross-checks.
    total_yes        INTEGER NOT NULL DEFAULT 0 CHECK (total_yes >= 0),
    total_no         INTEGER NOT NULL DEFAULT 0 CHECK (total_no >= 0),
    total_abstain    INTEGER NOT NULL DEFAULT 0 CHECK (total_abstain >= 0),
    total_dispensed  INTEGER NOT NULL DEFAULT 0 CHECK (total_dispensed >= 0),
    UNIQUE (chamber_id, external_id)
);

-- Vote enum is closed but lossless: 'other' + vote_raw is the escape hatch
-- for values the API adds later (observed today: Afirmativo, En Contra,
-- Abstencion, No Vota, Dispensado; pareos arrive in a separate collection).
-- Composite PK: no surrogate id, no separate UNIQUE constraint needed.
CREATE TABLE individual_votes (
    votation_id       INTEGER NOT NULL REFERENCES votations(id) ON DELETE CASCADE,
    representative_id INTEGER NOT NULL REFERENCES representatives(id),
    vote     TEXT NOT NULL
             CHECK (vote IN ('yes', 'no', 'abstain', 'absent', 'dispensed', 'paired', 'other')),
    vote_raw TEXT NOT NULL,                              -- verbatim <Opcion> text (the vote element is nested
                 -- <Voto><Diputado><DIPID> + <Opcion Codigo="…">, NOT
                 -- 'OpcionVoto'). For a pareo OVERRIDE of an explicit `No
                 -- Vota` row, keeps the original 'No Vota' (the override is
                 -- visible in vote); 'Pareo' only for rows created by the
                 -- pareo pass (live shape: <Pareos><Pareo><Diputado1|2>)
    PRIMARY KEY (votation_id, representative_id)
);

-- OUT-OF-BAND discovery sources only (boletín completeness checks, manual
-- adds, future Senate sources) enqueue candidate external IDs here with
-- INSERT OR IGNORE; the drain loop fetches pending candidates and stamps
-- fetched_at. A crashed drain resumes for free. The sequential scan does NOT
-- use this table — it advances sync_metadata cursors per batch instead.
-- Drain NEVER advances high_water (a queue candidate may sit far above the
-- unswept frontier): high_water advances only from the sequential scanner.
-- Lifecycle: miss -> fetched_at stamped; error -> attempts++/last_error;
-- attempts >= 5 quarantines the row (excluded from pending, surfaced in
-- the sync result) — never an unbounded poison retry.
CREATE TABLE votation_candidates (
    chamber_id     TEXT NOT NULL REFERENCES chambers(id),
    external_id    TEXT NOT NULL CHECK (external_id <> ''),
    source         TEXT NOT NULL,           -- 'boletin_check' | 'manual' | 'senado' | …
    discovered_at  TEXT NOT NULL DEFAULT (datetime('now')),
    fetched_at     TEXT,
    attempts       INTEGER NOT NULL DEFAULT 0,
    last_error     TEXT,
    PRIMARY KEY (chamber_id, external_id)
);
CREATE INDEX idx_candidates_pending ON votation_candidates (fetched_at, attempts)
    WHERE fetched_at IS NULL;

-- Cursors and high-water marks shared by full ingest and incremental sync.
-- Keys: 'camara.high_water', 'camara.floor', 'camara.scan_cursor' (durable
--       downward-scan resume point, updated per batch inside the votation
--       transaction), 'camara.deputies.synced_at', 'camara.sync.last_run_at',
--       'camara.sync_lock' (single-writer lease: owner + expiry; the server
--       scheduler skips its tick while a CLI scan holds it — one shared
--       150ms rate limiter per lock owner)
CREATE TABLE sync_metadata (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Indexes shaped by the dominant read patterns:
--   votation list page      -> chamber + date DESC pagination
--   bill detail page        -> all votations of one boletín
--   representative profile  -> voting history join, then per-votation lookup (PK)
--   party as-of join        -> membership valid on the votation date
CREATE INDEX idx_votations_chamber_date ON votations (chamber_id, date DESC);
CREATE INDEX idx_votations_chamber_vdate ON votations (chamber_id, vote_date);
CREATE INDEX idx_votations_bill         ON votations (bill_id) WHERE bill_id IS NOT NULL;
CREATE INDEX idx_votations_session      ON votations (session_id) WHERE session_id IS NOT NULL;
CREATE INDEX idx_iv_rep_votation        ON individual_votes (representative_id, votation_id);
CREATE INDEX idx_rp_rep_dates           ON representative_parties (representative_id, start_date, end_date);
-- (chamber_id, external_id) UNIQUE indexes double as the ingest lookup indexes.

-- +goose Down
DROP TABLE IF EXISTS votation_candidates;
DROP TABLE IF EXISTS sync_metadata;
DROP TABLE IF EXISTS individual_votes;
DROP TABLE IF EXISTS votations;
DROP TABLE IF EXISTS bills;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS representative_parties;
DROP TABLE IF EXISTS representatives;
DROP TABLE IF EXISTS parties;
DROP TABLE IF EXISTS chambers;
