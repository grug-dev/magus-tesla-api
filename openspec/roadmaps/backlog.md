# Backlog — deferred & cross-cutting work

Items that are **not** part of a single lifecycle roadmap tier — security hardening, tech debt,
and cross-cutting improvements. Each becomes its own OpenSpec change **when picked up**;
until then, this file is the durable record of **what to do next**.


## How to use this file

- Add a new subsection on [Pending to be picked up](#pending-to-be-picked-up) when a decision defers real work (with a clear **trigger** so it isn't forgotten). Use the following template:

Architecture is a valid MODULE-NAME when it is cross-cutting (e.g., `security`, `logging`, `metrics`, `deployment`) or a new module that doesn't yet exist.

```
## {{NUMBER}}. {{MODULE-NAME}} — {{TITLE}}

### PROPOSAL

{{Description of the work/proposal, why it was deferred, and what triggers it to be picked up.}}

### ORIGIN

{{Where the proposal came from (tier design.md, discussion, etc.)}}

```

- When you pick an item up, run the skill `/kkpa-dev-harness-pipeline:propose` to generate a new OpenSpec change (proposal.md, design.md, specs/<domain>/spec.md, tasks.md) and move the item into that change's design.md.




# Pending to be picked up


## 1. tesla — Percent-encode `dx/charging/history` date query params

### PROPOSAL

`internal/tesla` `Client.ChargingHistory` (in `vehicles.go`) builds the request URL by
concatenating the optional `startTime`/`endTime` query params as raw strings, WITHOUT
percent-encoding. Date/time values (e.g. `2026-06-28T10:24:41-05:00`) contain `:` and `+`,
which are URL-significant — an unencoded value can produce a malformed query and a failed or
wrong-window API call.

**Currently dormant / harmless:** no caller passes date params — the nightly collector calls
`ChargingHistory(creds, ChargingHistoryParams{})` (full fetch, no filter), so the affected
code path never runs.

**TRIGGER — fix this FIRST when** a date-windowed / incremental charging-history backfill is
added (i.e. the collector starts passing `StartTime`/`EndTime` to avoid re-fetching the whole
history every night). Encode both params with `url.QueryEscape` (or build the query via
`url.Values`) before that feature ships.

### ORIGIN

`RM2-tesla-add-charging-history` (RM2 tier 1) review finding **R1**, deferred through
`RM2-telemetry-add-charging-stats` (tier 2, which chose full-fetch nightly so no date params
are passed). Recorded in the archived RM2 progress.json.

Note: the former "CHARGING STATS" backlog item shipped as roadmap **RM2-charging-stats** (both
tiers archived 2026-07-16) — see `openspec/roadmaps/archive/RM2-charging-stats/`.


## 2. manualcharge / telemetry — Semi-automatic home-charge detection

### PROPOSAL

Infer home / AC charging sessions from the vehicle's **own** telemetry — `charge_energy_added`
deltas + `charging_state` transitions (unambiguously the vehicle's own data, so **no**
multi-tenant attribution problem) — to **pre-fill or suggest** entries in the manual charge
log (`internal/manualcharge`, shipped by RM3). This directly reduces the manual-entry
friction (people forget to log) that RM3 ships with.

**TRIGGER — pick this up when** polling gets finer than the current once-nightly cadence: a
charge session is minutes-to-an-hour long, so a single daily snapshot cannot reconstruct one.
Pair with an event-driven / sub-hourly poller (or Tesla Fleet Telemetry streaming). It is the
vehicle-side automatic counterpart to RM2's "charge-session detector" future note.

### ORIGIN

`RM3-manual-charge-log` roadmap "Future work" (leader session 2026-07-18) — the known
adoption-friction gap of the user-asserted manual-entry approach.


## 3. manualcharge — Integration tests for the remaining CHECK-constraint scenarios

### PROPOSAL

The `manual_charge_entries` migration enforces three CHECK constraints that the spec
(`RM3-manualcharge-add-entries` `specs/manual-charge-log/spec.md`) names as scenarios but that
have **no dedicated integration test**: `end_battery_pct` out of range (0–100), invalid
`charging_type` (not `AC`/`DC`), and invalid `location_kind` (not `HOME`/`WORK`/`OTHER`). The
constraints are live and verified to exist (psql), and the closely-related `start_battery_pct=101`
case IS tested (T6.2e) — so this is a **test-coverage** gap only, not a correctness gap.

**TRIGGER — pick up when** the `manualcharge` module is next touched (e.g. RM3 tier 2 gateway UI
work, or any change adding fields/constraints). Add three `TestCreate_CheckConstraint_*` cases in
`internal/manualcharge/db_integration_test.go` mirroring the existing pattern; each asserts a DB
error is returned. Cheap (~30 lines) against the live DATABASE_URL-gated harness.

### ORIGIN

`RM3-manualcharge-add-entries` (RM3 tier 1) review finding **R3** (minor, accepted). The verdict
was `approved`; the mandated T6.2 criteria were met, so the added coverage was deferred here
rather than reopening the review. Recorded in the RM3 tier-1 progress.json review round 1.


## 4. gateway — Supercharger Stats screen

### PROPOSAL

Build the "Supercharger Stats" page the navigation sidebar has been pointing to as a
placeholder ("soon") since `gateway-add-stitch-design-handoff`. The nav entry currently
renders as `Href="#"` with a "Soon" badge (the Stitch design's "Supercharger Stats" item,
icon `analytics`); this change makes it a live route + page.

Likely consumes the existing `telemetry.Reader` and/or `tesla` charging-history data
(read-only, same indexed reads as the dashboard). The concrete metrics/layout are a
design-time decision for the change that picks this up; mirror the `charges`
gold-standard slice (view model → page/fragments → Gin handler → routes) composed from
the `ui/` kit, semantic tokens only.

**TRIGGER — pick up when** the user wants the "Supercharger Stats" nav entry to become a
live page (replace the placeholder link + "Soon" badge with a real `/supercharger-stats`
route + page). The placeholder wiring (nav item + `ui.NavItem.Placeholder`) was added by
`gateway-add-stitch-design-handoff`.

### ORIGIN

`gateway-add-stitch-design-handoff` grill decision **D9** (user, 2026-07-26): Stitch nav
items "Supercharger Stats" and "Settings" are placeholders in this change, both recorded
as future work here. See the change's `tasks.md` T6 and `design.md` §3.4.


## 5. gateway — Settings page

### PROPOSAL

Build the "Settings" page the navigation sidebar has been pointing to as a placeholder
("soon") since `gateway-add-stitch-design-handoff`. The nav entry currently renders as
`Href="#"` with a "Soon" badge (the Stitch design's "Settings" item, icon `settings`);
this change makes it a live route + page.

Likely a read/write surface for user preferences (account, units, notification, Tesla
connection management). Any writes go through the owning module's port (e.g. `account`),
respecting the gateway's read-only-at-request-time rule + the documented user-initiated
write exception pattern. Concrete settings are a design-time decision for the change that
picks this up; mirror the `charges` gold-standard slice, `ui/` kit, semantic tokens.

**TRIGGER — pick up when** the user wants the "Settings" nav entry to become a live page
(replace the placeholder link + "Soon" badge with a real `/settings` route + page). The
placeholder wiring (nav item + `ui.NavItem.Placeholder`) was added by
`gateway-add-stitch-design-handoff`.

### ORIGIN

`gateway-add-stitch-design-handoff` grill decision **D9** (user, 2026-07-26): Stitch nav
items "Supercharger Stats" and "Settings" are placeholders in this change, both recorded
as future work here. See the change's `tasks.md` T6 and `design.md` §3.4.



# BRAINSTORMING


## 1. **Security** — encrypt Tesla tokens at rest

### PROPOSAL

Encrypt Tesla tokens at rest (tesla_tokens.access_token / refresh_token) — app-level column encryption (AES-GCM with a key from the environment) or pgcrypto.

 kept tokens plaintext to stay scoped; acceptable on a locked-down local Postgres.

### ORIGIN
add-account-module (Tier 1) design.md Open Question


## 2. tesla / energy — Wall Connector charge history (EVALUATED & DECLINED 2026-07-18)

### PROPOSAL

Tesla's Fleet API exposes **Wall Connector** energy: `GET /api/1/products` discovers the
`energy_site_id` + connector `device_id`/`din`, and
`GET /api/1/energy_sites/{id}/telemetry_history?kind=charge` returns per-session
`energy_added_wh` + start + duration. The adapter already has `ProductsRaw` +
`EnergyChargeHistoryRaw` (raw / exploration only — off the `VehicleService` interface).

**DECLINED for nightly persistence:** the payload is **charger-centric** — every session is
keyed only by the connector (`target_id`/`din`), carries **no VIN / vehicle_id**, and cannot
be reliably attributed to a specific owned vehicle in a multi-tenant platform (a second car,
guest, or neighbor on the same connector is indistinguishable). Observed historical sessions
also predate telemetry collection and can never be back-correlated. Shipped
`RM3-manual-charge-log` (user-asserted manual entry) instead.

**TRIGGER to revisit — only if** a single-vehicle-connector guarantee **plus a
user-configured `connector → vehicle` mapping** is acceptable (attribution by configuration,
not inference), OR Tesla adds a vehicle identifier to the charge-history payload.

### ORIGIN

Leader analysis session 2026-07-18 — inspected `cmd/explore-tesla-api/output/3-Products.json`
+ `4-EnergyChargeHistory.json`; user decision to skip the energy API in favor of manual entry.
