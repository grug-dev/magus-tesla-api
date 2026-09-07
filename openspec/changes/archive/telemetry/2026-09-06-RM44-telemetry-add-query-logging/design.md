# Design — RM44-telemetry-add-query-logging

## Context

`internal/telemetry` writes and reads through four interfaces that already exist:

- `store` (unexported, `service.go:42`) — 8 methods total. `*service` (the `Collector`
  write path) calls exactly 3 of them: `insertSnapshot`, `insertPollAttempt`,
  `upsertSuperchargerHistory`. `*reader` (the `Reader` port's implementation, `reader.go`)
  calls the other 5: `latestSnapshotsByAccount`, `snapshotsByVehicleSince`,
  `snapshotsByVehicleBetween`, `snapshotsByVehicleUpdatedSince`, `snapshotPrecedingDay`.
  Both `*service.store` and `*reader.store` are constructed as separate `*dbStore`
  instances (`NewService`, `NewReader`), so at runtime the two usages never share one
  object — they merely satisfy the same Go interface type.
- `Reader` (public, `telemetry.go`) — 5 methods, implemented by `*reader`
  (`reader.go`), which is a thin pass-through to `store`'s 5 read methods.
- `SuperchargerHistoryReader` (public, `telemetry.go`) — 4 methods, implemented by
  `*superchargerHistoryReader` (`reader.go`), which talks to `telemetrydb.Queries`
  directly — **not** through `store`.
- `RunWriter` (public, `telemetry.go`) — 1 method (`RecordRun`), implemented by
  `*runWriter` (`run_writer.go`), which also talks to `telemetrydb.Queries` directly —
  **not** through `store`. This mirrors `SuperchargerHistoryReader`'s precedent (see
  `RM36-telemetry-add-poll-runs` design D12).

None of these four interfaces' methods take a `tesla.Credentials` argument — that
parameter exists only on `tesla.VehicleService`, decorated separately by
`call_counter.go`. This fact is load-bearing for D3 below.

## Goals / Non-Goals

**Goals:**
- Every method call through the four interfaces above, and every call through
  `tesla.VehicleService`, prints one `log.Printf` line naming its non-credential
  arguments (and, for read methods, the row count returned).
- Zero behavior change: every decorated method's return value and error are passed
  through unaltered.
- No credential, access token, refresh token, or `raw_data` content ever reaches a log
  line — enforced structurally and by test (roadmap D9, ticket's "hard constraint").
- The two dead `query.sql` entries (`ListSnapshotsByVehicle`, `ListPollAttemptsByVehicle`)
  are removed cleanly, with their only consumers (10 test call sites) rewritten to raw
  SQL, independent of the module's own reader queries (roadmap D13).

**Non-goals (explicitly out of scope for this tier):**
- Fixing `updated_at` semantics or bounding the mirror's read (tiers 2–4).
- Adding `internal/analytics` logging (roadmap "Future work", separate backlog item).
- Any schema, table, column, index, or migration change (there is none in this tier).
- Structured logging / `slog` adoption (roadmap D8 — explicitly rejected for this
  ticket).

## Decisions

### D1 — The `store` decorator logs only its 3 write methods; the other 5 are silent pass-throughs, because `Reader` gets its own decorator

The roadmap's tier-1 row is explicit and binding: "four interface decorators — `store`
(3 writes), `Reader` (5), `SuperchargerHistoryReader` (4), `RunWriter` (1)." `store` and
`Reader` are two *different* Go values at runtime that merely share the `store` type's 8
methods for 5 of them. If a single fully-logging `store` decorator were wired into both
`NewService` and `NewReader`, that would still satisfy the roadmap's grouping by
coincidence, but it would collapse two decorators into one and rename `Reader`'s log
lines to `store`'s lowercase seam names (`latestSnapshotsByAccount` instead of
`LatestSnapshotsByAccount`) — a needless deviation from what the ticket's own
per-port table asks for, and a design decision this tier does not need to make since
the dispatch already settled it.

The chosen shape: `loggingStore` implements the full 8-method `store` interface
(required to satisfy `var _ store = (*loggingStore)(nil)` and to be assignable to
`service.store store`), but only `insertSnapshot`/`insertPollAttempt`/
`upsertSuperchargerHistory` call `log.Printf`; the 5 read methods delegate to `inner`
with no logging at all. This is safe and non-redundant because:

