# charging — Module Agent Identity

Agent-Name: charging

## Doc-Pack (module)

Extends the project base Doc-Pack (`CLAUDE.md` → "Pipeline config") — never replaces it, and
never restates it: the base list lives in `CLAUDE.md` alone, so a copy here cannot drift.
A dispatched worker reads: base pack + this list + this file, before any write.

*(empty — this module needs nothing beyond the base pack. It is backend-only: no HTML, no
Templ, no htmx. Wiring its ports into `cmd/web` Deps is the `gateway` module's work, not
this one's.)*

## Responsibility

`internal/charging` is the domain module for two related but distinct charging record
types, in two separate tables with two separate vocabularies (see §Data Ownership):

- **User-asserted charge entries** (`manual_charge_entries`) — home/work/third-party
  charging sessions that Tesla's Fleet API cannot attribute to a specific vehicle. Users
  manually log the date, energy added (kWh), cost, and optional metadata (battery
  before/after, timing, charging type, location). The module stores and retrieves these
  entries, enforces multi-tenant data isolation, and computes derived values
  (cost-per-kWh, battery delta, session duration) on read as value-receiver methods on
  `charging.Entry`.
- **Mirrored Supercharger charge sessions** (`supercharger_sessions`, renamed from
  `charge_sessions` in RM39 tier 3, RM29 tier 6,
  RM29-charging-add-charge-sessions) — a dense, one-row-per-session nightly mirror of
  `internal/telemetry`'s `supercharger_sessions`, plus a human-owned battery-percentage
  verification channel that only this module ever writes. The nightly orchestrator
  (`internal/app` since RM29 tier 7; `cmd/poller` before it) reads sessions from
  telemetry's public `SuperchargerReader` port,
  maps each one to a `charging.SessionMirror`, and calls
  `SessionWriter.MirrorSessions` to upsert them here — the mapping itself lives in
  `internal/app`, the application layer, never in this module or in `telemetry`
  (design.md D7; the mapping moved out of `cmd/poller` with RM29 tier 7). The two
  tables are deliberately not merged: roadmap D4 defers that convergence to
  backlog item 12.

Since RM52 tier 1 (MAG-32, RM52-charging-add-monthly-effective-capacity), the module also
measures and stores each vehicle's effective pack capacity once a month, computed from the
two record types above and kept in a third table, `monthly_effective_capacity`.

This module:

- Owns the `manual_charge_entries` table exclusively.
- Owns the `supercharger_sessions` table exclusively (renamed from `charge_sessions`,
  RM39 tier 3, D5b).
- Owns the `monthly_effective_capacity` table exclusively (RM52 tier 1, MAG-32).
- Is isolated from the Tesla Fleet API — it imports no `internal/tesla` package, needs no OAuth
  scope, and wakes no car.
- Exposes CRUD (Writer) and read (Reader) ports for manual entries, and both a
  `SessionWriter` (mirroring) and a `SessionReader` (windowed per-vehicle reads) port for
  Supercharger sessions — all public Go interfaces. `supercharger_sessions` gained its reader in
  RM30 tier 1 (RM30-charging-add-session-read-port), superseding RM29 tier 6's design.md D9
  note that no consumer needed one.
- Computes no HTML, no templates, no htmx fragments — that is the gateway's job (Tier 2).

This module was renamed from `manualcharge` in RM29 tier 2, and gained `charge_sessions`
(renamed to `supercharger_sessions` in RM39 tier 3, D5b) in RM29 tier 6 — a scope this
module did not have when it was named `manualcharge`.

---

## Public Interface

**Ports:** `Writer` (Create / Update / Delete) and `Reader`, each with a `New…(pool *pgxpool.Pool)` constructor. Signatures and the field-level doc comments live in `internal/charging/charging.go` — read them there, they are not copied here.

**`Entry.InferredCapacityKWhCalc *float64`** (MAG-25, charging-add-inferred-capacity)
is reachable through both `Writer` and `Reader` above — it is a field on `Entry`, not
a new method. It is **database-computed and read-only**: a value set on the `Entry`
passed to `Writer.Create` / `Writer.Update` is silently ignored (the underlying
`INSERT`/`UPDATE` never names the column), and the database physically rejects any
direct write to it. `nil` means the record's inputs (energy, both percentages, a
strictly increasing delta) did not support the formula — never an error. See
§Data Ownership below for the guard and §Units convention for the naming rule.

