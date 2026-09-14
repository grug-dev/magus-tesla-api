# charging tables — columns, constraints and index decisions

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.
> All KB links are relative to `kkpa/context/`.

## Glossary

- **Known as:** `manual_charge_entries`, `supercharger_sessions`, `monthly_effective_capacity`,
  `mirror_watermarks`, `charging schema`, `inferred_capacity_kwh_calc`, `energy_source`,
  `price_source`, `odometer_km`, `entry status`, `session status`, `generated column`,
  `charge_sessions` (the old name)
- **Internal name:** the `charging` Postgres schema. `internal/charging` is the sole owner of
  every table in it.

## Component map

| Layer | File / symbol | Role |
|---|---|---|
| schema source | `internal/charging/db/migrations/` | The only schema source of truth. No `schema.sql`. |
| generated | `internal/charging/db/` (package `chargingdb`) | sqlc output from `query.sql`. Module-private. |
| ports | `internal/charging/charging.go` | `Writer` / `Reader` / `SessionWriter` / `SessionReader` / `SessionVerifier` / `MonthlyCapacityCalculator`. |
| rules | `internal/charging/AGENTS.md` | Which tables the module owns, and the port contracts. Points here for column detail. |

Why this guide exists: the column-by-column detail below used to live in
`internal/charging/AGENTS.md`, which every worker dispatched to this module re-reads in
full. An agent changing one column was paying for all three tables. The ownership rules and
the port contracts stayed there; the schema detail is here.

`internal/charging` is the **sole owner** of two tables, both living in the dedicated
`charging` Postgres schema (`charging.manual_charge_entries`,
`charging.supercharger_sessions`) since `RM39-charging-move-to-own-schema` (MAG-31,
`internal/charging/db/migrations/20260902000003_move_charging_to_own_schema.sql`) — moved
out of `public`, in the same migration that renamed `charge_sessions` to
`supercharger_sessions` (design.md D5b) and the Go db model `ChargeSession` to
`SuperchargerSession` (design.md D5c). **Every** catalog object still carrying the old
table name was renamed with it (design.md D16) — the index, the named CHECK, the primary
key, the unique constraint, and the five CHECKs Postgres auto-named from inline column
constraints (`battery_pct_source`, `start`/`end_battery_pct`, and their `_est` siblings).
The completeness criterion is the **catalog, not a list**: no relation, index or constraint
owned by this module may have a name beginning `charge_sessions`. Verify with
`SELECT conname FROM pg_constraint WHERE conrelid = 'charging.supercharger_sessions'::regclass`
— the five auto-named CHECKs appear nowhere in this repo, so grep cannot confirm this.

### `manual_charge_entries`

- No other module may read or write this table directly (ai/architecture.md §2).
- All access goes through the `Writer` and `Reader` public Go interfaces.
- The migration file `internal/charging/db/migrations/20260718000001_add_manual_charge_entries.sql`
  is the single schema source of truth (no separate `schema.sql` — ai/go-conventions.md §persistence).
- sqlc generates `package chargingdb` into `internal/charging/db/` from `query.sql`
  against the migration directory. Only `service.go` and `session_writer.go` (inside
  this module) may import it.
- `inferred_capacity_kwh_calc` (MAG-25, charging-add-inferred-capacity,
  `internal/charging/db/migrations/20260829000001_add_inferred_capacity.sql`) —
  **engine-generated**: a PostgreSQL `GENERATED ALWAYS AS (...) STORED` column,
  written by nobody. Present (non-`NULL`) only when `start_battery_pct` and
  `end_battery_pct` are both non-`NULL` and `end_battery_pct > start_battery_pct`;
  `NULL` otherwise (an equal delta is a division by zero, a decreasing one a
  negative capacity — design.md D3). Recomputed automatically by the engine on
  every `INSERT`/`UPDATE` through `Writer.Create`/`Writer.Update`; unwritable by
  any caller (`428C9` on a direct attempt).
