# Tasks — RM41-charging-add-session-status

Ownership legend: **[module: charging worker]** — inside `internal/charging/`, this
tier's own sandbox. **[doc: charging worker, granted paths]** — the root `README.md`
row it is granted to edit, one `kkpa/context/` file, and this change's own
`openspec/specs/charge-session-log/spec.md` delta. See design.md's "Database Design",
"Go-side call shape", "Code changes", "Test Contract", and "Docs" sections for the
source-of-truth content behind every task below — this file assigns work and gives
acceptance checks, design.md is authoritative for content.

**This tier trips the `database` design gate** (design.md "Database Design"). Task
1.1 (the migration) MUST NOT be applied, and no downstream task may start, until the
leader has shown design.md's Database Design section to the owner and the owner has
explicitly confirmed it.

**Hard ordering constraints:**
- 1.1 (migration) has no code dependency but gates every other task per the database
  design gate above — treat it as depending on the owner's confirmation, not on any
  other task.
- 1.2 (`charging.go`), 1.4 (`db/query.sql`) are mutually independent edits and may run
  in parallel with each other once 1.1 is confirmed — neither depends on the
  migration having been *applied* to a live database, only on the design being
  confirmed (they edit source, not run against a database).
- 1.3 (`session_verifier.go`) depends on 1.2 — it references `SessionStatus` and its
  three constants, which 1.2 defines.
- 1.5 (`session_reader.go`) depends on 1.2 — it references `SessionStatus`.
- 2.1 (regenerate `db/{models.go,query.sql.go}` via `make sqlc`) depends on 1.1 AND
  1.4 — sqlc reads both the migration directory (schema) and `query.sql` (including
  its comments, copied verbatim into the generated file).
- 3.1 (extend `db_session_verifier_integration_test.go` with Group S + the T8 repair)
  depends on 1.2, 1.3, 1.5, AND 2.1 — it references `charging.SessionStatus`'s three
  constants, `Session.Status`, and the regenerated `chargingdb` package shape.
- 4.1 (`internal/charging/AGENTS.md`) depends on 1.2 AND 1.3 (it documents the real
  `Session`/`SessionVerifier` shapes both change) and may run in parallel with 4.2
  and 4.3.
- 4.2 (root `README.md`) and 4.3 (`kkpa/context/use-case/charging/verify-session-battery.md`)
  have no code dependency and may run in parallel with everything from Wave 1
  onward — listed in a later wave only for scheduling convenience.
- 4.4 (spec delta) has no code dependency and may run in parallel with everything.
- 5.1 (verification) runs after every other task.

## Wave 0 — database design gate (leader, before any implementation task)

- [x] **0.1** Leader presents design.md's "Database Design" section (migration SQL
  up/down, state truth table, rationale — including the recompute-mismatch evidence
  behind the BACKFILL decision — and index plan) to the owner and iterates until
  explicitly confirmed. No task below may start until this gate passes.
  `depends_on`: — · `parallel_ok`: no

## Wave 1 — independent source edits (module: charging worker)

- [x] **1.1** Create
  `internal/charging/db/migrations/20260903000004_add_session_status.sql` with the
  exact Up/Down SQL from design.md "Database Design" → "Schema change", verbatim,
  including its comments. Do NOT apply the migration yet (`make migrate-up` is the
  owner's step, per tasks.md's final wave). Acceptance:
  `grep -c "ADD COLUMN status TEXT NOT NULL DEFAULT 'IN_PROGRESS'" internal/charging/db/migrations/20260903000004_add_session_status.sql`
  returns `1`; `grep -c "UPDATE charging.supercharger_sessions SET status = 'DONE_CALCULATED';" internal/charging/db/migrations/20260903000004_add_session_status.sql`
  returns `1`; the file has both a `-- +goose Up` and a `-- +goose Down` section;
  `ls internal/charging/db/migrations/ | sort | tail -1` shows this file sorts last;
  `make migration-guard` reports no duplicate version across `MIGRATIONS_DIRS`.
  `depends_on`: 0.1 · `parallel_ok`: with 1.2, 1.4

- [x] **1.2** `internal/charging/charging.go` — apply design.md "Code changes" →
  `charging.go`'s three edits exactly: add the `SessionStatus` type + three
  constants, add the `Status SessionStatus` field to `Session` (after
  `BatteryPctSource`, before `InferredCapacityKWhCalc`) and update its doc comment
  ("Eighteen fields" → "Nineteen fields"), and rewrite `SessionVerifier`'s doc
  comment to the four-column version. Acceptance:
  `grep -c "SessionStatusInProgress\|SessionStatusDoneCalculated\|SessionStatusDone" internal/charging/charging.go`
  returns at least `4` (the three `const` declarations plus the doc-comment
  references); `grep -c "Nineteen fields" internal/charging/charging.go` returns
  `1`; `grep -c "updates exactly four columns" internal/charging/charging.go`
  returns `1`.
  `depends_on`: 0.1 · `parallel_ok`: with 1.1, 1.4

