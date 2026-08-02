## Context

`internal/telemetry` — tire-pressure typed-column extraction. The four TPMS pressure
values (`tpms_pressure_fl/fr/rl/rr`, in bar) are already in every stored
`vehicle_snapshots.raw_data` JSONB payload; only the typed-column extraction and the
domain-type companion are missing. This change mirrors exactly the prior
`RM2-telemetry-add-charging-stats` Source A change: add nullable columns via a goose
migration, extend `Snapshot`, extend `snapshotFrom`, and project the new columns in the
existing sqlc queries — no new read method, no new index.

Primary module: **`internal/telemetry/`**. No gateway changes (D2/D3 are out of scope
for this change — gateway display wiring is deferred to a follow-on gateway change).

## Goals / Non-Goals

**Goals:**

- Add four nullable `REAL` columns to `vehicle_snapshots` via a goose migration.
- Add four `*float64` pointer fields on `telemetry.Snapshot` (bar, API-native).
- Add a `barToPSI` package-level constant alongside `milesToKm`.
- Add four nil-safe value-receiver companion methods (`TpmsPressureFLPSI()`, etc.),
  one per corner, returning `*float64`.
- Extend `snapshotFrom` to pointer-wrap the four TPMS DTO values.
- Ensure the Tesla adapter DTO (`VehicleStateTesla`) exposes the four TPMS fields; add
  them if absent (they are absent — see D0 below).
- Project the four new columns in the existing sqlc SELECT queries so `LatestSnapshotsByAccount`
  and `SnapshotsByVehicleSince` return the enriched snapshot.

**Non-Goals:**

- Gateway rendering, averaging, or "—" fallback display (D2 / D3 are deliberately
  deferred to a follow-on gateway change — this change stops at the telemetry port
  returning four nil-safe fields with PSI companions).
- Low-pressure alerting.
- Tire-pressure history charts (unlocked for free once columns exist, but a separate feature).

---

## Design Decisions

### D0 — Tesla adapter DTO enrichment (precondition)

`VehicleStateTesla` in `internal/tesla/types.go` does **not** currently have the four
TPMS pressure fields. They must be added as a leaf, additive DTO enrichment before any
telemetry tasks can use them. This is NOT a new port method — `VehicleData` already
returns `VehicleStateTesla`, and its call path already captures the whole
`vehicle_state` block in `raw_data`. The four fields are:

```go
// TPMS (tire-pressure monitoring system) pressures in bar — API-native.
// These are plain float64 (not pointer) in the DTO: the Fleet API always
// includes them in vehicle_state when the vehicle has TPMS sensors.
// snapshotFrom pointer-wraps them (D12/DSA3 convention) so a reported 0.0
// bar is stored non-NULL and pre-migration rows stay NULL.
TpmsPressureFL float64 `json:"tpms_pressure_fl"`
TpmsPressureFR float64 `json:"tpms_pressure_fr"`
TpmsPressureRL float64 `json:"tpms_pressure_rl"`
TpmsPressureRR float64 `json:"tpms_pressure_rr"`
```

**No `Raw*` method needed.** `VehicleDataRaw` already returns the verbatim
`vehicle_data` payload; the TPMS values are in `vehicle_state` within that payload.
No new `VehicleService` interface method is required (the `raw.go` / explore-tesla-api
sync rule applies only to NEW typed port methods, not to additive DTO field additions).

**No `internal/tesla/AGENTS.md` sync rule violation.** The AGENTS.md sync rule
("whenever a new Fleet API call is added to `internal/tesla`") covers new _methods_,
not JSON struct field additions. This is a leaf-additive struct change only.

### D1 — Companion method shape (BINDING — overrides proposal's D1)

The proposal floated "four `*float64` fields + a `barToPSI` constant the gateway
formats against, with no companion method." That conflicts with the go-conventions
non-negotiable: every API-native unit field must have a value-receiver companion method
(the `OdometerKm()` pattern), nil-safe for pointer fields. The correct design is:

**Fields on `telemetry.Snapshot`:**

