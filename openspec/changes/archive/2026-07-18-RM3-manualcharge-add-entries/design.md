## Context

Tier 1 (backend) of the RM3-manual-charge-log roadmap. This change introduces a new isolated
domain module `internal/manualcharge` that allows users to record charge sessions that Tesla's
Fleet API cannot capture (home / work / third-party AC or DC charging). The design session
(grill-me, 2026-07-18) resolved all decisions below. This section is final — do not re-open it.

Ports consumed: **none from other modules at this tier.** The `manualcharge` module owns its own
data entirely; it does not call `internal/tesla`, `internal/account`, or `internal/telemetry`
internals. The gateway (Tier 2) will call `manualcharge.Writer` and `manualcharge.Reader` via
their public Go interfaces — it will also call `account.Service` (to resolve the user's vehicle
list before writing) — but that wiring is out of scope for this module.

## Goals / Non-Goals

**Goals:**

- Persist user-asserted charge entries with required and optional fields, in a mutable table
  (correctable hand-typed data).
- Expose a `Writer` port (Create / Update / Delete) and a `Reader` port (list by vehicle / by
  account, newest-first) shaped for dashboard access patterns.
- Multi-tenant correctness: every row is scoped to `(account_id, tesla_id)` and every read
  path filters by `account_id` first.
- No cross-module DB access, no HTML, no Tesla API call.

**Non-Goals:**

- Frontend (deferred to Tier 2 gateway change).
- Currency conversion, display formatting, or FX rates.
- Aggregation / summary tables (deferred).
- Semi-automatic home-charge inference from telemetry deltas (future work).

---

## D1 — Full Schema

```sql
-- manual_charge_entries: user-asserted home/work/third-party charge sessions.
-- Owned by internal/manualcharge; no other module reads this table directly.
--
-- This table IS mutable: users correct hand-typed entries. UPDATE and DELETE are
-- supported via the Writer port. Unlike the append-only vehicle_snapshots /
-- poll_attempts, this table carries updated_at and the Writer port exposes full CRUD
-- (design D4).
--
-- No FK on account_id or tesla_id: a cross-module FK into the account module's tables
-- would couple manualcharge migrations to the account schema — exactly the coupling
-- ai/architecture.md §2 forbids. Referential integrity is upheld by flow: the gateway
-- (Tier 2) resolves the vehicle from the user's own registered vehicles (via the account
-- port) before calling Writer.
--
-- No raw_data JSONB: this table holds user-typed data, not an external API response.
-- The raw_data JSONB rule applies only to external API ingestion tables
-- (ai/go-conventions.md §persistence). User-typed data has no vendor payload to preserve
-- (design D5).
CREATE TABLE manual_charge_entries (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id         UUID   NOT NULL,         -- multi-tenant scope
    tesla_id           BIGINT NOT NULL,         -- which of the user's vehicles
    vin                TEXT   NOT NULL,         -- durable vehicle key (survives re-registration)

    -- REQUIRED (user must supply)
    charged_on         DATE          NOT NULL,                         -- the day of the charge
    energy_added_kwh   NUMERIC(6,2)  NOT NULL CHECK (energy_added_kwh > 0),
    price              NUMERIC(14,2) NOT NULL CHECK (price >= 0),
    currency           TEXT          NOT NULL DEFAULT 'COP',

    -- OPTIONAL (user may omit)
    started_at         TIMESTAMPTZ,
    ended_at           TIMESTAMPTZ,
    start_battery_pct  SMALLINT CHECK (start_battery_pct BETWEEN 0 AND 100),
    end_battery_pct    SMALLINT CHECK (end_battery_pct   BETWEEN 0 AND 100),
    charging_type      TEXT CHECK (charging_type IN ('AC','DC')),
    location_kind      TEXT CHECK (location_kind IN ('HOME','WORK','OTHER')),
    location_label     TEXT,                                           -- free text, esp. for OTHER
    notes              TEXT,

    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Session time sanity: end must not precede start when both are given.
    CHECK (ended_at IS NULL OR started_at IS NULL OR ended_at >= started_at)
);
```

---

## D2 — Rationale: why this schema design is right

