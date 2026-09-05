Source: MAG-48 — https://linear.app/magus-monitor/issue/MAG-48/nightly-reconcile-recalculates-the-whole-vehicle-metrics-history-on
Roadmap: openspec/roadmaps/RM44-incremental-supercharger-sync.md
Tier: 1 of 4 (`RM44-telemetry-add-change-detecting-upsert`, `RM44-charging-add-change-detecting-mirror`,
`RM44-charging-add-mirror-watermark` all depend on this tier per roadmap D11 — logging
ships first so tiers 2–4 can be proved with its log lines, not because anything downstream
imports this tier's code)

## Why

`analytics.Recalculator.Reconcile` recalculates `analytics.vehicle_metrics` back to the
account's oldest Supercharger session on every poller run instead of narrowing to what
actually changed — measured 2026-09-05 as a 70-day window and 51-of-51 rows rewritten,
where the snapshot source alone needed 4 days. The root cause (fixed in tiers 2–4) is
that `updated_at` on two mirrored tables means "the last pass touched this row," not
"this row's data changed."

This bug ran unnoticed for months for one reason: **nothing in the codebase prints a
query's date filters or the Fleet API call parameters that produce them.**
`internal/telemetry` has two log statements today (`LogCycle`, and an error-path-only
line in `internal/analytics`), neither of which shows an argument. Roadmap decision D11
puts the instrument first, deliberately, so the fix in tiers 2–4 is provable by the log
lines this tier adds — before the fix, `SuperchargerHistoryByAccount`'s log line must
show `limit=2147483647` with no date bound; after the fix, it must show a bounded,
watermark-driven `since`.

Roadmap decision D12 closes an open question from the source ticket: MAG-48 asked
whether to extract a seam for "five queries with no interface" or log at call sites. That
premise is wrong — `internal/telemetry/service.go:42` already declares a private `store`
interface covering all three of this module's internal writes
(`insertSnapshot`/`insertPollAttempt`/`upsertSuperchargerHistory`). Every live query in
this module is already behind an interface. This change's own investigation (see
design.md D1) confirms that claim structurally: all four existing test fakes of `store`
compile unchanged after adding the decorator this change introduces.

## What Changes

