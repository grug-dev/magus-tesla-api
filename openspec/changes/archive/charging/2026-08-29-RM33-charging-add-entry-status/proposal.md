# Proposal — RM33-charging-add-entry-status

Source: MAG-18 — https://linear.app/magus-monitor/issue/MAG-18/adding-status-to-manual-records
Roadmap: `openspec/roadmaps/RM33-manual-record-status.md` — **tier 1 of 3**, module `charging`,
implementing roadmap decisions **D1–D8** verbatim.
Design gate: **TRIPPED.** This change adds three columns to `manual_charge_entries` and drops a
`NOT NULL` on a fourth, via a new goose migration. Per `openspec/config.yaml` §design and
`CLAUDE.md` §Pipeline config → `Design-Gates: database`, design.md carries the full DDL, the
rationale with rejected alternatives, and the index plan, and **the owner must confirm that
design before any implementation is dispatched.**
Unit tests: **included** — confirmed by the user at the roadmap's Step 2 interview, overriding
the standing "no unit tests" default. Both kinds: offline unit tests for `RequiredFieldsFor` and
the derivation arithmetic (no DB), plus `DATABASE_URL`-gated integration tests for the schema and
the write paths. Expected values are fixed in design.md §Test Contract **before** implementation,
per `ai/go-conventions.md` §Testing authoring order.

---

## Why

`manual_charge_entries` today demands a complete session up front. `energy_added_kwh` is
`NOT NULL CHECK (> 0)`, and the gateway additionally requires `end_battery_pct`. A user who plugs
in at 07:40 and wants to log it **cannot** — there is no honest value for energy or for the end
percentage until the charge finishes, and the module rejects the row. So charges get logged from
memory hours later, or not at all.

MAG-18 asks for a record that can exist in two states: **IN PROGRESS**, holding only what is known
at plug-in time, completed to **DONE** later. That makes the required-field set a *function of the
record's status* rather than a fixed list, which is the substance of this tier.

Two things follow from it and are also in this tier:

- **Energy must become optional** (roadmap **D2**) — forced, not chosen. An `IN_PROGRESS` record
  legitimately has no `end_battery_pct`, so nothing can be derived and no honest value exists.
- **Energy should be derivable** (roadmap **D3**) — when the user leaves it blank but both battery
  percentages are present, `capacity × (Δ% / 100)` is a better answer than `NULL`. Deriving it
  **on write** follows the project's standing "conversion happens once, on write, never on read"
  convention, and matches the `Performance-Profile`: precomputation paid by the writer for the
  benefit of reads.

