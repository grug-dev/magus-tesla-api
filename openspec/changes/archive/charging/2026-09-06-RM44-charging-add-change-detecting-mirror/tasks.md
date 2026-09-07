# Tasks — RM44-charging-add-change-detecting-mirror

Ownership legend: **[module: charging worker]** — inside `internal/charging/`, this
tier's own sandbox. **[doc: charging worker, granted paths]** — this change's own
`openspec/specs/charge-session-log/spec.md` delta (already written) and
`internal/charging/AGENTS.md`. See design.md's "Database Design," "Test Contract," and
"Local Decisions" sections for the source-of-truth content behind every task below —
this file assigns work and gives acceptance checks; design.md is authoritative for
content.

**This tier trips the `database` design gate** (design.md "Database Design"). Task 1.1
MUST NOT be applied, and no downstream task may start, until the leader has shown
design.md's Database Design section to the owner and the owner has explicitly confirmed
it. This change ships no new migration — the gate covers a query's write semantics, not
a schema change.

**Hard ordering constraints:**
- 1.1 (`db/query.sql`) has no code dependency but is gated by the owner's confirmation
  above.
- 2.1 (`make sqlc`) depends on 1.1 — it reads the edited `query.sql`.
- 3.1 (T1–T6 integration tests) depends on 2.1 — the tests call `SessionWriter` and read
  the row back, which needs the regenerated `chargingdb` package to compile the change
  cleanly, though the interface itself does not change.
- 3.2 (T7 regression check on the existing `SessionVerifier` test) has no code
  dependency of its own beyond 2.1, since it shares the same test file family, but adds
  no new behavior — it only confirms the existing test still holds.
- 3.3 (D8 self-checking schema test) depends on 2.1 for the same reason as 3.1, and may
  run in parallel with 3.1 and 3.2 (separate test file).
- 4.1 (`internal/charging/AGENTS.md`) depends on 1.1 (it quotes the new comment) and may
  run in parallel with everything from Wave 1 onward.
- 4.2 (spec delta placement check) has no code dependency and may run any time.
- 5.1 (verification) runs after every other task.

## Wave 0 — database design gate (leader, before any implementation task)

- [x] **0.1** Leader presents design.md's "Database Design" section (the exact final
  SQL, the rewritten doc comment, the governing rule and the three deny-list buckets,
  the rejected alternatives, the index plan, and the downstream effect on
  `internal/analytics`) to the owner and iterates until explicitly confirmed. No task
  below may start until this gate passes.
  `depends_on`: — · `parallel_ok`: no

## Wave 1 — the query change (module: charging worker)

- [x] **1.1** `internal/charging/db/query.sql` — replace `MirrorSuperchargerSession`'s
  doc comment and its `ON CONFLICT DO UPDATE SET` clause with the exact text in
  design.md "The exact final SQL," verbatim, including the 14-name deny-list array
  literal (NOT the earlier 8-name version — the owner corrected it after a live test;
  design.md D2 explains why). Do not change the `INSERT` column list or the `VALUES`
  list — both stay exactly as they are today. Acceptance:
  `grep -c "to_jsonb(supercharger_sessions.\*)" internal/charging/db/query.sql` returns
  `1`; `grep -c "'{id,account_id,vin,session_id,charge_start_date_time,charge_stop_date_time,site_location_name,start_battery_pct,end_battery_pct,battery_pct_source,created_at,updated_at,inferred_capacity_kwh_calc,status}'::text\[\]" internal/charging/db/query.sql`
  returns `2` (once per side of the `IS DISTINCT FROM`); `grep -c "updated_at = now();" internal/charging/db/query.sql`
  no longer matches inside `MirrorSuperchargerSession` (the other queries in this file
  that still legitimately use `now()` unconditionally — none do today outside this
  query and `VerifySuperchargerSession`, which is unchanged — are unaffected);
  `grep -c "THE RULE: this comparison covers EXACTLY" internal/charging/db/query.sql`
  returns `1`.
  `depends_on`: 0.1 · `parallel_ok`: no

## Wave 2 — codegen (module: charging worker)

- [x] **2.1** Run `make sqlc` (or `sqlc generate`) to regenerate
  `internal/charging/db/query.sql.go` against the edited `query.sql` (1.1). Never
  hand-edit the generated file. Confirm `MirrorSuperchargerSessionParams` is
  byte-identical to before this change — the leader already verified this with sqlc
  v1.31.1 against this exact SQL shape. Acceptance: `git diff --stat
  internal/charging/db/models.go` shows no changes (no schema change, so `models.go` is
  untouched); `git diff internal/charging/db/query.sql.go` shows only the embedded SQL
  string inside `mirrorSuperchargerSession` changed, with `MirrorSuperchargerSessionParams`'s
  field list unchanged; `go build ./internal/charging/...` succeeds.
  `depends_on`: 1.1 · `parallel_ok`: no

## Wave 3 — tests (module: charging worker)