The gateway (Tier 2 `RM3-gateway-add-manual-charge-ui`) wires these interfaces into `cmd/web`
Deps and calls them from handlers. The gateway never imports `chargingdb` directly.

### Entry lifecycle status and energy provenance (MAG-18/RM33, RM33-charging-add-entry-status)

**Types:** `Status` (`StatusInProgress` / `StatusDone`), `EnergySource` (`EnergySourceUser` / `EnergySourceEstimated`), `Field` and `RequiredFieldsFor(s Status) []Field`. See `internal/charging/charging.go`.

`DONE → IN_PROGRESS` has **no transition rule**. The required-field set per status:

| `Status` | `RequiredFieldsFor` set |
|---|---|
| `IN_PROGRESS` | `charged_on`, `location_kind` |
| `DONE` | `charged_on`, `location_kind`, `ended_at`, `end_battery_pct` |

### Auto-promotion to `DONE` (RM51 tier 1, MAG-58, RM51-charging-derive-status-and-price-source)

`Writer.Create` and `Writer.Update` both call `promoteIfComplete(e Entry) Entry`
(`validation.go`) right after `normalizeStatus` and right before `missingFields`. It
promotes an entry submitted as `IN_PROGRESS` to `DONE` when every field
`RequiredFieldsFor(StatusDone)` demands is already present. It reuses
`missingFields`/`RequiredFieldsFor` — no hardcoded field list — so a future change to
the `DONE` set tightens promotion automatically. It never demotes: an entry already
`DONE` is returned unchanged.

This does **not** change `RequiredFieldsFor`'s own rule. It changes only when an entry
gets stored as `DONE` without the caller moving the status control by hand. An explicit
`DONE` submission missing a required field is still rejected exactly as before.

**Known cost:** because promotion also runs on `Update`, and `DONE → IN_PROGRESS` has no
transition rule (still true — see above), "reopening" a `DONE` entry without also
clearing `ended_at` or `end_battery_pct` gets immediately re-promoted back to `DONE`.
To really reopen an entry, the caller must clear at least one of those two fields.

**`Entry.EnergyAddedKWh` is `*float64`, not `float64`** (was `float64` before this
change). `nil` means not supplied and not derivable -- an `IN_PROGRESS` entry
legitimately has no end-of-session facts, so no honest value exists yet. When
`nil` and both battery percentages are present with `EndBatteryPct >
StartBatteryPct`, `Writer.Create`/`Update` derives a value on write from the
(currently hardcoded `62.0`) pack capacity and sets `EnergySource` to
`EnergySourceEstimated` -- never a fabricated `0`. `CostPerKWh()` is nil-safe
over the pointer: `nil` when `EnergyAddedKWh == nil` and (as before) when it
points at `0`.

`Entry` gained three more fields in the same change:

- **`Status Status`** -- see above; an empty `Status` normalizes to
  `StatusInProgress` on `Create`/`Update` (any other unrecognized value is
  rejected in Go, before any DB call).
- **`EnergySource EnergySource`** -- **module-computed and ignored when
  supplied**: a value set on the `Entry` handed to `Writer.Create`/`Update` is
  never read; the module always overwrites it (`EnergySourceEstimated` when it
  derived the energy, `EnergySourceUser` in every other case, including a `nil`
  it could not derive).
- **`OdometerKm *int`** -- the odometer reading, in kilometres, observed **at**
  this charge event (an observation belonging to the event, not current vehicle
  state). `nil` means not recorded.

### Price provenance (RM51 tier 1, MAG-58, RM51-charging-derive-status-and-price-source)

**Type:** `PriceSource` (`PriceSourceUser` / `PriceSourceUnconfirmed`), always computed by this module on Create/Update — a value set on the `Entry` passed to `Writer` is ignored and overwritten, the same shape `EnergySource` uses. See `internal/charging/charging.go`.

`Entry` gained two more fields in the same change:

- **`PriceSource PriceSource`** -- module-computed output. A value set here on the
  `Entry` passed to `Writer` is never read; the module always overwrites it. The rule:
  a positive `Price` is always `USER`; a zero `Price` is `USER` only when
  `PriceConfirmed` is `true`, else `UNCONFIRMED`. A positive price always wins over
  `PriceConfirmed` — the flag is only consulted when `Price == 0`.
