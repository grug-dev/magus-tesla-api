Source: MAG-31 — https://linear.app/magus-monitor/issue/MAG-31/analyze-scheme-per-module
Roadmap: openspec/roadmaps/RM39-schema-per-module.md
Tier: 3b of 5 (`account` and `analytics`'s schema move are archived; `charging`'s schema
move + rename, tier 3, is archived; this tier is the analytics-owned hand-off tier 3
created — roadmap D8 CORRECTED / D15 — because `vehicle_metric_watermarks` is analytics'
table, not charging's; the stopper gate — an owner-run `make db-reset` — sits immediately
after this tier; tier 4 is `RM39-telemetry-move-to-own-schema`, blocked on a separate
boundary ticket, D6; tier 5 is `RM39-telemetry-rename-supercharger-port`, blocked behind
tier 4)

## Why

Tier 3 (`RM39-charging-move-to-own-schema`, archived) renamed `internal/charging`'s
Supercharger-session table from `charge_sessions` to `supercharger_sessions` (roadmap
D5b), inside its own new `charging` schema. `internal/analytics` owns a separate table,
`vehicle_metric_watermarks`, whose `source` column is a closed 3-value vocabulary naming
the physical table each of `Recalculator.Reconcile`'s three independent cursors tracks.
That vocabulary still reads `('vehicle_snapshots', 'charge_sessions',
'manual_charge_entries')` — `'charge_sessions'` now names a table that no longer exists
under that name. Left alone, the label silently drifts from the table it is supposed to
document, and `internal/analytics/recalculate.go`'s `sourceChargeSessions = "charge_sessions"`
constant — the literal `Reconcile` reads and writes its cursor by — would keep querying and
writing the stale string forever, with nothing in the database or the Go type system to
catch the mismatch (`source` is a free-standing `TEXT` label, not a foreign key).

**Why this is analytics' change, not charging's (roadmap D15, owner-confirmed twice).**
`vehicle_metric_watermarks` moved into the `analytics` Postgres schema in tier 2
(`RM39-analytics-move-to-own-schema`, archived). A migration mutating another module's
table is a cross-module database write regardless of whether it runs as deploy-time DDL
or at runtime (`ai/architecture.md` §2, "no cross-module database leaks" — the rule does
not carve out an exception for migration files). This is not a new pattern: migration
`20260828000001_migrate_vehicle_metric_watermarks_source.sql` already performed the exact
mirror-image operation once — triggered by a charging-side change
(`RM31-analytics-read-sessions-from-charging`), yet it lives in
`internal/analytics/db/migrations/`, not charging's or telemetry's directory, precisely
because the table it touches is analytics'. This change copies that precedent; it does
not invent a new one.

**No schema move here.** `internal/analytics` already got its own schema in tier 2. This
change is vocabulary-only: it rewrites a CHECK constraint's accepted values and the rows
that violate the new set, plus the one Go constant that names the retiring value.

**The reused-string question this change must answer, not hand-wave.** The new vocabulary
readmits `'supercharger_sessions'` as a legal `source` value — the exact string this
column held *before* `20260828000001` ever ran, back when it named `internal/telemetry`'s
table. `design.md`'s "Reused-String Ambiguity" section answers directly: whether a stored
row can self-disambiguate the two eras (it cannot — the column is deliberately
unqualified, tier 2's own decision), whether the DELETE this migration performs makes
that harmless in practice (yes for data — the value has been *impossible* to insert since
`20260828000001` landed, so no row can currently hold it; no for documentation — a reader
of an old backup, a stale doc, or `git log` on the migration files genuinely faces two
eras), and what changes once roadmap tier 4 renames telemetry's table to
`supercharger_history` (the bare name becomes globally unique in the database catalog,
closing the lexical half of the ambiguity for good; the *semantic* half was already closed
by RM31, which redirected analytics' own Supercharger read to `internal/charging` and
never looks at telemetry's copy for this purpose).

## What Changes

- **Migration** — one new goose migration,
  `internal/analytics/db/migrations/20260902000004_migrate_vehicle_metric_watermarks_source_supercharger.sql`,
  mirroring `20260828000001`'s own shape (DROP CONSTRAINT → DELETE the rows the retiring
  value labels → ADD CONSTRAINT with the new vocabulary → refresh both `COMMENT ON`
  strings), with the roles reversed and every table reference schema-qualified
  (`analytics.vehicle_metric_watermarks` — the precedent migration predates tier 2 and
  names the table bare; this one must not). A real `-- +goose Down` reverses all four
  statements. No existing migration is edited (roadmap D1).
- **`internal/analytics/recalculate.go`** — the `sourceChargeSessions` constant is renamed
  to `sourceSuperchargerSessions` and its value changes from `"charge_sessions"` to
  `"supercharger_sessions"`, matching the new vocabulary (both the name and the value
  change — a stale identifier next to a fresh value would be worse than either alone).
- **Test files** — `internal/analytics/db_watermark_migration_integration_test.go`'s
  shared cleanup, which currently leaves the table at `20260828000001`'s own post-Up
  vocabulary after every run, is extended to also restore this change's migration on top
  of it, or every other DB-backed test in the package would see the wrong constraint after
  this file's test runs (see design.md's "Test file impact" — a real interaction this
  change's own migration introduces, not a pre-existing bug). Comments in
  `db_integration_test.go` that describe the watermark label as `charge_sessions` are
  updated to describe the current vocabulary; the tests' own logic (which reads the
  renamed Go constant symbolically, never the bare string) needs no behavioral change.
  D9/D17 sweep (this module and every other module's `_test.go` files, by table name and
  by the `'charge_sessions'`/`'supercharger_sessions'` string literal) confirms no other
  module's test hand-writes SQL against `vehicle_metric_watermarks` or holds this literal
  as a watermark source value — see design.md.
