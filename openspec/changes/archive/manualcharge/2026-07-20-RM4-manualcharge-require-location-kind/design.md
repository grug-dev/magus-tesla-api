## Context

Tier 3 (backend required-field tightening) of the RM4-charge-form-required-location-and-vehicle-autoselect
roadmap. This change promotes `location_kind` from an optional column (`NULL` permitted) to a
required column (`NOT NULL`) in `manual_charge_entries`, and adds matching service-layer
validation in `manualcharge.Writer` (both `Create` and `Update`). The domain field
`LocationKind *string` stays a pointer type to avoid breaking the gateway build before Tier 4
lands. All decisions below are final — confirmed in the grill-me session for RM4 on 2026-07-19.

**Ports consumed:** none from other modules. This change is entirely within `internal/manualcharge`.

## Goals / Non-Goals

**Goals:**

- Promote `location_kind` to `NOT NULL` via a defensive migration (backfill then constrain).
- Reject nil or empty `LocationKind` in `Writer.Create` and `Writer.Update` at the service layer.
- Preserve the existing `CHECK (location_kind IN ('HOME','WORK','OTHER'))` constraint.
- Keep the domain field `LocationKind *string` (pointer stays) so the gateway build is not
  broken before Tier 4 updates its call sites.

**Non-Goals:**

- Gateway UI changes (asterisk, `required` attribute, client-side validation) — deferred to Tier 4.
- Changing `LocationKind *string` → `string` in the domain type — deferred to Tier 4.
- Any read-path change — `Reader` is untouched.

---

## D1 — Migration: Defensive Backfill Then NOT NULL Constraint

```sql
-- +goose Up

-- Step 1: Defensive backfill — set any NULL location_kind rows to 'OTHER' before
-- the NOT NULL constraint is applied. After the planned DB wipe this UPDATE matches
-- zero rows and is a no-op. It is included so the migration is safe to run against
-- any non-empty environment (a CI restore, a staging database, a developer's local
-- database that was never wiped) without failing the subsequent ALTER.
--
-- Why 'OTHER' as the backfill value: it is the most conservative choice for unknown
-- historical data. 'HOME' or 'WORK' would assert a fact not in evidence; 'OTHER'
-- explicitly signals "we do not know the location kind for this session."
UPDATE manual_charge_entries
    SET location_kind = 'OTHER'
WHERE location_kind IS NULL;

-- Step 2: Apply the NOT NULL constraint. This ALTER succeeds only because Step 1
-- has already eliminated all NULLs. Reversing the order — ALTER first, then UPDATE
-- — would fail immediately on a non-empty database that has NULL rows.
ALTER TABLE manual_charge_entries
    ALTER COLUMN location_kind SET NOT NULL;

-- +goose Down

-- Reverse: drop the NOT NULL constraint, restoring the column to nullable.
-- The backfill UPDATE from the Up migration is NOT reversed — rows that were NULL
-- before the Up migration now carry 'OTHER'. This is acceptable: the Down migration
-- is a schema rollback, not a data rollback; the data change is irreversible by
-- design (we cannot know which rows were originally NULL).
ALTER TABLE manual_charge_entries
    ALTER COLUMN location_kind DROP NOT NULL;
```

### Why this order (backfill before ALTER)

Postgres evaluates the `NOT NULL` constraint when the `ALTER COLUMN ... SET NOT NULL` statement
executes. If any existing row has `location_kind IS NULL` at that moment the ALTER fails with
`ERROR: column "location_kind" of relation "manual_charge_entries" contains null values`. The
UPDATE must run first so the ALTER sees a column with no NULLs. This is the standard two-step
pattern for adding a NOT NULL constraint to a table that may contain existing rows.

---

## D2 — Why No DB DEFAULT

A `DEFAULT 'OTHER'` on the column would silently fill in `'OTHER'` whenever the application
omits `location_kind` — hiding a programming mistake (a caller that forgot to supply the field).
The intent of this change is to **fail loudly** when the field is missing, not to paper over it
with a fallback. Two layers enforce this:

1. **Service validation** (D3 below) — rejects nil or empty `LocationKind` before the INSERT/UPDATE
   reaches the database.
2. **DB NOT NULL constraint** — the last line of defence; rejects any row where the application
   layer somehow bypassed validation.

A `DEFAULT` would make both layers invisible to a buggy caller that omits the field: the row
would be created successfully with `location_kind = 'OTHER'`, the caller would see no error, and
the data would be silently wrong. Without a DEFAULT, any bypass of the service validation hits
the NOT NULL constraint and returns an error — the right behavior for a required field.

**Rejected alternative:** `DEFAULT 'OTHER'` on the column. Rejected because it masks a missing
field instead of failing loudly (see above).

