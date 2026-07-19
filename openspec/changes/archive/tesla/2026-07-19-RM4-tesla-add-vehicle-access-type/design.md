## Context

`internal/tesla` is the anti-corruption adapter over the Tesla Fleet API
(`ai/architecture.md` §1.4, §6): stateless about identity, credentials passed per call,
vendor-shaped `...Tesla` DTOs in `types.go`, port `VehicleService` in `client.go`,
implementations in `vehicles.go`.

`VehicleTesla` is the DTO returned by `ListVehicles` (`GET /api/1/vehicles`). The live
Fleet API response includes an `access_type` field per vehicle (`"OWNER"` or `"DRIVER"`),
but the current struct does not decode it — the field is silently dropped on unmarshal.
This is a pure DTO gap: `ListVehicles` already calls the right endpoint; it just needs the
field wired up.

The binding decisions from the pre-proposal interview (authoritative):
- `access_type` values are `"OWNER"` / `"DRIVER"` (Tesla-supplied); may also be empty
  string if Tesla omits the field for a vehicle.
- The field is a plain `string` on the DTO (no enum type, no validation, no pointer).
- Downstream tiers (account persistence, gateway auto-select) own validation/normalisation;
  the adapter decodes-and-passes-through only.

## Goals / Non-Goals

**Goals:**

- Expose `AccessType` on `VehicleTesla` so `ListVehicles` callers see the field.
- Prove correct decoding with an offline unit test (httptest, no live Fleet API call).
- Keep `AGENTS.md` public interface note in sync with the updated DTO shape.

**Non-Goals:**

- No enum type or constant for `"OWNER"` / `"DRIVER"` — plain `string`, validated downstream.
- No persistence — no table, no migration. That belongs to the `account` tier (RM4 tier 2).
- No change to `raw.go` or `cmd/explore-tesla-api` — `ListVehiclesRaw` already exists and
  already decodes the full `access_type` from the raw bytes; no new raw method is added.
- No change to `VehicleService` interface signature — `ListVehicles` already returns
  `[]VehicleTesla`; callers automatically receive the new field on their existing slice.

## No Database Object

This change introduces **no database object** — no table, no column, no index, no
migration, no sqlc query. The `database` Design Gate is not triggered.

## No Miles-to-Km Companions

`access_type` is a string classification field (not a distance or speed). The mandatory
`milesToKm` companion rule (`ai/go-conventions.md`) is inapplicable. A reviewer must not
flag the absence of a `Km()` or `Kmh()` companion on `AccessType`.

## Design Decisions

**D1 — Plain `string` field, no enum type or validation in the adapter.**

`AccessType` is typed as `string` with json tag `access_type`. Tesla currently returns
`"OWNER"` or `"DRIVER"`, but the adapter's role is to decode-and-pass-through vendor data
faithfully, not to enforce enumerations. Defining a Go enum type here would:
(a) require updating the adapter whenever Tesla adds a new `access_type` value, and
(b) risk silent zero-value coercion (`""`) for unrecognised future values, hiding them from
callers rather than surfacing the raw string.

A plain `string` preserves whatever Tesla sends (including unexpected future values) and
pushes enumeration enforcement to the `account` module, which owns normalisation and
persistence and is the correct home for that policy.

**D2 — No new raw sibling; no `cmd/explore-tesla-api` change.**

The raw-explorer sync rule (`CLAUDE.md` §Tesla API Exploration) states: "Whenever a new
Fleet API call is added to `internal/tesla` (a new typed _method_ in `vehicles.go`)…"
This change adds a field to an existing DTO struct — it does not add a new method to
`vehicles.go` or to `VehicleService`. `ListVehiclesRaw` already exists in `raw.go` and
already returns the full raw JSON of the `/api/1/vehicles` response including `access_type`
(raw methods return `json.RawMessage` so no field is ever omitted). Therefore:
- No new `Raw*` method is needed.
- No change to `cmd/explore-tesla-api/main.go` is needed.
- No change to `cmd/explore-tesla-api/README.md` is needed.
The sync rule is already satisfied.

**D3 — Offline httptest decode test; no live Fleet API call.**

The adapter's existing test convention (`AGENTS.md` §Testing notes) is to test typed
methods against a local `httptest.Server` serving canned JSON, reusing the unexported
`baseURL` field to point the client at the fake. The new test follows this convention:
a canned `/api/1/vehicles` response with `"access_type": "OWNER"` on one vehicle is served
by an `httptest.Server`; the test calls `client.ListVehicles(...)` and asserts
`vehicles[0].AccessType == "OWNER"`. No live Tesla call, no `.env`, no DB.

**D4 — `AGENTS.md` "Other public symbols" note updated; no other doc changes.**

The `VehicleTesla` DTO shape note in `AGENTS.md` lists the `...Tesla` DTOs. Adding a field
to `VehicleTesla` makes the documented shape stale, so the note must be updated in the same
change. No other doc requires updating: `README.md` defers to
`ai/tesla-fleet-api-endpoints.md` for endpoint status (which does not need updating — no
new method was added), and the root `README.md` / `ai/architecture.md` do not enumerate DTO
fields.

## Read-Path Impact

None. `VehicleTesla` is a DTO struct living entirely in memory. Adding a field does not
affect any DB query, any index, or any hot read path. The performance profile (read-heavy)
is fully satisfied.
