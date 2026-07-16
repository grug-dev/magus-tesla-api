## Context

Tier 2 (charging stats) of the RM2-charging-stats roadmap. Tier 1
(`RM2-tesla-add-charging-history`) is archived and shipped. This tier extends `internal/telemetry`
with two sources:

- **Source B** — a new `supercharger_sessions` table backed by `tesla.VehicleService.ChargingHistory`.
- **Source A** — six new nullable extracted columns on the existing `vehicle_snapshots` table, read
  from the `ChargeState` block of an existing nightly `vehicle_data` fetch.

The design session (grill-me, 2026-07-16) resolved all DB decisions below. The section is
final — do not re-open it.

Ports this tier consumes (already exist, no changes from the telemetry worker):

- `tesla.VehicleService.ChargingHistory(ctx, creds, ChargingHistoryParams) (*ChargingHistoryTesla, error)` —
  account-scoped, no vehicle wake. Returns `[]ChargingSessionTesla`, each with `Fees []ChargingFeeTesla`.
- `account.Service.AllRegisteredVehicles(ctx) ([]OwnedVehicle, error)` — provides `AccountID`,
  `TeslaID`, and `VIN` per vehicle.
- `account.Service.AccessTokenFor(ctx, accountID) (string, error)` — per-account token already
  resolved by `collectAccount`.

## Goals / Non-Goals

**Goals:**

- Persist all Tesla-billed Supercharger sessions per account as a durable ledger with derived cost
  and energy summaries for cheap dashboard reads.
- Enrich `vehicle_snapshots` with six charge-telemetry fields observable at the 03:30 snapshot
  (home/AC charging visibility).
- Expose a new `SuperchargerReader` port (separate from the existing `Reader`) with two read
  methods shaped for dashboard access patterns.
- Extend `CycleReport` with Supercharger-specific counters.
- Preserve every existing non-negotiable: raw JSONB + typed columns; no cross-module DB access;
  no HTML; `pgtype` confined to the DB boundary.

**Non-Goals:**

- Home/AC charging inference from snapshot energy-added deltas.
- Summary/aggregation tables (deferred until the gateway tier).
- Retention policy or partitioning for `supercharger_sessions`.
- Tier-1 review finding R1 (percent-encode date params) — this tier passes no date params.

---

## Source B — `supercharger_sessions` table

### DBS1 — Full schema

```sql
CREATE TABLE supercharger_sessions (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id              BIGINT NOT NULL,          -- Tesla's globally-unique session id
    account_id              UUID NOT NULL,            -- owning account (multi-tenant scope)
    vin                     TEXT NOT NULL,            -- durable vehicle key from the session
    tesla_id                BIGINT,                   -- resolved from account's owned vehicles; NULL when VIN is not a current registered vehicle
    site_location_name      TEXT NOT NULL,
    country_code            TEXT NOT NULL,
    charge_start_date_time  TIMESTAMPTZ NOT NULL,
    charge_stop_date_time   TIMESTAMPTZ NOT NULL,
    unlatch_date_time       TIMESTAMPTZ,              -- nullable: not always present in Tesla response
    billing_type            TEXT NOT NULL,
    vehicle_make_type       TEXT NOT NULL,

    -- DERIVED AT WRITE TIME (computed from fees[]; refreshed on upsert)
    energy_kwh              DOUBLE PRECISION,         -- sum of usageBase + usageTier1..4 where lower(uom) = 'kwh'; NULL if no kWh fee
    total_cost              DOUBLE PRECISION,         -- sum of totalDue over all fees; NULL if fees empty
    currency                TEXT,                     -- currencyCode from first fee; NULL if fees empty
    is_paid                 BOOLEAN,                  -- logical AND of every fee's isPaid; NULL if no fees

    raw_data                JSONB NOT NULL,           -- whole session object (fees[] + invoices[]) lossless

    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),  -- first-seen; never updated
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()   -- refreshed on every upsert
);

COMMENT ON TABLE supercharger_sessions IS
    'Tesla-billed Supercharger and DC fast-charging sessions per account. '
    'Covers sessions returned by GET /api/1/dx/charging/history only '
    '(no home/AC charging, no battery percentage). '
    'Owned by internal/telemetry; no other module reads this table directly.';

-- Dedup / upsert target: session_id is Tesla''s globally unique session id
CONSTRAINT supercharger_sessions_session_id_unique UNIQUE (session_id)

-- Per-vehicle time-series read (main dashboard: show sessions for this vehicle, newest first)
CREATE INDEX idx_supercharger_sessions_vehicle_time
    ON supercharger_sessions (account_id, tesla_id, charge_start_date_time DESC);

-- Account-wide spend/energy dashboard (all sessions for this account, newest first)
-- Also covers (account_id, vin) orphan fallback via the account_id prefix
CREATE INDEX idx_supercharger_sessions_account_time
    ON supercharger_sessions (account_id, charge_start_date_time DESC);
```

