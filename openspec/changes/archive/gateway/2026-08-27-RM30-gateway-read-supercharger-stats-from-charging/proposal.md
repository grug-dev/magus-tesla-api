Source: MAG-30 — https://linear.app/magus-monitor/issue/MAG-30/supercharger-stats-reads
Roadmap: openspec/roadmaps/RM30-supercharger-stats-read-from-charging.md
Tier: 2 of 2 (`gateway` swaps `/supercharger-stats` onto the `charging.SessionReader` port
tier 1 added). Depends on tier 1 (`RM30-charging-add-session-read-port`, archived
2026-08-27 at `openspec/changes/archive/charging/2026-08-27-RM30-charging-add-session-read-port/`).

Design gate: **not tripped.** This change creates or alters no database object — no migration,
no table, no column, no index, no constraint. `charging.SessionReader` and its backing index
(`idx_charge_sessions_vehicle_stop`) already exist, built by tier 1. `openspec/config.yaml`'s
blanket "design.md is REQUIRED for any DB-touching change" does not apply narrowly here (this
change touches no `db/` package and issues no SQL of its own), but design.md is written anyway
because this change alters a read path's shape and cap — the project's Performance-Profile
requires performance-sensitive proposals to name the read paths they affect (see below), and
design.md documents that analysis in full.

Unit tests: **all pure/offline**, unlike tier 1's DATABASE_URL-gated integration suite. Every
new/changed function in this tier (`parseSuperchargerRange`, `buildSuperchargerPresets`,
`buildSuperchargerStatsView`, `buildSuperchargerTiles`, `buildSuperchargerChart`,
`buildSuperchargerRows`) is a pure Go function over a fake `charging.SessionReader` — no
database, no `DATABASE_URL` gate. This mirrors `history_test.go`'s existing pattern for
`parseHistoryRange`/`buildHistoryView` exactly. `go vet ./...` compiles every test; the owner
runs `go test ./internal/gateway/...` per the binding Test-Execution-Policy.

Grill-me: the open design questions this tier would otherwise need interrogated (query
contract shape, default window, cap width, ordering, column removal) are **already settled**
by the roadmap's owner-approved Decisions 1, 3, 5, 6, 7 in
`openspec/roadmaps/RM30-supercharger-stats-read-from-charging.md` — that roadmap-authoring
pass is this change's grill-me equivalent, the same precedent tier 1's proposal followed (it
likewise ran no separate grill-me session; the roadmap Decisions section carries the resolved
interrogation for both tiers). The only new judgment calls this artifacts pass had to make on
its own (not covered by a roadmap Decision) are recorded in design.md as D8–D10 and restated in
this worker's final report to the pipeline leader.

## Why

RM30 tier 1 (`RM30-charging-add-session-read-port`, archived) gave `internal/charging` a read
port over `charge_sessions` — the record the platform's charging capability actually owns,
including the human-verified battery-percentage columns `internal/telemetry` can never write —
specifically so a consumer could stop reading Supercharger session history from
`telemetry.SuperchargerReader` over `supercharger_sessions`, the raw ingestion buffer
`charging` mirrors *from*, not the record it *owns*. MAG-30 names the consumer: the
`/supercharger-stats` page. This tier is that swap.

