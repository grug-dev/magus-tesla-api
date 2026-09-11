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


## 2. charging / telemetry — Semi-automatic home-charge detection

### PROPOSAL

Infer home / AC charging sessions from the vehicle's **own** telemetry — `charge_energy_added`
deltas + `charging_state` transitions (unambiguously the vehicle's own data, so **no**
multi-tenant attribution problem) — to **pre-fill or suggest** entries in the manual charge
log (`internal/charging`, shipped by RM3). This directly reduces the manual-entry
friction (people forget to log) that RM3 ships with.

**TRIGGER — pick this up when** polling gets finer than the current once-nightly cadence: a
charge session is minutes-to-an-hour long, so a single daily snapshot cannot reconstruct one.
Pair with an event-driven / sub-hourly poller (or Tesla Fleet Telemetry streaming). It is the
vehicle-side automatic counterpart to RM2's "charge-session detector" future note.

### ORIGIN

`RM3-manual-charge-log` roadmap "Future work" (leader session 2026-07-18) — the known
adoption-friction gap of the user-asserted manual-entry approach.


## 3. charging — Integration tests for the remaining CHECK-constraint scenarios

### PROPOSAL

The `manual_charge_entries` migration enforces three CHECK constraints that the spec
(`RM3-manualcharge-add-entries` `specs/manual-charge-log/spec.md`) names as scenarios but that
have **no dedicated integration test**: `end_battery_pct` out of range (0–100), invalid
`charging_type` (not `AC`/`DC`), and invalid `location_kind` (not `HOME`/`WORK`/`OTHER`). The
constraints are live and verified to exist (psql), and the closely-related `start_battery_pct=101`
case IS tested (T6.2e) — so this is a **test-coverage** gap only, not a correctness gap.

**TRIGGER — pick up when** the `charging` module is next touched (e.g. RM3 tier 2 gateway UI
work, or any change adding fields/constraints). Add three `TestCreate_CheckConstraint_*` cases in
`internal/charging/db_integration_test.go` mirroring the existing pattern; each asserts a DB
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

**NOTE (2026-09-10, after RM52 tier 3).** RM52 added a *measured* per-vehicle capacity in
`charging.monthly_effective_capacity`, read by `internal/charging`'s own `packCapacityKWh`.
That is a different table from this item's `internal/analytics/capacity.go`, which RM52 never
touched. Before building the trim table, weigh reading the measured value instead: `analytics`
already imports `charging`, so there is no cycle. The measured value is not always there — a
month with too few big charges stores no number — so a fallback would still be needed. Both
routes stay open; this note only says the cheaper one now exists.

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
`manual_charge_entries` (`internal/charging`) and `supercharger_sessions`
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


## 14. charging — `AGENTS.md`'s forbidden-import list never named `internal/analytics`

### PROPOSAL

`internal/charging/AGENTS.md` "MUST NOT import" lists `internal/tesla`, `internal/account`,
`internal/telemetry`, `internal/gateway` and any other module's `db` sub-package — but not
`internal/analytics`. That was harmless while `GapWriter` lived in `internal/telemetry`
(explicitly forbidden). RM29 tier 5 moved `GapWriter` and `charge_gaps` to
`internal/analytics`, so the sentence in `docs/battery-consumed-graph.md` §"So when does your
edit show up?" that read "its `AGENTS.md` forbids them, so it structurally cannot reach
`GapWriter`" stopped being literally true of the forbidden list.

The isolation itself is **not** broken: the same file's "Allowed Imports" is a closed
allowlist (stdlib, `uuid`, `pgx`/`pgxpool`/`pgtype`, its own `chargingdb`), which excludes
`internal/analytics` already, and `internal/charging` imports no domain module today —
verified during tier 5. Tier 5 reworded the doc to cite the allowlist rather than the
forbidden list, which is accurate. The remaining work is one belt-and-braces line: add
`internal/analytics` to the MUST NOT list so the two lists agree.

Deferred because `internal/charging` is not in RM29 tier 5's scope (analytics + telemetry +
leader only), and pulling a third module into a pure ownership move would widen the change
the owner deliberately kept narrow (tier 5 decision **I2**).

