## Why

The dashboard today lists each registered vehicle with only its display name and VIN —
fetched from the `account` registry. The nightly telemetry batch (tiers 1-3) has been
storing snapshots in `vehicle_snapshots` since tier 3, and tier 4 opened a `Reader` port
(`telemetry.Reader.LatestSnapshotsByAccount`) so the gateway can consume them without
violating the module boundary. The only remaining gap is the gateway itself: it does not
yet call the `Reader`, does not carry snapshot fields on its vehicle view model, and does
not render any telemetry data on the dashboard. This change closes that gap.

A signed-in user opening their dashboard will now see an enriched vehicle card for every
registered vehicle: battery %, range (km), charge state, odometer (km), inside/outside
temp (°C), locked, sentry mode, and last-updated timestamp — all sourced from the
pre-stored nightly snapshot, with no live Tesla call. A vehicle awaiting its first
nightly snapshot shows a placeholder card. A snapshot older than 36 h is flagged as
stale. If the `Reader` call fails, the registry vehicles still render (graceful
degradation — telemetry is non-critical to identity).

**NOT breaking.** No existing route, DTO, template, or interface is removed or renamed.
The change extends the `fragments.Vehicle` view model, enriches `mapVehicles`, adds
`telemetry.Reader` to `gateway.Deps` and `handlers.Deps`, and updates the
`VehiclesList` template. The `/dashboard` and `/ui/vehicles` routes are unchanged.

## What Changes

Affected modules: **`gateway`** (primary). Thin leader-integrated wiring in **`cmd/web`**
(construct `telemetry.NewReader(pool)` and pass it into `gateway.Deps`).

Read path affected: `GET /dashboard` and `GET /ui/vehicles` — both call `vehiclesFor`,
which will now call both `account.RegisteredVehicles(ctx, uid)` AND
`telemetry.Reader.LatestSnapshotsByAccount(ctx, uid)`, merge by `TeslaID`, and render
enriched vehicle cards.

Performance: 2 indexed queries per render (account registry + telemetry DISTINCT ON
batch). No N+1. Matches the read-heavy profile.

> Grill requirement is satisfied by the roadmap Decisions section
> (`openspec/roadmaps/nightly-vehicle-telemetry.md`) and the leader's tier-5 Step-2 user
> interview (unit/field-set/staleness choices recorded as D1–D7 in design.md). The
> performance/design grill did NOT trigger: no DB object, single obvious read design.

## Capabilities

### Modified Capabilities

- `gateway`: Extends the dashboard fragment to enrich each registered vehicle card with
  its latest stored telemetry snapshot. Adds `telemetry.Reader` as a dependency. All
  formatting and km/stale derivation handled in the handler (Go), not in templates.

## Impact

- **New / modified (within `internal/gateway/` sandbox)**
  - `internal/gateway/gateway.go` — add `telemetry.Reader` field to `Deps`
  - `internal/gateway/handlers/handlers.go` — add `telemetry.Reader` to `Deps` /
    `Handler` / `New()`; extend `vehiclesFor` to call the `Reader`; add
    `mergeSnapshots` helper; extend `mapVehicles` to accept and merge snapshots; extend
    `fragments.Vehicle` fields on the view model
  - `internal/gateway/templates/fragments/vehicles.templ` — extend `Vehicle` view struct
    (add snapshot fields + `HasSnapshot bool` + `IsStale bool`); update `VehiclesList`
    component to render extended card and placeholder/stale states; run `templ generate`
  - `internal/gateway/AGENTS.md` — append `telemetry.Reader` dependency note (append-only)
- **Leader-integrated (outside sandbox)**
  - `cmd/web/main.go` — construct `telemetry.NewReader(pool)` and pass it into
    `gateway.Deps{TelemetryReader: ...}` alongside the existing account/tesla/googleauth
    deps
- **Not touched**: `internal/telemetry`, `internal/account`, `internal/tesla`,
  `internal/googleauth`, `internal/config`, DB migrations, `sqlc.yaml`, `Makefile`,
  `openspec/roadmaps/`, test infra outside the gateway sandbox
- **Dependencies**: no new Go modules; `internal/telemetry` is already in the monolith;
  `pgxpool` is already used by `cmd/web` (passed to `telemetry.NewReader`)
- **Operational**: read-only; no Tesla API calls on normal renders; no DB writes
