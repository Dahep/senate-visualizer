# Chilean Congress Vote Visualizer - Project Plan

## 1. Project Overview

A web application to analyze and visualize votes from the Chilean Congress (both the **Senado** and the **Cámara de Diputados**). Users can browse each votation, see how individual representatives and parties voted, filter by representative or party, and explore voting history over time.

---

## 2. API Research & Viability

### 2.1 Cámara de Diputados (Chamber of Deputies) — ✅ EXCELLENT API

**Base URL:** `https://opendata.camara.cl/wscamaradiputados.asmx`

All endpoints return **XML**. No authentication required. Fully public.

| Endpoint | Description | Parameters |
|---|---|---|
| `getDiputados_Vigentes` | Current deputies (ID, name, birth date, gender) | None |
| `getDiputados` | All deputies (including historical) | None |
| `getDiputados_Periodo` | Deputies by legislative period | `prmPeriodoID` |
| `getVotaciones_Boletin` | All votes for a given bill | `prmBoletin` (e.g. `18036-05`) |
| `getVotacion_Detalle` | **Per-deputy vote breakdown** for a specific vote | `prmVotacionID` (e.g. `87460`) |
| `getSesiones` | List of sessions | `prmLegislaturaID` |
| `getSesionDetalle` | Session details | `prmSesionID` |
| `getLegislaturas` | All legislatures | None |
| `getLegislaturaActual` | Current legislature | None |
| `getPeriodosLegislativos` | All legislative periods | None |
| `getPeriodoLegislativoActual` | Current legislative period | None |
| `getComisiones_Vigentes` | Active commissions | None |

**Key finding:** The `getVotacion_Detalle` endpoint returns exactly what we need — for each vote it lists every deputy with their individual vote:
- `Afirmativo` (Yes)
- `En Contra` (No)
- `Abstencion` (Abstain)
- `No Vota` (Did not vote)

### 2.2 Senado (Senate) — ⚠️ PARTIAL API

**Open Data Portal:** `https://opendata.camara.cl` (shared portal for both chambers)

The portal lists "Votaciones por Boletín - Senado" as available, but the Senate's web services are more limited:

| Source | URL | Status |
|---|---|---|
| Senadores Vigentes | Listed on opendata portal | XML endpoint (need to discover exact URL) |
| Votaciones por Boletín | Listed on opendata portal | Returns aggregate data per bill |
| Sesiones de Sala | Listed on opendata portal | Available |
| Diario de Sesión | Listed on opendata portal | Available |
| Comisiones Vigentes | Listed on opendata portal | Available |
| Senate voting page | `https://www.senado.cl/actividad-legislativa/sala/votaciones` | Web page with vote details |
| Tramitación | `https://tramitacion.senado.cl/wspublico/` | PHP-based endpoints, returns HTML |

**Key challenge:** The Senate does NOT appear to have a clean per-senator vote detail API equivalent to `getVotacion_Detalle`. The vote details are available on the Senate website but may need to be **scraped** or accessed through undocumented endpoints.

**Mitigation strategies:**
1. Start with Cámara de Diputados (fully supported)
2. Investigate the Senate website's XHR/fetch requests to find hidden JSON/XML APIs
3. If no API exists, build a scraper for the Senate voting pages
4. Check if the "Datos Abiertos Legislativos" section has downloadable datasets

### 2.3 Missing Data: Party Affiliation

Neither API directly returns party affiliation in the vote detail. We'll need to:
1. Maintain a mapping table of `representative_id -> party -> date_range`
2. Source this from the Senate/Cámara websites or from SERVEL (electoral service)
3. Account for party changes over time (representatives sometimes switch parties)

---

## 3. Architecture

### 3.1 Tech Stack

| Layer | Technology | Rationale |
|---|---|---|
| **Backend** | Go (Golang) | Fast, simple concurrency for API fetching, single binary deployment |
| **HTTP Router** | `net/http` + `chi` or `echo` | Lightweight, idiomatic Go |
| **Database** | SQLite (via `modernc.org/sqlite`) | Zero-config, portable, perfect for this data volume |
| **Data Ingestion** | Go workers / cron jobs | Periodically fetch & sync from congressional APIs |
| **Frontend** | HTML + HTMX + Tailwind CSS | Server-rendered, interactive without SPA complexity |
| **Charting** | Chart.js or D3.js (via CDN) | Voting visualizations |
| **XML Parsing** | `encoding/xml` (Go stdlib) | Parse API responses |
| **Deployment** | Single binary + SQLite file | Can run on any VPS, Fly.io, Railway, etc. |