### D2a — MUTABLE table (has `updated_at`, supports UPDATE + DELETE)

Manual entries are hand-typed and therefore correctable. The user may mistype the kWh,
discover the price later, or need to delete a duplicate. An append-only design like
`vehicle_snapshots` / `poll_attempts` (which record immutable historical facts from the
Tesla API) would be wrong here. The design adds `updated_at` and the `Writer` port is full
CRUD — aligning with the `supercharger_sessions` precedent (another mutable, non-append-only
table in the codebase).

**Rejected alternative — append-only with a "corrections" log:** adds complexity without benefit
when the source of truth is the user themselves. Corrections-as-events are appropriate when an
external system is the authority; here the user IS the authority.

### D2b — No `raw_data JSONB` column

The `raw_data JSONB NOT NULL` rule (ai/go-conventions.md §persistence) exists as a schema-drift
hedge for **external API responses**: if the API renames a field, the raw payload allows
backfilling without re-calling the API. User-typed data has no external vendor payload to
preserve — every field is what the user typed, already decomposed into columns. Adding JSONB
would be dead storage that never provides value. Explicitly omitted (not forgotten).

### D2c — Money as `NUMERIC`, energy as `NUMERIC`

`NUMERIC(14,2)` for `price` and `NUMERIC(6,2)` for `energy_added_kwh` store exact decimal
values. IEEE 754 floating point (`DOUBLE PRECISION` / `REAL`) would introduce sub-cent rounding
errors when summing across entries (e.g., $0.1 + $0.2 ≠ $0.3 in float). `NUMERIC` arithmetic
is exact, which is required for any currency column. Energy is user-entered to 2 decimal places
and the same precision argument applies.

**Rejected alternative — integer cents:** avoids the float issue but requires the application to
remember the scale, complicates display, and differs from the user's mental model (they think in
COP, not centavos). `NUMERIC` is the idiomatic Postgres answer for money.

### D2d — No `Km()`/`Kmh()` companion methods

The miles→km companion-method rule (ai/go-conventions.md non-negotiables) applies to
**distance and speed** fields sourced from Tesla's Fleet API (which reports in miles). This table
has no distance or speed fields: `energy_added_kwh` is kWh (already SI), `price` is currency,
battery percentages are dimensionless, and timing fields are timestamps. No companion methods are
required or appropriate here.

### D2e — Enums as `TEXT` + `CHECK` (not Postgres `ENUM` types)

`charging_type TEXT CHECK (charging_type IN ('AC','DC'))` and
`location_kind TEXT CHECK (location_kind IN ('HOME','WORK','OTHER'))` follow the established
codebase pattern (`billing_type TEXT`, `outcome TEXT`, `reason TEXT` in existing tables). Postgres
`ENUM` types require `ALTER TYPE ... ADD VALUE` to extend (which cannot run inside a transaction
in older Postgres versions) and make schema evolution painful. `TEXT` + `CHECK` can be loosened
in a migration with a simple `ALTER TABLE ... DROP CONSTRAINT ...` and re-add. Zero behavior
difference for a small, stable set of values; significant operational advantage for evolution.

### D2f — No cross-module FK on `account_id` or `tesla_id`

A `REFERENCES accounts(id)` or `REFERENCES vehicles(tesla_id)` foreign key from
`manual_charge_entries` into the account module's tables would couple the `manualcharge` migration
to the account schema — exactly what `ai/architecture.md §2` ("No cross-module database leaks")
forbids. Referential integrity is upheld by flow: the gateway (Tier 2) calls `account.Service`
to resolve the user's vehicle list before calling `manualcharge.Writer`, ensuring `account_id`
and `tesla_id` are always valid for the calling user's registered vehicles.

### D2g — `vin` stored alongside `tesla_id`

`tesla_id` is Tesla's mutable integer vehicle ID (changes if the vehicle is re-registered after
a sale). `vin` is the durable chassis identifier that survives re-registration. Storing both
mirrors the `supercharger_sessions` precedent and allows lookups by VIN in future without a
migration — e.g. if a user sells their vehicle and re-registers it under a new Tesla account,
their historical manual entries can still be matched to the same physical car.

