# Stack Comparison — Chilean Congress Visualizer

## Alternatives

### 1. Go (current)

Go (`net/http` + chi), SQLite (`modernc.org/sqlite`), `html/template`, HTMX + Tailwind, go-chart SVG, sqlc + goose, colly/goquery for scraping, single binary deploy.

### 2. Elixir + Phoenix LiveView

Elixir + Phoenix + LiveView, `ecto_sqlite3`, Yesql `.sql` queries, Oban background jobs, Plox/Contex server-side SVG charts, Floki + Req for scraping, Fly.io deploy.

### 3. Gleam + Lustre + Wisp

Wisp on BEAM, `sqlight`, Parrot `.sql` query codegen, OTP GenServer jobs, Contex SVG charts via Elixir FFI bridge, scraping via Elixir FFI (Floki), Erlang release deploy.

### 4. Ruby on Rails + Hotwire

Rails 8, SQLite3 via ActiveRecord, Turbo Frames/Streams, GoodJob background jobs, custom SVG helpers or Chartkick, Nokogiri + Mechanize for scraping, Kamal deploy.

### 5. Rust + Leptos (Full-Stack WASM)

Rust + Axum via Leptos server functions, sqlx + SQLite with `query_file!`, Tokio tasks, leptos-chartistry WASM SVG charts, scraper + reqwest for scraping, single binary + WASM bundle deploy.

---

## Comparative Table

Categories are split into two groups: **constraint compliance** (hard requirements from the project plan) and **practical factors** (affect ability to ship).

### Constraint compliance

Each row maps to a numbered constraint from the plan. Constraint 6 ("compile/build-time SQL errors") is a hard requirement — runtime-only validation is disqualifying.

| # | Constraint                    | Go                       | Elixir / LiveView         | Gleam / Lustre            | Rails / Hotwire          | Rust / Leptos              |
|---|-------------------------------|:------------------------:|:-------------------------:|:-------------------------:|:------------------------:|:--------------------------:|
| 1 | **SQLite**                    | ✅ `modernc.org/sqlite`  | ✅ `ecto_sqlite3`         | ✅ `sqlight`              | ✅ `sqlite3` adapter     | ✅ sqlx                    |
| 2 | **Type-safe language**        | ✅ static, no sum types  | ❌ dynamic (Dialyzer opt-in) | ✅ compile-time, exhaustive | ❌ dynamic             | ✅ compile-time, exhaustive |
| 3 | **Minimize authored JS**      | ✅ zero (HTMX runtime)   | ✅ zero (LV runtime)      | ✅ zero (Gleam→JS compiled) | ✅ zero (Turbo runtime)  | ✅ zero (WASM only)        |
| 4 | **Queries in `.sql` files**   | ✅ sqlc                  | ✅ Yesql                  | ✅ Parrot                 | ❌ inline only           | ✅ `query_file!`           |
| 5 | **Schema in `.sql` files**    | ✅ goose migrations      | ⚠️ Ecto DSL (`execute` workaround) | ✅ plain `.sql`  | ⚠️ Ruby DSL (`structure.sql` dump) | ✅ sqlx-cli migrations |
| 6 | **Compile/build-time SQL check** | ✅ static (no DB needed) | ❌ runtime only         | ✅ validates vs DB at codegen | ❌ runtime only       | ✅ compile-time (needs DB or `sqlx prepare`) |
| 7 | **Charts (no authored JS)**   | ✅ go-chart SVG or Chart.js via CDN | ✅ Plox / Contex SVG | ✅ Contex via FFI bridge  | ✅ Chartkick (wraps Chart.js) | ✅ leptos-chartistry  |
| 8 | **Web scraping libs**         | ✅ colly / goquery        | ✅ Floki + Req             | ⚠️ FFI to Elixir libs     | ✅ Nokogiri + Mechanize   | ✅ scraper + reqwest       |

### Practical factors

| Criteria                   | Go                 | Elixir / LiveView   | Gleam / Lustre       | Rails / Hotwire     | Rust / Leptos        |
|----------------------------|-:------------------:|:--------------------:|:--------------------:|:-------------------:|:--------------------:|
| **Dev speed**              | Fast               | Fast                 | Slow                 | Fastest             | Very Slow            |
| **Learning curve**         | Low                | Medium               | High                 | Low                 | Very High            |
| **Ecosystem maturity**     | ✅✅ Mature        | ✅✅ Mature          | ⚠️ Young (v1 2024)  | ✅✅ Mature         | ✅ Maturing          |
| **XML parsing**            | ✅ `encoding/xml`  | ✅ SweetXml / xmerl  | ⚠️ FFI to xmerl     | ✅ Nokogiri         | ✅ quick-xml         |
| **Web scraping (Senate)**  | ✅ colly / goquery | ✅ Floki + Req       | ⚠️ FFI to Elixir libs | ✅ Nokogiri + Mechanize | ✅ scraper + reqwest |
| **Background jobs**        | Goroutines         | Oban ✅✅            | OTP GenServer        | GoodJob             | Tokio tasks          |
| **Local dev experience**   | ✅✅ `go run` + `air` hot reload, no runtime deps | ✅ `mix phx.server` + live reload, needs Erlang + Elixir | ⚠️ needs Gleam + Erlang + Elixir toolchains | ✅✅ `bin/rails s` + generators + console, needs Ruby | ⚠️ `cargo leptos watch`, 30–90s compiles, needs wasm32 target |
| **Deploy simplicity**      | ✅✅ single binary | ✅ Fly.io            | ✅ Erlang release    | ✅ Kamal            | ✅ single binary     |

