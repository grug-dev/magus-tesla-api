# Tasks — RM41-telemetry-drop-estimate-columns

Ownership legend: **[module: telemetry worker]** — inside `internal/telemetry/`, this
tier's own sandbox. **[granted: charging, cross-module test]** — the single file
`internal/charging/db_backfill_integration_test.go`, explicitly granted by the leader's
dispatch (the roadmap's tier 3 row did not name it), and nothing else under
`internal/charging/`. **[granted: charging, one sentence]** — one sentence in
`internal/charging/AGENTS.md` that tier 2 deliberately left as a forward reference for
this tier to close. **[granted: charging, historic migration comment]** — one
comment-only edit inside the already-applied historic migration
`internal/charging/db/migrations/20260823000001_add_charge_sessions.sql`, pre-authorized
by that file's own author for exactly this change (see proposal.md "Findings
correction" and design.md "Docs"). See design.md's "Database Design", "Code changes",
"Test Contract", and "Docs" sections for the source-of-truth content behind every task
below — this file assigns work and gives acceptance checks, design.md is authoritative
for content.

**This tier trips the `database` design gate** (design.md "Database Design"). Task
1.1 (the migration) MUST NOT be applied, and no downstream task may start, until the
leader has shown design.md's Database Design section to the owner and the owner has
explicitly confirmed it.

**Hard ordering constraints:**
- 1.1 (migration) has no code dependency but gates every other task per the
  database design gate above — treat it as depending on the owner's confirmation,
  not on any other task.
- 1.2 (`telemetry.go`), 1.3 (`mapping.go`), 1.4 (`service.go`), 1.5 (`db/query.sql`
  comment) are mutually independent Go/SQL-comment edits and may run in parallel
  with each other once 1.1 is confirmed — none of them depends on the migration
  having been *applied* to a live database, only on the design being confirmed
  (they edit source, not run against a database).
- 2.1 (regenerate `db/{models.go,query.sql.go}` via `make sqlc`) depends on 1.1 AND
  1.5 — sqlc reads both the migration directory (schema) and `query.sql` (including
  its comments, which it copies verbatim into the generated file).
- 3.1 (the module's own integration test) depends on 1.2, 1.3, AND 2.1 — it
  references `SuperchargerHistory`'s field set (1.2), the mapping helper's
  behavior (indirectly, via 1.3), and the regenerated `telemetrydb` package shape
  (2.1).
- 3.2 (the granted charging fixture file) depends on 1.1 only — it is raw SQL
  against `telemetry.supercharger_history`, never the generated `telemetrydb`
  package, so it needs the migration's schema shape but not the sqlc regeneration.
  It does NOT depend on 3.1 (different file, different module) and may run in
  parallel with it.
- 4.1 (`internal/telemetry/AGENTS.md`) depends on 1.2 (it documents the real
  `SuperchargerHistory` struct and doc comments 1.2 changes) and may run in
  parallel with 4.2 and 4.3.
- 4.2 (`internal/charging/AGENTS.md`, one sentence) and 4.3 (the historic migration
  comment) have no code dependency and may run in parallel with everything from
  Wave 1 onward — listed in a later wave only for scheduling convenience.
- 5.1 (verification) runs after every other task.

The spec delta (`specs/telemetry/spec.md`, "Supercharger Session Ledger" and
"Supercharger Session Read Port") ships with this change's own artifacts — already
authored in full alongside proposal.md and design.md — and needs no implementation
task; `openspec` syncs it into the main spec at archive time.

## Wave 0 — database design gate (leader, before any implementation task)

- [x] **0.1** Leader presents design.md's "Database Design" section (migration SQL
  up/down, rationale, index plan, data-loss statement) to the owner and iterates
  until explicitly confirmed. No task below may start until this gate passes.
  `depends_on`: — · `parallel_ok`: no

## Wave 1 — independent source edits (module: telemetry worker)

- [ ] **1.1** Create
  `internal/telemetry/db/migrations/20260903000003_drop_supercharger_est_columns.sql`
  with the exact Up/Down SQL from design.md "Database Design" → "Schema change",
  verbatim, including its comments. Do NOT apply the migration yet (`make
  migrate-up` is the owner's step, per tasks.md's final wave). Acceptance:
  `grep -c "DROP COLUMN start_battery_pct_est" internal/telemetry/db/migrations/20260903000003_drop_supercharger_est_columns.sql`
  returns `1`; the file has both a `-- +goose Up` and a `-- +goose Down` section;
  `find internal -path "*/db/migrations/*.sql" | xargs -n1 basename | sort | tail -1`
  shows this file sorts last of every migration in the repository.
  `depends_on`: 0.1 · `parallel_ok`: with 1.2, 1.3, 1.4, 1.5

- [ ] **1.2** `internal/telemetry/telemetry.go` — apply design.md "Code changes" →
  `telemetry.go`'s two edits exactly: delete the "Reserved, currently unwritten
  (design D6)" comment block and the two fields it describes, and update the
  trailing "(see below)" cross-reference in the surviving trio comment. Acceptance:
  `grep -c "StartBatteryPctEst\|EndBatteryPctEst" internal/telemetry/telemetry.go`
  returns `0`; `grep -c "Reserved, currently unwritten" internal/telemetry/telemetry.go`
  returns `0`; `grep -c "(see below)" internal/telemetry/telemetry.go` returns `0`.
  `depends_on`: 0.1 · `parallel_ok`: with 1.1, 1.3, 1.4, 1.5

- [ ] **1.3** `internal/telemetry/mapping.go` — apply design.md "Code changes" →
  `mapping.go`'s three edits exactly: remove the two mapping lines from
  `rowToSuperchargerHistory`, update the field-block comment above them, and
  update the mapping-rules doc comment bullet above the function. Acceptance:
  `grep -c "StartBatteryPctEst\|EndBatteryPctEst" internal/telemetry/mapping.go`
  returns `0`; `grep -c "frozen snapshot pair" internal/telemetry/mapping.go`
  returns `0`; `grep -c "no override exists" internal/telemetry/mapping.go`
  returns `1`.
  `depends_on`: 0.1 · `parallel_ok`: with 1.1, 1.2, 1.4, 1.5

- [ ] **1.4** `internal/telemetry/service.go` — apply design.md "Code changes" →
  `service.go`'s comment rewrite on `upsertSuperchargerHistory`, verbatim.
  Acceptance:
  `grep -c "StartBatteryPctEst\|EndBatteryPctEst" internal/telemetry/service.go`
  returns `0`; `grep -c "RM41-telemetry-drop-estimate-columns" internal/telemetry/service.go`
  returns `1`.
  `depends_on`: 0.1 · `parallel_ok`: with 1.1, 1.2, 1.3, 1.5

- [ ] **1.5** `internal/telemetry/db/query.sql` — rewrite the `UpsertSuperchargerHistory`
  guarding comment per design.md "Code changes" → `db/query.sql`, verbatim. No SQL
  statement in this file changes (only comment text). Acceptance:
  `grep -c "start_battery_pct_est\|end_battery_pct_est\|BatteryPctEst" internal/telemetry/db/query.sql`
  returns `0`; `grep -c "RM41-telemetry-drop-estimate-columns" internal/telemetry/db/query.sql`
  returns `1`.
  `depends_on`: 0.1 · `parallel_ok`: with 1.1, 1.2, 1.3, 1.4

## Wave 2 — codegen (module: telemetry worker)

- [ ] **2.1** Run `make sqlc` (or `sqlc generate`) to regenerate
  `internal/telemetry/db/models.go` and `internal/telemetry/db/query.sql.go`
  against the new migration (1.1) and the edited `query.sql` (1.5). Never
  hand-edit either generated file. Acceptance:
  `grep -c "StartBatteryPctEst\|EndBatteryPctEst" internal/telemetry/db/models.go internal/telemetry/db/query.sql.go`
  returns `0` across both files; `go build ./internal/telemetry` (non-test
  package only) succeeds; `go build ./internal/telemetry/...` fails at this point
  (expected — Wave 3's test file still references the removed fields).
  `depends_on`: 1.1, 1.5 · `parallel_ok`: no

## Wave 3 — test repair (module: telemetry worker, one file granted outside it)

- [ ] **3.1** `internal/telemetry/db_supercharger_battery_pct_integration_test.go` —
  apply design.md "Test Contract" → item 1 exactly: update the file-level doc
  comment's column count; delete the two `*Est` assertion blocks in
  `TestStore_SuperchargerUpsert_FreshInsertSeedsBatteryPctColumnsNull` and update
  its doc comment; leave `TestStore_SuperchargerUpsert_LeavesVerifiedBatteryPctUntouched`
  UNCHANGED; **delete the entire
  `TestStore_SuperchargerUpsert_LeavesVerificationSnapshotUntouched` function**
  (its doc comment included — this is the whole-function deletion design.md's Test
  Contract explains, resolving roadmap D7's "the two telemetry tests that assert
  the columns survive a re-UPSERT"); in
  `TestStore_SuperchargerBatteryPctChecks_RejectOutOfRangeAndUnrecognizedValues`,
  delete the three `*_est` entries from the `rejected` table and remove the two
  `*_est` assignments from the boundary/`'polled'` sanity-check UPDATE (update its
  trailing comment too); rename
  `TestStore_SuperchargerHistoryReader_ReturnsBatteryPctTrioAndSnapshot` to
  `TestStore_SuperchargerHistoryReader_ReturnsBatteryPctTrio` and apply its four
  edits (fixture UPDATE, delete `assertSnapshot`, replace its call site with an
  inline trio assertion for `sessionSnapshot`, update `assertUntouched`).
  Acceptance:
  `grep -c "BatteryPctEst" internal/telemetry/db_supercharger_battery_pct_integration_test.go`
  returns `0`; `grep -c "func TestStore_SuperchargerUpsert_LeavesVerificationSnapshotUntouched"
  internal/telemetry/db_supercharger_battery_pct_integration_test.go` returns `0`;
  `grep -c "func TestStore_SuperchargerHistoryReader_ReturnsBatteryPctTrio\b"
  internal/telemetry/db_supercharger_battery_pct_integration_test.go` returns `1`
  (the renamed function, matched with a trailing word boundary so it does not also
  match the old "...AndSnapshot" name were it still present); `grep -c
  "assertSnapshot" internal/telemetry/db_supercharger_battery_pct_integration_test.go`
  returns `0`.
  `depends_on`: 1.2, 1.3, 2.1 · `parallel_ok`: with 3.2

- [ ] **3.2** **[granted path, cross-module test]**
  `internal/charging/db_backfill_integration_test.go` — apply design.md "Test
  Contract" → item 2 exactly: `superchargerFixtureRow` drops its
  `StartBatteryPctEst`/`EndBatteryPctEst` fields; `insertSuperchargerSessionFixture`'s
  INSERT drops `start_battery_pct_est, end_battery_pct_est` from its column list
  and the two `r.StartBatteryPctEst, r.EndBatteryPctEst` args, renumbering
  `$1..$20` down to `$1..$18`. Touch NOTHING else under `internal/charging/`.
  Acceptance: `grep -c "BatteryPctEst" internal/charging/db_backfill_integration_test.go`
  returns `0`; `git diff --stat` (or equivalent) for this task shows exactly one
  file changed under `internal/charging/`.
  `depends_on`: 1.1 · `parallel_ok`: with 3.1

## Wave 4 — docs (module: telemetry worker, two granted paths outside it)

- [ ] **4.1** `internal/telemetry/AGENTS.md` — apply design.md "Docs" → the six
  numbered edits exactly (the nightly-collection intro sentence, the Data
  Ownership `supercharger_history` bullet, the "Battery-% verification columns"
  section's field-count sentence, the SCOPE NOTE, the "Never auto-written
  (R3/R7)" bullet's three numeral substitutions, and the
  `StartBatteryPctEst`/`EndBatteryPctEst` RESERVED bullet replaced with the
  reversal note). Acceptance:
  `grep -c "StartBatteryPctEst\|EndBatteryPctEst\|start_battery_pct_est\|end_battery_pct_est" internal/telemetry/AGENTS.md`
  returns `0`; `grep -c "no longer exist" internal/telemetry/AGENTS.md` returns
  at least `1` (the reversal note); the battery-percentage sections of the file
  (the nightly-collection intro sentence, the Data Ownership bullet, and the
  entire "Battery-% verification columns" section) contain no remaining "five" —
  confirm by reading the diff, not by a bare repo-wide `grep -c "five"`, since
  "five" legitimately still appears elsewhere in this file describing something
  unrelated (e.g. "Owns five tables", the five derived-consumption figures
  dropped by RM29 — neither touched by this task).
  `depends_on`: 1.2 · `parallel_ok`: with 4.2, 4.3

- [ ] **4.2** **[granted path, one sentence]** `internal/charging/AGENTS.md` —
  apply design.md "Docs" → the exact replacement sentence, verbatim (the
  `internal/telemetry.supercharger_history` forward reference tier 2 left open).
  Touch NOTHING else in this file. Acceptance: the file contains no remaining
  "until tier 3 of this roadmap ... drops its own est-column pair — see that
  change once it lands" text (future tense); `grep -c
  "RM41-telemetry-drop-estimate-columns" internal/charging/AGENTS.md` returns at
  least `1`; `git diff --stat` for this task shows exactly one file changed under
  `internal/charging/` (this file only — 3.2 already covers the fixture file in a
  separate task).
  `depends_on`: — · `parallel_ok`: with 4.1, 4.3

- [ ] **4.3** **[granted path, historic migration comment]**
  `internal/charging/db/migrations/20260823000001_add_charge_sessions.sql` —
  apply design.md "Docs" → the exact replacement text for the Down comment's
  second paragraph, verbatim. Do NOT touch the Down comment's first paragraph
  ("Safe by construction TODAY...") or ANY `-- +goose Up`/`-- +goose Down` SQL
  statement in this file — this is a comment-only edit to an already-applied
  historic migration, pre-authorized by the file's own original text for exactly
  this change (see design.md "Docs" for why this does not violate
  `ai/go-conventions.md`'s "historic migrations are never edited" rule).
  Acceptance: the file contains no remaining "That change owns updating this
  comment; see design.md D9, step 6" text; `grep -c
  "RM41-telemetry-drop-estimate-columns" internal/charging/db/migrations/20260823000001_add_charge_sessions.sql`
  returns at least `1`; a diff of this file shows changes confined to comment
  lines (lines starting `--`) — no `ALTER`, `INSERT`, `DROP`, `CREATE`, or other
  SQL keyword line differs from the version on `main`.
  `depends_on`: — · `parallel_ok`: with 4.1, 4.2

- [ ] **4.4** **[granted path, one sentence — added by the leader at the design gate]**
  `kkpa/context/architecture/nightly-cycle.md` — the line stating that
  `MirrorSuperchargerSession` "never names the five battery-percentage columns" is
  stale: tier 2 dropped charging's estimate pair, so the mirror's trio is three, not
  five. Correct the count and cite `RM41-charging-drop-estimate-columns` (tier 2) as
  the change that reduced it — this line describes CHARGING's mirror, so do not
  attribute it to this tier's telemetry drop. Touch nothing else in the file. The
  worker that wrote these artifacts found the staleness and correctly ruled it out of
  its own scope as tier 2's gap; the owner chose at the tier 3 design gate to fix it
  here rather than defer it to the backlog, because `kkpa-context-fetch` presents the
  KB as authoritative and a wrong count misleads the next agent. Acceptance:
  `grep -c "five battery-percentage columns" kkpa/context/architecture/nightly-cycle.md`
  returns `0`; `grep -c "RM41-charging-drop-estimate-columns" kkpa/context/architecture/nightly-cycle.md`
  returns `1`.
  `depends_on`: — · `parallel_ok`: with 4.1, 4.2, 4.3

## Wave 5 — verification (assistant-run signals, then owner-run suite + migration)

- [ ] **5.1** Run and report: `go build ./internal/telemetry/... ./internal/charging/...`,
  `go vet ./internal/telemetry/... ./internal/charging/...`, `gofmt -l
  internal/telemetry internal/charging`, `make migration-guard`, `make
  boundary-guard`. This tier's own definition of done: `grep -rln
  "StartBatteryPctEst\|EndBatteryPctEst\|start_battery_pct_est\|end_battery_pct_est"
  internal/telemetry/ internal/charging/` (excluding `.claude/worktrees/`) returns
  only `internal/telemetry/db/migrations/20260815000001_add_supercharger_battery_pct.sql`
  (historic, never edited), `internal/telemetry/db/migrations/20260903000001_move_telemetry_to_own_schema.sql`
  (historic, never edited), this tier's own new migration file, and
  `internal/charging/db/migrations/20260823000001_add_charge_sessions.sql` (the
  historic migration whose Down COMMENT this tier corrects per task 4.3 — its
  remaining hits are inside comment prose describing the drop, not SQL naming a
  live column) and `internal/charging/db/migrations/20260903000002_drop_supercharger_est_columns.sql`
  (tier 2's own migration, already archived, never edited by this tier). Per the
  Test-Execution-Policy, never run `go test ./...`, `make test`, `make
  test-with-db`, or `make check`.
  `depends_on`: 1.1, 1.2, 1.3, 1.4, 1.5, 2.1, 3.1, 3.2, 4.1, 4.2, 4.3, 4.4 ·
  `parallel_ok`: no

- [ ] **5.2** Hand off to the owner the exact commands to run and report, in
  order:
  1. `make migrate-up` (applies this tier's migration — the design gate in Wave 0
     already confirmed its content; this is the first time it touches a real
     database).
  2. `SELECT column_name FROM information_schema.columns WHERE table_schema =
     'telemetry' AND table_name = 'supercharger_history' AND column_name IN
     ('start_battery_pct_est', 'end_battery_pct_est');` — expect zero rows.
  3. `go test ./internal/telemetry/... ./internal/charging/...`.
  Until the owner reports all three pass, this tier's implementation status is
  **awaiting-user-verification**, never "done."
  `depends_on`: 5.1 · `parallel_ok`: no