1. `loggingStore` is wired **only** into `NewService`'s `store` field. `NewReader`
   continues to construct a bare `&dbStore{...}` (undecorated), so `loggingStore`'s
   5 silent methods are never even reached from the read side in production — reading
   never happens through this decorator instance.
2. The read side is logged by `loggingReader` (D2), which wraps the whole `Reader`
   port and therefore produces exactly one log line per read call, with the *public*
   method name and the exact argument list the ticket's table specifies.

**Rejected alternative — decorate `store` fully (8/8 logged) and wire the same
instance into both `NewService` and `NewReader`, dropping a separate `Reader`
decorator.** This would also produce one log line per call (no double-logging), and
would arguably be a smaller vocabulary (one decorator instead of two touching this
half of the module). Rejected anyway because it contradicts the roadmap's explicit,
already-settled 4-decorator enumeration, and because it would print the private
seam's lowercase method names instead of the public port's names the ticket's table
uses — a cosmetic but real mismatch with what the acceptance criteria describe.

### D2 — Wiring: `loggingStore` goes into `NewService` only; `NewReader` gets `loggingReader` wrapping a bare, undecorated `*reader`

```go
// service.go, NewService — BEFORE:
store: &dbStore{q: telemetrydb.New(pool)},
// AFTER:
store: newLoggingStore(&dbStore{q: telemetrydb.New(pool)}),
```

```go
// reader.go, NewReader — BEFORE:
func NewReader(pool *pgxpool.Pool) Reader {
	return &reader{store: &dbStore{q: telemetrydb.New(pool)}}
}
// AFTER:
func NewReader(pool *pgxpool.Pool) Reader {
	return newLoggingReader(&reader{store: &dbStore{q: telemetrydb.New(pool)}})
}
```

`*reader`'s own `store` field is deliberately **not** `loggingStore` — that would log
every read twice (once from `loggingReader`, once from a hypothetically-logging store
read method). Since D1 keeps `loggingStore`'s read methods silent, this concern is
moot in practice, but the explicit non-decoration here documents the invariant so a
future change does not "fix" what looks like an inconsistency by decorating both
layers.

### D3 — Credential-leak risk is structurally confined to `call_counter.go`; the four new decorators cannot leak a credential by construction

None of `store`, `Reader`, `SuperchargerHistoryReader`, or `RunWriter`'s methods take a
`tesla.Credentials` (or any token) argument — verified by re-reading every method
signature in `telemetry.go`/`service.go` (Context above). `query_log.go`'s decorators
therefore have no `creds` value in scope to log, in any method, by construction — there
is no code path to test against because there is no parameter to misuse. This is a
stronger guarantee than "we didn't log it": the compiler enforces it, since a decorator
method whose signature has no `creds` parameter cannot reference one.

The entire credential-leak risk is confined to the `call_counter.go` extension (D4),
whose methods legitimately receive `creds tesla.Credentials` as a parameter they must
not reference in the new `log.Printf` calls. D9's test (Test Contract, Group C) is
written specifically against `call_counter.go`.

### D4 — `call_counter.go` extension: explicit non-credential argument whitelist, one `log.Printf` per method, logged before delegating

Mirrors the existing counting pattern (`c.calls++` before delegating) — logging also
happens before delegating, so the argument line is emitted even when the Fleet API call
itself fails (consistent with "even a rejected request counts" from the original
design). Each method's `log.Printf` call spells out only the safe fields by name; `creds`
is never referenced inside the format-args list:

| Method | New log line (prefix `fleet api:`) |
|---|---|
| `ListVehicles` | `fleet api: ListVehicles` — no vehicle/account identity is available on this signature beyond `creds` (which must never appear), so the line is a bare call marker. |
| `VehicleData` | `fleet api: VehicleData vehicle_id=%d` (arg: `vehicleID`) |
| `WakeUp` | `fleet api: WakeUp vehicle_id=%d` (arg: `vehicleID`) |
| `ChargingHistory` | `fleet api: ChargingHistory start_time=%q end_time=%q page_no=%d count=%d` (args: `params.StartTime, params.EndTime, params.PageNo, params.Count`) |

`ChargingHistory` logs its full `ChargingHistoryParams` as the ticket requires — all
four of that struct's fields, none of which are sensitive.

### D5 — `raw_data` is logged as a byte count, never content; two DB-integration test files' 10 call sites (not 8) are rewritten to raw SQL

