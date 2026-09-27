# Tasks — RM69-analytics-add-unfinished-for-date

All work is inside `internal/analytics`, except T5 (leader-owned, outside
this module's sandbox — see `design.md` D8).

## Dependency graph

```
T1 (query.sql)  ──► T2 (sqlc generate) ──► T3 (analytics.go + reader.go + query_log.go) ──► T4a (offline test)
                                                                                         └──► T4b (DB integration tests)
T3 ──► T6 (docs)
T1..T4b, T6 ──► T7 (verification)
T5 (leader, cross-module) — independent, may run any time before T7's repo-wide go vet
```

- **T1 → T2 is a hard chain.** `sqlc generate` reads `query.sql` directly;
  the `UnfinishedVehicleIDsForDateParams` type does not exist before it runs.
- **T2 → T3 is a hard chain.** `reader.go` and `analytics.go` reference the
  generated type.
- **T3 → T4a/T4b**: both test files call the real method, which must exist
  and compile first. T4a (offline, a fake store) and T4b (DB-backed) touch
  disjoint files and may run in parallel with each other.
- **T6 (docs) depends on T3 only** (content dependency — docs describe the
  post-change state) and may run in parallel with T4a/T4b.
- **T5 is independent of every other task here** — it touches files outside
  `internal/analytics/` and can run at any point before `go vet ./...` is
  expected to pass repo-wide. It is NOT part of this module's own sandbox;
  the pipeline leader does it, not the analytics worker.
- **T7 depends on everything** — it is the final build/vet/lint pass.

---

## T1 — New query (`internal/analytics/db/query.sql`)

Depends on: nothing.

- [ ] 1.1 Add `UnfinishedVehicleIDsForDate` (a `:many` query) per `design.md`
      D3: `SELECT DISTINCT t.tesla_id FROM unnest(@tesla_ids::bigint[]) AS
      t(tesla_id) WHERE NOT EXISTS (SELECT 1 FROM analytics.vehicle_metrics
      vm WHERE vm.tesla_id = t.tesla_id AND vm.metric_date = @metric_date)
      ORDER BY t.tesla_id;`. Doc comment states: what it backs
      (`Reader.UnfinishedForDate`), why `unnest`+`NOT EXISTS` instead of a
      loop (D3), why `DISTINCT` (D4), why `ORDER BY` (D6), and which index
      serves it (`vehicle_metrics_tesla_date_unique`, design.md "Index
      Plan" #1) — no new index.

## T2 — Codegen

Depends on: 1.1.

- [ ] 2.1 Run `make sqlc` (or `sqlc generate` — allowed codegen command,
      `CLAUDE.md` "Builds & local checks") to regenerate
      `internal/analytics/db/*.go`. Confirm the generated method's shape:
      `UnfinishedVehicleIDsForDate(ctx context.Context, arg
      UnfinishedVehicleIDsForDateParams) ([]int64, error)` — a single-column
      `SELECT` generates a plain `[]int64` return, no `Row` wrapper struct
      (mirrors `ChargeGapDatesByVehicleBetween`'s existing generated shape).
      Confirm `UnfinishedVehicleIDsForDateParams` has `TeslaIds []int64` and
      `MetricDate pgtype.Date` fields.

## T3 — Port, store seam, implementation, logging

Depends on: 1.1, 2.1.

- [ ] 3.1 `internal/analytics/analytics.go` — add `UnfinishedForDate(ctx
      context.Context, teslaIDs []int64, date time.Time) ([]int64, error)`
      to the `Reader` interface, immediately after `LatestMetricsForVehicles`,
      with the full doc comment from `design.md` "Signatures" (the
      plain-`[]int64` rationale, the no-ownership-check consequence, the
      empty-input/no-query contract, the dedup contract, the ascending-order
      contract).
      `depends_on`: 1.1, 2.1 · `parallel_ok`: no

- [ ] 3.2 `internal/analytics/reader.go` — `vehicleMetricsStore` gains
      `UnfinishedVehicleIDsForDate(ctx context.Context, arg
      analyticsdb.UnfinishedVehicleIDsForDateParams) ([]int64, error)`.
      Implement `UnfinishedForDate` on the concrete `reader` exactly as
      `design.md` "Signatures" specifies: `len(teslaIDs) == 0` short-circuits
      to `[]int64{}, nil` with no store call; otherwise call the store with
      `dateFrom(date)`, and guard the generated result against a bare `nil`
      before returning (the "every vehicle is done" case).
      `depends_on`: 3.1 · `parallel_ok`: no

- [ ] 3.3 `internal/analytics/query_log.go` — add
      `UnfinishedForDate` to `loggingReader`, logging AFTER delegating
      (design.md D7): `logging.Note("Reader", "UnfinishedForDate",
      "analytics query: date=%s requested=%d unfinished=%d", ...)`. Update
      the file's top-of-file comment, which currently says only
      `ConsumedByDay` logs and the other three methods are silent — it must
      now say `ConsumedByDay` and `UnfinishedForDate` log (both have a
      poller caller), and `OdometerDeltaByDay`/`BatteryLevelByDay`/
      `LatestMetricsForVehicles` stay silent (dashboard-only).
      `depends_on`: 3.1 · `parallel_ok`: with 3.2 (disjoint edit regions,
      same file family but no overlapping lines)

## T4a — Offline test (write from design.md, before or alongside T3)

Depends on: 3.1, 3.2 (needs the real method signature to compile against).

- [ ] 4a.1 `internal/analytics/reader_test.go` — TR-1: a fake
      `vehicleMetricsStore` whose `UnfinishedVehicleIDsForDate` calls
      `t.Fatal` if invoked. Call `UnfinishedForDate(ctx, nil, anyDate)` and
      `UnfinishedForDate(ctx, []int64{}, anyDate)`; assert both return
      `[]int64{}` (non-nil) and a nil error, and that the fake was never
      called. Test writes only — per the Test-Execution-Policy, do not run
      `go test`.
      `depends_on`: 3.1, 3.2 · `parallel_ok`: with 4b.1

## T4b — DB integration tests (final wave, write from design.md)

Depends on: 3.1, 3.2.

- [ ] 4b.1 New file `internal/analytics/db_unfinished_integration_test.go`.
      Add a `seedUnfinishedVehicleMetric(t, pool, teslaID, metricDate)`
      helper mirroring `db_monthly_sync_integration_test.go`'s
      `seedMonthlyVehicleMetric` (direct `INSERT` with placeholder `NOT
      NULL` raw columns — no public writer creates a bare row on demand).
      Add a cleanup helper purging `analytics.vehicle_metrics` for the
      fixture's `tesla_id` range before and after each test (mirrors
      `db_gap_writer_integration_test.go`'s `cleanupChargeGaps` pattern).
      Implement TR-2 through TR-5 from `design.md` "Test contract" exactly,
      using tesla_id values in the `920xxx` range. Test writes only — per
      the Test-Execution-Policy, do not run `go test`.
      `depends_on`: 3.1, 3.2 · `parallel_ok`: with 4a.1

## T5 — Cross-module fake stubs (LEADER-OWNED, outside this module's sandbox)

Depends on: 3.1 (the interface must exist to know the new method's exact
signature). Independent of T4a/T4b/T6.

- [ ] 5.1 Add a one-method `UnfinishedForDate` stub to each of the four
      `Reader`-implementing test fakes so repo-wide `go vet ./...` compiles
      (design.md D8): `internal/app/processor_test.go`,
      `internal/gateway/handlers/handlers_test.go`,
      `internal/gateway/handlers/history_test.go`,
      `internal/gateway/handlers/external_charges_test.go`. Each stub
      matches that file's own existing convention for an unused interface
      method (a `nil, nil` return, or a panic, whichever that file already
      does for its other unused methods) — do not invent a new convention.
      This task is NOT inside `internal/analytics`'s sandbox; the pipeline
      leader performs it, not the analytics worker (mirrors how
      `RM40-analytics-add-battery-level-read` handled the identical
      fallout).
      `depends_on`: 3.1 · `parallel_ok`: yes, with everything except itself

## T6 — Documentation

Depends on: 3.1.

- [ ] 6.1 `internal/analytics/AGENTS.md` — "Public interface (the port)"
      table: add the `UnfinishedForDate` row to `Reader`
      (`"[]int64 — the tesla_ids with no vehicle_metrics row for a given date"`
      or similar, matching the table's existing terseness).
      `depends_on`: 3.1 · `parallel_ok`: with 4a.1, 4b.1

- [ ] 6.2 `kkpa/context/entities/vehicle-metrics/guide.md` — update the
      `Reader` port method list (the "Read the metrics:" bullet and the
      `internal/analytics/analytics.go` file-map row) to include
      `UnfinishedForDate` alongside the four existing methods.
      `depends_on`: 3.1 · `parallel_ok`: with 4a.1, 4b.1, 6.1

## T7 — Verification

Depends on: everything above (1.1 through 6.2), including T5.

- [ ] 7.1 Run and report, inside `internal/analytics/`: `go build ./...`,
      `go vet ./...`, `gofmt -l`. Per the Test-Execution-Policy, never run
      `go test ./...`, `make test`, `make test-with-db`, or `make check`.
- [ ] 7.2 Run and report repo-wide: `go build ./...`, `go vet ./...`,
      `gofmt -l`, `make build`, `make vet`, `make bins`, `make lint`. This
      is where T5's fix is actually verified — repo-wide `go vet` fails
      until all four fakes are stubbed. `make ui-guard` /
      `make i18n-guard` / `make money-guard` / `make tz-guard` /
      `make migration-boundary-guard` / `make boundary-guard` /
      `make naming-guard` / `make archive-guard` / `make logdir-guard` /
      `make delta-guard` are all no-ops for this tier (no gateway code, no
      user-facing string, no monetary or raw-time-zone code, no migration
      file, no log-dir change, no naming-guard-triggering type) — running
      them is optional but harmless.
- [ ] 7.3 Hand off to the owner the exact command to run and report: `go
      test ./internal/analytics/...` (covers the new offline test T4a and
      the new DB-integration file T4b, plus every pre-existing test in the
      package). Until the owner reports a pass, this tier's implementation
      status is **awaiting-user-verification**, never "done."
