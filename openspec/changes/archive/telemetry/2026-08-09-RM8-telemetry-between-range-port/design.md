## Context

`telemetry.Reader` exposes `SnapshotsByVehicleSince(ctx, accountID, teslaID, since)` — an open
lower bound over `vehicle_snapshots`:

```sql
-- name: SnapshotsByVehicleSince :many
SELECT ... FROM vehicle_snapshots
WHERE account_id = @account_id AND tesla_id = @tesla_id AND captured_at >= @since
ORDER BY captured_at ASC
LIMIT 400;
```

It reuses the existing `idx_vehicle_snapshots_vehicle_time (account_id, tesla_id, captured_at)`
ASCENDING index as a forward range scan (no sort step, no new DB object).

MAG-7 / the RM8 roadmap wants a **bounded calendar-day window** read: `Between(start, end)` where
`start`/`end` are whole UTC-midnight-bounded calendar days and `end` is **inclusive**, returning the
snapshots whose **`EffectiveDate` calendar day** falls in `[start, end]` inclusive (Decision #2).
`EffectiveDate` (added by `telemetry-add-effective-date`) is a read-derived field computed in the
single DB→domain mapper `rowToSnapshot`:

```go
EffectiveDate: r.CapturedAt.Time.AddDate(0, 0, -1),
```

`r.CapturedAt.Time` from pgx is a `time.Time` in **UTC** (TIMESTAMPTZ default), so `EffectiveDate`'s
**calendar day = the UTC calendar day of `captured_at`, minus 1.** The `vehicle_snapshots` table also
stores a `captured_date DATE` column — but that one is **Go-computed in the poller's configured
timezone** (`POLLER_TIMEZONE`, currently `America/Bogota`, UTC−5), used solely for the
`vehicle_snapshots_account_tesla_date_unique (account_id, tesla_id, captured_date)` dedupe constraint
(`telemetry-dedupe-daily-snapshots` design D2). These are **two different calendar-day concepts**:
`EffectiveDate` is UTC-derived; `captured_date` is poller-zone-derived. They coincide for the
03:30-local nightly capture only by deployment coincidence (UTC−5 ⇒ 03:30 local = 08:30 UTC, same
calendar day); a capture between 19:00–24:00 local would straddle UTC midnight and the two would
diverge by a day.

The single read mapper (`rowToSnapshot`, `internal/telemetry/mapping.go:84`) is the one place every
read path already funnels through; it already populates `EffectiveDate`, so the new method gets it for
free with no per-method duplication.

## Goals / Non-Goals

**Goals:**
- Add a bounded `[start, end]`-inclusive range read to `telemetry.Reader` that returns snapshots
  whose `EffectiveDate` calendar day falls in the window, ascending by EffectiveDate.
- Keep the change additive: no DB migration, no new index, no method-signature change to existing
  methods (`Since` is kept, not replaced).
- Keep the port a clean `Between(start, end)` — the 1-day lookback the gateway needs is a gateway
  concern (Decision #4); no lookback parameter leaks into the port.
- Bias toward read performance (Performance-Profile: read-heavy): reuse the existing
  `(account_id, tesla_id, captured_at)` ascending index as a forward range scan with no sort step.

**Non-Goals:**
- NOT changing the DB schema, any migration, or any index (no `database` design-gate trigger).
- NOT removing or deprecating `SnapshotsByVehicleSince` (kept; existing callers unaffected).
- NOT adding the gateway HTTP surface, the lookback math, or the fixed-axis rendering — those are
  tier 2 (`RM8-gateway-history-date-range`).
- NOT validating that `start`/`end` are UTC-midnight-bounded inside the port — that is the HTTP
  contract's job in tier 2 (Decision #2). The port honors the semantics its implementer translates
  (see D1) and documents the expectation.
- NOT computing the window itself (`since` analog kept caller-supplied; `Between(start, end)` is a
  pure data accessor).

## Decisions

### D1 — Filter on `captured_at` (TIMESTAMPTZ), not `captured_date` (DATE)

**Decision:** The new query filters on the `captured_at TIMESTAMPTZ` column, with bounds derived
from the `EffectiveDate` window. Concretely, to honor "return snapshots whose `EffectiveDate`
calendar day ∈ `[start, end]` inclusive" (with `start`/`end` UTC-midnight-bounded):

`EffectiveDate.CalendarDay(UTC) = CapturedAt.CalendarDay(UTC) − 1`, so
`EffectiveDate ∈ [start, end]` ⇔ `CapturedAt.CalendarDay(UTC) ∈ [start+1, end+1]` (inclusive both
ends, calendar days). Converted to TIMESTAMPTZ predicates:

```text
captured_at >= start.AddDate(0, 0,  1)   -- UTC midnight beginning the first eligible capture day
captured_at <  end  .AddDate(0, 0,  2)   -- UTC midnight ending the last eligible capture day (inclusive)
```

i.e. the implementation translates the caller's `(start, end)` once into `(startBound, endBound)`
and binds them as `pgtype.Timestamptz`:

```go
startBound := start.AddDate(0, 0,  1)  // start + 1 day, UTC midnight
endBound   := end  .AddDate(0, 0,  2)  // (end + 1 day) + 1 day = end + 2 days, exclusive
```

This is `AddDate` calendar-day arithmetic (DST-safe, mirroring `rowToSnapshot`'s `EffectiveDate`
computation since `start`/`end` are UTC-midnight instants); not a 24-hour duration.

**Boundary walkthrough** (`start = 2026-08-01 00:00 UTC`, `end = 2026-08-07 00:00 UTC`, 7-day
window, `end` inclusive):

| capture instant (UTC) | `startBound <= captured_at < endBound`? | `EffectiveDate` (cal day) | in window? |
|---|---|---|---|
| 2026-08-02 08:30 (Aug 2 cal day) | `Aug 2 00:00 <= x < Aug 9 00:00` ✓ | Aug 1 | ✓ included (start boundary) |
| 2026-08-08 08:30 (Aug 8 cal day) | ✓ | Aug 7 | ✓ included (`end` inclusive) |
| 2026-08-09 08:30 (Aug 9 cal day) | `Aug 9 08:30 < Aug 9 00:00` ✗ | Aug 8 | ✗ excluded (just past `end`) |
| 2026-08-01 23:59 (Aug 1 cal day) | `Aug 1 23:59 < Aug 2 00:00` ✗ | Jul 31 | ✗ excluded (just before `start`) |

**Alternatives considered (rejected):**

- **Filter on `captured_date DATE` with bounds `[start+1, end+1]` inclusive.** Rejected.
  `captured_date` is Go-computed in `POLLER_TIMEZONE` (design D2 of
  `telemetry-dedupe-daily-snapshots`), not UTC. `EffectiveDate` is UTC-derived
  (`CapturedAt.Time` is UTC). The two coincide only because the nightly capture falls on the same
  calendar day in UTC and America/Bogota — a deployment coincidence, not a guarantee. Filtering on
  `captured_date` would couple the port's correctness to the poller's timezone (the exact fragility
  the dedupe change's D2 rejected), and would break quietly the moment a capture straddles UTC
  midnight or `POLLER_TIMEZONE` changes. **Also**, the implicit index from
  `vehicle_snapshots_account_tesla_date_unique (account_id, tesla_id, captured_date)` has the right
  leading prefix and would *serve* such a query — but the semantic correctness problem disqualifies
  it regardless of index fit.
- **Add a generated/stored `effective_date` column + index.** Rejected. Adds a migration, a new
  index, and a write-path maintenance burden for what is a cheap, deterministic read-time
  derivation; `EffectiveDate` was deliberately read-derived (no column) in
  `telemetry-add-effective-date` design D2. Re-introducing a stored column here would invert that
  decision and trigger the `database` design-gate for no read-performance gain (the existing
  `captured_at` index already serves the query — see D2/D3).

**Rationale:** Filtering on `captured_at` keeps the port's semantics tied to the same time-origin
`EffectiveDate` is derived from (UTC `captured_at`), so the query and the mapper stay consistent by
construction, with zero timezone coupling. The translation `start → start+1day` / `end → end+2days`
lives inside the `dbStore` impl (`service.go`) — the public `Between(start, end)` method stays a
clean window.

### D2 — Reuse the existing ASCENDING index; no new index (no `database` design-gate)

**Decision:** The new query reuses `idx_vehicle_snapshots_vehicle_time (account_id, tesla_id,
captured_at)` (migration `20260710000002_init_telemetry.sql`). No new index is created. The change
does not touch any schema migration, so the `database` design-gate is **not triggered**.

The query shape is:

```sql
-- name: SnapshotsByVehicleBetween :many
SELECT
    id, account_id, tesla_id, captured_at, raw_data,
    battery_level_pct, battery_range_km, charging_state, charge_limit_soc_pct,
    odometer_km, inside_temp_c, outside_temp_c, locked, sentry_mode,
    car_version,
    charge_energy_added_kwh, charger_power_kw, charger_voltage_v,
    charger_actual_current_a, usable_battery_level_pct,
    max_range_charge_counter,
    tpms_pressure_fl_psi, tpms_pressure_fr_psi, tpms_pressure_rl_psi, tpms_pressure_rr_psi,
    captured_date, updated_at
FROM vehicle_snapshots
WHERE account_id = @account_id
  AND tesla_id   = @tesla_id
  AND captured_at >= @start_bound
  AND captured_at <  @end_bound
ORDER BY captured_at ASC
LIMIT 400;
```

**Why the existing index serves this as a forward range scan with no sort step:** the index's leading
columns are `(account_id, tesla_id)` — both equality predicates — and its trailing column is
`captured_at` (ASC), which the query constrains with a half-open range `[start_bound, end_bound)`
and orders `ASC`. The Postgres planner seeks to `(account_id, tesla_id, start_bound)` and reads
**forward in index order**, satisfying the WHERE and the ORDER BY simultaneously; the upper bound
`captured_at < end_bound` is also a range predicate on the same indexed column, so it prunes the scan
in-place rather than causing a re-scan or a sort. This is the identical access pattern `Since` uses
(D3 of `telemetry-add-snapshot-history-read-port`), with one extra range predicate — strictly
cheaper than the open `Since` scan for the same dashboard window.

**Rejected alternative:** filter on `captured_date` (see D1) — the implicit unique index
`(account_id, tesla_id, captured_date)` would also serve a forward range scan, but the semantic
correctness problem (timezone coupling) disqualifies it.

### D3 — `end` inclusive via a half-open `captured_at` range; LIMIT 400 safety cap retained

**Decision:** `end` is inclusive (Decision #2). Implemented as a **half-open** range on
`captured_at` — `captured_at >= start_bound AND captured_at < end_bound` where `end_bound =
end.AddDate(0,0,2)` — so every capture instant on the last eligible calendar day is included
(see the boundary table in D1). Half-open upper bounds are the project's standard idiom (the
`Since` query uses `>= @since … LIMIT`), and they avoid the off-by-one a `<=` TIMESTAMPTZ
comparison would introduce when the caller's `end` is a UTC-midnight instant (a `<=` would
exclude the last day's 03:30 capture; a `< end+1day` would include captures one calendar day past
`end+1` only if `EffectiveDate` derivation flipped — it does not, but the half-open `[start+1,
end+2)` form is provably correct directly from the `EffectiveDate ∈ [start, end]` definition
without appealing to that).

**LIMIT:** keep `LIMIT 400`, mirroring `SnapshotsByVehicleSince` (D4 of
`telemetry-add-snapshot-history-read-port`). The HTTP contract (Decision #2) caps the window at
≤ 90 days; the gateway's 1-day lookback (Decision #4) adds one day, so the realistic max return is
**91 rows** (one nightly snapshot per calendar day under the current cadence). `400` retains the
same safety net `Since` has against an accidentally high capture cadence — comfortably above 91
without being so large it re-introduces the unbounded-scan risk the cap exists to prevent. We do
**not** tighten the LIMIT to e.g. 91: a tighter cap would silently truncate legitimate future
high-cadence reads, and the bounded window itself (not the LIMIT) is the real protection on this
path.

### D4 — `SnapshotsByVehicleSince` is kept, not replaced (additive / non-breaking)

**Decision:** `SnapshotsByVehicleSince` is left exactly as-is. `Between` is added alongside it.
(Decision #4 of the RM8 roadmap.)

**Alternatives considered (rejected):**
- **Replace `Since` with `Between` and rewrite existing callers.** Rejected. `Since` is an open
  lower bound (no upper); `Between` is a bounded window. They serve genuinely different access
  patterns (open-ended "everything from X onward" vs scoped window). Replacing `Since` would force
  tier 2 (gateway) into this same change — breaking the single-module scope this tier was split out
  for — and would couple the MAG-7 feature delivery to the gateway rewrite. Keeping `Since` additive
  preserves all current callers (the gateway's `?days=N` handler, until tier 2 ships) and lets tier 1
  land independently of tier 2 / RM7 tier 2's same-files ordering constraint.
- **Mark `Since` deprecated.** Rejected. Deprecation would imply a removal plan; there is no
  removal plan (an open-bound read is a legitimate future call site even after the gateway migrates).
  Deprecation/removal, if ever, is its own change.

**Rationale:** additive design. Tier 1 and tier 2 ship through their own changes; the existing
hot path stays green and unchanged until tier 2 routes it through `Between`.

### D5 — Translation of `(start, end)` → `(start_bound, end_bound)` lives inside the `dbStore` impl, not the port

**Decision:** The public method signature is `SnapshotsByVehicleBetween(ctx, accountID, teslaID,
start, end time.Time) ([]Snapshot, error)` — a clean window. The `+1day` / `+2day` translation that
honors `EffectiveDate ∈ [start, end]` (D1) is an **implementation detail of the `dbStore`
implementation** in `service.go` (the DB→domain boundary), exactly where `pgtype` conversions
already live (`timestamptzFrom`). The `reader`/`store` seam passes the caller's `start`/`end`
through unchanged; `dbStore.snapshotsByVehicleBetween` computes the two bounds and binds them. The
public doc comment on the interface states the `EffectiveDate ∈ [start, end]` semantics and the
UTC-midnight expectation, but the offset math never appears in the port contract.

**Rationale:** matches the existing pattern (`snapshotsByVehicleSince` converts `time.Time` →
`pgtype.Timestamptz` inside `dbStore`, not in `reader`). Keeps the boundary math in one testable
place and the public interface free of `pgtype` / offset artifacts. Decision #4 ("the port stays a
clean `Between(start, end)`") is honored at the interface; the translation is the port's internal
machinery for honoring it.

## Index plan (justified against declared read patterns)

**Affected read path:** the dashboard history hot path — `gateway.handlers.history` →
`telemetry.Reader.SnapshotsByVehicleBetween` (once tier 2 wires it). One call per history-chart
render; user-initiated htmx refresh, not a per-boot hot path.

**Read pattern (declared, `ai/architecture.md` §7 + `ai/go-conventions.md` §Read optimization):**
every dashboard read scopes by `account_id` (leading index column); per-vehicle time-series reads
filter `(account_id, tesla_id)` and order by time ascending; the workload is ~99% reads.

**Index serving this query (reuse, no new object):**

- Index: `idx_vehicle_snapshots_vehicle_time (account_id, tesla_id, captured_at)` — created in
  `20260710000002_init_telemetry.sql`, ASCENDING (default).
- Access: **Index-range scan, forward, no sort step.**
  - Seek to `(account_id = $1, tesla_id = $2, captured_at = start_bound)`.
  - Read forward in `captured_at ASC` order.
  - Stop at `captured_at < end_bound` (the upper bound prunes the scan in-place).
  - `ORDER BY captured_at ASC` is satisfied by the index order — no `Sort` node in the plan.
- Cost: O(rows in the window). With the 90-day HTTP cap + 1-day gateway lookback → ≤ 91 rows per
  call. Strictly cheaper than `Since` (which scans from `since` to the table's end, capped only by
  `LIMIT 400`).

**No new index is needed. No migration is needed. The `database` design-gate is NOT triggered.**

## Risks / Trade-offs

- **[Risk] A caller passes non-UTC-midnight-bounded `start`/`end` and gets a surprising window.**
  The port trusts the caller; the `+1day` / `+2day` translation assumes `start`/`end` are UTC
  midnights. → **Mitigation:** the interface doc comment states the UTC-midnight-bounded/
  inclusive-`end` contract; the HTTP contract enforces it in tier 2 (Decision #2: 400 on malformed
  non-ISO dates, clamp `end >= start` and `end <= today`, hard cap ≤ 90 days). The port does no
  validation by design (Decision #4 — clean window).
- **[Risk] A future caller mistakes `Between` for an open `Since`-style read.** They differ: `Since`
  is open-lower-bounded; `Between` is bounded both ends, `end` inclusive, on `EffectiveDate`. →
  **Mitigation:** doc comments on both methods state their semantics; `Between`'s comment names
  `EffectiveDate` explicitly and points to `Since` for the open-bound case.
- **[Trade-off] `LIMIT 400` is looser than the realistic 91-row max.** Accepted — it keeps parity
  with `Since`'s safety cap and does not introduce a tight bound that could silently truncate a
  future high-cadence read. The bounded window itself is the real protection.
- **[Trade-off] The `+1day`/`+2day` translation is implementation-hidden.** A reader of the SQL
  query sees `captured_at >= @start_bound AND captured_at < @end_bound` without seeing that
  `start_bound = start+1day` and `end_bound = end+2day`. → **Mitigation:** the query's
  header comment in `query.sql` states the bounds' derivation and the `EffectiveDate ∈ [start, end]`
  semantics; the `dbStore.snapshotsByVehicleBetween` doc comment does the same. Discoverable where
  the next agent already looks (`query.sql`, `service.go`).

## Migration Plan

None. No schema change, no migration, no config, no new index. The change is purely Go code in
`internal/telemetry/` plus a new sqlc query (regenerated via `make sqlc`). Deploy and the new
method is available on `telemetry.Reader`; existing callers are unaffected until tier 2 routes the
history handler through it.

## Open Questions

None. The design questions that matter (D1 column choice, D2 index reuse, D3 `end`-inclusive
bounds + LIMIT, D4 keep `Since`) are settled here against the read patterns; the binding product
decisions (bounded port, UTC-midnight inclusive `end`, lookback is gateway-only) are settled in the
RM8 roadmap Decisions #1, #2, #4 from the 2026-08-09 grill-me interview.