```go
// TpmsPressureFL is the front-left tire pressure in bar (API-native). nil when
// the vehicle did not report TPMS at capture OR the row predates this extraction.
// A truthful reported 0.0 bar is stored non-NULL (pointer-wrapped via ptr() in
// snapshotFrom — D12/DSA3 convention). Use TpmsPressureFLPSI() for PSI.
TpmsPressureFL *float64
TpmsPressureFR *float64
TpmsPressureRL *float64
TpmsPressureRR *float64
```

**Package-level constant (alongside `milesToKm`):**

```go
// barToPSI is the exact bar→PSI factor. Every bar field on a domain type exposes
// a companion *float64 value-receiver method (ai/go-conventions.md non-negotiable).
// Tesla sends tire pressure in bar; PSI is derived on read, never stored.
const barToPSI = 14.503773773
```

**Four nil-safe value-receiver companion methods:**

```go
func (s Snapshot) TpmsPressureFLPSI() *float64 {
    if s.TpmsPressureFL == nil {
        return nil
    }
    return ptr(*s.TpmsPressureFL * barToPSI)
}
// ...repeated for FR, RL, RR
```

**Return type: `*float64`** (not `float64`). Rationale: these fields are pointer
fields. The `OdometerKm()` returns `float64` because `Odometer` is a non-pointer
field (it is always present in the DTO). `TpmsPressureFL` is `*float64` because the
vehicle may not report TPMS — so the nil/not-reported distinction must survive the
conversion. A `*float64 → float64` companion would silently collapse nil into 0.0,
losing the "not reported" signal. The `nil → nil` companion is the correct nil-safe
shape (analogous to how the charge-enrichment fields handle nil), consistent with
"nil-safe for pointer fields" in `ai/go-conventions.md`.

**Rejected alternatives:**

- "Constant-only, gateway converts, no method" — violates the companion-method
  non-negotiable in `ai/go-conventions.md`. Rejected.
- "Single `TpmsPSI() [4]float64` array helper" — over-abstraction for a display
  value; loses per-corner nil fidelity (an array of `float64` cannot represent
  "corner not reported" vs "reported 0.0 PSI"). Rejected.

### D2 — Dashboard display unit (OUT OF SCOPE)

Which unit (PSI vs bar) the dashboard hero mini-stat renders is a gateway-display
decision. This change stops at the telemetry port returning the four nil-safe bar
fields and their PSI companions. The gateway change that follows will wire the display.

### D3 — Which corner shown in the hero mini-stat (OUT OF SCOPE)

The gateway decides how to aggregate or select across the four returned corners
(e.g. average of non-NULL, front-left only, etc.). This change does not implement
any aggregation logic in the telemetry module.

---

## Schema: Four New Nullable Columns

### DDL (goose migration)

**Filename:** `internal/telemetry/db/migrations/20260802000001_add_tpms_pressure_columns.sql`

Timestamp `20260802000001` is the next available after `20260801000001`
(`add_maxrange_drop_location_fastchargertype.sql`). The `000001` suffix preserves the
existing convention (one migration per date-slot, incrementing the last part).

```sql
-- internal/telemetry — add TPMS pressure typed columns to vehicle_snapshots.
-- Four nullable REAL columns: tpms_pressure_{fl,fr,rl,rr} in bar (API-native).
-- NULL semantics: NULL means the vehicle did not report TPMS at capture (no sensors,
-- absent reading) OR the row predates this extraction (pre-migration rows). A reported
-- 0.0 bar is stored non-NULL — the "NULL is reserved exclusively for pre-migration rows
-- / not reported" invariant (telemetry AGENTS.md DTO/units conventions). No backfill:
-- values remain in raw_data->vehicle_state for any row that needs retroactive extraction.
--
-- No new index: tpms_pressure_* are projected columns read alongside battery/odometer
-- on the same heap row. There is no new query predicate — the existing
-- idx_vehicle_snapshots_vehicle_time (account_id, tesla_id, captured_at) continues to
-- serve all read paths (design D1, no-index justification).
--
-- Owned by internal/telemetry. No cross-module FK. No raw_data change.

-- +goose Up

ALTER TABLE vehicle_snapshots
    ADD COLUMN tpms_pressure_fl REAL,    -- bar; NULL when not reported or pre-migration
    ADD COLUMN tpms_pressure_fr REAL,    -- bar; NULL when not reported or pre-migration
    ADD COLUMN tpms_pressure_rl REAL,    -- bar; NULL when not reported or pre-migration
    ADD COLUMN tpms_pressure_rr REAL;    -- bar; NULL when not reported or pre-migration

-- +goose Down

ALTER TABLE vehicle_snapshots
    DROP COLUMN IF EXISTS tpms_pressure_fl,
    DROP COLUMN IF EXISTS tpms_pressure_fr,
    DROP COLUMN IF EXISTS tpms_pressure_rl,
    DROP COLUMN IF EXISTS tpms_pressure_rr;
```

