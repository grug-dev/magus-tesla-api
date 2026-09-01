# Design — RM38-analytics-add-vehicle-status-columns

## Context

`vehicle_metrics` (`internal/analytics/db/migrations/20260821000001_add_vehicle_metrics.sql`,
`RM29-analytics-add-vehicle-metrics`) is analytics' precomputed daily read model: one
row per `(account_id, tesla_id, metric_date)` for every day that has a
`telemetry.Snapshot`, dense (a row exists even for a predecessor-less day), written by
`Recalculator.Recalculate`/`Reconcile`, read by `Reader`. Today it carries three raw
observations (`battery_level_pct`, `odometer_km`, `battery_range_km`, all `NOT NULL`),
five derived `_calc` columns (nullable — NULL iff the row's day has no computable
predecessor), and the D13-corrected consumption columns.

`RM38-dashboard-vehicle-status-from-metrics` (the roadmap this tier belongs to) wants
`vehicle_metrics` to become a **complete substitute** for
`telemetry.Reader.LatestSnapshotsByAccount` at the four gateway call sites that
currently read it (roadmap D3, verified there against the actual `mapDashboardSnapshot`/
`mapVehicles`/nav-header/`charges.go` code, not assumed from the ticket). The union of
fields those four sites read is `BatteryLevelPct`, `BatteryRangeKm`, `OdometerKm`,
`InsideTempC`, `OutsideTempC`, `Locked`, `SentryMode`, `CarVersion`, `ChargingState`,
`ChargeLimitSocPct`, `CapturedAt`, `TeslaID` — twelve fields. Three of the twelve
already exist on `vehicle_metrics`; this tier adds the other eight, plus the read port
that exposes all twelve as one analytics-owned domain type.

This tier does not touch the gateway. It only makes the substitute exist; tier 2
(`RM38-gateway-read-dashboard-from-metrics`, depends on this tier) is the only place
any call site moves.

## Goals / Non-Goals

**Goals:**
- Add the eight nullable columns (roadmap D1) to `vehicle_metrics`, with a
  `COMMENT ON COLUMN` on each documenting its NULL semantics — including
  `sentry_mode`'s new ambiguity (roadmap D2).
- Extend `Recalculate`'s derivation (`consumed.go`'s `deriveVehicleMetrics`,
  `vehicleMetricRow`) and the generated `UpsertVehicleMetric` call
  (`recalculate.go`) to copy all eight verbatim from the day's own
  `telemetry.Snapshot` — no re-derivation, mirroring how the three pre-existing raw
  observations are already copied.
- Add `analytics.Reader.LatestMetricsByAccount(ctx, accountID) ([]VehicleStatus,
  error)` and the `VehicleStatus` domain type, covering all twelve fields tier 2's
  call sites need (verified below, "D3 — coverage check").
- Settle and justify the index plan for the new read pattern
  (`WHERE account_id = $1`, latest row per `tesla_id`) against the existing
  `vehicle_metrics_account_tesla_date_unique` index.
- Update `internal/analytics/AGENTS.md` (and the root `README.md` if its Architecture
  table entry goes stale) in this same change (`CLAUDE.md` docs-track-structural-change).

**Non-Goals (explicitly deferred, do not implement here):**
- Repointing any gateway call site — tier 2.
- Backfilling any historical row — `openspec/roadmaps/backlog.md` §22 (roadmap D2).
- Any UI decision (badges, stat grid, i18n) — tier 2, roadmap D4/D5.
- Touching `ConsumedByDay`/`OdometerDeltaByDay` or the five `_calc` columns — untouched
  by this tier.

## Decisions

Decisions below are numbered fresh for this design document (D1…D8), independent of
the roadmap's own D1–D7 numbering — cross-referenced explicitly wherever one carries a
roadmap decision forward rather than introducing something new.

### D1 — Column list and types (carries roadmap D1)

Eight columns, typed to match `telemetry.Snapshot`'s own field types and this
project's unit-suffix convention (`ai/go-conventions.md`), and cross-checked against
`vehicle_snapshots`' own column types for the identical fields
(`internal/telemetry/db/migrations/20260710000002_init_telemetry.sql` +
`20260806000001_store_display_units_vehicle_snapshots.sql`, which is where
`inside_temp`/`outside_temp`/`battery_level`/`charge_limit_soc` were renamed to their
current `_c`/`_pct`-suffixed names):