### 3.2 System Diagram

```
┌──────────────────────────────────────────────────────┐
│                   Chilean Congress APIs                │
│  ┌─────────────────────┐  ┌────────────────────────┐  │
│  │ opendata.camara.cl  │  │  senado.cl / scraper   │  │
│  │ (XML Web Services)  │  │  (XML/HTML)            │  │
│  └─────────┬───────────┘  └──────────┬─────────────┘  │
└────────────┼─────────────────────────┼────────────────┘
             │                         │
             ▼                         ▼
┌──────────────────────────────────────────────────────┐
│              Go Backend (Data Ingestion)              │
│  ┌──────────────┐  ┌──────────────┐  ┌────────────┐  │
│  │ API Fetcher  │  │  XML Parser  │  │  Scraper   │  │
│  │ (Cámara)     │  │              │  │  (Senado)  │  │
│  └──────┬───────┘  └──────┬───────┘  └─────┬──────┘  │
│         └─────────────────┼────────────────┘          │
│                           ▼                           │
│                  ┌────────────────┐                    │
│                  │    SQLite DB   │                    │
│                  └────────┬───────┘                    │
│                           │                           │
│              ┌────────────┼──────────────┐             │
│              ▼            ▼              ▼             │
│  ┌────────────────┐ ┌──────────┐ ┌──────────────┐     │
│  │  API Handlers  │ │  Views   │ │  HTMX        │     │
│  │  (JSON)        │ │  (HTML)  │ │  Partials    │     │
│  └────────────────┘ └──────────┘ └──────────────┘     │
└──────────────────────────────────────────────────────┘
             │
             ▼
┌──────────────────────────────────────────────────────┐
│                    Browser (Frontend)                  │
│  ┌───────────┐  ┌───────────┐  ┌──────────────────┐  │
│  │  HTMX     │  │ Tailwind  │  │ Chart.js / D3.js │  │
│  └───────────┘  └───────────┘  └──────────────────┘  │
└──────────────────────────────────────────────────────┘
```

---

## 4. Database Schema

```sql
-- Which chamber a representative belongs to
-- 'senado' or 'camara'

CREATE TABLE chambers (
    id TEXT PRIMARY KEY,  -- 'senado', 'camara'
    name TEXT NOT NULL
);

CREATE TABLE parties (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    short_name TEXT,
    color TEXT  -- hex color for visualizations
);

CREATE TABLE representatives (
    id INTEGER PRIMARY KEY,  -- use the API's ID
    external_id INTEGER,     -- ID from the source API
    chamber_id TEXT NOT NULL REFERENCES chambers(id),
    first_name TEXT NOT NULL,
    last_name TEXT NOT NULL,
    second_last_name TEXT,
    gender TEXT,
    birth_date TEXT,
    photo_url TEXT,
    district TEXT,
    region TEXT
);

-- Track party membership over time (reps can switch parties)
CREATE TABLE representative_parties (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    representative_id INTEGER NOT NULL REFERENCES representatives(id),
    party_id INTEGER NOT NULL REFERENCES parties(id),
    start_date TEXT NOT NULL,
    end_date TEXT,  -- NULL means current
    UNIQUE(representative_id, party_id, start_date)
);

CREATE TABLE legislative_periods (
    id INTEGER PRIMARY KEY,
    external_id INTEGER,
    start_date TEXT,
    end_date TEXT
);

CREATE TABLE legislatures (
    id INTEGER PRIMARY KEY,
    external_id INTEGER,
    legislative_period_id INTEGER REFERENCES legislative_periods(id),
    start_date TEXT,
    end_date TEXT
);

CREATE TABLE sessions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    external_id INTEGER,
    chamber_id TEXT NOT NULL REFERENCES chambers(id),
    legislature_id INTEGER REFERENCES legislatures(id),
    session_number INTEGER,
    session_type TEXT,  -- 'Ordinaria', 'Extraordinaria', 'Especial'
    date TEXT NOT NULL
);

CREATE TABLE bills (
    id TEXT PRIMARY KEY,  -- boletín number e.g. '18036-05'
    title TEXT NOT NULL,
    description TEXT,
    law_number TEXT,      -- if it became law
    status TEXT,
    filed_date TEXT
);

CREATE TABLE votations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    external_id INTEGER,
    chamber_id TEXT NOT NULL REFERENCES chambers(id),
    session_id INTEGER REFERENCES sessions(id),
    bill_id TEXT REFERENCES bills(id),
    date TEXT NOT NULL,
    subject TEXT,          -- what was being voted on
    vote_type TEXT,        -- 'General', 'Particular', 'Única'
    result TEXT,           -- 'Aprobado', 'Rechazado'
    quorum_type TEXT,      -- 'Quorum Simple', 'Quorum Calificado', etc.
    legislative_step TEXT, -- 'PRIMER TRÁMITE', 'SEGUNDO TRÁMITE', etc.
    total_yes INTEGER DEFAULT 0,
    total_no INTEGER DEFAULT 0,
    total_abstain INTEGER DEFAULT 0,
    total_absent INTEGER DEFAULT 0
);

CREATE TABLE individual_votes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    votation_id INTEGER NOT NULL REFERENCES votations(id),
    representative_id INTEGER NOT NULL REFERENCES representatives(id),
    vote TEXT NOT NULL,    -- 'yes', 'no', 'abstain', 'absent'
    UNIQUE(votation_id, representative_id)
);

-- Indexes for common queries
CREATE INDEX idx_individual_votes_votation ON individual_votes(votation_id);
CREATE INDEX idx_individual_votes_representative ON individual_votes(representative_id);
CREATE INDEX idx_individual_votes_vote ON individual_votes(vote);
CREATE INDEX idx_votations_chamber ON votations(chamber_id);
CREATE INDEX idx_votations_date ON votations(date);
CREATE INDEX idx_votations_bill ON votations(bill_id);
CREATE INDEX idx_representatives_chamber ON representatives(chamber_id);
CREATE INDEX idx_representative_parties_rep ON representative_parties(representative_id);
```

