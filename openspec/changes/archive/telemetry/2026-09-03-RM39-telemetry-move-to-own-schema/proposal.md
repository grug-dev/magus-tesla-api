Source: MAG-31 — https://linear.app/magus-monitor/issue/MAG-31/analyze-scheme-per-module
Roadmap: openspec/roadmaps/RM39-schema-per-module.md
Tier: 4 of 5 (tiers 1 `account`, 2 `analytics`, 3 `charging` and 3b `analytics` watermark
vocabulary are archived; the stopper gate G1 was **waived** by the owner on 2026-09-03,
roadmap D24; tier 5 is `RM39-telemetry-rename-supercharger-port`, queued behind this one)

## Why

MAG-31 asks that every `internal/` module owning persistence get its own PostgreSQL schema,
named after the module, so the modular-monolith boundary — today enforced only by Go import
guards (`ai/architecture.md` §2) and convention — becomes visible in the database catalog and
checkable at codegen time. Three tiers have already proved the pattern: one additive goose
migration, schema-qualified queries, `gen.go.rename` entries that freeze the Go surface, and a
`models.go` diff as the mandatory verification step. This tier repeats it for
`internal/telemetry` — the largest of the four, four tables — and, like tier 3, also carries a
table rename.

**Why this tier was blocked and why it no longer is.** Roadmap D6 parked tier 4 behind "a
separate boundary ticket" covering `internal/charging`'s historic migration
`20260823000001_add_charge_sessions.sql`, which reads `public.supercharger_sessions` inside a
PL/pgSQL `DO` block — a genuine cross-module database read that neither `make boundary-guard`
(Go imports only) nor sqlc (a PL/pgSQL body is an opaque string) can see. On 2026-09-03 the
owner established that **no such ticket ever existed** (MAG-31's only Linear `blockedBy` is
MAG-41, which was the unrelated *gateway → telemetry Go import* violation, fixed by RM38/RM40
and Done). Roadmap **D25** therefore supersedes D6 and folds the residue into this tier:
**one comment in that migration becomes false, and it is corrected as a comment only** —
roadmap D1 still forbids touching a shipped migration's SQL.

**Why the rename, not just the schema move (roadmap D5a).** Two tables in this repository are
called `*_sessions` and one mirrors the other. Tier 3 already renamed
`charging.charge_sessions` → `charging.supercharger_sessions` (roadmap D5b), deliberately
reusing the name this tier frees. `telemetry.supercharger_sessions` now becomes
`telemetry.supercharger_history`. `_history` rather than `_snapshots` is a correctness point,
not taste: `vehicle_snapshots` is append-only, one row per poll, whereas this table is
**upserted** — Tesla settles fees after a session ends, so a row is never "done" on first
insert. Calling both "snapshots" would teach an agent a false rule and invite a query that
double-counts. `_history` also names the Fleet endpoint the rows come from
(`GET /api/1/dx/charging/history`).

**Why the public port is deliberately left half-renamed.** Roadmap D5c renames the Go names
that follow the table, but the public port `SuperchargerReader` — its four
`SuperchargerSessions*` methods, `NewSuperchargerReader`, and the internal
`superchargerReader` / `rowToSuperchargerSession` / `upsertSuperchargerSession` helpers — has
**88 references outside `internal/telemetry`**, in `charging`, `analytics`, `gateway`, `app`
and both `cmd/` binaries. Folding that into this tier would make the roadmap's already-largest
tier unreviewable, so it is roadmap tier 5. This change therefore ends in an intentional
half-state: a port named `SuperchargerReader`, with `SuperchargerSessions*` methods, returning
`[]SuperchargerHistory`. That is the designed outcome, not an oversight — do not "tidy" it.

**Two hard-won findings this tier inherits.** Roadmap **D18**: a rename tier's completeness
criterion is the **catalog**, never a hand-written object list — tier 3 shipped, passed review
and archived with five constraints still misnamed, because Postgres auto-names one CHECK per
inline column constraint and those names exist only in `pg_constraint`, where no grep can
reach them. The owner has already queried a migrated database for this tier: **9 objects**
(7 constraints + 2 indexes). Roadmap **D9/D13/D17**: raw SQL in `_test.go` files is invisible
to sqlc and to `go vet`, so **no assistant-runnable signal catches a miss** — only the owner's
suite. Re-measured here with the quote-agnostic pattern: **41 statements across 7 files** in
`internal/telemetry`, plus — because a *rename* escapes the module sandbox where a pure move
would not — statements in `internal/charging` and `internal/analytics`.

