> **Additive, non-breaking new-module change.** Creates `internal/battery/` — the platform's
> first derived-metrics module — with its `Reader` port (`RecentEfficiency`), the Wh/km
> derivation (measured kWh + capacity SoC correction, design.md D1/D1b/D2), an in-package
> pack-capacity reference table, and offline unit tests (no DB, no live Tesla call). No
> migration, no new table, no new index, no gateway change — wiring `battery.Reader` into
> `gateway.Deps` is the separate follow-on change `apex-dashboard-efficiency-tile`. Depends on
> `telemetry-add-snapshot-history-read-port` (archived; already provides
> `telemetry.Reader.SnapshotsByVehicleSince`) and `RM6-telemetry-capture-vehicle-config`
> (archived; already provides `account.Vehicle.CarType`).
>
> **Dependencies / parallelism:**
>
> - T1 (module skeleton: `Reader`, `Efficiency`, `DefaultWindow`) has no dependencies.
> - T2 (pack-capacity reference table) has no dependencies. Disjoint file from T1 — MAY run in
>   parallel with T1.
> - T3 (pure derivation functions) depends on T1 (needs the `Efficiency` type). Disjoint file
>   from T2 — MAY run in parallel with T2 once T1 lands.
> - T4 (port wiring: `reader` struct, `vehicleLookup`, `NewReader`, `RecentEfficiency`) depends
>   on T1, T2, T3 (uses `Efficiency`, `capacityFor`, `deriveEfficiency` together). This is the
>   core task — serialize it after T1–T3.
> - T5 (pure-function unit tests) depends on T3 only — does NOT need T4. MAY run in parallel
>   with T4.
> - T6 (fake-port unit tests) depends on T4 (exercises `RecentEfficiency` end-to-end via fakes).
> - T7 (module `AGENTS.md`) has no functional dependencies — author alongside T1, but keep as
>   its own checklist item since it is a distinct deliverable file.
> - T8 (verification) depends on T1–T7.
>
> **No leader-integrated step required.** No `sqlc.yaml` change, no `make sqlc`, no migration, no
> `make migrate-up` — this change touches zero database objects (design.md "Database Changes").

---

## T1. Module skeleton — `Reader`, `Efficiency`, `DefaultWindow` (`internal/battery/battery.go`) — no dependencies

- [x] T1.1 Create `internal/battery/battery.go` with the package doc comment (module
      responsibility, one-line summary of what it derives and from what — mirror the doc-comment
      style at the top of `internal/manualcharge/manualcharge.go` / `internal/telemetry/telemetry.go`),
      and:
      ```go
      package battery

      import (
          "context"
          "time"

          "github.com/google/uuid"
      )

      // DefaultWindow is the recommended NewReader window — 30 days, matching the dashboard's
      // 30-day history cards (design.md D3). Deployment code may pass a different duration for
      // a future analytics page without changing the port.
      const DefaultWindow = 30 * 24 * time.Hour

      // Reader is the battery module's public port (ai/go-conventions.md interface-first).
      type Reader interface {
          RecentEfficiency(ctx context.Context, accountID uuid.UUID, teslaID int64) (Efficiency, bool, error)
      }

      // Efficiency is one computed rolling-efficiency result — our own domain model, no vendor
      // suffix (ai/architecture.md §6).
      type Efficiency struct {
          WhPerKm         float64
          FromKm          float64
          ToKm            float64
          BatteryDeltaPct float64
          Approximate     bool
      }
      ```
      Copy the full doc comments for `Reader.RecentEfficiency` and each `Efficiency` field
      verbatim from design.md "Public Surface" (they encode the ok=false/Approximate contract a
      future reader must not have to re-derive from the design doc).
      Acceptance: `go build ./internal/battery/...` passes (package compiles standalone with no
      other files yet — T2–T4 add the rest).

## T2. Pack-capacity reference table (`internal/battery/capacity.go`) — no dependencies

