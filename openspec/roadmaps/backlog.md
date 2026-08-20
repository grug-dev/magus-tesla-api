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



## 6. gateway — Vehicle selector (multi-vehicle nav header)

### PROPOSAL

The navigation header added by `gateway-add-stitch-design-handoff` (design DD2/DD4) shows the PRIMARY vehicle only — the first entry of `account.RegisteredVehicles`. Accounts with multiple Tesla vehicles have no way to switch which vehicle the nav header surfaces (battery %, "Connected" status). This change adds a vehicle selector to the nav header (or the top bar — the Stitch "Tesla Core" Dashboard design carries a top-bar vehicle-switch affordance), letting the user pick which owned vehicle the header reflects.

Reuses the existing `account.RegisteredVehicles` + `telemetry.Reader.LatestSnapshotsByAccount` read ports — no new read path, no DB object. The selected vehicle id is a session/cookie preference (or query param) read at request time; mirror the `charges` gold-standard slice, `ui/` kit, semantic tokens, zero client JS (CSS-only DaisyUI `dropdown`/`select`).

**TRIGGER — pick up when** the user wants multi-vehicle accounts to choose which vehicle the nav header shows (currently it always shows the first registered vehicle).

### ORIGIN

`gateway-add-stitch-design-handoff` design decision **DD4** + leader decision **D13** (2026-07-26): the nav header was scoped to the primary vehicle in this change; the multi-vehicle selector was deferred and recorded here per the project's future-work rule. See the change's `design.md` §3.2 and `progress.json` `decisions[]` D13.


## 7. analytics / tesla / account / telemetry — Trim-exact pack capacity

### PROPOSAL

`internal/analytics`'s pack-capacity reference table (`internal/analytics/capacity.go`) is keyed
only on the Fleet API's `vehicle_config.car_type` (e.g. `"model3"`, `"modely"`) — it cannot
distinguish trims of the same model (e.g. Model 3 Standard Range vs. Long Range), which have
materially different usable pack capacities. This makes the Wh/km efficiency metric's capacity
correction model-coarse, not trim-exact, whenever `car_type` IS known (the "unknown" case is
already handled — `Efficiency.Approximate=true`, no correction — that is NOT what this item is
about).

The live `vehicle_config` payload carries `trim_badging` (confirmed present, ~47 keys total),
but `internal/tesla/types.go`'s `VehicleConfigTesla` extracts only `exterior_color` + `car_type`
(`RM6-telemetry-capture-vehicle-config`, archived). Closing this gap needs: (1) extracting
`trim_badging` in `internal/tesla` (leaf DTO field addition, same shape as
`VehicleConfigTesla.CarType`); (2) a new `account.Vehicle`/`account.OwnedVehicle` field +
`SetVehicleConfigIfEmpty`-equivalent port method to persist it (mirrors the `RM6` tier-1/tier-2
split — a schema-touching tier plus an application-wiring tier); (3) the nightly collector
capturing it (`internal/telemetry/service.go`, same `captureVehicleConfig` shape); (4) a
trim-keyed (or trim+car_type-keyed) `packCapacityKWh` table in `internal/analytics/capacity.go`,
replacing or supplementing the model-coarse one.

**TRIGGER — pick this up when** trim-exact pack capacity is wanted (i.e. the model-coarse
~±10-15% capacity error within a single `car_type` is judged material enough to fix). Until
then, `internal/analytics` returns a value with a documented model-coarse approximation
(`Efficiency.Approximate` stays reserved for the separate "capacity fully unknown" case).

### ORIGIN

`battery-add-efficiency-metric` design.md decision **D1b** (leader ↔ user grill-me pass,
2026-08-03): pack capacity is sourced from an in-package table keyed on `car_type` only,
because `trim_badging` extraction was explicitly scoped out of that change. Recorded here per
the project's future-work rule.


## 8. gateway — Top charging sites breakdown

### PROPOSAL

