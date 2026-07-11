> **Additive, non-breaking change** (tier 5 of `openspec/roadmaps/nightly-vehicle-telemetry.md`).
> Read-only gateway extension — no DB object, no migration, no live Tesla call on normal renders.
> Consumes the existing `telemetry.Reader` port from tier 4. All business logic (km conversion,
> staleness, sentry-nil rendering) lives in the Go handler; templates stay logic-light.
>
> **Dependencies / parallelism:**
> - Tasks 1 and 2 touch disjoint files and have no inter-dependencies — MAY run in parallel.
> - Task 3 depends on task 1 (needs the updated `Deps`/`Handler`/`New()` signatures from gateway.go
>   and handlers.go before the `.templ` file references the extended view struct).
> - Task 4 (`AGENTS.md` append) has no dependencies — disjoint file; MAY run any time.
> - Task 5 (leader-integrated cmd/web wiring) has no dependency on tasks 1–4 but SHOULD be
>   confirmed complete before Task 6 verification (so `go build ./...` includes the wiring).
> - Task 6 (verification) depends on tasks 1–5 all complete (including leader-integrated 5).
>
> **Leader-integrated task (outside `internal/gateway/` sandbox):** Task 5 — `cmd/web/main.go`
> construction of `telemetry.NewReader(pool)` and injection into `gateway.Deps`. The gateway
> worker adds `TelemetryReader telemetry.Reader` to `Deps` (task 1) and the leader wires the
> concrete instance in `cmd/web/main.go` (task 5).

## 1. Extend `gateway.Deps`, `handlers.Deps`, `Handler`, and `New()` — no dependencies, parallel-ok with 2

- [ ] 1.1 In `internal/gateway/gateway.go`: add `TelemetryReader telemetry.Reader` to the `Deps`
      struct. Import `internal/telemetry` (the public port — never `internal/telemetry/db`).
      No other change to `gateway.go` at this stage (routes `/dashboard` and `/ui/vehicles`
      are unchanged).

- [ ] 1.2 In `internal/gateway/handlers/handlers.go`: mirror the new field in `handlers.Deps`
      (add `TelemetryReader telemetry.Reader`), in the `Handler` struct (add `telemetryReader
      telemetry.Reader`), and in the `New(d Deps) *Handler` constructor (assign
      `telemetryReader: d.TelemetryReader`). Import `internal/telemetry`.

- [ ] 1.3 Extend `vehiclesFor(ctx, uid)` in `internal/gateway/handlers/handlers.go`:
      - After a successful `account.RegisteredVehicles(ctx, uid)` call, call
        `h.telemetryReader.LatestSnapshotsByAccount(ctx, uid)`.
      - On error from the `Reader`, log the error and continue with an empty snapshot
        map (graceful degradation — do NOT return early or propagate the error; set a
        notice field instead).
      - Build a `map[int64]telemetry.Snapshot` keyed by `TeslaID` from the returned slice.
      - Pass the snapshot map to `mapVehicles` (updated in task 1.4).
      - When the `Reader` returns an error, the `VehiclesData` notice must convey
        "Telemetry unavailable — showing vehicle identity only."

- [ ] 1.4 Extend `mapVehicles` (or create a new `mergeSnapshots` helper, whichever keeps
      the function clean) in `internal/gateway/handlers/handlers.go`:
      - Accept the snapshot map `map[int64]telemetry.Snapshot` as a second argument.
      - For each registered `account.Vehicle`, look up `snaps[v.TeslaID]`:
        - Found: set `HasSnapshot: true`; populate all Extended fields on the view model
          (see D3 in design.md). Compute:
          - `BatteryRangeKm` via `snap.BatteryRangeKm()`
          - `OdometerKm` via `snap.OdometerKm()`
          - `InsideTempC`, `OutsideTempC` as `snap.InsideTemp`, `snap.OutsideTemp` (°C, no conversion)
          - `IsStale` as `time.Since(snap.CapturedAt) > stalenessThreshold`
          - `LastUpdated` as a pre-formatted string (e.g. `snap.CapturedAt.Format(time.RFC1123)` or
            a project-consistent format — document the choice)
          - `SentryMode *bool` passed through as-is (nil/false/true)
        - Not found: set `HasSnapshot: false`; leave all snapshot-derived fields at
          zero values.
      - Add the named constant `stalenessThreshold = 36 * time.Hour` at the top of
        the handler file (package-level, exported or unexported — `unexported` is fine
        since it is used only within the handler package).

## 2. Extend `fragments.Vehicle` view struct and update `VehiclesList` template — no dependencies, parallel-ok with 1

- [ ] 2.1 In `internal/gateway/templates/fragments/vehicles.templ`: extend the `Vehicle`
      struct with the following fields (all display-ready, pre-computed by the handler):
      ```
      HasSnapshot    bool
      BatteryLevel   int
      BatteryRangeKm float64
      ChargingState  string
      OdometerKm     float64
      InsideTempC    float64
      OutsideTempC   float64
      Locked         bool
      SentryMode     *bool
      LastUpdated    string
      IsStale        bool
      ```
      No business logic is added to the struct or template. Fields are only read in the
      component.

