# Sync proposal — telemetry (guide: telemetry-tables)

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `architecture/telemetry-tables.md`
Source spec:  `openspec/specs/telemetry/spec.md`
Generated:    2026-09-12
Status: APPLIED 2026-09-11

---

## [guide] ## Glossary — REPLACE

- **Known as:** `vehicle_snapshots`, `poll_attempts`, `supercharger_history`, `poll_runs`,
  `captured_date`, `raw_data`, `sentry_mode`, `max_range_charge_counter`,
  `battery_pct_source`, `start_battery_pct`, `end_battery_pct`, `telemetry schema`,
  `polled_by_account_id`, `supercharger_sessions` (the old name), `charge_gaps` (moved away)
- **Internal name:** the `telemetry` Postgres schema. `internal/telemetry` is its sole owner.

## [index] ## Architecture topics — ADD ROWS

| `polled_by_account_id` | the `poll_attempts` column that records whose token paid for the call (was `account_id`) → `architecture/telemetry-tables.md` |

---

## MANUAL EDITS NEEDED — review these by hand before `apply-sync`

`from-spec` does not rewrite a guide's table/column map: a capability spec carries behavior,
not schema. But the re-key made three lines of `## The tables` factually wrong. Fix them by
hand. Each correction below is verified against the migration
`internal/telemetry/db/migrations/20260911000002_rekey_vehicle_snapshots_on_tesla_id.sql`.

**1. `## The tables` → `vehicle_snapshots`, the uniqueness rule (around line 42)**

- Now reads: at most one row per `(account_id, tesla_id, captured_date)`, enforced by the
  `vehicle_snapshots_account_tesla_date_unique` constraint.
- Should read: at most one row per `(tesla_id, captured_date)`, enforced by the
  `vehicle_snapshots_tesla_date_unique` constraint. The account is no longer part of the key,
  because exactly one elected account polls each car per cycle.

**2. `## The tables` → `vehicle_snapshots`, the column list (around line 45)**

- Now reads: `Columns: account_id, tesla_id, captured_at, …`
- Should read: `Columns: tesla_id, captured_at, …`. `account_id` still exists but is
  **NULLABLE and no longer written or read**. It was kept, not dropped, because the analytics
  migration `20260908000002_add_tpms_pressure_columns.sql` joins on it, and migrations apply
  one module directory at a time, so telemetry runs first. The drop is a follow-up, tracked
  as Linear MAG-76.

**3. `## The tables` → `poll_attempts`, the column list (around line 61)**

- Now reads: `account_id, tesla_id, attempted_at, outcome, reason`
- Should read: `polled_by_account_id, tesla_id, attempted_at, outcome, reason`. The column was
  renamed. It records which account's token made the Fleet API call. It is not a key, and no
  read filters on it.

**4. `INDEX.md`, the `vehicle_snapshots` row (around line 253)**

- Now reads: the nightly per-vehicle snapshot — one row per (account, vehicle, `captured_date`)
- Should read: the nightly per-vehicle snapshot — one row per (vehicle, `captured_date`)
