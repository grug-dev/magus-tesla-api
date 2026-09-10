# RM52-analytics-add-monthly-metrics

> Source: MAG-32 — https://linear.app/magus-monitor/issue/MAG-32/vehicle-monthly-metrics-new-table
> Roadmap: RM52-vehicle-monthly-metrics, tier 1 of 4. See that file for RD1–RD11
> (binding decisions this change does not re-open).

## Why

`internal/charging` divides by a hardcoded `62.0` kWh pack capacity for every vehicle. This
is wrong for every car that is not actually 62.0 kWh.

MAG-25 already stores `inferred_capacity_kwh_calc` on each charge record — the capacity one
single charge implies. One record is noisy. Many records, filtered and pooled well, are not.

This change adds the table and the job that turns those records into one trusted number per
vehicle per month.

## What changes

**A. New table.** `analytics.vehicle_monthly_metrics` — one row per `(tesla_id,
effective_period)`. RD11's exact DDL, reproduced in `design.md`.

**B. A pure estimator function.** Filters out rows whose capacity would have come from the
`62.0` constant (RD2), drops small-delta rows (RD3), then takes the median. No `ctx`, no I/O
— unit-testable with a plain slice.

**C. The calculator.** Reads every registered vehicle, groups by `tesla_id` (RD5, because two
accounts can register the same car), fetches that vehicle's manual entries and Supercharger
sessions for the requested month through `internal/charging`'s existing public ports, runs
them through the estimator, and upserts one row.

**D. A new, separate read port.** `PackCapacityReader` — a small new interface, own file,
with one method: `PackCapacityKWh`. Its shape matches `internal/charging`'s own
`PackCapacityReader` interface exactly (RD1), so `charging` can consume it later without
either module importing the other. This is a NEW interface, not an addition to the existing
`Reader` port — see `design.md` D7 for why: `analytics.Reader` already has fakes in
`internal/app` and `internal/gateway` that this tier may not touch and, for `gateway`, no
later RM52 tier owns either.

**E. A doc fix.** `internal/analytics/analytics.go`'s package comment still says this module
"owns no database and no store." False since `RM29-analytics-add-vehicle-metrics`. Fixed here.

**F. Unit and integration tests.** Pure-function tests for the estimator. Fake-backed tests
for the calculator's filter and pooling logic. One `DATABASE_URL`-gated integration test for
the full write-then-read path, including the RD4 fallback.

## Breaking?

No. This change only adds a new table and two new methods on `analytics`. It touches no
existing column, query, or method signature. No other module is changed or read from in a
way that was not already allowed.

## Modules affected

`internal/analytics` only. `internal/charging` is read from (its existing public `Reader` and
`SuperchargerSessionAnalyticsReader` ports, plus its `Entry`/`Session` domain types) but not
changed. `internal/account` is read from (`AllRegisteredVehicles`) but not changed. Tiers 2–4
of the roadmap wire this module's output into `app` and `charging` — out of scope here.

## Read paths affected

None of this tier's writes run on a user-facing read path — `Calculate` runs from the nightly
processor (tier 2) or the manual `cmd/monthly-metrics` tool (tier 4), never from the gateway.

`PackCapacityKWh` becomes a new read path once tier 3 wires it into `charging`'s two existing
callers (`resolveEnergy`, `VerifySession`) — both already run on a manual-charge write, not a
dashboard read. This tier only adds the method; it has no caller yet.

## Non-goals (later tiers)

- No trigger wiring — tier 2 (`app`) decides when `Calculate` runs.
- No `charging` consumption of `PackCapacityKWh` — tier 3.
- No `cmd/monthly-metrics`, no `make` target, no docs sweep — tier 4.
- No recomputation of historical `ESTIMATED`/`DONE_CALCULATED` records — rejected at the
  roadmap level (RD2's own note), not re-opened here.

## Accepted cost — stated up front, not a defect

A vehicle with fewer than `minSamples` (3) valid charge records in a month gets no capacity
for that month (`effective_capacity_kwh` stays `NULL`, RD4). This is correct, not a gap: the
reader falls back to the newest earlier measured value, and pack capacity changes slowly
enough that last month's number is still a good answer.
