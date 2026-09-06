# Tasks — RM44-platform-add-mirror-watermark

Ownership legend: **[module: telemetry worker]** — inside `internal/telemetry/`,
that worker's own sandbox. **[module: charging worker]** — inside
`internal/charging/`, that worker's own sandbox. **[leader]** — inside
`internal/app/`, which belongs to neither module worker's sandbox for this
change (see design.md "Cross-Module Wiring" and "Findings"). **[doc: <worker>,
granted paths]** — this change's own spec-delta files (already written) plus
the named `AGENTS.md`.

See design.md's "Database Design," "Interfaces," "Cross-Module Wiring," "Test
Contract," and "Local Decisions" sections for the source-of-truth content
behind every task below — this file assigns work and gives acceptance checks;
design.md is authoritative for content.

**This tier trips the `database` design gate** (design.md "Database Design").
No task below may start until the leader has shown design.md's full schema,
both rejected alternatives, and the index plan to the owner and the owner has
explicitly confirmed it.

**The `internal/telemetry` group and the `internal/charging` group are
INDEPENDENT of each other** — neither module's tasks read or import the
other's code, and both depend only on tiers 1–3 (already archived), not on
each other. They MAY run in parallel, by two separate workers. **The
`internal/app` group depends on BOTH being complete** — it is the only group
that touches both ports at once, and it is leader-owned, not a module
worker's task.

## Wave 0 — database design gate (leader, before any implementation task)

- [ ] **0.1** Leader presents design.md's "Database Design" section (the full
  `charging.mirror_watermarks` schema, both rejected alternatives cited from
  roadmap D4, the two new sqlc queries, the new telemetry query, and the
  index plan for both) to the owner and iterates until explicitly confirmed.
  No task below may start until this gate passes.
  `depends_on`: — · `parallel_ok`: no

## Wave 1a — telemetry read method (module: telemetry worker)

- [ ] **1a.0** New migration
  `internal/telemetry/db/migrations/20260906000001_add_supercharger_history_account_updated_idx.sql`
  — creates `idx_supercharger_history_account_updated` on
  `telemetry.supercharger_history (account_id, updated_at)`. Exact SQL and
  comment in design.md "Full schema — the new telemetry index." The index
  lives in a TELEMETRY migration, never in the charging one: a module may
  only alter its own tables (`ai/architecture.md` §2). Acceptance: `make
  migration-guard` passes; the filename sorts after
  `20260903000003_drop_supercharger_est_columns.sql`; the `-- +goose Down`
  drops the index.
  `depends_on`: 0.1 · `parallel_ok`: with charging's Wave 1b

- [ ] **1a.1** `internal/telemetry/db/query.sql` — add
  `SuperchargerHistoryByAccountUpdatedSince`, exact SQL in design.md
  "The new telemetry query." Acceptance: `grep -c "name: SuperchargerHistoryByAccountUpdatedSince"
  internal/telemetry/db/query.sql` returns `1`; the query has no `tesla_id`
  parameter or predicate.
  `depends_on`: 1a.0 · `parallel_ok`: no

- [ ] **1a.2** Run `make sqlc` to regenerate `internal/telemetry/db/query.sql.go`
  against 1a.1. Never hand-edit the generated file. Acceptance: `go build
  ./internal/telemetry/...` succeeds; the generated package exposes
  `SuperchargerHistoryByAccountUpdatedSinceParams{AccountID, Since}` with no
  `TeslaID` field.
  `depends_on`: 1a.1 · `parallel_ok`: no

- [ ] **1a.3** `internal/telemetry/telemetry.go` — add
  `SuperchargerHistoryByAccountUpdatedSince(ctx, accountID, since)
  ([]SuperchargerHistory, error)` to the `SuperchargerHistoryReader`
  interface, doc comment verbatim from design.md "Interfaces." Acceptance:
  `go vet ./internal/telemetry/...` fails at this point (implementation and
  decorator not yet added) — expected; do not treat as a blocker for the
  next task.
  `depends_on`: 1a.2 · `parallel_ok`: no

- [ ] **1a.4** `internal/telemetry/reader.go` — implement
  `superchargerHistoryReader.SuperchargerHistoryByAccountUpdatedSince`,
  mirroring `SuperchargerHistoryByAccount`'s shape (account-only params, the
  existing `rowToSuperchargerHistory` mapper). Acceptance: `go build
  ./internal/telemetry/...` succeeds.
  `depends_on`: 1a.3 · `parallel_ok`: no

- [ ] **1a.5** `internal/telemetry/query_log.go` — add ONE explicit method to
  `loggingSuperchargerHistoryReader` (no interface embedding, per D7):
  `SuperchargerHistoryByAccountUpdatedSince`, logging AFTER delegating,
  printing `account`, `since`, `rows` — same shape as its three siblings on
  this decorator. Acceptance: the existing compile-time assertion
  (`var _ SuperchargerHistoryReader = (*loggingSuperchargerHistoryReader)(nil)`)
  compiles; `go vet ./internal/telemetry/...` succeeds.
  `depends_on`: 1a.4 · `parallel_ok`: no