---

## 5. Features & Pages

### 5.1 MVP (Phase 1) — Cámara de Diputados Only

| Page | URL | Description |
|---|---|---|
| **Home / Dashboard** | `/` | Recent votations, summary stats, quick links |
| **Votations List** | `/votations` | Paginated list of all votations, filterable by date, result, bill |
| **Votation Detail** | `/votations/:id` | Full breakdown: how each deputy voted, party breakdown chart, vote counts |
| **Deputies List** | `/representatives` | All current deputies, filterable by party, region |
| **Deputy Profile** | `/representatives/:id` | Voting history, party loyalty %, most aligned/opposed peers |
| **Parties List** | `/parties` | All parties, member counts, aggregate voting stats |
| **Party Profile** | `/parties/:id` | Party voting history, cohesion %, member list |
| **Bills List** | `/bills` | All bills with their votation history |
| **Bill Detail** | `/bills/:id` | All votations for a bill, who voted how |

### 5.2 Phase 2 — Senate + Analytics

- Add Senate data (scraper or API discovery)
- **Cross-chamber comparison** for bills that pass both chambers
- **Party cohesion scores**: How often do party members vote the same?
- **Representative alignment matrix**: Heatmap of how often two reps vote the same
- **Voting trends**: Time series of how parties shift on issues
- **"Rebels" view**: Reps who vote against their party most often

### 5.3 Phase 3 — Advanced

- Full-text search across bill descriptions
- Email/RSS alerts for new votations
- Embed widgets for journalists
- Public JSON API for other developers
- Comparison tool: "Compare two representatives"

---

## 6. Project Structure

```
senate-visualizer/
├── cmd/
│   └── server/
│       └── main.go              # Entry point
├── internal/
│   ├── config/
│   │   └── config.go            # App configuration
│   ├── database/
│   │   ├── database.go          # DB connection & migrations
│   │   ├── migrations/          # SQL migration files
│   │   └── queries/             # SQL query files
│   ├── models/
│   │   ├── representative.go
│   │   ├── party.go
│   │   ├── votation.go
│   │   ├── vote.go
│   │   ├── bill.go
│   │   └── session.go
│   ├── ingestion/
│   │   ├── camara/
│   │   │   ├── client.go        # HTTP client for Cámara API
│   │   │   ├── parser.go        # XML response parsing
│   │   │   ├── types.go         # XML struct types
│   │   │   └── sync.go          # Sync logic (fetch & store)
│   │   └── senado/
│   │       ├── client.go        # Senate data fetcher
│   │       ├── scraper.go       # HTML scraping if needed
│   │       └── sync.go
│   ├── handlers/
│   │   ├── home.go
│   │   ├── votations.go
│   │   ├── representatives.go
│   │   ├── parties.go
│   │   └── bills.go
│   ├── services/
│   │   ├── votation_service.go  # Business logic
│   │   ├── stats_service.go     # Computed statistics
│   │   └── sync_service.go      # Orchestrates data sync
│   └── views/
│       ├── layouts/
│       │   └── base.html
│       ├── pages/
│       │   ├── home.html
│       │   ├── votations_list.html
│       │   ├── votation_detail.html
│       │   ├── representatives_list.html
│       │   ├── representative_detail.html
│       │   ├── parties_list.html
│       │   ├── party_detail.html
│       │   ├── bills_list.html
│       │   └── bill_detail.html
│       └── partials/            # HTMX partial templates
│           ├── vote_breakdown.html
│           ├── party_chart.html
│           └── vote_table.html
├── static/
│   ├── css/
│   ├── js/
│   └── images/
├── data/
│   ├── parties.json             # Manual party data with colors
│   └── congress.db              # SQLite database (gitignored)
├── scripts/
│   └── seed.go                  # Initial data population script
├── go.mod
├── go.sum
├── Makefile
├── Dockerfile
├── .env.example
├── .gitignore
├── README.md
└── PROJECT_PLAN.md              # This file
```