- **`PriceConfirmed bool`** -- caller-supplied intent, read **only** when `Price == 0`;
  ignored when `Price > 0`. **Not persisted directly** -- it drives `PriceSource`,
  which is what gets written and read back. A round-trip through `Reader` always
  returns `PriceConfirmed: false` on every entry.

### The Supercharger mirror port (RM29 tier 6)

**Port:** `SessionWriter.MirrorSessions`, taking `SessionMirror` values. See `internal/charging/charging.go`.

**`SessionMirror` has NO field for `start_battery_pct`, `end_battery_pct`, or
`battery_pct_source` — by design, and this is the single most important invariant in
this file.** The three battery-percentage columns are absent from `SessionMirror` and
absent from the `MirrorSuperchargerSession` SQL query entirely (`db/query.sql`), so the
nightly sync path has no field and no column to
bind one to even if a future edit tried — a human's verified reading is protected by a
**compile error**, not by a comment a reviewer has to notice (design.md D6). Do not
"complete" `SessionMirror` by adding these fields; the future verification UI (backlog
item 11) writes them directly, never through this port.

**The refresh set `MirrorSuperchargerSession`'s `ON CONFLICT DO UPDATE SET` touches is
telemetry's own conflict set, minus `raw_data`** (a column `supercharger_sessions` does not
carry): `energy_kwh, total_cost, currency, is_paid, tesla_id`. This is not five
independent judgement calls — it is one rule applied mechanically: *a mirrored column gets
exactly the write semantics its source column has* (design.md D1). Concretely,
`site_location_name` is **not** refreshed on a re-mirror, and the reason is purely
structural: `telemetry`'s own upsert never refreshes `site_location_name` either, so
neither does this one — **not** because a site name was judged unlikely to change. Apply
the same reasoning before adding any future mirrored column: check telemetry's conflict
clause first, and mirror it exactly.

**`updated_at` means "this row's data changed," not "the last mirror pass touched this
row"** (`RM44-charging-add-change-detecting-mirror`, MAG-48; design.md D1–D3). Before
this change, the query set `updated_at = now()` on every mirror pass, whether or not any
of the five refreshed columns above actually changed. That made
`internal/analytics.Recalculator.Reconcile` — which reads this column to find sessions
worth recalculating — see every session as new, every night. The fix: `updated_at` now
advances only when the row's current values differ from the five columns this SET clause
writes. The comparison is a `to_jsonb` deny-list, not a hand-picked `WHERE`: it deny-lists
every column this query never refreshes — bookkeeping (`id`, `created_at`, `updated_at`
itself), human-owned columns (`start_battery_pct`, `end_battery_pct`,
`battery_pct_source`, `status`, `inferred_capacity_kwh_calc`), and write-once mirrored
columns (`vin`, `session_id`, `charge_start_date_time`,
`charge_stop_date_time`, `site_location_name`) — so the comparison covers exactly the
five refreshed columns and nothing else. This keeps working even as the table grows:
`db_mirror_schema_selfcheck_integration_test.go` fails the moment a new column belongs to
neither list. See that test file, and
`internal/charging/db_session_mirror_change_detection_integration_test.go`, for the full
behavioral proof.

The gateway and any other future caller of `SessionWriter` never import `chargingdb`
directly, exactly as for `Writer`/`Reader` above.

### The Supercharger session read ports (RM30-charging-add-session-read-port, widened by RM31-charging-add-session-read-ports)

**Types and ports:** `SessionStatus` (`IN_PROGRESS` / `DONE_CALCULATED` / `DONE`), the `Session` domain type, `SessionReader.ListSessionsByVehicleBetween`, and `SuperchargerSessionAnalyticsReader` (`ListSessionsByVehicleUpdatedSince` / `ListSessionsByVehicle`). See `internal/charging/charging.go`.

`Session` is the read-only counterpart to `SessionMirror` and is **not** built by widening it. `SessionMirror` stays deliberately percentage-free, so the nightly sync path has no field to bind a human-verified percentage to. They are distinct types for exactly that reason, even though most of their fields match.

`SessionStatus` is **always computed** by `SessionVerifier.VerifySession`. No port accepts it as input, so no caller can set it.

The gateway and any other future caller of `SessionReader` never import `chargingdb`
directly, exactly as for `Writer`/`Reader`/`SessionWriter` above. The same applies to
`SuperchargerSessionAnalyticsReader`'s future caller, `internal/analytics`.

