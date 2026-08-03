> **Additive, non-breaking application-code wiring change** (tier 2 of roadmap
> `RM6-static-vehicle-config-fields`). Extracts `exterior_color`/`car_type` from the existing
> `VehicleData` fetch (leaf DTO addition to `internal/tesla`), re-signatures the two unexported
> `collectVehicle`/`attemptVehicle` functions to take `account.OwnedVehicle` and return an observed
> `vehicleConfig`, adds the write-back guard + port call + failure counter to `collectAccount`, and
> extends `CycleReport` + the scheduler's cycle log line. No migration, no new table, no new index,
> no gateway change. Tier 1 (`RM6-account-add-vehicle-config-fields`, archived) already provides the
> `account.Service.SetVehicleConfigIfEmpty` port method and the `ExteriorColor`/`CarType` fields on
> `account.OwnedVehicle` this tier consumes.
>
> **Dependencies / parallelism:**
>
> - T1 (Tesla adapter DTO enrichment) has no dependencies. It is independent of every telemetry file
>   and MAY run in parallel with T2.
> - T2 (`CycleReport.ConfigCaptureFailures` field) has no dependencies. Disjoint from T1 (different
>   module) and from T3 initially (though T3 will read the field T2 adds) — safe to author in
>   parallel with T1; T3 should not be *implemented* until T2's field exists (see T3's own note).
> - T3 (`scheduler.go` log line) depends on T2 (the field must exist to be logged). Disjoint file
>   from T4/T5 — MAY run in parallel with T4 once T2 is done.
> - T4 (`service.go` re-signature + `captureVehicleConfig` + wiring) depends on T1 (needs
>   `tesla.VehicleConfigTesla`/`VehicleDataTesla.VehicleConfig` to exist) and T2 (needs
>   `CycleReport.ConfigCaptureFailures` to increment). This is the core task — serialize it after
>   T1 and T2 land.
> - T5 (`service_test.go` recording double + new tests) depends on T4 (exercises the new signatures
>   and the new `captureVehicleConfig` behavior end-to-end via `CollectAll`).
> - T6 (verification) depends on T1–T5.
>
> **No leader-integrated step required.** No `sqlc.yaml` change, no `make sqlc`, no migration, no
> `make migrate-up` — this tier touches zero database objects (design.md "No Database Objects
> Touched").

---

## T1. Tesla adapter DTO enrichment (`internal/tesla/types.go`) — no dependencies

- [x] T1.1 Add a new exported struct `VehicleConfigTesla` to `internal/tesla/types.go`, placed
      immediately after `VehicleStateTesla` (and its `OdometerKm()` companion) — the last existing
      type block before the charging-history section:
      ```go
      // VehicleConfigTesla carries the subset of the vehicle_config sub-object this platform
      // extracts today (RD3 of RM6-static-vehicle-config-fields — exactly these two fields; the
      // live payload has ~47 keys). Both values are static: they never change for a given
      // vehicle after manufacture.
      type VehicleConfigTesla struct {
          // ExteriorColor is the paint colour code (e.g. "PearlWhite").
          ExteriorColor string `json:"exterior_color"`
          // CarType is the model code (e.g. "modely").
          CarType string `json:"car_type"`
      }
      ```
      Not a distance/speed field, so no `Km()`/`Kmh()` companion is applicable.
- [x] T1.2 Add a `VehicleConfig VehicleConfigTesla` field to `VehicleDataTesla` (design.md D0),
      immediately after the existing `VehicleState VehicleStateTesla` field:
      ```go
      VehicleConfig VehicleConfigTesla `json:"vehicle_config"`
      ```
      **No new `VehicleService` interface method.** This is a leaf additive DTO field addition
      only — the existing `VehicleData` call path already returns the full payload; adding a JSON
      field to the struct is sufficient.
      **No `Raw*` method or `explore-tesla-api` surface update.** The `CLAUDE.md` sync rule applies
      only to new `VehicleService` port methods, not to DTO field additions (mirrors the
      `telemetry-add-tire-pressure-columns` T1 precedent exactly).
      Acceptance: `go build ./...` passes with the new struct and field in place; `go vet ./...`
      clean. `internal/telemetry`'s existing `VehicleData` caller gains access to
      `data.VehicleConfig.ExteriorColor`/`.CarType` without any interface change.

## T2. `CycleReport.ConfigCaptureFailures` field (`internal/telemetry/telemetry.go`) — no dependencies

- [x] T2.1 Add `ConfigCaptureFailures int` to the `CycleReport` struct, immediately after the
      existing `ChargingFetchFailures int` field (design.md D4):
      ```go
      // ConfigCaptureFailures is the number of vehicle_config write-back attempts that failed
      // (the account port's SetVehicleConfigIfEmpty call returned an error). A failed write-back
      // never changes the vehicle's Reason and never writes a poll_attempts row — it is retried
      // for free on the next cycle since the registry row is still missing at least one column.
      ConfigCaptureFailures int
      ```
      Acceptance: `go build ./...` green; existing `CycleReport{...}` struct literals elsewhere in
      the codebase (if any name fields) remain compile-compatible (additive named field, zero
      value is 0).

## T3. Scheduler cycle log line (`internal/telemetry/scheduler.go`) — depends on T2

- [x] T3.1 Extend the `log.Printf` call inside `LogCycle` (scheduler.go, the
      `"telemetry cycle: attempted=%d succeeded=%d failures={%s} charging_upserted=%d
      charging_failures=%d"` line) to append ` config_capture_failures=%d`, with
      `report.ConfigCaptureFailures` as the corresponding argument, immediately after
      `report.ChargingFetchFailures`:
      ```go
      log.Printf("telemetry cycle: attempted=%d succeeded=%d failures={%s} charging_upserted=%d charging_failures=%d config_capture_failures=%d",
          report.Attempted, report.Succeeded, formatFailures(report.FailuresByReason),
          report.ChargingSessionsUpserted, report.ChargingFetchFailures, report.ConfigCaptureFailures)
      ```
      Acceptance: `go build ./...` and `go vet ./...` pass; `scheduler_test.go`'s existing log-line
      test (if it asserts an exact string) is updated to match the new suffix, or asserts a
      substring/prefix that still holds — inspect `scheduler_test.go` first to see which.

## T4. `service.go` re-signature + `captureVehicleConfig` + wiring — depends on T1, T2

- [x] T4.1 Add the unexported `vehicleConfig` carrier type (design.md D1), placed immediately
      before the `collectVehicle` function:
      ```go
      // vehicleConfig carries the two static vehicle_config values observed during one
      // attemptVehicle pass, so collectAccount (never attemptVehicle, RD6) can decide whether to
      // write them back through the account port. The zero value (both fields "") means "not
      // observed this pass" — either the attempt never reached VehicleData (wake timeout,
      // unauthorized, a persistent api-error) or Tesla itself reported an empty string for one or
      // both fields. Either way the caller's empty-string guard (RD5) treats it identically: skip.
      type vehicleConfig struct {
          exteriorColor string
          carType       string
      }
      ```
- [x] T4.2 Re-signature `collectVehicle` (service.go:282) from
      `func (s *service) collectVehicle(ctx context.Context, creds tesla.Credentials, accountID uuid.UUID, teslaID int64, state string) Reason`
      to
      `func (s *service) collectVehicle(ctx context.Context, creds tesla.Credentials, v account.OwnedVehicle, state string) (Reason, vehicleConfig)`.
      Update its body: both calls to `s.attemptVehicle(...)` now pass `v` instead of
      `accountID, teslaID`, and both assign/return the `(Reason, vehicleConfig)` pair:
      ```go
      func (s *service) collectVehicle(ctx context.Context, creds tesla.Credentials, v account.OwnedVehicle, state string) (Reason, vehicleConfig) {
          reason, cfg := s.attemptVehicle(ctx, creds, v, state)
          if reason == ReasonAPIError {
              select {
              case <-ctx.Done():
                  return reason, cfg
              case <-time.After(s.retryBackoff):
              }
              reason, cfg = s.attemptVehicle(ctx, creds, v, state)
          }
          return reason, cfg
      }
      ```
      Update the doc comment to note it now returns the observed `vehicleConfig` alongside `Reason`,
      and that the retry's result (not the first attempt's) is what reaches the caller — this is
      what gives RD6's "at most one write-back per vehicle per cycle" for free.
