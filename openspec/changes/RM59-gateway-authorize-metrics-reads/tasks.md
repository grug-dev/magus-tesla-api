# Tasks — RM59-gateway-authorize-metrics-reads

All work is inside `internal/gateway`, plus this change's own `specs/gateway/spec.md`
delta (already written as part of this change's artifacts).

## Dependency graph

```
T1 (history_test.go fake fixup)         ─┐
T2 (external_charges.go — RD6 rewrite)  ─┼─► T6 (final verification)
T3 (external_charges_test.go fixup)     ─┤
T4 (handlers_test.go comment fixup)     ─┘
T5 (gateway spec delta) — already done, independent of T1–T4
```

- **T1, T2, T3, T4 touch four disjoint files** and may all run in parallel, each
  by a separate agent.
- **T3 is not a hard dependency of T2**, but reads more naturally after it: T3's
  only behavioral question (does the RD6 rewrite change what any existing test in
  this file observes?) is already answered "no" in `design.md` D2, verified
  against the existing test suite text. An agent doing T3 does not need T2's
  diff to finish its own edit — the fake's signature change and the dropped
  assertion are independent of `buildExternalChargesPage`'s internals.
- **T5 needs no further work** — the delta spec is complete in this change's
  `specs/gateway/spec.md`. Listed for the record and for T6's own check.
- **T6 depends on T1–T4** (verification needs every file to compile).

## T1 — `history_test.go`: `fakeAnalyticsReader` signature fixup

Depends on: nothing. Touches only `internal/gateway/handlers/history_test.go`.

- [x] 1.1 Add the import
      `"github.com/cristianpena/magus-tesla-api/internal/vehicleref"`.
- [x] 1.2 Remove the three now-unread struct fields `gotAccount`, `gotOdoAccount`,
      `gotBattAccount` (design.md D3 — confirmed unread by any assertion).
- [x] 1.3 `ConsumedByDay`, `OdometerDeltaByDay`, `BatteryLevelByDay`: drop the
      `accountID uuid.UUID` parameter from each signature and the
      `f.gotXxxAccount = accountID` line each currently sets. Every other
      recorded field and the returned fixture values are unchanged — see
      design.md D3's before/after table for the exact signatures.
- [x] 1.4 Rename `LatestMetricsByAccount(context.Context, uuid.UUID)
      ([]analytics.VehicleStatus, error)` to `LatestMetricsForVehicles(context.Context,
      []vehicleref.Ref) ([]analytics.VehicleStatus, error)`. Body unchanged:
      `return f.statuses, f.statusesErr`.
- [x] 1.5 Update the doc comments naming `LatestMetricsByAccount` — the struct's
      field-group comment above `statuses`/`statusesErr`, and the renamed
      method's own comment (design.md D5) — to say `LatestMetricsForVehicles`.
      Do not add a design-doc or change-ID citation to either comment
      (`ai/go-conventions.md`'s code-comment rule) — state the reason itself if
      the surrounding sentence needs one.
- [x] 1.6 Fix the one remaining `LatestMetricsByAccount` mention near line 1885
      (a doc comment on a dashboard/vehicles/nav-header test in this same file).
- [x] 1.7 `go vet ./internal/gateway/handlers/...` still fails at this point
      (T2/T3 not done yet) — expected. Confirm the specific error this file used
      to contribute is gone; do not chase remaining errors from other files here.

## T2 — `external_charges.go`: collapse `buildExternalChargesPage` to one read (RD6)

Depends on: nothing. Touches only
`internal/gateway/handlers/external_charges.go` (production code).

- [x] 2.1 Replace the `vehicles, err := h.acct.RegisteredVehicles(ctx, uid)` /
      `if err != nil { ... }` block near the top of `buildExternalChargesPage`
      with `refs, vehicles, ok := h.ownedVehicles(ctx, uid)` / `if !ok { ... }`,
      returning the same `ExternalChargesPageData{CSRFToken, Error:
      i18n.T(ctx, i18n.KeyChargesErrorCouldNotLoadVehicles)}` on the failure
      path — design.md D2 has the exact before/after code.
- [x] 2.2 In the battery-suggestion block later in the same function, remove
      the second `h.ownedVehicles(ctx, uid)` call
      (`if refs, _, ok := h.ownedVehicles(ctx, uid); teslaIDFilter != 0 &&
      h.analyticsReader != nil && ok`) and reuse the `refs` from 2.1 instead.
      The guard becomes `if teslaIDFilter != 0 && h.analyticsReader != nil`
      (`ok` is already guaranteed true at this point in the function, since 2.1
      already returned otherwise).
- [x] 2.3 Every other use of `vehicles` in this function (the VM-mapping loop
      that calls `externalChargeEntryVMFromEntry(e, vehicles)`) is unchanged —
      `ownedVehicles` returns the same `[]account.Vehicle` shape
      `RegisteredVehicles` did.
- [x] 2.4 `go build ./internal/gateway/...` — expect clean (this is a
      production-code change; it must not break the build on its own).
- [x] 2.5 Do not touch `handlers.go`'s three `LatestMetricsForVehicles` call
      sites — design.md D1 (RD7) keeps them as tier 1 wrote them. This task
      touches `external_charges.go` only.

## T3 — `external_charges_test.go`: `fakeRecalculator` fixup + stale comments

Depends on: nothing (see the dependency-graph note above on why this does not
need to wait on T2). Touches only
`internal/gateway/handlers/external_charges_test.go`.

- [x] 3.1 Remove the `accountID uuid.UUID` field from the `recalculateCall`
      struct.
- [x] 3.2 `Recalculate`: drop the `accountID uuid.UUID` parameter and the
      `accountID: accountID` entry in the `recalculateCall{...}` literal it
      appends — design.md D4 has the exact before/after signature.
- [x] 3.3 In `TestExternalChargeCreate_RecalculatesAfterSuccessfulWrite`,
      delete the `got.accountID != uid` assertion block. Keep the `got.teslaID
      != 1001` assertion and the `start`/`end` window assertion unchanged —
      design.md D4 has the exact before/after for this test body. Do not touch
      `TestExternalChargeCreate_NoRecalculateWhenWriteFails` or
      `TestExternalChargeCreate_RecalculateErrorDoesNotFailTheRequest`; neither
      reads the removed field.
- [x] 3.4 Fix the two remaining `LatestMetricsByAccount` comment mentions near
      lines 1490 and 1539 to say `LatestMetricsForVehicles` (design.md D5).
- [x] 3.5 `go build`/`go vet` on this package still fail until T1 also lands
      (both files are in the same package) — expected; do not chase T1's
      errors from here.

## T4 — `handlers_test.go`: stale comment fixups only

Depends on: nothing. Touches only
`internal/gateway/handlers/handlers_test.go`. No signature in this file
changes — every `fakeAnalyticsReader{statuses: ...}` construction already
compiles against the fake once T1 lands; this task is comments only.

- [x] 4.1 Fix the three `LatestMetricsByAccount` comment mentions near lines
      217, 233, and 1156 to say `LatestMetricsForVehicles` (design.md D5).

## T5 — Gateway delta spec (already complete)

- [x] 5.1 `specs/gateway/spec.md` in this change folder renames all five
      `LatestMetricsByAccount` mentions (across the "Dashboard renders enriched
      vehicle cards from stored telemetry", "Create Charge Entry", "Navigation
      Vehicle Header", and "Gateway Imports No chargingdb Package"
      requirements) to `LatestMetricsForVehicles`, and nothing else. Already
      written; no further edit needed before archive-time sync.

## T6 — Final verification

Depends on: T1–T4.

- [x] 6.1 `go build ./...` — expect clean (already clean before this change;
      confirms no regression).
- [x] 6.2 `go vet ./...` — expect clean, including `internal/gateway` for the
      first time since tier 1 landed.
- [x] 6.3 `gofmt -l` over every file this change touched — expect no output.
- [x] 6.4 `make vehicleref-guard` — expect clean; confirms T2 introduced no new
      `vehicleref.All`/`vehicleref.Authorize` call site outside the existing
      allow-list (design.md's Verification section).
- [x] 6.5 `make boundary-guard`, `make i18n-guard`, `make tz-guard`,
      `make migration-guard`, `make archive-guard` — expect clean; none of this
      change's edits touch what any of these five check.
- [x] 6.6 Grep the whole repo for `LatestMetricsByAccount` and confirm the only
      remaining hits are the three named in design.md's "Docs" section
      (`openspec/specs/analytics/spec.md`'s historical scenario, the roadmap
      file's own narrative, and the KB's `pending-spec-to-sync/applied/`
      snapshots) plus anything under `openspec/changes/archive/` — never a live
      guide, an `AGENTS.md`, or a non-archived `.go` file.
- [ ] 6.7 Suite command for the owner to run (this worker does not run it):
      `go test ./internal/gateway/...`, or `make test` / `make test-with-db`
      for the full suite now that both RM59 tiers are done.