### The Supercharger session verification port (RM31-charging-add-session-verification-port)

**Port:** `SessionVerifier.VerifySession`. See `internal/charging/charging.go`.

`SessionVerifier` is this module's third narrow, single-purpose interface over
`supercharger_sessions` (alongside `SessionWriter` and `SessionReader`) — one port per access
pattern (batch write, read, human write), not one port per table, consistent with how
`manual_charge_entries` already splits `Writer`/`Reader` (design.md D9). The gateway and
any other future caller never import `chargingdb` directly, exactly as for
`Writer`/`Reader`/`SessionWriter`/`SessionReader` above. This port ships with no caller
in this tier — `cmd/web`/`internal/gateway` wiring is deferred to
`RM31-gateway-add-session-battery-edit` (tier 4).

### Derived start battery percentage (MAG-36, charging-add-derived-start-battery-pct)

`SessionVerifier.VerifySession` can now derive `start_battery_pct` instead of leaving it
absent. The trigger is exactly four conditions, all required: the caller's
`startBatteryPct` is `nil`, the caller's `endBatteryPct` is non-`nil`, the session row's
`energy_kwh` is non-`NULL`, and the algebraic result — `start = end -
energy_kwh/packCapacityKWh*100`, rounded `math.Round` (half away from zero) — lands in
`[0, 100]` (design.md D2/D4). A caller-supplied `startBatteryPct` is **never** recomputed
or overridden, under any condition — clearing the start field is the caller's way of
asking for it to be calculated. When `energy_kwh` is `SQL NULL` (design.md D5) or the
derived result falls outside `[0, 100]` (design.md D3), `start_battery_pct` is left
`NULL`, silently — no error, no clamp to `0`/`100`. `battery_pct_source` computation is
otherwise unaffected: still `batteryPctSourceUserVerified` when either the (possibly
derived) start or the end percentage is non-nil, still the only value this port ever
writes (design.md D1 — no new source value). The derivation runs inside a transaction
(`pool.Begin`/`WithTx`/`SELECT ... FOR UPDATE` via the new `LockSessionForVerification`
query/`Commit`) only when the trigger fires, mirroring `internal/account`'s
`AccessTokenFor` and this module's own `SessionWriter.MirrorSessions` (design.md D7/D9);
every other call keeps the prior single-statement, non-transactional path unchanged.

### Session lifecycle status (RM41 tier 4, MAG-45)

`SessionVerifier.VerifySession` now also computes a stored `status` column on every
call, alongside `battery_pct_source`, sharing its "never accepted from a caller"
property — no port takes a `Session` as input for this field. One-sentence rule: a
session's status is `IN_PROGRESS` unless BOTH percentage columns end up non-`NULL`
after the write; when both are present, it is `DONE_CALCULATED` if THIS write derived
the start percentage rather than storing a caller-supplied one, and `DONE` otherwise.

| `start_battery_pct` (final, after this write) | `end_battery_pct` (final) | This write derived `start`? | `status` |
|---|---|---|---|
| NULL | NULL | — | `IN_PROGRESS` |
| NULL | non-NULL | derivation not attempted or failed (no `energy_kwh`, or out of `[0,100]`) | `IN_PROGRESS` |
| non-NULL | NULL | — | `IN_PROGRESS` |
| non-NULL (derived this call) | non-NULL | yes | `DONE_CALCULATED` |
| non-NULL (caller-supplied) | non-NULL | no | `DONE` |

A session carrying a typed `start_battery_pct` but no `end_battery_pct` is
`IN_PROGRESS` — the same status as a session with nothing recorded at all,
deliberately not a fourth state (design.md "State truth table"): this is what keeps
"sessions still in progress" a meaningful worklist for the gateway's own follow-up
recommendation (RM41 tier 5) even when a session is half-recorded.

**BACKFILL decision:** every row that existed before this migration
(`20260903000004_add_session_status.sql`) was backfilled to `DONE_CALCULATED`
unconditionally — an owner decision about data provenance (every one of those rows'
percentages was manually reconstructed by the owner, not read from the car) the
stored percentages themselves cannot show, not a recompute of the truth table above
against their actual values (design.md "Rationale").

### The Supercharger mirror watermark (RM44-platform-add-mirror-watermark, MAG-48)

