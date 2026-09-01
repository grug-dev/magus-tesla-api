# Tasks — RM38-gateway-read-dashboard-from-metrics

Ownership legend: **[module: gateway worker]** — inside `internal/gateway/`, the sandboxed
worker's own territory, plus the explicitly granted `kkpa/context/`. This tier touches no other
module (tier 1, `analytics`, is already archived). See design.md D1–D11 for the rationale behind
each task; the Test Contract (fixtures RM38-G-Full / RM38-G-Nil / badge matrix / RM38-G-Empty)
is authoritative for every test's expected values.

**Hard ordering constraints:**
- Wave 1 (pure Go helpers: `dashLockedBadge`/`dashSentryBadge`, `dashStatus`'s new signature,
  `mergeVehicleStatuses`) has no dependency on any handler rewrite — it can be written and unit
  tested (Wave 2) against design.md's Test Contract before Wave 3 touches the call sites, so
  Wave 1 + Wave 2 run FIRST per the dispatch's ordering rule ("offline/pure-function tests go
  early — they compile against code that exists").
- Wave 3 (the four call-site rewrites) depends on Wave 1's helpers existing (it calls them).
- Wave 4 (template changes: badges, 3-col grid) depends on Wave 3's `DashboardData.Locked`/
  `SentryMode` fields existing, and on Wave 1's badge helpers.
- Wave 5 (fake test double + handler/integration tests) depends on Wave 3 AND Wave 4 — it
  exercises the full render path end to end, so it is the FINAL code wave, per the dispatch's
  ordering rule ("handler/integration tests go in the final wave").
- Wave 6 (docs) describes the POST-change state — runs after Wave 3/4 land (content, not
  compile, dependency).
- Wave 7 (verification) runs after every other wave.

## Wave 1 — pure Go helpers (module: gateway worker)

- [x] **1.1** `internal/gateway/templates/pages/dashboard.go` — add `dashLockedBadge(ctx
  context.Context, locked *bool) (text, kind string, show bool)` and `dashSentryBadge(ctx
  context.Context, sentry *bool) (text, kind string, show bool)` exactly per design.md D4 (badge
  matrix D7). Both are pure functions of `(ctx, *bool)` — no gin, no domain-module import beyond
  `i18n`.
  `depends_on`: — · `parallel_ok`: with 1.2, 1.3

- [x] **1.2** `internal/gateway/handlers/handlers.go` — rewrite `dashStatus`'s signature from
  `(ctx, telemetry.Snapshot) string` to `(ctx, chargingState *string) string` per design.md D2
  ("dashStatus's new signature"): nil or any value other than `"Charging"` collapses to
  `KeyDashboardStatusParked`. Do not touch its two i18n keys.
  `depends_on`: — · `parallel_ok`: with 1.1, 1.3

- [x] **1.3** `internal/gateway/handlers/handlers.go` — rename `mergeSnapshots` to
  `mergeVehicleStatuses`, retype its parameter/return from `[]telemetry.Snapshot`/
  `map[int64]telemetry.Snapshot` to `[]analytics.VehicleStatus`/`map[int64]analytics.
  VehicleStatus` (design.md D6). Body is otherwise byte-identical (same loop, same doc-comment
  shape, updated to name the new type).
  `depends_on`: — · `parallel_ok`: with 1.1, 1.2

## Wave 2 — offline unit tests for Wave 1 (module: gateway worker)

- [x] **2.1** `internal/gateway/templates/pages/dashboard_test.go` (new file, or add to an
  existing pages-package test file if one exists) — table-driven test covering all **nine**
  combinations of the badge matrix in design.md's Test Contract for `dashLockedBadge` and
  `dashSentryBadge` (three `Locked` states × three `SentryMode` states, asserting `text`, `kind`,
  and `show` for each). Pure function tests — no gin, no fakes.
  `depends_on`: 1.1 · `parallel_ok`: with 2.2, 2.3

- [x] **2.2** `internal/gateway/handlers/handlers_test.go` — extend (or add) a table-driven test
  for `dashStatus`'s new signature: `nil` → Parked, `ptrString("Charging")` → Charging,
  `ptrString("Disconnected")` → Parked, `ptrString("")` → Parked (parity with the old
  empty-string case).
  `depends_on`: 1.2 · `parallel_ok`: with 2.1, 2.3

