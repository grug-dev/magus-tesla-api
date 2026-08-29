# Tasks — RM31-analytics-read-sessions-from-charging

Design gate: **tripped** — do not start Wave 1 until the owner has confirmed `design.md`.

Wave grouping follows `ai/go-conventions.md` §Testing's authoring order: pure/offline retypes
first (they compile against code that already exists), the migration file in parallel with
them (a disjoint, non-Go file), and every `DATABASE_URL`-gated integration test **last**,
because those tests cannot compile until both the migration and the retyped signatures exist.

## Wave 1 — migration + offline retypes (parallel-safe: disjoint files)

- [x] **1.1 — Migration: `vehicle_metric_watermarks.source` vocabulary change**
  (`depends_on`: none)
  Add `internal/analytics/db/migrations/20260828000001_migrate_vehicle_metric_watermarks_source.sql`
  with the exact `-- +goose Up` / `-- +goose Down` SQL specified in design.md §2e: drop the
  existing `vehicle_metric_watermarks_source_check` constraint, `DELETE` every
  `source = 'supercharger_sessions'` row (resetting that source's cursor to epoch — roadmap
  Decision 10, D7 — leaving `vehicle_snapshots` and `manual_charge_entries` rows for the same
  vehicles untouched), re-add the constraint with the new 3-value vocabulary, and refresh the
  table/column `COMMENT`s. No other file changes. No `make sqlc` regeneration needed — this
  migration touches no column type or shape sqlc's generated types depend on, only a CHECK
  constraint and existing row values.
  **Acceptance:** file matches design.md §2e verbatim (SQL, not paraphrased) — in particular a
  `DELETE FROM vehicle_metric_watermarks WHERE source = 'supercharger_sessions'`, NOT an
  `UPDATE`. The Down block restores the old CHECK constraint and original `COMMENT`s but does
  **NOT** attempt to resurrect deleted rows (design.md §2e's Down is `SELECT`-free schema-only
  restoration — the row loss is documented as irreversible-but-harmless, mirroring
  `20260822000002_reset_vehicle_metric_watermarks.sql`'s own Down).

- [x] **1.2 — Retype `consumed.go` + `consumed_test.go`** (`depends_on`: none)
  In `consumed.go`: retype `sumSuperchargerPctBetween`, `inferMissingChargingType`, and
  `deriveVehicleMetrics`'s `sessions` parameter from `[]telemetry.SuperchargerSession` to
  `[]charging.Session` (design.md §3 "consumed.go" — bodies unchanged, only the parameter
  type and doc comments referencing the old type). In `consumed_test.go`: retype every
  `telemetry.SuperchargerSession{...}` fixture passed as a `sessions` argument to
  `charging.Session{...}` (literal field-for-field swap — same field names/types, design.md
  §1a). Do not change any assertion value; the formulas are untouched by this tier.
  **Acceptance:** `go vet ./internal/analytics/...` compiles clean once Waves 1.2–1.4 all
  land (this task alone will not compile in isolation — that is expected, see design.md §3's
  opening note).

- [x] **1.3 — Retype `reader.go` + `reader_test.go`** (`depends_on`: none)
  In `reader.go`: retype the `reader.supercharger` field and `NewReader`'s `supercharger`
  parameter from `telemetry.SuperchargerReader` to
  `charging.SuperchargerSessionAnalyticsReader`; retype `sumSuperchargerKWh`'s `sessions`
  parameter to `[]charging.Session`; change `RecentEfficiency`'s call site from
  `SuperchargerSessionsByVehicle` to `ListSessionsByVehicle` (design.md §3 "reader.go"). In
  `reader_test.go`: retype `fakeSuperchargerReader` to implement
  `charging.SuperchargerSessionAnalyticsReader` (method names change; preserve every existing
  "must not be called from this path" panic on whichever methods `Reader` never calls) and
  retype every `sessions: []telemetry.SuperchargerSession{...}` fixture to
  `[]charging.Session{...}`.
  **Acceptance:** same compile note as 1.2. `RecentEfficiency`'s existing test assertions
  (efficiency values, `ok`/error cases) are unchanged — this is a pure type/call-site swap.

- [x] **1.4 — Retype `recalculate.go` + `recalculate_test.go`** (`depends_on`: none)
  In `recalculate.go`: retype the `recalculator.supercharger` field and `NewRecalculator`'s
  `supercharger` parameter to `charging.SuperchargerSessionAnalyticsReader`; change
  `Recalculate`'s call site from `SuperchargerSessionsByVehicleBetween` to
  `ListSessionsByVehicleBetween`; change `Reconcile`'s call site from
  `SuperchargerSessionsByVehicleUpdatedSince` to `ListSessionsByVehicleUpdatedSince`; **rename**
  the constant `sourceSuperchargerSessions` to `sourceChargeSessions` and change its value from
  `"supercharger_sessions"` to `"charge_sessions"` (design.md §3 "recalculate.go" — rename
  rationale: this module's own established "name the constant after the physical table"
  convention). Update all 4 usage sites of the renamed constant within this file. In
  `recalculate_test.go`: retype `fakeSuperchargerReader` identically to task 1.3's (this file
  has its own separate fake, per design.md §3 "Test files") and every fixture.
  **Acceptance:** same compile note as 1.2/1.3. Grep `internal/analytics/*.go` (excluding this
  task's own file) for `sourceSuperchargerSessions` after this task to confirm no stray
  reference survives outside `recalculate.go`/`recalculate_test.go`/`db_integration_test.go`
  (the last is task 2.2's scope).

## Wave 2 — DB-integration tests + docs (depends on ALL of Wave 1)

- [x] **2.1 — Update `internal/analytics/AGENTS.md`** (`depends_on`: 1.2, 1.3, 1.4)
  Update "Allowed / forbidden imports" §"May import": `internal/telemetry` bullet drops
  `SuperchargerReader (SuperchargerSessionsByVehicle)` and `telemetry.SuperchargerSession`
  (keep `telemetry.Reader`'s methods and `telemetry.Snapshot` unchanged); `internal/charging`
  bullet gains `SuperchargerSessionAnalyticsReader (ListSessionsByVehicleBetween,
  ListSessionsByVehicleUpdatedSince, ListSessionsByVehicle)` alongside the existing `Reader`/
  `Entry`. Update the "Public interface" and "Data ownership" prose referencing "the two
  charging-cost sources (`telemetry.SuperchargerReader` and `internal/charging`)" to name
  `charging.SuperchargerSessionAnalyticsReader` and `charging.Reader` (design.md §3
  "`internal/analytics/AGENTS.md`"). This is a docs-only change — no code file.
  **Acceptance:** no remaining reference to `telemetry.SuperchargerReader` or
  `telemetry.SuperchargerSession` anywhere in this file; `NewRecalculator`/`NewReader`
  constructor signatures documented here match the retyped Go signatures exactly.

- [x] **2.2 — Retarget `db_integration_test.go`'s Supercharger fixture seeding to `charge_sessions`**
  (`depends_on`: 1.1, 1.2, 1.3, 1.4)
  Retarget the direct-SQL seeding helper(s) that currently `INSERT`/`UPDATE` against
  `supercharger_sessions` to seed `charge_sessions` instead, using that table's actual column
  set (`internal/charging/db/migrations/20260823000001_add_charge_sessions.sql` is the schema
  reference — design.md §3 "Test files"). This remains a direct-SQL seed, per this module's
  own established D19 convention (`AGENTS.md` §Testing) — no public writer exists for
  constructing an arbitrary `charge_sessions` row with specific percentages in one call
  (`SessionWriter.MirrorSessions` cannot set percentages; `SessionVerifier.VerifySession`
  requires an existing row's ID). Update every `sourceSuperchargerSessions` reference in this
  file to `sourceChargeSessions`. Update every existing test's fixture/assertion that
  previously asserted against `supercharger_sessions` rows or the old watermark source label.
  **Add Test Contract T2 and T4** (design.md §5) as new or adapted tests in this file — T2
  ("`Reconcile` reads sessions through the `charging` port, not `telemetry`" — including the
  call-log assertion that the OLD method names are never invoked) and T4 (the nil-`TeslaID`
  non-regression case).
  **Acceptance:** no remaining `INSERT INTO supercharger_sessions` / reference to
  `supercharger_sessions` as a Supercharger-session fixture target in this file (a
  `telemetry.supercharger_sessions` row IS still legitimately seeded by Test Contract T3's own
  task, 2.3, as the "stale telemetry copy" side of that comparison — do not remove that
  capability from the shared seeding helpers, only the module's OWN Supercharger-session
  fixtures move to `charge_sessions`). Every assertion in this file matches design.md §5's T2/T4
  pinned values exactly — write assertions against those values, not against whatever the
  implementation happens to produce.

- [x] **2.3 — Add Test Contract T1 and T3 as DB-integration tests** (`depends_on`: 1.1, 1.2,
  1.3, 1.4, 2.2)
  Depends on 2.2 for its shared seeding helpers (retargeted to `charge_sessions`). **T1** (the
  migration DELETEs only the `supercharger_sessions` watermark, leaving its sibling sources
  untouched): a dedicated test — likely its own file, e.g.
  `db_watermark_migration_integration_test.go`, since it exercises the migration itself rather
  than `Recalculator`/`Reader` — asserting the pre/post migration state exactly as pinned in
  design.md §5 T1: seed one watermark row per source for the same vehicle, apply the migration,
  assert the `supercharger_sessions` row is GONE (not renamed), the `vehicle_snapshots` and
  `manual_charge_entries` rows are byte-identical to their pre-migration values, the CHECK
  rejects `'supercharger_sessions'` and accepts `'charge_sessions'`, `r.watermark(...,
  sourceChargeSessions)` returns the zero-value epoch for this vehicle, and a Down round-trip
  restores the old constraint/comments but does NOT resurrect the deleted row. **T3** (a
  verified percentage differing from telemetry's stale copy lands the
  charging-sourced value): add to `db_integration_test.go` or a new file, seeding BOTH a
  `telemetry.supercharger_sessions` row (the stale copy, `StartBatteryPct=30`/
  `EndBatteryPct=70`) and a `charging.charge_sessions` row for the SAME session
  (`StartBatteryPct=20`/`EndBatteryPct=90`), then asserting `vehicle_metrics.consumed_pct`
  equals the charging-sourced result (75) and explicitly asserting it does NOT equal the
  telemetry-sourced result (45) — design.md §5 T3's own note on why the negative assertion is
  the point of this test.
  **Acceptance:** T1 and T3 both pass their pinned assertions from design.md §5 exactly (values
  copied verbatim from design.md, not re-derived); the T3 negative assertion
  (`!= 45`) is present, not just the positive one (`== 75`).

## Post-Wave-2 (leader-owned, NOT this worker's task list)

`cmd/web`'s (and `cmd/poller`'s, if applicable) constructor call sites — swapping
`telemetry.NewSuperchargerReader(pool)` for `charging.NewSuperchargerSessionAnalyticsReader(pool)`
wherever `analytics.NewRecalculator`/`analytics.NewReader` are constructed — is out of this
worker's sandbox (`cmd/` is outside `internal/`) and is not listed as a task here. Design.md §4
documents exactly what the leader must change. **The package will not build end-to-end
(`go build ./...` at the repo root) until that leader-owned wiring change lands alongside this
tier's Wave 1/2** — `go build ./internal/analytics/...` (module-scoped) is the correct signal
for this worker to run; the repo-wide build is expected to fail until the leader's follow-up.

- [x] **2.4 — Fix the stale package doc in `analytics.go`** (`depends_on`: 1.2, 1.3, 1.4)
  Appended by the leader after Wave 1. `internal/analytics/analytics.go` line 6's package
  doc still reads "internal/telemetry's `SuperchargerReader` and internal/charging", which
  is false as of this tier — analytics reads `charging.SuperchargerSessionAnalyticsReader`
  for the Supercharger path and `telemetry.Reader` only for snapshots. The Wave 1 worker
  found it and correctly left it alone: it is not in design.md §3's retype-plan file list.
  It is in scope for THIS change all the same, per `CLAUDE.md` §Non-negotiables
  ("docs track structural change ... in the same change, never as a follow-up").
  **Acceptance:** the package doc names the ports analytics actually consumes; no reference
  to `telemetry.SuperchargerReader` survives in `analytics.go`.