**No FK on `account_id` or `tesla_id`:** cross-module FK from `supercharger_sessions` into the
account module's `accounts` or `vehicles` tables is exactly the coupling `ai/architecture.md` §2
forbids — it would couple telemetry migrations to the account schema. Referential integrity is
upheld by the flow: the only writer resolves `account_id` from `account.AllRegisteredVehicles`
and resolves `tesla_id` from the same call's `OwnedVehicle.TeslaID` matched by VIN. `tesla_id`
is NULL when the session's VIN does not match any currently-registered vehicle (sold/removed car)
— the row is kept, the VIN is preserved (still identifies the car historically).

### DBS2 — Derived columns: rationale + derivation rules

Tesla's `dx/charging/history` endpoint does NOT expose a clean top-level `kWh` or `totalCost`
field. All financial and energy data lives inside `fees[]`, where each fee carries:
- `uom` — the unit of measure for the usage tiers (e.g. `"kwh"`, `"min"`, `"kwh_minute"`).
- `usageBase`, `usageTier1..4` — tier usage amounts in the fee's `uom`.
- `totalDue` — total charged for this fee.
- `isPaid` — billing status.

**`energy_kwh` derivation:**
```
energy_kwh = SUM( usageBase + usageTier1 + usageTier2 + usageTier3 + usageTier4 )
             WHERE lower(fee.uom) = 'kwh'
             TREATING nil tiers as 0
```
NULL when no fee has `uom = 'kwh'` (time-based billing only, or fees empty).

**`total_cost` derivation:**
```
total_cost = SUM( fee.totalDue ) OVER all fees
```
NULL when fees slice is empty.

**`currency` derivation:**
```
currency = first fee's currencyCode  (uniform across fees for one session)
```
NULL when fees slice is empty.

**`is_paid` derivation:**
```
is_paid = AND( fee.isPaid ) OVER all fees
```
NULL when fees slice is empty (impossible to determine billing status). `*false` when at least
one fee is unpaid; `*true` when all fees are paid.

These derivations are computed in Go at write time (in the domain layer, NOT in SQL), so they
stay testable offline and the logic is not duplicated in a migration trigger. The raw session
JSONB preserves all raw fee values; derivations are just convenience columns for cheap reads.

### DBS3 — UPSERT write path (NOT append-only)

`supercharger_sessions` is the single telemetry table that is NOT append-only. Rationale:
`is_paid`, invoices, and fee `status` legitimately change after a session ends — a session
billed as unpaid at midnight may become paid the next day, and invoices (PDF references) arrive
asynchronously. Re-fetching nightly must therefore update the existing row, not duplicate it.

The upsert target is `(session_id)` (the UNIQUE constraint). The `INSERT ... ON CONFLICT (session_id) DO UPDATE` refreshes ONLY the mutable / derived columns:

```
DO UPDATE SET
    raw_data   = EXCLUDED.raw_data,
    energy_kwh = EXCLUDED.energy_kwh,
    total_cost = EXCLUDED.total_cost,
    currency   = EXCLUDED.currency,
    is_paid    = EXCLUDED.is_paid,
    tesla_id   = EXCLUDED.tesla_id,
    updated_at = now()
```

Immutable columns left untouched on conflict (never updated):
`session_id`, `account_id`, `vin`, `site_location_name`, `country_code`,
`charge_start_date_time`, `charge_stop_date_time`, `unlatch_date_time`,
`billing_type`, `vehicle_make_type`, `created_at`.

