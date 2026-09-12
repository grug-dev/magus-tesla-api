# platform-add-vehicle-authorization-seam

> Source: MAG-66 — https://linear.app/magus-monitor/issue/MAG-66/3-build-the-authorization-seam-no-table-changes
> Step 3 of the MAG-63 re-key decision record
> (`kkpa/plans/architecture/rekey-vehicle-data-on-vehicle-identity-2026-09-09.md`, section 3).
> Steps 1 and 2 (`analytics-rekey-charge-gaps-on-tesla-id`, MAG-64; and
> `telemetry-rekey-vehicle-snapshots-on-tesla-id`, MAG-65) already re-keyed two tables off
> `account_id`. This step writes no migration. It builds the safety net steps 4-6 will need
> once they remove `account_id` from gateway-facing tables.

## Why

Today `WHERE account_id = $1` is the tenant boundary in SQL, across roughly 40 queries. Even a
handler with a bug cannot read another user's data — the database itself refuses the row. Steps
4, 5 and 6 of the parent plan drop `account_id` from `charging.supercharger_sessions`,
`charging.manual_charge_entries`, and `analytics.vehicle_metrics`/`vehicle_metric_watermarks`.
Once that column is gone, the SQL guard is gone too — a handler that forgets to check ownership
would simply return the row.

This change builds the replacement **before** any of those columns are dropped: a way to prove,
in Go, that the caller owns the vehicle whose id it is about to pass to a module port — so a
handler that skips the check does not compile, rather than merely failing to filter.

**Cross-cutting, not one module's capability.** The new package cannot live in `internal/account`
— `internal/charging` may not import `account` (that module's own design decision), so an
authorization type that only `account` could construct would be unusable from the module that
needs it. It also is not gateway-only: the *type* crosses into every module whose port later
takes it (steps 4-6). Per `openspec/config.yaml`, a change that is not one module's capability
uses the `platform-` prefix.

## What Changes

- **New module `internal/vehicleref`** — a pure, in-memory package, no table, no query, no
  config, mirroring `internal/clock`'s shape. Exports:
  - `type Ref struct` with an unexported `teslaID` field — a value cannot be built by a struct
    literal outside the package.
  - `func Authorize(owned []int64, want int64) (Ref, bool)` — the only way to get a `Ref` for
    one vehicle id, and it demands the caller already hold the list of ids it owns.
  - `func All(owned []int64) []Ref` — every owned id, wrapped, for the "account-wide" read
    shape steps 4-6 will use.
  - `func TeslaIDs(refs []Ref) []int64` — unwraps a slice back to plain ids, once, so every
    module's SQL layer does not re-write the same loop.
  - `func (r Ref) TeslaID() int64` — the accessor a module port needs to build its query.
- **Gateway helper `authorizeVehicle`** in `internal/gateway/handlers/`, beside the existing
  `resolveSelectedVehicle`. It calls `account.Service.RegisteredVehicles(ctx, accountID)` — the
  same call `vehiclesFor` already makes, so this adds no second lookup and no new port method —
  and returns a `vehicleref.Ref` for the requested `teslaID` when it is in the caller's list, or
  a 404 when it is not.
- **`make vehicleref-guard`** — a new standalone guard, joining `make check`'s guard list, that
  fails if `vehicleref.Authorize` or `vehicleref.All` is called from anywhere outside the
  gateway's authorization helper. Escape hatch `// vehicleref:allow: <reason>`.
- **Docs** — `ai/architecture.md` and `internal/gateway/AGENTS.md` gain the rule: the gateway
  authorizes the vehicle; modules below it do not check tenancy. `CLAUDE.md` gains the new
  guard in its allowed-commands list and its `make check` explanation. The root `README.md`
  "Project Structure" tree and "Architecture" table gain a row for the new module. Any
  `kkpa/context/` guide the new module or rule invalidates is grepped for and fixed in this
  change.
- **Unit tests** — for `vehicleref.Authorize`/`All`/`TeslaIDs` and for the gateway's
  `authorizeVehicle` helper, using fakes. This is the one place in the parent plan where the
  owner overrode the plan's default "no unit tests": a mistake here leaks another user's data.

**Not breaking.** No module port changes shape in this change. `internal/vehicleref` has zero
importers other than the gateway helper this change adds. Every existing handler, query, and
port keeps working exactly as it does today.

**Affected modules:** `internal/vehicleref` (new), `internal/gateway` (adds the helper and one
new dependency edge on the new module — the gateway already may depend on any domain module).
No other module's Go source, schema, or config changes.

## Capabilities

### New Capabilities

- `vehicleref` — a vehicle-ownership-proof type: given the caller's own list of vehicle ids and
  a requested id, produce a value that can only exist if the requested id was in that list.

### Modified Capabilities

(none in the OpenSpec sense — the gateway capability gains one internal helper function, not a
new user-facing behavior; no existing requirement changes)

## Impact

- `internal/vehicleref` — new package, four exported symbols, unit tests, `AGENTS.md`.
- `internal/gateway/handlers/` — one new unexported helper (`authorizeVehicle`) and its tests.
  No existing handler is rewired to call it in this change (steps 4-6 do that, per-table, as
  each table's `account_id` column is actually dropped).
- `Makefile` — one new guard target, added to `check`'s dependency list.
- Docs — `ai/architecture.md`, `internal/gateway/AGENTS.md`, `CLAUDE.md`, root `README.md`, and
  any `kkpa/context/` guide the sweep finds.
- No database object of any kind is touched — no migration, no schema, no query.

**No hot read path is touched.** `authorizeVehicle` is not called by any existing handler in
this change, so no request path changes latency or query count. It becomes relevant to the
read-heavy performance profile only once steps 4-6 wire it into an actual handler — evaluated in
each of those changes, not here.
