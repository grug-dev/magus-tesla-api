# RM28 — Battery Consumed Graph

Source ticket: MAG-15 — https://linear.app/magus-monitor/issue/MAG-15/battery-consumed-graph

## Problem

The dashboard's `/ui/dashboard/history` page plots odometer km/day and battery %/day from
`vehicle_snapshots`' derived columns. Neither answers the question the owner actually wants
answered: **how much battery did the car consume that day?**

`battery_used_pct_calc` alone cannot answer it. It is a raw difference between two
consecutive snapshots (`prev.battery_level_pct − cur.battery_level_pct`), so any day the
vehicle was charged produces a meaningless or negative number — the battery went *up*
overnight while the car was also driven.

The correction requires the charge records the platform already stores: `manual_charge_entries`
(`internal/manualcharge`) and `supercharger_sessions` (`internal/telemetry`). Both carry
`start_battery_pct` / `end_battery_pct`. Adding back what a charge put in recovers what driving
actually took out.

**The formula simplifies, and the simple form is the one that generalizes.** The ticket states
it as `(yesterday − start) + (end − today)`, which is algebraically identical to:

```
battery_consumed_pct = battery_used_pct_calc + Σ(end_battery_pct − start_battery_pct)
```

Verified against the ticket's own worked example (snapshots 22 → 73, charge 18 → 80):
`−51 + 62 = 11` ✓. The Σ form is **required**, not merely tidier: the two-term version silently
assumes exactly one charge event per day. See decision **D13**.

**A day whose numbers do not add up is itself a signal.** When the corrected figure comes out
negative — or zero while the car demonstrably drove — a charge record is missing or incomplete.
That is recorded durably so a later ticket can notify the user.

## Decisions (binding — settled with the owner via grill-me, 2026-08-15)

