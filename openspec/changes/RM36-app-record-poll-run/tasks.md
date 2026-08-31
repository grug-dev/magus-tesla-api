# Tasks — RM36-app-record-poll-run

Ownership: every task below is **[module: app worker]**, inside `internal/app/`
only, plus `cmd/poller/main.go` — this tier's one explicitly granted path (design.md
"Modules Affected"). No task touches `internal/telemetry`, `internal/tesla`,
`internal/charging`, `internal/analytics`, or `internal/account` — every port this
tier calls already exists (tier 1, archived).

**Hard ordering constraints (see design.md for the "why" behind each):**

- **Wave 1 (constructor signature) → Wave 2 (control-flow refactor).** The
  `processor` struct needs its new `runWriter` field (Wave 1) before
  `ProcessVehicleData`/`recordRun` can reference `p.runWriter` (Wave 2).
  `buildPollRun` (Wave 2) has no dependency on the struct field and could in
  principle land first, but is grouped into Wave 2 since it lives in the same file
  and the same conceptual unit as `recordRun`.
- **Wave 2 → Wave 3 (tests).** Every offline fixture (P1–P5) exercises code Wave 2
  writes; none can compile before it lands.
- **Wave 1/2 → Wave 4 (`cmd/poller` wiring).** The composition root's call to
  `app.NewProcessor(...)` must match Wave 1's new parameter list — it cannot compile
  until that signature exists.
- **Wave 5 (docs) describes the POST-change state** — a content dependency on Waves
  1–4, not a compile one; it can be written any time after Wave 1's signature is
  final, but is sequenced last so it reflects what actually shipped rather than the
  plan.
- This tier adds **no `DATABASE_URL`-gated test** — `internal/app` owns no table and
  every fixture is offline (design D9), so there is no final DB-backed wave the way
  tier 1 had one.

---

## Wave 1 — Constructor signature + struct field

