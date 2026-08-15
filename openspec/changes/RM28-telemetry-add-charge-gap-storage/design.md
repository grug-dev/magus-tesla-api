## Context

`internal/telemetry` already owns two other write-heavy tables (`vehicle_snapshots`,
`poll_attempts`, both nightly-poller-written) and one upserted ledger
(`supercharger_sessions`). This change adds a fourth table, `charge_gaps`, and one new read
method on the existing `SuperchargerReader` port. Both pieces exist to support
`internal/battery`'s per-day battery-consumed derivation (tier 3 of this roadmap,
`RM28-battery-derive-consumed-per-day`) without giving `internal/battery` a database package
of its own (D3 — the owner's call, against the assistant's recommendation that `battery` own
the table).

The roadmap (`openspec/roadmaps/RM28-battery-consumed-graph.md`) settled sixteen binding
decisions (D1–D16, D16 superseding D1/D9/D9a) with the owner via grill-me before this design
was written. This design implements D3 (ownership), D7/D7a/D7b (the table shape, the
inferred type, the upsert-and-delete lifecycle), and D9/D12 (the new Supercharger read
method, filtered on stop time). D1, D2, D4–D6, D8, D10, D11, D13–D15 bound what this tier
must **not** build — see proposal.md's "Out of scope."

Primary and only module: **`internal/telemetry/`**. `internal/battery` is not created, read,
or touched by this change (it already exists in this repo for an unrelated efficiency
metric — `internal/battery/battery.go` — and nothing in that file is modified or referenced
here).

## Goals / Non-Goals

**Goals:**

- A new table, `charge_gaps`, recording one row per vehicle-day whose battery math does not
  add up (D7): `UNIQUE (account_id, tesla_id, gap_date)`, columns `vin`, `tesla_id`, `gap_date`,
  `missing_charging_type` all `NOT NULL`.
- A write port, `telemetry.GapWriter`, with one method (`ReconcileWindow`) that upserts every
  still-flagged day and deletes every previously-stored day in the window that no longer
  flags, in one call per vehicle per nightly run (D7b).
- Domain types `ChargeGap` and `MissingChargingType` (`MANUAL` | `SUPERCHARGER`) shaped so
  the same `ChargeGap` type can later serve, unmodified, as a future read port's return type
  (not built in this tier).
- A third `SuperchargerReader` method, `SuperchargerSessionsByVehicleBetween`, filtered on
  `charge_stop_date_time` (D12), additive alongside the two existing limit-based methods.
- An index plan that serves both of the two known read patterns this table needs to serve,
  now and in the described future: the nightly reconciliation (one vehicle, one window), and
  a future account-wide "outstanding gaps" notification query (one account, no vehicle
  filter).

**Non-Goals:**

- No gap-detection logic, no consumption derivation, no `MANUAL`/`SUPERCHARGER` inference
  algorithm (D5, D5a, D7a's rule) — this tier stores whatever `internal/battery` (tier 3)
  computes and hands it via `ChargeGap`; it computes nothing itself.
- No `resolved_at`, no soft delete, no audit trail of past (now-resolved) gaps — see D-Table1
  below for the rejected alternative and why.
- No read port for `charge_gaps` — the future notification consumer is out of scope for this
  roadmap tier.
- No `cmd/poller` wiring, no call site for `ReconcileWindow` anywhere in this repository
  after this change — that is tier 3.
- No change to `vehicle_snapshots`, `poll_attempts`, or the two existing `SuperchargerReader`
  methods' SQL text.

---

## Design Decisions

### D-Table1 (D7) — One row per vehicle-day, not one row per (day, missing_charging_type)

```sql
UNIQUE (account_id, tesla_id, gap_date)
```

`internal/battery`'s corrected figure for a day is a **single aggregate number**
(`battery_used_pct_calc + Σ(end_battery_pct − start_battery_pct)` across both charge
sources, roadmap D13). When that single number does not add up, the platform has observed
**one** shortfall for that day — not "a MANUAL shortfall and a SUPERCHARGER shortfall
independently." A per-`(day, type)` key would let one date carry both a `MANUAL` row and a
`SUPERCHARGER` row simultaneously, but the two sources cannot actually be distinguished from
a single combined number: which portion of the shortfall is attributable to a missing
Supercharger record and which to a missing manual entry is not observable from the inputs.
A second row keyed by type would therefore be a **fabricated finding**, not an observed one
— it would assert a claim ("both sources are implicated") the math does not support.

`missing_charging_type` is a column describing **which** source the one row's gap is
attributed to (D7a's inference rule — computed by `internal/battery`, not chosen by this
module), never part of the row's identity.

**Rejected alternative:** `UNIQUE (account_id, tesla_id, gap_date, missing_charging_type)`.
Rejected per the reasoning above — it does not fit an observation this module never makes
(no code path anywhere in this change or its caller can honestly emit two rows for the same
day), and it would double every index's row count and every read/write's row-count for a
distinction that carries no real information.

### D-Table2 (D7b) — Delete-on-resolve, not `resolved_at` / soft delete

`GapWriter.ReconcileWindow` **deletes** a row outright the first nightly run after its day
stops flagging; there is no `resolved_at` timestamp and no "soft delete" flag.

**Why:** the one described future consumer of this table is a notification asking "what is
outstanding **right now**" (proposal.md, roadmap "Problem" section: "so a later ticket can
notify the user"). A live worklist is the correct shape for that: the moment a user fixes
the underlying charge entry, the next nightly run's `internal/battery` derivation no longer
flags that day, and `ReconcileWindow` — called with that day simply **absent** from the
newly-computed `flagged` set — removes the row with zero extra code on either side. Keeping
a `resolved_at` row around would require every future reader to remember to filter
`WHERE resolved_at IS NULL`, and this table would otherwise accumulate rows forever (every
day that was ever flagged even transiently, for the lifetime of every vehicle) for no
consumer this roadmap describes.

**Rejected alternative:** add `resolved_at TIMESTAMPTZ` (nullable, set to `now()` instead of
deleting when a day stops flagging). Rejected as unrequested scope: it turns this table from
a worklist into an audit-trail/history table, a genuinely different feature (a "gap history"
view) that no requirement in the roadmap or the source ticket asks for. It is not free
either — every row this table will ever hold survives forever, and every future query must
add the `resolved_at IS NULL` filter to reconstruct the "outstanding now" answer this
design's delete-on-resolve shape gives for free. If a future ticket wants gap **history**
(not just current state), the honest fix is a dedicated event-log table alongside this one —
not overloading `charge_gaps`' single UNIQUE-constrained row per vehicle-day with two
different lifecycles. Logged here as the trigger to revisit if that need materializes.

### D-Table3 (D7) — `tesla_id NOT NULL` here, nullable on `supercharger_sessions`

```sql
tesla_id  BIGINT NOT NULL
```

`supercharger_sessions.tesla_id` is nullable because a session can arrive for a VIN that is
no longer (or not yet) a registered vehicle on the account — the session is still stored,
with `tesla_id = NULL`, because it is a raw fact fetched from the Tesla API (D-Table language
mirrors `internal/telemetry/db/migrations/20260716000001_add_supercharger_sessions.sql:24`).
`charge_gaps` has no such case: `internal/battery`'s derivation only ever runs over a
vehicle it already resolved via `account.AllRegisteredVehicles` (mirroring the same
resolution `cmd/poller` already performs for every other telemetry write). A Supercharger
session whose VIN cannot be attributed to a currently-registered vehicle is invisible to the
derivation entirely — it is filtered out before gap detection ever runs, so it can never
produce (or need) a `charge_gaps` row with an unknown vehicle. Every row this table will
ever hold therefore already has a resolved `tesla_id` by construction, and `NOT NULL`
documents that invariant at the schema level rather than leaving it as an unstated property
of the caller.

**Rejected alternative:** mirror `supercharger_sessions.tesla_id`'s nullability for
consistency's sake. Rejected — copying a nullability that has no corresponding case in this
table's own domain would only weaken the schema's guarantees (every future reader would have
to nil-check a value that can never actually be nil) for no compensating benefit; matching a
sibling table's *shape* is valuable when the underlying invariant is the same (D-Table1 above
does exactly that for `_pct`/`SMALLINT CHECK` shapes elsewhere in this module), not when it
isn't.