**Documentation divergence from `vehicle_snapshots` / `poll_attempts`:** the existing telemetry
tables are append-only (per `ai/architecture.md` §7 convention). `supercharger_sessions` breaks
that convention deliberately and must be documented as such. The reason is that charging session
data is mutable post-session (billing finalizes over time), whereas a vehicle snapshot is a
point-in-time capture that is semantically immutable once stored.

### DBS4 — Index plan + read-performance justification

The **read-heavy profile** (`CLAUDE.md` Performance-Profile) requires every dashboard read to be
fast. Two access patterns drive the index design:

| Pattern | SQL shape | Index used |
|---|---|---|
| All sessions for one vehicle, newest first (main per-vehicle dashboard) | `WHERE account_id = $1 AND tesla_id = $2 ORDER BY charge_start_date_time DESC LIMIT $3` | `idx_supercharger_sessions_vehicle_time` `(account_id, tesla_id, charge_start_date_time DESC)` |
| All sessions for one account, newest first (account-wide spend/energy view) | `WHERE account_id = $1 ORDER BY charge_start_date_time DESC LIMIT $3` | `idx_supercharger_sessions_account_time` `(account_id, charge_start_date_time DESC)` |

`account_id` leads both indexes (multi-tenant convention: every dashboard read scopes by tenant
first — `ai/go-conventions.md` §persistence, `ai/architecture.md` §7.3). The per-vehicle index
adds `tesla_id` as the second column so the planner can satisfy both the WHERE filter and the
ORDER BY in a single range scan without a sort step. The account-wide index omits `tesla_id`
and is also the prefix for any `(account_id, vin)` orphan fallback since `vin` can be added as
an optional post-filter without an index miss (the `account_id` prefix prunes to a small set).

**Why `charge_start_date_time DESC` in the index definition (not ASC):**
Postgres can scan a DESC-indexed column efficiently in either direction, but making the sort
direction explicit matches the query `ORDER BY charge_start_date_time DESC` and avoids a sort
node in the plan for the common newest-first read.

**UNIQUE index on `session_id`:** doubles as the upsert conflict target and as the point-lookup
index for any direct `session_id` query (e.g. de-duplication checks). It is a B-tree index,
created automatically by the UNIQUE constraint.

### DBS5 — SuperchargerSession domain type (Go)

```go
// SuperchargerSession is one Tesla-billed Supercharger / DC fast-charging session — our
// own domain model (no vendor suffix, ai/architecture.md §6). It is distinct from
// tesla.ChargingSessionTesla: that is the vendor DTO; this is the mapped, stored domain record.
// Nullable fields use *T where the column allows NULL (energy, cost, currency, is_paid, tesla_id).
// No Km()/Kmh() companions — none of these fields are distances or speeds.
type SuperchargerSession struct {
    ID                   uuid.UUID
    SessionID            int64       // Tesla's globally-unique session id
    AccountID            uuid.UUID
    VIN                  string
    TeslaID              *int64      // NULL when VIN not a current registered vehicle
    SiteLocationName     string
    CountryCode          string
    ChargeStartDateTime  time.Time
    ChargeStopDateTime   time.Time
    UnlatchDateTime      *time.Time  // NULL when not present in response
    BillingType          string
    VehicleMakeType      string
    EnergyKWh            *float64    // derived; NULL when no kWh fee
    TotalCost            *float64    // derived; NULL when fees empty
    Currency             *string     // derived; NULL when fees empty
    IsPaid               *bool       // derived; NULL when fees empty
    RawData              []byte
    CreatedAt            time.Time
    UpdatedAt            time.Time
}
```

**No `Km()`/`Kmh()` companions**: none of the fields are distances or speeds (energy kWh, currency
amounts, voltage, current, %). The `milesToKm` companion rule applies only to miles/mph fields
(`ai/go-conventions.md` non-negotiable), which are absent here.

### DBS6 — SuperchargerReader port

