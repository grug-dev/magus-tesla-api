Source: MAG-15 — https://linear.app/magus-monitor/issue/MAG-15/battery-consumed-graph
Roadmap: openspec/roadmaps/RM28-battery-consumed-graph.md
Tier: 2 of 4 (manualcharge; tier 1 is `RM28-telemetry-add-charge-gap-storage`, module
`telemetry`, already archived; tier 3 is `RM28-battery-derive-consumed-per-day`, module
`battery`; tier 4 is `RM28-gateway-add-consumed-graph`, module `gateway`)

## Why

MAG-15 wants a "how much battery did the car consume today" graph. The derivation
(owned by `internal/battery`, tier 3) corrects the raw day-over-day battery delta by
adding back everything a charge put in that day, summed across **both** places the
platform stores charge records: `supercharger_sessions` (`internal/telemetry`, tier 1,
already shipped) and `manual_charge_entries` (`internal/manualcharge`, this tier) (D13).

Tier 3's derivation needs every manual entry whose `charged_on` falls inside the window
it is recomputing (typically ~90 days, D2), so it can sum
`end_battery_pct − start_battery_pct` across all of them. The two existing `Reader`
methods (`ListEntriesByVehicle`, `ListEntriesByAccount`) are limit-based ("most recent N
entries") — with a limit there is no safe number to guess for an unbounded window: a
heavy month would silently truncate the result and understate consumption (D9). This
tier adds the one date-range method tier 3 needs; it changes nothing else in the module.

## What Changes

- **New method on the existing `Reader` port**: `ListEntriesByVehicleBetween(ctx,
  accountID, teslaID, from, to time.Time) ([]Entry, error)`, filtering on `charged_on`
  (a DATE column), inclusive of **both** bounds `[from, to]` (D9, D12: "Manual entries
  match on `charged_on` (date)"). Ordered `charged_on DESC`, matching
  `ListEntriesByVehicle` and the module's existing ordering convention. Returns a
  non-nil empty slice when no rows match, matching both existing Reader methods.
  **No `limit` parameter** (D9) — a date-filtered caller is asking for a window, not a
  count.
- **One new query** in `db/query.sql` (`ListEntriesByVehicleBetween`), reusing the
  existing `idx_manual_charge_entries_vehicle_time (account_id, tesla_id, charged_on
  DESC)` index as a range scan — no new index, no migration.
- **No other change.** `ListEntriesByVehicle`, `ListEntriesByAccount`, the `Writer`
  port, and every existing query, index, and table are untouched.

## Breaking

**No.** Purely additive: one new interface method, one new query, one new generated
sqlc type (`ListEntriesByVehicleBetweenParams`). No existing method signature, query,
table, column, or index changes shape. No caller of the existing `Reader` methods is
affected.

## Modules Affected

- **`internal/manualcharge/`** — sole module touched: `manualcharge.go` (`Reader`
  interface addition, doc comment), `service.go` (`store` interface addition, `dbStore`
  method, `readerService.ListEntriesByVehicleBetween` implementation), `db/query.sql`
  (one new query), new DB-integration tests in `db_integration_test.go`.
- No other `internal/` module. `internal/battery` — this method's only intended caller
  — is **not created, read, or touched** by this change; it is tier 3
  (`RM28-battery-derive-consumed-per-day`), a separate proposal.
- `internal/telemetry` — its own date-range reader
  (`SuperchargerSessionsByVehicleBetween`) was tier 1, already archived. Not touched
  here.
- `internal/gateway` — no gateway change. Tier 4 of this roadmap.

## Database Changes

**None.** No migration, no new table, no new column, no new index. The existing index
`idx_manual_charge_entries_vehicle_time (account_id, tesla_id, charged_on DESC)`
(`internal/manualcharge/db/migrations/20260718000001_add_manual_charge_entries.sql`)
already covers `WHERE account_id = ? AND tesla_id = ? AND charged_on BETWEEN ? AND ?
ORDER BY charged_on DESC` as a single index range scan with no separate sort step — see
design.md's Index Plan for the full justification. The `database` design gate therefore
does not require schema/index sign-off for this tier; design.md still documents why, per
the project's design rule that any DB-touching change carries the reasoning.

## Read Paths Affected

- **`Reader.ListEntriesByVehicleBetween`** (new): a bounded date-range scan under the
  existing per-vehicle index, called once per vehicle per nightly `internal/battery` run
  (tier 3) over a window capped at ~90 days (D2), matching the platform's HTTP
  date-filter convention (`ai/go-conventions.md` §"Read optimization"). Not called on
  any user-facing request path in this tier — tier 3 is the only intended caller; no
  gateway handler calls it directly.
- No existing read path (`ListEntriesByVehicle`, `ListEntriesByAccount`) changes in
  shape, query plan, or cost.

## Capabilities

### Modified Capabilities

- **`manual-charge-log`** — adds a new requirement, "List entries by vehicle within a
  date range", to the read surface. See `specs/manual-charge-log/spec.md`.

### Out of scope (explicitly deferred)

- **The consumed-per-day derivation and gap-detection logic** (D5, D5a, D7a, D12's
  Supercharger-side matching, D13's summation formula) — `internal/battery`'s job,
  tier 3.
- **`cmd/poller` wiring** — tier 3 (D4, D4a).
- **Any gateway change** — tier 4 (`RM28-gateway-add-consumed-graph`).
- **Any change to `ListEntriesByVehicle`, `ListEntriesByAccount`, the `Writer` port, or
  the `manual_charge_entries` schema.** All untouched by this tier.

## Resolved decisions

D1–D16 were settled with the owner via grill-me before this proposal was written
(recorded verbatim in `openspec/roadmaps/RM28-battery-consumed-graph.md`, 2026-08-15).
This tier implements D9 and D12 (the manual-entry half). D1, D2, D3, D4, D4a, D5, D5a,
D6, D7, D7a, D7b, D8, D10, D11, D13, D14, D14a, D15 bound what this tier must **not** do
— see "Out of scope" above. design.md restates the directly-relevant decisions with full
rationale, not re-litigated.