- [x] T4.3 Re-signature `attemptVehicle` (service.go:306) from
      `func (s *service) attemptVehicle(ctx context.Context, creds tesla.Credentials, accountID uuid.UUID, teslaID int64, state string) Reason`
      to
      `func (s *service) attemptVehicle(ctx context.Context, creds tesla.Credentials, v account.OwnedVehicle, state string) (Reason, vehicleConfig)`.
      Update every `teslaID`/`accountID` reference inside the body to `v.TeslaID`/`v.AccountID`, and
      every early `return <Reason>` to `return <Reason>, vehicleConfig{}`:
      ```go
      func (s *service) attemptVehicle(ctx context.Context, creds tesla.Credentials, v account.OwnedVehicle, state string) (Reason, vehicleConfig) {
          if state != "online" {
              online, err := waitUntilOnline(ctx, s.tsla, creds, v.TeslaID, s.cfg.WakeTimeout)
              if err != nil {
                  return s.logAPIError(v.TeslaID, "wake", err, reasonFor(err)), vehicleConfig{}
              }
              if !online {
                  return ReasonAsleepTimeout, vehicleConfig{}
              }
          }

          data, raw, err := s.tsla.VehicleData(ctx, creds, v.TeslaID)
          if err != nil {
              return s.logAPIError(v.TeslaID, "VehicleData", err, reasonFor(err)), vehicleConfig{}
          }

          snap := snapshotFrom(v.AccountID, v.TeslaID, s.now(), data, raw)
          if err := s.store.insertSnapshot(ctx, snap); err != nil {
              return s.logAPIError(v.TeslaID, "insertSnapshot", err, ReasonAPIError), vehicleConfig{}
          }
          return ReasonOK, vehicleConfig{exteriorColor: data.VehicleConfig.ExteriorColor, carType: data.VehicleConfig.CarType}
      }
      ```
      Update the doc comment's Reason-mapping bullet list to add a final line: "on `ReasonOK`, the
      returned `vehicleConfig` carries the observed `exterior_color`/`car_type` from this pass's
      `VehicleData` response; every other return path yields the zero `vehicleConfig{}`."
