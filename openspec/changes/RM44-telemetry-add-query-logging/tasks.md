# Tasks — RM44-telemetry-add-query-logging

Ownership: every task below is **[module: telemetry worker]** — inside
`internal/telemetry/` only. No task touches `internal/tesla`, `internal/charging`,
`internal/analytics`, `internal/app`, or `internal/gateway` (design.md confirms none of
them need to change for this tier).

**Hard ordering constraints (see design.md for the "why" behind each):**

- **Wave 1 (query deletion + sqlc regen) → Wave 4 (test rewrites).** The 3 test files
  cannot compile with the old `q.ListSnapshotsByVehicle`/`q.ListPollAttemptsByVehicle`
  calls once the query.sql entries are gone; their raw-SQL rewrite must land in the same
  pass as the deletion, or the package fails to build in between. Treat Wave 1 + Wave 4
  as one atomic unit if executed by different sessions.
- **Wave 2 (`query_log.go` + `call_counter.go` extension) → Wave 3 (production wiring).**
  The wiring changes reference `newLoggingStore`/`newLoggingReader`/
  `newLoggingSuperchargerHistoryReader`/`newLoggingRunWriter`, which must exist first.
- **Wave 2 → Wave 5 (tests).** `query_log_test.go`/the `call_counter_test.go` addition
  exercise the Wave 2 types directly; they do not need Wave 3's wiring to compile (they
  construct decorators directly with a fake `inner`), but they do need Wave 2's types.
- **Wave 6 (docs) is content-dependent on Waves 2–3**, not a compile dependency.

---

## Wave 1 — Delete the two dead queries + regenerate

- [x] **1.1** `internal/telemetry/db/query.sql` — delete the `ListSnapshotsByVehicle`
  query block (including its doc comment, lines ~106–123) and the
  `ListPollAttemptsByVehicle` query block (including its doc comment, lines ~125–130).
  Run `make sqlc` to regenerate `telemetrydb` (removes `ListSnapshotsByVehicle`,
  `ListSnapshotsByVehicleParams`, `ListPollAttemptsByVehicle`,
  `ListPollAttemptsByVehicleParams` from the generated package).
  `depends_on`: — · `parallel_ok`: no

---

## Wave 2 — The four logging decorators + the `call_counter.go` extension

> Purely additive new code plus method-body edits to one existing file. No task in this
> wave edits `service.go`, `reader.go`, or `telemetry.go` — that is Wave 3.

- [x] **2.1** `internal/telemetry/query_log.go` (new file) — `loggingStore` +
  `newLoggingStore` per design.md D1/D6/D8: implements the full 8-method `store`
  interface explicitly (no embedding); `insertSnapshot`, `insertPollAttempt`,
  `upsertSuperchargerHistory` log per D6's table (before delegating); the other 5
  methods (`latestSnapshotsByAccount`, `snapshotsByVehicleSince`,
  `snapshotsByVehicleBetween`, `snapshotsByVehicleUpdatedSince`,
  `snapshotPrecedingDay`) delegate to `inner` with no logging, each with a one-line
  comment pointing at design.md D1 for why. Compile-time assertion
  `var _ store = (*loggingStore)(nil)`. Include the `formatNullableTeslaID(t *int64)
  string` helper (returns `"nil"` for nil, else the decimal value).
  `depends_on`: 1.1 (package must still compile against the reduced `store` interface
  — `store` itself is unaffected by Wave 1, so this is a soft ordering only for a
  single-session implementer; no hard compile dependency) · `parallel_ok`: with 2.2, 2.3, 2.4

- [x] **2.2** `internal/telemetry/query_log.go` — `loggingReader` + `newLoggingReader`
  per design.md D2/D6/D8: implements all 5 `Reader` methods explicitly, each logging
  per D6's table **after** delegating to `inner` (so `rows`/`found` reflect the actual
  result). Compile-time assertion `var _ Reader = (*loggingReader)(nil)`.
  `depends_on`: — · `parallel_ok`: with 2.1, 2.3, 2.4

- [x] **2.3** `internal/telemetry/query_log.go` — `loggingSuperchargerHistoryReader` +
  `newLoggingSuperchargerHistoryReader` per design.md D6/D8: implements all 4
  `SuperchargerHistoryReader` methods explicitly, logging after delegating.
  `SuperchargerHistoryByAccount`/`SuperchargerHistoryByVehicle` log
  `resolveLimit(limit)` (the existing unexported helper in `reader.go`, reused as-is —
  do not duplicate its logic), not the raw `limit` argument; `SuperchargerHistoryByAccount`
  additionally logs the literal `date_bound=none`. Compile-time assertion
  `var _ SuperchargerHistoryReader = (*loggingSuperchargerHistoryReader)(nil)`.
  `depends_on`: — · `parallel_ok`: with 2.1, 2.2, 2.4