- [x] T2.1 Create `internal/battery/capacity.go` with a doc comment explaining this is a
      human-maintained reference table, not a database object (design.md D1b — link it), sourced
      from public Tesla spec sheets, and is model-coarse (keyed on `car_type`), not trim-exact
      (`trim_badging` is not extracted anywhere in the platform — see backlog item 7).
      ```go
      package battery

      // packCapacityKWh maps a Fleet API vehicle_config.car_type code to that model's
      // approximate usable pack capacity in kWh. Human-maintained from public spec sheets;
      // model-coarse, not trim-exact (design.md D1b). Add a new entry when a model this
      // platform's users own is missing — until then, that model's efficiency is still
      // computed (Approximate=true), never blanked.
      var packCapacityKWh = map[string]float64{
          "model3": 75,
          "modely": 75,
          "models": 100,
          "modelx": 100,
      }

      // capacityFor returns the usable pack capacity in kWh for carType, and whether it was
      // found. An empty carType (unknown CarType, or a vehicle not found in the account's
      // registered list) naturally returns known=false — "" is never a map key.
      func capacityFor(carType string) (kWh float64, known bool) {
          kWh, known = packCapacityKWh[carType]
          return kWh, known
      }
      ```
      Acceptance: `go build ./internal/battery/...` passes; `go vet ./...` clean.

## T3. Pure derivation functions (`internal/battery/derive.go`) — depends on T1