### D2h — `charged_on DATE` (not `TIMESTAMPTZ`) as the required temporal field

The user records "which day" they charged, not an exact time (timing fields `started_at` /
`ended_at` are optional). Storing the day as `DATE` avoids timezone confusion (a charge at
23:45 local time is unambiguous as a date; as a UTC timestamp it might fall on the next calendar
day). The dashboard orders by `charged_on DESC` to show the most recent entries first — `DATE`
is naturally sortable and comparable for this purpose.

### D2i — Optional `started_at` / `ended_at` as `TIMESTAMPTZ`

When the user supplies timing (from the charger display or an app), the exact timestamps are
stored with timezone. The table CHECK constraint `(ended_at IS NULL OR started_at IS NULL OR
ended_at >= started_at)` prevents accidentally reversed sessions (e.g. copy-paste error). Both
being nullable preserves backwards compatibility: existing entries without timing data remain
valid.

### D2j — Derived values are NOT stored columns

`cost_per_kwh` (`price / energy_added_kwh`), `battery_delta` (`end_battery_pct - start_battery_pct`),
and `session_duration` (`ended_at - started_at`) are deterministically computable from stored
columns. Storing them would introduce update anomalies (the user changes `price` but forgets to
update `cost_per_kwh`). They are exposed as value-receiver methods on `manualcharge.Entry`,
nil-safe for the pointer/optional fields, following the companion-method style of the codebase.

---

## D3 — Index Plan

### Read path 1: Per-vehicle charge history (the hot dashboard path)

**Query:** list entries for a specific vehicle belonging to a specific account, ordered newest
charged day first, with a `LIMIT` for pagination.

```sql
SELECT * FROM manual_charge_entries
WHERE account_id = $1 AND tesla_id = $2
ORDER BY charged_on DESC
LIMIT $3;
```

**Index:**

```sql
CREATE INDEX idx_manual_charge_entries_vehicle_time
    ON manual_charge_entries (account_id, tesla_id, charged_on DESC);
```

**Why this covers it:** `account_id` leads the index (multi-tenant convention: every dashboard
read scopes by account — ai/go-conventions.md §persistence, ai/architecture.md §7.3). `tesla_id`
second lets the planner satisfy both the `WHERE account_id = $1 AND tesla_id = $2` filter in a
range scan without a separate filter step. `charged_on DESC` as the third column means the range
scan already arrives in the right order — the planner can satisfy the `ORDER BY charged_on DESC`
from the index, eliminating a sort step. `LIMIT` is then cheap (stop reading after N rows from
the already-ordered stream).

**Rejected alternative — index on `(tesla_id, charged_on DESC)` without `account_id`:** the
planner would have to scan across all tenants' entries for the given `tesla_id` and then filter
by `account_id`. With many tenants this is a wide scan. Leading with `account_id` prunes to the
tenant first — a narrow, efficient range.

### Read path 2: Account-wide charge history

**Query:** list all entries for a given account (across all vehicles), ordered newest first, with
a `LIMIT`.

```sql
SELECT * FROM manual_charge_entries
WHERE account_id = $1
ORDER BY charged_on DESC
LIMIT $2;
```

**Index:**

```sql
CREATE INDEX idx_manual_charge_entries_account_time
    ON manual_charge_entries (account_id, charged_on DESC);
```

**Why this covers it:** `account_id` is the single WHERE predicate; `charged_on DESC` eliminates
the sort step. The per-vehicle index above (`account_id, tesla_id, charged_on DESC`) would also
satisfy this query if the planner chose to scan the prefix, but having a dedicated two-column
index makes the planner's choice unambiguous and cheaper for account-wide reads.

### No additional indexes

Lookups by `id` (UUID primary key) use the built-in primary key index — point lookups for
Update and Delete do not need a separate index. No other filter or sort patterns are expected in
Tier 1.

---

## D4 — Writer Port Design

```go
type Writer interface {
    Create(ctx context.Context, e Entry) (Entry, error)
    Update(ctx context.Context, e Entry) (Entry, error)
    Delete(ctx context.Context, accountID uuid.UUID, id uuid.UUID) error
}
```