- [x] **2.4** `internal/telemetry/query_log.go` — `loggingRunWriter` +
  `newLoggingRunWriter` per design.md D6/D8: implements `RecordRun`, logging before
  delegating (`run_id`, `triggered_by`; no row count — this is a write). Compile-time
  assertion `var _ RunWriter = (*loggingRunWriter)(nil)`.
  `depends_on`: — · `parallel_ok`: with 2.1, 2.2, 2.3

- [x] **2.5** `internal/telemetry/call_counter.go` — add one `log.Printf` line inside
  each of the 4 existing methods (`ListVehicles`, `VehicleData`, `WakeUp`,
  `ChargingHistory`) per design.md D4's table, logged immediately after `c.calls++`
  and before delegating to `c.inner`. Do not reference `creds` anywhere in any new
  `log.Printf` call (D3 — the compiler cannot enforce this, so this task IS the
  enforcement; task 5.4 verifies it by test). No new type, no signature change.
  `depends_on`: — · `parallel_ok`: with 2.1–2.4 (different file)

---

## Wave 3 — Production wiring (4 one-line changes)

- [x] **3.1** `internal/telemetry/service.go` — in `NewService`, change the `store:`
  field from `&dbStore{q: telemetrydb.New(pool)}` to
  `newLoggingStore(&dbStore{q: telemetrydb.New(pool)})`.
  `depends_on`: 2.1 · `parallel_ok`: with 3.2, 3.3, 3.4

- [x] **3.2** `internal/telemetry/reader.go` — in `NewReader`, change the return from
  `&reader{store: &dbStore{q: telemetrydb.New(pool)}}` to
  `newLoggingReader(&reader{store: &dbStore{q: telemetrydb.New(pool)}})`.
  `depends_on`: 2.2 · `parallel_ok`: with 3.1, 3.3, 3.4

- [x] **3.3** `internal/telemetry/telemetry.go` — in `NewSuperchargerHistoryReader`,
  change the return from `newSuperchargerHistoryReaderImpl(pool)` to
  `newLoggingSuperchargerHistoryReader(newSuperchargerHistoryReaderImpl(pool))`.
  `depends_on`: 2.3 · `parallel_ok`: with 3.1, 3.2, 3.4

- [x] **3.4** `internal/telemetry/telemetry.go` — in `NewRunWriter`, change the return
  from `newRunWriter(pool)` to `newLoggingRunWriter(newRunWriter(pool))`.
  `depends_on`: 2.4 · `parallel_ok`: with 3.1, 3.2, 3.3

---

## Wave 4 — Rewrite the 10 call sites across 3 test files (must land with Wave 1)