- [x] **3.1** Extend `internal/charging/db_session_integration_test.go` (or a new
  `db_session_mirror_change_detection_integration_test.go` if the existing file is
  already large — worker's choice, note it in the final report) with T1–T6 AND T8 from
  design.md's "Test Contract" table, verbatim expected values. Each case mirrors a
  session via `SessionWriter.MirrorSessions`, reads `updated_at` back via the existing
  `fetchSuperchargerSession` direct-SQL helper (never `chargingdb.SuperchargerSession`,
  per `internal/charging/AGENTS.md`'s `pgtype` rule), calls `MirrorSessions` a second
  time per the case's stated input, and asserts the stated expected `updated_at` (and,
  for T5, the four human-owned columns; for T8, the stored `site_location_name`).
  **T8 makes the same renamed-site call TWICE in a row** (three `MirrorSessions` calls
  total), asserting `updated_at` and the stored name are unchanged after BOTH — this is
  the regression test for the write-once-column bug the owner found live (design.md D2).
  Acceptance: `grep -c "^func TestMirrorSessions_T[1-6]" internal/charging/*_test.go`
  returns `6`; `grep -c "^func TestMirrorSessions_T8" internal/charging/*_test.go`
  returns `1`; `gofmt -l` on the touched file prints nothing; `go vet
  ./internal/charging/...` succeeds.
  `depends_on`: 2.1 · `parallel_ok`: with 3.2, 3.3

- [x] **3.2** Confirm T7 (design.md Test Contract) needs no new test: search
  `internal/charging/db_session_verifier_integration_test.go` for an existing case
  asserting `SessionVerifier.VerifySession` advances `updated_at` on a real edit (it
  should already exist, from RM31/RM41's own test contracts, since `VerifySession` is
  unchanged by this design). If one exists, note its name in the final report — no new
  code. If none exists, add one small case asserting exactly T7's expected value.
  Acceptance: `grep -n "VerifySession" internal/charging/db_session_verifier_integration_test.go
  | grep -i updated_at` shows at least one relevant assertion, existing or newly added.
  `depends_on`: 2.1 · `parallel_ok`: with 3.1, 3.3

- [x] **3.3** New `internal/charging/db_mirror_schema_selfcheck_integration_test.go` —
  the D8 self-checking test from design.md's "The self-checking schema test": query
  `information_schema.columns` for `charging.supercharger_sessions` at runtime, assert
  the live column set equals `written` (the 5-column `SET`-clause list) `∪ deny` (the
  14-column list), both hardcoded per design.md matching production, then perform the
  behavioral poke-and-verify steps: seed one row, directly set every settable
  deny-listed column to the stated sentinels — this now includes
  `charge_start_date_time`, `charge_stop_date_time`, and `site_location_name`, not just
  the identity/human-owned columns — re-mirror with identical values, and assert every
  sentinel including `updated_at` is unchanged. Exempt only `id`, `account_id`, `vin`,
  `session_id` (identity / `ON CONFLICT` target — poking them would insert a new row
  instead of updating this one) and `inferred_capacity_kwh_calc` (GENERATED, cannot be
  set directly). Acceptance:
  `grep -c "information_schema.columns" internal/charging/db_mirror_schema_selfcheck_integration_test.go`
  returns at least `1`; `grep -c "site_location_name" internal/charging/db_mirror_schema_selfcheck_integration_test.go`
  returns at least `1` (proves the write-once bucket is poked, not just the
  never-written bucket); `grep -c "inferred_capacity_kwh_calc" internal/charging/db_mirror_schema_selfcheck_integration_test.go`
  returns at least `1`; `gofmt -l` on the file prints nothing; `go vet
  ./internal/charging/...` succeeds.
  `depends_on`: 2.1 · `parallel_ok`: with 3.1, 3.2

## Wave 4 — docs (module: charging worker, granted AGENTS.md + spec paths)

- [x] **4.1** `internal/charging/AGENTS.md` — rewrite the paragraph in the
  §Public Interface section that currently reads "NO WHERE PREDICATE on the DO UPDATE,
  deliberately (design.md D6)... Consequence: updated_at here means 'the last mirror
  pass touched this row'..." to instead describe the new structural, deny-list
  comparison and state that `updated_at` now means "this row's data changed,"
  cross-referencing this change (design.md's own D1–D3). No other section changes: the
  refresh SET column list itself is unchanged. Acceptance: `grep -c "the last mirror
  pass touched this row" internal/charging/AGENTS.md` returns `0` after the edit;
  `grep -c "RM44-charging-add-change-detecting-mirror" internal/charging/AGENTS.md`
  returns at least `1`.
  `depends_on`: 1.1 · `parallel_ok`: with 4.2

- [x] **4.2** Confirm this change's own `specs/charge-session-log/spec.md` delta (already
  written in this change folder) still reads coherently against the CURRENT
  `openspec/specs/charge-session-log/spec.md` at merge time — the sibling tier
  (`RM44-telemetry-add-change-detecting-upsert`) touches a different capability
  (`telemetry`, not `charge-session-log`) so no conflict is expected, but re-diff before
  archiving if any other charging change lands first. Acceptance: `openspec validate
  RM44-charging-add-change-detecting-mirror --strict` passes.
  `depends_on`: — · `parallel_ok`: with 4.1

## Wave 5 — verification (assistant-run signals, then owner-run suite)

- [x] **5.1** Run and report: `go build ./internal/charging/...`, `go vet
  ./internal/charging/...`, `gofmt -l internal/charging`, `make boundary-guard`. This
  tier's own definition of done: `grep -rln "to_jsonb(supercharger_sessions" internal/charging/`
  includes `db/query.sql` and (post-codegen) `db/query.sql.go`; the three new/extended
  test files from Wave 3 exist; `internal/charging/AGENTS.md` no longer describes
  `updated_at` as "the last mirror pass touched this row." Per the Test-Execution-Policy,
  never run `go test ./...`, `make test`, `make test-with-db`, or `make check`.
  `depends_on`: 1.1, 2.1, 3.1, 3.2, 3.3, 4.1, 4.2 · `parallel_ok`: no

- [x] **5.2** Hand off to the owner the exact command to run and report:
  `go test ./internal/charging/...`. Until the owner reports it passes, this tier's
  implementation status is **awaiting-user-verification**, never "done." After deploy,
  also hand off the owner-only check from proposal.md's "Testing" section (the
  query-log confirmation that a re-sync of an unchanged account logs no new
  `updated_at` values).
  `depends_on`: 5.1 · `parallel_ok`: no