**One finding this change adds to the roadmap's own list (design.md D11).**
`internal/charging/db_backfill_integration_test.go` replays the shipped backfill statement and
today rewrites exactly **one** name forward (the `INSERT` target), with an explicit comment
saying the statement's `FROM supercharger_sessions` "is already correct and must be left
alone" until this tier lands. After this tier it must rewrite **three**: the `INSERT` target,
the `to_regclass('public.supercharger_sessions')` **guard**, and the `FROM` source. Missing
the guard does not raise an error — the `DO` block returns early and the backfill silently
inserts nothing, so A1/A2 fail on empty assertions rather than on a missing relation. The
roadmap's tier-4 row named only "reconcile charging's backfill tests A1/A2"; this is the
precise shape of that reconciliation.

## What Changes

- **Migration** — one new goose migration,
  `internal/telemetry/db/migrations/20260903000001_move_telemetry_to_own_schema.sql`: creates
  the `telemetry` schema, moves `vehicle_snapshots`, `supercharger_sessions`, `poll_attempts`
  and `poll_runs` into it via `ALTER TABLE … SET SCHEMA`, renames
  `telemetry.supercharger_sessions` → `telemetry.supercharger_history`, then renames all
  **9** catalog objects still carrying the old table name (7 constraints, 2 standalone
  indexes), then refreshes the table/column `COMMENT ON` text sqlc copies into `models.go`.
  `-- +goose Down` reverses in the exact opposite order and ends `DROP SCHEMA IF EXISTS
  telemetry`. No existing migration's SQL is edited (roadmap D1).
- **`db/query.sql`** — all **15** table references become schema-qualified (roadmap D2, forced
  by sqlc, not chosen). The **5** query names embedding the old table name are renamed:
  `UpsertSuperchargerSession` → `UpsertSuperchargerHistory`, and
  `SuperchargerSessionsBy{Account,Vehicle,VehicleBetween,VehicleUpdatedSince}` →
  `SuperchargerHistoryBy{Account,Vehicle,VehicleBetween,VehicleUpdatedSince}`; their generated
  `*Params` types follow automatically.
- **`sqlc.yaml`** — the telemetry entry's `gen.go` block gains a `rename:` map (it has none
  today). `telemetry_poll_attempt`, `telemetry_poll_run` and `telemetry_vehicle_snapshot`
  preserve `PollAttempt`, `PollRun`, `VehicleSnapshot` (roadmap D3);
  `telemetry_supercharger_history` is a **new** key mapping to `SuperchargerHistory` (roadmap
  D5c — the deliberate exception). A wrong key form fails **silently at exit 0**, so diffing
  `models.go` is a mandatory task, not a nicety.
- **Go inside `internal/telemetry`** — the hand-written domain type
  `SuperchargerSession` (`telemetry.go`) becomes `SuperchargerHistory`; it is not
  sqlc-generated, so no config touches it. `reader.go`, `mapping.go` and `service.go` follow
  the two renamed types and the five renamed query functions. **`SuperchargerReader`, its four
  method names, `NewSuperchargerReader`, `superchargerReader`, `rowToSuperchargerSession` and
  `upsertSuperchargerSession` are unchanged** — tier 5.
- **Test files (roadmap D9/D13/D17)** — 41 hand-written SQL statements across 7 `_test.go`
  files in `internal/telemetry` are schema- (and, for the renamed table, name-) qualified,
  plus their `telemetry.SuperchargerSession` Go type references. **Outside the module**: 4 SQL
  statements + 3 Go type references in `internal/analytics/db_integration_test.go`, 4 Go type
  references in `internal/app/processor_test.go`, and 3 SQL statements + the `runBackfill`
  rewrite in `internal/charging/db_backfill_integration_test.go`.
- **The D25 comment correction** — `internal/charging/db/migrations/20260823000001_add_charge_sessions.sql`
  line ~213 claims "In a real database the guard always passes: MIGRATIONS_DIRS runs telemetry
  before charging." After this tier `to_regclass('public.supercharger_sessions')` is NULL
  forever and the backfill permanently skips. **Comment only.**