```go
// SuperchargerReader exposes supercharger session data for read-only consumption. It is a
// separate port from Reader (snapshot-centric) to keep concerns distinct and to allow
// the gateway to depend on only the port it needs. Callers MUST NOT import telemetrydb.
type SuperchargerReader interface {
    // SuperchargerSessionsByAccount returns all Supercharger sessions for the given account,
    // ordered by charge_start_date_time DESC, limited to limit rows (0 = server default).
    // Returns an empty non-nil slice when no sessions exist.
    SuperchargerSessionsByAccount(ctx context.Context, accountID uuid.UUID, limit int) ([]SuperchargerSession, error)

    // SuperchargerSessionsByVehicle returns Supercharger sessions for the given vehicle
    // within the given account, ordered by charge_start_date_time DESC, limited to limit rows.
    // Returns an empty non-nil slice when no sessions exist.
    SuperchargerSessionsByVehicle(ctx context.Context, accountID uuid.UUID, teslaID int64, limit int) ([]SuperchargerSession, error)
}

// NewSuperchargerReader constructs a SuperchargerReader backed by the telemetry DB pool.
func NewSuperchargerReader(pool *pgxpool.Pool) SuperchargerReader
```

**Why a separate port from `Reader`:** the snapshot Reader is consumed by the gateway for the
vehicle dashboard; the `SuperchargerReader` will be consumed by a different gateway handler (cost/
charging dashboard). Merging them would force every caller to depend on methods they don't use,
and would couple the snapshot read tier to the charging read tier in a single interface with no
benefit.

### DBS7 — Collector wiring (into `collectAccount`)

After the existing per-vehicle snapshot loop (valid `creds` already available):

1. Call `s.tsla.ChargingHistory(ctx, creds, tesla.ChargingHistoryParams{})` — zero params = full
   fetch, all pages, no vehicle wake.
2. Build `map[string]int64` from `VIN → TeslaID` using the account's `owned []account.OwnedVehicle`.
3. For each `ChargingSessionTesla` in the result, derive the four computed fields in Go, build a
   `SuperchargerSession`, and call `s.store.upsertSuperchargerSession(ctx, session)`.
4. Accumulate successes in `CycleReport.ChargingSessionsUpserted`.
5. On `ChargingHistory` call failure: log/count it in `CycleReport.ChargingFetchFailures` — do NOT
   abort the account's snapshot collection. A charging-fetch failure is isolated from the vehicle
   snapshot loop. (Same per-account isolation principle as snapshot collection.)

**No poll_attempts row for charging**: `poll_attempts` is per-vehicle; charging is per-account.
Outcomes live in the new `CycleReport` fields.

**`CycleReport` additive extension:**

```go
// ChargingSessionsUpserted is the total number of Supercharger sessions successfully
// upserted across all accounts in this cycle.
ChargingSessionsUpserted int

// ChargingFetchFailures is the number of accounts for which the ChargingHistory call
// failed (network error, 401, etc.). A non-zero value signals partial data.
ChargingFetchFailures int
```

---

## Source A — `vehicle_snapshots` charge enrichment

### DSA1 — Six new nullable columns on `vehicle_snapshots`

```sql
-- Migration: add charge enrichment columns to vehicle_snapshots
ALTER TABLE vehicle_snapshots
    ADD COLUMN charge_energy_added  DOUBLE PRECISION,   -- kWh added this charge session; NULL pre-enrichment
    ADD COLUMN charger_power        INTEGER,            -- kW; NULL pre-enrichment
    ADD COLUMN charger_voltage      INTEGER,            -- V; NULL pre-enrichment
    ADD COLUMN charger_actual_current INTEGER,          -- A; NULL pre-enrichment
    ADD COLUMN usable_battery_level INTEGER,            -- %; NULL pre-enrichment
    ADD COLUMN fast_charger_type    TEXT;               -- e.g. "Tesla", "Combo", NULL pre-enrichment
```

**Why nullable, not NOT NULL with defaults:** pre-enrichment rows (every row written before this
migration) predate extraction of these fields. Adding NOT NULL with a default of `0` or `''`
would falsely represent those historical rows as having had a 0 kWh add, which is actively
misleading. NULL is the correct sentinel: it means "this was not extracted at capture time".
Dashboard code must handle NULL for these columns. Data is recoverable from `raw_data` for any
row where extraction was omitted.

**No backfill:** extracting and backfilling from `raw_data` is a manual, one-time SQL operation
if ever needed. It is out of scope for this change.

**No new index:** these six fields are not expected to be filter/sort columns on the dashboard
hot path. The existing `(account_id, tesla_id, captured_at)` index on `vehicle_snapshots`
continues to serve all read patterns; these are projected columns read alongside the main fetch.

