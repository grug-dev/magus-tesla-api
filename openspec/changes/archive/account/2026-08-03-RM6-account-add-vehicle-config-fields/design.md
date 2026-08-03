## Context

The `account` module persists a per-account registry of Tesla vehicles in the `vehicles` table
(`internal/account/db/migrations/20260710000001_vehicles.sql`, extended by
`20260720000001_vehicles_add_access_type.sql`). Current columns: `id`, `account_id`, `tesla_id`,
`vin`, `display_name`, `created_at`, `updated_at`, `access_type`, plus a
`UNIQUE (account_id, tesla_id)` constraint. This is the direct precedent for adding a nullable
column over the same port — read it before this design (`access_type`'s own
`design.md` at `openspec/changes/archive/account/2026-07-20-RM4-account-persist-vehicle-access-type/design.md`).

Tesla's Fleet API `VehicleData` response carries a `vehicle_config` sub-object with ~47 keys.
Two of them — `exterior_color` (observed live value: `"PearlWhite"`) and `car_type` (observed:
`"modely"`) — are **static**: they never change for a given vehicle after manufacture. Today these
values exist only inside `internal/telemetry`'s `vehicle_snapshots.raw_data` JSONB column, a table
the `account` module does not own and must not read (`ai/architecture.md` §2: "no cross-module
database leaks" — a module may not read another module's tables, repositories, or private
structs). This roadmap (`RM6-static-vehicle-config-fields`) closes that gap by having `account` own
its own copy of the two values, written back once by the tier-2 (`telemetry`) nightly collector
through a port method this tier adds.

Performance profile: **read-heavy** (`ai/architecture.md` §7). Every dashboard page load / htmx
fragment refresh that reads a vehicle's registry row pays for these two columns as part of a row
already fetched by `account_id`/`(account_id, tesla_id)`. Writes happen **at most once per vehicle,
ever** — the nightly collector stops calling the write-back port for a vehicle the instant both
columns are non-NULL (RD2, enforced defense-in-depth at both the Go caller and the SQL layer).

## Goals / Non-Goals

**Goals:**
- Persist `exterior_color` and `car_type` on the `vehicles` table so the registry read port serves
  them with zero Tesla calls and zero cross-module JSONB reads.
- Add a conditional-update port method (`SetVehicleConfigIfEmpty`) whose SQL write condition is
  correct **on its own** — self-healing a partially-written row and refusing to clobber an
  already-captured value — independent of whatever Go-side guard a caller adds (RD2).
- Keep `pgtype` confined to `service.go` (never leaks into the public domain types), exactly as
  the `access_type` precedent does.
- Denormalize for reads: this is precisely the project's read-heavy performance profile in action
  — copy two immutable values onto the already-indexed registry row so reads never touch
  `vehicle_snapshots.raw_data` JSONB or call Tesla.

**Non-Goals:**
- `SeedVehicles` / the registration path — Tesla's `ListVehicles` (what seeds a vehicle) does not
  return `vehicle_config`; only a per-vehicle `VehicleData` call does. These columns start `NULL`
  at seed time and are back-filled later, entirely out of this tier's scope.
- The nightly collector's call site, the Go-side nil/empty guard, the `ConfigCaptureFailures`
  counter, and the `internal/tesla` DTO addition — all tier 2 (`RM6-telemetry-capture-vehicle-config`).
- Any additional `vehicle_config` field beyond these two, or a curated display set, or the full
  JSONB blob — explicitly rejected by RD3 (see D3 below). Strict YAGNI.
- Any gateway display of colour/model — not proposed by this roadmap at all.

## Decisions

### D1 — Schema: two nullable `TEXT` columns, no `CHECK`, no `DEFAULT`

Exact DDL (goose migration, both directions):

```sql
-- +goose Up
ALTER TABLE vehicles
    ADD COLUMN exterior_color TEXT,
    ADD COLUMN car_type TEXT;

-- +goose Down
ALTER TABLE vehicles
    DROP COLUMN IF EXISTS exterior_color,
    DROP COLUMN IF EXISTS car_type;
```

