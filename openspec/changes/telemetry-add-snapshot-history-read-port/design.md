# Design: telemetry-add-snapshot-history-read-port

## Context

The gateway dashboard has two history cards (`dashHistoryEmpty()` placeholders) with no backing
read port. The telemetry module exposes only `LatestSnapshotsByAccount` (one row per vehicle). This
change adds a per-vehicle **history** read port that returns the nightly snapshots for one vehicle
since a caller-supplied instant, oldest-first — the data source for RM5 tier 2's odometer/battery
bar charts. Implementation is **not** started; this document is the design for review.

## Goals / Non-Goals

**Goals**
- Add `telemetry.Reader.SnapshotsByVehicleSince(ctx, accountID, teslaID, since)` returning
  `[]Snapshot` oldest-first, empty-non-nil on no data.
- Keep the window boundary in the caller (`since time.Time`), account+vehicle scoped, index-served.

**Non-Goals**
- Any gateway/UI change (tier 2 owns the API, charts, and days selector).
- Any pre-aggregation, materialized view, or new DB object (see Decisions).
- Any change to capture (`Collector`) or to the existing latest read port.

## Decisions

### D1 — `since time.Time`, not `days int`
The port is a pure data accessor. The gateway computes `since = startOfWindow(now, days)` from the
`days` query param and passes an absolute instant. This keeps date math at the edge (one place,
testable there) and lets every future consumer choose its own window without a port change.

### D2 — Method takes `accountID`; query filters `account_id AND tesla_id`
Defense-in-depth tenant isolation. The gateway only ever resolves `tesla_id` from
`account.RegisteredVehicles(uid)`, so a cross-account request is already impossible from the
gateway — but filtering `account_id` in the query too guarantees isolation for any non-gateway
caller at a negligible cost (the columns are the leading index columns anyway).

### D3 — No new index, no migration (reuse the existing ascending index)
`internal/telemetry/db/migrations/20260710000002_init_telemetry.sql` creates:

```sql
CREATE INDEX idx_vehicle_snapshots_vehicle_time
    ON vehicle_snapshots (account_id, tesla_id, captured_at);
```

That index is **ascending** on `captured_at`, with `(account_id, tesla_id)` as the leading
exact-match columns. The new query —

```sql
WHERE account_id = $1 AND tesla_id = $2 AND captured_at >= $3
ORDER BY captured_at ASC
```

— is exactly a forward range scan on this index: the planner seeks to `(account_id, tesla_id,
since)` and reads forward in index order until the leading columns change, satisfying both the
`WHERE` and the `ORDER BY` with **no sort step and no extra index**. (The same index already serves
`LatestSnapshotsByAccount`, per that query's comment.) Adding an index would only add write cost on
the nightly append path for zero read benefit — rejected. **Result: this change adds no database
object, so the `database` design gate does not trigger.**

### D4 — `LIMIT 400` safety cap in the query
The 30-day window bounds normal results to ~30 rows (one per nightly capture). A baked-in
`LIMIT 400` is a cheap guard against an accidentally huge scan if capture cadence ever increases
(e.g. hourly). It is not a functional pagination limit — 400 comfortably exceeds any realistic
dashboard window (≈13 months of nightly rows).

### Return type: existing `Snapshot`, oldest-first
Mirrors `LatestSnapshotsByAccount` (same domain type, miles-native + `Km()` companions, `raw_data`
included). Oldest-first ordering matches how a left-to-right bar chart consumes the series and lets
tier 2 compute day-over-day odometer deltas in one forward pass.

### Rejected alternative — pre-aggregated `daily_summary` table
A `daily_summary(account_id, tesla_id, day, km_driven, battery_level)` write-time rollup was
considered and rejected as premature: the raw range scan is ~30 rows on an existing index (cheap),
and a rollup table would duplicate truth, add a migration + a write path in the nightly collector,
and risk drift. It stays consistent with the module's "store historical events rather than
overwrite state" philosophy. Revisit only if a future consumer needs sub-second aggregates over
years of data.

## Risks / Trade-offs

- [A non-gateway caller passes a `tesla_id` from another account] → the `account_id` filter (D2)
  returns no cross-account rows.
- [Capture cadence increases and windows grow] → `LIMIT 400` (D4) caps the scan; if a real need for
  larger windows arises, revisit the pre-aggregation alternative.
- [Test fakes implementing `Reader` break the build] → each gains a one-line `nil, nil` stub;
  called out in tasks.

## Migration Plan

None — no schema or data migration (D3). Rollback is reverting the code commit.

## Open Questions

None. D1–D4 resolved with the user (RM5 RD1–RD4).
