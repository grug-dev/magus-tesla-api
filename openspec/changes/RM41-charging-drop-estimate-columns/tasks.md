# Tasks — RM41-charging-drop-estimate-columns

Ownership legend: **[module: charging worker]** — inside `internal/charging/`, this
tier's own sandbox. **[granted: analytics, D6]** — the single file this worker is
explicitly granted outside `internal/charging/`
(`internal/analytics/db_integration_test.go`), and nothing else under
`internal/analytics/`. **[doc: charging worker, granted paths]** — three KB files
under `kkpa/context/` and this change's own `openspec/specs/charge-session-log/spec.md`
delta. See design.md's "Database Design", "Code changes", "Test Contract", and
"Docs" sections for the source-of-truth content behind every task below — this file
assigns work and gives acceptance checks, design.md is authoritative for content.

**This tier trips the `database` design gate** (design.md "Database Design"). Task
1.1 (the migration) MUST NOT be applied, and no downstream task may start, until the
leader has shown design.md's Database Design section to the owner and the owner has
explicitly confirmed it.

**Hard ordering constraints:**
- 1.1 (migration) has no code dependency but gates every other task per the
  database design gate above — treat it as depending on the owner's confirmation,
  not on any other task.
- 1.2 (`charging.go`), 1.3 (`session_reader.go`), 1.4 (`db/query.sql` comments) are
  mutually independent Go/SQL-comment edits and may run in parallel with each other
  once 1.1 is confirmed — none of them depends on the migration having been
  *applied* to a live database, only on the design being confirmed (they edit
  source, not run against a database).
- 2.1 (regenerate `db/{models.go,query.sql.go}` via `make sqlc`) depends on 1.1 AND
  1.4 — sqlc reads both the migration directory (schema) and `query.sql` (including
  its comments, which it copies verbatim into the generated file).
- 3.1-3.5 (the five test-file repairs) each depend on 1.2, 1.3, AND 2.1 — every one
  of them references either `charging.Session`'s field set (1.2), the mapping
  helper's behavior (indirectly, via 1.3), or the regenerated `chargingdb` package
  shape (2.1). They do NOT depend on each other and may run in parallel (five
  separate files; 3.5 is a different module's file entirely).
- 4.1 (`internal/charging/AGENTS.md`) depends on 1.2 only (it documents the real
  `Session` struct and doc comments 1.2 changes) and may run in parallel with 3.x
  and 4.2.
- 4.2 (the three `kkpa/context/` doc fixes) has no code dependency and may run in
  parallel with everything from Wave 1 onward — listed in a later wave only for
  scheduling convenience.
- 5.1 (verification) runs after every other task.

## Wave 0 — database design gate (leader, before any implementation task)

- [x] **0.1** Leader presents design.md's "Database Design" section (migration SQL
  up/down, rationale, index plan, data-loss statement) to the owner and iterates
  until explicitly confirmed. No task below may start until this gate passes.
  `depends_on`: — · `parallel_ok`: no

## Wave 1 — independent source edits (module: charging worker)

- [ ] **1.1** Create
  `internal/charging/db/migrations/20260903000002_drop_supercharger_est_columns.sql`
  with the exact Up/Down SQL from design.md "Database Design" → "Schema change",
  verbatim, including its comments. Do NOT apply the migration yet (`make
  migrate-up` is the owner's step, per tasks.md's final wave). Acceptance:
  `grep -c "DROP COLUMN start_battery_pct_est" internal/charging/db/migrations/20260903000002_drop_supercharger_est_columns.sql`
  returns `1`; the file has both a `-- +goose Up` and a `-- +goose Down` section;
  `ls internal/charging/db/migrations/ | sort | tail -1` shows this file sorts
  last.
  `depends_on`: 0.1 · `parallel_ok`: with 1.2, 1.3, 1.4

