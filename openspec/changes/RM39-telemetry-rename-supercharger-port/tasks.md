## Ownership

`[telemetry]` — inside the `internal/telemetry` module worker's sandbox.
`[leader-owned]` — outside `internal/telemetry`; per this project's pipeline, a module worker
is sandboxed to exactly one module plus explicitly granted paths (`ai/agentic-workflow.md`
§pipeline), so cross-module edits are either explicitly granted to the `telemetry` worker for
this change or done by the leader/a separately-dispatched worker. Listed here so the whole
change's scope is visible in one place, matching tier 4's precedent.

## Dependencies / parallelism

**This is one atomic Go compile unit (design.md D4) — treat the numbering below as a required
order, not a menu of independent, parallelizable work.** Renaming an exported interface and
its methods breaks every real consumer simultaneously; there is no correct intermediate state
where only some of T1–T9 have landed and `go build ./...` still passes. Two exceptions, called
out per-task below: T5 (a comment, touches no symbol another task depends on) and the docs/KB
tasks T12–T13 (touch no Go file at all) may run in parallel with the compile-chain tasks or in
either order relative to them.

Within the compile chain, T1 must land before anything that names the port
(`SuperchargerReader`/its methods/its constructor); T2–T4 must land before the `_test.go`
sweeps (T7–T9) that exercise them; nothing after T1 can be verified with `go build`/`go vet`
until the full chain (T1–T9) has landed, because a partial rename leaves undefined identifiers
on both sides of the interface boundary.

## T1. Rename the public port — `internal/telemetry/telemetry.go` — `[telemetry]` — no dependencies, must land first

- [x] T1.1 Rename `type SuperchargerReader interface { ... }` → `type SuperchargerHistoryReader interface { ... }`.
- [x] T1.2 Rename the interface's four methods: `SuperchargerSessionsByAccount` →
  `SuperchargerHistoryByAccount`, `SuperchargerSessionsByVehicle` →
  `SuperchargerHistoryByVehicle`, `SuperchargerSessionsByVehicleBetween` →
  `SuperchargerHistoryByVehicleBetween`, `SuperchargerSessionsByVehicleUpdatedSince` →
  `SuperchargerHistoryByVehicleUpdatedSince`. Signatures, parameter lists, and doc comments'
  behavioral content are unchanged — update only the identifier and any comment prose that
  names the old method/interface name (design.md D2).
- [x] T1.3 Rename the constructor `func NewSuperchargerReader(pool *pgxpool.Pool) SuperchargerReader`
  → `func NewSuperchargerHistoryReader(pool *pgxpool.Pool) SuperchargerHistoryReader`, including
  its body's call to the (still-to-be-renamed in T2) internal constructor helper.
- [x] T1.4 Sweep this file's own comments naming the old identifiers outside the declarations
  above (e.g. the `RunWriter`/`GapWriter`-adjacent comments at ~L658, ~L674 that say "mirroring
  this module's own SuperchargerReader" / "mirrors NewSuperchargerReader's own pattern") —
  update to the new names.

## T2. Rename the implementation — `internal/telemetry/reader.go` — `[telemetry]` — depends on T1

- [x] T2.1 Rename `type superchargerReader struct { ... }` → `type superchargerHistoryReader struct { ... }`.
- [x] T2.2 Rename `func newSuperchargerReaderImpl(pool *pgxpool.Pool) *superchargerReader` →
  `func newSuperchargerHistoryReaderImpl(pool *pgxpool.Pool) *superchargerHistoryReader`.
- [x] T2.3 Update the compile-time assertion `var _ SuperchargerReader = (*superchargerReader)(nil)`
  → `var _ SuperchargerHistoryReader = (*superchargerHistoryReader)(nil)`.
- [x] T2.4 Rename all four method receivers (`func (r *superchargerReader) SuperchargerSessionsByAccount(...)`
  etc.) to match T1.2's new method names on the new receiver type
  `*superchargerHistoryReader`, including each method's own reference to
  `rowToSuperchargerSession` (T3) in its body.
- [x] T2.5 Sweep this file's doc comments (the `// --- Source B: SuperchargerReader ---` section
  header and per-method "implements SuperchargerReader" comments) to the new names.

