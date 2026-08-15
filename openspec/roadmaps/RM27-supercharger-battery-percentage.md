# RM27 — Supercharger start/end battery percentage

Source ticket: MAG-14 — https://linear.app/magus-monitor/issue/MAG-14/startend-battery-percentage

## Problem

MAG-14 asked for `start_battery_pct` / `end_battery_pct` on `supercharger_sessions`,
derived "based on the API response".

**The premise is disproven.** The `GET /api/1/dx/charging/history` payload carries no
state-of-charge field of any kind. Verified against the real captured response
(`cmd/explore-tesla-api/output/2-ChargingHistory.json`, 4 sessions): every key in the
payload is already mapped by `ChargingSessionTesla` — `sessionId`, `vin`,
`siteLocationName`, the three timestamps, `countryCode`, `fees[]`, `billingType`,
`invoices[]`, `vehicleMakeType`. Nothing unmapped, no SOC, no range, no odometer. The
existing migration already documented this ("no battery percentage",
`20260716000001_add_supercharger_sessions.sql:53`).

**But the values are recoverable by solving, not by reading.** Each session yields two
independent equations in two unknowns:

1. `end − start = energy_kwh / pack_kwh × 100`  (energy, from `fees[].usage*` where `uom = 'kwh'`)
2. `duration = ∫ₛᵉ (pack_kwh/100) / P(soc) · d(soc)`  (time, from `chargeStartDateTime` → `chargeStopDateTime`)

where `P(soc)` is the DC charge taper curve. Two equations, two unknowns ⇒ a unique
`(start, end)`. The observed data supports this strongly — the same car at the same site
averaged **127 kW** over 16.6 min in one session and **19 kW** over 54.3 min in the next,
a spread only explicable by where in the SOC band each charge sat.

`pack_kwh` is already owned by `internal/battery` (`capacity.go`, `car_type` → usable kWh).

## Decisions (binding — settled with the user via grill-me, 2026-08-15)

| ID | Decision | Rationale |
|---|---|---|
| **R1** | Values are **estimated by solving**, never read from the API. | No SOC field exists in the payload (verified against the captured response). |
| **R2** | `telemetry` stores the **human-owned trio** — `start_battery_pct`, `end_battery_pct`, `battery_pct_source` — plus a **frozen estimate snapshot** `start_battery_pct_est` / `end_battery_pct_est` (R8). | The trio is the user's override/verification channel, not model output; the snapshot is the drift log. |
| **R3** | The trio is **never auto-written**: excluded from `UpsertSuperchargerSession`'s `ON CONFLICT DO UPDATE SET`. | The poller re-upserts nightly (billing mutates post-session). Including them would silently overwrite the user's verified values on the next poll — the concrete bug this design exists to prevent. |
| **R4** | The estimator lives in **`internal/battery`** and computes **on read**; no `_est` columns are persisted. | `battery → telemetry` is one-directional; telemetry calling the estimator would be an **import cycle**. battery is the derived-metrics module and already owns `packCapacityKWh` and consumes `telemetry.SuperchargerReader`. |
| **R5** | Reads serve **`verified ?? estimated`**. | Estimates are visible from day one; the human trio overrides when present. |
| **R6** | No `_est` persistence. The estimate is a pure function of stored data (`energy_kwh`, timestamps, `car_type`). | A persisted estimate goes stale the moment the taper model improves; recomputing always reflects the current model — which also makes drift analysis (`est` vs `verified`) *more* correct, not less. |
| **R7** | The **edit/verify form is out of scope** — follow-up ticket. RM27 ships the estimates **read-only** (tier 3). | Keeps the write path, CSRF/tenant checks and the input-shape questions out of this roadmap, while still putting a visible number on screen. **Corrected 2026-08-15:** the original rationale claimed the estimates would be visible without any gateway work. That was wrong — see R9. |
| **R9** | RM27 includes a **third tier wiring `gateway → battery`**: `cmd/web` constructs `battery.NewReader`, `gateway.Deps` gains a `BatteryReader battery.Reader`, and the Supercharger Stats page reads the estimated start/end % from it. | Without this the feature renders nothing. `internal/battery` is currently imported by **no package at all** (its own `AGENTS.md`: "not yet wired"), and the Supercharger Stats page calls `telemetry.SuperchargerReader` **directly** from `cmd/web`. Tiers 1+2 alone would compute estimates that never reach a screen. This is the roadmap's only new module edge. |
| **R8** | `start_battery_pct_est` / `end_battery_pct_est` store a **frozen snapshot of the estimate as displayed at the moment a human verified**, written once by the future verification UI in the same write as the trio, never refreshed, NULL for unverified sessions, and excluded from the nightly upsert exactly like the trio. | The user wants a permanent drift log (model said 82, human said 79). This does **not** contradict R6: R6 forbids a column that silently goes stale while pretending to be current, whereas a dated observation is *supposed* to never change. It is also the only shape with a **legal writer** — the gateway reads `battery` and writes through a telemetry port (`gateway → battery`, `gateway → telemetry`, no cycle), whereas a nightly-refreshed `_est` has none: `battery` may not write into telemetry's table, and telemetry computing it is the import cycle. Tier 2 never reads these columns as a cache. |

### Rejected alternatives