- [ ] **1a.6** `internal/telemetry/query_log_test.go` — add the new method to
  `fakeQueryLogSCHReader` (this file's own fake, inside this worker's
  sandbox) and one test case exercising 1a.5's new log line. Acceptance:
  `go vet ./internal/telemetry/...` succeeds; `gofmt -l
  internal/telemetry/query_log_test.go` prints nothing.
  `depends_on`: 1a.5 · `parallel_ok`: with 1a.7

- [ ] **1a.7** New `internal/telemetry/db_supercharger_account_updated_since_integration_test.go`
  (or extend an existing `db_supercharger_*_integration_test.go` file —
  worker's choice, note it in the final report) covering T-tel-1 through
  T-tel-6 from design.md's Test Contract, verbatim expected values.
  Acceptance: `grep -c "^func TestSuperchargerHistoryByAccountUpdatedSince"
  internal/telemetry/*_test.go` returns at least `1`; the test file asserts
  a `tesla_id IS NULL` session IS included (T-tel-2); the file also covers
  T-tel-7 — an `EXPLAIN` assertion that the query uses
  `idx_supercharger_history_account_updated` and needs no sort step;
  `gofmt -l` on the
  touched file prints nothing; `go vet ./internal/telemetry/...` succeeds.
  `depends_on`: 1a.4 · `parallel_ok`: with 1a.6

## Wave 1b — charging watermark table and port (module: charging worker)

- [ ] **1b.1** New migration
  `internal/charging/db/migrations/20260906000001_add_mirror_watermarks.sql`
  — exact SQL (including both `COMMENT ON` statements) from design.md "Full
  schema." Acceptance: `make migration-guard` passes; the filename sorts
  after `20260903000004_add_session_status.sql`; `grep -c "CREATE TABLE
  charging.mirror_watermarks" internal/charging/db/migrations/20260906000001_add_mirror_watermarks.sql`
  returns `1`; no `tesla_id` or `source` column anywhere in the file.
  `depends_on`: 0.1 · `parallel_ok`: with telemetry's Wave 1a

- [ ] **1b.2** `internal/charging/db/query.sql` — add `GetMirrorWatermark`
  and `UpsertMirrorWatermark`, exact SQL from design.md "The two new sqlc
  queries." Acceptance: `grep -c "name: GetMirrorWatermark"
  internal/charging/db/query.sql` returns `1`; `grep -c "name:
  UpsertMirrorWatermark" internal/charging/db/query.sql` returns `1`.
  `depends_on`: 1b.1 · `parallel_ok`: no

- [ ] **1b.3** Run `make sqlc` to regenerate `internal/charging/db/{models.go,query.sql.go}`
  against 1b.1/1b.2. Never hand-edit the generated files. Acceptance: `go
  build ./internal/charging/...` succeeds; `models.go` gains a
  `MirrorWatermark` struct with `AccountID`/`SourceUpdatedAt`/`CreatedAt`/`UpdatedAt`
  fields and no `TeslaID`/`Source` field.
  `depends_on`: 1b.2 · `parallel_ok`: no

- [ ] **1b.4** `internal/charging/charging.go` — declare the
  `MirrorWatermarkStore` interface and `NewMirrorWatermarkStore` constructor,
  doc comments verbatim from design.md "Interfaces."
  `depends_on`: 1b.3 · `parallel_ok`: no

- [ ] **1b.5** New `internal/charging/mirror_watermark.go` — the concrete
  `mirrorWatermarkStore` type and `newMirrorWatermarkStore` internal
  constructor, mirroring `session_writer.go`'s exact pattern.
  `MirrorWatermark` translates `pgx.ErrNoRows` to `(time.Time{}, nil)`
  (design.md D3); `AdvanceMirrorWatermark` performs a plain upsert with no
  row-count validation (design.md D5). `pgtype` stays confined to this
  file. Acceptance: `go build ./internal/charging/...` succeeds; `var _
  MirrorWatermarkStore = (*mirrorWatermarkStore)(nil)` compiles.
  `depends_on`: 1b.4 · `parallel_ok`: no

- [ ] **1b.6** New `internal/charging/db_mirror_watermark_integration_test.go`
  covering T-cw-1 through T-cw-6 from design.md's Test Contract, verbatim
  expected values. Read back with direct SQL or `MirrorWatermarkStore`
  itself (no `pgtype` in assertions, per `internal/charging/AGENTS.md`).
  Acceptance: `grep -c "^func TestMirrorWatermark" internal/charging/*_test.go`
  returns at least `6`; `gofmt -l` on the file prints nothing; `go vet
  ./internal/charging/...` succeeds.
  `depends_on`: 1b.5 · `parallel_ok`: no

## Wave 2 — docs (module workers, granted AGENTS.md + spec paths)

- [ ] **2.1 [doc: telemetry worker]** `internal/telemetry/AGENTS.md` — add a
  short section describing `SuperchargerHistoryByAccountUpdatedSince`: its
  signature, that it is the only updated-since method that can return a
  `tesla_id IS NULL` row, and why (mirrors design.md "Interfaces").
  Acceptance: `grep -c "SuperchargerHistoryByAccountUpdatedSince"
  internal/telemetry/AGENTS.md` returns at least `1`.
  `depends_on`: 1a.4 · `parallel_ok`: with 2.2

- [ ] **2.2 [doc: charging worker]** `internal/charging/AGENTS.md` — add a
  short section describing `charging.mirror_watermarks`, the
  `MirrorWatermarkStore` port, and the epoch/never-advance-on-empty-read
  rule (D5), under a new subsection alongside the module's other
  Supercharger-session documentation. Acceptance: `grep -c
  "mirror_watermarks" internal/charging/AGENTS.md` returns at least `1`;
  `grep -c "MirrorWatermarkStore" internal/charging/AGENTS.md` returns at
  least `1`.
  `depends_on`: 1b.5 · `parallel_ok`: with 2.1

- [ ] **2.3** Confirm this change's own `specs/telemetry/spec.md`,
  `specs/charging/spec.md`, and `specs/charge-session-log/spec.md` deltas
  (already written in this change folder) still read coherently against
  the CURRENT main specs at merge time. Acceptance: `openspec validate
  RM44-platform-add-mirror-watermark --strict` passes.
  `depends_on`: — · `parallel_ok`: with 2.1, 2.2

## Wave 3 — cross-module wiring (leader, after both module groups complete)

- [ ] **3.1 [leader]** `internal/app/app.go` — add a `mirrorWatermarks
  charging.MirrorWatermarkStore` field to `processor` and thread it through
  the existing constructor, alongside `sessionWriter`/`superchargerHistoryReader`.
  Wire `charging.NewMirrorWatermarkStore(pool)` wherever
  `charging.NewSessionWriter(pool)` is already constructed (`cmd/web`'s
  wiring). Acceptance: `go build ./cmd/web/...` succeeds.
  `depends_on`: 1b.5 · `parallel_ok`: no

- [ ] **3.2 [leader]** `internal/app/processor.go` — rewrite
  `processChargingData`'s body to the exact shape in design.md
  "Cross-Module Wiring": read the watermark, call
  `SuperchargerHistoryByAccountUpdatedSince(accountID, cursor - 24h)`,
  `continue` on zero rows WITHOUT advancing the watermark (D5), map and
  mirror as today, then advance the watermark to the max `updated_at`
  observed — never to `now()`. Add the new `24 * time.Hour` overlap
  constant local to `internal/app` (do not import `internal/analytics`'s
  private constant). Acceptance: `grep -c "AdvanceMirrorWatermark"
  internal/app/processor.go` returns `1`; `grep -c "SuperchargerHistoryByAccount(ctx"
  internal/app/processor.go` returns `0` (the old unbounded call is fully
  replaced, not left as dead code); `go build ./internal/app/...` succeeds.
  `depends_on`: 3.1, 1a.4 · `parallel_ok`: no

- [ ] **3.3 [leader]** `internal/app/processor_test.go` — add
  `SuperchargerHistoryByAccountUpdatedSince` to `fakeSuperchargerHistoryReader`
  (design.md "Findings" — this fake fully implements the interface today
  and breaks compilation otherwise), and add a new
  `fakeMirrorWatermarkStore` test double for `charging.MirrorWatermarkStore`.
  Acceptance: `go vet ./internal/app/...` succeeds.
  `depends_on`: 3.2 · `parallel_ok`: no

- [ ] **3.4 [leader]** Extend `internal/app/processor_test.go` with T-app-1,
  T-app-2, and T-app-3 from design.md's Test Contract, verbatim expected
  values, against the new `processChargingData` shape and the fakes from
  3.3. Acceptance: `grep -c "^func TestProcessChargingData_.*Watermark"
  internal/app/processor_test.go` returns at least `3`; `gofmt -l
  internal/app/processor_test.go` prints nothing; `go vet
  ./internal/app/...` succeeds.
  `depends_on`: 3.3 · `parallel_ok`: no

## Wave 4 — verification (assistant-run signals, then owner-run suite)

- [ ] **4.1** Run and report, per module: `go build ./...`, `go vet ./...`,
  `gofmt -l internal/telemetry internal/charging internal/app`, `make
  migration-guard`, `make boundary-guard`. This tier's own definition of
  done: `charging.mirror_watermarks` exists in the new migration;
  `telemetry.SuperchargerHistoryReader` has 5 methods, not 4;
  `processChargingData` no longer calls `SuperchargerHistoryByAccount`
  with a bare `0` limit. Per the Test-Execution-Policy, never run `go test
  ./...`, `make test`, `make test-with-db`, or `make check`.
  `depends_on`: 1a.7, 1b.6, 2.1, 2.2, 2.3, 3.4 · `parallel_ok`: no

- [ ] **4.2** Hand off to the owner the exact commands to run and report:
  `go test ./internal/telemetry/... ./internal/charging/... ./internal/app/...`.
  Until the owner reports these pass, this tier's implementation status is
  **awaiting-user-verification**, never "done." After deploy, also hand
  off the owner-only check from proposal.md's "Testing" section (the
  query-log confirmation that the second post-deploy night shows a bounded
  `since=` value, not an absent date bound).
  `depends_on`: 4.1 · `parallel_ok`: no