- [ ] **1.2** `internal/charging/charging.go` — apply design.md "Code changes" →
  `charging.go`'s three edits exactly: remove the two `Session` fields, update the
  doc comment above `Session` ("five" → "three", "Twenty fields" → "Eighteen
  fields"), and rewrite `SessionVerifier.VerifySession`'s doc comment per the
  given before/after. Acceptance:
  `grep -c "StartBatteryPctEst\|EndBatteryPctEst" internal/charging/charging.go`
  returns `0`; `grep -c "the five charging-owned" internal/charging/charging.go`
  returns `0`; `grep -c "Eighteen fields" internal/charging/charging.go` returns
  `1`.
  `depends_on`: 0.1 · `parallel_ok`: with 1.1, 1.3, 1.4

- [ ] **1.3** `internal/charging/session_reader.go` — remove the two
  `StartBatteryPctEst`/`EndBatteryPctEst` mapping lines from `rowToSession` and
  update the mapping-rules doc comment bullet per design.md "Code changes" →
  `session_reader.go`. Acceptance:
  `grep -c "StartBatteryPctEst\|EndBatteryPctEst" internal/charging/session_reader.go`
  returns `0`.
  `depends_on`: 0.1 · `parallel_ok`: with 1.1, 1.2, 1.4

- [ ] **1.4** `internal/charging/db/query.sql` — rewrite the two guarding comments
  on `MirrorSuperchargerSession` and `VerifySuperchargerSession` per design.md
  "Code changes" → `db/query.sql`, verbatim. No SQL statement in this file changes
  (only comment text). Acceptance:
  `grep -c "start_battery_pct_est\|end_battery_pct_est\|BatteryPctEst" internal/charging/db/query.sql`
  returns `0`; `grep -c "RM41-charging-drop-estimate-columns" internal/charging/db/query.sql`
  returns `2` (one mention per rewritten comment).
  `depends_on`: 0.1 · `parallel_ok`: with 1.1, 1.2, 1.3

## Wave 2 — codegen (module: charging worker)

- [ ] **2.1** Run `make sqlc` (or `sqlc generate`) to regenerate
  `internal/charging/db/models.go` and `internal/charging/db/query.sql.go` against
  the new migration (1.1) and the edited `query.sql` (1.4). Never hand-edit either
  generated file. Acceptance:
  `grep -c "StartBatteryPctEst\|EndBatteryPctEst" internal/charging/db/models.go internal/charging/db/query.sql.go`
  returns `0` across both files; `go build ./internal/charging/...` fails at this
  point (expected — the four integration test files in Wave 3 still reference the
  removed fields) but `go build ./internal/charging` (non-test package only, if
  checked in isolation) succeeds.
  `depends_on`: 1.1, 1.4 · `parallel_ok`: no

## Wave 3 — test repair (module: charging worker, one file granted outside it)

- [ ] **3.1** `internal/charging/db_session_integration_test.go` — apply design.md
  "Test Contract" → item 1 exactly: drop the two fields from
  `superchargerSessionRow`, drop the two columns/scan targets from
  `fetchSuperchargerSession`, delete the two `if row.StartBatteryPctEst...`/
  `EndBatteryPctEst` assertion blocks in the first-mirror test, and in the B6-shaped
  re-mirror test remove `start_battery_pct_est = 39, end_battery_pct_est = 90` from
  the direct-SQL `UPDATE` AND delete the two corresponding assertion blocks.
  Acceptance:
  `grep -c "BatteryPctEst\|battery_pct_est" internal/charging/db_session_integration_test.go`
  returns `0`.
  `depends_on`: 1.2, 1.3, 2.1 · `parallel_ok`: with 3.2, 3.3, 3.4, 3.5

- [ ] **3.2** `internal/charging/db_session_reader_integration_test.go` — apply
  design.md "Test Contract" → item 2 exactly: delete the two `s940002.*Est*`
  assertions, drop the two `*Est` disjuncts from the 940001/940003 trailing-loop
  condition, and remove the `StartBatteryPctEst`/`EndBatteryPctEst` values (22/78)
  from whichever fixture seeds session 940002. Acceptance:
  `grep -c "BatteryPctEst" internal/charging/db_session_reader_integration_test.go`
  returns `0`.
  `depends_on`: 1.2, 1.3, 2.1 · `parallel_ok`: with 3.1, 3.3, 3.4, 3.5

- [ ] **3.3** `internal/charging/db_backfill_integration_test.go` — apply
  design.md "Test Contract" → item 3 exactly: **leave
  `superchargerFixtureRow`'s two `*Est` fields and
  `insertSuperchargerSessionFixture`'s INSERT into `telemetry.supercharger_history`
  completely UNCHANGED** (that table is tier 3's, unaffected here); delete only the
  four `if row.StartBatteryPctEst...`/`EndBatteryPctEst` assertion blocks that read
  back the BACKFILLED `charging.supercharger_sessions` row (two per branch, both
  branches). Acceptance:
  `grep -c "BatteryPctEst" internal/charging/db_backfill_integration_test.go`
  returns `3` (the two `superchargerFixtureRow` struct field declarations plus the
  one line passing both as `Exec` args in `insertSuperchargerSessionFixture` —
  `grep -c` counts matching LINES, and that Exec call names both fields on one
  line; all three stay deliberately unchanged — count this task done when this is
  the ONLY remaining occurrence type, not `0`); confirm with
  `grep -n "BatteryPctEst" internal/charging/db_backfill_integration_test.go`
  that every remaining hit is inside `superchargerFixtureRow`'s declaration or
  `insertSuperchargerSessionFixture`'s INSERT/Exec, never inside a `row.`
  (charging-side) assertion.
  `depends_on`: 1.2, 1.3, 2.1 · `parallel_ok`: with 3.1, 3.2, 3.4, 3.5

- [ ] **3.4** `internal/charging/db_session_verifier_integration_test.go` — apply
  design.md "Test Contract" → item 4 exactly: delete the T1 `*Est` assertion pair
  and the T8 bit-identical `*Est` assertion pair. Acceptance:
  `grep -c "BatteryPctEst" internal/charging/db_session_verifier_integration_test.go`
  returns `0`.
  `depends_on`: 1.2, 1.3, 2.1 · `parallel_ok`: with 3.1, 3.2, 3.3, 3.5

- [ ] **3.5** **[granted path, roadmap D6]**
  `internal/analytics/db_integration_test.go` — apply design.md "Test Contract" →
  item 5 exactly: `seedChargeSession`'s INSERT drops
  `start_battery_pct_est, end_battery_pct_est` from its column list and the two
  `pgInt2FromIntPtr(s.Start/EndBatteryPctEst)` args, renumbering `$1..$18` down to
  `$1..$16`. Touch NOTHING else under `internal/analytics/`. Acceptance:
  `grep -c "BatteryPctEst" internal/analytics/db_integration_test.go` returns `0`;
  `git diff --stat` (or equivalent) for this task shows exactly one file changed
  under `internal/analytics/`.
  `depends_on`: 1.2, 1.3, 2.1 · `parallel_ok`: with 3.1, 3.2, 3.3, 3.4

## Wave 4 — docs (module: charging worker, granted KB + spec paths)

- [ ] **4.1** `internal/charging/AGENTS.md` — apply design.md "Docs" → the five
  numbered edits exactly (`SessionMirror` exclusion note, the `Session` struct code
  block, `SessionVerifier`'s doc comment, the Data Ownership lead sentence, and the
  column-by-column bullet deletion). Do NOT edit the D16/rename-scope sentence
  (design.md explains why it stays). Acceptance:
  `grep -c "StartBatteryPctEst\|EndBatteryPctEst\|start_battery_pct_est\|end_battery_pct_est" internal/charging/AGENTS.md`
  returns `0` — the deliberately-untouched D16 historical sentence refers to the
  auto-named CHECKs generically as "their `_est` siblings" and never spells out
  either literal column name, so it does not match this pattern. Separately
  confirm with `grep -c '_est. siblings' internal/charging/AGENTS.md` returning
  `1` that the D16 sentence itself is still present and unedited.
  `depends_on`: 1.2 · `parallel_ok`: with 4.2, 4.3, 4.4

- [ ] **4.2** `kkpa/context/use-case/charging/verify-session-battery.md` — apply
  design.md "Docs" → the exact replacement text, verbatim. Acceptance: the file
  contains no remaining "await an estimator that does not exist yet" text; `grep -c
  "RM41-charging-drop-estimate-columns" kkpa/context/use-case/charging/verify-session-battery.md`
  returns `1`.
  `depends_on`: — · `parallel_ok`: with 4.1, 4.3, 4.4

- [ ] **4.3** `kkpa/context/workflows/supercharger-stats-read.md` — apply design.md
  "Docs" → the exact replacement clause. Acceptance: the file contains no
  remaining "until tier 2 of `RM41-supercharger-battery-pct-cleanup` drops the
  columns" text (future tense); it instead states tier 2 already dropped them.
  `depends_on`: — · `parallel_ok`: with 4.1, 4.2, 4.4

- [ ] **4.4** `kkpa/context/architecture/charge-record-mutation.md` — apply
  design.md "Docs" → the exact replacement text, verbatim. This file was NOT named
  by the roadmap; it was found by re-verifying the Findings before writing these
  artifacts (proposal.md "Findings correction"). Acceptance: the file contains no
  remaining "are never written ... always show `—`" text for these two columns;
  `grep -c "RM41-charging-drop-estimate-columns" kkpa/context/architecture/charge-record-mutation.md`
  returns `1`.
  `depends_on`: — · `parallel_ok`: with 4.1, 4.2, 4.3

## Wave 5 — verification (assistant-run signals, then owner-run suite + migration)

- [ ] **5.1** Run and report: `go build ./internal/charging/... ./internal/analytics/...`,
  `go vet ./internal/charging/... ./internal/analytics/...`, `gofmt -l
  internal/charging internal/analytics`, `make migration-guard`, `make
  boundary-guard`. This tier's own definition of done: `grep -rln
  "StartBatteryPctEst\|EndBatteryPctEst\|start_battery_pct_est\|end_battery_pct_est"
  internal/charging/ internal/analytics/` returns only
  `internal/charging/db/migrations/20260823000001_add_charge_sessions.sql`
  (historic, never edited), `internal/charging/db/migrations/20260902000003_move_charging_to_own_schema.sql`
  (historic, never edited), this tier's own new migration file, and
  `internal/charging/db_backfill_integration_test.go` (the deliberately-unchanged
  `telemetry`-side fixture, per 3.3). Per the Test-Execution-Policy, never run `go
  test ./...`, `make test`, `make test-with-db`, or `make check`.
  `depends_on`: 1.1, 1.2, 1.3, 1.4, 2.1, 3.1, 3.2, 3.3, 3.4, 3.5, 4.1, 4.2, 4.3, 4.4
  · `parallel_ok`: no

- [ ] **5.2** Hand off to the owner the exact commands to run and report, in
  order:
  1. `make migrate-up` (applies this tier's migration — the design gate in Wave 0
     already confirmed its content; this is the first time it touches a real
     database).
  2. `SELECT column_name FROM information_schema.columns WHERE table_schema =
     'charging' AND table_name = 'supercharger_sessions' AND column_name IN
     ('start_battery_pct_est', 'end_battery_pct_est');` — expect zero rows.
  3. `go test ./internal/charging/... ./internal/analytics/...`.
  Until the owner reports all three pass, this tier's implementation status is
  **awaiting-user-verification**, never "done."
  `depends_on`: 5.1 · `parallel_ok`: no