### D-Table4 — No cross-module FK on `account_id` or `tesla_id`

Mirrors the identical, already-established precedent in this same module
(`supercharger_sessions`, `20260716000001_add_supercharger_sessions.sql:10-14`) and in
`internal/manualcharge` (`manual_charge_entries`,
`20260718000001_add_manual_charge_entries.sql:10-14`): a cross-module FK from `charge_gaps`
into the `account` module's `accounts`/`vehicles` tables would couple `internal/telemetry`'s
migrations to `internal/account`'s schema — exactly the coupling `ai/architecture.md` §2
forbids ("no cross-module database leaks... cross-module data flows only through public
interfaces"). Referential integrity is upheld by flow, not by a database constraint: the
only writer (`internal/battery`, called from `cmd/poller`, D4/D4a) resolves `account_id` and
`tesla_id` from `account.AllRegisteredVehicles` before ever calling `ReconcileWindow`
(exactly the same flow `supercharger_sessions`' own header comment describes for its writer).

### D-Table5 — No `raw_data JSONB`

`charge_gaps` stores a **Go-computed conclusion** — `internal/battery`'s derivation result —
never an external API response. The `raw_data JSONB` rule
(`ai/go-conventions.md` §Persistence: "Always store raw JSONB when ingesting external API
responses") applies only to tables that ingest an external API payload; it does not apply
here for the identical reason `manual_charge_entries` omits it for its own user-typed,
non-vendor data (`20260718000001_add_manual_charge_entries.sql:16-19`, that migration's
design D5). There is no vendor payload to preserve, lossily or otherwise.

### D-Table6 (D7) — `missing_charging_type`: TEXT + CHECK, not a Postgres ENUM

```sql
missing_charging_type  TEXT NOT NULL CHECK (missing_charging_type IN ('MANUAL', 'SUPERCHARGER'))
```

Mirrors this project's own established precedent for exactly this kind of small closed
vocabulary — `manual_charge_entries.charging_type CHECK (IN ('AC','DC'))`,
`.location_kind CHECK (IN ('HOME','WORK','OTHER'))`, and
`supercharger_sessions.battery_pct_source CHECK (IN ('user_verified', 'polled'))` (the most
recent instance of this exact pattern in this same module, `20260815000001`, design D2
there). A Postgres `ENUM` type would need `ALTER TYPE ... ADD VALUE` (which cannot run
inside the same transaction as other DDL, and cannot be removed) to extend later; a `CHECK`
constraint extends by a plain `ALTER TABLE ... DROP CONSTRAINT ... ADD CONSTRAINT` in a
normal future migration. `NOT NULL` (not nullable, unlike the two `CHECK`-constrained
columns just cited): every row this table holds represents an actual detected gap with a
known inferred type (D7a runs unconditionally before a row is ever written) — there is no
"gap exists but its type is unknown" state to represent.

**Rejected alternative:** a third value, e.g. `'BOTH'` or `'UNKNOWN'`. Rejected — D7a's
inference rule is a deterministic two-way branch (a same-day Supercharger session with NULL
percentages exists, or it does not); there is no third case the roadmap or this module needs
to represent, and adding one would invite a future caller to invent a meaning for it that
D-Table1's own reasoning already rules out (this table never represents "both sources
implicated" as a fact, only as an inference about which single source to attribute the one
observed shortfall to).

---

## Schema

### DDL (goose migration)

**Filename:** `internal/telemetry/db/migrations/20260815000002_add_charge_gaps.sql`
(second migration on 2026-08-15, after `20260815000001_add_supercharger_battery_pct.sql`).

```sql
-- +goose Up
-- charge_gaps: nightly-detected vehicle-days whose battery math does not add
-- up -- a charge record is missing or incomplete (D3/D7/D7a/D7b of
-- RM28-telemetry-add-charge-gap-storage, MAG-15,
-- openspec/roadmaps/RM28-battery-consumed-graph.md). Owned by
-- internal/telemetry; written through the GapWriter port by internal/battery
-- (D3 -- battery derives the day's consumption and detects the gap, telemetry
-- only stores the conclusion; telemetry never calls battery, so the one-way
-- battery -> telemetry dependency this module's design already assumes is
-- preserved, not inverted). Read by a future notification feature -- out of
-- scope in this change; no read port for this table exists yet.
--
-- ONE ROW PER VEHICLE-DAY, not one row per (day, missing_charging_type): a
-- day's corrected battery-consumed figure is a single aggregate number
-- (battery_used_pct_calc + sum of that day's charge deltas across BOTH
-- sources, roadmap D13) -- when it does not add up, that is one observed
-- shortfall, not two independently-observed ones. A per-type key would let
-- one date carry both a MANUAL and a SUPERCHARGER row, but the two sources
-- cannot actually be told apart from a single combined shortfall; a second
-- row would be a fabricated finding, not an observed one.
-- missing_charging_type therefore describes WHICH source the one row's gap
-- is attributed to (D7a), not part of the row's identity.
--
-- NO resolved_at / soft delete: the write port (GapWriter.ReconcileWindow,
-- D7b) UPSERTs every day that still flags and DELETES every previously-
-- stored day, within the window it just recomputed, that no longer flags.
-- Fixing a charge entry clears the row on the very next nightly run with no
-- extra wiring. This table is a live worklist ("what is outstanding right
-- now"), not an audit trail of resolved gaps -- see design.md's D-Table2 for
-- the full rationale and the rejected resolved_at alternative.
--
-- tesla_id is NOT NULL here even though it is NULLABLE on
-- supercharger_sessions.tesla_id: a session/vehicle that cannot be
-- attributed to a currently-registered vehicle is filtered out of
-- internal/battery's derivation before gap detection ever runs, so it can
-- never produce a charge_gaps row with an unresolved vehicle -- every row
-- this table will ever hold already has a resolved tesla_id by construction
-- (design.md D-Table3).
--
-- No FK on account_id or tesla_id: a cross-module FK from charge_gaps into
-- the account module's accounts/vehicles tables would couple telemetry
-- migrations to the account schema -- exactly the coupling
-- ai/architecture.md §2 forbids. Referential integrity is upheld by flow,
-- mirroring manual_charge_entries'/supercharger_sessions' identical
-- precedent (internal/manualcharge/db/migrations/
-- 20260718000001_add_manual_charge_entries.sql:10-14,
-- internal/telemetry/db/migrations/
-- 20260716000001_add_supercharger_sessions.sql:10-14): the only writer
-- (internal/battery, via cmd/poller, D4/D4a) resolves account_id/tesla_id
-- from account.AllRegisteredVehicles before ever calling
-- GapWriter.ReconcileWindow.
--
-- No raw_data JSONB: this table stores a Go-computed conclusion
-- (internal/battery's derivation), not an external API response. The
-- raw_data JSONB rule (ai/go-conventions.md §persistence) applies only to
-- tables that ingest an external API payload -- mirrors
-- manual_charge_entries' identical "no raw_data" precedent for its own
-- user-typed, non-vendor data (design D5 of that migration).
CREATE TABLE charge_gaps (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id             UUID NOT NULL,             -- multi-tenant scope
    tesla_id               BIGINT NOT NULL,            -- which of the account's vehicles; never NULL (see header)
    vin                    TEXT NOT NULL,              -- durable vehicle key, recorded at detection time
    gap_date               DATE NOT NULL,              -- the flagged calendar day (plain DATE -- no time-of-day component)
    missing_charging_type  TEXT NOT NULL CHECK (missing_charging_type IN ('MANUAL', 'SUPERCHARGER')),

    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),  -- when this vehicle-day was FIRST flagged
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),  -- refreshed every nightly run this day is RE-flagged

    CONSTRAINT charge_gaps_account_tesla_date_unique UNIQUE (account_id, tesla_id, gap_date)
);

COMMENT ON TABLE charge_gaps IS
    'Nightly-detected vehicle-days whose battery math does not add up -- a charge '
    'record is missing or incomplete (RM28-telemetry-add-charge-gap-storage, MAG-15). '
    'One row per (account_id, tesla_id, gap_date): a day''s shortfall is a single '
    'aggregate observation, never split across two rows. Written by '
    'internal/battery through the GapWriter port (telemetry never calls battery). '
    'No resolved_at / soft delete: a day that stops flagging is DELETED by the next '
    'nightly reconciliation, not marked resolved -- this table is a live worklist, '
    'not an audit trail. Owned by internal/telemetry; no other module reads this '
    'table directly.';

COMMENT ON COLUMN charge_gaps.tesla_id IS
    'Always resolved and NOT NULL: a vehicle that cannot be attributed to a '
    'currently-registered vehicle is filtered out of internal/battery''s derivation '
    'before gap detection runs, unlike supercharger_sessions.tesla_id which is '
    'nullable for exactly that unattributed case.';
COMMENT ON COLUMN charge_gaps.missing_charging_type IS
    'Which charge source is suspected missing for this day (D7a): SUPERCHARGER when '
    'a Supercharger session exists that day with NULL start/end battery percentages '
    '(the exact record that needs filling is already known); MANUAL otherwise (the '
    'vehicle was charged somewhere the Tesla Fleet API does not report). Inferred by '
    'internal/battery at detection time, never user-chosen.';
COMMENT ON COLUMN charge_gaps.created_at IS
    'When this (account_id, tesla_id, gap_date) was FIRST flagged. Preserved across every '
    'subsequent nightly re-upsert of the same still-flagged day -- NOT refreshed on '
    'conflict -- so it answers "how long has this been outstanding" for a future '
    'notification consumer.';
COMMENT ON COLUMN charge_gaps.updated_at IS
    'When this row was last confirmed still-flagging by a nightly run. Refreshed to '
    'now() on every UPSERT conflict; a day that stops flagging is deleted outright '
    'rather than leaving a stale updated_at behind.';

-- Read path 1 (nightly reconciliation, GapWriter.ReconcileWindow): the write
-- port needs "every existing charge_gaps row for this vehicle whose date
-- falls in [start, end]" to compute what to delete, and every UPSERT
-- conflicts on exactly this constraint. This is the SAME index Postgres
-- builds automatically to enforce charge_gaps_account_tesla_date_unique --
-- (account_id, tesla_id, gap_date) -- so it costs nothing beyond what the
-- constraint already requires; no separate CREATE INDEX for this path. See
-- design.md's Index Plan for the full read-pattern justification.
--
-- Read path 2 (future notification: "outstanding gaps for one account", no
-- tesla_id predicate) is served by this dedicated index instead: account_id
-- leads (multi-tenant convention: every dashboard read in this project scopes
-- by account first -- ai/go-conventions.md §persistence, ai/architecture.md
-- §7.3), and gap_date DESC anticipates a newest-first ordering, matching every
-- other time-ordered index in this module (idx_supercharger_sessions_account_time,
-- idx_manual_charge_entries_account_time). See design.md's Index Plan for why
-- the UNIQUE constraint's own index does not serve this second pattern as
-- efficiently.
CREATE INDEX idx_charge_gaps_account
    ON charge_gaps (account_id, gap_date DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_charge_gaps_account;
DROP TABLE IF EXISTS charge_gaps;
```

### Index plan

Two known read patterns are declared for this table (task brief, and this design's own
Goals): **(1)** the nightly reconciliation — the flagged set for **one vehicle** over a
**date window**; **(2)** the future notification — outstanding gaps for **one account**
(across all its vehicles, no `tesla_id` predicate).

**Read path 1 — nightly reconciliation — is served by the UNIQUE constraint's own implicit
index, `charge_gaps_account_tesla_date_unique (account_id, tesla_id, gap_date)`. No separate
index is created for it.** `ReconcileWindow`'s internal existing-rows lookup is
`WHERE account_id = $1 AND tesla_id = $2 AND gap_date BETWEEN $3 AND $4` — a three-column
equality-equality-range predicate that is exactly what a `(account_id, tesla_id, gap_date)`
B-tree serves as a single contiguous range scan: the planner seeks to
`(account_id, tesla_id, start)` and reads forward to `(account_id, tesla_id, end)`, with no
sort step (the scan is already in `gap_date` order, though `ReconcileWindow` does not need any
particular order — see the Write Path section). Every `UpsertChargeGap`/`DeleteChargeGap`
call inside the same transaction is a point lookup on `(account_id, tesla_id, gap_date)`,
likewise served directly by this index — it is also the conflict target the `UPSERT`'s
`ON CONFLICT` clause matches against, so Postgres reuses the identical index for the
conflict check. Building a second, separate index with the same three leading columns would
be a byte-for-byte duplicate of an index Postgres already maintains for free as a side
effect of the `UNIQUE` constraint — pure write-side cost (one more B-tree to update on every
insert/delete) for zero read benefit.

**Read path 2 — future account-wide notification — is served by the new
`idx_charge_gaps_account (account_id, gap_date DESC)`. The UNIQUE constraint's own index does
NOT suffice for this pattern**, for a precise reason: a `(account_id, tesla_id, gap_date)`
B-tree orders rows by `tesla_id` before `gap_date`, so a query with only an `account_id`
predicate and no `tesla_id` predicate — `WHERE account_id = $1 ORDER BY gap_date DESC` — can use
the index's `account_id` prefix to prune to the tenant's rows, but those rows come back
grouped by `tesla_id` first, each vehicle's rows internally sorted by `gap_date` — **not** a
single globally-`gap_date`-sorted stream across all of the account's vehicles. Postgres would
therefore need an explicit sort step (or a multi-way merge across each vehicle's gap_date-sorted
sub-range) to satisfy `ORDER BY gap_date DESC` account-wide. A dedicated
`(account_id, gap_date DESC)` index removes that sort entirely: `account_id` still prunes to the
tenant, and every remaining row is already in the exact date order the notification wants,
across every vehicle at once. This is the same two-index shape (`(account_id, tesla_id, X)`
for the per-vehicle path, `(account_id, X)` for the account-wide path) both
`supercharger_sessions` and `manual_charge_entries` already use for their own identical
per-vehicle-vs-account-wide read pattern pair — mirroring an established precedent rather
than inventing a new judgment call (`ai/go-conventions.md`'s AI-efficiency "closed, small
vocabulary" principle).

**Why build this index now, when its consumer (the notification read port) is not built in
this tier — isn't that indexing for a consumer that doesn't exist yet, which
`ai/architecture.md` §3 and this project's own AI-efficiency "don't over-abstract" guidance
both counsel against?** The distinction that matters here: RM27's rejected
`battery_pct_source` index (the precedent that guidance was drawn from) had no *named* read
pattern anywhere in that change's roadmap — it was purely hypothetical ("a future
'worklist' query"). Here, the task brief that scoped this exact change **names** "the future
notification, which needs outstanding gaps for one account" as one of exactly two read
patterns this table must be designed against, in the same breath as the nightly
reconciliation pattern this design does build for. Declining to build the account-wide index
would leave this design serving only one of its two stated read patterns. The cost side is
also negligible either way: `charge_gaps` is written only once per vehicle per night (D4),
at low row counts (bounded by how many vehicle-days actually flag, which the roadmap expects
to be rare — a healthy charging habit produces zero rows), so a second index's
write-maintenance cost is immaterial under this project's read-heavy Performance-Profile,
which explicitly licenses "denormalizing, indexing aggressively... for reads... writes are
mostly done by pollers at midnight."

**No index on `missing_charging_type`.** Neither read pattern filters or sorts on it; it
rides along on the row fetch both existing indexes already cover, exactly like every prior
non-indexed column addition in this module (TPMS pressures, the `_calc` columns,
`supercharger_sessions`' battery-% trio).

---

## Go-Level Surface

### Domain types (`internal/telemetry/telemetry.go`)

```go
// MissingChargingType identifies which charge source a flagged charge_gaps
// day is attributed to (design D-Table6, roadmap D7a). Inferred by
// internal/battery at detection time -- never user-chosen, never a value this
// module computes itself.
type MissingChargingType string

const (
	// MissingChargingTypeManual -- no Supercharger session with NULL start/end
	// battery percentages exists for the day; the vehicle was charged
	// somewhere the Tesla Fleet API does not report (home/work/third-party AC,
	// or a DC session GET /api/1/dx/charging/history never returned).
	MissingChargingTypeManual MissingChargingType = "MANUAL"
	// MissingChargingTypeSupercharger -- a Supercharger session exists for the
	// day whose start_battery_pct/end_battery_pct are both NULL: the exact
	// record that needs filling is already known (roadmap D7a/D14).
	MissingChargingTypeSupercharger MissingChargingType = "SUPERCHARGER"
)

// ChargeGap is one flagged vehicle-day whose battery math does not add up --
// internal/battery's derivation could not fully account for the day's
// battery change from stored charge records, meaning a charge record is
// missing or incomplete (D3/D7/D7a of RM28-telemetry-add-charge-gap-storage).
// Our own domain model, no vendor suffix (ai/architecture.md §6):
// internal/battery computes it, internal/telemetry stores it through the
// GapWriter port, and internal/telemetry never computes one itself.
// AccountID/TeslaID are carried on the type -- even though every element of
// one ReconcileWindow call's flagged slice belongs to that call's own vehicle
// -- so this exact shape can also serve, unmodified, as the return type of a
// future read port for the notification feature (not built in this change).
type ChargeGap struct {
	AccountID uuid.UUID
	TeslaID   int64
	VIN       string
	// Date is the flagged calendar day -- a plain calendar DATE (UTC
	// midnight), never a timestamp; backed by the charge_gaps.gap_date column.
	// Must be normalized to UTC midnight the same way dateOnly/CapturedDate
	// already are elsewhere in this module -- ReconcileWindow compares Date
	// values for map-key equality against the stored gap_date column.
	//
	// DELIBERATE NAME DIFFERENCE, do NOT "fix" it in either direction: the
	// column is gap_date because a bare `date` would be the only non-descriptive
	// date column in this schema (cf. manual_charge_entries.charged_on,
	// vehicle_snapshots.captured_date, supercharger_sessions.charge_start_date_time)
	// AND `date` is a Postgres col_name_keyword. The Go field stays Date because
	// it is already namespaced by its type -- ChargeGap.GapDate would stutter,
	// which ai/go-conventions.md forbids. sqlc will generate GapDate on the
	// telemetrydb row struct; the single mapping seam translates it, exactly as
	// this module already translates every other db row into a domain type.
	Date time.Time
	// MissingChargingType is which charge source is suspected missing for
	// this day, inferred by internal/battery at detection time (D7a).
	MissingChargingType MissingChargingType
}
```

### `GapWriter` port (`internal/telemetry/telemetry.go`)

```go
// GapWriter is telemetry's write port for the charge_gaps ledger (D3 of the
// RM28 roadmap). internal/battery is its only intended caller: after
// deriving each day's consumption for a vehicle over a window and flagging
// the days whose math does not add up (roadmap D5/D5a), it calls
// ReconcileWindow once per vehicle per nightly run with the FULL flagged set
// it computed for that window. telemetry never calls battery -- this port is
// the one leg of the one-way battery -> telemetry data flow the rest of the
// platform's dependency graph already assumes (root README.md §Dependency
// graph, LAYER 2), so no import cycle opens (roadmap D4a).
type GapWriter interface {
	// ReconcileWindow makes charge_gaps agree with flagged for exactly the
	// vehicle-day range [start, end] inclusive (whole calendar days -- see
	// ChargeGap.Date): every day present in flagged is upserted (inserted, or
	// updated in place if its MissingChargingType or VIN changed since the
	// last run); every existing charge_gaps row for (accountID, teslaID)
	// whose date falls in [start, end] but has NO matching entry in flagged
	// is deleted (roadmap D7b). Days outside [start, end] are never read or
	// touched, even if this vehicle has older or newer flagged days stored
	// elsewhere -- reconciliation is scoped to exactly the window the caller
	// just recomputed, never the vehicle's whole history.
	//
	// flagged may be empty: every previously-flagged day in the window has
	// resolved, and every existing row in the window is deleted, none
	// re-inserted -- the normal steady state once a user fixes a missing
	// charge entry.
	//
	// Every element of flagged MUST carry the SAME accountID and teslaID as
	// this call's own arguments; ReconcileWindow returns an error, and writes
	// nothing, if one does not (defense-in-depth tenant isolation, mirroring
	// Reader.SnapshotsByVehicleBetween's account_id AND tesla_id filter
	// convention). Every element's Date MUST fall within [start, end];
	// ReconcileWindow returns an error, and writes nothing, if one does not
	// (a flagged day outside its own window is a caller bug, not data to
	// silently accept -- a later call for a different window could otherwise
	// orphan or duplicate the row).
	//
	// Runs inside a single database transaction: either every upsert and
	// every delete this call makes succeeds, or the whole call has no
	// effect. A failed call is always safe to retry from scratch on the next
	// nightly run, since flagged is freshly recomputed by the caller every
	// time -- ReconcileWindow never reads charge_gaps back as an input to
	// its own decisions, only as the set to reconcile against.
	ReconcileWindow(ctx context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time, flagged []ChargeGap) error
}

// NewGapWriter constructs a GapWriter backed by a real Postgres pool. Callers
// (internal/battery via cmd/poller, D4a) depend on the GapWriter interface,
// never on the concrete type or on telemetrydb directly. Implementation is in
// gap_writer.go (forward-declared here so this file compiles before that one
// is parsed, mirroring NewSuperchargerReader's identical pattern, design B6.3
// of RM27-telemetry-add-supercharger-battery-pct).
func NewGapWriter(pool *pgxpool.Pool) GapWriter {
	return newGapWriter(pool)
}
```

### `SuperchargerReader` addition (`internal/telemetry/telemetry.go`)

```go
type SuperchargerReader interface {
	SuperchargerSessionsByAccount(ctx context.Context, accountID uuid.UUID, limit int) ([]SuperchargerSession, error)
	SuperchargerSessionsByVehicle(ctx context.Context, accountID uuid.UUID, teslaID int64, limit int) ([]SuperchargerSession, error)

	// SuperchargerSessionsByVehicleBetween returns Supercharger sessions for
	// the given vehicle (within the given account) whose ChargeStopDateTime
	// falls in the caller-supplied [start, end] window, inclusive of the
	// whole end calendar day, ordered oldest-first (ascending by
	// ChargeStopDateTime). start/end are whole UTC-midnight-bounded calendar
	// days, matching this project's platform-wide HTTP date-filter
	// convention (ai/go-conventions.md §"Read optimization").
	//
	// Filters on ChargeStopDateTime, NOT ChargeStartDateTime (roadmap D12):
	// energy is fully delivered at session stop, which is what
	// EndBatteryPct corresponds to, so a session belongs to the window
	// containing its STOP instant even when it started the day before -- a
	// session spanning midnight (ChargeStartDateTime before start,
	// ChargeStopDateTime inside [start, end]) is deliberately INCLUDED. This
	// is a pure data accessor: the port does no charge-to-day attribution of
	// its own (that is internal/battery's job, roadmap D12) -- it only
	// answers "which sessions' energy finished landing in this window."
	//
	// Returns a non-nil empty slice and nil error when no sessions exist in
	// the window (parity with SuperchargerSessionsByAccount/ByVehicle's
	// existing empty-result contract, and with Reader.SnapshotsByVehicleBetween's
	// identical convention -- no nil-slice footgun for callers). The
	// account_id AND tesla_id filter provides defense-in-depth tenant
	// isolation, mirroring every other bounded-window method in this module.
	//
	// Purely additive alongside SuperchargerSessionsByAccount/ByVehicle
	// (both unchanged, both remain limit-based for their own "most recent N"
	// access pattern). This method has no limit parameter and no LIMIT-N
	// contract -- the caller-supplied window is the bound, exactly like
	// Reader.SnapshotsByVehicleBetween's own reasoning for why a bounded
	// window makes an unbounded-N limit the caller's job, not this query's.
	SuperchargerSessionsByVehicleBetween(ctx context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) ([]SuperchargerSession, error)
}
```

### `db/query.sql` additions

```sql
-- name: UpsertChargeGap :exec
-- Upsert one flagged vehicle-day. On conflict with the
-- charge_gaps_account_tesla_date_unique constraint, refresh vin (in case the
-- vehicle's VIN changed since the day was first flagged -- cheap safety, not
-- an expected case) and missing_charging_type (the inferred type can change
-- between nightly runs if detection logic evolves, or if a Supercharger
-- session with NULL percentages later appears for a day previously inferred
-- MANUAL), and refresh updated_at to now(). created_at is DELIBERATELY
-- ABSENT from the SET clause -- design D-Table2/the table's own column
-- comment: it must record when this vehicle-day was FIRST flagged, not the
-- most recent confirmation.
INSERT INTO charge_gaps (
    account_id, tesla_id, vin, gap_date, missing_charging_type
) VALUES (
    @account_id, @tesla_id, @vin, @gap_date, @missing_charging_type
)
ON CONFLICT (account_id, tesla_id, gap_date) DO UPDATE SET
    vin                    = EXCLUDED.vin,
    missing_charging_type  = EXCLUDED.missing_charging_type,
    updated_at             = now();

-- name: DeleteChargeGap :exec
-- Delete one charge_gaps row scoped to (account_id, tesla_id, gap_date) -- a
-- point delete served by the charge_gaps_account_tesla_date_unique
-- constraint's own index (design.md Index Plan, Read path 1). Called by
-- GapWriter.ReconcileWindow for every previously-stored day in the window
-- that is no longer present in the caller's freshly-computed flagged set
-- (roadmap D7b).
DELETE FROM charge_gaps
WHERE account_id = @account_id
  AND tesla_id   = @tesla_id
  AND gap_date   = @gap_date;

-- name: ChargeGapDatesByVehicleBetween :many
-- Return every stored charge_gaps date for one vehicle (within one account)
-- in the closed range [start, end]. Used ONLY by
-- GapWriter.ReconcileWindow's internal bookkeeping to compute which
-- previously-stored days are no longer in the caller's flagged set (and so
-- must be deleted) -- not a public read port, not consumed outside this
-- module's own write path. Single-column SELECT (date only): the caller
-- already has every other field it needs for any date it decides to keep
-- (it is re-upserting from its own freshly-computed flagged set, never
-- reading this table's other columns back).
--
-- Index reuse (design.md Index Plan, Read path 1): served directly by
-- charge_gaps_account_tesla_date_unique's own (account_id, tesla_id, gap_date)
-- index as a single contiguous forward range scan -- no new index.
SELECT gap_date FROM charge_gaps
WHERE account_id = @account_id
  AND tesla_id   = @tesla_id
  AND gap_date   >= @start
  AND gap_date   <= @end_date;

-- name: SuperchargerSessionsByVehicleBetween :many
-- Return Supercharger sessions for one vehicle within an account whose
-- charge_stop_date_time falls in the caller-supplied [start, end] window,
-- inclusive of the whole end calendar day, ordered oldest-first (ascending
-- by charge_stop_date_time). Used by
-- SuperchargerReader.SuperchargerSessionsByVehicleBetween to power RM28's
-- battery-consumed-per-day derivation (roadmap D9/D12).
--
-- Filters on charge_stop_date_time, NOT charge_start_date_time (D12): energy
-- is fully delivered at session stop, which is what end_battery_pct
-- corresponds to, so a session belongs to the day its STOP falls in even
-- when it started the day before (a session spanning midnight IS included in
-- the window containing its stop instant -- deliberate, per D12).
--
-- Bounds (start, end are whole UTC-midnight-bounded calendar days; end
-- inclusive, matching this project's platform-wide HTTP date-filter
-- convention, ai/go-conventions.md §"Read optimization"): end_bound = end +
-- 1 calendar day (computed in Go, reader.go's
-- SuperchargerSessionsByVehicleBetween, mirroring
-- Reader.SnapshotsByVehicleBetween's own bounds-translation precedent of
-- doing the day-arithmetic in Go, not in SQL) so
-- WHERE charge_stop_date_time >= start AND charge_stop_date_time < end_bound
-- includes every instant of the end calendar day without an off-by-one on a
-- UTC-midnight end value. Simpler than SnapshotsByVehicleBetween's two-sided
-- +1/+2-day shift: that method filters on captured_at to select rows by
-- their DERIVED EffectiveDate (one calendar day earlier than the row's own
-- timestamp); this method filters directly on charge_stop_date_time, which
-- already IS the value being windowed -- no EffectiveDate-style lag to
-- compensate for, so only the upper bound needs translating.
--
-- Index reuse: idx_supercharger_sessions_vehicle_time
-- (account_id, tesla_id, charge_start_date_time DESC) does NOT fully serve
-- this query -- it is sorted on charge_start_date_time, not
-- charge_stop_date_time, so the stop-time predicate cannot be satisfied as a
-- pure index range scan. It STILL prunes the scan to this one vehicle's rows
-- via its (account_id, tesla_id) leading-column prefix before the
-- stop-time filter is applied in-memory -- see design.md's Index Plan for
-- why no third, dedicated (account_id, tesla_id, charge_stop_date_time)
-- index is added in this change, and the documented fallback if per-vehicle
-- session volume ever grows enough to make that decision wrong.
--
-- No LIMIT: this is a bounded date-range query, not an unbounded "most
-- recent N" query -- the caller-supplied window is the safety bound, exactly
-- like Reader.SnapshotsByVehicleBetween's own reasoning (that method DOES
-- still add a defensive LIMIT 400 on top of its window, design D3 there; this
-- query does not add an equivalent cap -- see design.md's Index Plan for why
-- that asymmetry is deliberate, not an oversight).
SELECT * FROM supercharger_sessions
WHERE account_id = @account_id
  AND tesla_id   = @tesla_id
  AND charge_stop_date_time >= @start
  AND charge_stop_date_time <  @end_bound
ORDER BY charge_stop_date_time ASC;
```

**On the no-`LIMIT` asymmetry with `SnapshotsByVehicleBetween`'s defensive `LIMIT 400`:**
`SnapshotsByVehicleBetween` caps at 400 as insurance against capture cadence increasing
(one nightly snapshot today; a future change could poll more often, D3 there). Supercharger
sessions have no comparable "cadence" risk — the row count for a window is bounded by how
often the vehicle actually visits a Supercharger, which is a real-world physical constraint
(a single vehicle cannot rack up hundreds of charging sessions in a 90-day window), not a
polling-frequency configuration this codebase controls. A `LIMIT` here would be defending
against a scenario that cannot occur, at the cost of silently truncating a legitimately
busy account's data — the opposite of the insurance `SnapshotsByVehicleBetween`'s cap
provides. **Rejected alternative:** add `LIMIT 400` anyway "for consistency." Rejected —
mirroring a sibling query's *mechanism* is valuable when the underlying risk is the same
(this design does exactly that throughout); copying a defensive cap onto a query with no
matching risk is cargo-culting, not consistency.

### Go-Level Seam Summary (single-source, change-locality)

- **`gap_writer.go` (new file)** houses the `GapWriter` implementation exclusively —
  change-locality: a future reader looking for "how are charge gaps written" finds one file,
  not a scattered addition inside `service.go`'s existing `dbStore`. It is **not** part of
  the `store` interface `service.go` defines for `Collector`/`Reader` — `SuperchargerReader`
  already established the precedent that a port not shaped by that offline-fake-testable
  seam (because it is tested via real DB-integration tests instead, like
  `SuperchargerReader` already is) gets its own small concrete type talking directly to
  `telemetrydb.Queries`.
- **`gap_writer.go` never imports `pgtype` directly.** It calls the **existing**
  `dateFrom(time.Time) pgtype.Date` helper (`service.go`) to bind `ChargeGap.Date`/window
  bounds, and one **new**, tiny reverse helper added to `mapping.go`:

  ```go
  // dateFromPg converts a non-nullable pgtype.Date to a plain time.Time,
  // mirroring dateFrom's forward direction (service.go). Used by
  // gap_writer.go to read back charge_gaps.gap_date values (always NOT NULL --
  // no nullable variant needed, unlike the pgNullable* helpers above).
  func dateFromPg(d pgtype.Date) time.Time {
  	return d.Time
  }
  ```

  This keeps pgtype confined to `service.go`/`mapping.go` exactly as
  `ai/go-conventions.md` §Persistence requires, without `gap_writer.go` needing to reference
  the `pgtype` package by name anywhere in its own source (every value it touches flows
  through `dateFrom`/`dateFromPg`, both already typed in terms of `time.Time` at the call
  site — Go infers the intermediate `pgtype.Date` type from the generated `telemetrydb`
  return values, no explicit import required).

- **`gap_writer.go`'s `ReconcileWindow` implementation:**

  ```go
  type gapWriter struct {
  	pool *pgxpool.Pool
  	q    *telemetrydb.Queries
  }

  func newGapWriter(pool *pgxpool.Pool) *gapWriter {
  	return &gapWriter{pool: pool, q: telemetrydb.New(pool)}
  }

  var _ GapWriter = (*gapWriter)(nil)

  func (w *gapWriter) ReconcileWindow(ctx context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time, flagged []ChargeGap) error {
  	for _, g := range flagged {
  		if g.AccountID != accountID || g.TeslaID != teslaID {
  			return fmt.Errorf("charge gap for account %s vehicle %d does not match call scope (account %s vehicle %d)", g.AccountID, g.TeslaID, accountID, teslaID)
  		}
  		if g.Date.Before(start) || g.Date.After(end) {
  			return fmt.Errorf("charge gap date %s falls outside window [%s, %s]", g.Date, start, end)
  		}
  	}

  	// Mirrors internal/account's own Begin/WithTx/Commit pattern
  	// (account/service.go's AccessTokenFor) -- the only other transactional
  	// write in this codebase.
  	tx, err := w.pool.Begin(ctx)
  	if err != nil {
  		return fmt.Errorf("beginning tx: %w", err)
  	}
  	defer tx.Rollback(ctx) // no-op once committed

  	qtx := w.q.WithTx(tx)

  	existing, err := qtx.ChargeGapDatesByVehicleBetween(ctx, telemetrydb.ChargeGapDatesByVehicleBetweenParams{
  		AccountID: accountID,
  		TeslaID:   teslaID,
  		Start:     dateFrom(start),
  		End:       dateFrom(end),
  	})
  	if err != nil {
  		return fmt.Errorf("loading existing charge gaps: %w", err)
  	}

  	keep := make(map[time.Time]bool, len(flagged))
  	for _, g := range flagged {
  		keep[g.Date] = true
  	}

  	for _, d := range existing {
  		if t := dateFromPg(d); !keep[t] {
  			if err := qtx.DeleteChargeGap(ctx, telemetrydb.DeleteChargeGapParams{
  				AccountID: accountID,
  				TeslaID:   teslaID,
  				Date:      d,
  			}); err != nil {
  				return fmt.Errorf("deleting resolved charge gap: %w", err)
  			}
  		}
  	}

  	for _, g := range flagged {
  		if err := qtx.UpsertChargeGap(ctx, telemetrydb.UpsertChargeGapParams{
  			AccountID:            accountID,
  			TeslaID:              teslaID,
  			Vin:                  g.VIN,
  			Date:                 dateFrom(g.Date),
  			MissingChargingType:  string(g.MissingChargingType),
  		}); err != nil {
  			return fmt.Errorf("upserting charge gap: %w", err)
  		}
  	}

  	if err := tx.Commit(ctx); err != nil {
  		return fmt.Errorf("committing tx: %w", err)
  	}
  	return nil
  }
  ```

  **Why a `SELECT` + Go-side diff + per-row `DELETE`/`UPSERT` loop, not a single
  array-bound `DELETE ... WHERE date <> ALL(@keep_dates::date[])`:** the array form is
  possible in Postgres, but this codebase has no existing precedent for binding a Postgres
  array parameter through sqlc/pgx, and the window this loop ever runs over is small by
  construction (≤ 90-ish calendar days, the same bound the rest of this roadmap's date-range
  ports already assume) — at most ~90 rows read, ~90 compared in a Go map, and a handful of
  single-row deletes/upserts, once per vehicle per night. **Rejected alternative:** the
  array-bound single-statement `DELETE`. Rejected for implementation simplicity and to avoid
  introducing this codebase's first array-typed bind parameter for a write path this
  project's own read-heavy Performance-Profile explicitly licenses paying extra (bounded,
  small, off-hours) cost on, in exchange for a design that is easier to read, test, and get
  right the first time (`ai/go-conventions.md`'s testing-order note: unexecuted DB tests
  "need to be right first time").

  **Why the whole call is wrapped in one transaction** (a choice this port's own task brief
  does not strictly mandate, but this design makes deliberately): a `ReconcileWindow` call
  that deleted some resolved days and then failed partway through upserting the still-
  flagged ones would leave `charge_gaps` in a state that is neither "the old flagged set"
  nor "the new flagged set" — a plausible source of a flickering or incorrect notification
  if the next scheduled run is hours away. Wrapping in a transaction (mirroring
  `internal/account`'s own `Begin`/`WithTx`/`Commit` pattern, the only other transactional
  write already in this codebase) makes a failed call a strict no-op instead, which is both
  easier to reason about and free — this port already reads before it writes, so the extra
  transaction machinery adds no additional round-trip.

### `reader.go` addition

```go
func (r *superchargerReader) SuperchargerSessionsByVehicleBetween(ctx context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) ([]SuperchargerSession, error) {
	endBound := end.AddDate(0, 0, 1)
	rows, err := r.q.SuperchargerSessionsByVehicleBetween(ctx, telemetrydb.SuperchargerSessionsByVehicleBetweenParams{
		AccountID: accountID,
		TeslaID:   teslaIDToPgInt8(teslaID),
		Start:     timestamptzFrom(start),
		EndBound:  timestamptzFrom(endBound),
	})
	if err != nil {
		return nil, err
	}
	sessions := make([]SuperchargerSession, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, rowToSuperchargerSession(row))
	}
	return sessions, nil
}
```

Reuses `teslaIDToPgInt8` and `timestamptzFrom` (both existing, `service.go`) and the
**existing** `rowToSuperchargerSession` mapper (`mapping.go`) — no new mapping helper for
this piece; `SuperchargerSession` gains no new field, so the existing mapper already covers
every column this query returns.

---

## Test Contract (authored before implementation, binding)

Per `ai/go-conventions.md`'s Test-Execution-Policy, these DB-integration tests are written
but not run by the assistant; they belong in a new or existing DB-gated test file in
`internal/telemetry/` (the module's `testdb` helper self-skips without Docker/`DATABASE_URL`
— `AGENTS.md` §Testing notes).

### (a) Upserting a newly-flagged day, then re-running with the same day still flagged, does not duplicate

- **Given** account `A1`, vehicle `T1` (VIN `V1`), no existing `charge_gaps` row for
  `gap_date = 2026-08-10`.
- **When** `ReconcileWindow(ctx, A1, T1, 2026-08-01, 2026-08-15, []ChargeGap{{A1, T1, "V1", 2026-08-10, MissingChargingTypeManual}})` is called.
- **Then** exactly one row exists for `(A1, T1, 2026-08-10)` with `missing_charging_type = 'MANUAL'`, and `created_at` is set.
- **When** `ReconcileWindow` is called again with the identical arguments (simulating the
  next nightly run finding the same day still flagged).
- **Then** still exactly one row exists for `(A1, T1, 2026-08-10)` — the `UNIQUE` constraint
  is the mechanism (`ON CONFLICT DO UPDATE`, not a second `INSERT`) — `missing_charging_type`
  is unchanged, `updated_at` has advanced past the first call's value, and `created_at` is
  **byte-for-byte unchanged** from the first call (proving `created_at` truly answers "first
  flagged," not "last touched").

### (b) A day that flags, then stops flagging, has its row deleted

- **Given** the row from (a): `(A1, T1, 2026-08-10, MANUAL)`.
- **When** `ReconcileWindow(ctx, A1, T1, 2026-08-01, 2026-08-15, []ChargeGap{})` is called
  (an empty flagged set — the day resolved).
- **Then** zero rows exist for `(A1, T1)` with `gap_date = 2026-08-10` — a direct `DELETE`, not a
  soft-delete/flag flip.
- **And**, distinctly: a second row for a **different** day, `(A1, T1, 2026-08-05)`, stored
  by a prior call and left flagged in this call's `flagged` set, is **untouched** by the
  same `ReconcileWindow` call that deleted `2026-08-10` — proving the reconciliation is
  precise per-day, not a blunt "clear everything then re-insert."

### (c) `SuperchargerSessionsByVehicleBetween` boundary behavior

- **Given** account `A1`, vehicle `T1`, window `start = 2026-08-10T00:00:00Z`,
  `end = 2026-08-12T00:00:00Z` (whole UTC-midnight calendar days).
- **And** four stored sessions for `(A1, T1)`:
  - S1: `charge_stop_date_time = 2026-08-10T00:00:00Z` (exactly on `start`).
  - S2: `charge_stop_date_time = 2026-08-12T00:00:00Z` (exactly on `end`, i.e. the first
    instant of the `end` calendar day).
  - S3: `charge_stop_date_time = 2026-08-12T23:59:59Z` (inside the `end` calendar day, well
    after midnight — proves the whole day is included, not just the midnight instant).
  - S4: `charge_stop_date_time = 2026-08-13T00:00:00Z` (exactly on `end + 1 day` — "just
    outside").
- **When** `SuperchargerSessionsByVehicleBetween(ctx, A1, T1, start, end)` is called.
- **Then** the returned slice contains S1, S2, and S3, and does **not** contain S4.
- **And** the returned slice is ordered oldest-first (S1 before S2 before S3).

### (d) A session spanning midnight is included, because the filter is on stop time

- **Given** the same window as (c).
- **And** a fifth session S5 with `charge_start_date_time = 2026-08-09T23:30:00Z` (the day
  BEFORE `start`) and `charge_stop_date_time = 2026-08-10T00:15:00Z` (inside the window).
- **When** `SuperchargerSessionsByVehicleBetween(ctx, A1, T1, start, end)` is called.
- **Then** S5 IS included in the result, ordered before S1 (its stop time, `00:15:00Z`, is
  after S1's `00:00:00Z`) — proving the port filters purely on stop time and ignores where
  the session started.

### (e) Tenant isolation: another account's rows are never returned or written

- **Given** account `A1` vehicle `T1`, and a second account `A2` with its own vehicle `T2`.
- **When** `ReconcileWindow(ctx, A1, T1, ..., flagged)` is called with `flagged` containing
  only `ChargeGap`s whose `AccountID == A1, TeslaID == T1`.
- **Then** no row is written for `A2`/`T2`, and calling `ReconcileWindow` for `A2`/`T2`
  afterward with its own, disjoint `flagged` set does not delete or alter any row belonging
  to `A1`/`T1`, even for overlapping `gap_date` values.
- **And**, separately: calling `ReconcileWindow(ctx, A1, T1, ..., flagged)` where one element
  of `flagged` has `AccountID = A2` (a caller bug) returns an error and writes **nothing** —
  not even the other, correctly-scoped elements of the same `flagged` slice (the whole-call
  transaction rolls back).
- **And**, for `SuperchargerSessionsByVehicleBetween`: given `A1`/`T1` and `A2`/`T2` each
  with a session whose `charge_stop_date_time` falls in the same window, calling the method
  for `(A1, T1, ...)` returns only `A1`'s session, never `A2`'s.

---

## Migration Plan (implementation order for the worker(s))

Groups **A** (the `charge_gaps` table + `GapWriter`) and **B** (`SuperchargerSessionsByVehicleBetween`)
are independent in design and could be implemented in either order, but both touch
`telemetry.go` and `db/query.sql` — see tasks.md's header for the file-overlap note.

1. `internal/telemetry/db/migrations/20260815000002_add_charge_gaps.sql` — Group A, no
   dependencies.
2. `internal/telemetry/telemetry.go` — `MissingChargingType`, `ChargeGap`, `GapWriter`
   (Group A) and the `SuperchargerReader.SuperchargerSessionsByVehicleBetween` method
   addition (Group B) — both are pure Go-type/interface additions with no DB dependency, so
   the whole file can be edited in one pass regardless of which group's migration/query work
   has landed yet.
3. `internal/telemetry/db/query.sql` — `UpsertChargeGap`, `DeleteChargeGap`,
   `ChargeGapDatesByVehicleBetween` (Group A, depends on step 1) and
   `SuperchargerSessionsByVehicleBetween` (Group B, no DB dependency — no migration
   required). Leader runs `make sqlc` after this step to regenerate `telemetrydb`.
4. `internal/telemetry/mapping.go` — new `dateFromPg` helper (Group A, depends on step 3).
5. `internal/telemetry/gap_writer.go` (new file) — `GapWriter` implementation (Group A,
   depends on steps 2, 3, 4).
6. `internal/telemetry/reader.go` — `SuperchargerSessionsByVehicleBetween` implementation
   (Group B, depends on steps 2, 3).
7. New DB-integration tests: test-contract scenarios (a)–(e) above (depends on steps 1, 3,
   5, 6).
8. `internal/telemetry/AGENTS.md` — document the new table, port, and read method (depends
   on step 1; can run any time after the schema is finalized).
9. Verification: `go build ./...`, `go vet ./...`, `gofmt -l`,
   `openspec validate RM28-telemetry-add-charge-gap-storage --strict`.

## Risks / Trade-offs

- **`ReconcileWindow`'s implementation is a read-then-diff-then-write loop, not a single SQL
  statement.** Accepted per the rejected-alternative discussion above — simplicity and
  zero new array-bind machinery, traded for one extra `SELECT` round-trip per nightly call.
  Negligible under this project's read-heavy/write-light-and-off-hours Performance-Profile.
- **The account-wide `idx_charge_gaps_account` index is built ahead of its consumer** (the
  future notification read port, out of scope this tier). Accepted because the read pattern
  itself — not just the index — is explicitly named as one of two patterns this design must
  serve; see the Index Plan's own justification for why this differs from RM27's rejected
  hypothetical-index precedent.
- **No cross-column consistency check** (e.g., asserting `missing_charging_type = 'SUPERCHARGER'`
  implies a matching NULL-percentage session actually exists for that date). Not enforceable
  as a database `CHECK` (it would require a cross-table subquery, which Postgres `CHECK`
  constraints cannot express) and not worth a trigger for a single write path with one
  caller under this module's own control. If `internal/battery`'s inference ever produces a
  wrong `missing_charging_type`, that is a bug in the caller's D7a logic (tier 3's
  responsibility), not something this table's schema can catch.