Add a fourth section to the Supercharger Stats page ranking the most-used Supercharger
locations — `SiteLocationName` grouped by session count and summed energy (e.g. "Bogota
Norte · 12 visits · 340 kWh"). `SiteLocationName` and `CountryCode` are already on the
`telemetry.SuperchargerSession` domain type, so this is **pure in-Go grouping over the
slice the page already reads** — no new port method, no new query, no DB object.

Deferred to keep the initial page to three sections (tiles + kWh-per-month chart +
sessions table); a fourth section is another view-model type, Templ component, and test
group for a metric that is interesting rather than essential.

**TRIGGER — pick up when** the user wants to know *where* they charge, not just how much —
most naturally once the Supercharger Stats page has been in use for a while and the
sessions table has grown long enough that scanning it stops answering the question.

### ORIGIN

`gateway-add-supercharger-stats` grill decision **D3** (leader ↔ user grill-me pass,
2026-08-08): the "tiles + chart + table + top sites" composition was offered and the
three-section variant chosen. Recorded per the project's future-work rule; see that
change's `design.md` "Future work".


## 9. gateway — kWh values are not comma-grouped, unlike km

### PROPOSAL

`internal/gateway`'s kWh display labels (e.g. `fmt.Sprintf("%.1f kWh", ...)` in
`buildSuperchargerTiles`/`buildSuperchargerChart`, `fmt.Sprintf("%.2f kWh", ...)` in
`buildSuperchargerRows`/`chargeEntryVMFromEntry`) render with **no** thousands separator,
while km values (`formatKm`/`formatKmRaw`, `internal/gateway/handlers/format.go`) already are
comma-grouped. A large kWh figure — e.g. a yearly Supercharger energy total — currently renders
as `"12500.4 kWh"` instead of `"12,500.4 kWh"`, inconsistent with how the same page's km values
already read.

The fix is small: route kWh formatting through a new `formatKWh`-style helper built the same
way as `formatKm` (reuse the existing `commaGroup` helper in `format.go`), and swap the
relevant `fmt.Sprintf("%.Nf kWh", ...)` call sites over to it.

**TRIGGER — pick this up when** the user wants kWh values visually consistent with km/currency
values (comma-grouped), or when a kWh figure large enough to need grouping (≥ 1,000 kWh) is
first noticed rendering ungrouped in production.

### ORIGIN

Descoped from **MAG-9 / `gateway-format-currency-values`** (design.md D3, 2026-08-13): MAG-9
asked specifically for currency-value formatting; kWh formatting is a real, adjacent
inconsistency but out of that ticket's scope. Recorded here per the project's future-work rule
rather than silently bundled in or silently dropped.


## 10. testing — `testdb.Provision` can mask a real migration failure as "unavailable"

### PROPOSAL

`internal/testdb.Provision` tries `DATABASE_URL` first; when applying migrations against it
fails, it logs `DATABASE_URL not usable` and falls through to the testcontainer path. That
fallback does NOT distinguish *"the DB was unreachable"* from *"the DB was reachable and the
migration is genuinely broken"*. If Docker is then also absent, the error the caller finally
sees is the container path's `testdb.ErrUnavailable` — so a real, reproducible migration bug
gets reported as mere environment-unavailability, and a `TestMain` that skips on
`ErrUnavailable` (currently `internal/telemetry`) will skip instead of failing. The `AGENTS.md`
mitigation ("check the log for `DATABASE_URL not usable`") does not surface under default,
non-`-v` `go test` / `make check` output.

Narrower than the bug it descends from — it needs a reachable-but-broken `DATABASE_URL` **and**
no Docker fallback — but that combination is plausible in a CI runner without Docker-in-Docker,
which is exactly where a silent skip is most costly.

Fix sketch: have `Provision` classify the `DATABASE_URL` attempt — a connection/dial failure
keeps today's fall-through, whereas a successful connection with a failing `goose up` returns a
hard, unwrapped error immediately instead of degrading to the container path.

**Trigger:** pick this up when CI starts running these tests without Docker-in-Docker, or the
next time anyone touches `internal/testdb`.

### ORIGIN

Finding R3 (minor, non-blocking) from the round-2 review of
`telemetry-add-derived-consumption-columns` (MAG-10). Raised by `telemetry-reviewer` when
re-verifying the R1 fix, which closed the wider version of this same conflation. The reviewer
explicitly pushed back on the leader's justification for accepting the residual, and was right
to; deferred here rather than expanding that change's scope a third time at its archive gate.