**TRIGGER — fix this when** anything next opens `internal/charging/AGENTS.md`, or at the
latest during RM29 **T6** (`RM29-charging-add-charge-sessions`), which owns that module and
will be editing that file anyway.

### ORIGIN

`RM29-analytics-own-charge-gaps` (RM29 tier 5), leader's wave-4 doc sweep for task 4.4.
Recorded in that change's progress.json `decisions[]`.


## 15. analytics — `charge_gaps`' SQL `COMMENT ON` still names `internal/battery` and `internal/telemetry`

### PROPOSAL

`internal/analytics/db/migrations/20260815000002_add_charge_gaps.sql` carries a
`COMMENT ON TABLE charge_gaps` (line 73) whose text reads *"Written by `internal/battery`
through the GapWriter port (telemetry never calls battery) … Owned by
`internal/telemetry`"*. Both module names are wrong now: `internal/battery` was renamed
`internal/analytics` in **RM29 T1**, and ownership moved to `internal/analytics` in
**RM29 T5**. Because sqlc copies table/column comments into generated code, the same
stale sentence is reproduced verbatim in `internal/analytics/db/models.go:12`, where it
is the first thing a reader (human or agent) sees about the type.

**Why T5 did not fix it.** Two routes exist and both are closed to an unattended run:

1. *Edit the migration file.* T5's user-confirmed design decision **I1** is that the
   migration moves with **zero content change** — that is precisely what makes the
   `git mv` safe and lets goose recognize the already-applied version in its new
   directory. Editing it would break the gate the owner confirmed, and would not change
   the live database anyway: the migration is already applied, so its text is never
   re-executed. The comment in the running Postgres would stay stale.
2. *Add a new migration issuing a corrected `COMMENT ON TABLE` / `COMMENT ON COLUMN`.*
   This is the correct fix, but it is **DDL**, so it trips the pipeline's built-in
   `database` design gate — schema, rationale and index plan need the owner's explicit
   confirmation before implementation. The owner was asleep.

Note the staleness is **pre-existing**: the `internal/battery` half has been wrong since
T1. T5 neither introduced nor worsened it — it only relocated the file, which is what
made it visible inside `internal/analytics`.

**Suggested shape.** A comment-only migration (`COMMENT ON TABLE charge_gaps IS …` plus
the affected `COMMENT ON COLUMN` lines), re-running `make sqlc` so `models.go` picks up
the corrected text. No table, column, index or constraint changes — the cheapest possible
DDL, but DDL nonetheless, so it still needs the design-gate conversation.