Separately, and independently motivating this change on its own: the page's existing
`?months=N` query parameter has always violated the platform's own mandated date-filter
convention (`internal/gateway/AGENTS.md` §"HTTP date-filter convention" — *"a closed
vocabulary of one: `?start=&end=`"*). Swapping the read port is the natural moment to also
bring the endpoint into compliance (roadmap Decision 5) rather than doing it as a second,
later change against the same handful of files.

Together these two motivations — read from the module that owns the data, and comply with the
platform's one HTTP date-filter contract — are why this tier touches the query contract, the
read call, the display ordering, and the two dropped columns all in one change: they are four
symptoms of the same root cause (`/supercharger-stats` was built before `charging.SessionReader`
and the `?start=&end=` convention existed, and never retrofitted to either).

## Breaking change

**Yes**, in two independent ways:

1. **HTTP query contract.** `GET /ui/supercharger-stats?months=N` (and the page route's implicit
   support for the same param) is replaced by `GET /ui/supercharger-stats?start=YYYY-MM-DD&end=YYYY-MM-DD`.
   A caller still passing `?months=N` gets the new default/validation behavior applied to
   `start`/`end` instead — `months` is simply an unrecognized, ignored query key from that point
   on, not an error. This was already a violation of the platform's mandated
   `?start=&end=` date-filter convention (`internal/gateway/AGENTS.md` §"HTTP date-filter
   convention"); this change brings the endpoint into compliance (roadmap Decision 5).
2. **Rendered page columns.** The sessions table's **Country** and **Billing Type** columns are
   removed — `charge_sessions` (the table `charging.SessionReader` reads) carries neither column,
   and the roadmap owner chose dropping them over a schema migration + backfill (roadmap
   Decision 1). This is an intended UI reduction, not a regression to fix later.

**Modules affected:** `gateway` only. `internal/charging` is consumed through its existing public
`SessionReader` port (tier 1, already shipped) and is not modified by this tier. `internal/telemetry`
is unaffected: the gateway keeps reading `telemetry.Reader`/`Snapshot` for the dashboard tiles and
the history battery chart (roadmap Decision 2) — only the Supercharger Stats page's own read moves
off `telemetry.SuperchargerReader`. `cmd/web`'s dependency-injection wiring (one constructor call)
also changes, but that is leader-owned integration outside `internal/` (see tasks.md).

## Read paths affected (Performance-Profile: read-heavy)

- `GET /supercharger-stats` (full page) and `GET /ui/supercharger-stats` (htmx fragment) — both
  currently read `telemetry.SuperchargerReader.SuperchargerSessionsByVehicle`, a single call
  capped at a **row limit** (500), with the month-window filter applied **in Go, in memory**,
  over the 500-row result. After this change, both read
  `charging.SessionReader.ListSessionsByVehicleBetween`, a single call bounded by a **date
  window** at the database (served by `idx_charge_sessions_vehicle_stop`, tier 1 design.md
  "Index proof" — a pure index range scan, no sort step), capped at **400 days** wide
  (`superchargerRangeMaxDays`, this tier's own read-path protection, mirroring
  `historyRangeMaxDays`'s role for `GET /ui/dashboard/history`). This is a strictly tighter
  bound than the row-limit approach: the old path always fetched up to 500 rows regardless of
  how narrow the requested window was; the new path's window is pushed to the database, so a
  narrow request reads fewer rows at the source instead of over-fetching and filtering
  client-side.
- No other read path changes. The dashboard tiles' `telemetry.Reader.LatestSnapshotsByAccount`
  call and the dashboard history fragment's `telemetry.Reader`/`analytics.Reader` calls are
  untouched (roadmap Decision 2).

## What Changes

- **CHANGED** — `internal/gateway/gateway.go` and `internal/gateway/handlers/handlers.go`:
  `Deps.SuperchargerReader` (both the package-level and handlers-level `Deps` structs, and the
  `Handler.superchargerReader` field) changes type from `telemetry.SuperchargerReader` to
  `charging.SessionReader`. The field/variable **name** is unchanged — only the type. The
  gateway no longer imports `internal/telemetry` for the Supercharger Stats path specifically
  (it keeps importing `internal/telemetry` package-wide for the dashboard/history paths,
  roadmap Decision 2).
- **CHANGED** — `internal/gateway/handlers/supercharger.go`: the month-preset/limit-based read
  (`clampSuperchargerMonths`, `defaultSuperchargerMonths`, `superchargerReadLimit`, the in-memory
  `ChargeStartDateTime >= since` filter) is replaced by a `?start=&end=` date-range parser
  (`parseSuperchargerRange`, mirroring `parseHistoryRange`) and a single bounded
  `ListSessionsByVehicleBetween` call. The port returns sessions oldest-first
  (`ChargeStopDateTime` ASC); the handler reverses the slice once so the sessions table keeps
  displaying newest-first (unchanged UX) — see design.md D3.
- **CHANGED** — `internal/gateway/templates/fragments/supercharger_vm.go`: `SuperchargerStatsView`
  drops `Months int` and changes `Presets` from `[]int` to `[]fragments.RangePreset` (the type
  `RangePreset` already exists, added by `RM8-gateway-history-date-range` — reused verbatim, no
  new type). `SuperchargerRowVM` drops `CountryCode` and `BillingType`.
- **CHANGED** — `internal/gateway/templates/fragments/supercharger_stats.templ`: the month
  selector emits absolute `?start=&end=` hrefs (mirroring `historyDaysSelector` exactly) instead
  of `?months=N`; the sessions table drops the Country and Billing Type columns; the selector is
  conditionally rendered only when `Presets != nil` (mirrors `DashboardHistoryContent`'s
  malformed-request-hides-the-selector behavior).
- **CHANGED** — `internal/gateway/i18n/catalog.go`: `KeySuperchargerCountry` and
  `KeySuperchargerBillingType` are removed (both the `Key` constant and the `ES`/`EN` catalog
  entry). `KeySuperchargerMonthsPreset` ("%d meses" / "%d months") is **reused unchanged** — the
  preset buttons still read "3/6/12 months," only their underlying `hx-get` href changes.
- **CHANGED** — `internal/gateway/handlers/supercharger_test.go`: the fake reader is rewritten
  against `charging.SessionReader`'s contract (returns `ChargeStopDateTime` ASC; a session whose
  `TeslaID` does not equal the requested `teslaID`, including `nil`, is never returned — mirrors
  the real port's SQL `WHERE tesla_id = @tesla_id` semantics, tier 1 design.md D6); every existing
  test is updated for the new types/signatures per design.md's Test Contract.
- **CHANGED** (leader-owned, outside `internal/`) — `cmd/web/main.go`: the `gateway.Deps`
  construction swaps `SuperchargerReader: telemetry.NewSuperchargerReader(pool)` for
  `charging.NewSessionReader(pool)`. The two unrelated `telemetry.NewSuperchargerReader(pool)`
  calls inside `analytics.NewReader(...)`/`analytics.NewRecalculator(...)` are untouched — they
  are that module's own dependency, not the gateway's.
- **CHANGED** (leader-owned/explicitly-granted, outside `internal/gateway/`) —
  `internal/gateway/AGENTS.md` §"HTTP date-filter convention" states the window cap as
  per-endpoint (history 90 days / supercharger-stats 400 days, set by each table's row density)
  instead of a single flat 90; `ai/architecture.md` §"Concrete patterns already in use" gains a
  pointer to that section as the full handler contract; the root `README.md` documents the
  convention and its per-endpoint caps (roadmap Decision 7, owner request).

## Non-Goals

- No change to `internal/charging` (tier 1 already shipped the port this tier consumes).
- No change to `internal/telemetry`, the dashboard tiles, or the dashboard history charts
  (roadmap Decision 2 — tracked as a separate backlog item, not this change).
- No database migration, index, column, or constraint (roadmap Decision 1 — this is what keeps
  the `database` design gate untripped).
- No "browser-local today" / `browser_tz`-cookie handling for this endpoint (design.md D9a —
  `today` is plain UTC here; see design.md for the rationale). The endpoint DOES still reject an
  `end` later than today (design.md D9b), at UTC today rather than history's browser yesterday.