| Column | Type | Source field |
|---|---|---|
| `locked` | `BOOLEAN` | `telemetry.Snapshot.Locked` (`bool`) |
| `sentry_mode` | `BOOLEAN` | `telemetry.Snapshot.SentryMode` (`*bool`) |
| `car_version` | `TEXT` | `telemetry.Snapshot.CarVersion` (`string`) |
| `inside_temp_c` | `DOUBLE PRECISION` | `telemetry.Snapshot.InsideTempC` (`float64`) |
| `outside_temp_c` | `DOUBLE PRECISION` | `telemetry.Snapshot.OutsideTempC` (`float64`) |
| `charging_state` | `TEXT` | `telemetry.Snapshot.ChargingState` (`string`) |
| `charge_limit_soc_pct` | `INTEGER` | `telemetry.Snapshot.ChargeLimitSocPct` (`int`) |
| `captured_at` | `TIMESTAMPTZ` | `telemetry.Snapshot.CapturedAt` (`time.Time`) |

The ticket's `outsite_temp_c` is a typo; the column is `outside_temp_c`, matching the
`_c` suffix convention and the source field's actual name (roadmap D1, restated here
for the schema block's own completeness).

**Rejected — a JSONB blob of "extra vehicle fields" instead of eight typed columns**:
this table's existing convention is typed columns for hot reads (roadmap precedent,
`ai/architecture.md` §7 "Extract typed columns for hot reads"); a blob would force
`LatestMetricsByAccount` to extract on every read, which this project's own
conventions call out as the thing never to do on a hot path. `vehicle_metrics` has no
`raw_data JSONB` to begin with — it stores a Go-computed/copied conclusion, not an
external API response (the original migration's own "No raw_data" rationale), so a
JSONB fallback here would also be a new precedent this table deliberately avoids.

### D2 — Nullability, no backfill, `sentry_mode`'s new ambiguity (carries roadmap D2)

All eight columns are nullable. One migration, no backfill, no watermark reset —
existing rows keep all eight NULL forever. Accepted consequence: for up to one
nightly cycle after deploy, the latest `vehicle_metrics` row for a vehicle predates
this migration and `LatestMetricsByAccount` returns nil pointers for all eight new
fields on that row; it self-heals on the vehicle's next `Reconcile`.

`sentry_mode`'s NULL is now ambiguous — it means *either* "the vehicle did not report
sentry" (the meaning it already carries on `telemetry.Snapshot.SentryMode`/
`vehicle_snapshots.sentry_mode`) *or* "this row predates the RM38 migration." The
other seven columns' NULL means only the second case, because none of them was ever
nullable at the source: `telemetry.Snapshot.Locked`, `.CarVersion`, `.InsideTempC`,
`.OutsideTempC`, `.ChargingState`, `.ChargeLimitSocPct` and `.CapturedAt` are all
plain (non-pointer) fields — telemetry always has a value for them once it has a
snapshot row at all. `sentry_mode` is the one exception because its *source* field is
already `*bool` (Source A enrichment field, `telemetry.go`'s own doc comment: "nil =
not reported"). This ambiguity is written into the column's own `COMMENT ON COLUMN`
(see "Database Changes" below) — it is a documentation obligation, not a code change;
no Go logic in this tier disambiguates it (tier 2's badge table, roadmap D5, treats
NULL as "no badge" either way, which is correct under both readings).

**Rejected — `NOT NULL` + SQL backfill joining `vehicle_snapshots`**: a cross-module
database read from an `internal/analytics` migration, forbidden by
`ai/architecture.md` §2 (roadmap's own rejected alternative, restated here because it
bears directly on this tier's migration). **Rejected — a watermark reset** (the
`20260822000002_reset_vehicle_metric_watermarks.sql` mechanism, which forces every
vehicle's next `Reconcile` to re-derive its whole history): proven and available, but
the owner chose the minimal migration (roadmap D2) — deferred to backlog §22.

### D3 — Copy-verbatim semantics: all eight always populated from `cur`, regardless of predecessor

**This is a new decision this design pass makes — the roadmap does not settle it.**
`deriveVehicleMetrics` (`consumed.go`) branches on whether `prev` (the day's
predecessor snapshot) is nil: a predecessor-less day gets a row with only the three
pre-existing raw observations populated, every one of the five `_calc` columns left
nil, and `Flagged` forced `false` (`vehicle_metrics`' existing D9 rule). The eight new
columns do **not** follow the `_calc` columns' branch — they are populated from `cur`
(the day's own snapshot) in **both** branches, exactly like the three pre-existing raw
observations already are.

**Why:** `telemetry.Snapshot.Locked`, `.SentryMode`, `.CarVersion`, `.InsideTempC`,
`.OutsideTempC`, `.ChargingState`, `.ChargeLimitSocPct` and `.CapturedAt` are properties
of the day's own capture — they describe what the vehicle reported that day, not a
delta against a prior day the way the five `_calc` columns do. Nothing about them
requires a predecessor to compute; withholding them from a predecessor-less row would
be a strictly worse answer for no correctness reason, and would reintroduce exactly
the "first day silently has less data than every later day" asymmetry the dense-table
design (RM29 D9) exists to avoid for the columns that *can* be computed unconditionally.

**Rejected — mirror the `_calc` columns' nil-on-no-predecessor branch for these eight
too**: would leave `VehicleStatus` blank for `locked`/`sentry_mode`/etc. on a
vehicle's first-ever tracked day even though `telemetry.Snapshot` has real values for
all eight on that same row — a self-inflicted gap with no source data justifying it.
**Rejected — a third row-shape/branch just for these eight**: `deriveVehicleMetrics`
already has exactly two branches (predecessor / no predecessor); folding the eight new
fields into the "populate what `cur` gives you regardless" set the three existing raw
observations already belong to needs no new branch at all — same code path, more
fields.

### D4 — `VehicleStatus`: an analytics-owned domain type, never `telemetry.Snapshot`

New type in `analytics.go` (alongside `Efficiency`/`DayConsumption`/`DayDistance` —
no vendor or sibling-module suffix, `ai/architecture.md` §6):

```go
type VehicleStatus struct {
    TeslaID           int64
    BatteryLevelPct   int
    BatteryRangeKm    float64
    OdometerKm        float64
    InsideTempC       *float64
    OutsideTempC      *float64
    Locked            *bool
    SentryMode        *bool
    CarVersion        *string
    ChargingState     *string
    ChargeLimitSocPct *int
    CapturedAt        *time.Time
}
```

`Locked` is `*bool`, not `bool` — `telemetry.Snapshot.Locked` is a plain `bool`, but
D2 makes the column nullable, so the analytics-side type must be able to represent
"not yet recomputed since this migration" distinctly from a real `false` (roadmap D5's
explicit constraint on this tier, even though the badge logic that consumes the
distinction is tier 2's). `SentryMode` stays `*bool` exactly as it already is
everywhere else in this module (`telemetry.Snapshot.SentryMode`,
`DayConsumption`-adjacent code) — no representational change, just a new place it
flows through.

**D3 — coverage check (roadmap D3, verified against the type above):** the twelve
fields roadmap D3 lists as the union every gateway call site reads —
`BatteryLevelPct`, `BatteryRangeKm`, `OdometerKm`, `InsideTempC`, `OutsideTempC`,
`Locked`, `SentryMode`, `CarVersion`, `ChargingState`, `ChargeLimitSocPct`,
`CapturedAt`, `TeslaID` — are all present on `VehicleStatus` above, one field each,
no omissions and no extras beyond what roadmap D3 named. Confirmed by direct
enumeration, not by inference from the ticket.

**Rejected — returning `telemetry.Snapshot` directly (or a type aliasing it)**: this
module already imports `telemetry.Snapshot` (for `Recalculate`'s own reads), so it
would compile — but returning it from a `Reader` port method would leak a sibling
module's domain type through this module's public port, exactly the anti-corruption
violation `ai/architecture.md` §6 exists to prevent ("our own domain models carry no
vendor/sibling suffix... the adapter's job includes mapping before data flows into
domain logic"). `VehicleStatus` is analytics' own model over analytics' own table,
built from data `telemetry.Snapshot` originally supplied, once, at `Recalculate`-time
— not a live pass-through.

### D5 — Method name and shape: `LatestMetricsByAccount`, mirroring `LatestSnapshotsByAccount`

```go
// LatestMetricsByAccount returns the latest precomputed vehicle_metrics row for
// each vehicle belonging to the given account, as VehicleStatus — the
// analytics-owned equivalent of telemetry.Reader.LatestSnapshotsByAccount (never
// telemetry.Snapshot itself, ai/architecture.md §6). "Latest" means the row with
// the greatest metric_date for that (account_id, tesla_id) — vehicle_metrics'
// grain is a calendar day, not a capture instant, so this describes the vehicle's
// most recently RECALCULATED day, which is typically yesterday (metric_date is
// the snapshot's effective day, recalculate.go). If the account has no stored
// vehicle_metrics rows it returns an empty (non-nil) slice and a nil error, same
// contract as LatestSnapshotsByAccount. Order of the returned slice is
// unspecified.
LatestMetricsByAccount(ctx context.Context, accountID uuid.UUID) ([]VehicleStatus, error)
```

Added to `analytics.Reader` in `analytics.go`, implemented in `reader.go`.

### D6 — Query shape: `DISTINCT ON (tesla_id) ... ORDER BY tesla_id, metric_date DESC`

New sqlc query, `LatestVehicleMetricsByAccount`:

```sql
-- name: LatestVehicleMetricsByAccount :many
SELECT DISTINCT ON (tesla_id)
    tesla_id, battery_level_pct, battery_range_km, odometer_km,
    inside_temp_c, outside_temp_c, locked, sentry_mode, car_version,
    charging_state, charge_limit_soc_pct, captured_at
FROM vehicle_metrics
WHERE account_id = @account_id
ORDER BY tesla_id, metric_date DESC;
```

This is a direct structural mirror of `telemetry.Reader.LatestSnapshotsByAccount`'s
own query (`internal/telemetry/db/query.sql`'s `LatestSnapshotsByAccount`:
`SELECT DISTINCT ON (tesla_id) ... WHERE account_id = @account_id ORDER BY tesla_id,
captured_at DESC`) — same `DISTINCT ON` shape, same account-scoping predicate, same
tie-break column role (`captured_at` there, `metric_date` here — each table's own
"latest" axis). See "Index Plan" below for why the existing index serves it exactly
as `(account_id, tesla_id, captured_at)` already serves telemetry's identical query.

`reader.go`'s `vehicleMetricsStore` narrow consumer interface (already scoped to
exactly the `analyticsdb.Queries` methods `ConsumedByDay`/`OdometerDeltaByDay` use)
gains one more method, `LatestVehicleMetricsByAccount`, following the same pattern —
`*analyticsdb.Queries` satisfies it automatically, no adapter needed.

### D7 — Migration shape: additive `ALTER TABLE ... ADD COLUMN`, no new table, no rewrite

Eight `ADD COLUMN` statements against the existing `vehicle_metrics` table, each
nullable with no `DEFAULT`. On Postgres 11+, adding a nullable column with no default
is a metadata-only change — no table rewrite, no long-held lock, safe against a live
table under this project's read-heavy profile. This is the same reasoning that made
`20260821000002_add_vehicle_metric_watermarks.sql` (a sibling migration) uneventful;
here it applies to an `ALTER` rather than a `CREATE`, but the safety property is
identical: no existing row is touched, no existing query plan changes shape (a
`SELECT *`-style consumer would see new NULL columns, but this module has none —
every existing query names its columns explicitly).

**Rejected — a `DEFAULT false`/`DEFAULT ''`/`DEFAULT now()` on any of the eight**:
would fabricate a value for every existing row (a `locked = false` default on a row
whose actual locked state at that time is genuinely unknown is a lie, not a
convenience) — directly contradicts roadmap D2's explicit "no backfill, stays NULL"
decision. A `DEFAULT` would also turn "predates this migration" into
indistinguishable-from-real-data for exactly the rows this design deliberately leaves
NULL so a consumer can tell the difference.

### D8 — `sentry_mode`'s ambiguity is a documentation obligation, not a code branch

No Go code in this tier disambiguates `sentry_mode`'s two NULL meanings (D2). This is
deliberate: distinguishing them would require this module to know whether a given
`(account_id, tesla_id)` pair has ever had a post-migration `Recalculate` run for it —
information this design does not plan to track (that would be a second watermark-like
mechanism for a distinction no current consumer needs to make; roadmap D5's badge
table already treats "no badge" as the correct rendering for NULL under *either*
reading, so tier 2 has no need to disambiguate either). The `COMMENT ON COLUMN` is the
entire mitigation this tier provides — a future consumer that DOES need to
disambiguate reads the comment and knows to check `captured_at` (also nullable, from
this same migration, and NULL under only the "predates migration" reading) as a
proxy: `sentry_mode IS NULL AND captured_at IS NOT NULL` unambiguously means "not
reported"; `sentry_mode IS NULL AND captured_at IS NULL` unambiguously means
"predates this migration." Recorded here so a future design pass does not have to
re-derive it.

## Database Changes (design gate — full schema, rationale, index plan)

### Schema: `vehicle_metrics` (ALTER, not CREATE)

```sql
ALTER TABLE vehicle_metrics
    ADD COLUMN locked                BOOLEAN,
    ADD COLUMN sentry_mode           BOOLEAN,
    ADD COLUMN car_version           TEXT,
    ADD COLUMN inside_temp_c         DOUBLE PRECISION,
    ADD COLUMN outside_temp_c        DOUBLE PRECISION,
    ADD COLUMN charging_state        TEXT,
    ADD COLUMN charge_limit_soc_pct  INTEGER,
    ADD COLUMN captured_at           TIMESTAMPTZ;

CREATE INDEX idx_vehicle_metrics_latest
    ON vehicle_metrics (account_id, tesla_id, metric_date DESC);
```

No new constraint, and no change to `vehicle_metrics_account_tesla_date_unique` or either
`CHECK` constraint already on the table (D1). One new index — see "Index Plan" below; it
was added at the database design gate by the owner's explicit instruction, revising this
document's original "no new index" conclusion.

Column comments (the RM38-specific obligation D2/D8 create):

```sql
COMMENT ON COLUMN vehicle_metrics.locked IS
    'Copied verbatim from telemetry.Snapshot.Locked (no re-derivation). Always populated '
    'from the day''s own snapshot, independent of predecessor existence (design D3) -- unlike '
    'the five _calc columns, this is a raw per-day observation, not a delta. NULL means only '
    '"this row predates the RM38-analytics-add-vehicle-status-columns migration" -- no '
    'backfill was run (roadmap D2); it never means "not reported" (the source field is a '
    'plain bool, always populated once telemetry has a snapshot at all).';
COMMENT ON COLUMN vehicle_metrics.sentry_mode IS
    'Copied verbatim from telemetry.Snapshot.SentryMode (no re-derivation). Always populated '
    'from the day''s own snapshot, independent of predecessor existence (design D3). '
    'AMBIGUOUS NULL, unlike every other column added by this migration: NULL means EITHER '
    '"the vehicle did not report sentry" (SentryMode''s existing meaning on '
    'telemetry.Snapshot/vehicle_snapshots -- a *bool source field) OR "this row predates the '
    'RM38 migration" (roadmap D2, no backfill). Disambiguate via captured_at (also added by '
    'this migration): sentry_mode IS NULL AND captured_at IS NOT NULL means "not reported"; '
    'both NULL means "predates this migration" (design D8). No current consumer needs to '
    'disambiguate -- tier 2''s badge rendering (roadmap D5) treats NULL as "no badge" under '
    'either reading.';
COMMENT ON COLUMN vehicle_metrics.car_version IS
    'Copied verbatim from telemetry.Snapshot.CarVersion. Always populated from the day''s own '
    'snapshot, independent of predecessor existence (design D3). NULL means only "this row '
    'predates the RM38 migration" (roadmap D2) -- the source field is a plain string, never '
    'nil once telemetry has a snapshot.';
COMMENT ON COLUMN vehicle_metrics.inside_temp_c IS
    'Copied verbatim from telemetry.Snapshot.InsideTempC (already Celsius -- Tesla reports '
    'temps in Celsius natively, no conversion at any layer). Always populated from the day''s '
    'own snapshot, independent of predecessor existence (design D3). NULL means only "this '
    'row predates the RM38 migration" (roadmap D2).';
COMMENT ON COLUMN vehicle_metrics.outside_temp_c IS
    'Copied verbatim from telemetry.Snapshot.OutsideTempC. Same always-populated and NULL '
    'semantics as inside_temp_c above. The ticket (MAG-12) misspelled this column '
    '"outsite_temp_c" -- the correct name, matching the _c display-unit suffix convention '
    'and the source field''s own name, is outside_temp_c (roadmap D1).';
COMMENT ON COLUMN vehicle_metrics.charging_state IS
    'Copied verbatim from telemetry.Snapshot.ChargingState. Always populated from the day''s '
    'own snapshot, independent of predecessor existence (design D3). NULL means only "this '
    'row predates the RM38 migration" (roadmap D2). Added beyond the ticket''s original five '
    'columns because the gateway''s dashSubtitle needs it for the card subtitle (roadmap D1).';
COMMENT ON COLUMN vehicle_metrics.charge_limit_soc_pct IS
    'Copied verbatim from telemetry.Snapshot.ChargeLimitSocPct. Always populated from the '
    'day''s own snapshot, independent of predecessor existence (design D3). NULL means only '
    '"this row predates the RM38 migration" (roadmap D2). Added beyond the ticket''s original '
    'five columns for the Battery card''s "Limit N%" line (roadmap D1).';
COMMENT ON COLUMN vehicle_metrics.captured_at IS
    'Copied verbatim from telemetry.Snapshot.CapturedAt -- the exact capture instant, distinct '
    'from metric_date (this row''s own effective CALENDAR DAY, one day before CapturedAt''s own '
    'calendar day). Always populated from the day''s own snapshot, independent of predecessor '
    'existence (design D3). NULL means only "this row predates the RM38 migration" (roadmap '
    'D2). Added beyond the ticket''s original five columns for the gateway''s LastUpdated/ '
    'IsStale freshness badge (roadmap D1) -- also doubles as the disambiguation proxy for '
    'sentry_mode''s ambiguous NULL (design D8).';
```

**No FK, no `raw_data JSONB` change** — this ALTER inherits both existing decisions
from the original `vehicle_metrics` migration unchanged (this tier adds columns to an
existing, already-justified table; it does not revisit either call).

### Index Plan

> **Revised at the database design gate (owner's explicit, one-item change).** This section
> originally concluded "no new index", on the reasoning preserved verbatim below. The owner
> reviewed that reasoning together with its stated imprecision — see "What the existing index
> does and does not buy" — and directed that
> `idx_vehicle_metrics_latest (account_id, tesla_id, metric_date DESC)` be added. That index
> is now part of this tier's migration (task 1.1). The original reasoning is kept rather than
> deleted because it is still correct about what the *existing* index serves; what changed is
> the decision about whether the residual sort is worth an index write, and that call is the
> owner's.

**What the new index buys, precisely — and what it does not.** `LatestMetricsByAccount`
orders by `tesla_id ASC, metric_date DESC`. A btree cannot be walked in mixed direction, so
against the existing all-ascending `(account_id, tesla_id, metric_date)` index Postgres must
add an **incremental sort** on top of the index scan. `idx_vehicle_metrics_latest` matches the
ORDER BY exactly, so that sort disappears and the plan becomes a plain ordered index scan.

It does **not** reduce the number of rows scanned. Postgres has no loose/skip index scan, so
either way the query reads every `vehicle_metrics` row belonging to the account and collapses
them with `DISTINCT ON` — cost grows linearly with the account's history, not with its vehicle
count. Removing that scan would require passing the caller's known `tesla_id` list and a
`LATERAL` join per vehicle; that is **not** done here, because the vehicle list is
`internal/account`'s data and pulling it into this port's signature to satisfy a query plan
would trade a module boundary for a micro-optimisation (`ai/architecture.md` §2). Revisit only
if an account's row count ever makes the scan measurable.

**Write cost, accepted:** every `Recalculate`/`Reconcile` UPSERT now maintains a second index.
Under the declared read-heavy Performance-Profile — writes run in a midnight poller, reads are
on the dashboard hot path that roadmap D3 puts on four pages — this is the trade the profile
explicitly licenses.

---

**Original reasoning (superseded on the conclusion, still accurate on the premise):**

**No new index.** The one new read pattern this tier introduces —
`LatestMetricsByAccount`'s `SELECT DISTINCT ON (tesla_id) ... WHERE account_id = $1
ORDER BY tesla_id, metric_date DESC` — is served by the table's own existing
`vehicle_metrics_account_tesla_date_unique` index (`UNIQUE (account_id, tesla_id,
metric_date)`), for the identical reason `telemetry`'s `LatestSnapshotsByAccount`
query is already proven to be served by its own `(account_id, tesla_id, captured_at)`
index (`internal/telemetry/db/query.sql`'s own comment on that query, and
`internal/telemetry/db/migrations/20260710000002_init_telemetry.sql`'s index): the
account_id equality predicate narrows to one tenant's rows, and within that the
`(tesla_id, metric_date)` column order lets Postgres satisfy `DISTINCT ON (tesla_id)
... ORDER BY tesla_id, metric_date DESC` as one ordered index scan per `tesla_id`
group, picking the first (highest `metric_date`) row of each group — the same "one
index scan, no N+1" property `LatestSnapshotsByAccount`'s own comment already claims
for the structurally identical query over the structurally identical index shape.

| # | Read pattern | Served by |
|---|---|---|
| 1 | `LatestMetricsByAccount`: `WHERE account_id = $1`, latest row per `tesla_id`, `ORDER BY tesla_id, metric_date DESC` | **`idx_vehicle_metrics_latest` (added at the design gate)** — matches the ORDER BY exactly, so no sort node. Previously assessed as served by `vehicle_metrics_account_tesla_date_unique`'s own index — `account_id` leads (this project's "account_id is the leading index column" convention, `ai/go-conventions.md` §Read optimization), and `(tesla_id, metric_date)` matches the `DISTINCT ON`/`ORDER BY` clause exactly, mirroring `LatestSnapshotsByAccount`'s identical proof over `(account_id, tesla_id, captured_at)` |
| 2 | `Recalculate`'s UPSERT / DELETE (unchanged by this tier — same eight-column-wider row, same conflict target) | Same index, unchanged (RM29's original Index Plan #2/#3) |
| 3 | `ConsumedByDay`/`OdometerDeltaByDay` (unchanged, untouched by this tier) | Same index, unchanged (RM29's original Index Plan #1) |

**Deliberately not added:** a dedicated `(account_id, metric_date DESC)` index. Same
reasoning the original migration already gave for not adding one: no consumer in
this tier — or any tier — issues an account-wide, all-vehicles-collapsed-to-one-row
query; `LatestMetricsByAccount` still resolves per-`tesla_id` rows (many rows per
account, one per vehicle), which is exactly the access pattern the existing index
already leads with `account_id` then `tesla_id` to serve. Adding a second index here
would slow every `Recalculate`/`Reconcile` UPSERT for zero benefit to a query pattern
this tier does not have.

## Test Contract (authored before implementation, per `ai/go-conventions.md`)

Two fixtures, covering the two branches D3 settles (predecessor exists / does not),
plus the multi-vehicle `DISTINCT ON` case `LatestMetricsByAccount` exists to serve.
All three fixtures extend Fixture A/C's shape from the archived
`RM29-analytics-add-vehicle-metrics` design.md (same accountID `A`, teslaID `42`
convention) rather than inventing new ones, since the eight new columns are additive
to rows that fixture already fully describes.

### Fixture RM38-A — a normal day, predecessor exists, all eight populated

Same two snapshots as the archived RM29 Fixture A (`accountID=A`, `teslaID=42`,
predecessor `2026-08-10`/current `2026-08-11`), extended with the eight new fields on
the **current** snapshot only (the predecessor's values are irrelevant here — the
eight columns copy from `cur`, never from `prev`):

| Field on `cur` (`telemetry.Snapshot`) | Value |
|---|---|
| `Locked` | `true` |
| `SentryMode` | `*bool` pointing to `false` |
| `CarVersion` | `"2026.28.4"` |
| `InsideTempC` | `21.5` |
| `OutsideTempC` | `18.0` |
| `ChargingState` | `"Disconnected"` |
| `ChargeLimitSocPct` | `80` |
| `CapturedAt` | `2026-08-11T03:31:00Z` |

**Expected `vehicle_metrics` row** (`Recalculate(A, 42, 2026-08-10, 2026-08-10)`,
`metric_date = 2026-08-10` as in the original fixture) — every RM29 column value is
unchanged from the archived fixture; the eight new columns are:

| Column | Value |
|---|---|
| `locked` | `true` |
| `sentry_mode` | `false` (not NULL — a real reported value) |
| `car_version` | `'2026.28.4'` |
| `inside_temp_c` | `21.5` |
| `outside_temp_c` | `18.0` |
| `charging_state` | `'Disconnected'` |
| `charge_limit_soc_pct` | `80` |
| `captured_at` | `2026-08-11T03:31:00Z` |

**Expected `LatestMetricsByAccount(A)`** (assuming this is the only/latest row for
`teslaID=42` in account `A`): contains one `VehicleStatus{TeslaID: 42,
BatteryLevelPct: 65, BatteryRangeKm: 280.0, OdometerKm: 1050.0, InsideTempC: ptr(21.5),
OutsideTempC: ptr(18.0), Locked: ptr(true), SentryMode: ptr(false), CarVersion:
ptr("2026.28.4"), ChargingState: ptr("Disconnected"), ChargeLimitSocPct: ptr(80),
CapturedAt: ptr(2026-08-11T03:31:00Z)}` — every pointer field non-nil.

### Fixture RM38-B — a vehicle's first-ever snapshot, eight new fields STILL populated

Same single snapshot as the archived RM29 Fixture C (`accountID=A`, `teslaID=42`, no
predecessor at all, `CapturedDate = 2026-08-05`), extended with the eight new fields:

| Field on `cur` | Value |
|---|---|
| `Locked` | `false` |
| `SentryMode` | `nil` (vehicle did not report sentry this capture) |
| `CarVersion` | `"2026.28.4"` |
| `InsideTempC` | `19.0` |
| `OutsideTempC` | `14.0` |
| `ChargingState` | `"Charging"` |
| `ChargeLimitSocPct` | `90` |
| `CapturedAt` | `2026-08-05T03:30:15Z` |

**Expected `vehicle_metrics` row** (`Recalculate(A, 42, 2026-08-04, 2026-08-04)`,
`metric_date = 2026-08-04` as in the original fixture) — every RM29 `_calc`/`consumed_pct`
column is still `NULL` and `flagged` is still `false` (D9 of the archived design,
untouched by this tier). The eight new columns:

| Column | Value |
|---|---|
| `locked` | `false` (NOT NULL — populated even on a predecessor-less row, design D3) |
| `sentry_mode` | `NULL` (the vehicle genuinely did not report sentry this capture — the "not reported" reading of the ambiguous NULL, design D2/D8, not "predates migration") |
| `car_version` | `'2026.28.4'` |
| `inside_temp_c` | `19.0` |
| `outside_temp_c` | `14.0` |
| `charging_state` | `'Charging'` |
| `charge_limit_soc_pct` | `90` |
| `captured_at` | `2026-08-05T03:30:15Z` |

This is the fixture that proves D3: a predecessor-less row's `_calc` columns are NULL
(unchanged RM29 behavior) but its eight new columns are fully populated — the two
column groups follow different rules on the identical row, and this is the test that
would fail if a future edit accidentally folded the eight new columns into the
`_calc` columns' nil-on-no-predecessor branch.

**Expected `LatestMetricsByAccount(A)`** for this row (if it is the latest for
`teslaID=42`): `VehicleStatus{..., SentryMode: nil, Locked: ptr(false), ...}` — note
`SentryMode` is the one field that comes back `nil` here even though every other
pointer field is non-nil, demonstrating the genuine "not reported" reading is
representable and distinct from a pre-migration row (Fixture RM38-C below).

### Fixture RM38-C — a pre-migration row (both ambiguous and unambiguous NULLs)

A `vehicle_metrics` row written before this migration exists — i.e., only the
pre-existing columns (`battery_level_pct`, `odometer_km`, `battery_range_km`, and
whatever RM29 columns applied) are populated; all eight new columns are `NULL` because
the row predates them, not because `Recalculate` chose to leave them NULL.

**Expected `LatestMetricsByAccount(A)`** for this row: `VehicleStatus{TeslaID: 42,
BatteryLevelPct: <existing>, BatteryRangeKm: <existing>, OdometerKm: <existing>,
InsideTempC: nil, OutsideTempC: nil, Locked: nil, SentryMode: nil, CarVersion: nil,
ChargingState: nil, ChargeLimitSocPct: nil, CapturedAt: nil}` — every one of the eight
new fields nil, none fabricated. This is the direct proof of roadmap D2's accepted
consequence: a dashboard reading this row (via tier 2, not built in this tier) would
render blanks for locked/sentry/temps/etc. until this vehicle's next `Reconcile`.

### Multi-vehicle `DISTINCT ON` case

Given two vehicles (`teslaID=42` and `teslaID=99`) both registered to account `A`,
each with its own most-recent `vehicle_metrics` row on a different `metric_date`:
`LatestMetricsByAccount(A)` returns exactly two `VehicleStatus` entries, one per
`teslaID`, each carrying that vehicle's own latest row's values — never a row from an
earlier `metric_date` for either vehicle, and never a cross-account row from a
different account's vehicle sharing the same `teslaID` value (defense-in-depth tenant
isolation, mirroring every other query's `account_id` scoping in this module).

## Rollout note

This is an `ALTER TABLE ADD COLUMN` migration only — there is no data migration step
and no task in this tier that invokes `Reconcile` for existing vehicles (contrast with
the original `vehicle_metrics` migration's own rollout note, which DID require a
backfill-via-`Reconcile` pass because that migration introduced the table itself).
Every vehicle's next regular nightly `Reconcile` run populates the eight new columns
for that vehicle going forward — no manual intervention required, and none authorized
by roadmap D2's own "no backfill" decision.

## Risks / Trade-offs

- **`LatestMetricsByAccount` has no caller until tier 2 lands.** Tier order is forced
  (roadmap D7) — tier 2 depends on this tier's artifacts, so this is a temporarily
  "dead" port method, not a design flaw. `go vet`/`go build` do not flag an unused
  exported method, so this carries no build-signal risk either.
- **`sentry_mode`'s ambiguous NULL is a real, if narrow, footgun for a FUTURE
  consumer** who does not read the column comment (D8). Mitigated by making the
  disambiguation proxy (`captured_at`) explicit in the comment itself rather than
  leaving it to be re-derived, and by confirming (D8) that no consumer in this
  roadmap needs to make the distinction at all.
- **Eight new nullable columns widen every `UpsertVehicleMetric` row** — a handful of
  extra bytes and eight more `SET` clauses in an already-idempotent UPSERT run at most
  once per vehicle-day, off the hot path (nightly batch + rare manual-charge writes).
  No measurable cost under the read-heavy Performance-Profile's write-side latitude.
- **A vehicle's `CarVersion`/`ChargingState`/etc. is now duplicated in two tables**
  (`vehicle_snapshots` via `telemetry`, `vehicle_metrics` via `analytics`) — the same,
  already-accepted trade-off roadmap D1 makes explicitly for the original three raw
  observations ("duplicating observation columns... is intentional, not a smell" —
  RM29 proposal.md). This tier extends an already-approved pattern; it does not
  introduce a new one.

## Verification signals

Per the Test-Execution-Policy: the implementing worker runs and reports `go build
./...`, `go vet ./...`, `gofmt -l`, `make build`, `make vet`, `make bins`, and the
standalone guards (`make ui-guard` is a no-op here — no gateway code touched;
`make i18n-guard` is a no-op — no user-facing string added; `make money-guard`/
`make tz-guard` are no-ops — no monetary or raw-time-zone code touched;
`make migration-guard` — the new migration must pass it) — never `go test ./...` /
`make test` / `make test-with-db` / `make check`. The owner runs
`go test ./internal/analytics/...` (offline + `DATABASE_URL`-gated) and reports
results; until then this tier's implementation status is **awaiting-user-verification**,
never "done."