- **New file `internal/telemetry/query_log.go`** — four explicit, non-embedding logging
  decorators, one per existing interface, mirroring `internal/telemetry/call_counter.go`'s
  shape exactly (compile-time assertion per decorator, no interface embedding):
  - `loggingStore` wraps the private `store` interface. Only its three write methods
    (`insertSnapshot`, `insertPollAttempt`, `upsertSuperchargerHistory`) — the ones
    `*service`'s Collector write path actually calls — emit a log line; the other five
    (`store`'s read methods, never called from the write side) are silent pass-throughs,
    because the read side is logged at the `Reader` port instead (design D1 — avoids a
    double log line per read call).
  - `loggingReader` wraps the public `Reader` interface (5 methods).
  - `loggingSuperchargerHistoryReader` wraps the public `SuperchargerHistoryReader`
    interface (4 methods). `SuperchargerHistoryByAccount`/`SuperchargerHistoryByVehicle`
    log the **resolved** limit (via the existing unexported `resolveLimit` helper, reused
    — not re-derived), not the caller's raw `0`, because the roadmap's own acceptance
    test requires the log to show the number the query will actually run with
    (`limit=2147483647`), matching MAG-48's stated pre-fix expectation.
  - `loggingRunWriter` wraps the public `RunWriter` interface (`RecordRun`).
  - All four use `log.Printf` with the new prefix `telemetry query:` (D8 of the roadmap —
    no `slog`, matching the repo's existing convention of stdlib logging with a
    greppable prefix per concern).
- **Extend `internal/telemetry/call_counter.go`** (D14 of the roadmap — extend, do not add
  a sibling decorator): each of the four `tesla.VehicleService` methods gains one
  `log.Printf("fleet api: ...")` line logging its non-credential parameters.
  `ChargingHistory` logs its full `ChargingHistoryParams` (`StartTime`, `EndTime`,
  `PageNo`, `Count`).
- **Four one-line production wiring changes** (no interface changes, no new exported
  symbols beyond what already exists): `NewService` (service.go), `NewReader`
  (reader.go), `NewSuperchargerHistoryReader`, `NewRunWriter` (both telemetry.go) each
  wrap their concrete implementation in the matching decorator before returning it.
- **Delete `ListSnapshotsByVehicle` and `ListPollAttemptsByVehicle`** from
  `internal/telemetry/db/query.sql` (D13 of the roadmap) and regenerate `telemetrydb` via
  `make sqlc`. Neither has a production caller. Their only callers are 10 DB-integration
  test call sites across 3 files (`db_integration_test.go`, `db_sourcea_integration_test.go`,
  `db_tpms_integration_test.go`) — see design.md D5 for the count and the discrepancy
  with the roadmap's stated "8 assertions." Every call site is rewritten to a raw SQL
  `SELECT` issued directly against the pool inside the test, never through another sqlc
  reader query (D13's binding constraint — verifying a write by calling one of the
  module's own live readers would make the test pass through the very path it tests).
- **`internal/telemetry/AGENTS.md`** — "Testing notes" gains a short paragraph pointing
  at `query_log.go`/the extended `call_counter.go`, the two log prefixes, and where the
  credential/raw_data-never-logged test lives, so a future agent looking for "is there
  query logging here" finds it without grepping.

## Breaking

**No — externally.** No HTTP route, no rendered markup, no i18n key, no schema object.
`internal/telemetry`'s public port surface (`Collector`, `Reader`,
`SuperchargerHistoryReader`, `RunWriter`) gains no method and loses no method — every
signature is unchanged. `internal/tesla.VehicleService`'s signature is unchanged (only
the calling decorator inside `internal/telemetry` gains lines; `internal/tesla` itself is
not touched, per roadmap D7).

**No — internally.** `NewService`/`NewReader`/`NewSuperchargerHistoryReader`/`NewRunWriter`
keep their existing signatures; only the concrete value they construct and return
changes (now decorator-wrapped), which is invisible to every caller because callers
already depend on the interface, never the concrete type. The four existing test fakes
of the private `store` interface (`fakeStore`, `fakeReadStore`, `fakeHistoryStore`,
`fakeBetweenStore`) are unaffected — the `store` interface itself gains no method (design
D1 verifies this by re-reading every fake).

**Yes — behavior-visible only in logs.** Every call through the four decorated interfaces
and the four Fleet API methods now prints one `log.Printf` line it did not print before.
This is the entire point of the tier (roadmap D11) and produces no other observable
change. No behavior, return value, or error changes for any caller.

## Modules Affected

- **`internal/telemetry/`** — the sole owning module for this tier. New file
  (`query_log.go` + `query_log_test.go`), one extended file (`call_counter.go` +
  `call_counter_test.go`), four one-line wiring edits, one query-deletion + regenerate,
  three test files rewritten, one `AGENTS.md` addition.
- **`internal/tesla/`** — **not touched.** `callCounter` already lives inside
  `internal/telemetry` and wraps `tesla.VehicleService` from the outside; this tier adds
  log lines to that existing wrapper, never to the Fleet API client itself (roadmap D7).
- **`internal/charging/`, `internal/analytics/`, `internal/app/`** — **not touched.**
  Tiers 2–4 of this roadmap touch those modules; this tier changes no behavior any of
  them depends on.
- **`internal/gateway/`** — not touched, and not reachable: `internal/gateway` may not
  import `internal/telemetry` at all (`make boundary-guard`), so nothing here is on a web
  request path (ai/architecture.md §7 "Exception").

## Database Changes