---

## 7. Data Ingestion Strategy

### 7.1 Initial Load

1. Fetch all legislative periods and legislatures
2. Fetch all sessions per legislature
3. For each session, fetch all votations (via `getVotaciones_Boletin`)
4. For each votation, fetch individual vote detail (via `getVotacion_Detalle`)
5. Fetch all deputies (current + historical)
6. Build party mapping from external data

**Rate limiting:** Be respectful — add 100-200ms delays between requests. The full initial load could take several hours for historical data.

### 7.2 Incremental Sync

- Run a cron job (e.g., every 6 hours)
- Check for new sessions/votations since last sync
- Only fetch new data
- Store last sync timestamp in DB

### 7.3 Data Normalization

Map API vote values to our internal format:
```
"Afirmativo"  → "yes"
"En Contra"   → "no"
"Abstencion"  → "abstain"
"No Vota"     → "absent"
```

---

## 8. Key Queries & Computations

### Party breakdown for a votation
```sql
SELECT p.name, p.color, iv.vote, COUNT(*) as count
FROM individual_votes iv
JOIN representatives r ON iv.representative_id = r.id
JOIN representative_parties rp ON r.id = rp.representative_id
    AND rp.start_date <= v.date
    AND (rp.end_date IS NULL OR rp.end_date >= v.date)
JOIN parties p ON rp.party_id = p.id
JOIN votations v ON iv.votation_id = v.id
WHERE iv.votation_id = ?
GROUP BY p.name, iv.vote
ORDER BY p.name, iv.vote;
```

### Representative voting history
```sql
SELECT v.date, v.subject, b.title as bill_title, iv.vote, v.result
FROM individual_votes iv
JOIN votations v ON iv.votation_id = v.id
LEFT JOIN bills b ON v.bill_id = b.id
WHERE iv.representative_id = ?
ORDER BY v.date DESC
LIMIT ? OFFSET ?;
```

### Party cohesion score
```sql
-- For each votation, check if all party members voted the same
-- Cohesion = (max_same_vote_count / total_party_votes) averaged across votations
```

### Representative alignment
```sql
-- For each pair of representatives, count how often they vote the same
-- across all shared votations
```

---

## 9. Implementation Phases & Timeline

### Phase 1: Foundation (Week 1-2)
- [ ] Initialize Go project with dependencies
- [ ] Set up SQLite database with migrations
- [ ] Build Cámara de Diputados API client + XML parser
- [ ] Implement initial data ingestion for deputies + votations
- [ ] Basic representative and votation models

### Phase 2: Core Web App (Week 3-4)
- [ ] Set up HTTP server with router
- [ ] Build HTML templates with Tailwind CSS
- [ ] Implement Votation List page (with pagination)
- [ ] Implement Votation Detail page (with vote breakdown)
- [ ] Implement Representatives List page
- [ ] Implement Representative Profile page

### Phase 3: Analytics & Parties (Week 5-6)
- [ ] Build party data mapping
- [ ] Implement party-level voting aggregation
- [ ] Add party breakdown charts to votation detail (Chart.js)
- [ ] Implement Party List and Party Profile pages
- [ ] Add party cohesion scores

### Phase 4: Bills & Search (Week 7)
- [ ] Implement Bill List and Bill Detail pages
- [ ] Add search/filter functionality (HTMX)
- [ ] Add incremental sync (cron)
- [ ] Dashboard with summary stats

### Phase 5: Senate Integration (Week 8-9)
- [ ] Investigate Senate API/scraping
- [ ] Build Senate data ingestion
- [ ] Extend UI to support both chambers
- [ ] Cross-chamber bill tracking