- **`status`, `energy_source`, `odometer_km`, and a relaxed `energy_added_kwh`**
  (MAG-18/RM33, RM33-charging-add-entry-status,
  `internal/charging/db/migrations/20260829000002_add_entry_status.sql`):
  - `status TEXT NOT NULL DEFAULT 'IN_PROGRESS' CHECK (status IN ('IN_PROGRESS','DONE'))`
    — every pre-existing row backfilled to `IN_PROGRESS` by the column `DEFAULT`
    (deliberate: historical entries surface as unreviewed, design.md D1). The
    required-field set per status lives in Go (`RequiredFieldsFor`), **not** as a
    second DB `CHECK` — a `CHECK` backstop would turn every future change to the
    skip set into a migration (design.md D5).
  - `energy_source TEXT NOT NULL DEFAULT 'USER' CHECK (energy_source IN ('USER','ESTIMATED'))`
    — always computed by this module, never accepted from a caller (design.md D4).
  - `odometer_km INTEGER CHECK (odometer_km >= 0)` — nullable; the odometer
    reading observed at the charge event.
  - `energy_added_kwh` **drops its `NOT NULL`** (was `NOT NULL` before this
    change). `CHECK (energy_added_kwh > 0)` is **retained unchanged** and still
    rejects `0` and every negative — a `CHECK` evaluates `NULL`, not `false`, on
    a `NULL` input, so the same constraint now also accepts `NULL` for free
    (design.md D2).
  - **No index was added on any of the three new columns** — none is
    predicated on by any read query in this tier; an index on a column nothing
    filters/orders/joins by is pure write and storage cost for no read benefit
    (design.md §Index Plan, D9). If a future change needs to filter by `status`,
    the revisit trigger is a **partial**, `tesla_id`-leading index over `WHERE
    status = 'IN_PROGRESS'` — not a standalone `(status)` index. The table's key
    became `tesla_id` in `RM58-charging-demote-manual-charge-account-id`: the
    column that led every index used to be `account_id`; the sole index on this
    table is now `idx_manual_charge_entries_vehicle_time` on `(tesla_id,
    charged_on DESC)`.
  - Energy may be **derived on write** via the unexported `packCapacityKWh(ctx, lookup,
    teslaID) (float64, error)` seam in `capacity.go`. Since RM52 tier 1 (MAG-32,
    RM52-charging-add-monthly-effective-capacity), it no longer returns a hardcoded `62.0`
    for every vehicle — this closes backlog #18. It reads the vehicle's newest measured
    capacity from `monthly_effective_capacity` (see §Public Interface and §Data Ownership)
    and falls back to `defaultPackCapacityKWh` (`62.0`) only when no measured row exists yet.
    The monthly job that fills that table (`monthly_capacity.go`) **filters `WHERE
    energy_source = 'USER'`** on manual entries and `WHERE status = 'DONE'` on sessions,
    exactly as this section's original rule asked, so it never averages a derived value back
    into itself (design.md D4/D7 of RM33-charging-add-entry-status; RM52's own design.md
    Context facts 3-4).
- **`price_source`** (RM51 tier 1, MAG-58, RM51-charging-derive-status-and-price-source,
  `internal/charging/db/migrations/20260909000001_add_price_source.sql`):
  - `price_source TEXT NOT NULL DEFAULT 'UNCONFIRMED' CHECK (price_source IN ('USER','UNCONFIRMED'))`
    — always computed by this module (`resolvePriceSource` in `service.go`), never
    accepted from a caller.
  - **Not indexed.** Nothing filters, orders, joins, or groups by this column, in this
    tier or any planned one (design.md D4/§Index Plan). If a future change needs to
    filter by `price_source`, the revisit trigger is a **partial**, `tesla_id`-leading
    index — not a standalone `(price_source)` index. See the `status` bullet above:
    the table's key is `tesla_id`, not `account_id`, since
    `RM58-charging-demote-manual-charge-account-id`.
  - **The `DEFAULT` alone does not implement the price-based rule.** A raw `INSERT` at
    the SQL level that omits `price_source` always lands on `'UNCONFIRMED'`, even for a
    positive `price` — a column `DEFAULT` cannot see another column's value. The
    `price > 0 ⇒ USER` half of the rule is enforced in exactly two places: the
    migration's own one-time backfill `UPDATE` (for rows that existed before this
    change) and `resolvePriceSource` in Go (on every future write, via `Writer`).

### `supercharger_sessions` (renamed from `charge_sessions` in RM39 tier 3, D5b; RM29 tier 6,
RM29-charging-add-charge-sessions)

- No other module may read or write this table directly. Access goes through the
  `SessionWriter` port (write) and, since RM30-charging-add-session-read-port, the
  `SessionReader` port (read) — see §Public Interface above.
