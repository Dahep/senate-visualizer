# Tech Stack Alternatives — Chilean Congress Visualizer

A deep-dive analysis of alternative stacks for this project, with a focus on
**minimizing or eliminating JavaScript** while still delivering a fluid,
interactive UI with beautiful charts and animations.

---

## Table of Contents

1. [The "No JavaScript" Problem in Data Visualization](#1-the-no-javascript-problem)
2. [Current Stack Summary](#2-current-stack-summary)
3. [Alternative: Elixir + Phoenix LiveView](#3-elixir--phoenix-liveview)
4. [Alternative: Gleam + Lustre + Wisp](#4-gleam--lustre--wisp)
5. [Alternative: Ruby on Rails + Hotwire](#5-ruby-on-rails--hotwire)
6. [Alternative: Rust + Leptos (Full-Stack WASM)](#6-rust--leptos-full-stack-wasm)
7. [Cross-Cutting Concern: Database Layer](#7-cross-cutting-concern-database-layer)
8. [Cross-Cutting Concern: Charts Without JS](#8-cross-cutting-concern-charts-without-js)
9. [Comparison Matrix](#9-comparison-matrix)
10. [Recommendations](#10-recommendations)

---

## 1. The "No JavaScript" Problem

"No JavaScript" is a spectrum. It helps to define what we actually mean:

| Level | Description | Example |
|---|---|---|
| **0 — Zero JS** | No JS runtime at all in the browser | Pure HTML + CSS server render |
| **1 — Invisible JS** | A tiny runtime you never write or see | Phoenix LiveView, HTMX |
| **2 — Compiled-to-JS/WASM** | You write another language; a compiler produces the runtime | Gleam/Lustre → JS, Leptos → WASM |
| **3 — Minimal JS** | A tiny lib + a few lines of glue code | HTMX + 1 hook, Rails + Stimulus |
| **4 — JS-heavy** | SPA, React, Vue, Svelte, etc. | (current Chart.js dependency) |

The goal for this project is **Level 1 or 2**: interactive pages, smooth
animations, and rich charts — all authored in a non-JS language.

### Charting is the Hard Part

Almost every popular charting library (Chart.js, D3, ApexCharts, ECharts) is
JavaScript. To avoid JS charts, we have two real options:

1. **Server-rendered SVG** — the server emits `<svg>` markup directly in the
   HTML; CSS handles animations via `@keyframes`, `stroke-dasharray`, etc.
2. **WASM charts** — Rust/WASM crates render charts client-side without
   writing a single line of JS.

Both approaches are mature enough in 2024–2025 for production use.

---

## 2. Current Stack Summary

```
Backend:   Go (net/http + chi/echo)
DB:        SQLite (modernc.org/sqlite)
Templating: html/template (server-rendered)
Frontend:  HTMX + Tailwind CSS
Charts:    Chart.js or D3.js  ← the JS dependency
Ingest:    Go workers / cron
Deploy:    Single binary + SQLite file
```

**Strengths:** simple, fast, single binary, great concurrency for API fetching.  
**JS footprint:** HTMX is ~14 KB (Level 1), but Chart.js adds ~200 KB of JS.
The project is solid; the main JS concern is charting.

---

## 3. Elixir + Phoenix LiveView

> **"The closest thing to no-JS interactive web apps available today."**

### What It Is

Phoenix LiveView establishes a persistent WebSocket between client and server.
UI updates are computed on the server and sent as tiny diffs to the browser.
You write **zero JavaScript** for most interactions — the LiveView JS client
(~30 KB) handles everything transparently.

### Stack

```
Language:    Elixir (functional, on the BEAM/OTP runtime)
Framework:   Phoenix + Phoenix LiveView
DB:          Ecto + SQLite (via ecto_sqlite3) or PostgreSQL
Ingest:      Oban (robust background jobs on BEAM)
Frontend:    LiveView + Tailwind CSS (no JS authored by you)
Charts:      Plox (server-side SVG) — see §9
Deploy:      Elixir release (~30 MB) on Fly.io, Railway, Render
```

### Project Structure

```
lib/
├── congress/               # Core domain (Ecto schemas, queries)
│   ├── deputies.ex
│   ├── votations.ex
│   └── parties.ex
├── congress_web/
│   ├── live/               # LiveView modules (replace handlers + templates)
│   │   ├── votation_live/
│   │   │   ├── index.ex    # List of votations
│   │   │   └── show.ex     # Votation detail with live chart
│   │   ├── representative_live/
│   │   └── party_live/
│   ├── components/         # Reusable HEEx components (like partials)
│   │   ├── vote_chart.ex   # SVG pie/bar chart component
│   │   └── vote_table.ex
│   └── router.ex
├── congress/ingestion/
│   ├── camara_worker.ex    # Oban worker for Cámara API
│   └── senado_worker.ex
└── mix.exs
```

### Charts: Plox (Server-Side SVG)

[Plox](https://github.com/gridpoint-com/plox) is a Phoenix/LiveView library
that renders SVG charts entirely on the server. No JS. Charts update in real
time via LiveView diffs.

```elixir
# In a LiveView template (HEEx)
<Plox.Graph.new(
  title: "Votación #{@votation.id}",
  data: @party_breakdown,
  type: :bar
) />
```

CSS animations on SVG elements (e.g., bar-height transitions) work perfectly
since the browser sees standard `<svg>` markup with CSS classes.

### Interactivity Example

```elixir
defmodule CongressWeb.VotationLive.Show do
  use CongressWeb, :live_view

  def handle_event("filter_by_party", %{"party" => party_id}, socket) do
    votes = Congress.Votations.filter_votes(socket.assigns.votation_id, party_id)
    {:noreply, assign(socket, votes: votes)}
  end
end
```

Clicking a filter button → WebSocket message → server recomputes → diff sent
to browser → DOM updates. **No JavaScript authored.**

### Pros

- ✅ Essentially zero JS written by you
- ✅ Real-time updates via WebSocket out of the box
- ✅ `Plox` for server-side SVG charts (CSS animations included)
- ✅ Oban is the gold standard for background jobs (perfect for API ingestion)
- ✅ Excellent deployment story: Fly.io is practically built for Elixir
- ✅ BEAM fault tolerance: API ingestion workers restart on failure
- ✅ Pattern-matched, functional code reads very clearly for data pipelines
- ✅ LiveView JS hooks available for the rare case you *do* need JS

### Cons

- ❌ Requires Erlang + Elixir runtime (not a single binary like Go)
- ❌ Higher memory baseline than Go (~50–100 MB vs ~10 MB)
- ❌ Learning curve: BEAM, OTP, functional patterns
- ❌ Plox is newer and less featureful than Chart.js/D3

### JS Score: ⭐⭐⭐⭐⭐ (Level 1 — invisible runtime only)

---

## 4. Gleam + Lustre + Wisp

> **"Type-safe BEAM language — and yes, it can use the entire Elixir/Erlang ecosystem for charts."**

### What It Is

Gleam is a statically-typed functional language that compiles to **Erlang** (for
server) or **JavaScript** (for browser). Lustre is Gleam's primary web
framework, inspired by Elm's Model-View-Update (MVU) architecture. When targeting
the browser, Lustre *compiles your Gleam code to JavaScript* — so it is "no JS"
in the sense that **you** never write JS, but there is a compiled JS runtime
present.

[Wisp](https://github.com/gleam-wisp/wisp) is a lightweight HTTP server for
Gleam on the BEAM.

### Stack

```
Language:    Gleam (statically typed, compiles to Erlang or JS)
Server:      Wisp (HTTP) + Mist (TCP)
Frontend:    Lustre (compiles Gleam → JS, MVU architecture)
SSR:         Lustre supports server-side rendering + hydration
DB:          gleam_sqlight (SQLite) or Pog (PostgreSQL)
Charts:      Contex (Elixir lib) via FFI bridge  ← researched & viable
Deploy:      Erlang release or Docker
```

---

### 4.1 Gleam ↔ BEAM Interop: The FFI System

This is the critical finding that changes Gleam's viability for this project.

**Gleam can call any Erlang or Elixir function** using the `@external`
attribute. There is no performance cost — it is a direct BEAM function call.

```gleam
// Calling a standard Erlang function:
@external(erlang, "lists", "reverse")
pub fn reverse(list: List(a)) -> List(a)

// Calling an Elixir function (note the "Elixir." prefix):
@external(erlang, "Elixir.CSV", "encode")
fn csv_encode(data: List(List(String))) -> ElixirEnumerable
```

Elixir modules, once compiled, appear on the BEAM under the atom
`'Elixir.ModuleName'` — so `@external(erlang, "Elixir.CSV", "encode")`
is all that is needed.

**Adding hex packages:**
Both Erlang and Elixir packages from [hex.pm](https://hex.pm) can be added
directly to a Gleam project:

```toml
# gleam.toml
[dependencies]
gleam_stdlib = ">= 0.60.0 and < 2.0.0"
wisp        = ">= 1.0.0 and < 2.0.0"
contex      = ">= 0.5.0 and < 1.0.0"   # ← Elixir lib, works fine
```

```sh
gleam add contex   # downloads from hex.pm, compiles with mix
```

**Build tool nuance:** Gleam's build tool uses `rebar3` by default but will
prefer `mix` if it is installed on the system. Since `contex` is a `mix`-only
package, you need Elixir installed alongside Gleam. This is a real additional
dependency, but on any machine where you already want BEAM, installing Elixir
is trivial (`asdf install elixir ...`).

---

### 4.2 Using Contex from Gleam

[Contex](https://hex.pm/packages/contex) is a pure Elixir server-side SVG
charting library (675 stars, 927K all-time downloads). It outputs SVG strings
that can be embedded directly in HTML — zero JS.

**The challenge:** Contex's API is struct-heavy (`%Contex.Dataset{}`,
`%Contex.Plot{}`, etc.). Elixir structs are just maps at the BEAM level.
Gleam cannot inspect them natively, so each intermediate type must be treated
as an opaque type. This makes a deep wrapper messy.

**The recommended strategy: a thin Elixir bridge module.**

The cleanest pattern for wrapping a struct-heavy Elixir library from Gleam is
to write a single `.ex` file inside the Gleam project that hides all the
Elixir struct complexity and exposes only primitive-typed functions (strings,
lists, numbers). Gleam then calls only that one function.

```
src/
├── chart_bridge.ex          # ← Elixir file inside the Gleam project
└── congress/
    ├── charts.gleam          # ← Gleam wrapper
    └── ...
```

```elixir
# src/chart_bridge.ex  — thin Elixir bridge, lives inside the Gleam project
defmodule ChartBridge do
  @doc """
  Takes a list of {label, value} tuples and returns an SVG string.
  All types are primitives so Gleam can call this cleanly.
  """
  def bar_chart(rows, width \\ 600, height \\ 400) do
    rows
    |> Contex.Dataset.new(["Party", "Votes"])
    |> Contex.Plot.new(Contex.BarChart, width, height)
    |> Contex.Plot.to_svg()
    |> Phoenix.HTML.safe_to_string()   # or IO.iodata_to_binary/1
  end

  def pie_chart(rows, width \\ 400, height \\ 400) do
    rows
    |> Contex.Dataset.new(["Party", "Votes"])
    |> Contex.Plot.new(Contex.PieChart, width, height)
    |> Contex.Plot.to_svg()
    |> IO.iodata_to_binary()
  end
end
```

```gleam
// src/congress/charts.gleam — Gleam wrapper, one @external per chart type
pub opaque type SvgString = String

@external(erlang, "Elixir.ChartBridge", "bar_chart")
pub fn bar_chart(
  rows: List(#(String, Float)),
  width: Int,
  height: Int,
) -> String

@external(erlang, "Elixir.ChartBridge", "pie_chart")
pub fn pie_chart(
  rows: List(#(String, Float)),
  width: Int,
  height: Int,
) -> String
```

```gleam
// In a Wisp handler — server-side rendering
import congress/charts
import lustre/element/html

pub fn votation_detail(req, id) {
  let breakdown = db.party_breakdown(id)
  let chart_svg = charts.bar_chart(breakdown, 600, 300)

  // Embed the SVG string directly in server-rendered HTML
  html.div([attr.class("chart-container")], [
    html.unsafe_inner_html(chart_svg)
    // CSS @keyframes on .exc-bar animate the bars on load
  ])
}
```

The SVG that Contex emits contains CSS classes (`.exc-bar`, `.exc-tick`,
`.exc-domain`, etc.) that you can target with Tailwind or custom CSS to add
entry animations — no JavaScript at all.

---

### 4.3 BEAM Chart Libraries Available to Gleam

| Library | Language | Build Tool | Gleam Wrapping | Chart Types | Last Update |
|---|---|---|---|---|---|
| [`contex`](https://hex.pm/packages/contex) | Elixir | mix | Bridge `.ex` file | Bar, Line, Scatter, Pie, Gantt, Sparkline | May 2023 |
| [`plox`](https://hex.pm/packages/plox) | Elixir | mix | Bridge `.ex` file | Line, Bar (extensible) | 2024 |
| [`sparkline_svg`](https://hex.pm/packages/sparkline_svg) | Elixir | mix | Direct `@external` (returns String) | Sparkline only | Mar 2024 |
| [`erlang_svg`](https://hex.pm/packages/erlang_svg) | Erlang | rebar3 | Direct `@external` (no mix needed) | Shapes/primitives only | Jun 2021 |
| Custom Gleam SVG | Gleam | — | None (write it) | Whatever you build | — |

**Recommendation for this project:** Use `contex` via an Elixir bridge file for
full bar/pie charts (party breakdowns, vote distributions). For sparklines on
list pages (trend lines per representative), `sparkline_svg` can be called
directly without any bridge since its API is simple string-in/string-out.

---

### 4.4 Architecture Options

**Option A — SSR + Hydration (recommended for this project)**
```
Server: Wisp renders HTML (Lustre element.to_document_string)
       └── Chart SVG from Contex bridge (pure server-side)
       └── sends full HTML with embedded Lustre app
Client: Lustre hydrates → MVU loop takes over for interactivity
        CSS @keyframes on SVG elements handle chart animations
```

**Option B — SPA with JSON API**
```
Server: Wisp exposes JSON endpoints
        Charts rendered server-side, returned as SVG strings in JSON
Client: Lustre SPA fetches data + SVG strings, inserts into DOM
```

---

### 4.5 Example: Gleam Component with Server-Side Chart

```gleam
// Gleam server handler for a votation detail page
import congress/charts
import congress/db
import wisp.{type Request, type Response}
import lustre/element
import lustre/element/html
import lustre/attribute as attr

pub fn votation_detail(req: Request, id: Int) -> Response {
  let votes     = db.get_party_breakdown(id)
  let chart_svg = charts.bar_chart(votes, 620, 320)

  let page =
    html.div([attr.class("p-6")], [
      html.h1([attr.class("text-2xl font-bold mb-4")], [
        html.text("Votación #" <> int.to_string(id)),
      ]),
      html.div([attr.class("chart-wrapper animate-fade-in")], [
        // Raw SVG from Contex, styled with Tailwind + CSS animations
        html.unsafe_inner_html(chart_svg),
      ]),
    ])

  wisp.html_response(element.to_document_string(page), 200)
}
```

---

### 4.6 Pros & Cons

**Pros**

- ✅ **Full BEAM ecosystem access** — all Elixir/Erlang Hex packages callable via FFI
- ✅ `contex` provides production-quality SVG charts with a single bridge file
- ✅ 100% type-safe in Gleam code; bridge file is small and isolated
- ✅ SSR + hydration: fast initial loads, no layout shift on charts
- ✅ You write zero JS; Gleam compiles to JS for browser interactivity
- ✅ Runs on BEAM: same reliability, concurrency, and OTP story as Elixir
- ✅ CSS animations on Contex SVG classes work out of the box
- ✅ `gleam_erlang` stdlib provides OTP GenServer, process supervision, etc.

**Cons**

- ❌ **Requires both Gleam and Elixir installed** (for mix-based deps like contex)
- ❌ Elixir bridge files lose Gleam's type safety at the boundary
- ❌ Very young ecosystem: Gleam v1 launched March 2024, fewer examples
- ❌ Lustre is SPA-focused; SSR/hydration is newer and still evolving
- ❌ The browser runtime is compiled JS — not truly zero JS like WASM
- ❌ No equivalent to Phoenix LiveView's built-in real-time diffing
- ❌ `contex` last updated May 2023 (maintained but not rapidly evolving)

### JS Score: ⭐⭐⭐⭐ (Level 2 — Gleam compiles to JS, you write zero JS; charts are pure SVG)

---

## 5. Ruby on Rails + Hotwire

> **"Maximum productivity, minimal JS — but charts still need some JS under the hood."**

### What It Is

Rails with [Hotwire](https://hotwired.dev/) (Turbo + Stimulus) is the canonical
"server-rendered with sprinkles of interactivity" stack. Turbo handles page
navigation and partial updates without writing JS. Stimulus provides minimal
JS controllers where needed.

### Stack

```
Language:    Ruby
Framework:   Rails 7/8
DB:          SQLite (via activerecord) or PostgreSQL
Ingest:      Sidekiq or GoodJob (background jobs)
Frontend:    Hotwire (Turbo Frames + Turbo Streams) + Tailwind CSS
Charts:      Chartkick gem → wraps Chart.js/ApexCharts (still JS!)
             OR: custom SVG helpers (no JS)
Deploy:      Docker / Render / Fly.io
```

### Hotwire Partial Update Example

```erb
<!-- app/views/votations/show.html.erb -->
<%= turbo_frame_tag "vote-breakdown" do %>
  <%= render "vote_chart", votes: @votes %>
<% end %>

<%= turbo_frame_tag "party-filter" do %>
  <%= link_to "Filter: Frente Amplio",
      votation_path(@votation, party: "FA"),
      data: { turbo_frame: "vote-breakdown" } %>
<% end %>
```

Clicking the link swaps only the `vote-breakdown` frame — no full page reload,
no custom JS.

### Charts in Rails Without JS

Option 1: **Custom SVG helpers** (pure Ruby, no JS):
```ruby
# app/helpers/chart_helper.rb
def bar_chart_svg(data, width: 600, height: 300)
  # Generate <svg> markup from Ruby — CSS handles animations
  content_tag(:svg, width: width, height: height) do
    data.each_with_index.map do |(label, value), i|
      bar_width = (value.to_f / data.values.max * width).round
      content_tag(:rect, "",
        x: 0, y: i * 40, width: bar_width, height: 30,
        class: "bar bar-animated", fill: party_color(label)
      )
    end.join.html_safe
  end
end
```

Option 2: **Chartkick** (uses Chart.js underneath — still JS, but abstracted):
```erb
<%= bar_chart @party_breakdown, library: { animation: { duration: 800 } } %>
```

Option 3: **Vega-Lite** (declarative JSON spec → rendered client-side by Vega
JS library, ~100 KB). Less JS-authored, but JS is still present.

### Pros

- ✅ Fastest development velocity of all options
- ✅ Turbo Frames/Streams give SPA-like UX with zero authored JS
- ✅ Massive ecosystem: gems for everything (auth, jobs, search, etc.)
- ✅ ActionCable for WebSocket-based live updates (like LiveView lite)
- ✅ SQLite3 adapter is first-class in Rails 7.1+
- ✅ Best tooling, documentation, and community
- ✅ Custom SVG helpers can eliminate Chart.js entirely

### Cons

- ❌ Charting gems (Chartkick) still load Chart.js/ApexCharts under the hood
- ❌ Ruby is slower than Go/Elixir/Rust (fine for this data volume)
- ❌ Heavier deploy footprint (Puma, gems, bundler)
- ❌ No Oban-quality background jobs built in (Sidekiq requires Redis)
  → GoodJob uses Postgres/SQLite (better for single-server deploy)
- ❌ Less elegant than LiveView for real-time live updates

### JS Score: ⭐⭐⭐ (Level 1–3 depending on chart approach)

---

## 6. Rust + Leptos (Full-Stack WASM)

> **"Zero JavaScript, maximum performance — steep learning curve."**

### What It Is

[Leptos](https://leptos.dev/) is a full-stack Rust web framework. The server
runs with Axum; the client compiles to **WebAssembly** (WASM) — not JavaScript.
The browser runs native Rust compiled to WASM. You author **zero JS**.

### Stack

```
Language:    Rust (server + client, same codebase)
Framework:   Leptos (SSR + hydration via WASM)
HTTP Server: Axum (embedded in Leptos)
DB:          SQLx + SQLite
Ingest:      Tokio tasks (async) or custom scheduler
Frontend:    Leptos reactive components + Tailwind CSS
Charts:      leptos-chartistry (pure Rust/WASM, SVG-based)
             OR server-side SVG via svg crate
Deploy:      Single binary (server) + WASM bundle (client)
```

### Project Structure

```
src/
├── main.rs              # Axum server entry point
├── app.rs               # Root Leptos component (shared server/client)
├── components/
│   ├── vote_chart.rs    # Leptos component using leptos-chartistry
│   ├── party_badge.rs
│   └── vote_table.rs
├── pages/
│   ├── votations.rs     # /votaciones
│   ├── votation_detail.rs
│   ├── representatives.rs
│   └── party_detail.rs
├── db/
│   ├── schema.rs        # SQLx query functions
│   └── models.rs
└── ingestion/
    ├── camara.rs        # reqwest + serde_xml_rs for API
    └── senado.rs
```

### leptos-chartistry Example

```rust
use leptos::*;
use leptos_chartistry::*;

#[component]
pub fn VoteBreakdown(#[prop(into)] data: MaybeSignal<Vec<PartyVote>>) -> impl IntoView {
    let series = Series::new(|d: &PartyVote| d.party.clone())
        .bar(|d: &PartyVote| d.votes as f64);

    view! {
        <Chart
            aspect_ratio=AspectRatio::from_outer_height(300.0, 1.5)
            series=series
            data=data
        />
    }
}
```

`leptos-chartistry` generates pure SVG. No Chart.js. No JS. CSS animations
apply normally.

### Pros

- ✅ **Truly zero JavaScript** authored or executed — WASM only
- ✅ Same language server + client: share types, validations, business logic
- ✅ `leptos-chartistry` is a mature Rust charting lib (SVG, no JS)
- ✅ Blazing performance (Rust + WASM outperforms JS for complex charts)
- ✅ Single binary server deployment
- ✅ Full type safety across the entire stack
- ✅ Reactive fine-grained updates (no virtual DOM)

### Cons

- ❌ **Steepest learning curve** of all options (ownership, lifetimes, WASM)
- ❌ **Very slow compile times** (~30–90s full builds)
- ❌ WASM bundles can be large (~1–3 MB before compression)
- ❌ Leptos ecosystem still maturing (v0.7 as of late 2024)
- ❌ Debugging WASM in browser is more complex than JS
- ❌ `cargo-leptos` build toolchain adds complexity
- ❌ Async Rust (Tokio) + Leptos signals has a learning curve

### JS Score: ⭐⭐⭐⭐⭐ (Level 2 — WASM runtime, zero JS authored or present)

---

## 7. Cross-Cutting Concern: Database Layer

> **SQLite as the database, `.sql` files as the source of truth for schema and queries, a build-step that generates typed code, and SQL errors caught before runtime.**

This section covers how each stack handles the database requirements:

- SQLite as the database (zero-config, single file, excellent for this data volume)
- Schema defined in versioned `.sql` migration files
- Queries written in plain `.sql` files (one per query or with named annotations)
- A generation/compile step that produces typed, language-native database functions
- SQL errors (wrong column names, type mismatches, syntax errors) caught before the app runs

---

### 7.1 The Standard Layout

All stacks below converge on this directory shape:

```
db/
├── migrations/
│   ├── 001_initial_schema.sql    ← CREATE TABLE, indexes, constraints
│   ├── 002_add_party_colors.sql  ← ALTER TABLE or new tables
│   └── 003_add_senate_tables.sql
└── queries/
    ├── parties.sql               ← named SQL queries for parties
    ├── votations.sql
    ├── representatives.sql
    └── votes.sql
```

The **generation step** reads the migration files (to build a schema model) and the query files (to generate functions), then emits typed code in the target language. SQL errors are caught here — before you compile the application.

---

### 7.2 Go: sqlc + goose ⭐⭐⭐⭐⭐

**[sqlc](https://sqlc.dev)** (14K stars) is the gold standard for this exact pattern. It reads your schema SQL and query SQL files, validates both, and emits type-safe Go structs and functions. **[goose](https://github.com/pressly/goose)** (10K stars) manages the migration lifecycle (run, rollback, status). The two tools are explicitly designed to work together.

#### sqlc.yaml — single config file

```yaml
# sqlc.yaml
version: "2"
sql:
  - engine: "sqlite"
    schema:  "db/migrations/"    # reads all .sql files here as the schema
    queries: "db/queries/"       # reads all .sql files here as queries
    gen:
      go:
        package:              "database"
        out:                  "internal/database"
        emit_json_tags:       true
        emit_prepared_queries: true
        emit_interface:       true
```

#### Migration file (schema source of truth)

```sql
-- db/migrations/001_initial_schema.sql
CREATE TABLE IF NOT EXISTS parties (
  id    INTEGER PRIMARY KEY AUTOINCREMENT,
  name  TEXT    NOT NULL UNIQUE,
  slug  TEXT    NOT NULL UNIQUE,
  color TEXT    NOT NULL DEFAULT '#888888'
);

CREATE TABLE IF NOT EXISTS representatives (
  id         INTEGER PRIMARY KEY,
  chamber    TEXT    NOT NULL CHECK (chamber IN ('deputies','senate')),
  first_name TEXT    NOT NULL,
  last_name  TEXT    NOT NULL,
  party_id   INTEGER REFERENCES parties(id)
);

CREATE TABLE IF NOT EXISTS votations (
  id          INTEGER PRIMARY KEY,
  date        TEXT    NOT NULL,
  description TEXT    NOT NULL,
  result      TEXT    NOT NULL,
  total_yes   INTEGER NOT NULL DEFAULT 0,
  total_no    INTEGER NOT NULL DEFAULT 0,
  total_abs   INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS votes (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  votation_id       INTEGER NOT NULL REFERENCES votations(id),
  representative_id INTEGER NOT NULL REFERENCES representatives(id),
  option            TEXT    NOT NULL CHECK (option IN ('Si','No','Abstencion','Dispensado')),
  UNIQUE (votation_id, representative_id)
);
```

#### Query file

```sql
-- db/queries/votations.sql

-- name: GetPartyBreakdown :many
SELECT  p.name  AS party_name,
        p.color AS party_color,
        COUNT(CASE WHEN v.option = 'Si'         THEN 1 END) AS votes_yes,
        COUNT(CASE WHEN v.option = 'No'         THEN 1 END) AS votes_no,
        COUNT(CASE WHEN v.option = 'Abstencion' THEN 1 END) AS abstentions
FROM    votes v
JOIN    representatives r ON r.id = v.representative_id
JOIN    parties p          ON p.id = r.party_id
WHERE   v.votation_id = ?
GROUP   BY p.id, p.name, p.color
ORDER   BY votes_yes DESC;

-- name: ListVotations :many
SELECT id, date, description, result, total_yes, total_no, total_abs
FROM   votations
ORDER  BY date DESC
LIMIT  ? OFFSET ?;
```

#### The build step

```sh
# Generate type-safe Go code from schema + queries
sqlc generate          # catches SQL errors here — before go build

# Run migrations against SQLite
goose -dir db/migrations sqlite3 data/congress.db up
```

`sqlc generate` produces:
- `internal/database/models.go` — one Go struct per table row
- `internal/database/votations.sql.go` — type-safe functions

```go
// internal/database/votations.sql.go  (AUTO-GENERATED — do not edit)
type GetPartyBreakdownRow struct {
    PartyName   string `json:"party_name"`
    PartyColor  string `json:"party_color"`
    VotesYes    int64  `json:"votes_yes"`
    VotesNo     int64  `json:"votes_no"`
    Abstentions int64  `json:"abstentions"`
}

func (q *Queries) GetPartyBreakdown(ctx context.Context, votationID int64) ([]GetPartyBreakdownRow, error) {
    // ...generated prepared-statement execution...
}
```

If you rename a column in the migration file and forget to update the query,
`sqlc generate` **refuses to proceed** with a compile-time error.

**Makefile integration:**

```makefile
.PHONY: generate
generate:
	sqlc generate
	go generate ./...

.PHONY: migrate
migrate:
	goose -dir db/migrations sqlite3 $(DB_PATH) up

.PHONY: build
build: generate
	go build -o bin/server ./cmd/server
```

---

### 7.3 Gleam: Parrot ⭐⭐⭐⭐⭐ (SQLite ✅)

Two tools exist for type-safe SQL in Gleam:

| Tool | Stars | SQLite | PostgreSQL | MySQL | How |
|---|:---:|:---:|:---:|:---:|---|
| **[Parrot](https://github.com/daniellionel01/parrot)** | 188 | ✅ | ✅ | ✅ | reads `.sql` files → generates Gleam |
| **[Squirrel](https://github.com/giacomocavalieri/squirrel)** | 617 | ❌ | ✅ | ❌ | reads `.sql` files → generates Gleam |

**Parrot** is the right choice for this project (SQLite). It is explicitly
described as a Gleam port of sqlc. Squirrel is more mature but PostgreSQL-only.

#### Query file (identical structure to Go)

```sql
-- src/congress/sql/get_party_breakdown.sql
SELECT  p.name  AS party_name,
        p.color AS party_color,
        COUNT(CASE WHEN v.option = 'Si'         THEN 1 END) AS votes_yes,
        COUNT(CASE WHEN v.option = 'No'         THEN 1 END) AS votes_no,
        COUNT(CASE WHEN v.option = 'Abstencion' THEN 1 END) AS abstentions
FROM    votes v
JOIN    representatives r ON r.id = v.representative_id
JOIN    parties p          ON p.id = r.party_id
WHERE   v.votation_id = $1
GROUP   BY p.id, p.name, p.color
ORDER   BY votes_yes DESC
```

#### The build step

```sh
# Point Parrot at the schema and generate typed Gleam functions
gleam run -m parrot    # validates SQL against schema; errors fail the build
gleam build            # then compile Gleam as normal
```

Parrot reads the `.sql` files, connects to the schema, validates every query,
and generates a `sql.gleam` module:

```gleam
// src/congress/sql.gleam  (AUTO-GENERATED by Parrot — do not edit)

pub type GetPartyBreakdownRow {
  GetPartyBreakdownRow(
    party_name:   String,
    party_color:  String,
    votes_yes:    Int,
    votes_no:     Int,
    abstentions:  Int,
  )
}

pub fn get_party_breakdown(
  db: sqlight.Connection,
  votation_id: Int,
) -> Result(List(GetPartyBreakdownRow), sqlight.Error) {
  // ...generated decoder and query execution...
}
```

Schema migrations are managed via plain `.sql` files applied with
`gleam_migrate` or a simple shell script that runs them with `sqlite3`.

---

### 7.4 Elixir: Yesql / AyeSql + Ecto ⭐⭐⭐

Elixir has `.sql` file support but **not** compile-time SQL validation.
This is the most honest gap in the Elixir story for this requirement.

#### Schema: Ecto migrations

Ecto migrations use a Ruby-style DSL by default, but raw SQL is supported
via `execute/1`:

```elixir
# priv/repo/migrations/20240101000000_initial_schema.exs
defmodule Congress.Repo.Migrations.InitialSchema do
  use Ecto.Migration

  def change do
    create table(:parties) do
      add :name,  :string, null: false
      add :slug,  :string, null: false
      add :color, :string, null: false, default: "#888888"
    end
    create unique_index(:parties, [:name])
    create unique_index(:parties, [:slug])

    # Raw SQL for CHECK constraints not supported by DSL:
    execute """
      CREATE TABLE IF NOT EXISTS votes (
        id                INTEGER PRIMARY KEY AUTOINCREMENT,
        votation_id       INTEGER NOT NULL REFERENCES votations(id),
        representative_id INTEGER NOT NULL REFERENCES representatives(id),
        option            TEXT    NOT NULL
          CHECK (option IN ('Si','No','Abstencion','Dispensado')),
        UNIQUE (votation_id, representative_id)
      )
    """
  end
end
```

Alternatively, configure `config.active_record.schema_format = :sql` equivalent:
set `config :congress, Congress.Repo, migration_primary_key: [type: :integer]`
and use `mix ecto.dump` to get a `structure.sql` snapshot.

#### Query files: Yesql

```sql
-- priv/queries/votations.sql
-- name: party_breakdown
SELECT  p.name  AS party_name,
        p.color AS party_color,
        COUNT(CASE WHEN v.option = 'Si'         THEN 1 END) AS votes_yes,
        COUNT(CASE WHEN v.option = 'No'         THEN 1 END) AS votes_no,
        COUNT(CASE WHEN v.option = 'Abstencion' THEN 1 END) AS abstentions
FROM    votes v
JOIN    representatives r ON r.id = v.representative_id
JOIN    parties p          ON p.id = r.party_id
WHERE   v.votation_id = :votation_id
GROUP   BY p.id, p.name, p.color
ORDER   BY votes_yes DESC

-- name: list_votations
SELECT id, date, description, result, total_yes, total_no, total_abs
FROM   votations
ORDER  BY date DESC
LIMIT  :limit OFFSET :offset
```

```elixir
# lib/congress/queries.ex
defmodule Congress.Queries do
  use Yesql, driver: Ecto, conn: Congress.Repo

  # Each defquery/1 reads the .sql file at *Elixir compile time*
  # and creates a function with the query's name:
  Yesql.defquery("priv/queries/votations.sql")
end

# Usage:
Congress.Queries.party_breakdown(votation_id: 12345)
# => {:ok, [%{party_name: "Frente Amplio", votes_yes: 45, ...}]}
```

**AyeSql** (132 stars) is an alternative with async/concurrent execution support,
useful for running multiple queries in parallel during page renders.

#### ⚠️ The critical limitation

Yesql and AyeSql turn `.sql` files into Elixir functions **at Elixir compile
time**, but they do **not** parse or validate the SQL. A typo in a column name
compiles fine and only blows up at runtime. This is fundamentally different from
sqlc (Go) and sqlx/Parrot (Rust/Gleam).

**Mitigation:** Comprehensive ExUnit tests that exercise every query against the
test database, run in CI before deployment. This is the standard Elixir approach.

---

### 7.5 Rust + Leptos: sqlx ⭐⭐⭐⭐⭐ (strongest compile-time guarantee)

`sqlx` provides the most rigorous compile-time SQL checking of any tool in
this comparison. It connects to the **actual database** at compile time
(via `DATABASE_URL`), validates SQL syntax, column names, and types, and maps
results to Rust structs — all as a compile error, not a runtime error.

#### Schema: sqlx-cli migrations

```sh
# Create versioned .sql migration files
sqlx migrate add initial_schema   # creates migrations/20240101000000_initial_schema.sql
sqlx migrate add party_colors     # creates migrations/20240102000000_party_colors.sql

# Apply migrations
sqlx migrate run                  # runs pending migrations against DATABASE_URL
```

```sql
-- migrations/20240101000000_initial_schema.sql
CREATE TABLE parties (
  id    INTEGER PRIMARY KEY AUTOINCREMENT,
  name  TEXT    NOT NULL UNIQUE,
  color TEXT    NOT NULL DEFAULT '#888888'
);

CREATE TABLE votes (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  votation_id       INTEGER NOT NULL REFERENCES votations(id),
  representative_id INTEGER NOT NULL REFERENCES representatives(id),
  option            TEXT    NOT NULL
    CHECK (option IN ('Si','No','Abstencion','Dispensado')),
  UNIQUE (votation_id, representative_id)
);
```

#### Query files: `query_file!` macro

```sql
-- queries/get_party_breakdown.sql
SELECT  p.name  AS "party_name",
        p.color AS "party_color",
        COUNT(CASE WHEN v.option = 'Si'         THEN 1 END) AS "votes_yes!: i64",
        COUNT(CASE WHEN v.option = 'No'         THEN 1 END) AS "votes_no!: i64",
        COUNT(CASE WHEN v.option = 'Abstencion' THEN 1 END) AS "abstentions!: i64"
FROM    votes v
JOIN    representatives r ON r.id = v.representative_id
JOIN    parties p          ON p.id = r.party_id
WHERE   v.votation_id = $1
GROUP   BY p.id, p.name, p.color
ORDER   BY "votes_yes!: i64" DESC
```

```rust
// src/db/votations.rs
#[derive(Debug, sqlx::FromRow)]
pub struct PartyBreakdownRow {
    pub party_name:  String,
    pub party_color: String,
    pub votes_yes:   i64,
    pub votes_no:    i64,
    pub abstentions: i64,
}

pub async fn get_party_breakdown(
    pool: &SqlitePool,
    votation_id: i64,
) -> sqlx::Result<Vec<PartyBreakdownRow>> {
    sqlx::query_file_as!(
        PartyBreakdownRow,
        "queries/get_party_breakdown.sql",  // ← external .sql file
        votation_id
    )
    .fetch_all(pool)
    .await
}
```

#### Compile-time checking + offline mode

```sh
# DATABASE_URL must point to a migrated SQLite file at compile time:
export DATABASE_URL=sqlite://data/dev.db

cargo build   # connects to DB, validates ALL query_file! macros — errors = compile errors

# For CI (no live DB): generate a cached metadata snapshot first:
cargo sqlx prepare          # writes .sqlx/ directory
cargo build --offline       # uses .sqlx/ cache, no live DB needed
```

A wrong column name, a missing table, or a type mismatch in any `.sql` file
is a **hard Rust compile error**.

---

### 7.6 Ruby on Rails: No Equivalent ⭐

Rails does not have a sqlc/sqlx-equivalent. The honest picture:

| Concern | Rails approach | SQL files? | Compile-time check? |
|---|---|:---:|:---:|
| Schema | ActiveRecord migration DSL (Ruby) | ❌ | ❌ |
| Schema (alt) | `db/structure.sql` dump | ✅ generated | ❌ |
| Queries | ActiveRecord ORM (Ruby DSL) | ❌ | ❌ |
| Raw queries | `find_by_sql` / `execute` | ❌ inline strings | ❌ |
| SQL files | No built-in support | ❌ | ❌ |

The closest you can get:
- Use `config.active_record.schema_format = :sql` to get a `db/structure.sql`
  dump (this is raw SQL maintained by Rails, not hand-written)
- Write raw SQL inline in `find_by_sql("SELECT ...")` calls
- Use a comprehensive RSpec/Minitest suite to catch SQL errors at test time

Rails is powerful and the ORM is excellent for most queries. But if the
requirement is **`.sql` files + codegen + compile-time check**, Rails is the
weakest of the five stacks.

---

### 7.7 Summary Table

| | Go | Gleam | Elixir | Rust/Leptos | Rails |
|---|:---:|:---:|:---:|:---:|:---:|
| **SQLite support** | ✅ | ✅ | ✅ | ✅ | ✅ |
| **Schema in `.sql` files** | ✅ goose | ✅ custom | ⚠️ Ecto DSL + `execute` | ✅ sqlx-cli | ⚠️ `structure.sql` only |
| **Queries in `.sql` files** | ✅ sqlc | ✅ Parrot | ✅ Yesql / AyeSql | ✅ `query_file!` | ❌ inline only |
| **Code generation step** | ✅ `sqlc generate` | ✅ `gleam run -m parrot` | ❌ (macro, no codegen) | ⚠️ `sqlx prepare` (cache) | ❌ |
| **SQL error at codegen time** | ✅ static analysis | ✅ validates vs schema | ❌ runtime only | ✅ compile-time (live DB) | ❌ runtime only |
| **No live DB needed for check** | ✅ schema file only | ⚠️ needs DB or schema | N/A | ⚠️ needs `sqlx prepare` first | N/A |
| **Tool maturity** | ⭐⭐⭐⭐⭐ sqlc | ⭐⭐⭐ Parrot (new) | ⭐⭐⭐⭐ Yesql | ⭐⭐⭐⭐⭐ sqlx | ⭐ no equivalent |

**Winner for this requirement: Go (sqlc) or Rust (sqlx)** — both catch SQL
errors before any code runs. Gleam (Parrot) is close but younger. Elixir and
Rails trade compile-time guarantees for developer ergonomics.

---

## 8. Cross-Cutting Concern: Charts Without JS

This section applies to **all** stacks.

### 8.1 Server-Side SVG + CSS Animations ⭐⭐⭐⭐⭐

Any language can emit `<svg>` markup in HTML. The browser renders it natively.
CSS handles animations with zero JS required.

```svg
<!-- Bar chart, server-rendered, CSS-animated -->
<svg viewBox="0 0 400 200" xmlns="http://www.w3.org/2000/svg">
  <style>
    .bar { animation: grow 0.8s ease-out forwards; transform-origin: bottom; }
    @keyframes grow { from { transform: scaleY(0); } to { transform: scaleY(1); } }
  </style>
  <rect class="bar" x="10"  y="142" width="60" height="58" fill="#7c3aed" style="animation-delay: 0s"/>
  <rect class="bar" x="80"  y="151" width="60" height="49" fill="#1d4ed8" style="animation-delay: 0.1s"/>
  <text x="40"  y="195" text-anchor="middle" font-size="11">FA</text>
  <text x="110" y="195" text-anchor="middle" font-size="11">CV</text>
</svg>
```

**Available libraries for server-side SVG generation:**

| Language | Library | Chart Types |
|---|---|---|
| Elixir | `Plox` | Line, Bar, custom |
| Elixir | `Contex` | Bar, Line, Pie, Gantt |
| Rust | `plotters` | Most types, SVG/PNG output |
| Ruby | `Gruff` | Bar, Line, Pie (PNG/SVG) |
| Go | `go-chart` | Line, Bar, SVG output |
| Any | Custom SVG helpers | Whatever you build |

### 8.2 WASM Charts (Rust/Leptos) ⭐⭐⭐⭐⭐

```
leptos-chartistry  — SVG-based, reactive, production-ready (v0.2)
leptos_chart       — SVG charts (Pie, Bar, Line, Radar, Scatter)
```

Both render to `<svg>` in the DOM via WASM. No JS authored or present.

### 8.3 Pure CSS Bar Charts ⭐⭐ (no JS, limited to simple cases)

For simple horizontal bar charts with percentages, pure CSS + HTML is enough:

```html
<div class="chart">
  <div class="bar" style="--pct: 29%; --color: #7c3aed">
    <span class="label">Frente Amplio</span>
    <span class="value">45</span>
  </div>
</div>
```
```css
.bar::after {
  content: '';
  width: var(--pct);
  background: var(--color);
  animation: grow-bar 0.8s cubic-bezier(0.4, 0, 0.2, 1) forwards;
}
@keyframes grow-bar { from { width: 0 } to { width: var(--pct) } }
```

---

## 9. Comparison Matrix

*(Zig and Rust/Axum dropped per updated scope)*

| Criteria | Go (current) | Elixir/LiveView | Gleam/Lustre | Rails/Hotwire | Rust/Leptos |
|---|:---:|:---:|:---:|:---:|:---:|
| **JS authored** | Low | None | None | Low | None |
| **JS at runtime** | HTMX (~14 KB) | LiveView client | Gleam→JS compiled | Turbo + charts JS | WASM only |
| **Charts without JS** | ⚠️ + go-chart SVG | ✅ Plox/Contex SVG | ✅ Contex FFI bridge | ⚠️ SVG helpers | ✅ leptos-chartistry |
| **CSS animations** | ✅ | ✅ | ✅ | ✅ | ✅ |
| **Dev speed** | Fast | Fast | Slow | Fastest | Very Slow |
| **Real-time UI** | HTMX partials | LiveView ✅✅ | Lustre MVU | ActionCable | Leptos signals |
| **Background jobs** | Go goroutines | Oban ✅✅ | OTP GenServer | GoodJob | Tokio tasks |
| **Type safety** | Good | Runtime | Compile-time ✅ | Runtime | Compile-time ✅ |
| **Single binary** | ✅ | ❌ Erlang release | ❌ Erlang release | ❌ | ✅ + WASM bundle |
| **Ecosystem maturity** | ✅✅ | ✅✅ | ⚠️ young (v1 2024) | ✅✅ | ✅ |
| **Learning curve** | Low | Medium | High | Low | Very High |
| **Deploy simplicity** | ✅✅ | ✅ Fly.io | ✅ | ✅ Kamal | ✅ |
| **Memory footprint** | ~10 MB | ~50–100 MB | ~50 MB | ~100 MB | ~5 MB |
| **SQL `.sql` query files** | ✅ sqlc | ✅ Yesql / AyeSql | ✅ Parrot | ❌ inline only | ✅ `query_file!` |
| **Schema in `.sql` files** | ✅ goose migrations | ⚠️ Ecto DSL + `execute` | ✅ plain `.sql` | ⚠️ `structure.sql` only | ✅ sqlx-cli migrations |
| **Code generation step** | ✅ `sqlc generate` | ❌ macro expansion only | ✅ `gleam run -m parrot` | ❌ none | ⚠️ `sqlx prepare` (cache) |
| **Compile-time SQL check** | ✅ static (no DB needed) | ❌ runtime only | ✅ validates vs DB | ❌ runtime only | ✅ compile-time (needs DB) |
| **Recommended** | Current ⭐ | Best overall ⭐⭐ | Adventurous | Pragmatic | Ambitious |

---

## 10. Recommendations

### 🥇 Best Option: Elixir + Phoenix LiveView

**Best overall for minimal JS, real-time UI, and good charts:**

```
Backend:   Elixir + Phoenix + LiveView
DB:        ecto_sqlite3 (SQLite)
Schema:    Ecto migrations (.exs files with DSL + execute for raw SQL)
Queries:   Yesql — .sql files → Elixir functions at compile time
Jobs:      Oban (background API ingestion workers)
Charts:    Plox or Contex (server-side SVG, no JS)
CSS:       Tailwind CSS + CSS @keyframes on SVG classes
Deploy:    Fly.io (first-class Elixir support, free tier)
```

**On SQL files:** Yesql compiles `.sql` query files into Elixir functions at
Elixir compile time. SQL errors are caught at runtime, not codegen time —
mitigate this with comprehensive ExUnit tests in CI. For schema, Ecto migrations
can use raw `execute("""...""")` blocks for SQLite-specific syntax (CHECK
constraints, triggers). Use `mix ecto.dump` to generate a `structure.sql`
snapshot as documentation.

---

### 🥈 Ambitious Option: Rust + Leptos

**Truly zero JS (WASM), strongest compile-time guarantees across the entire stack:**

```
Backend:   Rust + Axum (via Leptos server functions)
DB:        sqlx + SQLite
Schema:    sqlx-cli versioned .sql migration files
Queries:   query_file!("queries/foo.sql") macros — .sql files, compile-time checked
Jobs:      Tokio scheduled tasks
Charts:    leptos-chartistry (pure Rust/WASM SVG, no JS)
CSS:       Tailwind CSS
Deploy:    Single binary + WASM bundle
```

**On SQL files:** `query_file!` macros read external `.sql` files and validate
them against the live database at compile time (`DATABASE_URL` env var must be
set). Wrong column name = compile error. Run `cargo sqlx prepare` to cache
query metadata in `.sqlx/` for offline CI builds where no database is available.

---

### 🥉 Pragmatic Option: Ruby on Rails + Hotwire

**Fastest development velocity; weakest SQL-file discipline of the five:**

```
Backend:   Rails 8
DB:        SQLite3 via ActiveRecord (first-class since Rails 7.1)
Schema:    ActiveRecord migration DSL (Ruby) — no hand-written .sql schema
Queries:   ActiveRecord ORM or inline find_by_sql — no .sql query files natively
Jobs:      GoodJob (SQLite-backed, no Redis needed)
Charts:    Custom SVG helpers (no JS) or Chartkick (wraps Chart.js)
CSS:       Tailwind CSS
Deploy:    Kamal (Docker-based, free Rails tool)
```

**On SQL files:** Rails has no sqlc/sqlx equivalent. There is no `.sql` query
file → typed code generation pipeline. SQL errors surface at runtime. Use
`config.active_record.schema_format = :sql` to produce a `db/structure.sql`
snapshot (auto-generated by Rails, not hand-written). This is the trade-off
for the highest developer velocity.

---

### 🔬 Experimental Option: Gleam + Lustre + Wisp

**Cutting-edge type-safe language with a viable SQL-file story:**

```
Backend:   Wisp (HTTP) on BEAM
DB:        sqlight (SQLite via Erlang NIF)
Schema:    Plain .sql migration files applied via gleam_migrate or shell scripts
Queries:   Parrot — .sql files → typed Gleam functions (SQLite ✅)
Jobs:      OTP GenServer / BEAM processes
Frontend:  Lustre (Gleam compiles to JS, MVU architecture)
Charts:    Contex (Elixir) via a thin .ex bridge file
Deploy:    Erlang release or Docker
```

**On SQL files:** Parrot (188 stars, active development) is a Gleam port of
sqlc that supports SQLite, PostgreSQL, and MySQL. Run `gleam run -m parrot`
before `gleam build` to generate typed query functions from `.sql` files,
validating them against the schema. SQL errors are caught at generation time.
This is the most complete `.sql`-files story of the BEAM options.

**Trade-off:** Parrot is newer and less battle-tested than sqlc or sqlx. The
Gleam ecosystem is very young (v1 launched March 2024). Higher risk for a
finished project, but a genuinely exciting and principled stack.

---

### Decision Flowchart

```
Priority: .sql files + compile-time SQL errors?
├── Yes, and I want max safety + single binary → Rust + Leptos (sqlx)
├── Yes, and I want type-safe BEAM language    → Gleam + Lustre (Parrot)
│
└── SQL files at build time is nice but not critical
    ├── I want real-time UI + fast dev + BEAM  → Elixir + Phoenix LiveView  ⭐
    ├── I want max dev speed / familiar OOP    → Ruby on Rails + Hotwire
    └── I want to stay in Go                  → Go (current) + sqlc + go-chart SVG
```

---

### Quick-Win: Keep Go, Add sqlc and Fix Charts

If you want to **stay with the current Go stack** and satisfy all three
new requirements (SQLite, `.sql` files, compile-time SQL errors):

```sh
# 1. Add sqlc — generates type-safe Go from your .sql query files
#    sqlc.yaml points schema: at goose migrations, queries: at db/queries/
sqlc generate          # catches SQL errors before go build

# 2. Add goose — manages .sql migration files
goose -dir db/migrations sqlite3 data/congress.db up

# 3. Replace Chart.js with go-chart (outputs <svg>) + CSS @keyframes
#    HTMX swaps chart partials — no custom JS needed
go build ./...
```

This satisfies every new requirement without changing languages:
- ✅ SQLite (already planned)
- ✅ Schema in `.sql` migration files (goose)
- ✅ Queries in `.sql` files (sqlc)
- ✅ Typed Go code generated from SQL (sqlc generate)
- ✅ SQL errors caught before `go build` (sqlc static analysis)
- ✅ No Chart.js (go-chart SVG + CSS animations)