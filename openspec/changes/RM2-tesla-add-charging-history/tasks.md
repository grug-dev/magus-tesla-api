> **No tests for the exploration capability.** Per `CLAUDE.md`, `internal/tesla/raw.go` and
> everything under `cmd/explore-tesla-api/` must keep zero `_test.go` files and no test may call
> the `Raw*` methods. The existing `ChargingHistoryRaw` is already covered by this rule and is
> complete — do not add tests for it. The new typed `ChargingHistory` method IS tested offline
> against a local fake Fleet API (httptest), never the live Tesla API.
>
> **Pending leader confirmation — LEADER FLAG (DES6):** T2 and T3 depend on the pagination
> approach selected. design.md recommends Option A (auto-fetch-all-pages). If the leader
> selects Option B instead, T2's method signature and T3's test scenarios must be updated
> before implementation begins. Mark this flag resolved in tasks.md once the leader confirms.
>
> **Dependencies / parallelism:**
> T1 has no dependencies (types.go only; disjoint from other files).
> T2 depends on T1 (adds ChargingHistoryParams from T1 to the interface and impl; touches
> client.go and vehicles.go which T1 does not touch).
> T3 depends on T2 (tests call the method added in T2; uses ChargingHistoryParams from T1).
> T4 is independent of T1–T3 (disjoint file: ai/tesla-fleet-api-endpoints.md only) — may
> run in parallel with T1–T3 but requires the leader's path grant for that file.
> T5 is independent of T1–T3 (disjoint file: internal/tesla/AGENTS.md only) — may run in
> parallel with T1–T3.
> T6 is leader-integrated (cross-module: internal/gateway/handlers/handlers_test.go) —
> outside the tesla sandbox; depends on T2 so the new method signature is known.
> T7 depends on T1–T6.

## T1 — DTO types (`internal/tesla/types.go`) — no dependencies

- [ ] T1.1 Add the unexported `chargingHistoryResponseTesla` envelope struct with
      `Data []ChargingSessionTesla` (json: `"data"`) and `TotalResults int`
      (json: `"totalResults"`).
- [ ] T1.2 Add `ChargingHistoryTesla` (exported result type) with fields `Data
      []ChargingSessionTesla` and `TotalResults int` (no JSON tags — this is the merged
      caller-facing struct, not an envelope).
- [ ] T1.3 Add `ChargingSessionTesla` with all session fields per design.md §ChargingSessionTesla.
      The three datetime fields (`ChargeStartDateTime`, `ChargeStopDateTime`,
      `UnlatchDateTime`) are `time.Time`; add a custom `UnmarshalJSON` on
      `ChargingSessionTesla` (or a named `rfcTime` helper type) that parses
      `time.RFC3339` to handle Tesla's offset-format timestamps correctly.
- [ ] T1.4 Add `ChargingFeeTesla` with all fee fields per design.md §ChargingFeeTesla.
      `RateTier3`, `RateTier4`, `UsageTier3`, `UsageTier4` are `*float64` (nullable);
      all `Total*` fields are `float64` (non-nullable, per live payload).
- [ ] T1.5 Add `ChargingInvoiceTesla` with fields `FileName`, `ContentID`, `InvoiceType`
      per design.md §ChargingInvoiceTesla.
- [ ] T1.6 Add `ChargingHistoryParams` struct with fields `StartTime string`, `EndTime string`,
      `PageNo int`, `Count int` per design.md §Typed Method Signature.
- [ ] T1.7 Audit: confirm no `Km()` or `Kmh()` companion methods are needed (no miles/mph
      fields in this payload per DES3). Confirm no JSON-tagged km field is added. Document
      this fact in a comment on `ChargingSessionTesla` or `ChargingFeeTesla`.

## T2 — `VehicleService.ChargingHistory` port + `*Client` impl — depends on T1

- [ ] T2.1 Add `ChargingHistory(ctx context.Context, creds Credentials, params
      ChargingHistoryParams) (*ChargingHistoryTesla, error)` to the `VehicleService`
      interface in `internal/tesla/client.go`. Add the doc comment from design.md
      (account-scoped, no wake, `vehicle_charging_cmds` scope required).
- [ ] T2.2 Update the compile-time check in `client.go`:
      `var _ VehicleService = (*Client)(nil)` — this already exists and will catch a
      missing method implementation immediately.