**No `Km()`/`Kmh()` companions on the new fields:** `charge_energy_added` is kWh, `charger_power`
is kW, `charger_voltage` is V, `charger_actual_current` is A, `usable_battery_level` is %, and
`fast_charger_type` is a string — none are distances or speeds expressed in miles or mph. The
`milesToKm` companion rule is inapplicable (`ai/go-conventions.md`).

### DSA2 — `Snapshot` domain type additions

```go
// Charge enrichment fields (nullable — absent for rows captured before this change)
ChargeEnergyAdded    *float64   // kWh; nil when not reported
ChargerPower         *int       // kW; nil when not reported
ChargerVoltage       *int       // V; nil when not reported
ChargerActualCurrent *int       // A; nil when not reported
UsableBatteryLevel   *int       // %; nil when not reported
FastChargerType      *string    // nil when not reported
```

### DSA3 — `snapshotFrom` extension (blocked on leader tesla edit)

```go
// Source A enrichment — fields added in RM2-telemetry-add-charging-stats.
// Requires ChargeEnergyAdded, ChargerPower, ChargerVoltage, ChargerActualCurrent,
// UsableBatteryLevel, FastChargerType on tesla.ChargeStateTesla (leader-added).
// The DTO fields are plain (non-pointer) types, so the Fleet API always supplies a
// concrete value (0 / "" when the vehicle is idle) — NO zero-is-absent heuristic.
// snapshotFrom stores the ACTUAL value on every write, pointer-wrapped so the column is
// non-NULL for every row captured after this change. NULL is reserved for pre-migration
// rows only (see DSA1). ptr(v) returns &v (a tiny generic helper).
ChargeEnergyAdded:    ptr(data.ChargeState.ChargeEnergyAdded),
ChargerPower:         ptr(data.ChargeState.ChargerPower),
ChargerVoltage:       ptr(data.ChargeState.ChargerVoltage),
ChargerActualCurrent: ptr(data.ChargeState.ChargerActualCurrent),
UsableBatteryLevel:   ptr(data.ChargeState.UsableBatteryLevel),
FastChargerType:      ptr(data.ChargeState.FastChargerType),
```

**Why store the actual value (D12, no zero-is-absent heuristic):** a `0`/`""` from a plain DTO
field is a truthful reading — `charge_energy_added = 0` (charge just started), `charger_actual_current
= 0` (plugged in, drawing nothing), `usable_battery_level = 0` (genuinely empty) are all real. A
heuristic that maps `0` → NULL would silently discard those real measurements; interpreting a 0 in
context (e.g. against `charging_state`) is the dashboard's job, not the collector's. Every new row
therefore carries a concrete value; the columns are nullable ONLY so pre-migration rows can stay
NULL (not backfilled, DSA1). On the READ path (DSA4), `pgNullable*` maps a NULL column back to a
`nil` pointer — so a `nil` domain field means "row predates enrichment", never "reported zero".

**CROSS-MODULE DEPENDENCY (leader-owned):** `ChargeStateTesla` in `internal/tesla/types.go` does
NOT yet have these six fields. Source A implementation tasks are blocked until the leader adds:

```go
// RM2-telemetry-add-charging-stats Source A additions
ChargeEnergyAdded    float64 `json:"charge_energy_added"`
ChargerPower         int     `json:"charger_power"`
ChargerVoltage       int     `json:"charger_voltage"`
ChargerActualCurrent int     `json:"charger_actual_current"`
UsableBatteryLevel   int     `json:"usable_battery_level"`
FastChargerType      string  `json:"fast_charger_type"`
```

### DSA4 — `rowToSnapshot` extension

```go
// Charge enrichment (Source A) — nullable columns → domain pointer fields.
ChargeEnergyAdded:    pgNullableFloat64(r.ChargeEnergyAdded),
ChargerPower:         pgNullableInt32AsInt(r.ChargerPower),
ChargerVoltage:       pgNullableInt32AsInt(r.ChargerVoltage),
ChargerActualCurrent: pgNullableInt32AsInt(r.ChargerActualCurrent),
UsableBatteryLevel:   pgNullableInt32AsInt(r.UsableBatteryLevel),
FastChargerType:      pgNullableText(r.FastChargerType),
```