- [x] **1.3** `internal/charging/session_verifier.go` — add the `sessionStatusFor`
  function (design.md "Go-side call shape", verbatim) and apply the `VerifySession`
  body edit shown there: introduce `calculated`, set it when the derivation branch
  runs and produces a non-nil `startToStore`, compute `status` via
  `sessionStatusFor`, and pass `Status: string(status)` into
  `VerifySuperchargerSessionParams`. Update `VerifySession`'s doc comment per
  design.md's numbered-list addition. Acceptance:
  `grep -c "func sessionStatusFor" internal/charging/session_verifier.go` returns
  `1`; `grep -c "Status:.*string(status)" internal/charging/session_verifier.go`
  returns `1`; `go vet ./internal/charging/...` fails at this point (expected —
  `chargingdb.VerifySuperchargerSessionParams` has no `Status` field until 2.1 runs)
  — this is a KNOWN, ACCEPTED intermediate state, not a task failure.
  `depends_on`: 1.2 · `parallel_ok`: no (must follow 1.2; 1.4/2.1 sequencing below
  governs when it becomes compilable)

- [x] **1.4** `internal/charging/db/query.sql` — add `status = @status` to
  `VerifySuperchargerSession`'s `SET` clause and rewrite its doc comment to the
  four-column version; add the one-paragraph addition to
  `MirrorSuperchargerSession`'s guarding comment, per design.md "Code changes" →
  `db/query.sql`, verbatim. No other query changes (the three `SELECT *` reads and
  `LockSessionForVerification` are untouched). Acceptance:
  `grep -c "status             = @status" internal/charging/db/query.sql` returns
  `1`; `grep -c "RM41-charging-add-session-status" internal/charging/db/query.sql`
  returns at least `2` (one per rewritten/extended comment).
  `depends_on`: 0.1 · `parallel_ok`: with 1.1, 1.2

- [x] **1.5** `internal/charging/session_reader.go` — add
  `Status: SessionStatus(r.Status)` to `rowToSession` (after `BatteryPctSource`,
  before `CreatedAt`) and the one new mapping-rules bullet, per design.md "Code
  changes" → `session_reader.go`. Acceptance:
  `grep -c "Status: SessionStatus(r.Status)" internal/charging/session_reader.go`
  returns `1`.
  `depends_on`: 1.2 · `parallel_ok`: with 1.3, 1.4 (after 1.2 lands)

## Wave 2 — codegen (module: charging worker)

- [x] **2.1** Run `make sqlc` (or `sqlc generate`) to regenerate
  `internal/charging/db/models.go` and `internal/charging/db/query.sql.go` against
  the new migration (1.1) and the edited `query.sql` (1.4). Never hand-edit either
  generated file. Acceptance: `grep -c "Status string" internal/charging/db/models.go`
  returns at least `1` on the `SuperchargerSession` struct (in addition to the
  pre-existing one on `ManualChargeEntry`, so `grep -c` across the whole file may
  read `2`); `grep -c "Status" internal/charging/db/query.sql.go` shows
  `VerifySuperchargerSessionParams` gained a `Status string` field;
  `go build ./internal/charging` (non-test package only, if checked in isolation)
  succeeds.
  `depends_on`: 1.1, 1.4 · `parallel_ok`: no

## Wave 3 — test extension (module: charging worker)

- [ ] **3.1** `internal/charging/db_session_verifier_integration_test.go` — add the
  `seedVerifierSessionEnergy` helper and the eight new test functions
  (`TestVerifySession_S1_FreshMirrorIsInProgress` through
  `TestVerifySession_S8_DerivedOutOfRangeStaysInProgress`), verbatim from design.md
  "Test Contract" → "Group S". Repair `TestVerifySession_OnlyTargetColumnsChange`
  (T8) per design.md's "Repair" subsection: capture the returned `Session` instead
  of discarding it, and assert `Status == charging.SessionStatusDone`. Acceptance:
  `grep -c "^func TestVerifySession_S[1-8]_" internal/charging/db_session_verifier_integration_test.go`
  returns `8`; `grep -c "charging.SessionStatusDone)$" internal/charging/db_session_verifier_integration_test.go`
  shows the T8 repair landed (at least `1` new occurrence beyond the new S-group
  tests' own references); `go vet ./internal/charging/...` succeeds (this is the
  point where 1.3's intermediate "expected failure" from Wave 1 resolves, since
  `chargingdb.VerifySuperchargerSessionParams.Status` now exists via 2.1 and this
  test file is the first to exercise the whole path end-to-end); `gofmt -l
  internal/charging/db_session_verifier_integration_test.go` prints nothing.
  `depends_on`: 1.2, 1.3, 1.5, 2.1 · `parallel_ok`: no (single file)