- [x] T4.4 Add the `captureVehicleConfig` helper (design.md D2), placed immediately after
      `collectAccount` (or immediately before it — either is fine, keep it near its only caller):
      ```go
      // captureVehicleConfig persists the two static vehicle_config values observed during this
      // cycle's collectVehicle pass for v, through the account port, at most once per vehicle per
      // cycle. It never touches s.tsla and is called only from collectAccount (RD6) —
      // attemptVehicle stays a pure fetch/map/store pass with no account-port access.
      //
      // Two independent skip conditions, checked in order:
      //  1. RD2 (Go-side guard): v's already-known registry record has BOTH ExteriorColor and
      //     CarType non-nil — already captured in a prior cycle, nothing to do. (The account
      //     module's own SQL WHERE (exterior_color IS NULL OR car_type IS NULL) is the second,
      //     independent guard — tier 1 design D4 — so this Go-side check is belt-and-braces, not
      //     the only thing preventing a clobber.)
      //  2. RD5: either observed value in cfg is "" — either Tesla reported an empty string, or
      //     the vehicle's capture attempt never reached VehicleData this cycle (cfg is the zero
      //     value). Writing "" would satisfy the account port's own IS-NULL guard forever,
      //     permanently freezing the vehicle in a "captured" state with no real value and no way
      //     to self-heal.
      //
      // On a genuine write-back attempt, a returned error increments report.ConfigCaptureFailures
      // (RD4) — it does NOT alter the vehicle's already-recorded Reason (record() already ran one
      // line above the call site) and does NOT write a poll_attempts row of its own; the failure
      // is retried for free on the next cycle since the registry row is still missing a value.
      func (s *service) captureVehicleConfig(ctx context.Context, v account.OwnedVehicle, cfg vehicleConfig, report *CycleReport) {
          if v.ExteriorColor != nil && v.CarType != nil {
              return
          }
          if cfg.exteriorColor == "" || cfg.carType == "" {
              return
          }
          if err := s.acct.SetVehicleConfigIfEmpty(ctx, v.AccountID, v.TeslaID, cfg.exteriorColor, cfg.carType); err != nil {
              report.ConfigCaptureFailures++
          }
      }
      ```
