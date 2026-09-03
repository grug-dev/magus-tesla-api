# Tasks — RM39-analytics-fix-watermark-vocabulary (roadmap tier 3b)

Design gate (`database`) must be confirmed by the owner before any task below starts —
see `design.md` §1 "Design gate: TRIPPED."

Legend: `[ ]` pending · `[x]` done. Update live as work proceeds (`openspec/config.yaml`
tasks rule).

## Wave 1 — migration + Go constant (independent of each other, no shared files)

- [x] **1.1 Write the migration.** New file
  `internal/analytics/db/migrations/20260902000004_migrate_vehicle_metric_watermarks_source_supercharger.sql`,
  exact content specified in `design.md` §3 — copy verbatim, including comments. Verify the
  chosen timestamp is still unused by re-running `find internal -path '*/db/migrations/*.sql'
  | xargs -n1 basename | grep -oE '^[0-9]{14}' | sort | uniq -d` (should report nothing new)
  immediately before creating the file, since other tiers may land migrations concurrently.
  No dependency on task 1.2.

- [x] **1.2 Rename the Go constant.** `internal/analytics/recalculate.go`: rename
  `sourceChargeSessions` to `sourceSuperchargerSessions` and change its value from
  `"charge_sessions"` to `"supercharger_sessions"` (design.md §7). Update every call site
  in the same file (the two `r.watermark`/`r.advanceWatermark` calls and their `fmt.Errorf`
  format strings — `grep -n sourceChargeSessions internal/analytics/recalculate.go` to
  confirm all are caught). No dependency on task 1.1. Run `go build ./... && go vet
  ./... && gofmt -l internal/analytics/` after this task — a missed call site is a compile
  error, not a silent bug.

## Wave 2 — test fixes (depends on 1.2 for symbol references; independent of each other)

- [ ] **2.1 Fix `db_watermark_migration_integration_test.go`'s cross-migration cleanup.**
  Apply the exact fix in `design.md` §8: add
  `const superchargerVocabMigrationVersion int64 = 20260902000004` (match task 1.1's actual
  filename/version) alongside the existing `watermarkSourceMigrationVersion` constant, and
  extend the test's `t.Cleanup` to force-toggle that version (`ApplyVersion(..., false)`
  then `ApplyVersion(..., true)`) after the existing `20260828000001` re-application step.
  Do NOT change any of this file's other assertions or its pinned `"charge_sessions"`/
  `"supercharger_sessions"` string literals — those test `20260828000001`'s own historic
  Up/Down and must stay exactly as they are (design.md §8, D1). Depends on 1.1 (needs the
  real migration version number to exist).

- [ ] **2.2 Update stale comments in `db_integration_test.go`.** Three regions (currently
  around lines 1059, 1233, 1379 — re-locate by searching for `sourceChargeSessions` and
  the phrase "charge_sessions watermark") reference the retiring label in prose. Update
  the comment text to describe the current vocabulary (design.md §8 gives example wording
  for the first one). The assertions themselves (`fetchWatermark(...,
  sourceSuperchargerSessions)` after the rename) need no logic change — this is a
  comment-only task, verify with `gofmt -l` and a visual diff, not a test run. Depends on
  1.2 (the symbol must already be renamed for the surrounding code to compile).

- [ ] **2.3 Write the migration round-trip test (T1).** Either append a new `Test...`
  function to `db_watermark_migration_integration_test.go` or create a new file (e.g.
  `db_watermark_supercharger_migration_integration_test.go`) — implementer's choice, both
  reuse the existing file's helpers unchanged (`seedWatermarkRow`, `fetchWatermarkUpdatedAt`,
  `countWatermarkRows`, `insertWatermarkSource`, `isCheckViolation`, `withSearchPath`,
  `newAnalyticsMigrationProvider`). Implement exactly `design.md` §9 T1's Given/When/Then,
  using the pinned account/tesla IDs and timestamps given there — do not derive them by
  running the migration first and recording what came out (`ai/go-conventions.md`
  §Testing "Authoring order"). This is a **DB-integration test** (final wave per
  `ai/go-conventions.md` — it cannot compile meaningfully before the migration exists, and
  cannot be verified as correct before then either). Depends on 1.1 (the migration must
  exist) and 2.1 (this test and `db_watermark_migration_integration_test.go`'s existing
  test both toggle shared package-level DB state across the same package — write 2.1's fix
  first so the two tests do not fight over the constraint's final state).

## Wave 3 — docs (independent of every other task; may start any time after 1.1/1.2 land, since it documents their outcome)

- [ ] **3.1 Update `internal/analytics/AGENTS.md`.** "Data ownership" section, the line
  currently reading `vehicle_metric_watermarks.source`'s stored string values
  (`'vehicle_snapshots'`, `'charge_sessions'`, `'manual_charge_entries'`)` — change
  `'charge_sessions'` to `'supercharger_sessions'` and add a one-clause note that the value
  is reused from before RM31 and now names `charging`'s table (point at this change's
  design.md §6 for the full reused-string rationale rather than duplicating it).

- [ ] **3.2 Update `kkpa/context/entities/vehicle-metrics/guide.md`.** Five lines
  currently name the vocabulary as `'charge_sessions'` or describe the Supercharger table
  by its retiring name in a way that will read as stale once this change lands (found via
  `grep -n "charge_sessions" kkpa/context/entities/vehicle-metrics/guide.md` — re-run to
  get exact current line numbers, since RM39 tier 3's own archival may have already
  touched some of them). Update each to the current vocabulary/table name. This is the
  `CLAUDE.md` "Docs track structural change" KB-sync requirement, not optional cleanup.

- [ ] **3.3 Sweep for any other stale reference.** `grep -rln "charge_sessions" --include='*.md'
  internal/ kkpa/context/ docs/ ai/` (outside `openspec/changes/archive/`, which is
  historical and frozen by convention) to catch anything 3.1/3.2 missed. Report findings
  even if the list is empty — an empty result is itself the confirmation this task exists
  to produce.

## Explicitly out of scope (do not implement here)

- Any change to `internal/charging/` — that module's rename is already archived (tier 3).
  This change only reads `internal/charging`'s public port names in comments/docs; it
  never edits that module's files.
- Any change to `sqlc.yaml` or a `sqlc generate` run — this migration changes no column
  type (design.md §10).
- The stopper gate (`make db-reset`) that sits immediately after this tier in the roadmap —
  owner-run, not a task of this change (see `openspec/roadmaps/RM39-schema-per-module.md`
  "Stopper gate").
