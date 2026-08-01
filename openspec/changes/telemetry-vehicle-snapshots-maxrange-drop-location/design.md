## Context

This design is for the change `telemetry-vehicle-snapshots-maxrange-drop-location`. It trims and
improves the typed column surface of `vehicle_snapshots`:

- **ADD** `max_range_charge_counter INTEGER` (nullable) — the lifetime count of times the vehicle
  has been charged to its true 100 % Maximum-Battery-Range limit, a first-class charging-habits
  health signal.
- **DROP** `latitude`, `longitude` (unused typed columns; values remain recoverable from
  `raw_data->'drive_state'`).
- **DROP** `fast_charger_type` (low-value free-text brand string; no dashboard filters/sorts on
  it; values remain recoverable from `raw_data->'charge_state'`).

All dropped columns are present in the lossless `raw_data JSONB` — no historical information is
lost. The `raw_data` column itself is NOT affected.

---

## Resolved open question: `raw_data` JSON path for `max_range_charge_counter`

**Path confirmed: `raw_data->'charge_state'->>'max_range_charge_counter'`.**

Evidence: inspecting `internal/tesla/types.go`, `VehicleDataTesla` embeds `ChargeState ChargeStateTesla json:"charge_state"`. The `max_range_charge_counter` field does NOT yet appear on `ChargeStateTesla` — it must be added to the adapter DTO as `MaxRangeChargeCounter int json:"max_range_charge_counter"`. The Tesla Fleet API documents this field under the `charge_state` object of the `vehicle_data` response. The integer value is the lifetime counter (e.g. `3` = charged to max range three times). The `vehicle_state` block (`VehicleStateTesla`) has no such field.

The backfill query and `snapshotFrom` extraction therefore operate on `charge_state.max_range_charge_counter`, not `vehicle_state`.

---

## Schema change

### New migration

File: `internal/telemetry/db/migrations/20260801000001_add_maxrange_drop_location_fastchargertype.sql`

Timestamp `20260801000001` is the next value after `20260716000002` (use the current date prefix).

```sql
-- internal/telemetry — vehicle_snapshots refactor:
--   ADD max_range_charge_counter (nullable int, lifetime charge-to-max-range counter from
--       charge_state.max_range_charge_counter in raw_data).
--   DROP latitude, longitude (unused typed columns; lossless in raw_data->drive_state).
--   DROP fast_charger_type (low-value free-text; lossless in raw_data->charge_state).
--
-- Nullable: pre-migration rows predate extraction and stay NULL rather than being
-- misrepresented as 0. A real reported 0 is stored as non-NULL (pointer-wrapped in
-- snapshotFrom — see DSA3/D12 convention from RM2 design.md). NULL means "not yet
-- extracted", never "counter was zero".
--
-- No new index: max_range_charge_counter is not a filter/sort column on any hot read path;
-- see Index Plan section below.
--
-- Owned by internal/telemetry; no cross-module FK. No raw_data change.

-- +goose Up

ALTER TABLE vehicle_snapshots
    ADD COLUMN max_range_charge_counter INTEGER;    -- nullable; NULL for pre-migration rows

-- One-shot backfill: populate max_range_charge_counter from raw_data for rows that
-- already have the field in their charge_state payload. Rows whose raw_data lacks the
-- path (e.g. older snapshots before Tesla started reporting it, or snapshots captured
-- before this field existed in the DTO) stay NULL — correct behavior, not a data loss.
UPDATE vehicle_snapshots
SET max_range_charge_counter =
        (raw_data -> 'charge_state' ->> 'max_range_charge_counter')::INTEGER
WHERE jsonb_typeof(raw_data -> 'charge_state' -> 'max_range_charge_counter') = 'number';

ALTER TABLE vehicle_snapshots
    DROP COLUMN IF EXISTS latitude,
    DROP COLUMN IF EXISTS longitude,
    DROP COLUMN IF EXISTS fast_charger_type;

-- +goose Down

-- Re-add the dropped columns as nullable (DOWN cannot recover the original data — values
-- remain in raw_data but this migration does not back-populate them). Honestly documented:
-- a Down migration after data loss from a DROP COLUMN is best-effort schema restoration
-- only; actual historical values are not recovered here (only re-derivable from raw_data).
ALTER TABLE vehicle_snapshots
    ADD COLUMN IF NOT EXISTS latitude        DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS longitude       DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS fast_charger_type TEXT;

ALTER TABLE vehicle_snapshots
    DROP COLUMN IF EXISTS max_range_charge_counter;
```

