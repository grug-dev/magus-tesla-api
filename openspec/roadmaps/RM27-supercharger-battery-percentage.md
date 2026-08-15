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
| **R2** | `telemetry` stores only the **human-owned trio** — `start_battery_pct`, `end_battery_pct`, `battery_pct_source`. | These are the user's override/verification channel, not model output. |
| **R3** | The trio is **never auto-written**: excluded from `UpsertSuperchargerSession`'s `ON CONFLICT DO UPDATE SET`. | The poller re-upserts nightly (billing mutates post-session). Including them would silently overwrite the user's verified values on the next poll — the concrete bug this design exists to prevent. |
| **R4** | The estimator lives in **`internal/battery`** and computes **on read**; no `_est` columns are persisted. | `battery → telemetry` is one-directional; telemetry calling the estimator would be an **import cycle**. battery is the derived-metrics module and already owns `packCapacityKWh` and consumes `telemetry.SuperchargerReader`. |
| **R5** | Reads serve **`verified ?? estimated`**. | Estimates are visible from day one; the human trio overrides when present. |
| **R6** | No `_est` persistence. The estimate is a pure function of stored data (`energy_kwh`, timestamps, `car_type`). | A persisted estimate goes stale the moment the taper model improves; recomputing always reflects the current model — which also makes drift analysis (`est` vs `verified`) *more* correct, not less. |
| **R7** | The **verification UI is out of scope** — follow-up ticket. | Keeps RM27 to two modules; the feature still ships visible value (estimates on the Supercharger stats page) without it. |

### Rejected alternatives

- **Persist all 5 columns (`_est` pair written nightly)** — the user's initial shape.
  Rejected on R4/R6: it requires either an import cycle, relocating `packCapacityKWh` out
  of the module that owns it, or business logic in `cmd/poller` (which `CLAUDE.md` requires
  stay thin). And the persisted estimate goes stale on model change.
- **Two nullable columns, populated by nothing** — ships dead columns; no user value.
- **Change collection strategy (poll `vehicle_data` during active sessions)** — the only
  route to *measured* rather than solved values, but wakes the car on paid API calls and
  can never recover history. Logged to the backlog as the trigger to revisit.
- **Close MAG-14 as not-feasible** — rejected once duration was recognised as the second
  equation; the values are genuinely solvable.

## Tiers

| Status | Change | Module | Scope | depends_on | Proposal prompt |
|---|---|---|---|---|---|
| `[~]` | `RM27-telemetry-add-supercharger-battery-pct` | `telemetry` | Migration adding `start_battery_pct SMALLINT NULL CHECK (0..100)`, `end_battery_pct SMALLINT NULL CHECK (0..100)`, `battery_pct_source TEXT` to `supercharger_sessions`. Columns **excluded** from the `ON CONFLICT DO UPDATE SET` in `UpsertSuperchargerSession` (R3). Extend `SuperchargerReader`'s row type so battery can read them. No index (see design). | — | Add the human-owned battery-percentage trio to `supercharger_sessions`, insert-only and protected from the nightly upsert per R2/R3. Mirror the `_pct` suffix and `SMALLINT CHECK` shape already used by `manual_charge_entries`. |
| `[ ]` | `RM27-battery-estimate-supercharger-soc` | `battery` | Taper-curve estimator solving `(start, end)` from `energy_kwh` + duration + `capacityFor(car_type)`. Serves `verified ?? estimated` through the `battery` read port. Handles unknown `car_type`, unsolvable and out-of-range results. | `RM27-telemetry-add-supercharger-battery-pct` | Implement the SOC estimator in the derived-metrics module per R1/R4/R5/R6, computing on read with no persistence. |

**Legend:** `[ ]` pending (change not created) · `[~]` in progress (change created, not archived) · `[x]` done (archived).

## Future work

Deferred items are recorded in [`backlog.md`](backlog.md) — see entry **11** (Supercharger
start/end SOC verification UI + the measured-SOC polling alternative). Estimator accuracy is
additionally bounded by backlog entry **7** (trim-exact pack capacity): `capacityFor` is
model-coarse, so every `model3` trim shares one capacity constant.
