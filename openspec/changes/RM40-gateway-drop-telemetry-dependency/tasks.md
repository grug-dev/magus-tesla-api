# Tasks — RM40-gateway-drop-telemetry-dependency

Ownership legend: **[module: gateway worker]** — inside `internal/gateway/`, this
tier's own sandbox. **[leader-owned / permission-gated]** — outside
`internal/gateway/`; not this worker's sandbox in the implementation wave, listed
here only so the leader schedules it in the same wave (`CLAUDE.md` "docs track
structural change" / proposal.md "Breaking"). See design.md D1–D8/D-gw1–D-gw3/
D-gw-test for the rationale behind each task.

**Hard ordering constraints:**
- Production call-site swap (1.1) must precede the `Deps`/`Handler` field removal
  (2.1, 2.2) — deleting `TelemetryReader` while `history.go` still calls
  `h.telemetryReader` would not compile.
- 2.1 (`handlers.go`) and 2.2 (`gateway.go`) are two different files with no
  dependency on each other — parallelizable, but BOTH depend on 1.1 having already
  removed the last `telemetryReader` call.
- Test rework (3.1, 3.2) depends on 1.1/2.1/2.2 having landed — the test files
  reference `Deps.TelemetryReader`/`Handler.telemetryReader`, which do not exist
  once 2.1/2.2 land, so the OLD test bodies do not compile against the NEW
  production code and must be updated together, not staged before it.
- 3.1 (`handlers_test.go`) and 3.2 (`history_test.go`) are two different files with
  no dependency on each other — parallelizable.
- Verification (4.1) runs after every other in-sandbox task. It cannot make
  `go build ./...` (whole-repo) pass alone — see the leader-owned task below; run
  `go build ./internal/gateway/...` for this tier's own compile signal instead, and
  `go vet ./internal/gateway/...` similarly.

## Wave 1 — production call-site swap (module: gateway worker)

- [ ] **1.1** `internal/gateway/handlers/history.go` —
  - In `buildHistoryView`: replace the `readStart := start.AddDate(0, 0, -1)` +
    `h.telemetryReader.SnapshotsByVehicleBetween(ctx, uid, teslaID, readStart,
    end)` block with `h.analyticsReader.BatteryLevelByDay(ctx, uid, teslaID, start,
    end)` (design.md D-gw1 — exact code block given there, including the
    `batteryDays` variable name chosen to avoid colliding with the consumed
    chart's own `days` variable in the same function). Preserve the
    independent-failure shape: on error, log and set `v.Battery =
    fragments.HistoryChart{Empty: true}`; on success, `v.Battery =
    buildBatteryChart(ctx, batteryDays, start, end)`.
  - Retype `buildBatteryChart`'s signature from `(ctx context.Context, snaps
    []telemetry.Snapshot, start, end time.Time)` to `(ctx context.Context, days
    []analytics.DayBattery, start, end time.Time)` (design.md D-gw2 — exact body
    given there). Bucket on `d.Date` **verbatim** — do NOT call `effectiveDayUTC`
    on it (roadmap D4). Every other line (empty-chart rule, tooltip format string,
    `Present`/`HeightPct` shape, `YAxisTicks`) is unchanged.
  - Update the function's own doc comments (the `DashboardHistoryFragment` and
    `buildHistoryView` doc blocks currently describe "THREE independent reads" and
    name `telemetry.Reader`/`SnapshotsByVehicleBetween` explicitly) to describe the
    battery read as `analytics.Reader.BatteryLevelByDay`, with no lookback.
  - Remove the `internal/telemetry` import.
  - Acceptance: `grep -c '"github.com/cristianpena/magus-tesla-api/internal/telemetry"' internal/gateway/handlers/history.go` returns `0`.
  `depends_on`: tier 1 (`analytics.Reader.BatteryLevelByDay` exists — already true)
  · `parallel_ok`: no (this is the one task every other task in this change
  depends on)

## Wave 2 — Deps/Handler field removal (module: gateway worker)

- [ ] **2.1** `internal/gateway/handlers/handlers.go` — remove `TelemetryReader
  telemetry.Reader` from `Deps`, remove `telemetryReader telemetry.Reader` from the
  `Handler` struct, remove the `telemetryReader: d.TelemetryReader,` line from
  `New()`'s wiring, remove the `internal/telemetry` import. Acceptance:
  `grep -c telemetry internal/gateway/handlers/handlers.go` returns `0` (no code
  reference — a doc comment mentioning "telemetry" in prose is fine only if it
  does not name the package path or type; prefer rewording over leaving a stale
  mention).
  `depends_on`: 1.1 · `parallel_ok`: with 2.2

- [ ] **2.2** `internal/gateway/gateway.go` — remove `TelemetryReader
  telemetry.Reader` from `Deps`, remove the `TelemetryReader: d.TelemetryReader,`
  line from the `handlers.Deps{...}` literal `NewEngine` builds, remove the
  `internal/telemetry` import. Acceptance:
  `grep -c telemetry internal/gateway/gateway.go` returns `0`.
  `depends_on`: 1.1 · `parallel_ok`: with 2.1

## Wave 3 — test rework (module: gateway worker)

- [ ] **3.1** `internal/gateway/handlers/handlers_test.go` — delete the `fakeReader`
  type (all 5 `telemetry.Reader` method stubs) and `newHandlerWithReader` outright
  (design.md D-gw-test, final paragraph). Update the four call sites that used
  them (`TestDashboardFor_RegisteredEmptyPromptsConnect`,
  `TestDashboardFor_AccountReadErrorShowsNotice`,
  `TestVehicleSelect_FiresVehicleChangedTrigger`,
  `TestHome_SignedInRedirectsToDashboard`) to build their `*Handler` via the
  existing `newHandler(acct, tsvc)` helper, or a direct `Deps{...}` literal with
  the `TelemetryReader` field dropped, whichever each call site's existing shape
  is closer to — verify by reading each test body that no assertion depends on
  telemetry data (none does; confirmed while writing design.md). Remove the
  `internal/telemetry` import. Acceptance:
  `grep -c telemetry internal/gateway/handlers/handlers_test.go` returns `0`, and
  every one of the four tests above still asserts the same outcome it did before
  (only its `*Handler` construction line changes).
  `depends_on`: 2.1 · `parallel_ok`: with 3.2

- [ ] **3.2** `internal/gateway/handlers/history_test.go` — the larger rework
  (design.md D-gw-test has the full investigation and exact contract; design.md's
  Test Contract section has the per-test expected-value mapping — treat both as
  binding, not advisory):
  1. Delete `fakeHistoryReader` (type + its 5 `telemetry.Reader` method stubs)
     outright.
  2. Retype `snapsForDays` off `[]telemetry.Snapshot` onto a new,
     test-file-local fixture struct carrying only `OdometerKm float64` and
     `EffectiveDate time.Time` (design.md's suggested name: `odometerCapture`; the
     implementer may choose another). Drop the now-unused `batteryBase`
     parameter. Retype `distancesFromSnaps`'s parameter to match — its body (the
     delta+clamp+bucket loop) does not otherwise change.
  3. Add a new helper building `[]analytics.DayBattery` directly (design.md's
     suggested name: `batteryForDays(days []time.Time, batteryBase int)
     []analytics.DayBattery`), setting `Date: startOfDay(d)`,
     `BatteryLevelPct: batteryBase+i`, `BatteryRangeKm: 300` (matching
     `snapsForDays`' old hardcoded value) — see design.md D-gw-test item 2 for the
     exact body.
  4. On `fakeAnalyticsReader`: replace the `BatteryLevelByDay` panic stub with a
     real, call-recording implementation mirroring `ConsumedByDay`'s shape; add
     `battery []analytics.DayBattery`, `batteryErr error`,
     `gotBattAccount/gotBattTeslaID/gotBattStart/gotBattEnd`, `batteryByDayCalled
     bool` fields.
  5. Collapse `newHandlerForHistory`/`newHandlerForHistoryWithAnalytics` into ONE
     constructor taking only a `*fakeAnalyticsReader`, `teslaID`, `vin` — there is
     no second reader left to wire.
  6. Rework every call site that built `&fakeHistoryReader{historySnaps: ...}`
     (there are ~15 — `grep -n fakeHistoryReader
     internal/gateway/handlers/history_test.go` before this task to get the
     current exact list/line numbers, since line numbers shift as earlier waves'
     edits land) to build the SAME day list once and derive an odometer fixture
     (via the retyped `snapsForDays`/`distancesFromSnaps`, unchanged numbers) and,
     wherever the test exercises the battery chart, a battery fixture (via the new
     `batteryForDays`, same `batteryBase`).
  7. Apply design.md's Test Contract's four "changed on purpose" assertion
     rewrites (the D5 lookback drop): `TestBuildHistoryView_PassesReadStartLookbackToEndToReader`
     (rename optional; assert `gotBattStart.Equal(start)`, not `start.AddDate(0,
     0, -1)`), `TestBuildHistoryView_BothChartsShareFixedAxis` (drop its lookback
     assertion block), `TestDashboardHistoryFragment_DefaultWindowPassedToReader`
     and `TestDashboardHistoryFragment_DaysParamIsIgnored` (`wantStart` shifts
     from `yesterday.AddDate(0,0,-7)` to `yesterday.AddDate(0,0,-6)`).
  8. Every other test in the file keeps its existing expected values verbatim
     (design.md Test Contract's "Unchanged" sections) — do not touch an assertion
     this list does not name.
  9. Remove the `internal/telemetry` import.
  Acceptance:
  `grep -c telemetry internal/gateway/handlers/history_test.go` returns `0`, and
  `go vet ./internal/gateway/...` compiles the whole package (vet compiles
  `_test.go` files — this is the cheap signal that every retyped call site was
  actually updated, not just the ones enumerated above).
  `depends_on`: 2.1, 2.2 · `parallel_ok`: with 3.1

- [ ] **3.3** `internal/gateway/handlers/history_test.go` — extend
  `TestHandler_AnalyticsReaderDepsForwarding` with a `BatteryLevelByDay` forwarding
  assertion (`batteryByDayCalled`, `gotBattStart`/`gotBattEnd` matching the passed
  `start`/`end`), mirroring the existing `consumedByDayCalled`/
  `odometerByDayCalled` assertions in the same test (design.md Test Contract,
  "Tenant/argument-forwarding proof"). This is a REPAIR-adjacent addition (D7): the
  test already exists and already proves the pattern for the two sibling ports;
  extending it to the third port this tier adds a caller for is the minimum needed
  so the pattern stays proven for all three, not new coverage of a new concern.
  `depends_on`: 3.2 · `parallel_ok`: no

## Wave 4 — verification (assistant-run signals, then owner-run suite)

- [ ] **4.1** Run and report: `go build ./internal/gateway/...`, `go vet
  ./internal/gateway/...`, `gofmt -l internal/gateway`, `make boundary-guard`
  (**this change's own definition of done** — must print
  `boundary-guard: internal/gateway/ does not import internal/telemetry` with NO
  preceding `WARNING` block, per design.md D-gw3's stricter-than-the-Makefile bar).
  `make ui-guard`/`make i18n-guard`/`make money-guard`/`make tz-guard`/
  `make migration-guard` are no-ops for this tier (no new user-facing string, no
  monetary/raw-time-zone code, no migration file) — running them is optional but
  harmless. **Do NOT run `go build ./...` or `make build` for the WHOLE repository
  and treat a failure as this tier's bug** — `cmd/web/main.go` will not compile
  until the leader's own companion fix (below) lands in the same wave; a whole-repo
  build failure at this point is expected, not a regression to chase. Per the
  Test-Execution-Policy, never run `go test ./...`, `make test`,
  `make test-with-db`, or `make check`.
  `depends_on`: 1.1, 2.1, 2.2, 3.1, 3.2, 3.3 · `parallel_ok`: no

- [ ] **4.2** Hand off to the owner the exact command to run and report, once the
  leader-owned task below has landed: `go test ./internal/gateway/...` (covers the
  full existing offline suite this tier repairs; no new test file is added).
  Separately, once BOTH this tier's artifacts and the leader-owned `cmd/web` fix
  are applied, `go test ./...` becomes the whole-repo signal worth running. Until
  the owner reports a pass, this tier's implementation status is
  **awaiting-user-verification**, never "done."
  `depends_on`: 4.1 · `parallel_ok`: no

## Leader-owned / permission-gated (outside this worker's sandbox — schedule in the same wave)

- [ ] **L.1** `cmd/web/main.go:58` — remove the `TelemetryReader:
  telemetry.NewReader(pool)` line from the `gateway.Deps{...}` literal (it no
  longer exists on `Deps` after 2.2 lands). **Do NOT touch lines ~72/~86 of the
  same file** — they call `telemetry.NewReader(pool)` for OTHER, unrelated
  consumers (not the gateway) and must stay. Without this task, the whole repo
  does not build once 2.2 lands (proposal.md "Breaking").
  `depends_on`: 2.2 · `parallel_ok`: no

- [ ] **L.2** `internal/gateway/AGENTS.md` — under "Public interface (the port)",
  remove or rewrite the `Deps.TelemetryReader telemetry.Reader` bullet (currently
  states it is "DEPRECATED, being removed" and names `SnapshotsByVehicleBetween`
  as its "sole remaining caller" — both now false: there is no remaining caller
  and no remaining field). This file IS inside `internal/gateway/`, so it may be
  in-sandbox for the gateway worker rather than leader-owned — the leader decides
  at dispatch time whether to fold this into wave 3 or handle it separately;
  listed here so it is not silently dropped either way (`CLAUDE.md` "docs track
  structural change").
  `depends_on`: 2.1, 2.2 · `parallel_ok`: with L.1

- [ ] **L.3** Root `README.md` — "Dependency graph" section: remove `telemetry,`
  from the `gateway ────────────► account, telemetry, charging, analytics,` line
  and from the `handlers ──────► account, auth, telemetry, charging,` line. Leave
  the `cmd/web`/`cmd/poller` lines and the `analytics ─► …, telemetry, …` line
  untouched — those modules still depend on `internal/telemetry` after this
  change. This file is outside `internal/gateway/`, so it is leader-owned or
  needs an explicitly granted path (`CLAUDE.md` "docs track structural change";
  design.md "Risks").
  `depends_on`: 1.1, 2.1, 2.2 · `parallel_ok`: with L.1, L.2