## T3. Rename the mapper — `internal/telemetry/mapping.go` — `[telemetry]` — depends on T1, parallel-ok with T2

- [x] T3.1 Rename `func rowToSuperchargerSession(r telemetrydb.SuperchargerHistory) SuperchargerHistory`
  → `func rowToSuperchargerHistory(...)`. Its doc comment (which already correctly says it
  converts a `telemetrydb.SuperchargerHistory` row) updates its own name and its
  "for the SuperchargerReader read path" phrase to name the new port.
- [x] T3.2 Update every call site of `rowToSuperchargerSession` (four, one per method in
  `reader.go`, covered by T2.4) to the new name — verify no call site is missed by grepping
  `internal/telemetry/*.go` for the old name after T2 and T3 both land.

## T4. Rename the write-seam helper — `internal/telemetry/service.go` — `[telemetry]` — no dependency on T1 (a separate, `Collector`-side interface), parallel-ok with T1–T3

- [x] T4.1 Rename the unexported `store` interface's method `upsertSuperchargerSession(ctx
  context.Context, s SuperchargerHistory) error` → `upsertSuperchargerHistory(...)`, and its
  doc comment "upsertSuperchargerSession is the Source B write seam" → the new name.
- [x] T4.2 Rename the `dbStore` implementation `func (d *dbStore) upsertSuperchargerSession(...)`
  → `upsertSuperchargerHistory`, and its own doc comment.
- [x] T4.3 Update the one call site, `s.store.upsertSuperchargerSession(ctx, domainSession)` →
  `s.store.upsertSuperchargerHistory(ctx, domainSession)`.

## T5. Comment-only sweep — `internal/telemetry/run_writer.go` — `[telemetry]` — independent, parallel-ok with everything

- [x] T5.1 Update the two comments that name the old vocabulary for illustrative comparison
  ("mirroring SuperchargerReader's and internal/analytics' gapWriter's identical precedent";
  "mirroring newSuperchargerReaderImpl's and newGapWriter's identical pattern") to the new
  names. No code in this file changes — `runWriter`/`RunWriter` are unrelated to this rename.

## T6. `internal/telemetry/db/query.sql` comments + regeneration — `[telemetry]` — depends on nothing, parallel-ok with T1–T5

- [x] T6.1 In `internal/telemetry/db/query.sql`, update the two `-- name:` block doc comments
  that name the old port for cross-reference: the `SuperchargerHistoryByVehicleBetween` query's
  "Used by SuperchargerReader.SuperchargerSessionsByVehicleBetween..." and the
  `SuperchargerHistoryByVehicleUpdatedSince` query's "Used by
  SuperchargerReader.SuperchargerSessionsByVehicleUpdatedSince..." → name the new port/method
  (design.md D3). **No SQL statement, parameter, or `-- name:` line changes** — the 5 query
  names are already `SuperchargerHistoryBy*`/`UpsertSuperchargerHistory` from tier 4.
- [x] T6.2 Run `sqlc generate` (equivalent to `make sqlc`). Confirm `internal/telemetry/db/models.go`
  has **zero** diff (this task changes no schema and no query name) and
  `internal/telemetry/db/query.sql.go` only reflects the 2 comment edits from T6.1 — diff the
  regenerated file and confirm nothing else moved.

## T7. FINAL WAVE — telemetry's own `_test.go` files — `[telemetry]` — depends on T1–T4

- [x] T7.1 `reader_test.go`: rename `fakeReadStore.upsertSuperchargerSession`,
  `fakeHistoryStore.upsertSuperchargerSession`, `fakeBetweenStore.upsertSuperchargerSession` →
  `upsertSuperchargerHistory` (interface satisfaction for T4's renamed `store` interface — each
  still panics with its existing message, updated to name the new symbol). No assertion or
  expected-value change (design.md Test Contract).
- [x] T7.2 `service_test.go`: rename `fakeStore.upsertSuperchargerSession` →
  `upsertSuperchargerHistory` and its surrounding comments (`upsertedSessions`/`upsertErr`
  field names may stay — they name what the field holds, not the renamed method). No
  assertion or expected-value change.
- [x] T7.3 `db_supercharger_integration_test.go`: update all `newSuperchargerReaderImpl(pool)` →
  `newSuperchargerHistoryReaderImpl(pool)` and `.SuperchargerSessionsByAccount(...)` →
  `.SuperchargerHistoryByAccount(...)` call sites (design.md Test Contract lists this file).
  Update `t.Fatalf` format strings that echo the old method name for accuracy. No
  row/expected-value change.
- [x] T7.4 `db_supercharger_between_integration_test.go`: same shape —
  `newSuperchargerReaderImpl` → `newSuperchargerHistoryReaderImpl`,
  `.SuperchargerSessionsByVehicleBetween(...)` → `.SuperchargerHistoryByVehicleBetween(...)`.
  Test function names (`TestSuperchargerSessionsByVehicleBetween_*`) MAY be renamed to
  `TestSuperchargerHistoryByVehicleBetween_*` for vocabulary consistency — optional, not
  required for compilation; do it if touching the file anyway. No expected-value change.
- [x] T7.5 `db_supercharger_battery_pct_integration_test.go`: same shape as T7.3/T7.4, plus
  rename the test function `TestStore_SuperchargerReader_ReturnsBatteryPctTrioAndSnapshot` →
  `TestStore_SuperchargerHistoryReader_ReturnsBatteryPctTrioAndSnapshot` (design.md D2 — this
  one directly names the old port in its own test name). No expected-value change.
- [x] T7.6 Grep `internal/telemetry/*.go` for the 5 old identifiers
  (`SuperchargerReader\b`, `SuperchargerSessionsBy`, `NewSuperchargerReader`,
  `rowToSuperchargerSession`, `upsertSuperchargerSession`) after T1–T7 land and confirm zero
  hits inside `internal/telemetry` (this module has no gateway/charging-style name collision,
  so — unlike the repo-wide grep in T14 — zero hits IS the correct bar here).

## T8. FINAL WAVE — `internal/app` (the port's only remaining real caller, design.md D1) — `[leader-owned]` — depends on T1–T4

- [x] T8.1 `internal/app/app.go`: rename the constructor parameter `superchargerReader
  telemetry.SuperchargerReader` → `superchargerHistoryReader telemetry.SuperchargerHistoryReader`
  (the type per T1; the local parameter name per design.md D2's consistency argument, not a
  compiler requirement), and its comment mentioning `superchargerReader`.
- [x] T8.2 `internal/app/processor.go`: rename the struct field `superchargerReader
  telemetry.SuperchargerReader` → `superchargerHistoryReader telemetry.SuperchargerHistoryReader`,
  and its one call site `p.superchargerReader.SuperchargerSessionsByAccount(ctx, v.AccountID, 0)`
  → `p.superchargerHistoryReader.SuperchargerHistoryByAccount(ctx, v.AccountID, 0)`.
- [x] T8.3 `internal/app/processor_test.go`: rename `fakeSuperchargerReader`'s four methods to
  match T1.2 (`SuperchargerHistoryByAccount` etc.), update the compile-time assertion `var _
  telemetry.SuperchargerReader = fakeSuperchargerReader{}` → `var _
  telemetry.SuperchargerHistoryReader = fakeSuperchargerReader{}` (the fake's own type name
  MAY optionally follow to `fakeSuperchargerHistoryReader` — not required), and its doc comment
  "fakeSuperchargerReader satisfies telemetry.SuperchargerReader". No assertion or
  expected-value change in any `TestProcessVehicleData_*` test.
- [x] T8.4 **Do NOT touch `internal/gateway` or `internal/charging`'s own `SuperchargerReader`/
  `SessionReader` identifiers while doing this sweep** — confirm before starting that any file
  edited under T8 imports `internal/telemetry` (only `app.go`, `processor.go`,
  `processor_test.go` do; `internal/gateway/**` and `cmd/web/main.go` do not and must not be
  touched — design.md D1).

## T9. FINAL WAVE — `cmd/poller/main.go` — `[leader-owned]` — depends on T1

- [x] T9.1 Rename the local variable `superchargerReader := telemetry.NewSuperchargerReader(pool)`
  → `superchargerHistoryReader := telemetry.NewSuperchargerHistoryReader(pool)`, its downstream
  usage (passed into `app.New(...)` or equivalent), and the comment above it ("superchargerReader
  is telemetry's, and stays: the mirror step reads...").

## T10. Comment-only accuracy fix — `internal/charging/charging.go` — `[leader-owned]` — depends on T1, independent of T8/T9

- [x] T10.1 Update the genuine prose mentions of telemetry's port, confirmed by design.md D1.3
  to be about `telemetry.SuperchargerReader`/its methods (NOT the file's other
  `Deps.SuperchargerReader` mentions, which describe the **gateway's own**,
  `charging.SessionReader`-typed field and must NOT change): the comment near
  `SessionReader`'s doc ("mirroring telemetry.SuperchargerSessionsByVehicleBetween's
  end.AddDate(0,0,1)/half-open contract exactly") and the comment near
  `SuperchargerSessionAnalyticsReader`'s doc ("not telemetry.SuperchargerReader's
  updated_at-ordering choice"). No code, no test, no behavior change — comments only.

## T11. Comment-only accuracy fixes — `internal/analytics` — `[leader-owned]` — depends on T1, independent of T8–T10

- [x] T11.1 `analytics.go`: update the comment "mirroring NewSuperchargerReader's identical
  pattern, design B6.3 of..." to the new constructor name.
- [x] T11.2 `gap_writer.go`: update "SuperchargerReader already established the precedent that
  a port not..." and "(mirroring newSuperchargerReaderImpl's identical pattern, design..." to
  the new names.
- [x] T11.3 `db_integration_test.go`: update the historical/comparative comment at ~L31
  ("telemetry.NewReader/NewSuperchargerReader/charging.NewReader...") and ~L398 ("this table at
  all — upsertSuperchargerSession is unexported, reachable...") to the new names. **Confirm
  before editing** that `fakeSuperchargerReader`/`recordingSuperchargerReader` in this file and
  in `reader_test.go`/`recalculate_test.go` satisfy `charging.SuperchargerSessionAnalyticsReader`
  (verified by their own `var _ charging.SuperchargerSessionAnalyticsReader = ...` assertions),
  NOT telemetry's port — these types and their tests are OUT OF SCOPE and must not be renamed
  (design.md D1.2). No test code, assertion, or expected-value change anywhere in this module.

## T12. Module docs — `internal/telemetry/AGENTS.md` — `[telemetry]` — depends on T1–T7, parallel-ok with T8–T11

- [x] T12.1 Replace the `SuperchargerReader` section's "Deliberate half-state (design D6,
  tier 4) — do not 'fix' this" note (the one instructing future agents not to rename
  `SuperchargerSessionsBy…`/`NewSuperchargerReader`/`superchargerReader`/
  `rowToSuperchargerSession`/`upsertSuperchargerSession`) with the completed state: the port is
  now `SuperchargerHistoryReader`, its methods are `SuperchargerHistoryBy*`, its constructor is
  `NewSuperchargerHistoryReader`, and it fully matches the table/domain-type vocabulary. Record
  this change (`RM39-telemetry-rename-supercharger-port`) as the tier that closed it, mirroring
  how other renamed-vocabulary sections in this file read after their own tier landed.
- [x] T12.2 Update every other mention of the old port/method/helper names elsewhere in this
  file (the "Public interface (the port)" intro, any cross-reference in "Data ownership" or
  "Testing notes").

## T13. Repo docs + knowledge base — `[leader-owned]` — depends on T1–T11, parallel-ok with T12

- [x] T13.1 Command that produced the file list below (re-run it; do not trust the counts —
  same discipline as tier 4's T10.1):
  ```
  grep -rln "SuperchargerReader\|NewSuperchargerReader" kkpa/context/ 2>/dev/null
  ```
- [x] T13.2 Update `kkpa/context/architecture/telemetry-ingest-only.md` and
  `kkpa/context/architecture/nightly-cycle.md` — both name telemetry's port genuinely (not the
  gateway's unrelated field) at multiple lines, and both already independently record this
  design's D1 finding ("the only remaining caller repo-wide") — update the port/method/
  constructor names to match T1's rename while preserving that finding's substance.
- [x] T13.3 Update `kkpa/context/pending-spec-to-sync/telemetry.md` (a staged, not-yet-applied
  proposal that also names the old port and its half-renamed-state note) to the new names.
- [x] T13.4 **Do NOT edit** `kkpa/context/workflows/supercharger-stats-read.md` — its
  `SuperchargerReader` mention is explicitly the gateway's own field ("field NAME kept from the
  telemetry era; the TYPE is the charging port since RM30") — out of scope (design.md D5). **Do
  NOT edit** anything under `kkpa/context/pending-spec-to-sync/applied/` — that is a historical
  sync record, never edited after the fact (same convention as archived OpenSpec changes).

## T14. Verification — `[telemetry]` for the assistant-runnable half, `[leader-owned]` for the repo-wide grep — depends on T1–T13

- [x] T14.1 Claude-runnable signals, repo-wide: `go build ./...`, `go vet ./...`, `gofmt -l`
  (empty output). All three MUST be clean before this change is reported anything but
  `awaiting-user-verification` on its testable tasks.
- [x] T14.2 `make boundary-guard` passes with zero `// boundary:allow:` escape hatches — this
  tier does not touch `internal/gateway` at all (design.md D1), so the guard's zero-hit state
  from tier 4 is unaffected; re-run it to confirm rather than assume.
- [x] T14.3 Repo-wide grep for the 5 old telemetry-side identifiers, **triaged by file, not
  treated as a zero-hits gate** (design.md D1 — a blind grep can never return zero here, because
  `internal/gateway`'s and `internal/charging`'s own, permanently different
  `SuperchargerReader`/`SessionReader`/`SuperchargerSessionAnalyticsReader` identifiers will
  always match a substring search):
  ```
  grep -rn "SuperchargerReader\|NewSuperchargerReader\|superchargerReader\|rowToSuperchargerSession\|upsertSuperchargerSession" \
    --include='*.go' internal cmd kkpa/context 2>/dev/null | grep -v '.claude/worktrees'
  ```
  Confirm every remaining hit is one of the two known-permanent, out-of-scope identifiers named
  in design.md D1.1 (`internal/gateway/**`, `cmd/web/main.go`'s one wiring line) or D1.2
  (`internal/analytics`'s `charging.SuperchargerSessionAnalyticsReader`-satisfying fakes) — any
  hit that is NOT one of those is a missed real reference and must be fixed before this task is
  marked done.
- [x] T14.4 `openspec validate RM39-telemetry-rename-supercharger-port --strict` passes, and
  every task above is checked off with `tasks.md` reflecting real, current status (per
  `openspec/config.yaml`'s tasks rule).
- [x] T14.5 **Report the exact test-suite commands the owner must run.** Per this project's
  Test-Execution-Policy, no task above may be reported `done` on the strength of a test run
  Claude performed — report `awaiting-user-verification` for every task with test coverage and
  hand back:
  ```
  go test ./internal/telemetry/... ./internal/app/... ./internal/analytics/... ./internal/charging/...
  ```
  (No `DATABASE_URL`/Docker-gated behavior changes in this tier — every affected test is either
  pure/offline or a `DATABASE_URL`-gated integration test whose SQL and expected rows are
  unchanged, per design.md's Test Contract — but the full path is given so the owner does not
  have to guess which packages this rename touches.)
