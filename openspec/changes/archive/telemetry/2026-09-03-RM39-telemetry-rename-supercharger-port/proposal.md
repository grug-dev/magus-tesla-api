Source: MAG-31 — https://linear.app/magus-monitor/issue/MAG-31/analyze-scheme-per-module
Roadmap: openspec/roadmaps/RM39-schema-per-module.md
Tier: 5 of 5 (last tier — tiers 1 `account`, 2 `analytics`, 3 `charging`, 3b `analytics`
watermark vocabulary and 4 `telemetry` are archived; the stopper gate was **waived** by the
owner on 2026-09-03, roadmap D24)

## Why

Roadmap D5c renamed telemetry's hand-written domain type and its sqlc-generated database
model from `SuperchargerSession` to `SuperchargerHistory` in tier 4, in the same migration
that renamed the underlying table `supercharger_sessions` → `supercharger_history` and moved
it into the `telemetry` Postgres schema. Tier 4 deliberately left telemetry's **public port**
on the old vocabulary: the exported interface `SuperchargerReader`, its four
`SuperchargerSessions*` methods, the constructor `NewSuperchargerReader`, and the unexported
`superchargerReader` / `rowToSuperchargerSession` / `upsertSuperchargerSession` helpers. Tier
4's own `AGENTS.md` note calls this a "deliberate half-state... do not fix this" — a port
still named `SuperchargerReader` that now returns `[]SuperchargerHistory`. This change
finishes D5c by renaming that surface so the Go vocabulary matches the table.

**Why this was split from tier 4.** The port's cross-module footprint was measured at 88
references outside `internal/telemetry`, across `charging`, `analytics`, `gateway`, `app` and
both `cmd/` binaries — enough to make tier 4, already the roadmap's largest, unreviewable if
the port rename were folded in. Splitting it out is ordering, not an external blocker
(roadmap tiers table: "Queued behind tier 4").

