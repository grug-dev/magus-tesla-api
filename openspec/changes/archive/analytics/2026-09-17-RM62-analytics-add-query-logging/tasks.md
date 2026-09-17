# Tasks — RM62-analytics-add-query-logging

Ownership: every task below is **[module: analytics worker]** — inside
`internal/analytics/` only. No task touches `internal/telemetry`, `internal/charging`,
`internal/account`, `internal/tesla`, `internal/app`, `internal/gateway`, or `cmd/`
(design.md confirms none of them need to change for this tier).

**No unit tests** — roadmap Decision 1 / design.md "Tests excluded". No task below
writes a `_test.go` file.

## Parallel-safety

- **Wave 1** (the new file) has no dependency and must land first — the three wiring
  edits in Wave 2 reference the types it declares.
- **Wave 2** (three one-line wiring edits) touches three disjoint existing files
  (`reader.go`, `recalculate.go`, `analytics.go`) — its three tasks are independent of
  each other and may run in parallel, but all three depend on Wave 1.

---

## Wave 1 — `internal/analytics/query_log.go` (new file)

- [x] **1.1** Create `internal/analytics/query_log.go`. Package header comment states,
  in the file's own words, what the file does and why each decorator implements its
  interface explicitly rather than by embedding (a future port method added without a
  matching override must fail to compile, not silently skip logging) — do NOT cite this
  change's ID, the roadmap, or any decision letter in the comment; state the reason
  itself, mirroring `internal/telemetry/query_log.go`'s comment shape but without its
  now-forbidden `design.md D1/D2/...` citations (per `ai/go-conventions.md`'s
  code-comment rule).
  `depends_on`: — · `parallel_ok`: no (first task, defines the file)

- [x] **1.2** In `query_log.go`, add `loggingReader` per design.md D1/D4/D5:
  - `type loggingReader struct { inner Reader }` and `func newLoggingReader(inner
    Reader) *loggingReader`.
  - `var _ Reader = (*loggingReader)(nil)`.
  - `ConsumedByDay` implements `Reader`: delegates to `inner` first, counts
    `result[i].Flagged == true` into `flaggedCount`, then calls `logging.Note("Reader",
    "ConsumedByDay", "analytics query: tesla_id=%d start=%s end=%s rows=%d flagged=%d",
    teslaID, start.UTC().Format("2006-01-02"), end.UTC().Format("2006-01-02"),
    len(result), flaggedCount)`, then returns `result, err` unchanged.
  - `OdometerDeltaByDay`, `BatteryLevelByDay`, `LatestMetricsForVehicles` each implement
    `Reader` as silent pass-throughs (`return l.inner.Method(...)`, no `logging.Note`
    call), each with a one-line comment stating it is a dashboard-only read with no
    poller caller, off this tier's scope.
  `depends_on`: 1.1 · `parallel_ok`: no (same file as 1.1, sequential within the file)

- [x] **1.3** In `query_log.go`, add `loggingRecalculator` per design.md D2/D4/D5:
  - `type loggingRecalculator struct { inner Recalculator }` and `func
    newLoggingRecalculator(inner Recalculator) *loggingRecalculator`.
  - `var _ Recalculator = (*loggingRecalculator)(nil)`.
  - `Recalculate` implements `Recalculator`: calls `logging.Note("Recalculator",
    "Recalculate", "analytics query: tesla_id=%d start=%s end=%s", teslaID,
    start.UTC().Format("2006-01-02"), end.UTC().Format("2006-01-02"))` BEFORE
    delegating, then `return l.inner.Recalculate(ctx, teslaID, start, end)` unchanged.
  - `Reconcile` implements `Recalculator`: calls `logging.Note("Recalculator",
    "Reconcile", "analytics query: tesla_id=%d", teslaID)` BEFORE delegating, then
    `return l.inner.Reconcile(ctx, teslaID)` unchanged. Doc comment states plainly (per
    design.md D3, in the code's own words, no decision-ID citation) that this line does
    not repeat when `Reconcile` internally re-derives a window and calls `Recalculate`
    on the concrete type directly — that window is visible instead on the
    `telemetry query:` line immediately below it in the log, from
    `internal/telemetry`'s own existing decorator.
  `depends_on`: 1.1 · `parallel_ok`: with 1.2 (different methods, same new file — treat
  as sequential edits within one session; no file conflict if split across agents that
  coordinate on append order)

- [x] **1.4** In `query_log.go`, add `loggingGapWriter` per design.md D2/D4/D5:
  - `type loggingGapWriter struct { inner GapWriter }` and `func newLoggingGapWriter(
    inner GapWriter) *loggingGapWriter`.
  - `var _ GapWriter = (*loggingGapWriter)(nil)`.
  - `ReconcileWindow` implements `GapWriter`: calls `logging.Note("GapWriter",
    "ReconcileWindow", "analytics query: tesla_id=%d start=%s end=%s flagged=%d",
    teslaID, start.UTC().Format("2006-01-02"), end.UTC().Format("2006-01-02"),
    len(flagged))` BEFORE delegating, then `return l.inner.ReconcileWindow(ctx, teslaID,
    start, end, flagged)` unchanged.
  `depends_on`: 1.1 · `parallel_ok`: with 1.2, 1.3 (different methods, same new file)

---

## Wave 2 — Wiring (three disjoint existing files)

- [x] **2.1** `internal/analytics/reader.go` — `NewReader`: wrap the returned `&reader{
  metrics: analyticsdb.New(pool)}` in `newLoggingReader(...)`, per design.md D5's exact
  before/after diff. No other line in the function changes.
  `depends_on`: 1.2 · `parallel_ok`: with 2.2, 2.3

- [x] **2.2** `internal/analytics/recalculate.go` — `NewRecalculator`: wrap the returned
  `&recalculator{...}` struct literal in `newLoggingRecalculator(...)`, per design.md
  D5's exact before/after diff. No other line in the function changes.
  `depends_on`: 1.3 · `parallel_ok`: with 2.1, 2.3

- [x] **2.3** `internal/analytics/analytics.go` — `NewGapWriter`: change `return
  newGapWriter(pool)` to `return newLoggingGapWriter(newGapWriter(pool))`, per
  design.md D5's exact before/after diff.
  `depends_on`: 1.4 · `parallel_ok`: with 2.1, 2.2

---

## Verification (owner-run; see Test-Execution-Policy)

After every wave above is implemented:

```
go build ./...
go vet ./...
gofmt -l .
make lint
```

Owner-only manual smoke check: `go run ./cmd/poller --once` (wakes the real car, paid
Fleet API calls), confirming the log matches design.md's Test Contract — one
`[Recalculator] [Reconcile] analytics query: tesla_id=...` line per vehicle before the
metrics-reconciliation telemetry lines, and `[Reader] [ConsumedByDay]` / `[GapWriter]
[ReconcileWindow]` `analytics query:` lines under the gap-reconciliation half.