`Snapshot.RawData` and `SuperchargerHistory.RawData` are `[]byte` holding a full Fleet
API JSON blob. `loggingStore.insertSnapshot` and `loggingStore.upsertSuperchargerHistory`
log `len(s.RawData)` under the key `raw_data_bytes` — never the slice itself, never a
field derived from unmarshaling it. No other decorated method touches `raw_data` (the
Reader-family return values include it on `Snapshot`/`SuperchargerHistory`, but the
Reader/SuperchargerHistoryReader decorators log only `account`/`tesla_id`/date-filter/
`rows` — never a field of the returned rows — so `raw_data` never even enters scope
there).

**Discrepancy found and flagged, per the dispatch's instruction to report
contradictions plainly:** the roadmap states "their only users are 8 assertions across
3 DB-integration test files." An exhaustive grep of all three files found **10** call
sites across **10** distinct test functions, not 8:

| File | Test functions calling the doomed queries |
|---|---|
| `db_integration_test.go` | `TestStore_SnapshotRoundTrip_SentryNilIsNull`, `TestStore_SentryTrueAndFalseRoundTripFaithfully`, `TestStore_SnapshotUpsert_SameDayReplaces`, `TestStore_SnapshotInsert_DifferentDayCreatesNewRow` (all 4 via `ListSnapshotsByVehicle`), `TestStore_PollAttemptRoundTrip` (via `ListPollAttemptsByVehicle`) — **5** |
| `db_sourcea_integration_test.go` | `TestSourceA_ChargeEnrichment_TruthfulZeroStoredAndRead`, `TestMaxRangeChargeCounter_TruthfulZeroStoredAsNonNil`, `TestMaxRangeChargeCounter_NilStoresAsNullAndRoundTripsNil` (all via `ListSnapshotsByVehicle`) — **3** |
| `db_tpms_integration_test.go` | `TestTPMS_NilRoundTrip`, `TestTPMS_ZeroNonNilRoundTrip` (both via `ListSnapshotsByVehicle`) — **2** |

Total: 10. Tasks.md is sized against the real count (10), not the roadmap's stated 8.
This does not change the shape of the fix — every one of the 10 follows the exact
"direct SQL SELECT" pattern this file's own `TestStore_PollAttemptRoundTrip_*` tests
(Test Contract group B, `RM29-app-add-process-vehicle-data`) already established a
precedent for at lines 343–351 of `db_integration_test.go`: read the column(s) back
with `pool.QueryRow`/`pool.Query` and a literal `SELECT ... FROM telemetry.<table>
WHERE account_id = $1 AND tesla_id = $2`, scanning into the same `pgtype.*` columns the
deleted sqlc query used to return, never through another sqlc reader.

Each rewrite keeps the existing test's assertions verbatim (values, `Valid`/`Int32`/
`Float32`/`Float64` checks) — only the row-fetch mechanism changes, from
`q.ListSnapshotsByVehicle(...)`/`q.ListPollAttemptsByVehicle(...)` to a raw
`pool.Query`/`pool.QueryRow` call with an explicit column list matching what each test
actually asserts (not necessarily every column the deleted query returned — e.g.
`TestTPMS_NilRoundTrip` only needs the 4 TPMS columns, not all 24).

### D6 — Log line grammar (Test Contract) for the four `query_log.go` decorators