- [x] T3.1 Create `internal/battery/derive.go` implementing `socReadings` (design.md D2) and
      `deriveEfficiency` (design.md D1/D1b/D-ok). Import `internal/telemetry` for the `Snapshot`
      type only (no port, no pgxpool — this file has zero I/O).
      ```go
      package battery

      import "github.com/cristianpena/magus-tesla-api/internal/telemetry"

      // socReadings returns the state-of-charge percent at the window's start and end,
      // choosing UsableBatteryLevel for BOTH endpoints when both are non-nil, else
      // BatteryLevel for both — never a mixed pair (design.md D2).
      func socReadings(start, end telemetry.Snapshot) (socStart, socEnd float64) {
          if start.UsableBatteryLevel != nil && end.UsableBatteryLevel != nil {
              return float64(*start.UsableBatteryLevel), float64(*end.UsableBatteryLevel)
          }
          return float64(start.BatteryLevel), float64(end.BatteryLevel)
      }

      // deriveEfficiency computes the Wh/km result from a window's ordered (oldest-first)
      // snapshots, the summed measured charging energy (kWh) observed in the window, and the
      // vehicle's pack capacity (capacityKnown=false when unknown — design.md D1b). It returns
      // ok=false (design.md D-ok) when: fewer than 2 snapshots; distance <= 0; or the derived
      // energy <= 0.
      func deriveEfficiency(snapshots []telemetry.Snapshot, kWhIn float64, capacityKWh float64, capacityKnown bool) (Efficiency, bool) {
          if len(snapshots) < 2 {
              return Efficiency{}, false
          }
          start, end := snapshots[0], snapshots[len(snapshots)-1]

          distance := end.OdometerKm() - start.OdometerKm()
          if distance <= 0 {
              return Efficiency{}, false
          }

          socStart, socEnd := socReadings(start, end)
          deltaSoC := socEnd - socStart // positive = net charge, per design.md D1's formula

          var energy float64
          approximate := false
          if capacityKnown {
              energy = kWhIn - capacityKWh*deltaSoC/100
          } else {
              energy = kWhIn
              approximate = true
          }
          if energy <= 0 {
              return Efficiency{}, false
          }

          return Efficiency{
              WhPerKm:         energy * 1000 / distance,
              FromKm:          start.OdometerKm(),
              ToKm:            end.OdometerKm(),
              BatteryDeltaPct: socStart - socEnd, // negative = net charge (Efficiency doc comment)
              Approximate:     approximate,
          }, true
      }
      ```
      Acceptance: `go build ./internal/battery/...` passes; `go vet ./...` clean. Note the sign
      flip between `deltaSoC` (used in the energy formula, positive = net charge, per design.md's
      literal `ΔSoC = SoC_end − SoC_start`) and `Efficiency.BatteryDeltaPct` (the reported field,
      `start − end`, negative = net charge, per the field's doc comment) — both are intentional
      and must NOT be unified into one sign convention; T5 tests both directions explicitly.

## T4. Port wiring (`internal/battery/reader.go`) — depends on T1, T2, T3

- [x] T4.1 Create `internal/battery/reader.go` with the `vehicleLookup` narrow interface, the
      `reader` struct, `NewReader`, and `RecentEfficiency`. Import `internal/telemetry`,
      `internal/manualcharge`, `internal/account`, `uuid`, `context`, `time`.
      ```go
      package battery

      import (
          "context"
          "time"

          "github.com/google/uuid"

          "github.com/cristianpena/magus-tesla-api/internal/account"
          "github.com/cristianpena/magus-tesla-api/internal/manualcharge"
          "github.com/cristianpena/magus-tesla-api/internal/telemetry"
      )

      // chargingSourceLimit bounds each of the two charging-cost source reads (design.md D6).
      // Neither port supports a since filter; battery fetches this many newest-first rows and
      // filters to the window in Go. Generous relative to any plausible 30-day session count.
      const chargingSourceLimit = 200

      // vehicleLookup is a narrow consumer interface over account.Service — this module needs
      // only CarType resolution for one vehicle (ai/go-conventions.md "accept interfaces").
      // Any account.Service implementation satisfies this automatically.
      type vehicleLookup interface {
          RegisteredVehicles(ctx context.Context, accountID uuid.UUID) ([]account.Vehicle, error)
      }

      type reader struct {
          telemetry    telemetry.Reader
          supercharger telemetry.SuperchargerReader
          manual       manualcharge.Reader
          account      vehicleLookup
          window       time.Duration
          now          func() time.Time
      }

      // Compile-time assertion: *reader must satisfy the public Reader interface.
      var _ Reader = (*reader)(nil)

      // NewReader constructs a Reader over the three sibling ports it consumes plus a narrow
      // account lookup, with window fixed at construction time (design.md D3).
      func NewReader(telemetryReader telemetry.Reader, supercharger telemetry.SuperchargerReader, manual manualcharge.Reader, acct vehicleLookup, window time.Duration) Reader {
          return &reader{
              telemetry:    telemetryReader,
              supercharger: supercharger,
              manual:       manual,
              account:      acct,
              window:       window,
              now:          time.Now,
          }
      }

      func carTypeFor(ctx context.Context, acct vehicleLookup, accountID uuid.UUID, teslaID int64) (string, error) {
          vehicles, err := acct.RegisteredVehicles(ctx, accountID)
          if err != nil {
              return "", err
          }
          for _, v := range vehicles {
              if v.TeslaID == teslaID && v.CarType != nil {
                  return *v.CarType, nil
              }
          }
          return "", nil
      }

      func sumSuperchargerKWh(sessions []telemetry.SuperchargerSession, since time.Time) float64 {
          var total float64
          for _, s := range sessions {
              if s.ChargeStartDateTime.Before(since) {
                  continue
              }
              if s.EnergyKWh != nil {
                  total += *s.EnergyKWh
              }
          }
          return total
      }

      func sumManualKWh(entries []manualcharge.Entry, since time.Time) float64 {
          var total float64
          for _, e := range entries {
              if e.ChargedOn.Before(since) {
                  continue
              }
              total += e.EnergyAddedKWh
          }
          return total
      }

      // RecentEfficiency implements Reader (design.md "Public Surface", D4 accountID scoping).
      func (r *reader) RecentEfficiency(ctx context.Context, accountID uuid.UUID, teslaID int64) (Efficiency, bool, error) {
          since := r.now().Add(-r.window)

          snapshots, err := r.telemetry.SnapshotsByVehicleSince(ctx, accountID, teslaID, since)
          if err != nil {
              return Efficiency{}, false, err
          }

          sessions, err := r.supercharger.SuperchargerSessionsByVehicle(ctx, accountID, teslaID, chargingSourceLimit)
          if err != nil {
              return Efficiency{}, false, err
          }

          entries, err := r.manual.ListEntriesByVehicle(ctx, accountID, teslaID, chargingSourceLimit)
          if err != nil {
              return Efficiency{}, false, err
          }

          carType, err := carTypeFor(ctx, r.account, accountID, teslaID)
          if err != nil {
              return Efficiency{}, false, err
          }

          kWhIn := sumSuperchargerKWh(sessions, since) + sumManualKWh(entries, since)
          capacity, known := capacityFor(carType)

          return deriveEfficiency(snapshots, kWhIn, capacity, known)
      }
      ```
      Acceptance: `go build ./...` passes; `go vet ./...` clean. Confirm `internal/battery`
      imports only `internal/telemetry`, `internal/manualcharge`, `internal/account` PUBLIC types
      (`telemetry.Reader`, `telemetry.SuperchargerReader`, `telemetry.Snapshot`,
      `telemetry.SuperchargerSession`, `manualcharge.Reader`, `manualcharge.Entry`,
      `account.Vehicle`) — grep confirms zero references to `telemetrydb`, `manualchargedb`,
      `accountdb`.

## T5. Pure-function unit tests (`internal/battery/derive_test.go`) — depends on T3

- [x] T5.1 Add `TestDeriveEfficiency_KnownCapacity_NetConsumption` — two snapshots with a known
      capacity and net SoC decrease; assert `ok=true`, `Approximate=false`, and the returned
      `WhPerKm`/`BatteryDeltaPct` match a hand-computed expected value (assert with a small
      epsilon, mirroring `TestReader_KmCompanions`'s `1e-9` pattern in
      `internal/telemetry/reader_test.go`).
- [x] T5.2 Add `TestDeriveEfficiency_UnknownCapacity_ApproximateTrue` — same shape but
      `capacityKnown=false`; assert `ok=true`, `Approximate=true`, and `WhPerKm` equals
      `kWhIn*1000/distance` with NO capacity term subtracted.
- [x] T5.3 Add `TestDeriveEfficiency_FewerThanTwoSnapshots_NotOK` — 0 and 1-element slices;
      assert `ok=false` for both, zero-value `Efficiency`.
- [x] T5.4 Add `TestDeriveEfficiency_NonIncreasingOdometer_NotOK` — end odometer <= start
      odometer; assert `ok=false`.
- [x] T5.5 Add `TestDeriveEfficiency_NetChargeExceedsConsumption_NotOK` — capacity known, `kWhIn`
      small, large positive `deltaSoC` (net charge) driving `energy <= 0`; assert `ok=false`.
- [x] T5.6 Add `TestSocReadings_UsableAtBothEndpoints` and
      `TestSocReadings_FallsBackWhenEitherEndpointNil` (one test per case: nil at start only, nil
      at end only, nil at both) — assert `socReadings` never mixes `UsableBatteryLevel` from one
      endpoint with `BatteryLevel` from the other (design.md D2 — the phantom-ΔSoC bug this
      guards against).
- [x] T5.7 Add `TestDeriveEfficiency_BatteryDeltaPctSignConvention` — asserts
      `Efficiency.BatteryDeltaPct` is negative when the vehicle net-charged (SoC end > SoC start)
      and positive when it net-consumed (SoC end < SoC start) — locks in the sign-flip documented
      in T3.1's acceptance note so a future refactor cannot silently invert it.
      Acceptance for T5.1–T5.7: `go test ./internal/battery/... -run TestDeriveEfficiency` and
      `-run TestSocReadings` pass; zero network, zero DB, sub-second.

## T6. Fake-port unit tests (`internal/battery/reader_test.go`) — depends on T4

- [x] T6.1 Define fakes for the four dependencies, one per interface, inline in this file
      (mirror `fakeReadStore`/`fakeHistoryStore` in `internal/telemetry/reader_test.go` — a
      struct holding canned return values / errors, plus captured call arguments where the test
      needs to assert pass-through):
      ```go
      type fakeTelemetryReader struct {
          snapshots  []telemetry.Snapshot
          err        error
          gotSince   time.Time
      }
      // SnapshotsByVehicleSince records gotSince and returns snapshots/err.

      type fakeSuperchargerReader struct {
          sessions []telemetry.SuperchargerSession
          err      error
      }
      // SuperchargerSessionsByVehicle returns sessions/err.

      type fakeManualReader struct {
          entries []manualcharge.Entry
          err     error
      }
      // ListEntriesByVehicle returns entries/err.

      type fakeVehicleLookup struct {
          vehicles []account.Vehicle
          err      error
      }
      // RegisteredVehicles returns vehicles/err.
      ```
- [x] T6.2 Add `TestRecentEfficiency_HappyPath_ComputesValue` — wires all four fakes with a
      coherent scenario (2+ snapshots, a matching `account.Vehicle` with a known `CarType`, one
      supercharger session and one manual entry inside the window), a fixed `now` (inject via
      `&reader{..., now: func() time.Time { return fixed }}`, constructed directly — mirrors
      `r := &reader{store: fake}` in `internal/telemetry/reader_test.go`, NOT through
      `NewReader`). Assert `ok=true` and the returned `Efficiency` matches a hand-computed value.
- [x] T6.3 Add `TestRecentEfficiency_WindowExcludesOldEntries` — a supercharger session and a
      manual entry both dated BEFORE the window's `since`; assert they are excluded from the
      summed `kWhIn` (i.e. the computed result differs from a version where they'd been
      included) — proves the D6 in-Go date filter actually filters.
- [x] T6.4 Add `TestRecentEfficiency_UnknownVehicle_ApproximateTrue` — `fakeVehicleLookup`
      returns a vehicle list with no entry for the requested `teslaID` (or `CarType=nil`); assert
      `ok=true`, `Approximate=true`.
- [x] T6.5 Add one test per port-error propagation case —
      `TestRecentEfficiency_TelemetryError_Propagates`,
      `TestRecentEfficiency_SuperchargerError_Propagates`,
      `TestRecentEfficiency_ManualChargeError_Propagates`,
      `TestRecentEfficiency_AccountError_Propagates` — each fake returns a distinct sentinel
      error; assert `RecentEfficiency` returns it via `errors.Is`, unwrapped (same contract as
      `TestReader_StoreError_PropagatesError` in `internal/telemetry/reader_test.go`).
- [x] T6.6 Add `TestRecentEfficiency_AccountIDScoping_PassedToEveryPort` — asserts the same
      `accountID` argument reaches all four fakes' captured call arguments unchanged (design.md
      D4 / spec.md "Multi-Tenant Scoping").
      Acceptance for T6.1–T6.6: `go test ./internal/battery/... -run TestRecentEfficiency`
      passes; zero network, zero DB.