`Create` and `Update` return the stored `Entry` (with server-assigned `id`, `created_at`,
`updated_at`) so the gateway can display the result without a second round-trip.

`Delete` takes `accountID` as a required argument so the SQL WHERE clause always scopes to the
caller's own account (`WHERE id = $1 AND account_id = $2`) — a user cannot delete another
tenant's entry even with a valid UUID.

---

## D5 — Reader Port Design

```go
type Reader interface {
    ListEntriesByVehicle(ctx context.Context, accountID uuid.UUID, teslaID int64, limit int) ([]Entry, error)
    ListEntriesByAccount(ctx context.Context, accountID uuid.UUID, limit int) ([]Entry, error)
}
```

Both methods return a non-nil empty slice when no entries exist. `limit = 0` is treated as a
server default (e.g., 100). Both are shaped for the dashboard's access pattern: the gateway
calls `ListEntriesByVehicle` for the per-vehicle tab and `ListEntriesByAccount` for the
account-wide summary — the same two shapes the indexes serve (D3).

---

## D6 — `manualcharge.Entry` Domain Type

The domain type mirrors the table columns, with Go-idiomatic optionals (`*T` for nullable
columns) and `time.Time` for timestamps (pgtype confined to the DB boundary — ai/go-conventions.md
§persistence). `pgtype` never appears in public types or method signatures.

Derived value-receiver methods (all nil-safe for pointer fields):

- `CostPerKWh() *float64` — `price / energy_added_kwh`; nil if `energy_added_kwh` is zero
  (defensive; CHECK constraint prevents zero, but the method is nil-safe by convention).
- `BatteryDelta() *int` — `end_battery_pct - start_battery_pct`; nil if either field is nil.
- `SessionDuration() *time.Duration` — `ended_at - started_at`; nil if either field is nil.

No `Km()` / `Kmh()` companions: no distance or speed fields exist in this type (D2d).

---

## D7 — `sqlc.yaml` Entry (leader-integrated)

The leader must add a new `sql:` entry to `sqlc.yaml` after this change's migration and
`query.sql` are authored:

```yaml
- schema: "internal/manualcharge/db/migrations"
  queries: "internal/manualcharge/db/query.sql"
  engine: "postgresql"
  gen:
    go:
      package: "manualchargedb"
      out: "internal/manualcharge/db"
      sql_package: "pgx/v5"
      overrides:
        - db_type: "uuid"
          go_type: "github.com/google/uuid.UUID"
```

After `make sqlc` the generated files are `internal/manualcharge/db/models.go`,
`internal/manualcharge/db/query.sql.go`, and `internal/manualcharge/db/db.go`.

---

## D8 — Module Boundary Enforcement

- `internal/manualcharge` imports: `pgx/v5`, `pgxpool`, `uuid`, `context`, `time`, `math/big`
  (if needed for NUMERIC). It does NOT import `internal/tesla`, `internal/account`,
  `internal/telemetry`, or any other module's internal packages.
- `internal/manualcharge/db` (package `manualchargedb`) is module-scoped: no other module may
  import it. The public `Writer` and `Reader` interfaces are the only cross-module API surface.
- `pgtype` is confined to the service/mapping layer inside `internal/manualcharge`. It never
  appears in `manualcharge.Entry`, `Writer`, or `Reader`.

---

## D9 — NUMERIC Handling in Go

Postgres `NUMERIC` columns map to `pgtype.Numeric` in pgx/v5. The service layer maps them to
`float64` at the DB boundary for `energy_added_kwh` and `price` (two-decimal precision is
preserved with `float64` for typical charge values — no fractional cent / fractional Wh is lost
at 64-bit precision for the ranges `NUMERIC(6,2)` and `NUMERIC(14,2)` represent). The domain
type stores `float64` for both. The sqlc override maps `NUMERIC` to `pgtype.Numeric`; the
service converts via `pgtype.Numeric.Float64()` and stores the result in the domain struct.

If exact decimal arithmetic is required in a future aggregation (e.g., sum of costs), the
service may use `math/big.Float` or pass the raw `pgtype.Numeric` value. For read display
purposes, `float64` is sufficient.