All lines use prefix `telemetry query:`. `account`/`tesla_id` print via `%s`/`%d`
respectively (`uuid.UUID` and `int64`); instants (`since`) print RFC 3339 UTC; calendar
days (`start`/`end`/`day`) print `2006-01-02` (they are already whole UTC-midnight-
bounded days per the platform's date-filter convention, `ai/go-conventions.md`).

**`loggingStore` (3 logged methods; 5 silent pass-throughs):**

| Method | Format | Args |
|---|---|---|
| `insertSnapshot` | `telemetry query: insertSnapshot account=%s tesla_id=%d captured_at=%s raw_data_bytes=%d` | `s.AccountID, s.TeslaID, s.CapturedAt.UTC().Format(time.RFC3339), len(s.RawData)` |
| `insertPollAttempt` | `telemetry query: insertPollAttempt account=%s tesla_id=%d attempted_at=%s outcome=%s reason=%s` | `a.AccountID, a.TeslaID, a.AttemptedAt.UTC().Format(time.RFC3339), a.Outcome, a.Reason` |
| `upsertSuperchargerHistory` | `telemetry query: upsertSuperchargerHistory account=%s tesla_id=%s session_id=%d charge_start=%s charge_stop=%s raw_data_bytes=%d` | `s.AccountID, formatNullableTeslaID(s.TeslaID), s.SessionID, s.ChargeStartDateTime.UTC().Format(time.RFC3339), s.ChargeStopDateTime.UTC().Format(time.RFC3339), len(s.RawData)` |

All three log **before** delegating to `inner` (arguments are known upfront; the line
must still appear if the write fails). `formatNullableTeslaID(t *int64) string` is a new
small unexported helper in `query_log.go`: returns `"nil"` for a `nil` pointer (VIN not
currently registered — the documented, legitimate NULL case), else the decimal value.

**`loggingReader` (all 5 methods; logs after delegating, since `rows` needs the result):**

| Method | Format | Args |
|---|---|---|
| `LatestSnapshotsByAccount` | `telemetry query: LatestSnapshotsByAccount account=%s rows=%d` | `accountID, len(result)` |
| `SnapshotsByVehicleSince` | `telemetry query: SnapshotsByVehicleSince account=%s tesla_id=%d since=%s rows=%d` | `accountID, teslaID, since.UTC().Format(time.RFC3339), len(result)` |
| `SnapshotsByVehicleBetween` | `telemetry query: SnapshotsByVehicleBetween account=%s tesla_id=%d start=%s end=%s rows=%d` | `accountID, teslaID, start.UTC().Format("2006-01-02"), end.UTC().Format("2006-01-02"), len(result)` |
| `SnapshotsByVehicleUpdatedSince` | `telemetry query: SnapshotsByVehicleUpdatedSince account=%s tesla_id=%d since=%s rows=%d` | `accountID, teslaID, since.UTC().Format(time.RFC3339), len(result)` |
| `SnapshotPrecedingDay` | `telemetry query: SnapshotPrecedingDay account=%s tesla_id=%d day=%s found=%t` | `accountID, teslaID, day.UTC().Format("2006-01-02"), result != nil` |

`result` is whatever `inner`'s call returned (nil/empty slice on error), so `rows`/
`found` are accurate even when `err != nil`; the decorator does not special-case the
error — it logs the observed result and returns `(result, err)` unchanged.

**`loggingSuperchargerHistoryReader` (all 4 methods; logs after delegating):**

| Method | Format | Args |
|---|---|---|
| `SuperchargerHistoryByAccount` | `telemetry query: SuperchargerHistoryByAccount account=%s limit=%d date_bound=none rows=%d` | `accountID, resolveLimit(limit), len(result)` |
| `SuperchargerHistoryByVehicle` | `telemetry query: SuperchargerHistoryByVehicle account=%s tesla_id=%d limit=%d rows=%d` | `accountID, teslaID, resolveLimit(limit), len(result)` |
| `SuperchargerHistoryByVehicleUpdatedSince` | `telemetry query: SuperchargerHistoryByVehicleUpdatedSince account=%s tesla_id=%d since=%s rows=%d` | `accountID, teslaID, since.UTC().Format(time.RFC3339), len(result)` |
| `SuperchargerHistoryByVehicleBetween` | `telemetry query: SuperchargerHistoryByVehicleBetween account=%s tesla_id=%d start=%s end=%s rows=%d` | `accountID, teslaID, start.UTC().Format("2006-01-02"), end.UTC().Format("2006-01-02"), len(result)` |

**Critical detail — resolved, not raw, `limit`:** `SuperchargerHistoryByAccount`/
`SuperchargerHistoryByVehicle` take the caller's raw `limit int` (0 meaning "unbounded" —
`processChargingData` passes literal `0` today). The decorator calls the existing
unexported `resolveLimit(limit)` (already in `reader.go`, reused, not re-derived) and
logs *that* value, not the raw argument. This is not cosmetic: MAG-48's own acceptance
text for the logging capability says "before the fix, `SuperchargerHistoryByAccount`'s
line must visibly show `limit=2147483647`" — the number the query engine actually runs
with, not the caller's `0`. Logging the raw argument would print `limit=0` both before
and after tiers 2–4's fix (since a bounded read passes a real `limit` only if it ever
does; today it always passes `0`), which would fail to demonstrate anything. The
`date_bound=none` literal on `SuperchargerHistoryByAccount` alone (not
`SuperchargerHistoryByVehicle`) mirrors the ticket's own table, which singles out only
that method for the explicit "no date bound" callout.

**`loggingRunWriter` (1 method; logs before delegating — a write, no row count):**

| Method | Format | Args |
|---|---|---|
| `RecordRun` | `telemetry query: RecordRun run_id=%s triggered_by=%s` | `run.RunID, run.TriggeredBy` |