**None.** No table, column, index, constraint, view, or migration is added, changed, or
dropped. Deleting two `query.sql` entries is a codegen-input change, not a schema
change — `make sqlc` regenerates `telemetrydb` with two fewer generated functions;
`telemetry.vehicle_snapshots` and `telemetry.poll_attempts` themselves are untouched.
Per the dispatch's own note, the `database` design gate does not apply to this tier.

**Makefile/guard impact — checked, none found.** No new `MIGRATIONS_DIRS` entry, no
change to `db-setup`/`db-reset` role assumptions, no new guard needed. The two deleted
queries have no migration of their own to remove (they were plain `query.sql` entries
with no matching DDL). `make sqlc` must be re-run once (already on the allowed-commands
list) after the deletion; no other Makefile target is affected.

## Read Paths Affected

**None functionally.** Every decorated method's existing behavior, return value, and
error are unchanged — the decorator logs and delegates, nothing more. The four
Reader-family log lines execute on the same read paths documented in
`ai/architecture.md` §7 (nightly batch reads via `Collector`'s write-path store calls,
and `internal/analytics`/`internal/app`'s calls into `Reader`/`SuperchargerHistoryReader`)
— all off the gateway's hot path already, since the gateway cannot import this module.
`log.Printf` to stdout/stderr is not itself a hot-path-performance concern at this
module's call volume (nightly batch only, verified in MAG-48's own "Volume is not a
concern here" note).

## Capabilities

### Added Capabilities

- **Query and Fleet-API argument logging** — every call through `internal/telemetry`'s
  four existing port/seam interfaces (`store`'s three write methods, `Reader`,
  `SuperchargerHistoryReader`, `RunWriter`) and every call through the module's
  `tesla.VehicleService` wrapper logs its non-credential arguments and, for read
  methods, the row count returned. See `specs/telemetry/spec.md`.
- **Structural credential/raw-payload logging exclusion** — no decorator has any code
  path by which a `tesla.Credentials` value or a `raw_data` blob's content can reach a
  log line; a test asserts this for both. See `specs/telemetry/spec.md`.

### Modified Capabilities

None — no existing requirement's behavior changes for a caller. (`ListSnapshotsByVehicle`/
`ListPollAttemptsByVehicle`'s removal is a test-only, non-production-facing change; see
"Out of scope" below for why it is not framed as a capability change.)

### Out of scope (explicitly deferred)

- **Fixing the `updated_at` semantics or the mirror's unbounded read.** Tiers 2–4 of
  this roadmap. This tier changes no behavior, only observability.
- **`internal/analytics` logging.** Recorded in `openspec/roadmaps/backlog.md` per the
  roadmap's own "Future work": `Recalculate`'s derived window is still invisible on a
  successful run. Out of scope for this roadmap entirely, not just this tier.
- **Removing `ListSnapshotsByVehicle`/`ListPollAttemptsByVehicle` as a "capability."**
  These were test-only helper queries with no production caller and no public-port
  exposure; their deletion is described under "What Changes" as a cleanup this tier's
  own decorator work requires accounting for (D13), not as a capability the platform
  ever offered.

## Testing

Per the Test-Execution-Policy: this tier writes tests but does not run the suite.
`query_log_test.go` and the `call_counter_test.go` addition are pure offline unit tests
(fake inner implementations, `log.SetOutput` to a buffer) — no `DATABASE_URL` dependency,
authored against design.md's Test Contract (exact log-line formats fixed before the
decorator code is written, per `ai/go-conventions.md` "contract-first authoring"). The
three rewritten integration test files remain `DATABASE_URL`-gated exactly as before;
only their assertion mechanism changes (raw SQL instead of the deleted sqlc queries).

Exact commands for the owner to run, once implementation lands:

```
go build ./...
go vet ./...
gofmt -l .
make sqlc
```

Owner-only, once ready: `go test ./internal/telemetry/...` (or `make test` /
`make test-with-db` for the DB-backed fixtures — Docker or `DATABASE_URL` required for
those to actually execute rather than skip).