---

## D3 — Service Validation: Both Create and Update

The `writerService.Create` and `writerService.Update` methods in `service.go` gain a required
check on `LocationKind` before building DB params:

```go
if e.LocationKind == nil || *e.LocationKind == "" {
    return Entry{}, errors.New("manualcharge: location_kind is required")
}
```

This validation fires before any pgtype conversion or database call. The error message is
prefixed with `"manualcharge:"` to follow the existing convention in the file.

### Why both Create and Update

The existing spec requirement "Edit an existing entry" already states: "Update SHALL enforce the
same field constraints as create." `location_kind` is now a required field on Create; the same
rule therefore applies on Update. A user who corrects an existing entry must also supply a
`location_kind`. This is consistent with how `EnergyAddedKWh`, `Price`, and other required
fields are already treated (the UPDATE SQL sets them unconditionally from the params).

**Confirmed decision RD2 (grill-me, 2026-07-19):** required `location_kind` applies to both
create and update paths.

---

## D4 — Why LocationKind Stays *string

The domain field `Entry.LocationKind` is declared as `*string` (pointer). Changing it to `string`
(non-pointer) today would immediately break the `internal/gateway` build: the gateway tier
(RM3-gateway-add-manual-charge-ui, planned Tier 2) constructs `manualcharge.Entry` structs and
currently assigns `LocationKind` as `*string`. Until Tier 4 (`RM4-gateway-require-location-kind`)
updates those call sites, changing the Go type is a compile-time break.

The required-ness of `location_kind` is already guaranteed by two mechanisms without changing
the Go type:

- **Service validation** (D3): rejects nil or empty `LocationKind` before the DB call.
- **DB NOT NULL constraint** (D1): the database itself rejects any row with a NULL value,
  regardless of how it arrived.

The pointer type (`*string`) is therefore kept as a compatibility bridge for the inter-tier gap.
Changing it to `string` is Tier 4 work, which also updates the gateway call sites atomically.

**Confirmed decision RD1 (grill-me, 2026-07-19):** field stays `*string`; NOT NULL + service
validation enforce required-ness without a type change.

**Rejected alternative:** changing `LocationKind *string` → `string` in this tier. Rejected
because it breaks the `internal/gateway` build before Tier 4 lands.

---

## D5 — Index Plan (No New Index)

`NOT NULL` is a column constraint, not an index. Applying it does not create an index and does
not affect any existing index. The two indexes introduced in the original RM3 migration are
unchanged:

- `idx_manual_charge_entries_vehicle_time (account_id, tesla_id, charged_on DESC)`
- `idx_manual_charge_entries_account_time (account_id, charged_on DESC)`

`location_kind` is **not a query predicate** in either read path. Neither `ListEntriesByVehicle`
nor `ListEntriesByAccount` filters or sorts by `location_kind` — both queries filter by
`account_id` (and optionally `tesla_id`) and sort by `charged_on DESC`. Adding an index on
`location_kind` would add write overhead with no read benefit on these paths.

**No index is added or needed.** This is an explicit, deliberate decision consistent with the
project's read-heavy performance profile: index only columns that appear in WHERE or ORDER BY
clauses on the hot read paths.

---

## D6 — No sqlc Regeneration Required

The column's Go type in the generated `manualchargedb` package is `pgtype.Text` regardless of
whether the column is nullable or `NOT NULL`. pgx/v5's sqlc code generator does not distinguish
nullable from non-nullable `TEXT` at the Go type level — both map to `pgtype.Text`. The
`location_kind` parameter already appears in both `CreateEntryParams` and `UpdateEntryParams` as
`pgtype.Text`; the service already passes it via `stringPtrToPgText(e.LocationKind)`.

After the migration the DB will reject a NULL value, but the generated code does not need to
change to express that. `sqlc generate` / `make sqlc` is NOT required for this change.

---

## Summary of Design Decisions

| ID | Decision | One-line rationale |
|----|----------|--------------------|
| D1 | Defensive UPDATE then ALTER SET NOT NULL | Backfill must precede the constraint or ALTER fails on existing NULLs |
| D2 | No DB DEFAULT on location_kind | DEFAULT masks a missing field; NOT NULL + service validation must fail loudly |
| D3 | Validate LocationKind in both Create and Update | Update enforces same field constraints as Create (existing spec rule) |
| D4 | Keep LocationKind as *string domain field | Changing to string breaks gateway build before Tier 4 updates call sites |
| D5 | No new index on location_kind | NOT NULL is a constraint not an index; location_kind is not a query predicate on either hot read path |
| D6 | No sqlc regeneration needed | pgtype.Text maps nullable and NOT NULL TEXT identically; no query.sql changes required |