### NULL semantics rationale

Matches the charge-enrichment precedent (RM2-telemetry-add-charging-stats Source A,
design DSA1 / DSA3):

- **NULL** = vehicle did not report TPMS at capture (some vehicles have no TPMS
  sensors, or the reading was absent) OR the row predates this extraction.
- **Non-NULL 0.0** = a truthfully reported zero bar pressure. Stored non-NULL because
  `snapshotFrom` stores the ACTUAL DTO value pointer-wrapped via `ptr()` (D12/DSA3
  convention). A zero reading is a real (alarming) measurement and must not be silently
  discarded.
- On the read path, a `nil` domain field means "row predates enrichment or not
  reported" — never "reported zero".

The existing AGENTS.md invariant: "NULL is reserved exclusively for pre-migration rows
/ not reported" — preserved here.

### Index plan: no new index

Tire pressure is read on the **same heap row** as battery level, odometer, and charge
enrichment fields. There is no new query predicate — no `WHERE tpms_pressure_fl < X`
or `ORDER BY tpms_pressure_fl` in any existing or planned query. The
`(account_id, tesla_id, captured_at)` index continues to serve all read paths
(`LatestSnapshotsByAccount` via `DISTINCT ON`, `SnapshotsByVehicleSince` via range
scan). The new columns ride along for free on the row fetch.

This aligns directly with the read-heavy Performance-Profile (CLAUDE.md): "writes are
mostly done by pollers at midnight, so denormalizing, indexing aggressively, and
precomputing for reads is acceptable." Here, the correct read-optimization is to have
no new index — because there is no new predicate — and to let the extra projected
columns be free on the existing row fetch.

**Rejected alternative — index on one TPMS column:** would cost write overhead on
every nightly insert and benefit no read query shape. Rejected.

---

## Write Path

### `snapshotFrom` extension (`internal/telemetry/service.go`)

Extend `snapshotFrom` to pointer-wrap the four `data.VehicleState.Tpms*` values using
the existing `ptr()` helper (same D12/DSA3 convention as the charge-enrichment fields):

```go
// TPMS pressure enrichment — actual DTO values, pointer-wrapped (D12/DSA3).
// ptr(v) returns &v; a 0.0 bar is a truthful reading and is stored non-NULL.
// NULL is reserved for pre-migration rows (not backfilled; values in raw_data).
TpmsPressureFL: ptr(data.VehicleState.TpmsPressureFL),
TpmsPressureFR: ptr(data.VehicleState.TpmsPressureFR),
TpmsPressureRL: ptr(data.VehicleState.TpmsPressureRL),
TpmsPressureRR: ptr(data.VehicleState.TpmsPressureRR),
```

`dbStore.insertSnapshot` must also pass these four fields to
`InsertVehicleSnapshotParams`, mapping `*float64 → pgtype.Float8` using the existing
`float64PtrToPgFloat8` helper already in `service.go`.

---

## Read Path

### sqlc query edits (`internal/telemetry/db/query.sql`)

Four queries include explicit column lists and must be updated to project the new
columns (no `SELECT *` convention — explicit columns generate stable sqlc structs):