- [ ] T2.3 Implement `(*Client).ChargingHistory` in `internal/tesla/vehicles.go`:
      - Build the URL path `/api/1/dx/charging/history` with query params from
        `ChargingHistoryParams` (append `startTime`, `endTime`, `pageNo`, `count` only
        when non-zero/non-empty).
      - If `params.PageNo == 0` (auto-fetch): start with `pageNo=1`, iterate pages
        (incrementing `pageNo`) until `len(accumulated) >= totalResults` or a page
        returns an empty `data` slice. Merge all `data` slices into one
        `ChargingHistoryTesla`. Use the `Count` param for page size (default = server
        default when zero, i.e., omit the param).
      - If `params.PageNo != 0` (single-page): fetch only that page and return it.
      - Route each fetch through the existing `c.get(ctx, creds, path, &envelope)`;
        a 401 on any page yields `ErrUnauthorized`; a 403 yields `ErrForbidden`.
      - Return `nil, err` on any error. Return `&ChargingHistoryTesla{...}, nil` on success.

## T3 — Offline tests for `ChargingHistory` — depends on T2

> Tests use `httptest.Server` with canned JSON only. No live Tesla call. Use the same
> unexported `baseURL` field pattern established in `internal/tesla/vehicles_test.go`.

- [ ] T3.1 Add test cases in `internal/tesla/vehicles_test.go` (same package) for
      `ChargingHistory` with a canned single-page response matching the live-captured
      payload shape. Assert: `len(Data) == 1`, `TotalResults == 1`, session fields
      (SessionID, VIN, SiteLocationName, CountryCode, BillingType, VehicleMakeType),
      datetime fields parsed to `time.Time` with correct UTC offset, fee count,
      fee fields including `RateTier3 == nil`, invoice count, invoice fields.
- [ ] T3.2 Add a test asserting auto-pagination: serve two pages (page 1 returns 1 session
      with `totalResults: 2`; page 2 returns 1 session with `totalResults: 2`). Assert the
      merged result has `len(Data) == 2` and `TotalResults == 2`. Assert the fake handler
      was called twice (request counter).
- [ ] T3.3 Add a test asserting `PageNo != 0` fetches only one page: serve a handler that
      records `pageNo` from the query string and returns a single-page envelope. Assert
      the handler was called exactly once.
- [ ] T3.4 Add a test asserting 401 → `errors.Is(err, ErrUnauthorized)` and 403 →
      `errors.Is(err, ErrForbidden)`.
- [ ] T3.5 Add a unit test for the `time.RFC3339` timestamp parsing: assert that
      `"2026-06-28T10:24:41-05:00"` unmarshals to the correct `time.Time` value
      (UTC equivalent: `2026-06-28T15:24:41Z`).
- [ ] T3.6 Confirm no `_test.go` exists for `raw.go` scope and no test calls
      `ChargingHistoryRaw` or any other `Raw*` method.

## T4 — Update `ai/tesla-fleet-api-endpoints.md` — independent of T1–T3

> Requires leader path grant for `ai/tesla-fleet-api-endpoints.md` (outside the tesla
> sandbox; explicitly granted in this dispatch).

- [ ] T4.1 In the Charging endpoints table, replace the `/api/1/dx/charging/history` row
      status from `⬜ typed · ChargingHistoryRaw explorer-reachable` to
      `✅ ChargingHistory` (typed method on `VehicleService`) with a note that
      `ChargingHistoryRaw` remains available for exploration.

## T5 — Update `internal/tesla/AGENTS.md` Public interface section — independent of T1–T3

- [ ] T5.1 Add `ChargingHistory(ctx context.Context, creds Credentials, params
      ChargingHistoryParams) (*ChargingHistoryTesla, error)` to the `VehicleService`
      interface block in the `## Public interface (the port)` section of
      `internal/tesla/AGENTS.md`.
- [ ] T5.2 Update the "Off the interface, deliberately" paragraph to note that
      `ChargingHistoryRaw` remains the exploration sibling (raw, off-interface) for
      `ChargingHistory` (typed, on-interface) — clarify this is the reverse-order case
      where the raw sibling pre-existed the typed method.

## T6 — Cross-module compile fix (gateway test fake) — depends on T2; OUTSIDE tesla sandbox, leader-integrated

- [ ] T6.1 Add a stub `ChargingHistory` method to `fakeTesla` in
      `internal/gateway/handlers/handlers_test.go`:
      ```go
      func (f *fakeTesla) ChargingHistory(ctx context.Context, creds tesla.Credentials, params tesla.ChargingHistoryParams) (*tesla.ChargingHistoryTesla, error) {
          return nil, nil
      }
      ```
      This is a compile-only fix; no gateway production code calls `ChargingHistory`.

## T7 — Verification — depends on T1–T6

- [ ] T7.1 `go build ./...` passes with no errors.
- [ ] T7.2 `go vet ./...` passes with no warnings.
- [ ] T7.3 `go test ./...` green and fast: `internal/tesla` typed tests run offline with no
      live Fleet API calls; `internal/tesla/raw.go` and `cmd/explore-tesla-api` report no
      test files; no network request fires from the test suite.
- [ ] T7.4 `openspec validate RM2-tesla-add-charging-history --strict` passes and
      tasks.md checkboxes reflect real completion.