**Why nullable, no `DEFAULT`.** Exactly the `access_type` rationale: a migration cannot assign a
truthful value to pre-existing rows. `NULL` means "not yet captured" — the honest state for every
row until the nightly collector's write-back lands. A fabricated default (e.g. `DEFAULT ''` or any
placeholder colour/model string) would be factually wrong for every existing vehicle and would
permanently defeat the `IS NULL` capture-guard (see D4/RD5) since it is indistinguishable from a
genuinely-observed value.

**Why no `CHECK` constraint — the deliberate divergence from `access_type`.** `access_type` has
`CHECK (access_type IS NULL OR access_type IN ('OWNER','DRIVER'))` because its value set is a
closed, two-entry vocabulary owned by Tesla's authorization model. `exterior_color` and `car_type`
are **open-ended Tesla enums**: observed live values are `PearlWhite` and `modely`, but Tesla adds
new paint names and model codes with every trim/paint release and every new vehicle line (e.g. a
future `cybertruck`, a new "Ultra Red" paint). A `CHECK ... IN (...)` here would need updating
every time Tesla ships a new SKU — a maintenance burden with no domain logic built on the
constraint (nothing branches on "is this a valid color/model" the way authorization branches on
OWNER vs DRIVER). Rejected: hardcoding today's known values into a `CHECK` list, migrating monthly
as trims roll out.