### Phase 6: Polish & Deploy (Week 10)
- [ ] Mobile responsive design
- [ ] Performance optimization
- [ ] Dockerfile + deployment
- [ ] Documentation

---

## 10. Risks & Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Senate has no detailed vote API | Can't show per-senator votes | Scrape Senate website; start with Cámara only |
| API rate limits or downtime | Slow ingestion, stale data | Cache aggressively; store all data locally |
| Party affiliation data unavailable via API | Can't show party breakdown | Manually curate party data; source from SERVEL |
| Representatives change parties | Incorrect party attribution | Track party membership with date ranges |
| Large data volume for historical data | Slow initial load | Paginate ingestion; start with current period only |
| API XML format changes | Parser breaks | Defensive parsing; version detection; alerts |

---

## 11. Chilean Political Parties Reference

For the party color mapping, here are the main current parties:

| Party | Abbreviation | Coalition | Suggested Color |
|---|---|---|---|
| Renovación Nacional | RN | Chile Vamos | `#1E3A5F` (dark blue) |
| Unión Demócrata Independiente | UDI | Chile Vamos | `#0057A0` (blue) |
| Evópoli | EVO | Chile Vamos | `#00B4D8` (light blue) |
| Partido Republicano | REP | Republicanos | `#FF6B00` (orange) |
| Partido Socialista | PS | Socialismo Democrático | `#E60026` (red) |
| Partido Por la Democracia | PPD | Socialismo Democrático | `#FFD700` (gold) |
| Democracia Cristiana | DC | Independent / center | `#008C45` (green) |
| Revolución Democrática | RD | Apruebo Dignidad | `#7B2D8E` (purple) |
| Convergencia Social | CS | Apruebo Dignidad | `#D81B60` (magenta) |
| Partido Comunista | PC | Apruebo Dignidad | `#CC0000` (dark red) |
| Partido Liberal | PL | Socialismo Democrático | `#FFA500` (orange) |
| Independientes | IND | Various | `#808080` (gray) |

---

## 12. API Examples (for development reference)

### Get current deputies
```
GET https://opendata.camara.cl/wscamaradiputados.asmx/getDiputados_Vigentes
```

### Get votes for a bill
```
GET https://opendata.camara.cl/wscamaradiputados.asmx/getVotaciones_Boletin?prmBoletin=18036-05
```

### Get individual vote breakdown
```
GET https://opendata.camara.cl/wscamaradiputados.asmx/getVotacion_Detalle?prmVotacionID=87460
```

### Response format (Vote Detail - simplified)
```xml
<Votacion>
  <VotacionId>87460</VotacionId>
  <Fecha>2026-01-21T13:19:54</Fecha>
  <Tipo>Única</Tipo>
  <Resultado>Aprobado</Resultado>
  <Quorum>Quorum Calificado</Quorum>
  <Boletin>18036-05</Boletin>
  <Descripcion>Enmienda del Senado que incorpora...</Descripcion>
  <TotalSi>80</TotalSi>
  <TotalNo>45</TotalNo>
  <TotalAbstencion>2</TotalAbstencion>
  <TotalDispensado>0</TotalDispensado>
  <Votos>
    <Voto>
      <DiputadoId>1009</DiputadoId>
      <Nombre>Jorge</Nombre>
      <ApellidoPaterno>Alessandri</ApellidoPaterno>
      <ApellidoMaterno>Vergara</ApellidoMaterno>
      <OpcionVoto>Afirmativo</OpcionVoto>
    </Voto>
    <!-- ... more votes ... -->
  </Votos>
</Votacion>
```

---

## 13. Getting Started

```bash
# 1. Initialize the project
mkdir -p senate-visualizer && cd senate-visualizer
go mod init github.com/yourusername/senate-visualizer

# 2. Install dependencies
go get github.com/go-chi/chi/v5
go get modernc.org/sqlite
go get github.com/a-h/templ  # or use html/template

# 3. Create the database
make migrate

# 4. Run initial data ingestion
make ingest

# 5. Start the server
make run
# → http://localhost:8080
```

---

## 14. Conclusion

**This project is very viable.** The Cámara de Diputados has an excellent, well-structured public XML API that provides everything we need: representative data, votation listings, and per-deputy vote breakdowns. The Senate is more challenging but workable.

**Recommended approach:** Start with the Cámara de Diputados to build the full application, then add Senate support. This gives us a working, valuable product quickly while we figure out the Senate data access story.

The Go + SQLite + HTMX stack is ideal for this: low operational complexity, fast development, easy deployment, and more than enough performance for the data volumes involved (hundreds of votations per year, ~155 deputies, ~50 senators).