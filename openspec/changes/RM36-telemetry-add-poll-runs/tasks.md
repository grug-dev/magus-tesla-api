# Tasks — RM36-telemetry-add-poll-runs

Ownership: every task below is **[module: telemetry worker]** — inside
`internal/telemetry/` only, plus the two doc tasks in Wave 6 (`AGENTS.md` and root
`README.md`, both explicitly in this tier's granted scope). No task touches
`internal/app`, `cmd/poller`, or `internal/tesla` — see design.md D6/D8 for why the
design was shaped specifically to avoid needing to.

**Hard ordering constraints (see design.md for the "why" behind each):**

- **Wave 1 (migration) → Wave 2 (sqlc-dependent Go types) and → Wave 5 (DB
  integration tests).** `RunWriter`'s concrete implementation and the DB-backed test
  cannot compile before `telemetrydb.InsertPollRun`/`InsertPollRunParams` exist.
- **Wave 2 (types) and Wave 3 (counting decorator) → Wave 4 (service.go wiring).**
  `CollectAll` needs both `CycleReport`'s new fields and `callCounter` to exist
  before it can be wired to use them.
- **Wave 4 → Wave 5's offline fixtures.** Fixtures 1–4 (design.md Test Contract)
  exercise the wired `CollectAll`, not the decorator or types in isolation.
- **Wave 5 (all tests) is the FINAL implementation wave** — its DB-backed fixture
  (5a/5b/5c) cannot compile before Wave 1's migration and regenerated sqlc types
  exist, and its offline fixtures (1–4) cannot compile before Wave 4 lands.
- **Wave 6 (docs) describes the POST-change state** — a content dependency on
  Waves 1–5, not a compile one.
- **`make sqlc` must be re-run after Wave 1's migration** before any Wave 2 task
  that references `telemetrydb.InsertPollRunParams` is implemented.

---

## Wave 1 — Migration + sqlc

- [ ] **1.1** `internal/telemetry/db/migrations/20260830000001_add_poll_runs.sql` —
  create `poll_runs` exactly as specified in design.md "Database Changes" (full DDL,
  every column, every `COMMENT ON`, the `+goose Down` dropping the table). Add this
  migration's directory entry is already covered by the existing
  `internal/telemetry/db/migrations` entry in `MIGRATIONS_DIRS` — no `Makefile`
  change needed.
  `depends_on`: — · `parallel_ok`: no

- [ ] **1.2** `internal/telemetry/db/query.sql` — add the `InsertPollRun :exec` query
  exactly as specified in design.md "New sqlc query", including its doc comment. Run
  `make sqlc` to regenerate `telemetrydb` (`InsertPollRun`,
  `InsertPollRunParams`).
  `depends_on`: 1.1 · `parallel_ok`: no

---

## Wave 2 — Domain types and the `RunWriter` port

> Purely additive Go declarations. `PollRun`/`RunWriter`/`NewRunWriter`'s
> *declarations* do not need the sqlc types; only `runWriter`'s *implementation*
> (2.3) does.

- [ ] **2.1** `internal/telemetry/telemetry.go` — add the `PollRun` struct exactly as
  specified in design.md's "Go-Level Seam Summary" (17 fields: `RunID`,
  `TriggeredBy`, `StartedAt`, `FinishedAt`, `DurationSeconds`, `AccountsAttempted`,
  `AccountsSucceeded`, `AccountsFailed`, `VehiclesAttempted`, `VehiclesSucceeded`,
  `FailuresAsleepTimeout`, `FailuresUnauthorized`, `FailuresAPIError`,
  `TeslaAPICalls`, `ChargingSessionsUpserted`, `ChargingFetchFailures`,
  `ConfigCaptureFailures`), with the doc comment explaining the "no read port yet"
  and "written exactly once" contracts (design D3/D5).
  `depends_on`: — · `parallel_ok`: with 2.2, 2.4

- [ ] **2.2** `internal/telemetry/telemetry.go` — add `CycleReport.TeslaAPICalls`,
  `.AccountsAttempted`, `.AccountsSucceeded`, `.AccountsFailed` (all `int`) and
  `.Duration time.Duration` (design D6 — doc comment MUST state it is left zero by
  `CollectAll` and populated only by the caller after the full run is measured).
  `depends_on`: — · `parallel_ok`: with 2.1, 2.4

- [ ] **2.3** `internal/telemetry/run_writer.go` (new file) — the `runWriter`
  concrete type + `RecordRun` implementation (design D12), mapping `PollRun` fields
  to `telemetrydb.InsertPollRunParams` at the DB boundary (reuse the existing
  `runIDToPgUUID` helper from `service.go` for `run_id`; every other field binds as
  a plain non-nullable value since every `PollRun` field is always populated by
  contract — no nullable-pgtype mapping needed anywhere in this file). Compile-time
  assertion `var _ RunWriter = (*runWriter)(nil)`.
  `depends_on`: 1.2, 2.4 · `parallel_ok`: no