> design.md D5 documents the exact 10 call sites (not 8, per the roadmap's stated
> count) and the raw-SQL replacement pattern, mirroring the existing "direct SQL
> SELECT" precedent already in `db_integration_test.go` lines 343–351 (Test Contract
> group B of `RM29-app-add-process-vehicle-data`). Every rewrite keeps its test's
> existing assertions (values, `Valid`/`Int32`/`Float32`/`Float64` checks) verbatim —
> only the row-fetch mechanism changes. No rewrite may call another sqlc reader query
> (D13's binding constraint) — raw `pool.QueryRow`/`pool.Query` only.

- [x] **4.1** `internal/telemetry/db_integration_test.go` — rewrite the 5 call sites:
  `TestStore_SnapshotRoundTrip_SentryNilIsNull`, `TestStore_SentryTrueAndFalseRoundTripFaithfully`,
  `TestStore_SnapshotUpsert_SameDayReplaces`, `TestStore_SnapshotInsert_DifferentDayCreatesNewRow`
  (all 4 replace `q.ListSnapshotsByVehicle(...)` with a raw
  `SELECT id, account_id, tesla_id, captured_at, raw_data, battery_level_pct,
  charging_state, car_version, sentry_mode, updated_at FROM telemetry.vehicle_snapshots
  WHERE account_id = $1 AND tesla_id = $2 ORDER BY captured_at DESC` — narrow the
  column list per what each test actually asserts, scanning into local `pgtype.*`
  variables matching the deleted query's generated struct field types), and
  `TestStore_PollAttemptRoundTrip` (replaces `q.ListPollAttemptsByVehicle(...)` with a
  raw `SELECT outcome, reason, attempted_at FROM telemetry.poll_attempts WHERE
  account_id = $1 AND tesla_id = $2 ORDER BY attempted_at DESC`).
  `depends_on`: 1.1 · `parallel_ok`: with 4.2, 4.3

- [x] **4.2** `internal/telemetry/db_sourcea_integration_test.go` — rewrite the 3 call
  sites (`TestSourceA_ChargeEnrichment_TruthfulZeroStoredAndRead`,
  `TestMaxRangeChargeCounter_TruthfulZeroStoredAsNonNil`,
  `TestMaxRangeChargeCounter_NilStoresAsNullAndRoundTripsNil`), each replacing
  `q.ListSnapshotsByVehicle(...)` with a raw `SELECT` scoped to the columns each test
  asserts (charge-enrichment fields for the first; `max_range_charge_counter` for the
  other two).
  `depends_on`: 1.1 · `parallel_ok`: with 4.1, 4.3

- [x] **4.3** `internal/telemetry/db_tpms_integration_test.go` — rewrite the 2 call
  sites (`TestTPMS_NilRoundTrip`, `TestTPMS_ZeroNonNilRoundTrip`), each replacing
  `q.ListSnapshotsByVehicle(...)` with a raw `SELECT tpms_pressure_fl_psi,
  tpms_pressure_fr_psi, tpms_pressure_rl_psi, tpms_pressure_rr_psi FROM
  telemetry.vehicle_snapshots WHERE account_id = $1 AND tesla_id = $2`.
  `depends_on`: 1.1 · `parallel_ok`: with 4.1, 4.2

---

## Wave 5 — Tests for the new decorators (offline; D10)

- [x] **5.1** `internal/telemetry/query_log_test.go` (new file) — Test Contract Group A:
  a fake `store`/`Reader`/`SuperchargerHistoryReader`/`RunWriter` `inner` per decorator
  (or one small fake per interface reused across subtests); for each of the 13 logged
  methods, redirect `log.Output` to a buffer (`log.SetOutput`, restored via
  `t.Cleanup`), call the method, assert the line matches design.md D6's format exactly
  (prefix, every documented field/value, `rows=N`/`found=%t` as applicable). Include
  Group A.1 (`loggingStore`'s 5 silent methods produce zero log output — call all 5,
  assert `buf.Len() == 0`) and Group A.2 (`SuperchargerHistoryByAccount`/
  `SuperchargerHistoryByVehicle` log the resolved limit: `limit=0` in →
  `limit=2147483647` in the log line; `limit=25` in → `limit=25` in the log line).
  `depends_on`: 2.1, 2.2, 2.3, 2.4 · `parallel_ok`: with 5.2

- [x] **5.2** `internal/telemetry/query_log_test.go` — `TestQueryLog_NeverLogsRawDataContent`
  (Test Contract Group C, design.md D5/D9): call `loggingStore.insertSnapshot` and
  `.upsertSuperchargerHistory` with `RawData: []byte("MARKER_RAW_DATA_MUST_NOT_APPEAR_IN_LOG")`,
  assert the captured log buffer does NOT contain that substring and DOES contain the
  correct `raw_data_bytes=<N>`.
  `depends_on`: 2.1 · `parallel_ok`: with 5.1

- [x] **5.3** `internal/telemetry/call_counter_test.go` — Test Contract Group B:
  extend the existing test file with one assertion per method (using the existing
  `minimalFakeTesla`) that the captured log line matches design.md D4's table exactly,
  including `ChargingHistory`'s full 4-field `ChargingHistoryParams`.
  `depends_on`: 2.5 · `parallel_ok`: with 5.4

- [x] **5.4** `internal/telemetry/call_counter_test.go` —
  `TestCallCounter_NeverLogsCredentials` (Test Contract Group C, design.md D3/D9):
  construct `tesla.Credentials{AccessToken: "SECRET-TOKEN-DO-NOT-LOG-9f3a"}`, call all
  4 `callCounter` methods with it, assert the captured log buffer never contains that
  substring.
  `depends_on`: 2.5 · `parallel_ok`: with 5.3

---

## Wave 6 — Docs

- [x] **6.1** `internal/telemetry/AGENTS.md` — add a short paragraph to "Testing notes"
  naming `query_log.go` and the extended `call_counter.go`, the two log prefixes
  (`telemetry query:`, `fleet api:`), and pointing at
  `TestQueryLog_NeverLogsRawDataContent`/`TestCallCounter_NeverLogsCredentials` as
  where the credential/raw-data-never-logged guarantee is tested. Reference this
  change's archived design.md once archived, mirroring how other sections here point
  at their originating change.
  `depends_on`: 2.1, 2.5 · `parallel_ok`: no

---

## Verification (owner-run; see Test-Execution-Policy)

After every wave above is implemented:

```
go build ./...
go vet ./...
gofmt -l .
make sqlc
```

Owner-only, once ready: `go test ./internal/telemetry/...` (Wave 4's rewritten
DB-backed tests self-skip without `DATABASE_URL`/Docker — see
`internal/telemetry/AGENTS.md` "Testing notes" for confirming they actually ran rather
than skipped; Wave 5's decorator tests are pure offline and always run).