- The migration file
  `internal/charging/db/migrations/20260823000001_add_charge_sessions.sql` is the single
  schema source of truth, including its one-time backfill of every Supercharger session
  already collected (guarded so it is a no-op on a `charging`-only database — design.md
  D8a).
- `internal/telemetry.supercharger_history` keeps its own copy of the same three
  battery-percentage columns (`start_battery_pct`, `end_battery_pct`,
  `battery_pct_source`) — permanently, not temporarily. Tier 3 of this roadmap
  (`RM41-telemetry-drop-estimate-columns`, 2026-09-03) dropped telemetry's own
  `start_battery_pct_est`/`end_battery_pct_est` pair, the same drop this change
  performed one tier earlier on `charging.supercharger_sessions`; neither table
  has carried an `_est` column since.
- **Keyed on `tesla_id`, not `account_id`.** `tesla_id BIGINT NOT NULL` identifies the
  vehicle; the table carries no `account_id` column at all. `UNIQUE (session_id)`
  (`supercharger_sessions_session_id_unique`) is the whole uniqueness rule — a
  Supercharger session belongs to exactly one car, so one row per `session_id` is
  enough. `idx_supercharger_sessions_vehicle_stop (tesla_id, charge_stop_date_time)`
  is the only index: it prunes to one vehicle and serves the ordered vehicle reads in
  the same scan.
- Column-by-column:
  - `vin`, `session_id`, `charge_start_date_time`, `charge_stop_date_time`,
    `site_location_name` — mirrored, **write-once**: telemetry never refreshes these
    either, so this table doesn't.
  - `tesla_id`, `energy_kwh`, `total_cost`, `currency`, `is_paid` — mirrored,
    **refreshed on every nightly pass** (telemetry's own `ON CONFLICT DO UPDATE SET`
    refreshes them too — fees settle, invoices finalize).
  - `start_battery_pct`, `end_battery_pct`, `battery_pct_source` — **charging-owned**,
    never mirrored, never written by the nightly sync (see §Public Interface above for
    why that is a compile error, not a discipline). Since
    RM31-charging-add-session-verification-port, these three (and only these three) are
    writable through `SessionVerifier.VerifySession` — a human-triggered write, never
    the nightly sync. `battery_pct_source` is always computed by that port, never
    supplied by a caller (design.md D2/D7 of that change). Since MAG-36
    (`charging-add-derived-start-battery-pct`), a `VerifySession` call that leaves
    `start_battery_pct` unsupplied MAY derive it from `energy_kwh` and the supplied end
    percentage — still writable only through this same port, no new writer, no schema
    change (see §Public Interface above). **Accepted trade-off (design.md D1 of MAG-36):** a
    derived value is stored under the same `battery_pct_source = 'user_verified'` value
    a human-typed one gets, so the two are indistinguishable in this column alone. RM52
    tier 1's `MonthlyCapacityCalculator` (MAG-32) is the capacity-averaging feature this
    limitation once warned about, and it works around it: it filters sessions by `status =
    'DONE'` instead of `battery_pct_source`, so a `DONE_CALCULATED` session (one whose
    `start_battery_pct` this port derived) is skipped, not averaged in. `battery_pct_source`
    itself still cannot tell a derived value from a typed one — that fact has not changed.
    Since RM41 tier 4 (MAG-45), a fourth
    charging-owned column, `status`, is computed from these two on every `VerifySession`
    call — see §Public Interface above and the new "Session lifecycle status"
    subsection for the full rule; `status` is not itself mirrored, refreshed, or
    engine-generated, it is Go-computed.
  - Deliberately **not** carried, and the list is closed: `country_code`,
    `unlatch_date_time`, `billing_type`, `vehicle_make_type`, `raw_data` (design.md D1).
  - `inferred_capacity_kwh_calc` (MAG-25, charging-add-inferred-capacity) —
    a **fourth** category, alongside mirrored-write-once / mirrored-refreshed /
    charging-owned: **engine-generated**. Written by nobody — a PostgreSQL
    `GENERATED ALWAYS AS (...) STORED` column
    (`internal/charging/db/migrations/20260829000001_add_inferred_capacity.sql`) —
    recomputed automatically whenever `energy_kwh`, `start_battery_pct` or
    `end_battery_pct` changes, through either `SessionWriter.MirrorSessions`
    (the nightly refresh of `energy_kwh`) or `SessionVerifier.VerifySession`
    (a human correcting the percentages), without either write path naming the
    column. `NULL` when `energy_kwh` is `NULL` (no kWh fee), either percentage is
    `NULL`, or the delta is not strictly positive (design.md D3). The RM29
    "protection by compile error" pattern — `SessionMirror` has no field for the
    battery percentages, so the nightly sync cannot touch them even by mistake —
    is here strengthened to **protection by the database itself**: there is no
    query, port method, or Go code path that can write this column at all.