### D7 — File layout: one new file, `internal/telemetry/query_log.go` + `query_log_test.go`, holding all four decorators

Considered per-port files (`store_log.go`, `reader_log.go`, …) to mirror how
`reader.go`/`run_writer.go` each own one concrete type. Rejected: this tier is one
cohesive feature — "instrument the module's existing seams" — and a reader asking
"where's the query logging" should find one file, not four. `reader.go` itself already
mixes two concrete types (`reader`, `superchargerHistoryReader`) in one file, so a
single-file grouping by *feature* rather than by *port* has direct precedent in this
package. `call_counter.go` stays untouched as its own file and gains only method
bodies — it is a pre-existing single-purpose file, not a candidate for merging.

### D8 — Compile-time assertions, one per decorator, matching `call_counter.go`'s shape exactly

```go
var _ store                     = (*loggingStore)(nil)
var _ Reader                    = (*loggingReader)(nil)
var _ SuperchargerHistoryReader = (*loggingSuperchargerHistoryReader)(nil)
var _ RunWriter                 = (*loggingRunWriter)(nil)
```

Each `logging*` type holds exactly one field, `inner <Interface>`, set by an unexported
`newLogging*(inner <Interface>) *logging*` constructor — mirroring `newCallCounter`'s
shape precisely, including the doc comment explaining why embedding is forbidden (a
method added to the wrapped interface later must be a compile error here, not a
silent, unlogged pass-through via promotion).

## Go-Level Seam Summary (what the implementation tasks build)

- `internal/telemetry/query_log.go` (new): `loggingStore`, `newLoggingStore`,
  `loggingReader`, `newLoggingReader`, `loggingSuperchargerHistoryReader`,
  `newLoggingSuperchargerHistoryReader`, `loggingRunWriter`, `newLoggingRunWriter`,
  `formatNullableTeslaID`, the four `var _ ... = (*logging...)(nil)` assertions.
- `internal/telemetry/call_counter.go` (extended): one `log.Printf` line added inside
  each of the four existing methods; no new types, no signature changes.
- `internal/telemetry/service.go` (1-line change): `NewService`'s `store:` field wraps
  `&dbStore{...}` in `newLoggingStore(...)`.
- `internal/telemetry/reader.go` (1-line change): `NewReader` wraps `&reader{...}` in
  `newLoggingReader(...)`.
- `internal/telemetry/telemetry.go` (2 one-line changes): `NewSuperchargerHistoryReader`
  wraps its return in `newLoggingSuperchargerHistoryReader(...)`; `NewRunWriter` wraps
  its return in `newLoggingRunWriter(...)`.
- `internal/telemetry/db/query.sql` (deletion): remove the `ListSnapshotsByVehicle` and
  `ListPollAttemptsByVehicle` query blocks (including their doc comments). Run
  `make sqlc`.
- `internal/telemetry/db_integration_test.go`,
  `internal/telemetry/db_sourcea_integration_test.go`,
  `internal/telemetry/db_tpms_integration_test.go` (rewritten call sites, D5): each of
  the 10 identified call sites replaced with a raw `pool.QueryRow`/`pool.Query` + scan,
  assertions unchanged.
- `internal/telemetry/AGENTS.md` (doc addition): short paragraph in "Testing notes."

## Test Contract (authored before implementation, per `ai/go-conventions.md`)

All decorator tests are pure offline unit tests: no `DATABASE_URL`, a fake `inner`
implementation per interface, `log.SetOutput(&buf)` redirecting the standard logger for
the duration of the test (restored via `t.Cleanup`), and a substring assertion against
`buf.String()`. This is the same technique `RM36-telemetry-add-poll-runs`
(`report_test.go`) already uses for `LogCycle`, applied here for the first time to
per-call decorators.

### Group A — `query_log_test.go`: each decorator logs the documented line

For each of the 4 decorators and each of their logged methods (12 methods total: 3 on
`loggingStore`, 5 on `loggingReader`, 4 on `loggingSuperchargerHistoryReader`; plus
`loggingRunWriter.RecordRun` = 13), a fake `inner` returns a fixed, known result (a
2-element slice for the "many" methods, a non-nil `*Snapshot` for
`SnapshotPrecedingDay`, `nil` error for a happy-path write). The test asserts:

