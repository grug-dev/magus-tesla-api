## Context

The `account` module persists a per-account registry of Tesla vehicles in the `vehicles` table
(`internal/account/db/migrations/20260710000001_vehicles.sql`). Current columns: `id`, `account_id`,
`tesla_id`, `vin`, `display_name`, `created_at`, `updated_at`, plus a `UNIQUE (account_id, tesla_id)`
constraint. The seed path (`InsertVehicleIfMissing`) and the read paths (`ListVehiclesByAccount`,
`ListAllVehicles`) do not yet carry Tesla's per-vehicle `access_type` field. The charge-form UI (tier
4) must distinguish OWNER from DRIVER to decide which features to show — this change adds that
capability to the registry.

Performance profile: **read-heavy**. Writes happen at most once per vehicle per account (the seed)
and via potential re-seeds. Dashboard reads fetch the registered vehicle list on every page load.
Every design decision below optimizes for read performance while respecting module boundaries.

## Goals / Non-Goals

**Goals:**
- Persist `access_type` in the `vehicles` table, surfaced on all three public domain types.
- Keep the `SeedVehicles` ON CONFLICT semantics unchanged (insert new, leave existing untouched).
- Keep `pgtype` confined to `service.go` (it never leaks into the public domain types).
- Enable the account module to be built and integration-tested independently of the tesla adapter.

**Non-Goals:**
- Backfilling existing rows — the user is wiping DB data before deploying this change; all fresh
  seeds will carry `access_type`. See D3.
- Re-seeding / refresh path — no new path that overwrites an existing vehicle's stored attributes.
- Any Tesla API call, token logic, or inter-module wiring — those are tier 4's concern.
- Indexing on `access_type` — see D2 for explicit justification.

## Decisions

### D1 — Schema: `access_type TEXT` nullable, CHECK constraint, no DEFAULT

Exact DDL added via a new goose migration:

```sql
ALTER TABLE vehicles
    ADD COLUMN access_type TEXT
    CHECK (access_type IS NULL OR access_type IN ('OWNER','DRIVER'));
```

**Why nullable, no DEFAULT.** A migration cannot assign a truthful value to pre-existing rows —
`OWNER` and `DRIVER` are semantically distinct and the DB has no way to know which is correct
for a row that was seeded before `access_type` existed. Using `NULL` encodes "not yet known /
predates this change" honestly. A `DEFAULT 'OWNER'` or any other fabricated value would be
factually wrong and mislead any feature that uses the field.

**Why a CHECK constraint, not a Postgres enum or a separate lookup table.** The value set is
small (two entries: `OWNER`, `DRIVER`) and comes directly from Tesla's Fleet API vocabulary —
there is no domain logic built on it beyond display/routing. A CHECK on a TEXT column is:
- Lighter than an enum (`ALTER TYPE` is DDL that requires a table rewrite to remove values;
  CHECK constraints are additive/removable without rewriting).
- Correct enough: we need reject-on-insert, not a join-able FK.
- Simpler to query (`WHERE access_type = 'OWNER'`) with no extra table.

**Why no DEFAULT.** `NOT NULL DEFAULT 'OWNER'` would require either a truthful value (unavailable
for pre-existing rows) or a lie. NULL is the honest default for "value not yet captured."

**Rejected alternative:** `NOT NULL` with a backfill migration. Rejected because the table has
pre-existing rows from earlier seeds and we cannot know their correct `access_type` without
re-calling Tesla — which is paid, rate-limited, and wakes the car. The user has confirmed that
existing DB data will be wiped; this decision is valid for a clean-slate deploy.

### D2 — No index on `access_type`

`access_type` is NEVER used as a filter predicate (`WHERE access_type = ...`). It is read ONLY
as a field of the per-account vehicle list already fetched by `RegisteredVehicles` /
`AllRegisteredVehicles`:

- `RegisteredVehicles` uses `WHERE account_id = @account_id ORDER BY tesla_id` — the index
  `idx_vehicles_account_id` (implicitly the `UNIQUE (account_id, tesla_id)` constraint index)
  already covers this scan. Adding `access_type` to the SELECT adds zero index cost; the column
  is fetched as part of the heap row already located.