- sqlc generates the `SuperchargerSession` model and the `MirrorSuperchargerSession`,
  `ListSessionsByVehicleBetween`, and `VerifySuperchargerSession` queries into the same
  `chargingdb` package as `manual_charge_entries`'s queries. Only `session_writer.go`
  may call `MirrorSuperchargerSession`, only `session_reader.go` may call
  `ListSessionsByVehicleBetween`, and only `session_verifier.go` may call
  `VerifySuperchargerSession` (inside this module).

### `monthly_effective_capacity` (RM52 tier 1, MAG-32, RM52-charging-add-monthly-effective-capacity)

- No other module may read or write this table directly (`ai/architecture.md` §2). Access
  goes through the `MonthlyCapacityCalculator` port (write, see §Public Interface above) and
  through `packCapacityKWh`'s internal `packCapacityLookup` seam (read).
- The migration file
  `internal/charging/db/migrations/20260909000002_add_monthly_effective_capacity.sql` is the
  single schema source of truth.
- **No `account_id` column, on purpose.** This table describes a battery pack, not user data.
  One `tesla_id` is one car, whoever registered it. Grouping by `tesla_id` (done in Go, not
  SQL — `monthly_capacity.go`) pools every account's rows into one row automatically.
- **No foreign key on `tesla_id`, on purpose.** A cross-module FK into the `account` module's
  tables would couple this migration to a schema this module does not own
  (`ai/architecture.md` §2). There is also no `raw_data JSONB`: this table stores a computed
  conclusion, not a vendor payload, so there is nothing lossless to preserve.
- **`CHECK (EXTRACT(DAY FROM effective_period) = 1)`** — `effective_period` must always be the
  first day of the month it summarizes. The database rejects any other day; a caller cannot
  drift from this rule by accident.
- **`UNIQUE (tesla_id, effective_period)`** is the whole index plan — there is no separate
  `CREATE INDEX`. Its own btree serves both the read (equality on `tesla_id`, then a
  backwards scan on `effective_period`) and the upsert's conflict target.
- **`effective_capacity_kwh IS NULL` never means "guessed."** It means fewer than
  `minSamples` (a Go constant, currently `3`) valid records survived the delta gate this
  period. A stored number always means "we measured this" — `packCapacityKWh`'s read skips
  `NULL` rows and reads the newest non-`NULL` one instead, falling back to the hardcoded
  `defaultPackCapacityKWh` (`62.0`) only when no measured row exists at all for the vehicle.
- **`candidate_count` and `sample_count` count different things — never read them as one
  number.** `candidate_count` counts every valid record found this period, before the delta
  gate. `sample_count` counts only the ones that survived the gate and fed the median. A row
  always has `candidate_count >= 1`: a vehicle with zero valid records this period gets no
  row at all, never a row with `candidate_count = 0`.
- sqlc generates the `MonthlyEffectiveCapacity` model and the
  `ListValidManualEntryCapacitiesForPeriod`, `ListValidSessionCapacitiesForPeriod`,
  `UpsertMonthlyEffectiveCapacity`, and `LatestMeasuredCapacity` queries into the same
  `chargingdb` package. `monthly_capacity.go` calls the first three; `service.go` and
  `session_verifier.go` each call `LatestMeasuredCapacity` (inside this module).

---

## Related KB

- `architecture/schema-per-module.md` — why each module gets its own Postgres schema
- `architecture/charge-record-mutation.md` — the contract every charge write shares
- `workflows/manual-charge-crud.md` — the manual-entry write flow
- `use-case/charging/verify-session-battery.md` — the session verification write
- `workflows/vehicle-monthly-metrics.md` — the monthly capacity measurement
- `entities/vehicle-metrics/guide.md` — the analytics read model downstream