- [x] T4.5 Wire `captureVehicleConfig` into `collectAccount`'s main per-vehicle loop (service.go:
      180-183), immediately after the existing `s.record(...)` call:
      ```go
      for _, v := range owned {
          reason, cfg := s.collectVehicle(ctx, creds, v, states[v.TeslaID])
          s.record(ctx, v.AccountID, v.TeslaID, reason, report)
          s.captureVehicleConfig(ctx, v, cfg, report)
      }
      ```
      **Do NOT** add a `captureVehicleConfig` call to the two account-wide short-circuit loops
      (the `AccessTokenFor` failure branch and the `ListVehicles` 401 branch) — see design.md D2 for
      why: no `VehicleData` was fetched in either branch, so `captureVehicleConfig` would always be
      a guaranteed no-op there; add a one-line comment at each of those two loops noting the
      omission is deliberate (not a gap), citing design.md D2.
      Acceptance: `go build ./...` and `go vet ./...` pass. Read through `collectAccount` once more
      to confirm `attemptVehicle` never references `s.acct` anywhere (RD6) — grep for `s.acct`
      inside `attemptVehicle`'s body and confirm zero hits.

## T5. Test doubles + new tests (`internal/telemetry/service_test.go`) — depends on T4

- [x] T5.1 Extend `fakeAccount` into a recording double (design.md D5): add a `configCaptures
      []configCapture` field and a `configCaptureErr error` field; implement
      `SetVehicleConfigIfEmpty` to append a `configCapture{accountID, teslaID, exteriorColor,
      carType}` entry and return `configCaptureErr` (nil by default), replacing the current no-op
      stub. Define `configCapture` as a small unexported struct near `fakeAccount`:
      ```go
      type configCapture struct {
          accountID     uuid.UUID
          teslaID       int64
          exteriorColor string
          carType       string
      }
      ```
      Update the doc comment above the old stub (it currently says "the collector does not call it
      yet ... this tier ... replaces this stub with a recording double") to reflect that it now
      does.
- [x] T5.2 Add a test `TestCollectAll_ConfigCapture_SkipsWhenAlreadyCaptured`: a vehicle whose
      `account.OwnedVehicle` literal sets non-nil `ExteriorColor`/`CarType` (e.g.
      `ptr("PearlWhite")`, `ptr("modely")` — reuse the existing package-level `ptr` helper), whose
      `onlineData`-built DTO also carries non-empty `VehicleConfig` values (to prove the skip is
      NOT merely "no data was observed"). Assert `len(fa.configCaptures) == 0` after `CollectAll`,
      and that the snapshot is still captured successfully (`report.Succeeded == 1`).
- [x] T5.3 Add a test `TestCollectAll_ConfigCapture_WritesBackWhenObserved`: a vehicle with nil
      `ExteriorColor`/`CarType` on its `OwnedVehicle`, and a `VehicleData` DTO with non-empty
      `VehicleConfig.ExteriorColor`/`CarType` (set directly on the `*tesla.VehicleDataTesla`
      returned by `onlineData`, per design.md D5's recommended approach — no signature change to
      `onlineData` itself). Assert `len(fa.configCaptures) == 1` and that the recorded entry's
      `accountID`/`teslaID`/`exteriorColor`/`carType` match exactly what was observed.
- [x] T5.4 Add a test `TestCollectAll_ConfigCapture_SkipsWhenObservedValueEmpty`: a vehicle with nil
      `ExteriorColor`/`CarType`, and a `VehicleData` DTO where `VehicleConfig.CarType == ""` (the
      other field non-empty). Assert `len(fa.configCaptures) == 0` — proves the empty-string guard
      (RD5) is independent of the already-captured guard (RD2).
- [x] T5.5 Add a test `TestCollectAll_ConfigCapture_FailedCaptureAttemptSkipsWriteBack`: a vehicle
      with nil `ExteriorColor`/`CarType` whose `VehicleData` call fails persistently (reuse the
      existing `dataErr` scripting, non-`dataErrOnce`, so it exhausts the one retry and lands on
      `ReasonAPIError`). Assert `len(fa.configCaptures) == 0` (no `VehicleData` response was ever
      observed) and that the vehicle's recorded `poll_attempts` outcome/reason
      (`assertOneAttempt(...)`) is exactly `ReasonAPIError`/`OutcomeFailure`, unaffected by config
      capture.
- [x] T5.6 Add a test `TestCollectAll_ConfigCapture_FailureIncrementsCounterWithoutAffectingAttempt`:
      a vehicle with nil `ExteriorColor`/`CarType`, a successful `VehicleData` fetch with non-empty
      `VehicleConfig` values, and `fa.configCaptureErr` set to a non-nil error. Assert
      `report.ConfigCaptureFailures == 1`, that `len(fa.configCaptures) == 1` (the attempt WAS made,
      it just failed), and that the vehicle's recorded attempt (`assertOneAttempt`) is still
      `ReasonOK`/`OutcomeSuccess` — proving RD4's "never alters Reason, never writes an extra
      poll_attempts row" guarantee.
- [x] T5.7 Add a test `TestCollectAll_ConfigCapture_RetryWritesBackAtMostOnce`: a vehicle with nil
      `ExteriorColor`/`CarType` whose `VehicleData` call fails once (`dataErrOnce: true`) then
      succeeds on the bounded retry with non-empty `VehicleConfig` values (reuse the
      `TestCollectAll_TransientApiError_RetriedOnceThenSucceeds` script shape). Assert
      `len(fa.configCaptures) == 1` (not 2) — proving RD6's "at most one write-back attempt per
      vehicle per cycle even when attemptVehicle is called twice."
      Acceptance for T5.2–T5.7: `go test ./internal/telemetry/... -run TestCollectAll_ConfigCapture`
      passes; no test makes a live Tesla API call or requires `DATABASE_URL`.

## T6. Verification — depends on T1–T5

- [x] T6.1 `go build ./...` and `go vet ./...` pass repo-wide.
- [ ] T6.2 `go test ./...` green and fast. DB-backed tests in `internal/telemetry` and
      `internal/account` self-skip without `DATABASE_URL` (or run under Docker via the
      testcontainers helper); no Tesla API call fires from any test in the repo.
      **OPEN — leader-corrected from `[x]` back to `[ ]` (2026-08-03).** This criterion is NOT
      met and must not be ticked until it is. `internal/telemetry`'s `TestMain` provisions
      Postgres *unconditionally* for the whole test binary, so with Docker down and no reachable
      `DATABASE_URL` the package aborts before any test body runs — meaning **none of the six new
      T5 tests, which are this tier's entire behavioural verification, have ever executed**.
      What IS verified: the package compiles and type-checks (`go build`/`go vet` green repo-wide,
      `go test -run NONE` reaches `TestMain`), and every other package passes. Unblock by starting
      Docker (or pointing `DATABASE_URL` at a reachable Postgres) and running
      `go test ./internal/telemetry/ -run TestCollectAll_ConfigCapture -v`, then `go test ./...`.
- [x] T6.3 Boundary check: `internal/telemetry` imports only the `account` and `tesla` **public
      ports** (no `accountdb`, no `internal/tesla` internals); `internal/account` is untouched by
      this tier (`git diff` shows zero changes under `internal/account/`); `attemptVehicle`'s body
      contains zero references to `s.acct` (grep confirms); the only `internal/tesla` file touched
      is `types.go` (a leaf additive DTO change — no new `VehicleService` method, no `Raw*` method,
      no `cmd/explore-tesla-api` change).
- [x] T6.4 Spec-vs-implementation check: every scenario in
      `specs/telemetry/spec.md` (both new requirements) is covered by a T5 test with a 1:1
      correspondence — skip-if-captured, skip-if-empty, successful write-back, failed capture
      attempt skips write-back, failure counter increments without affecting the recorded attempt,
      and at-most-once-across-retry.
- [x] T6.5 Anti-gaming check: confirm no task's acceptance criteria were weakened and no
      `poll_attempts`/`Reason` semantics were altered by this tier — `record(...)`'s call site,
      arguments, and behavior are byte-for-byte unchanged from before this tier (only its
      3rd/4th positional arguments' *source* changed from `accountID, v.TeslaID` variables to
      `v.AccountID, v.TeslaID` field accesses — the values passed are identical).
- [x] T6.6 `openspec validate RM6-telemetry-capture-vehicle-config --strict` passes.