- [x] **2.3** `internal/gateway/handlers/handlers_test.go` — rename/retype the existing
  `mergeSnapshots` unit test(s) to `mergeVehicleStatuses`/`analytics.VehicleStatus` fixtures;
  assert the empty/nil-slice → empty-map case and the by-`TeslaID` lookup case both still hold
  under the new type.
  `depends_on`: 1.3 · `parallel_ok`: with 2.1, 2.2

## Wave 3 — four call-site rewrites (module: gateway worker)

- [x] **3.1** `internal/gateway/handlers/handlers.go` — `dashboardFor`: replace
  `h.telemetryReader.LatestSnapshotsByAccount` with `h.analyticsReader.LatestMetricsByAccount`;
  replace `mergeSnapshots(snaps)` with `mergeVehicleStatuses(statuses)` (1.3). Update the
  telemetry-unavailable log line's wording ("analytics reader error", matching design.md D9's
  site-4 log-line convention). `mapDashboardSnapshot`'s signature changes to `(ctx,
  *fragments.DashboardData, analytics.VehicleStatus, time.Time)` and implements every row of
  design.md D2's per-field table verbatim: nil-safe `"—"` temperature formatting (local
  unexported helper, NOT shared with 3.2's — design.md D2 "Rejected" note), nil/empty `CarVersion`
  omits `SoftwareVer`, nil/`<=0` `ChargeLimitSocPct` omits `ChargeLimit`, `dashStatus(ctx,
  vs.ChargingState)` (1.2) for `StatusLabel`, nil `CapturedAt` leaves `LastUpdated`/`IsStale` at
  zero value (no branch needed beyond an `if vs.CapturedAt != nil` guard around the two
  assignments), and sets the two new `vm.Locked`/`vm.SentryMode` fields verbatim from
  `vs.Locked`/`vs.SentryMode`.
  `depends_on`: 1.1, 1.2, 1.3 · `parallel_ok`: with 3.2, 3.3, 3.4

- [x] **3.2** `internal/gateway/handlers/handlers.go` — `vehiclesFor`/`mapVehicles`: replace
  `h.telemetryReader.LatestSnapshotsByAccount` with `h.analyticsReader.LatestMetricsByAccount`;
  `mapVehicles`'s signature changes to `(vs []account.Vehicle, statusMap map[int64]analytics.
  VehicleStatus) []fragments.Vehicle` (design.md D3). Nil-safe mapping per D3's table: `"—"` for
  nil temps (own local helper, `%.1f °C` precision — NOT the same helper as 3.1's), `""` for nil
  `ChargingState`, zero-value `LastUpdated`/`IsStale` for nil `CapturedAt`.
  `depends_on`: 1.3 · `parallel_ok`: with 3.1, 3.3, 3.4

- [x] **3.3** `internal/gateway/templates/fragments/vehicles.templ` — `Vehicle.Locked` field
  type changes from `bool` to `*bool` (design.md D3); rewrite its render branch to the three-way
  shape shown in design.md D3 (nil → `KeyVehiclesNotReported`, else Locked/Unlocked), mirroring
  the existing `SentryMode` branch one block below it. Run `make templ` after editing (pinned
  `go tool templ generate`) to regenerate `vehicles_templ.go`.
  `depends_on`: 3.2 · `parallel_ok`: no (must follow 3.2's `fragments.Vehicle` field-type change
  landing first so the two stay in sync)

- [x] **3.4** `internal/gateway/handlers/handlers.go` (`navHeaderFor`) and
  `internal/gateway/handlers/charges.go` (`buildChargesPage`'s battery-suggestion block) —
  replace their `h.telemetryReader.LatestSnapshotsByAccount` calls with
  `h.analyticsReader.LatestMetricsByAccount`. `navHeaderFor` uses `mergeVehicleStatuses` (1.3)
  and implements design.md D8's nil-`CapturedAt` → forced-Asleep-no-label branch (inserted
  BEFORE the existing `connectedAt`/`relativeLastSeen` calls, both of which now dereference
  `*vs.CapturedAt` inside their own already-nil-guarded branch). `charges.go`'s suggestion loop
  is a type-only change (`statuses []analytics.VehicleStatus`, `s.BatteryLevelPct` unchanged) per
  design.md D9 — no nil handling needed there.
  `depends_on`: 1.3 · `parallel_ok`: with 3.1, 3.2, 3.3

## Wave 4 — template changes (module: gateway worker)

- [x] **4.1** `internal/gateway/templates/fragments/dashboard_vm.go` — add `Locked *bool` and
  `SentryMode *bool` fields to `fragments.DashboardData`, doc-commented per design.md D2's table
  (mirrors the existing field-doc style in this file).
  `depends_on`: — · `parallel_ok`: with 4.2 (this is a struct-only edit Wave 3.1 already assumes
  exists — land it first or together; no compile-order risk either way since it's additive)

- [x] **4.2** `internal/gateway/templates/pages/dashboard.templ` — header row: add the Locked
  and Sentry `ui.Badge` pills next to the existing `[Stale]` badge, calling `dashLockedBadge`/
  `dashSentryBadge` (1.1) exactly per design.md D4's markup block. Verify the templ
  init-statement form (`if a, b, c := f(); c { ... }`) compiles against the pinned templ version
  via Context7 (`/a-h/templ`) before committing to that exact syntax; fall back to the
  Go-code-block form design.md D4 describes if unsupported — either way, zero business logic
  moves into the template. Stat grid: change `grid-cols-2` to `grid-cols-3` and delete the
  fourth `ui.StatTile` call (`KeyDashboardStatus`) per design.md D5. Do NOT delete the
  `KeyDashboardStatus` catalogue entry (D5 — left in place, unused, deliberately).
  `depends_on`: 1.1, 4.1 · `parallel_ok`: no (single file)

- [x] **4.3** Run `make templ` (regenerates `dashboard_templ.go`, `vehicles_templ.go` from 3.3 +
  4.2) and `make css` (only if 4.2 introduces a class not already in `app.css` — `grid-cols-3`
  and the badge markup use only classes already present elsewhere in this file, so this is a
  verification step, not expected to change `app.css`; run it anyway and check
  `git diff --stat internal/gateway/static/app.css` per the module's own "stale CSS" gotcha).
  `depends_on`: 3.3, 4.2 · `parallel_ok`: no

## Wave 5 — fake test double + handler/integration tests (module: gateway worker, FINAL code wave)

- [x] **5.1** `internal/gateway/handlers/history_test.go` — extend `fakeAnalyticsReader` with
  `statuses []analytics.VehicleStatus` and `statusesErr error` fields; implement
  `LatestMetricsByAccount` to return them instead of panicking (design.md "Fake test double").
  Confirm the existing history-fragment tests, which never call this method, are unaffected
  (their zero-value `statuses`/`statusesErr` fields are simply unused).
  `depends_on`: — · `parallel_ok`: yes (independent of Waves 3/4's production code, but must
  land before 5.2–5.5 use it)

- [x] **5.2** `internal/gateway/handlers/handlers_test.go` — `dashboardFor`/`mapDashboardSnapshot`
  tests: add Fixture RM38-G-Full and Fixture RM38-G-Nil (design.md Test Contract, exact field
  values) as `fakeAnalyticsReader{statuses: [...]}` cases; assert every `DashboardData` field
  listed in each fixture's expected-value table, including the two new `Locked`/`SentryMode`
  fields. Rewrite any existing test that built a `telemetry.Snapshot` fixture for this path to
  build an `analytics.VehicleStatus` fixture instead.
  `depends_on`: 3.1, 5.1 · `parallel_ok`: with 5.3, 5.4, 5.5

- [x] **5.3** `internal/gateway/handlers/handlers_test.go` (or a new `dashboard_test.go` in the
  `handlers` package) — `httptest` render assertions for the dashboard page/fragment covering:
  Fixture Full's Locked+Sentry badges both render with the correct text/kind; Fixture Nil's
  header shows neither badge and no `[Stale]` badge and no date line; one mixed case
  (`Locked=*true, SentryMode=nil`) showing exactly one badge. Confirms 1.1's helpers are actually
  wired into the template (Wave 2's tests only prove the helpers are correct in isolation).
  `depends_on`: 4.3, 5.1 · `parallel_ok`: with 5.2, 5.4, 5.5

- [x] **5.4** `internal/gateway/handlers/handlers_test.go` — `navHeaderFor` tests: add a case
  using Fixture RM38-G-Nil (or any `VehicleStatus` with `CapturedAt: nil`) asserting `Status ==
  NavStatusAsleep` and `LastSeenLabel == ""` (design.md D8/Test Contract) — the regression test
  for roadmap D9's "never Connected on a nil timestamp" rule. Rewrite existing
  Connected/Asleep/Awaiting tests for this function to build `analytics.VehicleStatus` fixtures
  instead of `telemetry.Snapshot`.
  `depends_on`: 3.4, 5.1 · `parallel_ok`: with 5.2, 5.3, 5.5

- [x] **5.5** `internal/gateway/handlers/charges_test.go` — retype the battery-suggestion test
  fixture(s) for `buildChargesPage` from `telemetry.Snapshot` to `analytics.VehicleStatus`; no
  new case is required (design.md D9 — no nil-handling was introduced at this site), but the
  existing suggestion-populated and suggestion-absent cases must still pass under the new type.
  `depends_on`: 3.4, 5.1 · `parallel_ok`: with 5.2, 5.3, 5.4

- [x] **5.6** `internal/gateway/templates/fragments/vehicles_templ.go`-adjacent test file (if one
  exists covering `VehiclesList`/`mapVehicles`; otherwise extend `handlers_test.go`'s
  `vehiclesFor` coverage) — assert the three-way `Locked` rendering (nil/false/true) added in
  3.3, mirroring the existing `SentryMode` three-way test if one exists.
  `depends_on`: 3.2, 3.3 · `parallel_ok`: with 5.2–5.5

## Wave 6 — documentation (module: gateway worker)

- [x] **6.1** `internal/gateway/AGENTS.md` — update the `Deps.AnalyticsReader` bullet to add
  `LatestMetricsByAccount` and its four callers (design.md D11 item 1); update the
  `Deps.TelemetryReader` bullet to say `SnapshotsByVehicleBetween` (history only) is its sole
  remaining caller as of this tier (design.md D11 item 2).
  `depends_on`: 3.1, 3.2, 3.4 · `parallel_ok`: with 6.2

- [x] **6.2** `kkpa/context/use-case/gateway/read-dashboard-bento.md` — rewrite step 5 of "Flow"
  and row 2 of "Database" from `telemetry.Reader.LatestSnapshotsByAccount`/`vehicle_snapshots`
  to `analytics.Reader.LatestMetricsByAccount`/`vehicle_metrics`; rewrite the "gateway must stop
  depending on telemetry" gotcha to state this specific use case is now resolved, while
  `read-dashboard-history.md`'s `SnapshotsByVehicleBetween` read is a different, untouched use
  case (design.md D11 item 3). Do not edit `read-dashboard-history.md`.
  `depends_on`: 3.1 · `parallel_ok`: with 6.1

## Wave 7 — verification (assistant-run signals, then owner-run suite)

- [x] **7.1** Run and report: `go build ./...`, `go vet ./...`, `gofmt -l`, `make build`,
  `make vet`, `make bins`, `make templ`, `make css` (already run in 4.3; re-run here to confirm
  a clean tree after Wave 5/6 edits), `make boundary-guard` (expected to still fail — unchanged
  from before this tier, since `handlers.go`/`gateway.go`/`history.go` still import
  `internal/telemetry` for `SnapshotsByVehicleBetween` and the `Deps.TelemetryReader` field type
  — confirm the LatestSnapshotsByAccount call sites it previously flagged are gone from its
  output), `make i18n-guard` (expected clean — no new hardcoded string, no new key). Per the
  Test-Execution-Policy, never `go test ./...`, `make test`, `make test-with-db`, or `make check`.
  `depends_on`: 1.1–6.2 (every prior wave) · `parallel_ok`: no

- [x] **7.2** Hand off to the owner the exact command to run and report:
  `go test ./internal/gateway/...` (covers Wave 2's offline helper tests and Wave 5's handler
  tests — none of this tier's tests are `DATABASE_URL`-gated, since no database object changed).
  Until the owner reports a pass, this tier's implementation status is
  **awaiting-user-verification**, never "done" (`CLAUDE.md` "Builds & local checks").
  `depends_on`: 7.1 · `parallel_ok`: no