| ID | Decision | Rationale |
|---|---|---|
| **D1** | **No attribution change is needed — `EffectiveDate` already solves it.** Consumption is read through `telemetry.Snapshot.EffectiveDate`, never through `CapturedDate`. The five derived columns stay exactly where they are, on the row that carries them today. | **Corrected 2026-08-15, after the tier-1 artifacts were written.** The original D1 called for shifting the five derived columns onto the predecessor row via a migration, a backfill, and a two-write poll transaction. That premise was false. `mapping.go:110` derives `EffectiveDate = CapturedAt.AddDate(0, 0, -1)` on read — "the calendar day this snapshot REPRESENTS, not the day it was read". The row captured Aug 14 03:30 carries `battery_used_pct_calc = 22 − 73 = −51`, the delta over Aug 13 03:30 → Aug 14 03:30, and its `EffectiveDate` is **Aug 13**. Value and label already agree, so the consumption is already filed under the day it happened. The gateway's existing charts already bucket this way via `effectiveDayUTC` (`handlers/history.go:62`). The whole attribution tier was dropped; see Rejected alternatives. |
| **D2** | `battery_consumed_pct` is **recomputed on read**, never stored. No cache table. | The window is capped at 90 days for one account + one vehicle (`handlers/history_test.go:828`) — roughly 90 snapshot rows plus a handful of charge rows. Recomputing also makes the ticket's own item-7 worry vanish: a user entering a long-past manual entry needs no resync, no invalidation, no refresh hook. There is nothing to invalidate. The read-heavy `Performance-Profile` *permits* denormalizing; it does not oblige it when the read is already trivial. |
| **D3** | The **missing-charge table is owned by `internal/telemetry`**; `internal/battery` writes to it through a new telemetry write port. | Owner's call, against the assistant's recommendation that battery own it. Reuses telemetry's existing persistence and keeps `internal/battery` free of a `db/` folder. No import cycle: `battery → telemetry` already flows one way (root `README.md` §Dependency graph, LAYER 2). |
| **D4** | **Detection runs in the nightly poller** (`cmd/poller`), after snapshots are written — not on the dashboard read path. | The table exists to notify the user *later*. If it were only written when someone opened the dashboard, a notification could only ever report gaps the user had already seen on screen. Also keeps GET requests write-free, matching the read-heavy profile. |
| **D4a** | `cmd/poller` — the **composition root** — orchestrates: it calls the battery derivation and passes the result to telemetry's gap writer. Telemetry never calls battery. | Telemetry calling battery would be the import cycle RM27's R4 identified. `cmd/` is explicitly allowed to know every concrete type (`ai/architecture.md`, root `README.md`), so orchestrating there is the sanctioned resolution. |
| **D5** | **Detection rule:** flag a day when `battery_consumed_pct < 0`, OR when it is `0` while `distance_traveled_km_calc > 10`. The `10` is a **named Go constant** with a comment, not a literal. | The owner's rule as specified. The zero-plus-distance clause catches the silent case where a charge exactly cancels the day's usage. |
| **D5a** | Days where `battery_used_pct_calc` is NULL (the vehicle's first-ever snapshot — `deriveConsumption(nil, cur)` returns all five nil) are **skipped, never flagged**. | No predecessor means no claim either way; flagging would be a fabricated finding. |
| **D6** | Calendar-day bucketing uses the **poller's configured zone** (`Config.Location`, currently resolving to `America/Bogota`). | The same zone `captured_date` is already computed in (`20260805000001_dedupe_vehicle_snapshots_daily.sql`, "derive in Go, not SQL"). Charges and snapshots must bucket by identical rules or the formula breaks at the edges. |
| **D7** | **Missing-charge table shape:** `UNIQUE (account_id, tesla_id, date)` — one row per vehicle-day. Columns `vin`, `tesla_id`, `date`, `missing_charging_type` (`MANUAL` \| `SUPERCHARGER`), all `NOT NULL`. | One aggregate discrepancy per day means one honest row. A per-type key would let a single date carry both a MANUAL and a SUPERCHARGER row, but we cannot actually tell those apart from one combined shortfall — the second row would be speculative. `tesla_id` is `NOT NULL` here even though it is nullable on `supercharger_sessions`: a session we cannot attribute to a registered vehicle is skipped by detection entirely. |
| **D7a** | `missing_charging_type` is **inferred**: `SUPERCHARGER` when a session exists that date with NULL percentages (we know exactly which record needs filling), otherwise `MANUAL` (the vehicle was charged somewhere the Fleet API does not report). | Directly from the ticket's own reasoning. |
| **D7b** | **Lifecycle:** each nightly run upserts days that flag and **deletes** days that no longer flag, within the window it recomputed. No `resolved_at`, no soft delete. | Fixing a charge entry clears the row automatically on the next run, with no extra wiring. The consuming notification wants "what is outstanding", not an audit trail of resolved gaps. |
| **D8** | **Multi-day spans** (`days_spanned_calc > 1`, i.e. a missed poll) plot a single bar on the **start day**, visually flagged, with the intervening days rendered as no-data. Charges inside the span are summed regardless. | Splitting the delta evenly across the span would invent a per-day distribution never observed; excluding it would silently drop real consumption. |
| **D9** | **Add two date-range read ports** (additive — existing methods stay): `telemetry.SuperchargerSessionsByVehicleBetween` and `manualcharge.ListEntriesByVehicleBetween`, each taking `from, to time.Time`. `telemetry.SnapshotsByVehicleBetween` **already exists** (`telemetry.go:282`) and is reused as-is. | A date-filtered graph asking for a date range is the honest contract. The two remaining reads are limit-based, and with a limit there is no safe number to guess — sessions per 90 days is unbounded, so a heavy month would silently truncate the window. **Corrected 2026-08-15:** the original D9 listed three ports to add; the snapshot one was already built and is already called by the gateway (`handlers/history.go:263`), windowing on `EffectiveDate ∈ [start, end]`. |
| **D9a** | The snapshot fetch spans **`[start−1, end]`** — a one-day **lookback**, not a look-ahead. | Day `D`'s stored delta is computed from `D`'s row and its **predecessor**, so bounding day `D`'s interval (needed by **D12**) requires the `D−1` row's `captured_at`. This is already the established gateway pattern: `readStart = start - 1 day` (`handlers/history.go:261-263`), and the port documents the lookback as a caller concern. **Corrected 2026-08-15** — the original D9a said `[start, end+1]`, which was the shape the abandoned store-shift design implied. |
| **D10** | **A flagged day renders as a zero-height bar with a distinct warning marker** — never the raw negative value, never omitted. Label bilingual ES/EN via `i18n.T`. | Plotting a number we do not believe reads as a bug; hiding the day makes it indistinguishable from having no data, so the user never learns there is something to fix. |
| **D11** | `parseHistoryRange`'s **default window and its `end <= today` cap both move to yesterday**, matching the presets. | Today's `EffectiveDate` row is not captured until tomorrow's ≈03:30 poll, so `end = today` can only ever produce an empty trailing bar. `handlers/history.go:217` already forces every **preset** to end at yesterday for exactly this reason; only the no-parameter default and the cap still allow `today`. This reconciles them. (**Rationale corrected 2026-08-15** — previously justified by the abandoned store-shift; the conclusion is unchanged, and it was already the gateway's own reasoning.) |
| **D12** | **Charge-to-day matching is source-specific.** A Supercharger session belongs to day `D` when `charge_stop_date_time` falls between `snapshot[D].captured_at` and `snapshot[D+1].captured_at`. Manual entries match on `charged_on` (date). | A session ending 01:00 on Aug 14 belongs to Aug 13's 03:00→03:00 window but has stop-*date* Aug 14 — pure date bucketing files it under the wrong day, producing a false gap flag on one day and an inflated bar on the next. Battery already holds both snapshots, so the interval test is free. Manual entries carry only a date, and the user picked it deliberately. This makes **D6** irrelevant for Supercharger sessions; it still governs manual entries. |
| **D13** | **The formula sums over ALL charge events** in the window, across **both** tables together: `battery_consumed_pct = battery_used_pct_calc + Σ(end_battery_pct − start_battery_pct)`. Neither source takes precedence. | The owner's initial "take only the latest `charge_stop_date_time` per day" was corrected during the interview. With two sessions on Aug 13 (20→50, 60→80) and snapshots 30 → 75: summing gives **5** ✓; latest-only gives **−25** ✗ (discards the earlier session and produces a false missing-record flag); first-start-to-last-end gives **15** ✗ (over-counts, treating the driving *between* sessions as charging). Latest-stop-time remains correct for *which day a session belongs to* — just not for *which sessions to count*. `internal/battery` already sums both sources this way (`sumSuperchargerKWh`, `sumManualKWh`). |
| **D14** | **Supercharger start/end percentages are always NULL today, and that is accepted.** No entry UI, no kWh-based estimation in RM28. Those days flag as `SUPERCHARGER` gaps. | MAG-14 / RM27 shipped the five columns as storage only; the Fleet API carries no state-of-charge, and the taper-solve estimator was explicitly descoped as disproportionate (RM27 D11, backlog entry 11). Reviving either here is out of scope. |
| **D14a** | **KNOWN LIMITATION — the detection rule has a blind spot, and it under-reports rather than over-reports.** `charge_gaps` catches only days where the corrected figure goes **negative**, or sits at **zero with `distance_traveled_km_calc > 10`** (**D5**). A charge that only *partially* offsets a day's driving leaves a **positive, plausible-looking, silently understated** bar that nothing flags. Example: drove ~300 km (true consumption 50%), supercharged +30% with NULL percentages ⇒ `consumed = 20`, not flagged, and the bar reads 20 instead of 50. | Inherent to the rule, not fixable in code: with the percentages NULL there is no quantity to add back, and no way to tell an understated day from a genuinely frugal one. Recorded so (a) nobody reads a low Supercharger-day bar as fact, and (b) the future notification consumer knows `charge_gaps` is a **lower bound** on days needing attention, never the complete set. The only real fix is being able to enter the percentages — backlog entry 11 item 1. |
| **D15** | `internal/battery` owns the **derivation**, not the storage. | It is the platform's derived-metrics module and already composes exactly the four dependencies this needs — `telemetry.Reader`, `telemetry.SuperchargerReader`, `manualcharge.Reader`, `account` — via `NewReader`. A new `internal/consumption` module was considered and rejected: it would have duplicated battery's purpose and its entire dependency graph. |