- [x] **1.1** `internal/app/app.go` — add `runWriter telemetry.RunWriter` to
  `NewProcessor`'s parameter list, placed immediately after `superchargerReader`
  (design D1 — grouped with this module's other `telemetry`-owned ports), and pass
  it through to the returned `&processor{...}` struct literal. Update the function's
  doc comment: it currently says "every argument is one of these seven arguments" —
  it is now eight.
  `depends_on`: — · `parallel_ok`: no

- [x] **1.2** `internal/app/processor.go` — add `runWriter telemetry.RunWriter` as a
  field on the unexported `processor` struct, in the same position as its
  constructor parameter (immediately after `superchargerReader`).
  `depends_on`: — · `parallel_ok`: with 1.1 (same file region is small; sequence if
  conflicts arise, but both are additive one-line edits)

---

## Wave 2 — `ProcessVehicleData` control-flow refactor + `buildPollRun`/`recordRun`

- [x] **2.1** `internal/app/processor.go` — add the import
  `"github.com/cristianpena/magus-tesla-api/internal/clock"`.
  `depends_on`: — · `parallel_ok`: with Wave 1

- [x] **2.2** `internal/app/processor.go` — refactor `ProcessVehicleData` exactly per
  design.md D4/D6: `start := clock.Now()` before step 1; the early
  `if err != nil { return report, err }` becomes `if err == nil { step2; step3 }`;
  after that guard, `finish := clock.Now()`, `report.Duration = finish.Sub(start)`,
  `p.recordRun(ctx, run, report, start, finish)`, then `return report, err`. Update
  the method's doc comment to state the run is now measured and recorded on every
  exit path, including the step-1 whole-cycle-failure short-circuit — cross-reference
  design.md D4 for why the short-circuit's own behavior is unchanged.
  `depends_on`: 1.2, 2.1 · `parallel_ok`: no

- [x] **2.3** `internal/app/processor.go` — add the unexported `buildPollRun` free
  function exactly as specified in design.md D7 (pure, no receiver, maps
  `telemetry.RunContext` + `telemetry.CycleReport` + `start`/`finish` onto
  `telemetry.PollRun`, reading `report.Attempted`/`.Succeeded` by field per tier 1's
  own D8, reading `report.FailuresByReason` map keys defensively since it may be
  `nil` on the whole-cycle-failure path).
  `depends_on`: 1.2 · `parallel_ok`: with 2.2

- [x] **2.4** `internal/app/processor.go` — add the unexported `recordRun` method
  exactly as specified in design.md D5: calls `p.runWriter.RecordRun(ctx,
  buildPollRun(...))`, logs (`log.Printf("poll run: recording run %s: %v", ...)`) and
  swallows any returned error — never returns anything itself, never influences
  `ProcessVehicleData`'s own return values.
  `depends_on`: 2.3 · `parallel_ok`: no

---

## Wave 3 — Tests (offline; all fixtures authored in design.md before this wave)

- [ ] **3.1** `internal/app/processor_test.go` (new file, `package app` — design D8)
  — the fake roster from design D9: `fakeCollector`, `fakeRunWriter`,
  `fakeAccountEmpty`, `fakeSuperchargerReader`, `fakeSessionWriter`,
  `fakeRecalculator`, `fakeAnalyticsReader`, `fakeGapWriter`. Each is a minimal
  struct satisfying its full interface; only `fakeCollector`/`fakeRunWriter`/
  `fakeAccountEmpty` need configurable behavior beyond a zero-value stub (design D9).
  `depends_on`: 2.4 · `parallel_ok`: no

- [ ] **3.2** `internal/app/processor_test.go` — Fixtures P1 and P2 (design.md Test
  Contract): direct unit tests of `buildPollRun`, no fakes needed. P1 the
  representative successful-run mapping; P2 the all-zero-counts whole-cycle-failure
  shape, including the nil-map-read case for the three `FailuresByReason` lookups.
  `depends_on`: 2.3 · `parallel_ok`: with 3.1

- [ ] **3.3** `internal/app/processor_test.go` — Fixture P3: a successful 3-step run
  via `NewProcessor` + the Wave 3.1 fakes, asserting `RecordRun` is called exactly
  once with a `PollRun` whose `RunID` matches the `RunContext` given to `CollectAll`,
  whose counts match Fixture P1's mapping, and whose `StartedAt`/`FinishedAt` fall
  within the test's own measured wall-clock bracket; also asserts
  `fakeAccountEmpty.AllRegisteredVehicles` **was** called (steps 2/3 ran).
  `depends_on`: 3.1 · `parallel_ok`: no

- [ ] **3.4** `internal/app/processor_test.go` — Fixture P4: the step-1
  whole-cycle-failure path. `fakeCollector` returns a distinct sentinel error;
  assert `ProcessVehicleData` returns that exact error, `RecordRun` is still called
  exactly once with the all-zero-counts shape (Fixture P2's shape), and
  `fakeAccountEmpty.AllRegisteredVehicles` was **not** called (short-circuit
  preserved).
  `depends_on`: 3.1 · `parallel_ok`: with 3.3

- [ ] **3.5** `internal/app/processor_test.go` — Fixture P5: `fakeRunWriter`
  configured to return a distinct sentinel error on `RecordRun`; assert
  `ProcessVehicleData`'s own returned `(report, err)` is exactly the collector's
  successful outcome, unaffected by the `RecordRun` failure, and that `RecordRun`
  was still called exactly once (the attempt was made, not skipped).
  `depends_on`: 3.1 · `parallel_ok`: with 3.3, 3.4

---

## Wave 4 — `cmd/poller` wiring (granted path)

- [x] **4.1** `cmd/poller/main.go` — construct `telemetry.NewRunWriter(pool)` and
  pass it as the new argument to `app.NewProcessor(...)`, in the position Wave 1.1
  established (immediately after `superchargerReader`). No other line in this file
  changes — it remains wiring-only, per its own header comment.
  `depends_on`: 1.1 · `parallel_ok`: no

---

## Wave 5 — Docs (post-change state; content-dependent on Waves 1–4, not a compile dependency)

- [ ] **5.1** `internal/app/AGENTS.md` — "Public interface (the port)" section:
  update `NewProcessor`'s documented signature to include `runWriter
  telemetry.RunWriter`, and add one sentence stating it is the port the module now
  calls to record a `poll_runs` row per invocation (mirroring how
  `superchargerReader`/`sessionWriter`/etc. are already documented as "another
  module's public port").
  `depends_on`: 1.1 · `parallel_ok`: with 5.2

- [ ] **5.2** `internal/app/AGENTS.md` — "Allowed / forbidden imports" section: add
  `internal/clock` to the "May import" list (design D3 — stdlib-only, zero cycle
  risk, same reasoning already given for every other allowed import in this list).
  `depends_on`: 2.1 · `parallel_ok`: with 5.1

- [ ] **5.3** `internal/app/AGENTS.md` — "Testing notes" section: add a new
  paragraph documenting `processor_test.go`'s coverage (the `buildPollRun`/
  `recordRun` seam, Fixtures P1–P5) as a **third**, narrowly-scoped covered surface
  alongside `scheduler_test.go`'s existing four tests — explicitly note that the
  three orchestration steps' own internals (`processChargingData`,
  `recalculateAnalytics`) remain deliberately uncovered per tier 1's own design D12,
  unchanged by this tier: this tier tests the code wrapped *around* calling them,
  not their own logic.
  `depends_on`: 3.5 · `parallel_ok`: no

- [ ] **5.4** `internal/app/AGENTS.md` — "Responsibility" section's three-step
  diagram / prose: add one sentence noting that every invocation now also records a
  poll-run summary via `telemetry.RunWriter`, cross-referencing this change once
  archived.
  `depends_on`: 2.4 · `parallel_ok`: with 5.1, 5.2, 5.3

---

## Verification (owner-run; see Test-Execution-Policy)

After every wave above is implemented:

```
go build ./...
go vet ./...
gofmt -l .
```

Owner-only, once ready: `go test ./internal/app/...` (fully offline — this tier adds
no `DATABASE_URL`-gated test) and the manual smoke check,
`go run ./cmd/poller --once`, confirming a `poll_runs` row lands and the poller's
log line prints a non-zero `duration=%s`.
