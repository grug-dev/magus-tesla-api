> **No tests for the exploration capability.** Per `CLAUDE.md`, `internal/tesla/raw.go` and
> everything under `cmd/explore-tesla-api/` must keep zero `_test.go` files and no test may call
> the `Raw*` methods — those calls are paid and wake the car. Typed methods (this change) ARE
> tested, offline, against a local fake Fleet API.
>
> **Dependencies / parallelism:** 1 has no dependencies. 2 depends on 1 (both touch
> `internal/tesla/types.go`). 3 depends on 2 (touches `client.go`/`vehicles.go` after the
> signature change). 4 is independent of 1–3 (disjoint files: `cmd/explore-tesla-api/` only) and
> may run in parallel with them. 5 depends on 2 (gateway fake must match the new signature;
> outside the tesla sandbox — leader-integrated). 6 depends on all of 1–5.

## 1. DTO enrichment (`internal/tesla/types.go`) — no dependencies

- [x] 1.1 Add `ChargeLimitSoc int` with JSON tag `charge_limit_soc` to `ChargeStateTesla`.
- [x] 1.2 Add `SentryMode bool` with JSON tag `sentry_mode` to `VehicleStateTesla`.
- [x] 1.3 Audit the enriched DTO tree: every miles/mph field has a `Km()`/`Kmh()` companion using
      the `milesToKm` constant (nil-safe for pointer fields); confirm the two new fields need
      none (percent and boolean) and that no km value exists as a JSON-tagged field.

## 2. VehicleData returns raw + typed from one fetch — depends on 1

- [x] 2.1 Change the `VehicleService` interface method in `internal/tesla/client.go` to
      `VehicleData(ctx context.Context, creds Credentials, vehicleID int64) (*VehicleDataTesla, json.RawMessage, error)`.
- [x] 2.2 Change `dataResponseTesla.Response` in `internal/tesla/types.go` to `json.RawMessage`
      so the envelope decode captures the inner `response` object's raw bytes.
- [x] 2.3 Reimplement `(*Client).VehicleData` in `internal/tesla/vehicles.go`: one `c.get` call,
      then unmarshal `VehicleDataTesla` from the captured raw bytes; return the DTO, the raw
      inner `response` payload, and any error. Keep the wake-first doc comment accurate.
- [x] 2.4 Confirm `WakeUp` needs no change: it issues `wake_up` once and returns the resulting
      `*VehicleTesla` (with `State`); no wait/retry loop exists in the adapter.
- [x] 2.5 Confirm a 401 on the changed method still surfaces `ErrUnauthorized` (it routes
      through `do`).

## 3. Offline tests for the typed methods — depends on 2

- [x] 3.1 Add an unexported `baseURL string` field to `Client` in `internal/tesla/client.go`,
      defaulted to the production const in `NewClient()`; `do` uses the field. Public API
      unchanged (`go vet` clean, no signature changes).
- [x] 3.2 Add `internal/tesla/vehicles_test.go` (same package) using `httptest.Server` with a
      canned `vehicle_data` payload: assert the returned DTO fields (including `ChargeLimitSoc`
      and `SentryMode`), assert the raw return equals the fake's inner `response` bytes, and
      assert DTO values re-parse from the raw payload (losslessness).
- [x] 3.3 Add a test asserting exactly one HTTP request is made per `VehicleData` call (counter
      in the fake handler).
- [x] 3.4 Add tests for `WakeUp` (returns reported state from a canned wake response) and for
      the 401 → `ErrUnauthorized` signal via `errors.Is`.
- [x] 3.5 Add unit tests for the metric companions on canned values: `BatteryRangeKm`,
      `ChargeRateKmh`, `OdometerKm`, and nil-safe `SpeedKmh` (nil in → nil out).
- [x] 3.6 Confirm no `_test.go` exists for `internal/tesla/raw.go` scope and no test calls any
      `Raw*` method or `cmd/explore-tesla-api`.

## 4. Exploration sync audit + README (`cmd/explore-tesla-api/`) — independent of 1–3, parallel-ok (disjoint files; needs leader path grant outside `internal/tesla/`)

- [x] 4.1 Verify the sync rule holds one-to-one: every typed `VehicleService` method
      (`ListVehicles`, `VehicleData`, `WakeUp`) has its `Raw*` sibling in `internal/tesla/raw.go`
      (reusing `get`/`post`, returning `json.RawMessage`, OFF the interface) — expected outcome:
      all three already exist; add none, remove none.
- [x] 4.2 Verify `cmd/explore-tesla-api/main.go` surfaces all three raw endpoints (inventory,
      wake+poll, vehicle_data) — expected outcome: already covered; change only if drift found.
- [x] 4.3 Update `cmd/explore-tesla-api/README.md`: note that the typed `VehicleData` now also
      returns the raw payload for callers, while the explorer remains the tool for full-envelope
      inspection; fix any drift found in 4.1/4.2. Keep the cost/wake warning and the test-free
      rule intact.

## 5. Cross-module compile fix (gateway test fake) — depends on 2; OUTSIDE the tesla sandbox, leader-integrated

- [x] 5.1 Update `fakeTesla.VehicleData` in `internal/gateway/handlers/handlers_test.go` to the
      new three-value signature (mechanical; behavior unchanged, gateway production code still
      never calls it).

## 6. Verification — depends on 1–5

- [x] 6.1 `go build ./...` and `go vet ./...` pass.
- [x] 6.2 `go test ./...` green and fast; `internal/tesla` typed tests run offline;
      `cmd/explore-tesla-api` and `internal/tesla/raw.go` scope still report no test files; no
      live Tesla call fires from the test run.
- [x] 6.3 `openspec validate tesla-add-vehicle-data-and-wake --strict` passes and tasks.md
      checkboxes reflect real completion.