`charging.mirror_watermarks` holds the highest
`telemetry.supercharger_history.updated_at` this module's nightly mirror has
already synchronized, one row per **vehicle** (`tesla_id NOT NULL`, `UNIQUE
(tesla_id)`) — not per account. An account with two cars needs two cursors:
one shared instant cannot say how far each car got, so a per-account cursor
risked skipping one car's rows forever. It has no `source` column: this
table mirrors exactly one upstream table, so a second source column would be
speculative, not something a caller needs today.

**Port:** `MirrorWatermarkStore` (`MirrorWatermark` / `AdvanceMirrorWatermark`). See `internal/charging/charging.go`.

**The most important rule: the watermark never advances to `now()`.** It
advances only to the maximum `updated_at` the caller actually observed on a
run, and only when that run's bounded read returned at least one row. A run
that reads zero rows leaves the watermark untouched. This is deliberate: a
row that commits to `telemetry.supercharger_history` a moment late would
otherwise sit permanently behind an advanced cursor and never get mirrored —
a silent, undetectable loss of data. `AdvanceMirrorWatermark` itself does no
row-count check; the caller (`internal/app.processChargingData`) must call
it only after a non-empty read, exactly mirroring
`analytics.recalculator`'s own `watermark`/`advanceWatermark` split.

No row yet for a vehicle means "epoch" — `MirrorWatermark` returns the zero
`time.Time`, not an error, translating `pgx.ErrNoRows` the same way
`analytics.recalculator.watermark` does. A missing cursor backfills that
vehicle's whole Supercharger history once, on its first-ever mirror run.
The migration that re-keyed this table deleted every existing cursor row, so
every vehicle backfills once on the first nightly run after it applies —
expected, since the mirror is an idempotent upsert and writes nothing new.