### Rejected alternatives

- **A precomputed `battery_consumed` table** refreshed by the nightly job and on manual-entry
  writes (the ticket's third option). Rejected on **D2**: it buys no measurable read win on a
  ~90-row computation, and it converts the owner's item-7 question into a permanent cache-
  invalidation obligation that every future writer of charge data must remember. Note the
  distinction the interview drew and the design must preserve — the missing-charge table is
  durable **output**; a consumed-pct table would be a **cache**. "We need a table" applies to
  the first and not the second.
- **A new `internal/consumption` module.** Rejected on **D15** — `internal/battery` already is
  that module.
- **Shifting the five derived columns onto the predecessor row** — a migration, a one-time
  backfill, and a two-write poll transaction (row `N` inserted with NULLs, row `N−1` updated in
  the same transaction). This was RM28's original tier 1 and was **dropped on 2026-08-15 before
  any code was written**, once `EffectiveDate` was found to already provide the attribution
  (**D1**). Do not re-propose it: it buys no behavioural change, because every reader reaches
  snapshots through a port that already carries `EffectiveDate`, while costing a backfill plus a
  write path that must recompute the predecessor on *every* poll to preserve the re-poll
  self-healing the current single-statement upsert gets for free.
- **Shifting the attribution at read time in the gateway view model.** Moot for the same reason —
  the shift already happens, one layer lower, in `telemetry`'s own mapper, so the gateway is
  handed correctly-dated data and needs no date arithmetic of its own.

### Future work (see [`backlog.md`](backlog.md))

- **Charging data is split across two modules** — `manual_charge_entries` in
  `internal/manualcharge`, `supercharger_sessions` in `internal/telemetry` — so every consumer
  of "how was this car charged" must compose two ports. The owner raised this during the
  interview and explicitly scoped it **out** of RM28. Recorded as a backlog entry.
- The Supercharger start/end % **verification UI** remains backlog entry 11 item 1. RM28 makes
  it more valuable — until it lands, every Supercharger day flags as a gap.

## Tiers

Status legend: `[ ]` pending (change not created) · `[~]` in progress (change created, not
archived) · `[x]` done (archived).

| Status | Change | Module | Scope | depends_on | Proposal prompt |
|---|---|---|---|---|---|
| `[x]` | `RM28-telemetry-add-charge-gap-storage` | `telemetry` | The missing-charge table (**D3**, **D7**, **D7a**, **D7b**) + its write port, plus one date-range read port (**D9**): `SuperchargerSessionsByVehicleBetween`. | — | Create the missing-charge table per D7 — `UNIQUE (account_id, tesla_id, date)`, columns `vin`/`tesla_id`/`date`/`missing_charging_type` all NOT NULL — and a write port supporting upsert-and-delete reconciliation per D7b. Add `SuperchargerSessionsByVehicleBetween` per D9, filtering on `charge_stop_date_time` (D12); the existing limit-based methods stay unchanged. Note `SnapshotsByVehicleBetween` already exists — reuse it, do not add another. `database` design gate applies: design.md must carry the schema, the rationale, and the index plan. |
| `[x]` | `RM28-manualcharge-add-date-range-reader` | `manualcharge` | `ListEntriesByVehicleBetween(ctx, accountID, teslaID, from, to)` (**D9**), filtering on `charged_on`. Additive. | — | Add one date-range read method to `manualcharge.Reader` per D9, filtering on `charged_on` inclusive of both bounds. Purely additive — `ListEntriesByVehicle` and `ListEntriesByAccount` are unchanged. No schema change. |
| `[ ]` | `RM28-battery-derive-consumed-per-day` | `battery` | The derivation (**D13**) and gap detection (**D5**, **D5a**, **D7a**), the charge-matching rules (**D12**), multi-day span handling (**D8**), the `[start−1, end]` lookback fetch (**D9a**), a `ConsumedByDay` port, and the `cmd/poller` wiring (**D4**, **D4a**). | T1, T2 | Implement the per-day consumed derivation in `internal/battery` per D13, keyed on `telemetry.Snapshot.EffectiveDate` — the stored `battery_used_pct_calc` on an `EffectiveDate = D` row already describes day D (**D1**); do NOT re-derive or shift it. Match charges to days per D12 — Supercharger by snapshot interval, manual by `charged_on`. Detect gaps per D5/D5a and infer the type per D7a. Fetch snapshots over `[start−1, end]` per D9a via the existing `SnapshotsByVehicleBetween`. Handle `days_spanned_calc > 1` per D8. Expose a `ConsumedByDay` read port for the gateway. Wire detection into `cmd/poller` per D4/D4a — the composition root orchestrates; telemetry never calls battery. Update `internal/battery/AGENTS.md`. |
| `[ ]` | `RM28-gateway-add-consumed-graph` | `gateway` | The bar graph mirroring the existing odometer/battery charts, flagged-day rendering (**D10**), and the default-window reconciliation (**D11**). | T3 | Add the battery-consumed bar chart to `/ui/dashboard/history`, mirroring `templates/fragments/history.templ` + `history_vm.go`. Consume `battery.ConsumedByDay` through the port — never a database. Render flagged days per D10 (zero-height bar, distinct warning marker) and multi-day spans per D8. Move `parseHistoryRange`'s default and its `end <= today` cap to yesterday per D11. Every new label bilingual ES/EN through `i18n.T` against `internal/gateway/i18n/catalog.go`, both non-empty. |

**Tier order.** T1 and T2 are mutually independent and could run in either order. T3 needs both
ports. T4 needs T3.

**Dropped tier.** RM28 originally opened with `RM28-telemetry-shift-consumption-attribution`, a
migration + backfill + two-write poll path to move the five derived columns onto the predecessor
row. It was dropped on 2026-08-15, before any implementation, once `EffectiveDate` was found to
already provide that attribution — see **D1** and Rejected alternatives. Its artifacts were
deleted uncommitted; no telemetry attribution work remains in this roadmap.