- [ ] 2.2 Update the `VehiclesList` component in `vehicles.templ` to render the enriched
      card and the two alternate states:
      - **Enriched card** (`HasSnapshot == true`): render all Extended fields. Sentry
        mode must visually distinguish nil ("Not reported"), false ("Off"), true ("On").
        Show the `LastUpdated` string. When `IsStale == true`, render a visible stale
        marker (e.g. a badge or text such as "⚠ Stale") alongside the last-updated label.
        No arithmetic, no method calls, no time formatting in the template — all values
        are pre-computed.
      - **Placeholder card** (`HasSnapshot == false`, no telemetry error): show vehicle
        identity (DisplayName, VIN) plus a placeholder message ("No data yet — awaiting
        first nightly snapshot"). No stale marker.
      - **Telemetry unavailable notice** (when `VehiclesData.Notice` is set due to a
        Reader error): render a non-fatal notice banner above the vehicle list (e.g.
        "Telemetry unavailable — showing vehicle identity only"). Each vehicle card still
        renders in placeholder state.
      - The `<div id="vehicles">` root element and `templ.Fragment("vehicles")` fragment
        id are UNCHANGED (htmx swap target must remain stable).

- [ ] 2.3 After editing `vehicles.templ`, note in `tasks.md` that **`templ generate` must
      be run** by the implementer (or the leader, per the project rule that codegen is a
      developer step, not an assistant step). The generated `vehicles_templ.go` must be
      committed alongside the `.templ` edit.

## 3. Verify handler–template contract alignment — depends on 1 and 2

- [ ] 3.1 Confirm that every field added to `fragments.Vehicle` in task 2.1 is populated
      by the handler mapper in task 1.4 — and vice versa: no template field is left
      without a mapping rule in the handler. Document any zero-value fields that are
      intentionally left unset for the placeholder state.

- [ ] 3.2 Write or extend unit tests in `internal/gateway/handlers/handlers_test.go`:
      - **Enriched state:** fake `TelemetryReader` returns one snapshot for the
        registered vehicle; assert the resulting `fragments.Vehicle` has `HasSnapshot:
        true`, correct `BatteryRangeKm`, correct `IsStale` value (test both fresh and
        stale), and `SentryMode` set correctly for all three pointer states (nil / false /
        true).
      - **Placeholder state:** fake `TelemetryReader` returns an empty slice (no
        snapshot for the vehicle); assert `HasSnapshot: false`, all snapshot fields at
        zero values.
      - **Graceful degradation:** fake `TelemetryReader` returns an error; assert the
        returned `VehiclesData.Notice` contains the degradation message and all vehicles
        have `HasSnapshot: false`.
      - **Stale boundary:** test that `IsStale` is false when `CapturedAt` is exactly
        at the threshold and true when it is one second past it.
      Use `httptest` + `NewEngine` (or direct handler invocation) following the existing
      test patterns in the handler package. Fake the `telemetry.Reader` interface — never
      import `telemetrydb`.

## 4. Append `telemetry.Reader` dependency note to `AGENTS.md` — no dependencies, parallel-ok

- [ ] 4.1 In `internal/gateway/AGENTS.md`, APPEND (never remove or modify existing content)
      the following note to the `## Public interface` section (or `Deps` section if one exists):

      > - `Deps.TelemetryReader telemetry.Reader` — the telemetry read port; injected at
      >   construction via `gateway.Deps` and `handlers.Deps`. The gateway calls
      >   `LatestSnapshotsByAccount(ctx, accountID)` once per dashboard render to populate
      >   vehicle card telemetry. Added by `gateway-read-stored-vehicles` (tier 5).
      >   NEVER import `internal/telemetry/db` (`telemetrydb`) — all access through this
      >   interface only.

## 5. Wire `telemetry.NewReader(pool)` in `cmd/web/main.go` — LEADER-INTEGRATED

- [ ] 5.1 In `cmd/web/main.go`: construct `telemetry.NewReader(pool)` (using the existing
      `pgxpool.Pool` already present in `cmd/web`). Pass the result as
      `gateway.Deps{TelemetryReader: telemetryReader, ...}` alongside the existing
      account, tesla, and googleauth deps. Import `internal/telemetry`. This is a
      leader-integrated step outside the gateway worker sandbox.

## 6. Verification — depends on 1–5

- [ ] 6.1 `go build ./...` passes with no errors. All new `telemetry.Reader` usages in
      the gateway compile cleanly. The generated `vehicles_templ.go` is present and
      current (regenerated after task 2 edits). `cmd/web` compiles with the new
      `TelemetryReader` field in `gateway.Deps`.

- [ ] 6.2 `go vet ./...` passes with no warnings.

- [ ] 6.3 `go test ./...` is green and fast. The handler unit tests added in task 3.2 run
      without a database or network. No Tesla API call fires. The `DATABASE_URL`-gated
      integration tests in `internal/telemetry` continue to self-skip when `DATABASE_URL`
      is unset.

- [ ] 6.4 `openspec validate gateway-read-stored-vehicles --strict` passes. All
      tasks.md checkboxes reflect real completion.

- [ ] 6.5 Boundary check: `internal/gateway` imports `internal/telemetry` (the public
      port package) but NOT `internal/telemetry/db` (`telemetrydb`). No `pgtype` type
      appears in any gateway file. No `...Tesla`-suffixed DTO leaks into a template.
      No km field exists on any domain type (km values live only in the gateway view
      model). The `<div id="vehicles">` fragment id is unchanged.