**Down migration data-recovery caveat (explicit):** `DROP COLUMN latitude / longitude /
fast_charger_type` is destructive — the column data is gone after the Up migration runs. The
Down migration re-adds the columns as nullable (`IF NOT EXISTS`, all NULLs), giving a valid
schema to roll back to, but it does NOT back-populate values. Historical values are re-derivable
from `raw_data` at any time via the same JSONB paths:
- `raw_data->'drive_state'->>'latitude'` / `'longitude'` — for latitude/longitude
- `raw_data->'charge_state'->>'fast_charger_type'` — for fast_charger_type

This is consistent with the `raw_data JSONB` insurance hedge established by
`ai/architecture.md §7` and `ai/go-conventions.md §Persistence`.

---

## Rationale

### Why ADD `max_range_charge_counter` as a typed column?

The field is a first-class charging-habits health metric (one of the `AGENTS.md` Primary
Objectives). Any dashboard that needs it currently must run `raw_data->'charge_state'->>'max_range_charge_counter'`
on every read — a JSONB extraction on the hot path, which the architecture explicitly prohibits
(`ai/architecture.md §7`: "never force a dashboard to extract from JSONB on the hot path"). Promoting
it to a typed `INTEGER` column puts it on the same read path as `battery_level`, `odometer`, etc. —
cheap, indexed (by the existing composite scan), no JSONB probe.

### Why nullable, not NOT NULL with a default of `0`?

Pre-migration rows predating this extraction must stay `NULL`, not `0`. A stored `0` would
incorrectly claim "this vehicle has charged to max-range zero times" for every historical row,
which is false and actively misleading for any health trend. `NULL` means "counter not yet
extracted from this snapshot". A real reported counter of `0` is also valid (new vehicle, never
charged to max-range) and is stored as a non-NULL `0` via pointer-wrapping in `snapshotFrom`
(the RM2 D12/DSA3 convention: `ptr(data.ChargeState.MaxRangeChargeCounter)` always yields a
non-NULL value for every new capture, so a `0` reading is truthful non-NULL).

### Why pointer-wrapped `*int` on the domain `Snapshot` struct?

Same as the six RM2 charge-enrichment fields: a nil pointer means "not extracted / pre-migration
row". A non-nil `*0` means "counter reported as zero". Collapsing nil into 0 would lose the
distinction between "row predates this extraction" and "vehicle has never charged to max-range".

### Why DROP `latitude` / `longitude`?

Zero current consumers. A grep across `internal/gateway/handlers` finds no read of
`Snapshot.Latitude` or `Snapshot.Longitude` in any handler. The nightly collector writes them
(`DriveStateTesla.Latitude / Longitude`), but nothing reads them through the `Reader` port. They
add two `float64` columns per row (~16 bytes) while providing no dashboard return. Privacy-
sensitive (GPS coordinates of where the car is parked every night) with zero analytical value.
The exact GPS position at capture time is preserved forever in `raw_data->'drive_state'` — no
information is lost. If a future geo-fence or trip-detection feature needs location, it reads from
`raw_data` or adds a purpose-built table (not a nightly-snapshot column).

### Why DROP `fast_charger_type`?

Added in RM2 as one of six charge-enrichment columns; in practice it is a short free-text brand
string (`"Tesla"`, `"Combo"`, `""`) that no dashboard filters, sorts, or aggregates on. The
charge-brand signal is already captured more usefully by the dedicated `supercharger_sessions`
ledger (per-session `site_location_name` + `billing_type`). Removing it trims one unused TEXT
column while the value stays recoverable from `raw_data->'charge_state'->>'fast_charger_type'`
for any future need.

### Rejected alternatives

**JSONB probe on read instead of promoted column for `max_range_charge_counter`:** rejected
because it violates the hard project convention "never force a dashboard to extract from JSONB on
the hot path" (`ai/architecture.md §7`, `ai/go-conventions.md §Persistence`). A typed promoted
column is the established pattern for any field a dashboard will read repeatedly.