- the log line begins with the exact prefix (`telemetry query: <MethodName>`),
- every documented field/value appears (e.g., for `SnapshotsByVehicleSince` with a
  known `accountID`/`teslaID`/`since`, the line contains `account=<accountID.String()>
  tesla_id=42 since=2026-01-15T00:00:00Z`), and
- `rows=2` (or `rows=0` for an empty-result fixture, or `found=true`/`found=false` for
  `SnapshotPrecedingDay`).

**Group A.1 — `loggingStore`'s 5 silent methods stay silent.** A separate fixture calls
all 5 read methods on `loggingStore` (via a fake `inner`) and asserts `buf.Len() == 0`
after each — proving D1's "silent pass-through" claim by test, not just by reading the
code.

**Group A.2 — `SuperchargerHistoryByAccount` logs the resolved limit, not the raw one
(D6).** Fixture: call with `limit=0`; assert the log line contains `limit=2147483647`
(the literal `math.MaxInt32` value, matching what `resolveLimit(0)` returns today) and
`date_bound=none`. A second fixture with `limit=25` asserts `limit=25` appears
unchanged (resolveLimit is identity above 0).

### Group B — `call_counter_test.go` addition: the 4 Fleet API log lines

Extends the existing `minimalFakeTesla`-based test file. For each of the 4 methods,
assert the log line matches D4's table exactly, e.g. `ChargingHistory` called with
`ChargingHistoryParams{StartTime: "2026-06-01", EndTime: "2026-09-01", PageNo: 2, Count: 50}`
produces `fleet api: ChargingHistory start_time="2026-06-01" end_time="2026-09-01" page_no=2 count=50`.

### Group C — the D9 security tests (both files)

- **`TestCallCounter_NeverLogsCredentials`** (`call_counter_test.go`): construct
  `tesla.Credentials{AccessToken: "SECRET-TOKEN-DO-NOT-LOG-9f3a"}`, call all 4 methods
  through `callCounter` with this value, assert the captured log buffer does **not**
  contain the substring `"SECRET-TOKEN-DO-NOT-LOG-9f3a"` anywhere.
- **`TestQueryLog_NeverLogsRawDataContent`** (`query_log_test.go`): call
  `loggingStore.insertSnapshot` and `.upsertSuperchargerHistory` with
  `RawData: []byte("MARKER_RAW_DATA_MUST_NOT_APPEAR_IN_LOG")`, assert the captured log
  buffer does **not** contain that marker substring, and **does** contain
  `raw_data_bytes=<N>` with the correct byte count.

Both tests are the concrete, structural proof the roadmap's D9 and the ticket's "hard
constraint" require — not a code-review-only guarantee.

## Risks / Trade-offs

- **Log volume.** Every one of ~13 method calls per nightly cycle per account now
  prints one line. Accepted per MAG-48's own "Volume is not a concern here" note: this
  module is reachable only from the nightly batch and two composition roots, never from
  a web request (`make boundary-guard` forbids `internal/gateway` importing
  `internal/telemetry`).
- **`loggingStore`'s 5 silent methods look inconsistent at a glance** — a reader seeing
  a `store`-shaped decorator with only 3 of 8 methods actually logging might assume a
  bug. Mitigated by an explicit doc comment on `loggingStore` itself (not just this
  design.md) stating the D1 rationale and pointing at `loggingReader` for the other 5.
- **`resolveLimit` reuse creates a soft coupling** between `query_log.go` and
  `reader.go`'s private helper. Accepted: both are the same package, the helper is
  small, stable, and reusing it (rather than re-deriving `math.MaxInt32` inline) is
  exactly the kind of duplication `ai/go-conventions.md`'s AI-efficiency principle asks
  to avoid.

## Verification signals

```
go build ./...
go vet ./...
gofmt -l .
make sqlc
```

Owner-only: `go test ./internal/telemetry/...` (offline decorator tests always run;
DB-backed rewritten tests self-skip without `DATABASE_URL`/Docker per the module's
existing `TestMain` convention — see `internal/telemetry/AGENTS.md` "Testing notes" for
confirming they actually ran rather than skipped).

Manual/operational verification (not part of `go test`, per roadmap D11's own intent):
running `cmd/poller --once` against a real or seeded database before tier 1 merges
would show no `telemetry query:`/`fleet api:` lines at all (they don't exist yet); after
this tier merges and before tiers 2–4, the same run should show
`SuperchargerHistoryByAccount account=<uuid> limit=2147483647 date_bound=none rows=<N>`
— the literal proof this bug existed, now visible for the first time.