`pgtype.Float8` / `pgtype.Int4` / `pgtype.Text` nullable helpers follow the same `Valid`-field
pattern as `boolPtrToPgBool` already in `service.go`:

```go
func pgNullableFloat64(v pgtype.Float8) *float64 {
    if !v.Valid { return nil }
    return &v.Float64
}
func pgNullableInt32AsInt(v pgtype.Int4) *int {
    if !v.Valid { return nil }
    n := int(v.Int32)
    return &n
}
func pgNullableText(v pgtype.Text) *string {
    if !v.Valid { return nil }
    return &v.String
}
```

---

## Risks / Trade-offs

- **UPSERT diverges from append-only convention.** `supercharger_sessions` is the first telemetry
  table to update existing rows. Documented clearly in schema comment and design. The divergence is
  motivated by billing state mutability; it is not a precedent for `vehicle_snapshots` or
  `poll_attempts`.
- **Full-fetch on every nightly run.** `ChargingHistoryParams{}` (no date filter) fetches all
  pages of history on every run. This is intentional for the initial backfill and acceptable at
  current scale (upsert is idempotent). A date-windowed incremental fetch (e.g. last 30 days)
  is a future optimization; the R1 percent-encode fix (deferred) is a prerequisite.
- **Nullable charge enrichment on pre-existing snapshots.** NULL in the new columns is expected
  and correct for rows before this migration. Dashboard code must handle NULL for these six fields.
- **`tesla_id` NULL for sessions with unrecognized VINs.** A session for a VIN that is no longer
  a registered vehicle gets stored with `tesla_id = NULL`. Dashboards must handle NULL and fall
  back to VIN-based lookup or orphan display. The account-wide index covers `(account_id,
  charge_start_date_time DESC)` which works without `tesla_id`.
- **No alert on `ChargingFetchFailures`.** A charging-history fetch failure is counted but not
  surfaced to the user. Future work: expose in the ops dashboard.
- **Source A stores actual values, not a zero-is-absent heuristic (D12).** New rows always carry
  the concrete DTO value (including a truthful `0`/`""`); the nullable columns exist only so
  pre-migration rows stay NULL. On read, a `nil` domain field therefore means "row predates
  enrichment", never "reported zero" — no real measurement is lost. Interpreting a 0 (e.g. against
  `charging_state`) is the dashboard's responsibility.

## Migration Plan

1. Add migration `<ts>_add_supercharger_sessions.sql` — CREATE TABLE + UNIQUE + 2 indexes (Source B).
2. Add migration `<ts>_enrich_vehicle_snapshots_charge.sql` — ALTER TABLE ADD COLUMN x6 (Source A).
3. Add sqlc queries: `UpsertSuperchargerSession`, `SuperchargerSessionsByAccount`,
   `SuperchargerSessionsByVehicle` to `internal/telemetry/db/query.sql`.
4. Run `make sqlc` to regenerate `telemetrydb`. (Leader-integrated — runs after step 3.)
5. Add `SuperchargerSession` domain type + derivation helpers + `SuperchargerReader` interface to
   `internal/telemetry/telemetry.go`.
6. Extend `CycleReport` (two new fields) in `internal/telemetry/telemetry.go`.
7. Extend `store` interface with `upsertSuperchargerSession`; implement `dbStore.upsertSuperchargerSession` in `service.go`.
8. Fold `ChargingHistory` into `collectAccount` in `service.go`.
9. **BLOCKED on leader tesla edit:** extend `Snapshot` domain type with 6 nullable fields;
   extend `snapshotFrom` in `service.go`; extend `InsertVehicleSnapshot` sqlc params; extend
   `rowToSnapshot` in `mapping.go`; add `pgNullable*` helpers.
10. Implement `SuperchargerReader` and `NewSuperchargerReader` in `reader.go`; implement
    `rowToSuperchargerSession` in `mapping.go`.
11. Add offline collector tests covering ChargingHistory fold-in + isolation.
12. Add `DATABASE_URL`-gated store/reader tests for Source B upsert + both read queries.
13. After Source A unblock: add store tests for the 6 new nullable snapshot columns.