**NOT NULL with default 0 for `max_range_charge_counter`:** rejected — a default of 0 falsely
assigns "never charged to max-range" to all historical rows. NULL is the correct sentinel for
"not yet extracted".

**Keep `latitude`/`longitude` for future use:** rejected — storing GPS on every nightly snapshot
is a pre-optimistic allocation with zero current return and ongoing privacy exposure. The raw JSONB
hedge means we can re-derive at any time; there is no reason to pay the column cost until a
concrete consumer exists.

**Keep `fast_charger_type` for general charge insight:** rejected — the `supercharger_sessions`
ledger already captures the per-session charger type at higher fidelity. A nightly snapshot column
captures a point-in-time string at a single midnight moment; the ledger captures the actual
session brand. The nightly snapshot column is redundant.

---

## Index plan

### Existing indexes on `vehicle_snapshots`

From `internal/telemetry/db/migrations/20260710000002_init_telemetry.sql` (reviewed):

| Index | Columns | Serves |
|---|---|---|
| `idx_vehicle_snapshots_account` | `(account_id, tesla_id, captured_at)` | `LatestSnapshotsByAccount` — `DISTINCT ON (account_id, tesla_id)` with `ORDER BY account_id, tesla_id, captured_at DESC` |
| `idx_vehicle_snapshots_vehicle_time` | `(account_id, tesla_id, captured_at)` | `SnapshotsByVehicleSince` — forward range scan `WHERE account_id=$1 AND tesla_id=$2 AND captured_at >= $3 ORDER BY captured_at ASC LIMIT 400` |

(Both indexes are effectively the same covering composite — one may be the primary and the other
an alias; both are served by the `(account_id, tesla_id, captured_at)` B-tree.)

### Does `max_range_charge_counter` need a new index?

**No.** The two existing read methods (`LatestSnapshotsByAccount` and `SnapshotsByVehicleSince`)
select the whole snapshot row — `max_range_charge_counter` is just another scalar column in the
`SELECT *` projection. Neither method filters or sorts on `max_range_charge_counter`. The
planner's index scan on `(account_id, tesla_id, captured_at)` picks the right rows; the new
column is fetched as a heap projection alongside the other fields at zero extra index cost.

A dedicated `max_range_charge_counter` index would only benefit a query like:
```sql
WHERE account_id = $1 AND max_range_charge_counter > $2
```
No such query exists or is planned. Dashboard reads will show the counter alongside other
snapshot fields — they do not filter on it.

**Conclusion: no new index. Justify on re-read only if a concrete filter/sort read pattern on
`max_range_charge_counter` is introduced.**

The existing `(account_id, tesla_id, captured_at)` index is sufficient for all current and
planned reads. The read-heavy profile is upheld — column promotion gives cheap scalar reads
within the existing index scan, which is what the architecture demands.

---

## Boundary confirmation

- **No cross-module FK.** `vehicle_snapshots` has no FK into account or tesla tables — this
  remains unchanged.
- **No boundary change.** The `telemetry.Reader` interface signature is unchanged — it still
  returns `[]Snapshot`. Only the `Snapshot` struct's fields change (add `MaxRangeChargeCounter *int`,
  remove `Latitude float64`, `Longitude float64`, `FastChargerType *string`). Callers that
  currently do not use the removed fields compile without change; callers that set them in test
  fixtures must remove those assignments (the compiler flags them).
- **No new port method.** `Reader.LatestSnapshotsByAccount` and `Reader.SnapshotsByVehicleSince`
  are unchanged in signature.
- **`pgtype` stays confined.** `pgtype.Int4` for `max_range_charge_counter` never leaves
  `service.go` / `mapping.go` — converted to `*int` at the DB→domain boundary following the
  `pgNullableInt32AsInt` pattern from RM2.
- **The Tesla adapter stays faithful.** `internal/tesla/types.go` `ChargeStateTesla` gains
  `MaxRangeChargeCounter int json:"max_range_charge_counter"`. `Latitude`/`Longitude` stay on
  `DriveStateTesla`; `FastChargerType` stays on `ChargeStateTesla`. The adapter models the full
  Fleet API response regardless of whether telemetry promotes a field to a typed column — only
  telemetry drops the typed columns.
