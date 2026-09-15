# RM59-gateway-authorize-metrics-reads

> Source: MAG-69 — https://linear.app/magus-monitor/issue/MAG-69/6-re-key-analyticsvehicle-metrics-and-vehicle-metric-watermarks-on
> Roadmap: `openspec/roadmaps/RM59-rekey-vehicle-metrics-on-tesla-id.md`, tier 2 of 2.
> Parent: MAG-63, step 6 of 7.

Unit tests: **excluded** — the roadmap header says so. Existing tests are fixed where a
signature changed. No new test file.

## Why

Tier 1 (`RM59-analytics-rekey-vehicle-metrics-on-tesla-id`, archived) re-keyed
`analytics.vehicle_metrics` on `tesla_id` alone and renamed the read port from
`LatestMetricsByAccount(ctx, accountID)` to
`LatestMetricsForVehicles(ctx, refs []vehicleref.Ref)`. It also touched every
gateway call site it needed to keep `go build ./...` green, as a stopgap
(design.md D12 of that change).

`go build ./...` passes today. `go vet ./...` does not: it fails inside
`internal/gateway/handlers`, only on `_test.go` files. Three fakes still
implement the old, five-argument-shorter signatures
(`fakeAnalyticsReader.LatestMetricsByAccount`, and `ConsumedByDay` /
`OdometerDeltaByDay` / `BatteryLevelByDay` all still take an `accountID`;
`fakeRecalculator.Recalculate` still takes one too), so they no longer satisfy
`analytics.Reader` / `analytics.Recalculator` and the package does not compile
its tests.

Tier 1's own review also found one live problem it could not fix inside its
sandbox: `buildExternalChargesPage` (`external_charges.go`) reads the
account's registered vehicles twice in one page render — once directly via
`h.acct.RegisteredVehicles`, once through `h.ownedVehicles`, which calls the
same method again. `make vehicleref-guard` forbids `external_charges.go` from
building a `[]vehicleref.Ref` any other way than through `ownedVehicles`, so
tier 1 could not just reuse the first read's slice — that is this tier's job
(tracked as F2 in the roadmap).

## What changes

- **Test fixups** (`internal/gateway/handlers`, no new test file): `fakeAnalyticsReader`
  (`history_test.go`) drops `accountID` from `ConsumedByDay`, `OdometerDeltaByDay`,
  `BatteryLevelByDay`, and renames `LatestMetricsByAccount` to
  `LatestMetricsForVehicles(ctx, refs []vehicleref.Ref)`. `fakeRecalculator`
  (`external_charges_test.go`) drops `accountID` from `Recalculate`. One assertion
  that compared the dropped `accountID` argument is removed — the sibling `teslaID`
  assertion already covers "scoped to the right vehicle."
- **One duplicate read removed** (`external_charges.go`, production code):
  `buildExternalChargesPage` calls `h.ownedVehicles(ctx, uid)` once, at the top,
  in place of its own `h.acct.RegisteredVehicles` call. The suggestion block later
  in the function reuses that same `refs` value instead of calling `ownedVehicles`
  a second time.
- **Stale comments fixed**: every comment across `handlers_test.go`, `history_test.go`,
  and `external_charges_test.go` naming the retired `LatestMetricsByAccount` is
  updated to `LatestMetricsForVehicles`.
- **Gateway delta spec**: `openspec/specs/gateway/spec.md` names
  `LatestMetricsByAccount` in five places, across four requirements. This change
  renames all five to `LatestMetricsForVehicles` — a delta sync tier 1 could not do
  itself, because a main spec only ever changes through a delta sync, and the
  gateway capability belongs to this tier.

**Explicitly out of scope (RD7, settled with the owner before this change was
written):** the three `LatestMetricsForVehicles` call sites in `handlers.go`
(`vehiclesFor`, `dashboardFor`, `navHeaderFor`) stay exactly as tier 1 left them —
calling `vehicleref.All(teslaIDsOf(registered))` on a `registered` slice each
handler already read for another reason. The roadmap's own tier-2 text suggested
routing these through `ownedVehicles` too; this change does not, because each
call site already holds its own vehicle list, and `ownedVehicles` would add a
second read where tier 1 added none. See `design.md` D1.

## Breaking?

Internal only — no external API. No production port signature changes. One
production function's read pattern changes (`buildExternalChargesPage`): one
account read instead of two, with one observable side effect — an account with
zero registered vehicles now renders the "could not load vehicles" error state
from this function instead of continuing past a `vehicles == nil` slice. This
path is unreachable in practice: `teslaIDFilter` is only non-zero when a vehicle
was already resolved from the same account, and the function already returns
its empty state before reaching this point whenever `teslaIDFilter == 0`. See
`design.md` D2.

## Modules affected

- `internal/gateway` — this worker's sandbox. Production edit
  (`external_charges.go`) and test edits (`handlers_test.go`, `history_test.go`,
  `external_charges_test.go`).
- `openspec/specs/gateway` — the delta spec this change carries; synced into the
  main spec at archive time.

## Read paths affected

- The external-charges page (`GET /external-charges`, `GET /ui/external-charges`)
  — a read-heavy hot path per this project's Performance-Profile. This change
  makes it **cheaper**: one `account.RegisteredVehicles` read per render instead
  of two. No index or query changes; this is a Go-level read-count fix only.
- The dashboard, vehicle cards, and nav header read paths (`handlers.go`'s three
  `LatestMetricsForVehicles` call sites) are unaffected — RD7 leaves them as
  tier 1 already wrote them.

## Non-goals

- No change to any `analytics` port signature — tier 1 already finished that.
- No change to the three `handlers.go` call sites (RD7).
- No new design decision about how the gateway proves vehicle ownership —
  `ownedVehicles`/`authorizeVehicle` already exist and are reused, not redesigned.
- No database object touched — no migration, no index, no query.
- `unit tests: excluded` — no new test file, no new test case testing new
  behavior. Existing tests are fixed to compile and to keep asserting what they
  already asserted, using the new shapes.
