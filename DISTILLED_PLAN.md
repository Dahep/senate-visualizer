# Chilean Congress Vote Visualizer — Distilled Plan

## 1. Objective

Build a web application to visualize and analyze votes from the Chilean Congress (Cámara de Diputados and Senado). Users browse votations, see how individual representatives and parties voted, and explore voting patterns over time.

### Core features

- **Votation browser:** paginated list, filterable by date/result/bill, with full per-representative breakdown and party-level charts.
- **Representative profiles:** voting history, party loyalty %, most aligned/opposed peers.
- **Party profiles:** member list, cohesion score, aggregate voting stats.
- **Bill tracking:** all votations for a bill, cross-chamber comparison when applicable.
- **Analytics:** party cohesion scores, representative alignment heatmap, voting trends over time, "rebels" view (reps who vote against their party).

### Phased scope

| Phase | Scope |
| ----- | ----- |
| **MVP** | Cámara de Diputados only — votation list/detail, deputy list/profile, party list/profile, bill list/detail. |
| **Phase 2** | Add Senate data, cross-chamber comparison, cohesion scores, alignment matrix, trend charts. |
| **Phase 3** | Full-text search, alerts (email/RSS), embed widgets, public JSON API, representative comparison tool. |

---

## 2. Data Sources

### 2.1 Cámara de Diputados — ✅ Full API

**Base URL:** `https://opendata.camara.cl/wscamaradiputados.asmx`

All endpoints return **XML**, require no authentication, and are fully public.

| Endpoint | Description | Params |
| -------- | ----------- | ------ |
| `getDiputados_Vigentes` | Current deputies (ID, name, birth date, gender) | — |
| `getDiputados` | All deputies including historical | — |
| `getDiputados_Periodo` | Deputies by legislative period | `prmPeriodoID` |
| `getVotaciones_Boletin` | All votes for a given bill | `prmBoletin` (e.g. `18036-05`) |
| `getVotacion_Detalle` | **Per-deputy vote breakdown** for a specific vote | `prmVotacionID` (e.g. `87460`) |
| `getSesiones` | Sessions in a legislature | `prmLegislaturaID` |
| `getSesionDetalle` | Session details | `prmSesionID` |
| `getLegislaturas` | All legislatures | — |
| `getLegislaturaActual` | Current legislature | — |
| `getPeriodosLegislativos` | All legislative periods | — |
| `getPeriodoLegislativoActual` | Current legislative period | — |
| `getComisiones_Vigentes` | Active commissions | — |

**Key endpoint — `getVotacion_Detalle` response (simplified):**

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
    <!-- ... -->
  </Votos>
</Votacion>
```

Vote values to normalize: `Afirmativo` → yes, `En Contra` → no, `Abstencion` → abstain, `No Vota` → absent.

### 2.2 Senado — ⚠️ Partial API / Scraping Required

| Source | URL | Status |
| ------ | --- | ------ |
| Open Data Portal | `https://opendata.camara.cl` | Lists "Votaciones por Boletín - Senado" — aggregate data per bill, no per-senator breakdown |
| Senadores Vigentes | Listed on opendata portal | XML endpoint, exact URL needs discovery |
| Votaciones por Boletín | Listed on opendata portal | Aggregate only |
| Sesiones de Sala | Listed on opendata portal | Available |
| Senate voting page | `https://www.senado.cl/actividad-legislativa/sala/votaciones` | Per-senator detail visible in HTML |
| Tramitación endpoints | `https://tramitacion.senado.cl/wspublico/` | PHP-based, returns HTML |

**Problem:** No clean per-senator vote detail API equivalent to `getVotacion_Detalle`.

**Mitigation path (in order):**

1. Inspect XHR/fetch requests on the Senate voting page to find hidden JSON/XML APIs.
2. Check the "Datos Abiertos Legislativos" section for downloadable datasets.
3. If nothing found, build a scraper for `senado.cl/actividad-legislativa/sala/votaciones`.
4. Start the MVP with Cámara only; add Senate in Phase 2.

### 2.3 Party Affiliation — Manual Curation

Neither chamber's API returns party affiliation in vote details. Required approach:

- Maintain a `representative → party → date_range` mapping table.
- Source from chamber websites or SERVEL (electoral service).
- Track date ranges to handle representatives who switch parties.

### 2.4 Ingestion Strategy

| Step | Detail |
| ---- | ------ |
| **Initial load** | Fetch all periods → legislatures → sessions → votations → vote details → deputies. Rate-limit to 100–200 ms between requests. Full historical load may take several hours. |
| **Incremental sync** | Cron job every ~6 hours. Check for new sessions/votations since last sync timestamp. Fetch only new data. |

---

## 3. Risks

| Risk | Mitigation |
| ---- | ---------- |
| Senate has no per-senator vote API | Scrape website; start with Cámara only |
| API rate limits or downtime | Cache aggressively; store all data locally in SQLite |
| Party affiliation unavailable via API | Manually curate; source from SERVEL |
| Representatives change parties | Track membership with date ranges |
| Large historical data volume | Paginate ingestion; start with current period |
| API XML format changes | Defensive parsing; version detection; monitoring |

---

## 4. Stack Constraints

The chosen technology stack must satisfy all of the following:

| # | Constraint | Rationale |
| - | ---------- | --------- |
| 1 | **SQLite as the database** | Zero-config, single-file, portable; data volume is well within SQLite limits. |
| 2 | **Type-safe language preferred** | Compile-time guarantees reduce runtime bugs across data ingestion, queries, and rendering. |
| 3 | **Minimize hand-written JavaScript** | Server-rendered UI with minimal or zero authored JS. JS libraries used through wrappers in the primary language (e.g. Chartkick in Ruby, Chart.js via HTMX data attributes) are acceptable — the goal is to avoid authoring JS, not to ban JS execution in the browser. |
| 4 | **SQL queries in `.sql` files** | Queries live in plain `.sql` files, not embedded in application code strings. A tool generates typed code from them. |
| 5 | **SQL schema defined in code or `.sql` files** | Schema managed through migration files (ideally plain `.sql`), not opaque ORM DSLs. |
| 6 | **Schema/query errors caught at compile/build time** | A codegen or macro step validates SQL against the schema before the application runs. Runtime-only SQL validation is not acceptable. |
| 7 | **Fluent, interactive charts from data analysis** | Rich charting for party breakdowns, voting trends, alignment heatmaps, and cohesion scores. Server-side SVG, WASM-based charts, or JS charting libraries wrapped by the primary language (e.g. Chartkick, Chart.js via CDN) are all acceptable as long as no JS is authored by hand. |
| 8 | **Mature web scraping libraries available** | Senate data requires scraping HTML pages (no clean API). The stack must have production-quality HTTP client + HTML parsing libraries (e.g. colly/goquery, Nokogiri, Floki, scraper+reqwest). |