## T7. Module `AGENTS.md` (`internal/battery/AGENTS.md`) — no functional dependencies

- [x] T7.1 Author `internal/battery/AGENTS.md` per the project's per-module doc convention
      (mirror `internal/telemetry/AGENTS.md` / `internal/gateway/AGENTS.md` structure): an
      `Agent-Name: battery` header, `## Doc-Pack (module)` (additive to the base pack — may list
      nothing extra), `## Responsibility`, `## Public interface` (the `Reader` port +
      `NewReader`), `## Allowed / forbidden imports` (may import the three sibling PORTS, never
      their `db` packages), `## Data ownership` (none — owns no store), `## Testing` (offline
      fake-port unit tests only, no DB, no `testdb`). This file already exists if T1–T6 workers
      created it inline; if so, this task is a review/completeness pass instead of authoring from
      scratch — do not duplicate content.

## T8. Verification — depends on T1–T7

- [x] T8.1 `go build ./...` and `go vet ./...` pass repo-wide.
- [x] T8.2 `go test ./...` green and fast. `internal/battery` introduces no `DATABASE_URL`/Docker
      dependency — confirm by running `go test ./internal/battery/... -v` with `DATABASE_URL`
      unset and Docker down; every test must still run and pass (none may self-skip, since none
      needs a DB).
- [x] T8.3 Boundary check: `internal/battery` imports only the public ports of `telemetry`,
      `manualcharge`, `account` (no `*db` package from any of the three); `internal/battery/db`
      does not exist; no other module imports `internal/battery` yet (this change ships the
      module with zero consumers, by design — proposal.md "Breaking").
- [x] T8.4 Spec-vs-implementation check: every scenario in `specs/battery/spec.md` is covered by
      a T5 or T6 test with a 1:1 correspondence — known/unknown capacity, usable-vs-nominal SoC
      selection, all three `ok=false` cases, account-scoping on every port call, and the
      no-cross-module-DB-access boundary check (T8.3 covers the last one).
- [x] T8.5 Anti-gaming check: confirm no task's acceptance criteria were weakened, and that
      `deriveEfficiency`'s sign conventions (`deltaSoC` vs. `Efficiency.BatteryDeltaPct`, T3.1's
      note) were NOT unified/simplified away — T5.7 must still pass.
- [x] T8.6 `openspec validate battery-add-efficiency-metric --strict` passes.