- [ ] **2.4** `internal/telemetry/telemetry.go` — add the `RunWriter` interface
  (`RecordRun(ctx context.Context, run PollRun) error`, doc comment per design.md's
  "Go-Level Seam Summary" quoting D3/D1/D11) and `NewRunWriter(pool *pgxpool.Pool)
  RunWriter` (forward-declared, calling into 2.3's constructor — mirrors
  `NewSuperchargerReader`'s forward-declaration pattern already in this file).
  `depends_on`: 2.1 · `parallel_ok`: with 2.2

---

## Wave 3 — Tesla API call-counting decorator

- [ ] **3.1** `internal/telemetry/call_counter.go` (new file) — the `callCounter`
  type (design D9): explicit, non-embedding implementations of all four
  `tesla.VehicleService` methods (`ListVehicles`, `WakeUp`, `VehicleData`,
  `ChargingHistory`), each incrementing `calls` unconditionally before delegating to
  `inner`. `newCallCounter(inner tesla.VehicleService) *callCounter`. Compile-time
  assertion `var _ tesla.VehicleService = (*callCounter)(nil)`.
  `depends_on`: — · `parallel_ok`: with Wave 2

- [ ] **3.2** `internal/telemetry/call_counter_test.go` (new file) — offline unit
  test: wrap a minimal fake `tesla.VehicleService` in `callCounter`, call each of
  the four methods a distinct number of times (including at least one call that
  returns an error), assert `calls` equals the total regardless of success/failure.
  `depends_on`: 3.1 · `parallel_ok`: no

---

## Wave 4 — Wire `service.go`: threading the counter, account counters, whole-cycle plumbing

- [ ] **4.1** `internal/telemetry/service.go` — thread `tsla tesla.VehicleService` as
  an explicit parameter through `collectAccount`, `listStates`, `collectVehicle`,
  `attemptVehicle`, and `collectChargingHistory` (design D10), replacing every
  internal read of `s.tsla` in those five methods with the parameter. `CollectAll`
  constructs `counted := newCallCounter(s.tsla)` once per call and passes `counted`
  down through `collectAccount`; `waitUntilOnline`/`isOnline` need no change (they
  already take `svc tesla.VehicleService` explicitly — `attemptVehicle` passes
  `counted` through unchanged).
  `depends_on`: 2.2, 3.1 · `parallel_ok`: no

- [ ] **4.2** `internal/telemetry/service.go` — in `CollectAll`, set
  `report.AccountsAttempted = len(byAccount)` immediately after building
  `byAccount`; after the account loop, set
  `report.AccountsSucceeded = report.AccountsAttempted - report.AccountsFailed`;
  set `report.TeslaAPICalls = counted.calls` immediately before returning. In
  `collectAccount`, increment `report.AccountsFailed++` in both existing
  whole-account short-circuit branches (the `AccessTokenFor` failure branch and the
  `errors.Is(err, tesla.ErrUnauthorized)` branch from `listStates`) — no other
  branch touches this counter (roadmap D4).
  `depends_on`: 4.1 · `parallel_ok`: no

---

## Wave 5 — Tests (offline first, DB-backed last — cannot compile before Waves 1–4)

- [ ] **5.1** `internal/telemetry/service_test.go` — add the four offline fixtures
  from design.md's Test Contract (Fixtures 1–4): the mixed online/asleep-vehicle
  call-count fixture, the `AccessTokenFor`-failure account fixture, the
  `ListVehicles`-unauthorized account fixture, and the whole-cycle
  `AllRegisteredVehicles`-failure fixture. Each asserts the exact `CycleReport`
  values design.md specifies, including the `TeslaAPICalls` cross-check against
  `fakeTesla`'s own independent `listCalls`/`wakeCalls`/`dataCalls` counters.
  `depends_on`: 4.2 · `parallel_ok`: no

- [ ] **5.2** `internal/telemetry/report_test.go` — extend (or add alongside
  `TestFormatFailures`) an assertion that `LogCycle`'s relabeled line prints
  `vehicles_attempted`/`vehicles_succeeded` (not bare `attempted`/`succeeded`) plus
  the new account/API-call/duration fields — capture `log.Printf` output via
  `log.SetOutput` to a buffer for the duration of the test, matching this file's
  existing style.
  `depends_on`: 4.2 · `parallel_ok`: with 5.1

- [ ] **5.3** `internal/telemetry/db_poll_run_integration_test.go` (new file,
  `DATABASE_URL`-gated via the existing `testdb`/`TestMain` pattern in this
  package) — the three `RunWriter.RecordRun` fixtures from design.md (5a: a normal
  successful run's full 17-field round-trip via direct SQL `SELECT`; 5b: the
  step-1 whole-cycle-failure all-zero-counts trace; 5c: a duplicate `run_id`
  fails on the second call and leaves the first row unmodified).
  `depends_on`: 1.2, 2.3 · `parallel_ok`: with 5.1, 5.2

---

## Wave 6 — Docs (post-change state; content-dependent on Waves 1–5, not a compile dependency)

- [ ] **6.1** `internal/telemetry/AGENTS.md` — add `poll_runs` to the "Data
  ownership" section as a fifth owned table: one row per `run_id`, written once by
  `RunWriter.RecordRun`, no read port yet (backlog), schema/rationale pointer to
  this archived change once archived.
  `depends_on`: 1.1, 2.4 · `parallel_ok`: with 6.2

- [ ] **6.2** Root `README.md` — add a `poll_runs` row to the "Database tables by
  module" table, in the existing `internal/telemetry` block (after
  `supercharger_sessions`), one sentence describing what it stores and that it has
  no reader yet.
  `depends_on`: 1.1 · `parallel_ok`: with 6.1

---

## Verification (owner-run; see Test-Execution-Policy)

After every wave above is implemented:

```
go build ./...
go vet ./...
gofmt -l .
make sqlc
```

Owner-only, once ready: `go test ./internal/telemetry/...` (Wave 5.3 self-skips
without `DATABASE_URL`/Docker — see `internal/telemetry/AGENTS.md` "Testing notes"
for how to confirm it actually ran rather than skipped).