- **Docs** — `internal/telemetry/AGENTS.md`, the root `README.md` schema/table table (which
  currently carries a "Two different tables share the base name `supercharger_sessions`" note
  this tier makes obsolete), `ai/go-conventions.md`, `docs/battery-consumed-graph.md`, and the
  KB under `kkpa/context/` — chiefly `architecture/telemetry-ingest-only.md`, whose header
  banner explicitly parks the `supercharger_sessions` → `supercharger_history` half of the
  rename on "a separate, blocked boundary ticket (roadmap D6)". D25 retired that block; this
  tier resolves the banner.

**Breaking?** Not for any Go consumer that goes through this module's public ports: every
exported `Collector`, `Reader`, `SuperchargerReader` and `RunWriter` **method name and
signature is unchanged**. One exported *type* changes name — `telemetry.SuperchargerSession`
→ `telemetry.SuperchargerHistory` — which is a compile error at its 7 real external references
(all in `_test.go` files, all listed and owned as sub-tasks) and is caught by `go vet`, which
compiles test files. Database-level: additive and reversible (catalog-only `SET SCHEMA` /
`RENAME` statements), but it retires the `supercharger_sessions` table, index and constraint
names in the `telemetry` namespace — a consumer with a raw SQL dependency breaks, which is
precisely why the test sweep crosses module lines here.

**Affected modules:** `internal/telemetry` (owner). `internal/charging`, `internal/analytics`
and `internal/app` are touched only in `_test.go` files and comments, plus one comment-only
edit inside charging's historic migration — all recorded as **leader-owned** sub-tasks in
`tasks.md`, none of them inside this worker's sandbox. `internal/gateway` is untouched: it may
not import `internal/telemetry` at all (`ai/architecture.md` §"Exception", enforced by
`make boundary-guard`).

## Capabilities

### New Capabilities

- `telemetry`: two new requirements — "Module-Scoped Database Schema", stating that all four
  of this module's tables live in a dedicated `telemetry` Postgres schema; and "Supercharger
  History Table Renamed", stating the `supercharger_sessions` → `supercharger_history` rename,
  its catalog-completeness criterion, and its Go-side consequences including the deliberately
  unchanged public port.

### Modified Capabilities

(none — the existing `telemetry` requirements describe capability *behavior*, which a
catalog-only namespacing and rename does not change; per `openspec/config.yaml`,
implementation HOW lives in `design.md`.)

## Impact

- `internal/telemetry` — one new migration; 15 `query.sql` table references schema-qualified;
  5 query names renamed; `sqlc.yaml` gains a 4-entry `rename:` block (3 preserving, 1 new);
  `make sqlc` regeneration verified against an expected, fully-accounted `models.go` diff
  (`SuperchargerSession` → `SuperchargerHistory` only); the hand-written domain type renamed;
  41 hand-written test SQL statements schema/name-qualified; `AGENTS.md` updated.
- `internal/charging` — `db_backfill_integration_test.go` (3 SQL statements + the two extra
  `runBackfill` name mappings + header comments), `testdb_test.go` (one comment),
  `charging.go` / `session_reader.go` (comments naming `telemetry.SuperchargerSession`), and
  the D25 comment-only correction in `db/migrations/20260823000001_add_charge_sessions.sql`.
- `internal/analytics` — `db_integration_test.go` only: 4 raw SQL statements seeding/reading
  telemetry's tables, and 3 `telemetry.SuperchargerSession` type references.
- `internal/app` — `processor_test.go` only: 4 `telemetry.SuperchargerSession` type
  references in a fake reader.
- Docs — root `README.md`, `ai/go-conventions.md`, `docs/battery-consumed-graph.md`,
  `kkpa/context/` (grep-verified list in `tasks.md`).

**Read paths affected** (per `openspec/config.yaml`'s performance rule): none in cost. Every
existing telemetry read path — `Reader.LatestSnapshotsByAccount`, `.SnapshotsByVehicleSince`,
`.SnapshotsByVehicleBetween`, `.SnapshotsByVehicleUpdatedSince`, `.SnapshotPrecedingDay`, and
all four `SuperchargerReader` methods — keeps its exact query shape, predicates and plan.
`ALTER TABLE … SET SCHEMA`, `… RENAME TO`, `ALTER INDEX … RENAME TO` and
`ALTER TABLE … RENAME CONSTRAINT` are catalog-only; every index keeps its OID, columns and
order. **No index is added and none is needed** — see `design.md`'s Index Plan, which states
that explicitly and justifies it against the read-heavy profile.