**A finding this change adds to the roadmap: the 88-reference figure is a measurement
artifact, not the real blast radius (see design.md D1).** `internal/telemetry.SuperchargerReader`
happens to share a name with an entirely unrelated field,
`internal/gateway.Deps.SuperchargerReader`, whose type is `charging.SessionReader` — the
gateway's own dependency on `internal/charging`, wired from `cmd/web/main.go`. The gateway
cannot import `internal/telemetry` at all (`ai/architecture.md` §"Exception: the gateway may
not depend on `telemetry` at all", enforced by `make boundary-guard` with zero escape
hatches), so every "SuperchargerReader" hit inside `internal/gateway/**` — `gateway.go`,
`handlers/handlers.go`, `handlers/supercharger.go`, `handlers/supercharger_test.go`,
`handlers/history_test.go`, and `cmd/web/main.go`'s one wiring line — is this unrelated,
permanent identifier. It is untouched by this change and must stay untouched: renaming it
would itself introduce a boundary-adjacent naming confusion the project does not want.
Separately, `internal/analytics` migrated its `Recalculator`/`Reader` off
`telemetry.SuperchargerReader` onto `charging.SuperchargerSessionAnalyticsReader` in an
earlier change (`db_integration_test.go`'s own comment: "retypes from
telemetry.SuperchargerReader to charging.SuperchargerSessionAnalyticsReader ... telemetry
.NewSuperchargerReader(pool) no longer satisfies NewRecalculator's ... signature"); its
remaining "SuperchargerReader" hits are historical/comparative prose in comments, not live
type references. `internal/charging/charging.go` imports no `internal/telemetry` package at
all — its 3 measured hits are 2 comments about the gateway's own `Deps.SuperchargerReader`
field and 1 (plus one the coarse measurement missed) genuine prose mention of telemetry's
port, both comment-only. The real, compiler-verified, cross-module surface is confined to
`internal/app` (`app.go`, `processor.go`, `processor_test.go`) and `cmd/poller/main.go` — see
design.md D1 for the file-by-file accounting.

## What Changes

- **`internal/telemetry/telemetry.go`** — rename the exported port interface
  `SuperchargerReader` → `SuperchargerHistoryReader`; rename its four methods
  `SuperchargerSessionsBy{Account,Vehicle,VehicleBetween,VehicleUpdatedSince}` →
  `SuperchargerHistoryBy{Account,Vehicle,VehicleBetween,VehicleUpdatedSince}` (byte-identical
  to the sqlc query names tier 4 already produced — see design.md D2 for the naming
  rationale); rename the constructor `NewSuperchargerReader` → `NewSuperchargerHistoryReader`.
- **`internal/telemetry/reader.go`** — rename the unexported implementation
  `superchargerReader` → `superchargerHistoryReader` and its internal constructor helper
  `newSuperchargerReaderImpl` → `newSuperchargerHistoryReaderImpl`; update all four method
  receivers and the compile-time interface assertion.
- **`internal/telemetry/mapping.go`** — rename the mapper `rowToSuperchargerSession` →
  `rowToSuperchargerHistory`.
- **`internal/telemetry/service.go`** — rename the write-seam interface method and `dbStore`
  implementation `upsertSuperchargerSession` → `upsertSuperchargerHistory`.
- **`internal/telemetry`'s own tests** — `reader_test.go`, `service_test.go`,
  `db_supercharger_integration_test.go`, `db_supercharger_between_integration_test.go`,
  `db_supercharger_battery_pct_integration_test.go`, `run_writer.go`'s comments — follow every
  renamed identifier. No assertion or expected value changes; see design.md's Test Contract.
- **`internal/telemetry/db/query.sql`** (hand-authored source) and the regenerated
  `internal/telemetry/db/query.sql.go`** — update two prose doc-comments that name
  `SuperchargerReader.SuperchargerSessionsByVehicleBetween` /
  `...ByVehicleUpdatedSince`, then run `sqlc generate`. **No SQL, no schema, no generated
  type or query-function name changes** — sqlc's `gen.go.rename` only affects generated
  struct names, not comment text or hand-written query names, both of which are already
  correct from tier 4 (design.md D3 explains why the two hits here are comment-only).
- **`internal/app/app.go`, `internal/app/processor.go`, `internal/app/processor_test.go`** —
  update the real, compiler-checked consumer: the constructor parameter and struct field
  typed `telemetry.SuperchargerReader` become `telemetry.SuperchargerHistoryReader`; the one
  call site `p.superchargerReader.SuperchargerSessionsByAccount(...)` becomes
  `.SuperchargerHistoryByAccount(...)`; the fake in `processor_test.go` implementing the
  interface is renamed method-for-method and its compile-time assertion updated. Local
  variable/field names (`superchargerReader`) are renamed to `superchargerHistoryReader` for
  vocabulary consistency — not compiler-required, but the same reasoning D5c already applied
  to the port itself.
- **`cmd/poller/main.go`** — update the one real wiring line,
  `telemetry.NewSuperchargerReader(pool)` → `telemetry.NewSuperchargerHistoryReader(pool)`,
  and its local variable name and comment.
- **Comment-only accuracy fixes (no compiled-code change)** — `internal/charging/charging.go`
  (the genuine prose mentions of `telemetry.SuperchargerReader` /
  `telemetry.SuperchargerSessionsByVehicleBetween`, NOT the gateway's own
  `Deps.SuperchargerReader` field, which stays untouched), and `internal/analytics/analytics.go`,
  `internal/analytics/gap_writer.go`, `internal/analytics/db_integration_test.go` (historical/
  comparative comments only). See design.md D1 for the exact line list.
- **Docs** — `internal/telemetry/AGENTS.md` (the tier-4 note that pins this exact scope, plus
  the port's method list), and the KB under `kkpa/context/` if it names the port (grep-
  verified list in tasks.md).

**Breaking?** Not at runtime and not for any external behavior — every renamed method keeps
its exact signature, predicates, ordering and returned data; this is a pure identifier
rename. It **is** a Go compile-time break for the small, real set of call sites named above
(`internal/app`, `cmd/poller/main.go`) until they are updated in the same change — which they
are, atomically (design.md's Task Contract explains why this cannot be split across separate
compile units). No database object, migration, or SQL changes.

**Affected modules:** `internal/telemetry` (owner). `internal/app` and `cmd/poller` are
touched for real, compiler-checked consumer updates. `internal/charging` and
`internal/analytics` are touched in comments only. `internal/gateway` and `cmd/web` are
**untouched** — their `SuperchargerReader` identifier names a different, `charging`-backed
type and must not be renamed (design.md D1).

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `telemetry`: the existing "Supercharger History Table Renamed" requirement (added by tier
  4) is extended — its "public port stays half-renamed" clause is superseded: the port itself
  now matches the table.

## Impact

- `internal/telemetry` — 4 non-test files renaming the port/constructor/impl/mapper/write-seam
  (`telemetry.go`, `reader.go`, `mapping.go`, `service.go`), plus `run_writer.go`'s comment; 5
  `_test.go` files following those identifiers (no expected-value changes); 2 comment lines in
  `db/query.sql` + regenerated `query.sql.go`; `AGENTS.md`.
- `internal/app` — `app.go`, `processor.go`, `processor_test.go` (real compiler-checked type
  and call-site updates).
- `cmd/poller` — `main.go` (one real wiring line + local names).
- `internal/charging`, `internal/analytics` — comment-only accuracy fixes, no compiled-code
  change, no test-behavior change.
- `internal/gateway`, `cmd/web` — **no changes**. Their same-named `SuperchargerReader`
  identifier is a different, unrelated, `charging`-backed symbol (design.md D1).

**Read paths affected** (per `openspec/config.yaml`'s performance rule): none. No SQL, no
migration, no index, no query predicate or ordering changes — every read path keeps its exact
plan. This is a Go-only identifier rename, fully verified by `go build ./...` and
`go vet ./...` (which compile `_test.go` files too).

## Design gate

`openspec/config.yaml`'s `database` design gate does not apply: this change touches no
database object (table, column, index, constraint, view, migration). design.md states this
explicitly rather than omitting the section (per this dispatch's instructions) and needs no
owner confirmation before Apply on that account.