**Why no whole-`vehicle_config` JSONB column (RD3).** The live payload carries 47 keys. Storing the
full blob was explicitly considered and rejected by the user: it would duplicate insurance already
provided by `vehicle_snapshots.raw_data` (owned by `telemetry`), invite exactly the kind of
JSONB-on-a-hot-read-path anti-pattern `ai/go-conventions.md` warns against ("never force a
dashboard to extract from JSONB on the hot path"), and re-introduce the cross-module temptation
this whole roadmap exists to remove. A curated 7-field "display set" was also considered and
rejected in favor of strict YAGNI — exactly two fields have a concrete, decided consumer (tier 2);
anything else is speculative and can be added as its own future change when a real read need
exists.

**Rejected alternative: parse the values out of `vehicle_snapshots.raw_data` JSONB at query time.**
This was the leading alternative to persisting a dedicated column and is rejected outright: the
`account` module does not own the `vehicle_snapshots` table — it belongs to `internal/telemetry`.
Reading it (JSONB-extract or otherwise) from `account` would be a direct violation of
`ai/architecture.md` §2 ("no cross-module database leaks... cross-module data flows only through
public interfaces"). Even a `telemetry`-side read port for this would still require an extra
in-process call and a JOIN-shaped read on every registry fetch, instead of a value already sitting
on the row the registry query fetches today.

### D2 — No index on `exterior_color` or `car_type`

Justified against the actual queries that touch `vehicles`, not asserted:

- **`ListVehiclesByAccount`** — `SELECT ... FROM vehicles WHERE account_id = @account_id ORDER BY
  tesla_id`. The row set is located by the existing `UNIQUE (account_id, tesla_id)` constraint's
  backing index scoped on `account_id`; adding two more columns to the `SELECT` list costs nothing
  beyond fetching two more bytes-on-disk per already-located heap row. Neither column is a
  predicate in this query.
- **`ListAllVehicles`** — `SELECT account_id, tesla_id, vin, display_name, access_type FROM
  vehicles ORDER BY account_id, tesla_id` (no `WHERE` at all — a full-table scan by design, since
  it enumerates every vehicle across every account for the nightly collector). An index cannot
  speed up a query with no filter predicate; the `ORDER BY` is already served by the unique
  constraint's index. Adding `exterior_color`/`car_type` to the `SELECT` list is free at read time.
- **`UpdateVehicleConfigIfEmpty`** (new, D4) — `UPDATE vehicles SET ... WHERE account_id = @account_id
  AND tesla_id = @tesla_id AND (exterior_color IS NULL OR car_type IS NULL)`. The row is located by
  `(account_id, tesla_id)` — already the `UNIQUE` constraint's index. The `IS NULL OR IS NULL`
  clause is evaluated only after that row is found (it is a post-lookup filter, not a search
  predicate that would benefit from an index of its own — there is no query in this system that
  scans `vehicles` filtering by "which rows have `exterior_color IS NULL`", so no partial index is
  justified either).

**Verdict: no new index.** Every read and write that touches these two columns locates its row(s)
via `(account_id, tesla_id)` or `account_id` alone, both already covered by the existing
`UNIQUE (account_id, tesla_id)` constraint index. An index on either new column would add write
cost to every `UPDATE`/`INSERT` on `vehicles` for zero read benefit, since neither column is ever a
`WHERE`, `JOIN`, or `ORDER BY` predicate in any existing or planned query. If a future feature needs
"find all vehicles with `car_type = 'modely'`" as a genuine filter, add a targeted index then, with
that query as the justification — not preemptively here.

### D3 — Field scope: exactly `exterior_color` + `car_type` (RD3)

No other `vehicle_config` key is added by this tier. See D1's JSONB-blob rejection above for the
full rationale; the short version is strict YAGNI — these two fields have a concrete consumer
(tier 2's nightly write-back) and a concrete future reader (a not-yet-proposed dashboard "vehicle
model/colour" display); every other key does not.

### D4 — `SetVehicleConfigIfEmpty`: conditional-update port method, `*string` domain fields

New port method on `account.Service`:

```go
// SetVehicleConfigIfEmpty persists exteriorColor and carType for the vehicle identified by
// (accountID, teslaID), but ONLY while at least one of the two is still uncaptured. Once a
// vehicle has both exterior_color and car_type non-NULL, subsequent calls are no-ops (the SQL
// WHERE clause matches zero rows). On a partially-captured row the write DOES rewrite both
// columns, including the one already set — harmless, because both values are immutable and come
// from the same vehicle_config payload, and it is what lets a partial row self-heal (D4/RD2).
// Callers pass non-empty strings; empty-string handling (RD5) is the caller's job, not this
// method's — see the account-vehicle-registry spec delta.
SetVehicleConfigIfEmpty(ctx context.Context, accountID uuid.UUID, teslaID int64, exteriorColor, carType string) error
```

Backing sqlc query (`UpdateVehicleConfigIfEmpty`):

```sql
-- name: UpdateVehicleConfigIfEmpty :exec
UPDATE vehicles
SET exterior_color = @exterior_color,
    car_type        = @car_type,
    updated_at      = now()
WHERE account_id = @account_id
  AND tesla_id    = @tesla_id
  AND (exterior_color IS NULL OR car_type IS NULL);
```

**Why `AND (exterior_color IS NULL OR car_type IS NULL)` — OR-semantics, not AND (RD2).** This is
the defense-in-depth condition: it must be correct standing alone, independent of whatever guard
the tier-2 caller adds in Go. Using `OR` (fire the UPDATE if *either* column is still unset) makes
a partially-written row (e.g. a hypothetical future bug that captured only one of the two values)
self-heal on the very next call — the row does not need both columns cleared to be eligible for a
repair write. Using `AND` instead (fire only if *both* are unset) would freeze a partial row
forever: the moment either column gets a value, `AND` would block ever filling in the other. `OR`
is therefore strictly the correct condition for "this row still needs work on at least one field."
A concurrent writer racing to fill in the same row cannot clobber an already-captured value either
way — the first writer to land flips at least one column non-NULL, and every subsequent writer's
`WHERE` only matches if something is still missing.

`updated_at = now()` is set inside the same `SET` clause, so it moves **only** when a write
actually lands (the `WHERE` matched and a row was updated) — not on every call attempt. A no-op
call (both columns already set) leaves `updated_at` untouched.

**Why `*string` for the domain fields, not a typed enum.** Exactly the `access_type` precedent's
D4 rationale: the values come from Tesla's API vocabulary, which is open-ended here (unlike
`access_type`'s closed two-value set) — `*string` avoids a hard dependency on Tesla-specific typing
inside the account module's public interface, and the account module does not import
`internal/tesla`. `nil` maps to `NULL` (not yet captured); a non-nil `*string` maps to the observed
Tesla value verbatim.

**Reuse, don't reinvent, the existing mapping helpers.** `nullableTextToPtr` and
`textPtrToNullable` (`internal/account/service.go` ~lines 169, 198, 212) already do exactly the
`pgtype.Text ↔ *string` conversion these two new fields need — they are reused unchanged, not
duplicated.

**Why the caller (not this query) is responsible for empty-string rejection (RD5).** The DTO
fields Tesla returns are plain `string`, and the tier-2 caller never passes an empty string to this
method — it skips the call entirely when either observed value is `""`. If `""` were written here,
it would satisfy the `IS NULL` guard's *negation* forever: `exterior_color = ''` is `NOT NULL`, so
the row would look "captured" to every future check, and the next cycle would never retry filling
in a real value. Keeping the emptiness check in the caller (not in this query or this method) keeps
"NULL = not yet captured" literally, permanently true, and this design explicitly calls out that
constraint so tier 2 does not regress it.

## Risks / Trade-offs

- **[Risk]** A future Tesla API change renames or removes `vehicle_config.exterior_color` /
  `car_type` → **Mitigation**: these two columns are independent, hand-extracted values (not a
  JSONB mirror), so a Tesla rename only affects the tier-2 extraction code (the `...Tesla` DTO
  JSON tags) — the `vehicles` table schema and all historical rows stay valid, exactly the
  `raw_data JSONB` insurance policy pattern documented in `ai/architecture.md` §7, applied here at
  the extraction-code layer instead of a table-schema layer since there is no `raw_data` column on
  `vehicles`.
- **[Risk]** A vehicle traded/sold and re-registered under a different account could in theory need
  its config re-captured → **Mitigation**: out of scope for this roadmap; `vehicles` rows are keyed
  by `(account_id, tesla_id)` and a genuinely new registration is a new row, which starts `NULL`
  and is captured fresh.
- **[Trade-off]** No `CHECK` means a malformed or unexpected string could be stored verbatim (e.g.
  if Tesla ever sends a genuinely garbage value) → accepted: these are pure display-style
  passthrough strings with no domain logic branching on their content in this tier; a `CHECK` would
  buy safety no consumer currently needs, at the cost of an ongoing enum-maintenance tax (see D1).

## Migration Plan

1. Add goose migration: `ALTER TABLE vehicles ADD COLUMN exterior_color TEXT, ADD COLUMN car_type
   TEXT` (D1) — no `DEFAULT`, no `CHECK`, no index (D2).
2. Add `ExteriorColor *string` and `CarType *string` to `Vehicle` and `OwnedVehicle` in
   `internal/account/account.go`. Add `SetVehicleConfigIfEmpty` to the `Service` interface.
3. Add `UpdateVehicleConfigIfEmpty` to `query.sql` (D4); extend `ListVehiclesByAccount` and
   `ListAllVehicles` to project the two new columns. Run `make sqlc`.
4. Implement `SetVehicleConfigIfEmpty` in `service.go`, reusing `textPtrToNullable` /
   `nullableTextToPtr`; update `vehicleFromRow` and `ownedVehicleFromRow` to map the two new
   columns.
5. Add `DATABASE_URL`-gated integration tests covering: a fresh capture (both NULL → both set), a
   no-op on an already-fully-captured row (values unchanged, `updated_at` untouched), a self-heal
   on a partially-captured row, and both read paths (`RegisteredVehicles` /
   `AllRegisteredVehicles`) surfacing the two columns correctly (including the `nil` case).
6. `go build ./...`, `go vet ./...`, `go test ./...` pass (integration tests self-skip without
   `DATABASE_URL`).

**Rollback:** the `-- +goose Down` drops both columns; any tier-2 code depending on them (a
separate, dependent change) would need to be rolled back first — this tier's Down migration is
self-contained and does not touch any other table.

## Open Questions

None — RD2, RD3, and RD5 (this tier's applicable binding decisions from the roadmap) are settled.
Tier 2's own open items (if any) belong to its own design.md, not this one.