**TRIGGER — fix this when** the owner next confirms a database design gate for
`internal/analytics`, or opportunistically during **T6**/**T7** if either already carries
a migration the owner is reviewing.

### ORIGIN

`RM29-analytics-own-charge-gaps` (RM29 tier 5). Found by the leader's post-review sweep
while fixing `analytics-reviewer` round-1 finding **R1-1**; the reviewer itself missed it.
Recorded in that change's progress.json `decisions[]` as **A6**.


## 16. telemetry / analytics — Drop the five battery-pct columns off `supercharger_sessions` (the contract half of RM29 T6)

### PROPOSAL

RM29 tier 6 was deliberately **expand-only**: `internal/charging` gained `charge_sessions`
carrying the five human-verified battery-percentage columns (`start_battery_pct`,
`end_battery_pct`, `battery_pct_source`, `start_battery_pct_est`, `end_battery_pct_est`),
and `internal/telemetry`'s `supercharger_sessions` kept its own copies. The contract half —
dropping telemetry's five columns — is still outstanding.

It could not ship in T6 for a concrete reason worth not rediscovering: `MIGRATIONS_DIRS`
runs the `telemetry` directory before `charging`, and goose walks each directory to
completion, so a DROP in the same change would have executed **before** the backfill that
reads those columns. The data is unrecoverable — nothing in the codebase writes them, so the
one populated row was hand-entered.

This is **not** a one-line `ALTER`. `internal/analytics`'s consumed correction and gap
detection read those columns today, so they must be re-pointed at `charging`'s copies first.
That makes this at minimum a two-module change and a database design gate. T4's history is
the direct precedent: its drop also turned out to need the derivation moved first, making
that tier wider than its roadmap line.

**TRIGGER — pick this up when** RM29 T7 has archived (so `cmd/poller`'s composition root has
stopped moving) and the owner is ready for another database design gate. It is explicitly
**not** part of T7.

### ORIGIN

`RM29-charging-add-charge-sessions` (RM29 tier 6), decision **I4** — the owner's expand-only
call at the database design gate, 2026-08-22.


## 17. gateway / telemetry — Decide whether `gateway` should read `telemetry.Reader` / `Snapshot` at all

### PROPOSAL

MAG-30 asked a broader question than it fixed: *"am I right that `gateway` should not read
any data from the `telemetry` module?"* RM30 moved the supercharger-stats path off
`telemetry` (charge sessions are owned by `charging`), but **two other `gateway` reads of
`telemetry` remain**, and both are through the public port, not the DB:

- `internal/gateway/handlers/handlers.go` — the dashboard vehicle tiles read
  `telemetry.Reader.LatestSnapshotsBy` and map `telemetry.Snapshot` (battery %, range,
  status) into the vehicle cards.
- `internal/gateway/handlers/history.go` — `buildBatteryChart` reads
  `telemetry.Reader.SnapshotsByVehicleBetween` and buckets `telemetry.Snapshot` per day.

Roughly 24 `telemetry.Reader` and 64 `telemetry.Snapshot` references live in `gateway`.

**The RM30 position was that these are correct and should stay.** The principle that
actually explains MAG-30 is *"the gateway reads the module that OWNS the domain, through
its port, never its DB"* — not *"gateway may never import telemetry"*. `telemetry` owns
live vehicle snapshots; no other module does. Supercharger stats were misfiled because
`charging` owns charge sessions, not because `telemetry` is off-limits.

Picking this up therefore means answering a **design** question, not doing a mechanical
move: is there a read model that should own dashboard/history snapshot presentation
(e.g. an `analytics` projection), or is `telemetry.Reader` the right owner permanently? If
the answer is "it stays", the outcome is a one-paragraph rule in
`internal/gateway/AGENTS.md` and this item closes with no code change.

**TRIGGER — pick this up when** the owner wants the boundary rule settled in writing, or
when a third consumer of `telemetry.Snapshot` appears in `gateway` and the ad-hoc mapping
starts to duplicate.

### ORIGIN

Linear MAG-30 acceptance criterion 2, and the RM30 Step 2 interview (2026-08-27) — the
owner asked for a follow-up ticket rather than folding the work into RM30.



## 19. telemetry — Per-vehicle poll duration

### PROPOSAL

`poll_attempts` (grain: one row per vehicle per run) records the outcome of each vehicle's
poll but not **how long that vehicle took**. RM36 adds `duration_seconds` at the run level
only (`poll_runs`), which answers *"the poller took 40s"* but not *"which car ate 35 of
them"*.

The wake-up wait is the dominant and most variable cost of a cycle — an asleep vehicle is
polled for online up to `WakeTimeout` while an already-online one goes straight to
`VehicleData`. That difference is per vehicle, so run-level duration cannot expose it.

Add `duration_seconds` to `poll_attempts`, timed around the per-vehicle work in
`internal/telemetry`'s `record()` path. One migration, one module, no new table.

**TRIGGER — pick this up when** a run's duration is actually observed to be a problem
(RM36's `poll_runs` is what will show that), or when the wake timeout is next tuned.

### ORIGIN

`RM36-poll-run-tracking` decision **D3**, settled in the 2026-08-31 grill-me interview for
Linear MAG-35. The user chose run-level duration only, to keep the change to the ticket's
literal scope; per-vehicle was judged useful but deferred rather than discarded.


## 20. gateway — A read surface for `poll_runs`

### PROPOSAL

RM36 creates `poll_runs` (one row per poller invocation: duration, account and vehicle
outcome counts, charging counters, Tesla Fleet API call count) but **nothing reads it** —
the roadmap is write-only by design, so the data starts accumulating before anyone commits
to how it should be displayed.

Once rows exist, add a gateway surface: a poller-health page or a section on an existing
admin/settings page showing recent runs, their durations, failures by reason, and the API
call count per run — the last being the input to the Tesla API cost figure MAG-35 wanted.

Note `poll_runs` was designed for exactly this (RM36 D5 denormalizes the vehicle counts into
the row precisely so a run summary reads without a join), so the read side should need no
schema change.

**TRIGGER — pick this up when** enough nightly runs have accumulated to be worth looking at
(a few weeks), or as part of the Settings page (backlog item 5).

### ORIGIN

`RM36-poll-run-tracking` "Future work" — the roadmap ships persistence only. Recorded
2026-08-31 from Linear MAG-35.


## 21. charging — Renumber `20260720000001_require_location_kind.sql`; its `NOT NULL` was never applied

### PROPOSAL

`internal/charging/db/migrations/20260720000001_require_location_kind.sql` shares its
version number with
`internal/account/db/migrations/20260720000001_vehicles_add_access_type.sql`. Every module's
migrations share **one** `goose_db_version` table keyed by version, so goose recorded
`20260720000001` when it applied account's file and then **silently skipped** charging's,
reporting it as applied. `-allow-missing` does not help: a duplicate number is not an
ordering problem.

**Confirmed live on 2026-08-31**, not theoretical:

- `manual_charge_entries.location_kind` is still `is_nullable = YES` — the `NOT NULL` the
  migration exists to add was never applied.
- `goose_db_version` holds exactly one row for `20260720000001`, timestamped
  `2026-07-20 06:50:59`, which is account's application of its own file.
- The migration's defensive backfill (`NULL` → `'OTHER'`) also never ran.

**Currently harmless:** a `SELECT count(*) ... WHERE location_kind IS NULL` returns **0**, so
no row violates the intended constraint today — Go-side validation
(`charging.RequiredFieldsFor`) has been holding the line. The risk is that nothing in the
database enforces it, so any future write path that bypasses that validation can insert a
NULL silently.

**The fix** mirrors what MAG-35 did for `poll_runs`: renumber the charging file to a free
number (e.g. `20260720000002_require_location_kind.sql`), run `make migrate-up`, confirm
`is_nullable = NO`, then **delete `20260720000001` from `KNOWN_DUPLICATE_MIGRATIONS` in the
`Makefile`** so `make migration-guard` stops warning and starts failing on it. Check for
NULL rows before applying — the backfill will handle them, but you want to know.

**TRIGGER — pick this up** the next time `internal/charging` is touched for any reason, or
immediately if a NULL `location_kind` ever appears.

### ORIGIN

Found by the `app-reviewer`'s collision sweep during `RM36-app-record-poll-run` (tier 2,
Linear MAG-35) as review finding **F1**, after the identical failure mode was discovered in
`poll_runs`. Deferred to the backlog by the owner rather than widening RM36 into a third
module; the `make migration-guard` target added in the same change warns on this pair and
fails on any new one. Recorded 2026-08-31.
## 19. gateway — Regression test: the history preset selector highlights for a non-UTC user

### PROPOSAL

`RM35-gateway-adopt-clock` fixed a pre-existing bug in `buildHistoryPresets`
(`internal/gateway/handlers/history.go`): `Active` compared `start.Equal(pStart)` —
UTC-midnight-of-D from `time.Parse` against browser-zone-midnight-of-D from
`browserToday(c)`. `time.Time.Equal` compares instants, so the preset button could only ever
highlight when the browser's UTC offset was exactly zero. Every signed-in user whose
`browser_tz` cookie named a non-UTC zone saw a dead selector. The fix compares the formatted
`"2006-01-02"` date on both sides.

No NEW test was written asserting the fixed behaviour, because roadmap RM35 decision D6
limited tiers 2–6 to *repairing* existing tests, not adding any. The existing coverage
(`TestDashboardHistoryFragment_DefaultWindowActivatesSixDayPreset`) exercises only the
no-cookie path, which is exactly the case that accidentally worked before.

Work: add a test that sets a `browser_tz` cookie to a non-UTC zone (e.g. `America/Bogota`),
requests the default 6-day preset href, and asserts `btn-primary` is present. Consider a
second case for a POSITIVE-offset zone (e.g. `Europe/Madrid`), since the sign of the offset
decides which side of the frame mismatch is later.

**Trigger:** next change that touches `internal/gateway/handlers/history.go`, or any time
the no-tests default is relaxed for the gateway module.

### ORIGIN

`openspec/changes/RM35-gateway-adopt-clock/design.md` D-gw-7, "Follow-up not taken". Found
when the owner's suite run failed after the tier moved the no-cookie fallback off `time.UTC`.



## 20. clock / all modules — A compiler-checkable calendar-day type

### PROPOSAL

`RM35`'s `make tz-guard` catches the CARELESS reintroduction of a raw `now`, a hand-rolled
midnight, a 24h `Truncate`, or a hardcoded zone. It cannot catch the bug class that actually
cost RM35 four suite runs and four review rounds: **two `time.Time` values in different
frames** being compared, or used as map keys.

Six such sites were found in `RM35-gateway-adopt-clock` alone. Each fails ONLY between 00:00
and 05:00 UTC, so a green test run proves nothing about them; every one was found by reading
code. Two of the six were found only after the leader had explicitly cleared them as safe.
The root mechanism is that `time.Time` map-key equality compares the `Location` field, so a
Bogota-midnight value never matches a UTC-midnight key even for the same calendar day.

Work: give `internal/clock` a distinct calendar-day type instead of returning a bare
`time.Time` — e.g. `type Day struct { t time.Time }`, always UTC-midnight, with explicit
constructors (`clock.DayIn(t, loc)`) and an explicit `.Time()` escape. The compiler then
rejects mixing a wall-clock instant with a calendar day, which is the deterministic-signal
approach `CLAUDE.md` asks for: a type replaces a review round.

Scope is real — every `clock.CalendarDay` caller across `telemetry`, `analytics`, `app`,
`gateway` and `charging`. This is its own roadmap, not a follow-up commit.

**Trigger:** the next time a time-zone or calendar-day bug reaches review, or any change that
already touches `clock.CalendarDay`'s callers broadly.

### ORIGIN

`RM35-platform-add-tz-guard` design.md "Known blind spots" table, which states plainly that
the guard would not have caught any of tier 6's six bugs. Reinforced by review rounds 1-2 of
that tier (findings R1-R5) and by backlog entry 13, the unnormalized `map[time.Time]` lookup
that is the same mechanism seen from the other side.


## 21. app — `scheduler.go`'s `nowFn` default should be `clock.Now`

### PROPOSAL

`internal/app/scheduler.go:36` declares `nowFn := time.Now` as a testability seam. It carries
a `// tz:allow:` justification, and the seam itself is a pattern worth keeping — but the
DEFAULT it holds is the raw stdlib `time.Now`, not `clock.Now`.

`nextRun` immediately calls `.In(loc)`, so today this is behaviour-neutral. It is nonetheless
inconsistent with the archived `RM35-app-adopt-clock` tier's own D-app-2 reasoning, which
argued that migrating a now-source is strictly safer than leaving it to be rediscovered
later.

Work: change the default to `clock.Now`, keeping the seam. One line plus a comment.

**Trigger:** the next change touching `internal/app/scheduler.go`.

### ORIGIN

`RM35-platform-add-tz-guard` review round 2, finding R6. Raised by the reviewer as a
candidate follow-up while verifying R3; explicitly out of tier 7's sandbox because
`internal/app` is an archived tier's module.



## 22. analytics — Backfill the eight RM38 vehicle-status columns for historical rows

### PROPOSAL

`RM38-analytics-add-vehicle-status-columns` added eight nullable columns to
`vehicle_metrics` (`locked`, `sentry_mode`, `car_version`, `inside_temp_c`,
`outside_temp_c`, `charging_state`, `charge_limit_soc_pct`, `captured_at`) but
**deliberately did not backfill them** (RM38 D2, the owner's explicit choice for the
minimal migration). Every row written before that migration therefore holds NULL in all
eight, permanently.

Two consequences are live in production:

1. History-shaped consumers cannot read these fields for any pre-migration day. Only the
   dashboard's "latest row" read is unaffected, and only because the nightly job refreshes
   the latest row within one cycle.
2. **NULL on `sentry_mode` is ambiguous.** On `vehicle_snapshots` NULL means "the vehicle
   did not report sentry"; on `vehicle_metrics` it now means that *or* "row written before
   RM38". Nothing can distinguish the two, so any future feature that reasons about sentry
   history must treat all pre-migration rows as unknown.

Work: a data-only migration deleting the `vehicle_snapshots` watermark, exactly as
`20260822000002_reset_vehicle_metric_watermarks.sql` already does — `Recalculator.Reconcile`
treats an absent watermark as the epoch, so the next nightly run re-derives every vehicle's
whole history through the analytics module's own Go path. No cross-module SQL, no one-off
binary, no new Go code. This mechanism was proposed and declined during the RM38 interview;
it is recorded here because the decision was about scope, not about the mechanism, which is
proven.

**Trigger:** the first feature that reads any of the eight columns for a day other than the
latest one — a history chart, a sentry-history view, a lock-state report — or any change
that needs NULL on `sentry_mode` to be unambiguous.

### ORIGIN

`RM38-dashboard-vehicle-status-from-metrics` roadmap, decision **D2** (settled with the
owner before any artifact was written). Recorded per the roadmap's own "Future work"
section.



## 23. architecture — Per-module goose version table (retires `make migration-guard`)

### PROPOSAL

All four module migration directories currently share ONE `public.goose_db_version` table.
goose keys it by version **number**, so if two modules pick the same number, goose records
the number once and silently SKIPS the second file. `make migration-guard` exists purely to
catch that collision before it happens.

Giving each module its own version table — `goose -table <module>.goose_db_version`, one per
directory in `MIGRATIONS_DIRS` — makes the collision structurally impossible and lets
`migration-guard` (~25 Makefile lines) be deleted.

Deferred from RM39 because the two concerns are independent: version collisions come from
goose's numbering, not from where tables live, so moving tables into schemas does not fix
it. Folding it in would also have forced a `make db-reset` — goose would see an empty
version table and try to replay every migration against tables that already exist — which
would have cancelled RM39's main advantage of being reset-free.

**Trigger:** pick this up once RM39 has landed all four tiers, or sooner if a version
collision actually bites. Requires either a `make db-reset` (acceptable — the project is not
public) or a seeding migration that copies applied version rows from the shared table into
each per-module one.

**A SECOND symptom of the same root cause, observed directly on 2026-09-04:** the shared
table also breaks *rollback*. `goose -dir internal/<module>/db/migrations ... down` fails
unless that module's migration happens to be the **globally** newest, because goose looks up
the current version in the one shared table and finds a version belonging to another module.
Reproduced against a fully-migrated DB: `goose -dir internal/account/db/migrations ... down`
errored `migration 20260902000004: no current version found` — a version owned by
`internal/analytics`. So **`make migrate-down` is unreliable past the first directory**, and
verifying any module's Down migration currently needs a disposable container instead. The
per-module version table fixes this at the same time as the collision problem.

### ORIGIN

`RM39-schema-per-module` roadmap, decision **D4** (settled with the owner during the design
interview, before any artifact was written). The rollback symptom was found while verifying
RM42 tier 1's Down migration (review round 1, finding F1).



## 24. gateway — Settings reads hit the DB on every request (tracked as MAG-47)

### PROPOSAL

`handlers.LanguageMiddleware` (`internal/gateway/handlers/lang.go`) calls `acct.LanguageFor`
— a real DB query — on **every request from a signed-in user**. It reads the `lang` cookie
but treats the DB as authoritative anyway.

It is registered at `internal/gateway/gateway.go:108` with `r.Use`, **before** `r.Static` at
line 128. Gin applies global middleware to every route registered after it, and the session
cookie is `Path: /`, so `/static/app.css`, the fonts, the images and `/healthz` all pay that
query too.

The agreed fix: cookies become the per-request cache for `lang` and `theme`; the DB is read
only at login and on a cookie miss; writes go DB-first then cookie; and `/static` +
`/healthz` are excluded from the middleware entirely. localStorage was considered and
rejected — `i18n.T` renders text server-side, so localStorage (unreadable by the server,
reachable only after the HTML is parsed) cannot carry the language.

**Trigger:** RM42 tier 1 must land first — `account.settings` has to exist so one query
returns `language` and `theme` together.

**Filed in Linear as MAG-47**, blocked by MAG-43:
https://linear.app/magus-monitor/issue/MAG-47/cache-settings-in-cookies-stop-reading-the-db-on-every-request

### ORIGIN

Discovered while designing RM42 (MAG-43, theme selector). Scope split confirmed with the
owner on 2026-09-04: keep MAG-43 to the settings table + page, do the caching separately.


## 25. analytics — Log the recalculation window `Recalculate(start, end)` derives

### PROPOSAL

`internal/analytics` contains **zero** log statements. `Recalculator.Reconcile` derives a
`[start, end]` window from its three watermarks and hands it to `Recalculate`, and on a
successful run **nothing prints it**. The only place the window appears is inside an error
string (`recalculating [%s, %s]`, `recalculate.go`), so it is visible only when the call
already failed.

This is why MAG-48 ran unnoticed for months: the nightly log showed `gap reconciliation:`
with a *different, unrelated* trailing window, and a reader could reasonably mistake it for
the recalculation window. Nothing ever showed that `Recalculate` was handed 70 days instead
of 4.

The fix is small: one log line per vehicle carrying the derived `[start, end]`, plus the
per-source row counts that widened it (snapshots / supercharger sessions / manual entries),
using the existing `metrics reconciliation:` prefix. Roughly three lines, no new dependency.

**Why it is not in RM44:** RM44 instruments the *telemetry* side — the queries and their
filters, i.e. the **inputs**. That is enough to see the cause. This item logs the analytics
side's derived **output**, which is the thing a reader actually wants to compare against
"how many days should this have been". Both are useful; only the first was in the owner's
stated scope for MAG-48.

**Trigger:** pick up any time — it has no dependency on RM44's tiers. Cheapest immediately
after RM44 tier 1, while the logging conventions (`telemetry query:` prefix, `log.Printf`,
no `slog`) are fresh.

### ORIGIN

Found while analysing MAG-48 on 2026-09-05 (the "Observability gap" section of that ticket).
Raised with the owner, who scoped MAG-48's logging to the telemetry module and the Fleet API
calls; this analytics-side line was left out of that scope and deferred here.



## 26. telemetry / charging — Late-arriving write-once values are never saved

### PROPOSAL

Both Supercharger upserts read ~16 columns from Tesla but their `ON CONFLICT DO UPDATE SET`
clause refreshes only a few: 6 in `telemetry.UpsertSuperchargerHistory`, 5 in
`charging.MirrorSuperchargerSession`. Every other column is **write-once** — written at the
first INSERT and never refreshed. That is deliberate, and RM44 tiers 2 and 3 did not change it.

The gap: when Tesla sends a **later, better** value for one of those columns, we drop it. The
clearest case is `unlatch_date_time`, which is NULL in 2 of the 8 live rows today and is
exactly the field Tesla fills in once a session finalizes. `billing_type` and
`charge_stop_date_time` can plausibly finalize late too. A renamed `site_location_name` is the
same shape.

Picking this up means deciding, per column, whether "write-once at the source" is actually
true or was only ever an assumption. Any column moved into the `SET` clause must be moved OUT
of RM44's deny-list in the same change — the two lists are exact complements, and the
self-checking test added by RM44 (`information_schema` columns == SET-written ∪ deny-list)
fails loudly if they drift apart. That test is the reason this can be picked up safely later.

**TRIGGER to revisit:** a user reports a Supercharger session showing a blank unlatch time, a
stale site name, or a billing state that never settles.

### ORIGIN

RM44 tier 2/3 design gate, 2026-09-06. The leader found the issue while reviewing the
telemetry design: leaving write-once columns inside the change comparison made `updated_at`
advance every night forever, because the mismatch could never resolve. Verified on the live
PG16 dev database. The owner chose to fix only the comparison (roadmap D18) and keep today's
write behaviour, deferring the "should we save the late value" question to this entry.


## 27. account / gateway — Let the user change `analysis_start_date`

### PROPOSAL

RM49 stores `account.settings.analysis_start_date` and makes it read-only. It is set
once at signup, copied from `account.accounts.created_at`. The user cannot change it.

This item is to make it editable on the `/settings` page. The work is: one form field,
a `SetAnalysisStartDate` write port method on `account.Service`, its own validation
(not in the future, not before the account was created), and ES + EN catalogue entries.

**Trigger:** the owner wants to analyze charges from before signup, or wants to move
the start date forward to drop an early period of bad data.

### ORIGIN

RM49 decision D7. The owner chose read-only to keep the ticket small.



## 28. deployment — No way to roll a migration back in the deployed stack

### PROPOSAL

The deployed images cannot reverse a migration. `deploy/docker/Dockerfile` never installs
the `goose` CLI — the Dockerfile's own comment explains it cannot be built here, because
`go.sum` has no entries for the CLI's optional driver dependencies. And `cmd/migrate/main.go`
only calls `provider.Up(ctx)`; it has no down path at all.

So the only rollback today is manual SQL inside the `db` container: run the migration's own
Down statement by hand, then `DELETE FROM goose_db_version WHERE version_id = <version>` so
a later deploy re-applies it. That is error-prone and easy to half-finish.

The work is: add a `down` subcommand to `cmd/migrate` (goose's library already supports it,
so no new dependency), and a matching `docker compose run` invocation documented in
`docs/0-set-up/deployment.md`.

**Trigger:** the first time a migration must actually be rolled back in production, or
before any migration that is risky enough to want a tested reverse path.

### ORIGIN

RM49 tier 1 design.md D9. The worker was told to verify the roadmap's suggested rollback
command and found it did not exist. Fixing the gap was out of scope for that tier.


## 29. analytics — Monthly metrics table, once a second metric exists

### PROPOSAL

MAG-32 / RM52 measures effective pack capacity monthly, and that table lives in `charging`,
because `charging` owns every input row. The ticket also sketched a wider table —
`analytics.vehicle_monthly_metrics`, keyed on `(tesla_id, effective_period)`, one column per
metric — to hold future monthly numbers: `full_charge_count`, average consumption per 100 km,
energy consumed per date, and comparison results across vehicles.

That table was deliberately NOT built in RM52 (roadmap decision RD12). Today it would hold one
column, copied from `charging`, that nothing reads.

**Trigger:** the first monthly metric that is NOT owned by a single module — a fleet comparison,
or a number derived from `telemetry` plus `charging` together. That is the point at which
`analytics` is the right owner rather than an extra hop.

When it is built it may duplicate `charging`'s capacity value into itself. That is allowed:
`analytics.vehicle_metrics` already duplicates other modules' observations on purpose, and the
read-heavy Performance-Profile accepts denormalization on the write path. It needs a refresh rule,
which `analytics`'s existing watermark/reconcile machinery already provides.

### ORIGIN

RM52 decision RD12, settled with the owner on 2026-09-10 during the MAG-32 design interview.


## 30. charging — Make `charge_sessions.tesla_id` NOT NULL

### PROPOSAL

`charge_sessions.tesla_id` is nullable today — "NULL when the VIN is not a currently-registered
vehicle" — and the nightly mirror refreshes it on every pass. `manual_charge_entries.tesla_id` is
already `NOT NULL`, so the two sibling tables disagree, and `Session.TeslaID` is a `*int64` while
`Entry.TeslaID` is a plain `int64`.

The owner's position is that on this platform `tesla_id` is always present and the column should
be `NOT NULL`.

This is a behaviour change, not a schema tidy-up, so it must first answer three questions:

1. How many rows have `tesla_id IS NULL` today? (Count before writing the migration.)
2. What does the nightly mirror write when a VIN stops being registered, if not NULL? Skipping the
   row, keeping the last known id, or refusing the sync are all different products.
3. `Session.TeslaID *int64` becomes `int64`, which touches every reader of that field.

**Trigger:** picked up on its own. RM52 does not need it — its new table declares its own
`tesla_id BIGINT NOT NULL` and simply skips session rows where the column is NULL.

**Care required:** the dev database holds hand-entered charge history that Tesla cannot backfill.
Count the affected rows and agree what happens to them before any `ALTER`.

### ORIGIN

RM52 decision RD13, raised at the MAG-32 database design gate on 2026-09-10 when the owner asked
for `tesla_id` instead of `vin` as the new table's key.



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