---

## Constraint Scoring

Score per constraint: ✅ = 2, ⚠️ = 1, ❌ = 0. Maximum = 18 (8 constraints + 1 practical factor).

| Constraint                    | Go | Elixir | Gleam | Rails | Rust |
|-------------------------------|:--:|:------:|:-----:|:-----:|:----:|
| 1. SQLite                     |  2 |   2    |   2   |   2   |  2   |
| 2. Type-safe language         |  2 |   0    |   2   |   0   |  2   |
| 3. Minimize authored JS       |  2 |   2    |   2   |   2   |  2   |
| 4. Queries in `.sql` files    |  2 |   2    |   2   |   0   |  2   |
| 5. Schema in `.sql` files     |  2 |   1    |   2   |   1   |  2   |
| 6. Compile/build-time SQL ⚠️  |  2 |   0    |   2   |   0   |  2   |
| 7. Charts (no authored JS)    |  2 |   2    |   2   |   2   |  2   |
| 8. Web scraping libs          |  2 |   2    |   1   |   2   |  2   |
| 9. Local dev experience       |  2 |   2    |   1   |   2   |  1   |
| **Total**                     | **18** | **13** | **16** | **11** | **17** |

**Hard-constraint failures** (constraint 6 — disqualifying):

- **Elixir:** Yesql compiles `.sql` into functions but does not validate SQL against the schema. Errors surface at runtime only.
- **Rails:** No `.sql`-file-to-code pipeline exists. SQL errors are runtime only.

**Scored practical factor (row 9 — Local dev experience):**

| Stack | Score | Notes |
|-------|:-----:|-------|
| Go | 2 | `go run` + `air` hot reload, no external runtime deps, instant feedback |
| Elixir | 2 | `mix phx.server` + LiveView live reload, smooth once Erlang + Elixir installed |
| Gleam | 1 | Needs Gleam + Erlang + Elixir toolchains; FFI deps add setup friction |
| Rails | 2 | `bin/rails s` + generators + console, best-in-class DX once Ruby installed |
| Rust | 1 | `cargo leptos watch` works but 30–90s compile cycles make iteration painful |

---

## Ranking

### 1. Go — Best overall balance ⭐⭐⭐

**Score:** 18/18 — meets every constraint, best local dev experience.

Go meets every hard constraint with no gaps. sqlc is the gold standard for `.sql` → typed code generation with static analysis that needs no running database. goose manages schema in plain `.sql` migration files. HTMX requires zero authored JS. For charts, go-chart produces server-side SVG and Chart.js can be loaded via CDN with HTMX data attributes — both approaches require zero hand-written JS. The language is statically typed (though it lacks sum types and exhaustive matching). Local development is instant: `go run` + `air` for hot reload, no external runtime dependencies.

**Why first:** fastest path to a working MVP with zero constraint violations and the smoothest dev loop.

### 2. Rust + Leptos — Strongest guarantees ⭐⭐

**Score:** 17/18 (local dev is ⚠️ — slow compile cycles).

sqlx `query_file!` validates SQL at compile time against the actual database. Leptos compiles to WASM, producing literally zero JavaScript. leptos-chartistry renders rich interactive SVG charts entirely in Rust. The type system is the strongest of all five options. Every constraint is met perfectly.

**Why second:** the sole weakness is developer experience — 30–90s compile cycles, steep learning curve (ownership, lifetimes, async Rust, WASM toolchain), and a still-maturing ecosystem (Leptos v0.7). The risk of getting bogged down before shipping an MVP is real.

### 3. Gleam + Lustre + Wisp — Most principled ⭐

**Score:** 16/18 (web scraping ⚠️, local dev ⚠️).

Parrot (Gleam's sqlc equivalent) generates typed query functions from `.sql` files. Gleam's type system has exhaustive pattern matching and compile-time safety. Contex produces server-side SVG charts via an Elixir FFI bridge.

**Why third:** the ecosystem launched v1 in March 2024. Parrot is young and less battle-tested than sqlc or sqlx. XML parsing and web scraping both require FFI bridges to Erlang/Elixir libraries, adding friction for the data ingestion pipeline — which is the core of Phase 1. The Senate scraper (constraint 8) would be written partly outside Gleam's type-safe boundary. Local setup requires three toolchains (Gleam + Erlang + Elixir). High risk for a project that needs to actually ship.

### 4. Elixir + Phoenix LiveView

**Score:** 13/18. **Fails constraint 6** (compile/build-time SQL check).

Elixir is dynamically typed (fails constraint 2, which is soft). Yesql reads `.sql` files but does not validate SQL at build time (fails constraint 6, which is hard). Ecto uses a Ruby-like DSL for schema instead of plain `.sql` (partial fail on constraint 5). Local dev experience is excellent once set up.

Would otherwise be an excellent choice — LiveView, Oban, and Plox/Contex are best-in-class for real-time UI, background jobs, and server-side charts respectively. If constraint 6 were relaxed, Elixir would rank first or second.

### 5. Ruby on Rails + Hotwire

**Score:** 11/18. **Fails constraints 4 and 6.**

No `.sql`-file-to-code pipeline exists in the Rails ecosystem. SQL queries live inline in Ruby code. SQL and schema errors are caught at runtime only. The language is dynamically typed. With the relaxed JS constraint, Chartkick (wrapping Chart.js with zero authored JS) now satisfies the chart requirement. Local dev experience is excellent.

Fastest development velocity of all options, but violates too many hard constraints to be viable under these requirements.