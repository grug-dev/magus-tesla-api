Source: MAG-96 — https://linear.app/magus-monitor/issue/MAG-96/show-gasoline-equivalent-km-per-gallon-on-the-vehicle-stats-page
Roadmap: openspec/roadmaps/RM68-gasoline-equivalent.md
Tier: 1 of 2. Tier 2 (`RM68-gateway-add-km-per-gallon-tile`, module `gateway`) depends on
this tier — it reads `PricesForMonths` through this module's public Go port to compute the
`/vehicle-stats` tile. This tier has no caller yet; that is expected, not a gap.
Design gate: **TRIPPED.** This change creates a brand-new Postgres schema and table,
`reference.fuel_prices`. `design.md` carries the full schema, the rationale — including why
`region` and `product` columns were rejected and why the result is never stored in
`analytics.vehicle_monthly_metrics` — and the index plan against the read pattern.
Unit tests: **EXCLUDED** (roadmap D13, stated in the ticket). `design.md` carries a worked
numeric example in place of a Test Contract, so the implementation has a target it can be
checked against by hand.

## Why

Colombia measures a gasoline car in km per gallon. This platform measures efficiency in km
per 1% of battery — a number that means nothing to someone who drives gasoline. Every input
for a cost-parity comparison already exists in this platform except one: the price of a
gallon of gasoline. Nothing today stores that price, and it belongs to no vehicle and no
user — it is an external reference value, the same category of fact as an exchange rate.

`internal/reference` is a new module that owns exactly this one fact: what a gallon of
gasoline cost, by month (roadmap D1). It has no other responsibility. A later tier
(`gateway`) reads it to compute "what would this month's driving have cost in gasoline
terms" and prints that as an eighth tile on `/vehicle-stats`.

## What Changes

**Additive only — this tier creates a module that does not exist yet. No other module's
code changes.**

- **ADDED** — `internal/reference`, a new domain module. Package doc lives in
  `reference.go`.
- **ADDED** — `reference.fuel_prices`, one new table in a new `reference` Postgres schema:
  `period date` (month-start, same `EXTRACT(day FROM period) = 1` CHECK
  `analytics.vehicle_monthly_metrics` uses), `currency text`, `price numeric(14,2)`,
  `UNIQUE (period)`, plus this module's own `id`/`created_at`/`updated_at`. Full schema,
  rationale and index plan in `design.md`.
- **ADDED** — three migrations in `internal/reference/db/migrations/`: one baseline that
  creates the schema and the table (schema only, reads and writes nothing — matching every
  other module's baseline convention), then two ordinary migrations, one row each:
  `period = 2026-08-01, currency = 'COP', price = 16000.00`, then `period = 2026-09-01,
  currency = 'COP', price = 16331.00`. `design.md` TD2 states why each price is its own
  migration, separate from the baseline.
- **ADDED** — `reference.Reader`, a one-method read port:
  `PricesForMonths(ctx, start, end) ([]MonthPrice, error)`, resolving the "newest row at or
  before" fallback for every month in `[start, end]` in a single SQL query. No default price
  (roadmap D8) — a month with nothing to fall back to is simply absent from the result.
- **ADDED** — `reference.NewReader(pool *pgxpool.Pool) Reader`, declared in `reference.go`,
  implemented in a new `price_reader.go`.
- **ADDED** — one new sqlc query, `PricesForMonths`, in `internal/reference/db/query.sql`,
  and a new `sqlc.yaml` entry generating package `referencedb` into `internal/reference/db`
  — the same one-entry-per-module shape every other DB-backed module already follows.
- **ADDED** — `reference` to `MIGRATION_MODULES` in the `Makefile`. `design.md` states
  where in the list and why (position does not affect correctness — see
  `ai/go-conventions.md` "the order the directories are applied in does not matter" — but a
  position is still chosen for a stable, readable log order).
- **ADDED** — `internal/reference/AGENTS.md` and `internal/reference/README.md`, the module's
  agent identity and human-facing docs, mirroring the shape of an existing small DB-backed
  module (`internal/clock/AGENTS.md` for brevity, `internal/charging/AGENTS.md` for the
  DB-related sections).
- **CHANGED** — root `README.md`: the "Project Structure" tree gains a `reference/` entry
  under `internal/`, and the "Architecture" table gains a `internal/reference` row. The
  dependency graph gains `reference` as a LAYER 1 leaf (a domain module with its own `db`
  package, no internal dependencies — like `charging`).

**Explicitly NOT in this tier:**

- No gateway wiring. `cmd/web` does not construct `reference.NewReader` and inject it — that
  is tier 2's job, the first real caller.
- No tile, no view model, no i18n string. Tier 2 owns all of that.
- No command, no UI form, no poller to load prices (roadmap D4) — every price arrives as a
  migration, by hand.
- No unit tests (roadmap D13).

**Breaking:** no. A brand-new module and table with no existing caller.

**Modules affected:** `internal/reference` only (new). `sqlc.yaml`, `Makefile`, and the root
`README.md` change as shared, module-agnostic files — no other module's own code changes.

## Read paths affected

Per `openspec/config.yaml`'s "performance-sensitive proposals must name the read paths they
affect":

- **One read path is added, with no caller yet.** `PricesForMonths` is new; tier 2 is the
  first caller, once per `/vehicle-stats` render (and once per period change on that page).
  The declared `Performance-Profile` is read-heavy with reads mandatory-fast — resolving up
  to 12 months of prices in **one** query (`generate_series` + a `LATERAL` "newest at or
  before" lookup per month, all served by the `UNIQUE (period)` btree) keeps that read at one
  round trip regardless of the window size, instead of one round trip per month.
- **No existing read path changes.** No other module or table is touched by this tier.

## Impact

- **Affected specs:** `reference` — brand-new capability, every requirement **ADDED**.
- **Affected code:** `internal/reference/` (new: `reference.go`, `price_reader.go`, `db/`,
  `AGENTS.md`, `README.md`), `sqlc.yaml` (new entry), `Makefile`
  (`MIGRATION_MODULES`), root `README.md` (structure tree, architecture table, dependency
  graph).
- **Design gate:** TRIPPED — new schema, new table. See `design.md` for the schema, the
  rejected alternatives, and the index plan.
- **Deferred, explicitly NOT in scope:** the `/vehicle-stats` tile, the gateway view model,
  ES/EN catalogue entries, `cmd/web` wiring, and the `kkpa/context/` KB entry for the tile —
  all tier 2 (`RM68-gateway-add-km-per-gallon-tile`).