- **A new DB-integration round-trip test** for this change's own migration, mirroring
  `TestMigration_WatermarkSourceVocabulary`'s shape exactly (`ApplyVersion` against this
  migration's own version, Given/When/Then, Down round-trip) — design.md's Test Contract
  T1 gives its pinned values.
- **Docs** — `internal/analytics/AGENTS.md`'s "Data ownership" section (the line naming
  the vocabulary as `'charge_sessions'`) and `kkpa/context/entities/vehicle-metrics/guide.md`
  (five lines naming the current vocabulary or the Supercharger table by its retiring
  name) are updated to the new vocabulary.

**Breaking?** Not for any Go consumer — `sourceChargeSessions`/`sourceSuperchargerSessions`
is unexported; no public `Reader`/`Recalculator`/`GapWriter` method name or signature
changes. Database-level: additive and reversible (the migration's Down restores the exact
prior CHECK and comments), but the DELETE against affected rows is real, accepted data
loss for a bookkeeping cursor — see design.md's "Rejected Alternatives" for why a rewrite
is worse. A fresh `Reconcile` for every affected vehicle re-derives the deleted cursor from
epoch on its next nightly run — self-healing, one redundant backfill pass, no lost
`vehicle_metrics`/`charge_gaps` rows (only the cursor bookkeeping row is deleted, never the
derived data it produced).

**Affected modules:** `internal/analytics` only. No other module imports
`vehicle_metric_watermarks`, `analyticsdb`, or the `sourceChargeSessions` constant — this
table has never been readable outside `internal/analytics` (`ai/architecture.md` §2).
`internal/charging`, `internal/telemetry`, `internal/gateway`, `internal/app`,
`internal/account` are untouched.

**Read paths affected** (per `openspec/config.yaml`'s performance rule): the sole read
this table serves, `Recalculator.watermark`'s single-row lookup (`WHERE account_id = $1
AND tesla_id = $2 AND source = $3`), is unaffected in cost — the CHECK constraint rewrite
touches no index, and the existing `vehicle_metric_watermarks_account_tesla_source_unique`
UNIQUE index continues to serve that lookup exactly as before (see design.md's index
plan). No new index is added and none is needed. This is a nightly-batch write path
(`Reconcile`), not a dashboard read path — the read-heavy performance profile's "reads are
mandatory-fast" bar does not apply here in the first place, and this change does not move
it either way.

## Capabilities

### Modified Capabilities

- `analytics`: the existing requirement "Incremental Recompute Via An Analytics-Owned
  Watermark" gains one scenario describing the one-time consequence of retiring a source's
  vocabulary label — an existing vehicle's affected cursor is reset to epoch, and its next
  reconciliation re-backfills that source's full history in one pass. The requirement's
  general contract (three independent per-source cursors, no-prior-cursor-means-epoch) is
  unchanged; this scenario documents an instance of that same, already-specified rule
  triggered by this migration, not a new rule.

## Impact

- `internal/analytics` — one new migration, one renamed+revalued Go constant, one new
  DB-integration test file (or a new test function in the existing migration-test file —
  see tasks.md), one existing test file's cleanup sequence fixed, a handful of stale
  comments corrected, `AGENTS.md` updated.
- `kkpa/context/entities/vehicle-metrics/guide.md` — five lines naming the retiring
  vocabulary/table-name pairing are corrected (KB sync rule, `CLAUDE.md` "Docs track
  structural change").
- No other module, no `sqlc.yaml` change (a CHECK constraint carries no column type sqlc
  needs to regenerate against), no new index.