- **`_est` pair refreshed nightly by the poller** — the user's initial shape. Rejected on
  R4/R6: it has **no legal writer** (it needs either an import cycle, relocating
  `packCapacityKWh` out of the module that owns it, or business logic in `cmd/poller`, which
  `CLAUDE.md` requires stay thin), and the persisted estimate goes stale on model change while
  still presenting itself as current. Note the *columns* were later adopted under **R8** with
  different semantics — written once at verification, frozen, never refreshed — which is what
  gives them both a legal writer and a coherent meaning.
- **A separate verification-audit table** instead of the `_est` snapshot columns — cleaner
  separation and it would preserve history across repeated re-verifications of one session, but
  a second table plus a second query for a low-volume log. Revisit if re-verification history
  turns out to matter.
- **Two nullable columns, populated by nothing** — ships dead columns; no user value.
- **Change collection strategy (poll `vehicle_data` during active sessions)** — the only
  route to *measured* rather than solved values, but wakes the car on paid API calls and
  can never recover history. Logged to the backlog as the trigger to revisit.
- **Close MAG-14 as not-feasible** — rejected once duration was recognised as the second
  equation; the values are genuinely solvable.

## Tiers

| Status | Change | Module | Scope | depends_on | Proposal prompt |
|---|---|---|---|---|---|
| `[x]` | `RM27-telemetry-add-supercharger-battery-pct` | `telemetry` | Migration adding five nullable columns to `supercharger_sessions`: the human trio `start_battery_pct` / `end_battery_pct` (`SMALLINT CHECK 0..100`) + `battery_pct_source` (`TEXT CHECK IN ('user_verified','polled')`), and the frozen snapshot pair `start_battery_pct_est` / `end_battery_pct_est` (`SMALLINT CHECK 0..100`, R8). All five **omitted entirely** from `UpsertSuperchargerSession` — both the `INSERT` list and the `ON CONFLICT DO UPDATE SET` (R3). Extend `SuperchargerReader`'s row type so battery can read them. No new index (see design). | — | Add the human-owned battery-percentage trio plus the frozen estimate-snapshot pair to `supercharger_sessions`, protected from the nightly upsert per R2/R3/R8. Mirror the `_pct` suffix and `SMALLINT CHECK` shape already used by `manual_charge_entries`. |
| `[ ]` | `RM27-battery-estimate-supercharger-soc` | `battery` | Taper-curve estimator solving `(start, end)` from `energy_kwh` + duration + `capacityFor(car_type)`. **Adds a method to the existing `battery.Reader` port** (today it exposes only `RecentEfficiency`) serving `verified ?? estimated`. Handles unknown `car_type`, unsolvable and out-of-range results — mirror the existing `Efficiency.Approximate` precedent (design D1b), which already flags "pack capacity unknown" rather than fabricating a number. | `RM27-telemetry-add-supercharger-battery-pct` | Implement the SOC estimator in the derived-metrics module per R1/R4/R5/R6, computing on read with no persistence, exposed as a new method on the `battery.Reader` port. |
| `[ ]` | `RM27-gateway-show-supercharger-soc` | `gateway` | Wire the module that nothing currently imports: construct `battery.NewReader` in `cmd/web`, add `BatteryReader battery.Reader` to `gateway.Deps`, and render estimated start/end % on the Supercharger Stats page (`templates/fragments/supercharger_*`). **Read-only — no edit form.** Bilingual ES/EN labels via `i18n.T`, `ui/` kit components, and a clear visual marking that the values are estimates. | `RM27-battery-estimate-supercharger-soc` | Surface the estimated start/end battery % on the Supercharger Stats page per R9, read-only, wiring `gateway → battery` for the first time. |

**Legend:** `[ ]` pending (change not created) · `[~]` in progress (change created, not archived) · `[x]` done (archived).

## Dependency flow

**When the estimate is computed: on read, per request — never on a schedule.** The nightly
poller writes only the raw session (`energy_kwh`, the timestamps, fees, billing state) and
computes nothing. There is no batch step, no post-poll job, and nothing persisted (R6).

```
WRITE (nightly, cmd/poller)
  tesla ──> telemetry.Collector ──> supercharger_sessions
                                      raw fields only; the five new columns are
                                      untouched by this path entirely (R3/R8)

READ (per request, cmd/web)
  gateway ──> battery.Reader ──> telemetry.SuperchargerReader ──> supercharger_sessions
              │                                                     │
              │ capacityFor(car_type) + taper solve                 │ verified values,
              │ = estimated (start, end)                            │ when a human set them
              └──────────── verified ?? estimated ──────────────────┘
```

**New module edges introduced by RM27: exactly one — `gateway → battery` (tier 3).**
`battery → telemetry` already exists (`derive.go`, `reader.go`), so tiers 1 and 2 add no edge
at all: tier 1 adds fields to a struct battery already reads, tier 2 adds logic inside battery
over an edge that is already there. The `gateway → telemetry` edge also already exists and
stays — the Supercharger Stats page keeps reading telemetry for the session rows themselves.

No cycle is created: `gateway → battery → telemetry` is a straight line. The reverse direction
is what the design had to avoid — telemetry computing the estimate itself would require
`telemetry → battery`, inverting the existing `battery → telemetry` edge (R4).

## Future work

Deferred items are recorded in [`backlog.md`](backlog.md) — see entry **11** (Supercharger
start/end SOC verification UI + the measured-SOC polling alternative). Estimator accuracy is
additionally bounded by backlog entry **7** (trim-exact pack capacity): `capacityFor` is
model-coarse, so every `model3` trim shares one capacity constant.
