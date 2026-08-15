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
| **D1** | **Consumption is attributed to the EARLIER day.** The nightly poll writes snapshot row `N`, then updates row `N−1`'s five derived columns, both in one transaction. Row `N` is always inserted with NULLs in those columns; the *next* poll fills them. | The 03:00 poll on Aug 14 measures the window Aug 13 03:00 → Aug 14 03:00 — 21 hours of Aug 13. Filing it under Aug 13 is more accurate *and* is what makes the formula line up: a charge on Aug 13 at 18:00 sits in that window, so consumption and charge must share a bucket. Re-polls stay self-healing because the previous-day recompute runs on **every** poll, not once. |
| **D1a** | The backfill shifts **all five** derived columns (`distance_traveled_km_calc`, `battery_used_pct_calc`, `km_per_pct_calc`, `estimated_range_km_calc`, `days_spanned_calc`) back one row per vehicle. The newest row per vehicle correctly ends with NULLs. | The five are computed together from the same snapshot pair; shifting a subset would leave the row internally inconsistent. |
| **D1b** | **The existing odometer and battery graphs shift one day earlier** as a consequence, and that is accepted. | They plot these same stored columns by row date. The correctness argument is identical; leaving them on the old attribution would put two graphs on the same page one day apart. |
| **D1c** | D1 makes the existing "presets end at yesterday" workaround **structural**. | `handlers/history.go:217` already forces every preset to end at yesterday because "the nightly batch captures today's data tomorrow — an end=today preset would always show an empty last bar". Under D1 the current day genuinely has no derived data, so the rule stops being a patch. See **D11**. |
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
| **D9** | **Add date-range read ports** (additive — existing methods stay): `telemetry.SnapshotsByVehicleBetween`, `telemetry.SuperchargerSessionsByVehicleBetween`, `manualcharge.ListEntriesByVehicleBetween`, each taking `from, to time.Time`. | A date-filtered graph asking for a date range is the honest contract. The existing reads are limit-based, and with a limit there is no safe number to guess — sessions per 90 days is unbounded, so a heavy month would silently truncate the window. |
| **D9a** | The snapshot fetch spans **`[start, end+1]`**, one day wider than the rendered window. | Under D1, computing day `D`'s bar needs the snapshots at `D` *and* `D+1`. Fetching only `[start, end]` silently drops the last bar of every window. |
| **D10** | **A flagged day renders as a zero-height bar with a distinct warning marker** — never the raw negative value, never omitted. Label bilingual ES/EN via `i18n.T`. | Plotting a number we do not believe reads as a bug; hiding the day makes it indistinguishable from having no data, so the user never learns there is something to fix. |
| **D11** | `parseHistoryRange`'s **default window and its `end <= today` cap both move to yesterday**, matching the presets. | Under D1 the current day has no derived values at all, so `end = today` can only ever produce an empty trailing bar. |
| **D12** | **Charge-to-day matching is source-specific.** A Supercharger session belongs to day `D` when `charge_stop_date_time` falls between `snapshot[D].captured_at` and `snapshot[D+1].captured_at`. Manual entries match on `charged_on` (date). | A session ending 01:00 on Aug 14 belongs to Aug 13's 03:00→03:00 window but has stop-*date* Aug 14 — pure date bucketing files it under the wrong day, producing a false gap flag on one day and an inflated bar on the next. Battery already holds both snapshots, so the interval test is free. Manual entries carry only a date, and the user picked it deliberately. This makes **D6** irrelevant for Supercharger sessions; it still governs manual entries. |
| **D13** | **The formula sums over ALL charge events** in the window, across **both** tables together: `battery_consumed_pct = battery_used_pct_calc + Σ(end_battery_pct − start_battery_pct)`. Neither source takes precedence. | The owner's initial "take only the latest `charge_stop_date_time` per day" was corrected during the interview. With two sessions on Aug 13 (20→50, 60→80) and snapshots 30 → 75: summing gives **5** ✓; latest-only gives **−25** ✗ (discards the earlier session and produces a false missing-record flag); first-start-to-last-end gives **15** ✗ (over-counts, treating the driving *between* sessions as charging). Latest-stop-time remains correct for *which day a session belongs to* — just not for *which sessions to count*. `internal/battery` already sums both sources this way (`sumSuperchargerKWh`, `sumManualKWh`). |
| **D14** | **Supercharger start/end percentages are always NULL today, and that is accepted.** No entry UI, no kWh-based estimation in RM28. Those days flag as `SUPERCHARGER` gaps. | MAG-14 / RM27 shipped the five columns as storage only; the Fleet API carries no state-of-charge, and the taper-solve estimator was explicitly descoped as disproportionate (RM27 D11, backlog entry 11). Reviving either here is out of scope. |
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
- **Shifting the attribution at read time** in the gateway view model. Cheaper (no migration,
  no backfill), but it puts date arithmetic in the presentation layer and leaves every future
  consumer of the five columns to re-derive the same convention. Rejected in favour of **D1**,
  which makes the stored data self-describing.

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
| `[~]` | `RM28-telemetry-shift-consumption-attribution` | `telemetry` | Move the five derived columns to the earlier row (**D1**): migration + one-time backfill (**D1a**), and change the write path so a poll upserts row `N` and updates row `N−1` in one transaction. Row `N` inserts with NULLs. Update `deriveConsumption`, the sqlc upsert, and every doc comment asserting the old attribution. | — | Implement D1/D1a in `internal/telemetry`. The five derived columns move from the current row to its predecessor. Write path: upsert row N, then update row N−1's five columns, same transaction. Preserve the re-poll self-healing property — a second poll for the same day must recompute N−1 from the fresh reading. Backfill all five columns one row back per `(account_id, tesla_id)`, oldest-first ordering by `captured_at`; the newest row per vehicle correctly ends NULL. `database` design gate applies: design.md must carry the migration, the backfill, the rationale, and the index plan. |
| `[ ]` | `RM28-telemetry-add-charge-gap-storage` | `telemetry` | The missing-charge table (**D3**, **D7**, **D7a**, **D7b**) + its write port, plus the two telemetry date-range read ports (**D9**): `SnapshotsByVehicleBetween`, `SuperchargerSessionsByVehicleBetween`. | — | Create the missing-charge table per D7 — `UNIQUE (account_id, tesla_id, date)`, columns `vin`/`tesla_id`/`date`/`missing_charging_type` all NOT NULL — and a write port supporting upsert-and-delete reconciliation per D7b. Add the two additive date-range readers per D9; existing limit-based methods stay. `SuperchargerSessionsByVehicleBetween` filters on `charge_stop_date_time` (D12). `database` design gate applies. |
| `[ ]` | `RM28-manualcharge-add-date-range-reader` | `manualcharge` | `ListEntriesByVehicleBetween(ctx, accountID, teslaID, from, to)` (**D9**), filtering on `charged_on`. Additive. | — | Add one date-range read method to `manualcharge.Reader` per D9, filtering on `charged_on` inclusive of both bounds. Purely additive — `ListEntriesByVehicle` and `ListEntriesByAccount` are unchanged. No schema change. |
| `[ ]` | `RM28-battery-derive-consumed-per-day` | `battery` | The derivation (**D13**) and gap detection (**D5**, **D5a**, **D7a**), the charge-matching rules (**D12**), multi-day span handling (**D8**), the `[start, end+1]` fetch (**D9a**), a `ConsumedByDay` port, and the `cmd/poller` wiring (**D4**, **D4a**). | T1, T2, T3 | Implement the per-day consumed derivation in `internal/battery` per D13, returning each day already correctly dated (D1). Match charges to days per D12 — Supercharger by snapshot interval, manual by `charged_on`. Detect gaps per D5/D5a and infer the type per D7a. Fetch snapshots over `[start, end+1]` per D9a. Handle `days_spanned_calc > 1` per D8. Expose a `ConsumedByDay` read port for the gateway. Wire detection into `cmd/poller` per D4/D4a — the composition root orchestrates; telemetry never calls battery. Update `internal/battery/AGENTS.md`. |
| `[ ]` | `RM28-gateway-add-consumed-graph` | `gateway` | The bar graph mirroring the existing odometer/battery charts, flagged-day rendering (**D10**), and the default-window reconciliation (**D11**). | T4 | Add the battery-consumed bar chart to `/ui/dashboard/history`, mirroring `templates/fragments/history.templ` + `history_vm.go`. Consume `battery.ConsumedByDay` through the port — never a database. Render flagged days per D10 (zero-height bar, distinct warning marker) and multi-day spans per D8. Move `parseHistoryRange`'s default and its `end <= today` cap to yesterday per D11. Every new label bilingual ES/EN through `i18n.T` against `internal/gateway/i18n/catalog.go`, both non-empty. |

**Tier order.** T1, T2 and T3 are mutually independent and could run in any order; T1 leads
because it is the riskiest and everything downstream depends on its attribution semantics.
T4 needs all three ports plus the new attribution. T5 needs T4.