Deriving energy creates a subtle trap that roadmap **D4** closes: MAG-25's
`inferred_capacity_kwh_calc` divides energy by the battery delta, so a row whose energy was itself
computed as `capacity × Δ/100` returns **exactly the capacity constant** by algebra. The user's
stated plan is to later replace the hardcoded 62 kWh with the *average of the inferred capacities*
(backlog #18) — feeding derived rows into that average would seed it with its own output and stop
it converging on the pack's real capacity. A `energy_source` provenance column, recorded now,
lets that future feature filter `WHERE energy_source = 'USER'`. It **cannot be reconstructed after
the fact**: once `31.00` is stored there is no way to tell a typed value from a derived one.

`odometer_km` (roadmap **D6**) rides along because it is an observation made *at the charge event*
and the ticket asks for it on the record.

## What Changes

- **ADDED** — one goose migration
  `internal/charging/db/migrations/20260829000002_add_entry_status.sql`, adding three columns to
  `manual_charge_entries` and relaxing one:
  - `status TEXT NOT NULL DEFAULT 'IN_PROGRESS' CHECK (status IN ('IN_PROGRESS','DONE'))`
  - `energy_source TEXT NOT NULL DEFAULT 'USER' CHECK (energy_source IN ('USER','ESTIMATED'))`
  - `odometer_km INTEGER NULL CHECK (odometer_km >= 0)`
  - `ALTER COLUMN energy_added_kwh DROP NOT NULL` — the existing `CHECK (energy_added_kwh > 0)`
    is **kept**; a `CHECK` passes on `NULL`.

  Because each backfill value equals its column's `DEFAULT`, every `ADD COLUMN` is a single
  statement and existing rows land on their value through the `DEFAULT` — no two-step
  backfill-then-constrain (unlike `20260720000001`, whose backfill value could not be a default).
  Full DDL, rationale and index plan in design.md §"Database Changes".
- **ADDED** — `Entry.Status`, `Entry.EnergySource`, `Entry.OdometerKm` on the domain type, and the
  exported `Status` / `EnergySource` / `Field` types with their constants.
- **ADDED** — `RequiredFieldsFor(status Status) []Field`, the single declarative source of truth
  for the status→required-fields rule (roadmap **D5**). `Writer.Create` / `Writer.Update` enforce
  it; `internal/gateway` imports the same lookup in tier 2 to decide which inputs render as
  required. **No DB `CHECK` backstop** — explicitly offered and rejected at the interview, because
  every future change to the skip set would then need a migration.
- **ADDED** — `packCapacityKWh(ctx, vin) (float64, error)`, module-private, returning a hardcoded
  `62.0` with a `TODO(MAG-18)` naming what it is pending on (roadmap **D8**, backlog #18). The
  `ctx`+`error` signature is deliberate future-proofing so the eventual DB/module-backed lookup is
  a one-body change with zero caller churn.
- **ADDED** — derived-energy-on-write in `Writer.Create` / `Writer.Update` (roadmap **D3**), and
  the `energy_source` provenance it records (roadmap **D4**).
- **CHANGED — BREAKING** — `Entry.EnergyAddedKWh float64` becomes `*float64` (roadmap **D2**), and
  `Entry.CostPerKWh()` becomes `nil`-safe over it.
- **CHANGED** — `internal/charging/db/query.sql`: `CreateEntry` and `UpdateEntry` bind the three
  new columns. **No read query changes** — all four are `SELECT *`, which sqlc expands to include
  the new columns automatically.
- **CHANGED** — `internal/charging/db/models.go`, `db/query.sql.go` (both **generated**;
  `make sqlc` re-run). `sqlc.yaml` needs no change.
- **CHANGED** — `internal/charging/service.go` (validation, derivation, nullable-energy mapping)
  and `internal/charging/charging.go` (domain surface).
- **CHANGED** — `internal/charging/AGENTS.md` (§Public Interface, §Data Ownership, §Testing
  Notes) — docs-track-structural-change, `CLAUDE.md` §Non-negotiables.
- **CHANGED** — `kkpa/context/workflows/manual-charge-crud.md`: a pointer to where the 62 kWh pack
  capacity lives and that it is pending a real source (roadmap **D8**).
- **UNCHANGED** — every index; `manual_charge_entries.inferred_capacity_kwh_calc` and its
  generated expression (roadmap **D4** leaves MAG-25 alone); `price` and `currency` (roadmap
  **D7** is a gateway-only change, no migration); the whole `charge_sessions` table and its four
  ports; every file outside `internal/charging/` except the KB note.

**Out of scope, deliberately:** every gateway change — the status dropdown, the optional
energy/price inputs, the COP suffix, the AC/DC labels, the odometer input, the date→time sync, the
value-preserving 4xx re-render, the Status column, the battery-range column, the date filter and
the aggregation tiles. Those are roadmap tiers 2 and 3 (`RM33-gateway-update-charge-form`,
`RM33-gateway-add-entries-dashboard`). This change does not touch `internal/gateway`.

## Breaking?

**YES — this change is breaking at the module's public Go surface.**

`Entry.EnergyAddedKWh` changes from `float64` to `*float64` (roadmap **D2**). Every reader of that
field fails to compile until it is updated. The field is part of `charging.Entry`, which
`internal/gateway` receives from `Reader` and supplies to `Writer`, so the break crosses the
module boundary.

Nothing else breaks: no interface gains, loses or re-signs a method; `Reader`, `Writer`,
`SessionReader`, `SessionWriter`, `SuperchargerSessionAnalyticsReader` and `SessionVerifier` are
untouched; `Session` and `SessionMirror` are untouched; the three added `Entry` fields are additive.

### ⚠️ The break leaves `internal/gateway` uncompilable, and this tier cannot fix it

This is the one thing the leader must decide before dispatching implementation. A module worker
sandboxed to `internal/charging` **may not** edit `internal/gateway`, so at the end of this tier
`go build ./...` and `go vet ./...` **fail repo-wide** unless the leader dispatches the compile fix
as well. Exact break sites, all `*float64` dereferences of `e.EnergyAddedKWh`:

| File | Lines | What it does |
|---|---|---|
| `internal/gateway/handlers/charges.go` | 612, 623 | formats the value into `ChargeEntryVM` (`EnergyKWh`, `RawEnergyKWh`) |
| `internal/gateway/handlers/charges.go` | 798 | builds `charging.Entry` in `parseChargeForm` |
| `internal/gateway/handlers/charges_test.go` | ~10 sites (385, 606–607, 654, 918–919, 952–953, 1008, 1083, 1138–1140, 1178, 1213, 1532, 1586) | fixtures + assertions |
| `internal/gateway/handlers/charges_error_visibility_test.go` | 169 | fixture |

Recommended: the leader dispatches a **minimal mechanical `gateway` compile-fix task in the same
wave** — nil-guard the two formatters (render the project's `—` placeholder when nil, per roadmap
**D14**), take the address of the parsed value at 798, and update the fixtures. It is a
`*float64` adaptation, nothing more; the real gateway behaviour lands in tiers 2 and 3. The
alternative — leaving the build red between tiers — would make this tier's reviewer gate
unenforceable, since `go build ./...` is one of the signals the reviewer runs.

**In-module break sites** (this worker's own responsibility, enumerated in tasks.md): `charging.go`
(field decl + `CostPerKWh`), `service.go:122,170,383-386,400`, `charging_test.go:21,40`,
`db_integration_test.go:69,123,238,256`,
`db_inferred_capacity_entries_integration_test.go:128,170,203`.

## Modules affected

- **`charging`** — owner. Schema, domain type, ports' behaviour, validation, derivation, tests,
  `AGENTS.md`.
- **`gateway`** — **compile-affected only** by the `*float64` change (see above). No behavioural
  gateway change belongs to this tier; tiers 2 and 3 own that.
- **`analytics`** — consumes `charging.Reader.ListEntriesByVehicleUpdatedSince`. It reads
  `Entry` but, per `grep`, does not read `EnergyAddedKWh`; the leader should confirm with
  `go build ./...` after the change rather than trusting this sentence.
- No other module. `manual_charge_entries` is owned exclusively by `internal/charging`
  (`ai/architecture.md` §2); no cross-module FK, no cross-module read.

## Read paths affected

Per `openspec/config.yaml` §proposal. Every read on `manual_charge_entries` is `SELECT *`, so all
four carry **three additional columns per row** after this change, and nothing else:

| Port method | Query | Effect |
|---|---|---|
| `Reader.ListEntriesByVehicle` | `ListEntriesByVehicle` | +3 columns per row |
| `Reader.ListEntriesByAccount` | `ListEntriesByAccount` | +3 columns per row |
| `Reader.ListEntriesByVehicleBetween` | `ListEntriesByVehicleBetween` | +3 columns per row |
| `Reader.ListEntriesByVehicleUpdatedSince` | `ListEntriesByVehicleUpdatedSince` | +3 columns per row |
| `Writer.Create` / `Writer.Update` | `RETURNING *` | +3 columns returned |

**No query plan changes.** No `WHERE`, `ORDER BY` or `LIMIT` clause is touched and no predicate
names a new column. `idx_manual_charge_entries_vehicle_time` and
`idx_manual_charge_entries_account_time` are untouched and still serve exactly the scans they
served before. Neither has an index-only scan to lose — every read is `SELECT *` already.

**No new index is added**, deliberately and with the reason stated rather than left implicit
(design.md §"Index Plan", **D9**): nothing filters, sorts, joins or groups by `status`,
`energy_source` or `odometer_km`. Tier 3's dashboard filter bounds on `charged_on` through the
**existing** `ListEntriesByVehicleBetween` and sums its tiles in Go over that same result set
(roadmap **D13**), so even the status column it renders is a *projection*, never a predicate.
This is the identical reasoning `20260829000001` already applied to `inferred_capacity_kwh_calc`.

**Write paths:** three `ADD COLUMN`s. `status` and `energy_source` carry a non-volatile `DEFAULT`,
so on PostgreSQL 11+ they are **metadata-only** adds (no table rewrite); `odometer_km` is
nullable with no default, likewise metadata-only. `DROP NOT NULL` is a catalogue update. All four
take `ACCESS EXCLUSIVE` briefly. This migration is materially cheaper than `20260829000001`, which
did rewrite both tables.

## Impact

- **Affected spec:** `manual-charge-log` — two **ADDED** requirements (record status with a
  status-conditional required-field set; energy provenance and odometer) and two **MODIFIED**
  requirements ("Create a manual charge entry" and "Derived read-time values", both of which
  currently state that energy added is mandatory). No requirement is removed. `charge-session-log`
  is **untouched** — `charge_sessions` gains nothing here.
- **Affected code:** `internal/charging/` only —
  `db/migrations/20260829000002_add_entry_status.sql` (new), `db/query.sql`,
  `db/models.go` + `db/query.sql.go` (regenerated), `charging.go`, `service.go`, `charging_test.go`,
  `db_integration_test.go`, `db_inferred_capacity_entries_integration_test.go`, two new `_test.go`
  files, `AGENTS.md` — plus `kkpa/context/workflows/manual-charge-crud.md` (KB note, roadmap D8)
  and the `internal/gateway` compile fix described above, which is **not** this worker's to make.
- **Design gate: tripped.** The owner confirms design.md before implementation. Three items want
  an explicit yes beyond the DDL itself:
  1. **The DONE required-field set** — design.md **D5** reads roadmap D5's "initial `IN_PROGRESS`
     skip set: `ended_at`, `end_battery_pct`" as making those two *required for DONE*. Today
     `ended_at` is optional everywhere (DB and gateway). This is the only reading consistent with
     the phrase "skip set", but it is a real tightening and tier 2's form must follow it.
  2. **What an empty `Status` means** (design.md **D8**) — not covered by any roadmap decision, and
     forced: until tier 2 ships, the gateway sends no status at all. Specified as *normalize `""`
     to `IN_PROGRESS`, reject any other non-enum value*, which is what keeps this tier shippable
     on its own.
  3. **Who fixes the `internal/gateway` compile break**, per the box above.
- **Deferred, explicitly NOT in scope:** every tier-2/tier-3 gateway change; any per-vehicle
  capacity aggregate (backlog #18); any change to `inferred_capacity_kwh_calc`'s expression
  (roadmap **D4** rejected it as a table-rewriting migration altering MAG-25 out of scope); any
  index on the new columns; any `price`/`currency` migration (roadmap **D7** is gateway-only); any
  status transition rule (nothing forbids DONE → IN_PROGRESS, deliberately — see design.md **D5**).