1. `InsertVehicleSnapshot` — add `tpms_pressure_fl, tpms_pressure_fr, tpms_pressure_rl,
   tpms_pressure_rr` to the INSERT column list and the corresponding `@tpms_pressure_*`
   params to the VALUES clause.
2. `ListSnapshotsByVehicle` — add the four column names to the SELECT list.
3. `SnapshotsByVehicleSince` — add the four column names to the SELECT list.
4. `LatestSnapshotsByAccount` — add the four column names to the SELECT list.

After editing `query.sql`, run `make sqlc` to regenerate `telemetrydb`.

### `rowToSnapshot` extension (`internal/telemetry/mapping.go`)

Map the four new nullable sqlc columns to the `Snapshot` pointer fields using the
existing `pgNullableFloat64` helper (already in `mapping.go` from the charge-enrichment
change):

```go
TpmsPressureFL: pgNullableFloat64(r.TpmsPressureFL),
TpmsPressureFR: pgNullableFloat64(r.TpmsPressureFR),
TpmsPressureRL: pgNullableFloat64(r.TpmsPressureRL),
TpmsPressureRR: pgNullableFloat64(r.TpmsPressureRR),
```

No new `Reader` method. `LatestSnapshotsByAccount` and `SnapshotsByVehicleSince` return
the enriched `Snapshot` automatically once the SELECT lists and scan are updated.

---

## Scope Boundary

**This change ends at the telemetry port.** The gateway display wiring (D2 — unit,
D3 — which corner for the hero mini-stat) is a deliberate follow-on gateway change.
This is not an oversight — it is the modular boundary enforced by the pipeline config
("one concern per module, one change per module"). The telemetry side is complete when
the `Reader.LatestSnapshotsByAccount` return value carries four nil-safe `*float64`
fields in bar plus their `*float64 PSI()` companion derivations.

---

## Risks / Trade-offs

- **Nullable charge enrichment on pre-existing snapshots.** NULL in the four new
  columns is expected and correct for rows before this migration. Dashboard and test
  code must handle nil for these fields.
- **DTO field type is plain `float64` (not pointer).** Tesla's `vehicle_state` payload
  always includes the four TPMS keys when the vehicle has TPMS sensors; when the
  vehicle has no TPMS sensors the keys are simply absent (JSON null or missing field).
  Because Go's JSON unmarshaling maps a missing or null key to the zero value for a
  plain `float64`, using `ptr()` in `snapshotFrom` means a vehicle without TPMS still
  stores `*0.0` (non-NULL) rather than `nil`. If per-vehicle TPMS absence detection
  is needed in the future, the DTO fields should be changed to `*float64` — but that
  is a follow-on concern. For now, `ptr()` preserves the D12/DSA3 convention
  (actual value, no zero-is-absent heuristic), and the raw_data provides the ground
  truth for any retroactive determination.
- **No test fires a live Tesla API call.** All tests are offline with fake ports or
  DATABASE_URL-gated. The `go test ./...` invariant is preserved.

---

## Migration Plan (implementation order for the worker)

1. Add `TpmsPressureFL/FR/RL/RR float64` to `VehicleStateTesla` in
   `internal/tesla/types.go` (D0 — prerequisite for all other tasks).
2. Add goose migration `20260802000001_add_tpms_pressure_columns.sql` (schema DDL above).
3. Add `barToPSI` constant + four `TpmsPressure*` fields on `Snapshot` + four nil-safe
   PSI companion methods to `internal/telemetry/telemetry.go`.
4. Extend `snapshotFrom` + `dbStore.insertSnapshot` in `service.go` (pointer-wrap via
   `ptr()` + pgtype mapping).
5. Edit the four sqlc SELECT queries in `query.sql` to project the new columns; run
   `make sqlc` to regenerate.
6. Extend `rowToSnapshot` in `mapping.go` to map the new columns.
7. Add unit tests for the four PSI companion methods (nil-in → nil-out; non-nil
   bar → correct PSI via `barToPSI`).
8. Add DATABASE_URL-gated integration tests: nil round-trip (nil → NULL → nil),
   non-nil round-trip (0.0 stored non-NULL, value preserved), pre-migration NULL fidelity.