- `AllRegisteredVehicles` uses `ORDER BY account_id, tesla_id` — also covered by the existing
  unique constraint index.

Under the project's **read-heavy performance profile**, indexing aggressively is encouraged — but
only where indexes benefit a read. An index on `access_type` would:
- Add write cost on every INSERT/UPDATE to `vehicles` (seed path).
- Provide zero read benefit, since `access_type` is never a `WHERE`/`JOIN`/`ORDER BY` predicate
  in any existing or planned query.

**Verdict: no index.** If a future query filters by `access_type` (e.g. "all DRIVER vehicles
across all accounts"), add the index then, with the specific query to justify it.

**Rejected alternative:** Add a partial index or composite index including `access_type`. Rejected
— no predicate use today; premature and write-cost-bearing with no read payoff.

### D3 — No backfill; ON CONFLICT semantics unchanged

The `InsertVehicleIfMissing` query uses `ON CONFLICT (account_id, tesla_id) DO NOTHING`. This
semantics is **intentionally preserved**: existing vehicles retain whatever they had (including
`NULL` for `access_type` if they predate this migration). No new "re-seed" or "refresh" path is
added that would overwrite the stored `access_type` of an existing row.

**Why.** The user confirmed that all existing vehicle rows will be wiped (a clean deploy precedes
going live with this feature). Fresh seeds via `SeedVehicles` will carry the `access_type` from
Tesla. Adding an ON CONFLICT UPDATE path is out of scope and would complicate the idempotency
semantics unnecessarily.

**Rejected alternative:** Change `InsertVehicleIfMissing` to `ON CONFLICT ... DO UPDATE SET
access_type = EXCLUDED.access_type`. Rejected — explicitly out of scope per user decision (RD3
in the dispatch prompt). The idempotency invariant ("insert new, leave existing untouched")
must not be weakened.

### D4 — `*string` for the domain field; `pgtype.Text` at the DB boundary

The public domain field is `AccessType *string`:
- `nil` maps to `NULL` in Postgres (unknown/not-yet-seeded).
- `"OWNER"` or `"DRIVER"` map to the corresponding TEXT value.
- `pgtype.Text` (from pgx/v5) is used only inside `service.go` for the DB→domain conversion;
  it never appears in `account.Vehicle`, `account.OwnedVehicle`, or `account.SeedVehicle`.

Mapping convention (matches the existing `display_name` pattern, but for a pointer):
```go
// nullable TEXT → *string at the DB→domain boundary
func nullableText(t pgtype.Text) *string {
    if !t.Valid {
        return nil
    }
    s := t.String
    return &s
}
// *string → pgtype.Text for DB writes
func textFromStringPtr(s *string) pgtype.Text {
    if s == nil {
        return pgtype.Text{}
    }
    return pgtype.Text{String: *s, Valid: true}
}
```

**Why `*string` and not a typed constant/enum in Go.** The two values (`OWNER`, `DRIVER`) come
from Tesla's API vocabulary. Using `*string` avoids a hard dependency on a Tesla-specific type
inside the account module's public interface (the account module does not import `internal/tesla`).
A typed Go enum in the account package would duplicate Tesla's vocabulary without adding safety.
If a future use case needs richer typing, it can be introduced as a named type over string without
breaking callers.

## Migration Plan

1. Add goose migration: `ALTER TABLE vehicles ADD COLUMN access_type TEXT CHECK (...)`.
2. Add `AccessType *string` to `Vehicle`, `OwnedVehicle`, `SeedVehicle` in `account.go`.
3. Update `InsertVehicleIfMissing` in `query.sql` to include `access_type`; update
   `ListVehiclesByAccount` and `ListAllVehicles` to select it. Run `make sqlc`.
4. Implement DB→domain mapping in `service.go` (new `nullableText`/`textFromStringPtr` helpers;
   update `vehicleFromRow`, `ownedVehicleFromRow`, `InsertVehicleIfMissingParams`).
5. Add `DATABASE_URL`-gated integration tests.
6. `go build ./...`, `go vet ./...`, `go test ./...` pass (integration tests self-skip without
   `DATABASE_URL`).