## 11. gateway / telemetry — Supercharger start/end SOC verification UI (and the measured-SOC alternative)

### PROPOSAL

`RM27-supercharger-battery-percentage` **shipped storage only**. It added five nullable columns
to `supercharger_sessions` — the human-owned trio `start_battery_pct` / `end_battery_pct` /
`battery_pct_source` plus the reserved snapshot pair `start_battery_pct_est` /
`end_battery_pct_est` — all five excluded from the nightly upsert so a re-poll can never
overwrite them.

**Nothing reads or writes those five columns.** The owner **descoped** the two remaining tiers
on 2026-08-15 (roadmap RM27 decision D11): there is no SOC estimator and no UI. All three
follow-ups below are therefore open, in priority order:

1. **Verification UI — the edit form (`gateway`)** — the **write** path: an edit form on the
   Supercharger Stats page to enter or correct start/end %, writing the trio and setting
   `battery_pct_source = 'user_verified'`. It needs a telemetry **write** port (none exists for
   this table yet — today's only writer is the poller) plus CSRF + tenant checks per the
   gateway's AGENTS.md rule for user-initiated writes, the `ui/` kit conventions and bilingual
   ES/EN labels. This is the **cheapest way to make the columns useful** and does not depend on
   items 2 or 3 — a human simply types the numbers they saw in the car. A "sessions with no
   value" queue is a straight `WHERE start_battery_pct IS NULL`. While no estimator exists the
   form leaves `start_battery_pct_est` / `end_battery_pct_est` NULL.

2. **SOC estimator (`analytics`) — DESCOPED from RM27, kept here** — solve start/end SOC from
   billed `energy_kwh` + session duration against a DC taper curve. The maths was worked out
   and validated during the MAG-14 grill (2026-08-15) and is worth recording so it need not be
   redone: `delta = energy_kwh / pack_kwh × 100` is exact and needs no curve; only `start` is
   unknown, and `T(start) = ∫[start→start+delta] (C/100)/P(s) ds` is **strictly increasing** in
   `start` for any decreasing `P`, so bisection on `[0, 100−delta]` has a unique solution.
   Validated against the four captured sessions: three solve, and the two records that form a
   single physical stop reconstruct to a clean 80.7% charge limit. **The fourth does not solve
   at all** — `chargeStopDateTime` marks the end of the *billing session*, not of power
   delivery, so duration is only an upper bound on charging time and ~25% of real sessions are
   "too slow" for any SOC band. Any implementation must therefore return delta-only rather than
   a fabricated pair. If this lands, item 1's form must also freeze the estimate into the
   `_est` pair per RM27 decision R8 (write-once, never refreshed; the gateway is its only legal
   writer, since it can read `analytics` and write through a telemetry port without an import
   cycle).

3. **Measured SOC instead of solved (`telemetry`)** — detect an active charging session and
   sample `vehicle_data.battery_level` at start and stop, giving true values rather than solved
   ones, and making item 2 unnecessary. **Deferred, not declined:** it wakes the car on paid
   Fleet API calls, needs a new poller mode (today's poller runs once nightly, and
   `vehicle_snapshots` is capped at one row per calendar day, so no snapshot ever falls inside a
   ~25-minute session), and it can never recover history — only future sessions benefit.

**TRIGGER (1):** as soon as you want a start/end % on any session at all — this is the only
item that makes the shipped columns do anything. **TRIGGER (2):** if typing values by hand
becomes tedious enough to want a machine guess, accepting that ~1 in 4 sessions yields only a
delta. **TRIGGER (3):** if estimates prove unsatisfying, or waking the car per session becomes
acceptable.

Estimator accuracy (2) would additionally be bounded by item **7** (trim-exact pack capacity) —
`capacityFor` is model-coarse, so every `model3` trim shares one capacity constant, and that
constant is a direct multiplier on the solved delta.

### ORIGIN

MAG-14 grill-me session, 2026-08-15 — the grill established that the API carries no SOC field
and that the verification loop needs a gateway tier of its own (roadmap RM27 decision D4).
Extended the same day when the owner **descoped the estimator and the UI tiers entirely**
(RM27 decision D11), reducing RM27 to the five columns and moving items 1–3 above here.


## 12. Architecture — charging data is split across two modules

### PROPOSAL

The platform stores charge sessions in **two tables owned by two different modules**:
`manual_charge_entries` (`internal/manualcharge`) and `supercharger_sessions`
(`internal/telemetry`). They carry the same conceptual payload — when the car charged, how
much energy went in, and (per RM27) the start/end battery percentage — but no module owns
"charging" as a domain.

The cost is paid by every consumer. Any question of the form *"how was this vehicle charged?"*
must compose two ports, sum across both shapes, and keep the two in step. `internal/analytics`
already does this twice (`sumSuperchargerKWh` + `sumManualKWh` for efficiency), and RM28 adds
a third pair of date-range readers plus a summation across both sources for the consumed
graph. Each new consumer re-implements the same fan-out.

Possible shapes, none yet evaluated: a `charging` module owning both tables; a read-side
`ChargeReader` port that unions the two behind one interface without moving data; or leaving
ownership alone and extracting only the shared summation.

**TRIGGER to pick up:** a third consumer needs combined charge data, OR the Supercharger
verification UI (item 11) lands and makes the two write paths visibly inconsistent to users.
Deliberately **not** bundled into RM28 — it would turn a graph ticket into a cross-module
data migration.

### ORIGIN

MAG-15 / RM28 grill-me session, 2026-08-15 — the owner raised it while settling which module
should own the missing-charge table ("maybe we don't have the right bundle for charging data
records… but that's out of the scope of this ticket") and scoped it out explicitly.


## 13. gateway — history charts key a `map[time.Time]` without normalizing the lookup side

### PROPOSAL

All three history charts in `internal/gateway/handlers/history.go` bucket their data into a
`map[time.Time]` and then read it with a loop variable derived from `start`:

```go
byDay[<some UTC-midnight date>] = row      // write side
for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
    row, ok := byDay[d]                    // read side — d's Location is NOT normalized
```

Go compares `time.Time` map keys on the wall clock **and the `*time.Location`**, so the two
sides must agree on both. The write sides are UTC (`effectiveDayUTC` for odometer/battery;
`analytics.DayConsumption.Date`, normalized by `internal/analytics`'s `calendarDay`, for
consumed). The read side is UTC only when `start` came from an explicit `?start=&end=` pair
(`time.Parse` defaults to UTC) or the no-cookie fallback. On the **both-params-absent** path
with a valid non-UTC `browser_tz` cookie, `start` carries that zone's `*time.Location`
(`browserToday` → `startOfDayIn`) — every lookup then misses and **all three charts render
as all-no-data, with no error logged anywhere.**

**Currently unreachable through the UI:** `defaultHistoryHref` (`handlers.go`) and every
preset button (`history.templ`) send explicit, UTC-parsed `start`/`end` params, so the
browser never issues a param-less request. The path is reachable only by hand-calling
`GET /ui/dashboard/history` with a `browser_tz` cookie and no params.

**Fix when picked up:** normalize the read side once — `d` (or the map key) through a single
`calendarKey(t) time.Time` helper that strips the zone to UTC midnight — and cover it with a
test that drives the both-absent path with a non-UTC cookie. Note the fix belongs to **all
three** charts, not just the consumed one; a per-chart patch would leave the same trap in the
other two.

**TRIGGER — fix this FIRST when** any caller can reach `parseHistoryRange` with both params
absent and a `browser_tz` cookie set: a param-less link or redirect to
`/ui/dashboard/history`, an external/API consumer of that endpoint, or a change that makes
`defaultHistoryHref` stop emitting explicit dates.

### ORIGIN

`RM28-gateway-add-consumed-graph` (RM28 tier 4) review finding **R1**, raised by the leader in
the review brief and confirmed by `gateway-reviewer` round 1 (`minor`, accepted, deferred).
Pre-existing — the odometer and battery charts have carried the identical read-side gap since
`gateway-dashboard-history-charts`; tier 4 neither introduced nor worsened it, and design.md
**D-G3** explicitly forbade adding gateway-side timezone handling to the new function.
Recorded in that change's archived progress.json.


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