Implementation lives in `mirror_watermark.go` (`mirrorWatermarkStore`,
mirroring `session_writer.go`'s exact concrete-type pattern). `pgtype` stays
confined to that one file.

### Monthly effective pack capacity (RM52 tier 1, MAG-32, RM52-charging-add-monthly-effective-capacity)

**Port:** `MonthlyCapacityCalculator.Calculate`, returning `MonthlyCapacityReport`. See `internal/charging/charging.go`.

`Calculate` reads valid records from both `manual_charge_entries` (`energy_source = 'USER'`)
and `supercharger_sessions` (`status = 'DONE'`), groups them by `tesla_id` in Go (this table
has no `account_id` -- see §Data Ownership below), and writes one row per vehicle with at
least one valid record. `ESTIMATED` entries and `DONE_CALCULATED` sessions are excluded on
purpose: both were themselves derived by dividing by the hardcoded `62.0` constant, so
counting them would feed that constant back into itself.

**`packCapacityKWh` (`capacity.go`) now reads this table.** It no longer returns a hardcoded
`62.0` for every vehicle -- this closes backlog #18. Through the `packCapacityLookup` seam, it
returns the vehicle's newest non-`NULL` `effective_capacity_kwh`, and falls back to
`defaultPackCapacityKWh` (`62.0`) only when no measured row exists yet for that vehicle. Its
two callers are unchanged: `resolveEnergy` (`service.go`) and `VerifySession`
(`session_verifier.go`).

The gateway and any other future caller never import `chargingdb` directly, exactly as for
every other port in this module.

`MonthlyCapacityCalculator` now has two callers: the nightly step (`internal/app`) and
the manual `cmd/monthly-capacity` tool. Both call the same port, so neither can drift
from the other's contract.

---

## Allowed Imports

This module may import:

- `context`, `time`, `math`, `errors`, and other Go standard library packages.
- `github.com/google/uuid` — for `uuid.UUID` primary and tenant keys.
- `github.com/jackc/pgx/v5` and `github.com/jackc/pgx/v5/pgxpool` — for DB connectivity.
- `github.com/jackc/pgx/v5/pgtype` — ONLY inside the five files that talk to the database
  directly: `service.go`, `session_writer.go`, `session_reader.go`,
  `mirror_watermark.go`, and `monthly_capacity.go`. Never in public types, interfaces,
  `charging.go`, or any `_test.go` file. The rule is "only the files that own a query",
  not "only these names": each of them translates plain Go `*T` fields into a generated
  params struct's nullable pgtype fields, and translates them back on the way out.
- `internal/charging/db` (package `chargingdb`) — ONLY inside the six files that talk to
  the database directly: `service.go`, `session_writer.go`, `session_reader.go`,
  `session_verifier.go`, `mirror_watermark.go`, and `monthly_capacity.go`. The generated
  package is module-private by convention; no other module imports it, and no `_test.go`
  file does either.

  Both lists above have gone stale before. MAG-36 corrected the `chargingdb` list, which had
  named only `service.go` and `session_writer.go` while `session_reader.go` and
  `session_verifier.go` had imported it since RM31. RM44
  (`RM44-platform-add-mirror-watermark`) corrected both lists again: it added
  `mirror_watermark.go` to each, and added `session_reader.go` to the `pgtype` list, which
  had been missing it. RM52 tier 1 (`RM52-charging-add-monthly-effective-capacity`) added
  `monthly_capacity.go` to both lists in this same change. In every case the access was
  correct and only the doc was wrong. When you add a file that owns a query, add it to
  both lists in the SAME change — a stale list here reads as a boundary rule and gets
  trusted like one.

This module MUST NOT import:

- `internal/tesla` — no Fleet API, no Tesla credentials, no VehicleService.
- `internal/account` — no token resolution, no OwnedVehicle types.
- `internal/telemetry` — no snapshot or Supercharger types. This is unchanged by the
  RM29 tier 6 Supercharger-session mirror: `SessionWriter.MirrorSessions` receives its
  data already mapped to `charging.SessionMirror`, from `internal/app` (the application
  layer; it was `cmd/poller` until RM29 tier 7 moved the mirror step there), never
  fetched here. The one path-only exception is test-scoped: this package's
  `_test.go` files provision a second migration DIRECTORY from `../telemetry/db/migrations`
  (see §Testing Notes) — a filesystem path, not a Go import, and it does not appear in any
  non-test file.
- `internal/gateway` — no HTML, no Templ, no handlers.
- Any other module's `db` sub-package.
- `html/template`, `templ`, or any rendering library.

---

## Coding Rules

**The monthly-capacity estimator keeps its gate and its method as separate functions**
(RM52 tier 1, MAG-32, RM52-charging-add-monthly-effective-capacity design.md D9).
`estimateEffectiveCapacity` (`monthly_capacity.go`) is the **gate**: it decides which samples
count as valid evidence (the `minDeltaPct` filter, the `minSamples` check, `sampleCount`).
`median` is the **method**: it turns the gated samples into one number. A new estimation
method is a **new function** next to `median`, with `median`'s exact signature
(`func(gated []capacitySample) float64`) — never an edit to `median`'s body, and never
inlined into the gate. The gate is **never duplicated**: every method sees the same evidence
and the same `sample_count`. See design.md D9 for the full reasoning and the priced path for
a second method's own stored column.

---

## Units convention

Platform-wide unit rule: `openspec/specs/unit-of-measure/spec.md` / `ai/go-conventions.md`
§Coding Rules — display units, unit-suffixed column names, converted once on write. This table
is **compliant**: `energy_added_kwh`, `start_battery_pct`, `end_battery_pct` already carry their
unit suffix. `price` is the platform's named monetary exemption — it takes no suffix and is
paired with the `currency` column instead of a unit. `inferred_capacity_kwh_calc`
(MAG-25, charging-add-inferred-capacity) is compliant too — see the naming rule below.

### Naming a stored, derived column: `<what>_<unit>_calc`

This project names a **stored, derived** column `<what>_<unit>_calc` — unit suffix
first, `_calc` last. `internal/analytics/vehicle_metrics`
(`internal/analytics/db/migrations/20260821000001_add_vehicle_metrics.sql:67-71`) is
the precedent: it carries five such columns — `distance_traveled_km_calc`,
`battery_used_pct_calc`, `km_per_pct_calc`, `estimated_range_km_calc`,
`days_spanned_calc` — each a value computed rather than observed, persisted rather
than derived on read, each placing `_calc` **after** its unit suffix.
`inferred_capacity_kwh_calc` follows the identical shape.

**`ai/go-conventions.md` documents the unit-suffix half of this pattern and says
nothing about `_calc`** — it is an established *code* convention with no written
home until this paragraph. The next agent naming a derived column should read this
rule rather than re-deriving it from `vehicle_metrics` (or missing it entirely).
This is also why `inferred_capacity_kwh_calc` is not the ticket's literal
`inferred_capacity_calc`: that name is the same convention, missing the mandatory
unit segment (design.md D1, charging-add-inferred-capacity).

## Data Ownership

`internal/charging` is the **sole owner** of four tables, all in the dedicated `charging`
Postgres schema. No other module may read or write any of them directly
(`ai/architecture.md` §2); all access goes through this module's public Go interfaces.

| Table | Holds | Written by |
|---|---|---|
| `manual_charge_entries` | User-asserted home/work/third-party charges | `Writer.Create` / `Update` / `Delete` |
| `supercharger_sessions` | The nightly Supercharger mirror + the human-owned battery percentages | `SessionWriter.MirrorSessions` (mirror), `SessionVerifier.VerifySession` (percentages) |
| `monthly_effective_capacity` | Measured pack capacity per vehicle per month | `MonthlyCapacityCalculator.Calculate` |
| `mirror_watermarks` | One mirror cursor per vehicle | `AdvanceMirrorWatermark` |

Rules that hold for all four:

- **The migration files are the single schema source of truth.** There is no `schema.sql`
  (`ai/go-conventions.md` §persistence).
- **sqlc generates `package chargingdb` from `query.sql`.** Only the files listed in
  §Allowed Imports may import it.
- **No cross-module FK.** Tenant scoping is enforced by each query's own predicate —
  `account_id` for `manual_charge_entries`, `tesla_id` for `supercharger_sessions` and
  `mirror_watermarks` — never by the database.
- **Renames must cover the catalog, not a list.** When `charge_sessions` became
  `supercharger_sessions`, every index, constraint and auto-named CHECK went with it. The
  criterion is that no relation, index or constraint owned by this module may still carry
  the old name. Five of those CHECKs were auto-named by Postgres and appear nowhere in this
  repo, so **grep cannot confirm a rename** — verify against `pg_constraint`.

**Column-by-column detail — every constraint, every generated column, and every index
decision with the reason it was not taken — lives in
`kkpa/context/architecture/charging-tables.md`.** Fetch it before adding or changing a
column. It also records the revisit trigger for each un-indexed column, so you do not
re-argue a settled decision.

---

## Testing Notes

- **Unit tests** run offline, no DB, no Tesla API. **Integration tests** are the `db_*` files
  and need a database.
- **A test that needs an unexported function lives in package `charging`, not
  `charging_test`** — `derivedEnergyKWh`, `packCapacityKWh`, `derivedStartBatteryPct` and
  `needsDerivedStartBatteryPct` are deliberately unexported and stay that way.
- **`pgtype` must not appear in any test helper or assertion.** Assert against
  `charging.Entry` / `charging.SessionMirror` / `charging.Session` domain fields, or against
  raw SQL column values scanned into plain Go `*T` (pgx v5 scans NULL into a pointer
  natively). Never against a `chargingdb` model — those are all `pgtype`.
- **The test database applies TWO migration directories, in this order:**
  `../telemetry/db/migrations` first, then this module's own, via `testdb.ProvisionDirs`
  (not the single-directory `testdb.Provision`). The migration that created
  `supercharger_sessions` ships a backfill reading telemetry's table, so that table must
  exist first. **This is a path dependency on a directory, not a Go import** — no `_test.go`
  file here imports `internal/telemetry`, and §Allowed Imports still forbids it.
- `testdb_test.go` provisions it: `TEST_DATABASE_URL` when set, otherwise a disposable
  `postgres:16-alpine` via `testcontainers-go`, one `*pgxpool.Pool` per package. Migrations
  run through the `github.com/pressly/goose/v3` Go API (`goose.NewProvider` records applied
  versions in `goose_db_version`), so re-running against a managed DB is a no-op. **No
  `createdb` and no `make migrate-up` step is needed** — the suite runs green with zero
  manual DB setup as long as Docker is running. None of it compiles into the deployed
  binary.
- **Backfill of rows that existed before a migration is deliberately NOT integration-tested.**
  The test database is provisioned fresh with every migration applied before any row exists,
  so there is nothing to backfill there. The real check is the owner's post-`migrate-up`
  query, recorded in the change's own tasks.md.
- **No Tesla API call fires in any test.** This is structural — the module does not import
  `internal/tesla` — not disciplinary. The `tesla-exploration` no-tests exception
  (`CLAUDE.md`) does NOT apply here: tests for this module are welcome and required.

Which test file covers what: `ls internal/charging/*_test.go`. The names say it. That
inventory is deliberately not duplicated here — it went stale three times.
