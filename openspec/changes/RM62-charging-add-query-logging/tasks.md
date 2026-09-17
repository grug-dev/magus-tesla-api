# Tasks — RM62-charging-add-query-logging

No unit tests (roadmap Decision 1). Every task below is implementation or
documentation. Update this file's checkboxes live as each task completes.

## Wave 1 — New file `internal/charging/query_log.go` (five decorators)

All five tasks touch the same new file. They are listed with explicit `depends_on` so a
single agent can do them in order; splitting across agents needs them to coordinate on
append order within the one file (mirrors
`RM62-analytics-add-query-logging`'s tasks.md wave 1 note).

- [x] **1.1** Create `internal/charging/query_log.go` with the package declaration,
  imports (`context`, `time`, `internal/logging`), and a header comment stating what the
  file holds (five logging decorators over the module's nightly-path seams) and the
  explicit-implementation-never-embedding rule and why (a future interface method added
  without a matching override must fail to build, not silently skip logging) — in the
  comment's own words, no `design.md`/decision-ID citation, per `ai/go-conventions.md`'s
  code-comment rule.
  `depends_on`: — · `parallel_ok`: no (first task, defines the file)

- [x] **1.2** In `query_log.go`, add `loggingReader` per design.md D4/D7/D8:
  - `type loggingReader struct { inner Reader }` and `func newLoggingReader(inner
    Reader) *loggingReader`.
  - `var _ Reader = (*loggingReader)(nil)`.
  - `ListEntriesByVehicleUpdatedSince` implements `Reader`: delegates to `inner` first,
    then calls `logging.Note("Reader", "ListEntriesByVehicleUpdatedSince", "charging
    query: tesla_id=%d since=%s rows=%d", teslaID, since.UTC().Format(time.RFC3339),
    len(result))`, then returns `result, err` unchanged.
  - `ListEntriesByVehicle`, `ListEntriesByVehicles`, `ListEntriesByVehicleBetween` each
    implement `Reader` as silent pass-throughs (`return l.inner.Method(...)`, no
    `logging.Note` call), each with a one-line comment stating why: the first two are
    dominated by live gateway reads, the third has no caller anywhere today (design.md
    D4).
  `depends_on`: 1.1 · `parallel_ok`: no (same file as 1.1, sequential within the file)

- [x] **1.3** In `query_log.go`, add `loggingSessionWriter` per design.md D6/D7/D8:
  - `type loggingSessionWriter struct { inner SessionWriter }` and `func
    newLoggingSessionWriter(inner SessionWriter) *loggingSessionWriter`.
  - `var _ SessionWriter = (*loggingSessionWriter)(nil)`.
  - `MirrorSessions` implements `SessionWriter`: computes a `firstTeslaID` from
    `sessions[0].TeslaID` when `len(sessions) > 0`, else `0` (a comment states this
    reflects the caller's own one-vehicle-per-call usage, not a port guarantee — design.md
    D7's `firstTeslaID` note), calls `logging.Note("SessionWriter", "MirrorSessions",
    "charging query: tesla_id=%d sessions=%d", firstTeslaID, len(sessions))` BEFORE
    delegating, then `return l.inner.MirrorSessions(ctx, sessions)` unchanged.
  `depends_on`: 1.1 · `parallel_ok`: with 1.2 (different methods, same new file)

- [x] **1.4** In `query_log.go`, add `loggingSuperchargerSessionAnalyticsReader` per
  design.md D3/D5/D7/D8:
  - `type loggingSuperchargerSessionAnalyticsReader struct { inner
    SuperchargerSessionAnalyticsReader }` and `func
    newLoggingSuperchargerSessionAnalyticsReader(inner
    SuperchargerSessionAnalyticsReader) *loggingSuperchargerSessionAnalyticsReader`.
  - `var _ SuperchargerSessionAnalyticsReader =
    (*loggingSuperchargerSessionAnalyticsReader)(nil)`.
  - `ListSessionsByVehicleBetween` implements the interface (satisfying its embedded
    `SessionReader`): delegates to `inner` first, then calls
    `logging.Note("SuperchargerSessionAnalyticsReader", "ListSessionsByVehicleBetween",
    "charging query: tesla_id=%d start=%s end=%s rows=%d", teslaID,
    from.UTC().Format("2006-01-02"), to.UTC().Format("2006-01-02"), len(result))`, then
    returns `result, err` unchanged.
  - `ListSessionsByVehicleUpdatedSince` implements the interface: delegates to `inner`
    first, then calls `logging.Note("SuperchargerSessionAnalyticsReader",
    "ListSessionsByVehicleUpdatedSince", "charging query: tesla_id=%d since=%s rows=%d",
    teslaID, since.UTC().Format(time.RFC3339), len(result))`, then returns `result, err`
    unchanged.
  - `ListSessionsByVehicle` implements the interface as a silent pass-through, with a
    comment stating it has no caller anywhere in the repo today (design.md D5).
  - A comment on the type states this decorator is wired ONLY into
    `NewSuperchargerSessionAnalyticsReader`, never into `NewSessionReader` — the sibling
    constructor over the same concrete type stays undecorated (design.md D2/D3).
  `depends_on`: 1.1 · `parallel_ok`: with 1.2, 1.3 (different methods, same new file)

- [x] **1.5** In `query_log.go`, add `loggingMirrorWatermarkStore` per design.md
  D6/D7/D8:
  - `type loggingMirrorWatermarkStore struct { inner MirrorWatermarkStore }` and `func
    newLoggingMirrorWatermarkStore(inner MirrorWatermarkStore)
    *loggingMirrorWatermarkStore`.
  - `var _ MirrorWatermarkStore = (*loggingMirrorWatermarkStore)(nil)`.
  - `MirrorWatermark` implements `MirrorWatermarkStore`: delegates to `inner` first, then
    calls `logging.Note("MirrorWatermarkStore", "MirrorWatermark", "charging query:
    tesla_id=%d cursor=%s", teslaID, cursor.UTC().Format(time.RFC3339))`, then returns
    `cursor, err` unchanged.
  - `AdvanceMirrorWatermark` implements `MirrorWatermarkStore`: calls
    `logging.Note("MirrorWatermarkStore", "AdvanceMirrorWatermark", "charging query:
    tesla_id=%d observed=%s", teslaID, observed.UTC().Format(time.RFC3339))` BEFORE
    delegating, then `return l.inner.AdvanceMirrorWatermark(ctx, teslaID, observed)`
    unchanged.
  `depends_on`: 1.1 · `parallel_ok`: with 1.2, 1.3, 1.4 (different methods, same new file)

- [x] **1.6** In `query_log.go`, add `loggingMonthlyCapacityCalculator` per design.md
  D6/D7/D8:
  - `type loggingMonthlyCapacityCalculator struct { inner MonthlyCapacityCalculator }`
    and `func newLoggingMonthlyCapacityCalculator(inner MonthlyCapacityCalculator)
    *loggingMonthlyCapacityCalculator`.
  - `var _ MonthlyCapacityCalculator = (*loggingMonthlyCapacityCalculator)(nil)`.
  - `Calculate` implements `MonthlyCapacityCalculator`: delegates to `inner` first,
    computes `scope` (`"all"` when `teslaID == nil`, else the decimal vehicle id via
    `strconv.FormatInt`), then calls `logging.Note("MonthlyCapacityCalculator",
    "Calculate", "charging query: period=%s tesla_id=%s found=%d measured=%d thin=%d",
    period.Format("2006-01"), scope, result.VehiclesFound, result.Measured,
    result.Thin)`, then returns `result, err` unchanged. Add `strconv` to the file's
    imports.
  `depends_on`: 1.1 · `parallel_ok`: with 1.2, 1.3, 1.4, 1.5 (different methods, same new
  file)

---

## Wave 2 — Wiring (five constructors, one file: `internal/charging/charging.go`)

All five edits are in the same existing file, at disjoint line ranges (each constructor's
own `return` statement). List with explicit `depends_on` on the matching Wave 1 task; a
single agent applies them in any order since each touches a different function body.

- [x] **2.1** `NewReader` — change `return newReader(pool)` to `return
  newLoggingReader(newReader(pool))`. No other line in the function changes.
  `depends_on`: 1.2 · `parallel_ok`: with 2.2, 2.3, 2.4, 2.5

- [x] **2.2** `NewSessionWriter` — change `return newSessionWriter(pool)` to `return
  newLoggingSessionWriter(newSessionWriter(pool))`. No other line in the function
  changes.
  `depends_on`: 1.3 · `parallel_ok`: with 2.1, 2.3, 2.4, 2.5

- [x] **2.3** `NewSuperchargerSessionAnalyticsReader` — change `return
  newSessionReader(pool)` to `return
  newLoggingSuperchargerSessionAnalyticsReader(newSessionReader(pool))`. Confirm
  `NewSessionReader` (the sibling constructor, a few lines above) is left byte-for-byte
  unchanged — design.md D2/D3's entire point depends on this.
  `depends_on`: 1.4 · `parallel_ok`: with 2.1, 2.2, 2.4, 2.5

- [x] **2.4** `NewMirrorWatermarkStore` — change `return
  newMirrorWatermarkStore(pool)` to `return
  newLoggingMirrorWatermarkStore(newMirrorWatermarkStore(pool))`. No other line in the
  function changes.
  `depends_on`: 1.5 · `parallel_ok`: with 2.1, 2.2, 2.3, 2.5

- [x] **2.5** `NewMonthlyCapacityCalculator` — change `return
  newMonthlyCapacityCalculator(pool)` to `return
  newLoggingMonthlyCapacityCalculator(newMonthlyCapacityCalculator(pool))`. No other
  line in the function changes.
  `depends_on`: 1.6 · `parallel_ok`: with 2.1, 2.2, 2.3, 2.4

**Confirm untouched:** `NewWriter`, `NewSessionReader`, `NewSessionVerifier` keep their
exact current bodies — no task in this wave (or any wave) edits them (design.md D1/D2).

---

## Wave 3 — Knowledge base

- [x] **3.1** Update `kkpa/context/architecture/nightly-cycle.md`'s "Conventions &
  gotchas" section: add bullets for `internal/charging`'s own query logging, mirroring
  the three existing bullets for tier 2's analytics logging (which ports/methods log
  and why, which stay silent and why — including the `SessionReader` vs
  `SuperchargerSessionAnalyticsReader` constructor split from design.md D2/D3 — and the
  explicit-implementation/compile-time-assertion rule). Check whether the "Rendered
  view" Artifact (linked at the guide's end) needs a republish note added to this
  change's own report — republishing the Artifact itself is not required by this tier
  (no port map, table effect, or failure-table fact changes; only what already-existing
  calls log changes) but state explicitly in the change's final report whether that
  check was done and what was found, per `CLAUDE.md`'s "Docs track structural change"
  rule.
  `depends_on`: 2.1, 2.2, 2.3, 2.4, 2.5 · `parallel_ok`: no (single doc file, needs the
  final port table settled first)

---

## Wave 4 — The caller-side log in `internal/analytics` (design.md D10)

**Explicit path grant.** This one task edits `internal/analytics/recalculate.go`, which
is OUTSIDE this change's module. The worker is granted that single file for this task
only. Nothing else under `internal/analytics/` may be touched — in particular not
`query_log.go`, whose decorators are already archived work.

- [x] **4.1** In `internal/analytics/recalculate.go`, inside `Recalculate`, immediately
  after the `r.manual.ListEntriesByVehicleBetween(ctx, teslaID, chargeStart, end)` call
  returns and its error is handled, add:
  `logging.Note("Recalculator", "Recalculate", "manual entries read: tesla_id=%d start=%s end=%s rows=%d", teslaID, chargeStart.UTC().Format("2006-01-02"), end.UTC().Format("2006-01-02"), len(entries))`
  Add the `internal/logging` import if the file does not have it yet. Log AFTER the read,
  so the row count is real. Keep the topic exactly `manual entries read:` — NOT
  `charging query:` and NOT `analytics query:`. Those two topics belong to the two
  modules' decorators, and a grep for either must never return a line a decorator did
  not emit. Add a short comment saying why the line lives here and not in charging's
  decorator: charging's manual-entry read port has one shared constructor, so logging it
  there would log every gateway page render too. Do not name a decision ID, a change ID,
  or a tier in that comment.
  `depends_on`: — · `parallel_ok`: yes (different module, different file from every other wave)

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
`[SessionWriter] [MirrorSessions]`, one `[MirrorWatermarkStore] [MirrorWatermark]`, and
one `[MirrorWatermarkStore] [AdvanceMirrorWatermark]` `charging query:` line per vehicle
around step 2's existing summary line, one `[SuperchargerSessionAnalyticsReader]
[ListSessionsByVehicleUpdatedSince]` and one `[Reader]
[ListEntriesByVehicleUpdatedSince]` `charging query:` line per vehicle right after each
`[Recalculator] [Reconcile]` line in step 3, and — on the first day of a month only —
one `[MonthlyCapacityCalculator] [Calculate]` `charging query:` line in step 4. Also
confirm one `[Recalculator] [Recalculate] manual entries read:` line appears per vehicle
when `Reconcile` finds new data, and that a manual-charges page render produces none. Also
confirm the manual-charges page, the Supercharger-stats page, and a manual-charge
save/edit/delete in the browser produce NO new log lines from this change.