## Wave 4 — docs (module: charging worker, granted README + KB + spec paths)

- [ ] **4.1** `internal/charging/AGENTS.md` — apply design.md "Docs" → the five
  numbered edits exactly (§Public Interface's `SessionStatus`/`Session` code block,
  `SessionVerifier`'s doc comment, the new "Session lifecycle status" subsection,
  the Data Ownership column-by-column sentence, and the Testing Notes sentence).
  Acceptance: `grep -c "SessionStatus" internal/charging/AGENTS.md` returns at
  least `3`; `grep -c "### Session lifecycle status" internal/charging/AGENTS.md`
  returns `1`; `grep -c "DONE_CALCULATED" internal/charging/AGENTS.md` returns at
  least `2` (the type doc and the new subsection).
  `depends_on`: 1.2, 1.3 · `parallel_ok`: with 4.2, 4.3, 4.4

- [ ] **4.2** Root `README.md` — apply design.md "Docs" → the `charging.supercharger_sessions`
  row rewrite exactly: "five" becomes "three" (correcting the pre-existing
  staleness from tier 2) AND the new `status` sentence is added. Touch only this
  one table cell; no other row or section. Acceptance:
  `grep -c "the five" README.md` returns `0` in the `charging.supercharger_sessions`
  row (confirm via `grep -n "supercharger_sessions" README.md` that the specific
  line no longer contains "five"); `grep -c "IN_PROGRESS.*DONE_CALCULATED.*DONE\|status" README.md`
  shows the new sentence present on that row.
  `depends_on`: — · `parallel_ok`: with 4.1, 4.3, 4.4

- [ ] **4.3** `kkpa/context/use-case/charging/verify-session-battery.md` — apply
  design.md "Docs" → the Database table row 1 rewrite (add `status` as the fourth
  SET target) and the one-clause addition to the mirror-images gotcha bullet.
  Acceptance: `grep -c "start_battery_pct.*end_battery_pct.*battery_pct_source.*status.*updated_at"
  kkpa/context/use-case/charging/verify-session-battery.md` returns `1` (allow
  flexible spacing/formatting matching the file's own table style —
  the check is that all four column names plus `updated_at` appear on that one
  row, in order); `grep -c "RM41-charging-add-session-status"
  kkpa/context/use-case/charging/verify-session-battery.md` returns at least `1`.
  `depends_on`: — · `parallel_ok`: with 4.1, 4.2, 4.4

- [ ] **4.4** `openspec/specs/charge-session-log/spec.md` — apply the spec delta
  from this change's own `specs/charge-session-log/spec.md` (this task is really
  "confirm `openspec archive` will sync it correctly" — no manual edit needed
  beyond what already exists in this change folder; verify the delta's ADDED
  Requirement and the two MODIFIED Requirements read coherently against the
  CURRENT `openspec/specs/charge-session-log/spec.md` at merge time, since tier 2/3
  may have landed further edits to this same file between when this design was
  written and when this task runs — re-diff before archiving if so). Acceptance:
  `openspec validate RM41-charging-add-session-status --strict` passes.
  `depends_on`: — · `parallel_ok`: with 4.1, 4.2, 4.3

## Wave 5 — verification (assistant-run signals, then owner-run suite + migration)

- [ ] **5.1** Run and report: `go build ./internal/charging/...`,
  `go vet ./internal/charging/...`, `gofmt -l internal/charging`,
  `make migration-guard`, `make boundary-guard`. This tier's own definition of
  done: `grep -rln "SessionStatus" internal/charging/` includes `charging.go`,
  `session_verifier.go`, `session_reader.go`,
  `db_session_verifier_integration_test.go`, and `AGENTS.md`; the new migration
  file exists and sorts last; no other module under `internal/` references
  `SessionStatus` (tier 5, gateway, is a separate, later, dependent tier). Per the
  Test-Execution-Policy, never run `go test ./...`, `make test`, `make
  test-with-db`, or `make check`.
  `depends_on`: 1.1, 1.2, 1.3, 1.4, 1.5, 2.1, 3.1, 4.1, 4.2, 4.3, 4.4 ·
  `parallel_ok`: no

- [ ] **5.2** Hand off to the owner the exact commands to run and report, in
  order:
  1. `make migrate-up` (applies this tier's migration — the design gate in Wave 0
     already confirmed its content; this is the first time it touches a real
     database).
  2. `SELECT status, count(*) FROM charging.supercharger_sessions GROUP BY status;`
     — expect every pre-existing row under `DONE_CALCULATED` and, once the next
     nightly cycle mirrors any brand-new session, that session under
     `IN_PROGRESS`.
  3. `go test ./internal/charging/...`.
  Until the owner reports all three pass, this tier's implementation status is
  **awaiting-user-verification**, never "done."
  `depends_on`: 5.1 · `parallel_ok`: no
