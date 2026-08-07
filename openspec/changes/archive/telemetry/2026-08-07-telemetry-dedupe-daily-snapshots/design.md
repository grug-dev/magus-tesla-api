## Context

`internal/telemetry` — `vehicle_snapshots` currently has no unique constraint and the
init migration documents it as append-only by design ("one immutable row per SUCCESSFUL
capture … no updated_at, no UNIQUE that would block a second capture of the same
vehicle"). `cmd/poller --once` and the nightly `Scheduler` both call the same
`Collector.CollectAll`, which calls `attemptVehicle` → `snapshotFrom` → `store.insertSnapshot`
once per vehicle per cycle, with **no idempotency check**. Running `--once` twice on the
same day therefore stores two rows. Live DB confirms this: 44 rows, 2 duplicate pairs,
both dated 2026-08-04.

The user wants at most one stored row per `(account, vehicle, calendar day)`. This design
implements the five decisions the user settled with the leader in the grill step (D1–D5
below, verbatim from the dispatch), plus the implementation-seam decisions needed to
realize them.

Primary module: **`internal/telemetry/`**. Secondary, thin-wiring-only: **`cmd/poller/main.go`**
— outside `internal/telemetry`'s sandbox; see "Scope Boundary" below.

## Goals / Non-Goals

**Goals:**

- At most one `vehicle_snapshots` row per `(account_id, tesla_id, captured_date)`.
- A same-day re-capture REPLACES the existing row (latest wins) — D1.
- `captured_date` is Go-computed from `captured_at` in the poller's configured timezone,
  never a DB expression tied to a fixed zone — D2.
- Backfill: dedupe the 2 known live duplicates, keeping the latest `captured_at` of each
  pair — D3.
- `poll_attempts` is completely unaffected — D4.
- Document the reversal of the append-only invariant everywhere it was previously
  asserted — D5.
- Zero read-path plan/shape regressions for `LatestSnapshotsByAccount` and
  `SnapshotsByVehicleSince`.

**Non-Goals:**

- Any gateway/dashboard change (no consumer needs to change).
- Collapsing `idx_vehicle_snapshots_vehicle_time` into the new UNIQUE index (see Index
  Plan — a future optimization, not required here).
- Fixing the pre-existing gap that `POLLER_TIMEZONE` is undocumented in `.env.example`.

---

## Design Decisions

### D1 — Conflict rule: REPLACE, latest wins (BINDING, verbatim from the grill step)

`InsertVehicleSnapshot` becomes `INSERT ... ON CONFLICT (account_id, tesla_id,
captured_date) DO UPDATE SET <every typed column>, raw_data, captured_at, updated_at =
now()`. Rationale: a re-run for a day that already has a row is normally the user
correcting a bad or partial nightly capture (a `--once` re-run after fixing a wake-timeout
or an api-error), so the freshest data should survive, not the first. `account_id`,
`tesla_id`, and `captured_date` are the conflict target and are never themselves
overwritten (they are identical between the old and new row by construction).

**Rejected alternative — IGNORE (keep first, discard re-runs' data):** silently discards
the user's corrective re-run, which is the exact scenario D1 exists to help. Rejected.

### D2 — `captured_date` is a Go-computed `DATE` column (BINDING, verbatim from the grill step)

A Postgres UNIQUE index cannot depend on a runtime env var (`POLLER_TIMEZONE`), so the
calendar day must be derived in Go and written as a plain, stored column; the constraint
is `UNIQUE (account_id, tesla_id, captured_date)`.

**Rejected alternatives (both are expression-index designs, explicitly rejected):**

- **(a) Expression index on `(captured_at AT TIME ZONE 'UTC')::date`.** A 03:30-local
  poller run near midnight lands on a different UTC calendar day depending on the
  operator's offset from UTC — e.g. a 03:30 America/Bogota (UTC-5) run is 08:30 UTC, safely
  same-day, but a 03:30 in a UTC+10 zone is 17:30 the PREVIOUS UTC day. The index would
  silently mean "the UTC day," not "the day the user means" (their local operational day).
  Rejected.
- **(b) Expression index on a hardcoded timezone literal (e.g. `'America/Bogota'`).**
  Correct for today's single known deployment, but hardcodes the timezone into the
  **schema** — a permanent, ongoing dependency that silently diverges the instant
  `POLLER_TIMEZONE` is changed (no error, no warning — just quietly wrong future dates),
  and recovering from that divergence requires a migration **and** a full index rebuild.
  Rejected as an ongoing schema dependency (see D3a below for why a hardcoded literal is
  fine in a one-time backfill instead — a categorically different kind of use).

**Chosen design matches existing module precedent:** `deriveEnergyKWh` and
`deriveTotalCost` (Source B, `service.go`) already derive computed values in Go rather
than in SQL, specifically so business logic stays testable offline without a DB. This
extends the same pattern to date derivation. It also needs **no migration** if
`POLLER_TIMEZONE` is ever changed — only future writes are affected, immediately, with no
schema change.

#### D2a — The seam: how the location reaches the mapping code (implementation decision)

`snapshotFrom` (the DTO→domain mapping function in `service.go`) has no access to a
`*time.Location` today. `telemetry.Config` is the natural carrier — it already holds
`Clock` for exactly this kind of test/production seam split.

```go
// Config (internal/telemetry/telemetry.go) — add:
type Config struct {
    WakeTimeout time.Duration
    Clock       func() time.Time
    // Location is the timezone used to derive CapturedDate — the calendar day a
    // snapshot belongs to — from CapturedAt at write time (D2 of
    // telemetry-dedupe-daily-snapshots). Nil means the poller's local timezone
    // (time.Local), mirroring NewScheduler's own "nil loc falls back to
    // time.Local" convention. cmd/poller sets this from config.PollerTimezone via
    // time.LoadLocation — the SAME *time.Location passed to NewScheduler — so the
    // day a snapshot is dated always agrees with the day the scheduler considers
    // "today" for that run.
    Location *time.Location
}
```

`service` gets a `location()` helper mirroring the existing `now()` helper:

```go
// location returns the configured timezone for calendar-day derivation, honoring
// the injected Config.Location when present so CapturedDate is deterministic in
// tests; production falls back to time.Local (D2a).
func (s *service) location() *time.Location {
    if s.cfg.Location != nil {
        return s.cfg.Location
    }
    return time.Local
}
```

`snapshotFrom` gains a `loc *time.Location` parameter and computes `CapturedDate`:

```go
func snapshotFrom(accountID uuid.UUID, teslaID int64, capturedAt time.Time, loc *time.Location, data *tesla.VehicleDataTesla, raw []byte) Snapshot {
    return Snapshot{
        AccountID:    accountID,
        TeslaID:      teslaID,
        CapturedAt:   capturedAt,
        CapturedDate: dateOnly(capturedAt, loc),
        // ...unchanged fields...
    }
}

// dateOnly returns the calendar date of t in loc, normalized to UTC midnight — the
// representation pgtype.Date expects. This is the single place the poller's
// configured timezone determines which calendar day a snapshot belongs to (D2).
func dateOnly(t time.Time, loc *time.Location) time.Time {
    y, m, d := t.In(loc).Date()
    return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
```

Call site in `attemptVehicle`: `snap := snapshotFrom(v.AccountID, v.TeslaID, s.now(), s.location(), data, raw)`.

**`cmd/poller/main.go` wiring (outside `internal/telemetry`, see Scope Boundary):** today,
`time.LoadLocation(cfg.PollerTimezone)` is called only inside the `if !*once` branch —
*after* `telemetry.Config` (`tcfg`) has already been built and handed to
`telemetry.NewService`. It must move to run **unconditionally**, before `tcfg` is
constructed, so both the `--once` path and the nightly `Scheduler` path share the exact
same `*time.Location` for both `Config.Location` and `NewScheduler`'s `loc` parameter:

```go
// BEFORE (today): loc is loaded only inside `if !*once`, after tcfg/collector exist.
// AFTER: load once, unconditionally, before tcfg is built.
loc, err := time.LoadLocation(cfg.PollerTimezone)
if err != nil {
    log.Fatalf("invalid POLLER_TIMEZONE %q: %v", cfg.PollerTimezone, err)
}

tcfg := telemetry.Config{WakeTimeout: cfg.PollerWakeTimeout, Location: loc}
collector := telemetry.NewService(pool, acct, tesla.NewClient(), tcfg)

if !*once {
    scheduler := telemetry.NewScheduler(collector, cfg.PollerScheduleHour, cfg.PollerScheduleMinute, loc, tcfg)
    // ...
} else {
    // ...
}
```

**Intentional behavior change:** `--once` now validates `POLLER_TIMEZONE` and will
`log.Fatalf` on an invalid value, where previously it silently ignored the setting
entirely. This is the direct consequence of both paths needing to agree on "what day is
it" (documented as a Breaking item in proposal.md).

### D3 — Backfill dedupe: keep the latest `captured_at` (BINDING, verbatim from the grill step)

Migration order, exactly as specified: (1) add `captured_date` nullable, (2) backfill it
from `captured_at`, (3) delete the older row of each duplicate group, (4) add the UNIQUE
constraint and set NOT NULL. Full DDL below. Deleting the 2 stale rows is expected and
authorized; the Down migration cannot recover them (documented in the migration and
restated below).

#### D3a — The backfill's timezone literal

The backfill converts an absolute `TIMESTAMPTZ` instant into a calendar `DATE`, which
requires picking a timezone — the Go-side (future writes) and the migration-side
(existing rows) must agree on what day an existing row belongs to.

**Chosen literal: `'America/Bogota'`.** Justification: `POLLER_TIMEZONE` has never been
set in this project's `.env` (grep confirms no entry in either `.env` or `.env.example`),
so `config.Load` defaults it to `"Local"` — Go's `time.Local`, which resolves to the
deployment host's OS timezone. That host's `/etc/localtime` resolves to
`America/Bogota` (confirmed on the live system). So `'America/Bogota'` reproduces, for the
44 already-stored rows, **exactly the date Go's `time.Local` would have computed for them
at capture time** — the identical rule D2 gives every future row, just evaluated once
here instead of per-row in Go.

**Why this does not reintroduce the D2(b) rejection.** D2(b) rejected a hardcoded
timezone literal specifically because an **expression index** is a permanent, ongoing part
of the schema that silently diverges the moment `POLLER_TIMEZONE` changes. This backfill
`UPDATE` is a one-time, point-in-time data migration: it runs once, against rows that were
**already captured** under the timezone that was actually in effect at the time (which,
for every existing row, was this same default), and is retired the instant the migration
completes. If `POLLER_TIMEZONE` is changed later, it affects only future Go-computed
`captured_date` values (D2) — it has no bearing on rows this backfill already dated,
because those rows' true capture timezone is a historical fact, not a moving target.

**Verification against the live data:** both known duplicate pairs are timestamped
2026-08-04 04:39–05:37 local (America/Bogota, UTC-5) / 09:39–10:37 UTC — safely inside the
same calendar day under either UTC or `America/Bogota`, so the chosen literal reproduces
the already-known "both dated 2026-08-04" fact with no ambiguity for this dataset.

### D4 — Scope is `vehicle_snapshots` only (BINDING, verbatim from the grill step)

`poll_attempts` stays append-only, one row per (vehicle, run) — it exists to record
**every** attempt including failures and retries, which is exactly the
availability/sleep-behavior signal a daily collapse would destroy (a vehicle that fails
three times in one night before finally succeeding must show three attempts, not one).
No migration, no query, no Go change touches `poll_attempts` in this change.

### D5 — Reverse the append-only invariant, and document the supersession (BINDING, verbatim from the grill step)

This change explicitly **SUPERSEDES design D1 of `RM1-telemetry-add-nightly-snapshots`**
(the decision that made `vehicle_snapshots` append-only-by-design, recorded in migration
`20260710000002_init_telemetry.sql`). Required, and delivered by this design:

1. The new migration's header comment states the supersession and why (see DDL below).
2. `internal/telemetry/AGENTS.md` "Data ownership" is rewritten: `vehicle_snapshots` is no
   longer append-only/immutable (`poll_attempts` still is); the new `captured_date` column
   and UNIQUE constraint are documented (task T7).
3. `proposal.md` names D1 (of RM1) as the superseded decision.
4. An `updated_at TIMESTAMPTZ NOT NULL DEFAULT now()` column is added — the original D1
   rationale for omitting it ("never overwritten") no longer holds.

**`updated_at` behavior (mirrors `supercharger_sessions.updated_at`, migration
`20260716000001`, this module's existing precedent for a mutable row's audit column):**

- **On a fresh INSERT** (new day, or the row's first-ever write): `DEFAULT now()` — no
  application param sent, matching how `UpsertSuperchargerSession` never sends
  `updated_at` either.
- **On the ON CONFLICT DO UPDATE replace** (D1): explicitly `SET updated_at = now()` in
  the SQL, refreshing it to the moment of the replace — the audit signal that this row
  was overwritten, and when.
- **On the 44 backfilled historical rows:** `ADD COLUMN ... DEFAULT now()` sets
  `updated_at` to the **migration's own run time** for every existing row. This is a
  documented artifact of the migration, not a claim that those rows were just
  overwritten — they were not. The migration's comment says so explicitly.

`updated_at` is selected in every read query's explicit column list (for
`telemetrydb.VehicleSnapshot` struct-sharing consistency across all four queries — see
Read Path below) but is **not** mapped onto the domain `Snapshot` type, mirroring the
existing precedent that `id` is likewise selected everywhere but never surfaced on
`Snapshot`. It is a DB-mechanics/audit column, not a domain concept callers need.

---

## Schema

### DDL (goose migration)

**Filename:** `internal/telemetry/db/migrations/20260805000001_dedupe_vehicle_snapshots_daily.sql`
(next available slot after `20260802000001`).

```sql
-- +goose Up
-- internal/telemetry — dedupe vehicle_snapshots to at most one row per
-- (account_id, tesla_id, captured_date). SUPERSEDES the append-only /
-- immutable invariant recorded in migration 20260710000002_init_telemetry.sql
-- (design D1 of RM1-telemetry-add-nightly-snapshots): a repeat capture for
-- the SAME calendar day now REPLACES that day's row (latest capture wins)
-- instead of adding a duplicate. poll_attempts is UNCHANGED by this
-- migration and stays append-only — it exists to record every attempt
-- including failures/retries, the availability/sleep-behavior signal a
-- daily collapse would destroy (design D4).
--
-- captured_date is NOT a generated/expression column: a UNIQUE index cannot
-- depend on POLLER_TIMEZONE (a runtime env var), and an expression index
-- pinned to a fixed zone would silently diverge from whatever
-- POLLER_TIMEZONE actually is, with no error — just quietly wrong future
-- dates, recoverable only via a migration AND a full index rebuild (design
-- D2, rejected alternatives). Instead, captured_date is a plain column
-- computed in Go (snapshotFrom / dateOnly, internal/telemetry/service.go)
-- from captured_at in the poller's configured *time.Location
-- (Config.Location, falling back to time.Local) — the same "derive in Go,
-- not SQL" precedent already used by deriveEnergyKWh / deriveTotalCost in
-- this module.

ALTER TABLE vehicle_snapshots ADD COLUMN captured_date DATE;

-- Backfill existing rows. This is a ONE-TIME, point-in-time conversion, not
-- an ongoing schema dependency like the rejected expression-index
-- alternative would be — so hardcoding a literal IANA zone here is safe and
-- does not reintroduce that coupling (design D3a). 'America/Bogota' is used
-- because POLLER_TIMEZONE has never been set in this project's .env (it
-- defaults to "Local", i.e. Go's time.Local), and the deployment host's
-- system zone IS America/Bogota — so this reproduces, for every existing
-- row, exactly the date Go's time.Local would have computed for it at
-- capture time. If this migration is ever run against a deployment whose
-- historical rows were captured under a genuinely different zone, update
-- this literal accordingly before applying.
UPDATE vehicle_snapshots
SET captured_date = (captured_at AT TIME ZONE 'America/Bogota')::date;

-- Dedupe: for every (account_id, tesla_id, captured_date) group with more
-- than one row, delete every row except the one with the latest
-- captured_at (ties broken by id, for determinism) — the same "latest
-- capture wins" rule D1 gives the write path going forward (design D3).
-- Deleted rows are NOT recoverable (raw_data is lost with them); this is
-- expected and authorized. As of this migration's authoring the live DB has
-- exactly 2 duplicate pairs, both dated 2026-08-04.
DELETE FROM vehicle_snapshots a
USING vehicle_snapshots b
WHERE a.account_id = b.account_id
  AND a.tesla_id = b.tesla_id
  AND a.captured_date = b.captured_date
  AND (a.captured_at < b.captured_at
       OR (a.captured_at = b.captured_at AND a.id < b.id));

-- Conflict target for the write path's new ON CONFLICT upsert (design D1),
-- and the structural mechanism that makes "at most one row per vehicle per
-- day" an enforced invariant rather than just an application convention.
ALTER TABLE vehicle_snapshots
    ADD CONSTRAINT vehicle_snapshots_account_tesla_date_unique
    UNIQUE (account_id, tesla_id, captured_date);

ALTER TABLE vehicle_snapshots ALTER COLUMN captured_date SET NOT NULL;

-- Audit trail for the now-possible same-day REPLACE (design D5). DEFAULT
-- now() backfills existing rows to this migration's run time (they were
-- never actually "updated" before now — this is a documented artifact of
-- the migration, not a claim those rows were overwritten) and gives every
-- future INSERT a value for free; the write path's ON CONFLICT ... DO
-- UPDATE explicitly SETs updated_at = now() to refresh it on every same-day
-- replace (see InsertVehicleSnapshot, query.sql). Mirrors
-- supercharger_sessions.updated_at (migration 20260716000001), this
-- module's existing precedent for a mutable row's audit column.
ALTER TABLE vehicle_snapshots ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

-- +goose Down
-- NOT a full rollback of history: the duplicate rows deleted by the Up
-- migration's dedupe step are NOT recoverable (their raw_data is gone with
-- them). Down only removes this migration's schema additions; it cannot,
-- and does not attempt to, restore the deleted rows.
ALTER TABLE vehicle_snapshots DROP CONSTRAINT IF EXISTS vehicle_snapshots_account_tesla_date_unique;
ALTER TABLE vehicle_snapshots DROP COLUMN IF EXISTS updated_at;
ALTER TABLE vehicle_snapshots DROP COLUMN IF EXISTS captured_date;
```

### Index plan

**Both indexes are kept — they are not redundant, and the new one is not merely an
optimization.**

- **`idx_vehicle_snapshots_vehicle_time (account_id, tesla_id, captured_at)`** (existing,
  unchanged) continues to serve both read queries exactly as before:
  - `LatestSnapshotsByAccount`: `SELECT DISTINCT ON (tesla_id) ... WHERE account_id = @account_id
    ORDER BY tesla_id, captured_at DESC` — needs `captured_at` (full timestamp) ordering.
    `captured_date` (DATE, coarser granularity) cannot serve this `ORDER BY` — a DATE-keyed
    index cannot resolve which of potentially-many historical rows sharing... (moot after
    this change, since there is now at most one row per day, but the query itself is
    **unchanged** by this design, so it still requires the `captured_at`-ordered index).
  - `SnapshotsByVehicleSince`: `captured_at >= @since ORDER BY captured_at ASC LIMIT 400` —
    a forward range scan keyed on `captured_at`; `captured_date` cannot serve a
    `>=`-on-timestamp predicate at timestamp granularity.
- **`vehicle_snapshots_account_tesla_date_unique (account_id, tesla_id, captured_date)`**
  (new) is **required**, not optional: it is the ON CONFLICT target for the upsert (D1).
  Postgres requires a matching UNIQUE index/constraint for `ON CONFLICT (...)` to be valid
  SQL at all — without it, the upsert cannot exist. Its read-side utility is secondary and
  narrow (an equality lookup on a specific vehicle+day, not exercised by either existing
  read query today).

Given the read-heavy Performance-Profile ("writes are mostly done by pollers at midnight
… denormalizing, indexing aggressively … is acceptable"), the extra index's write-time
cost is negligible (one nightly upsert per vehicle) and it is mechanically necessary for
D1, not a discretionary read optimization — so keeping both is correct, not merely
tolerated.

**Considered, explicitly out of scope:** now that at most one row exists per vehicle per
day going forward, `LatestSnapshotsByAccount`'s `ORDER BY` could in principle be changed
to `captured_date DESC` (same relative order as `captured_at DESC`, since the two are now
1:1 per row), which would let the new UNIQUE index also serve that read query and
potentially retire `idx_vehicle_snapshots_vehicle_time`. This is a read-query redesign, not
required by any binding decision, and the write-side saving from dropping one small index
is already low-priority under this project's read-heavy profile. Not done here.

---

## Write Path

### `InsertVehicleSnapshot` becomes an upsert (`internal/telemetry/db/query.sql`)

```sql
-- name: InsertVehicleSnapshot :exec
-- Upsert one snapshot: inserts a new row, or REPLACES the existing row for
-- the same (account_id, tesla_id, captured_date) if one already exists — the
-- newest capture for a calendar day always wins (design D1 of
-- telemetry-dedupe-daily-snapshots, which SUPERSEDES the table's prior
-- append-only invariant — migration 20260710000002 design D1 of
-- RM1-telemetry-add-nightly-snapshots). captured_date is Go-computed
-- (snapshotFrom/dateOnly, service.go) from captured_at in the poller's
-- configured timezone (design D2) — never a DB expression, because a UNIQUE
-- index cannot depend on the runtime POLLER_TIMEZONE env var.
-- [... existing nullable-column comment block (D12/DSA3, sentry_mode,
-- charge-enrichment, max_range_charge_counter, tpms_pressure_*) is
-- unchanged and stays in place above the statement ...]
-- updated_at is NOT sent as a param: DEFAULT now() handles a fresh INSERT;
-- the ON CONFLICT clause explicitly refreshes it to now() on a same-day
-- replace (design D5), mirroring UpsertSuperchargerSession's own
-- `updated_at = now()`.
INSERT INTO vehicle_snapshots (
    account_id, tesla_id, captured_at, raw_data,
    battery_level, battery_range, charging_state, charge_limit_soc,
    odometer, inside_temp, outside_temp, locked, sentry_mode,
    car_version,
    charge_energy_added, charger_power, charger_voltage,
    charger_actual_current, usable_battery_level,
    max_range_charge_counter,
    tpms_pressure_fl, tpms_pressure_fr, tpms_pressure_rl, tpms_pressure_rr,
    captured_date
) VALUES (
    @account_id, @tesla_id, @captured_at, @raw_data,
    @battery_level, @battery_range, @charging_state, @charge_limit_soc,
    @odometer, @inside_temp, @outside_temp, @locked, @sentry_mode,
    @car_version,
    @charge_energy_added, @charger_power, @charger_voltage,
    @charger_actual_current, @usable_battery_level,
    @max_range_charge_counter,
    @tpms_pressure_fl, @tpms_pressure_fr, @tpms_pressure_rl, @tpms_pressure_rr,
    @captured_date
)
ON CONFLICT (account_id, tesla_id, captured_date) DO UPDATE SET
    captured_at              = EXCLUDED.captured_at,
    raw_data                 = EXCLUDED.raw_data,
    battery_level            = EXCLUDED.battery_level,
    battery_range            = EXCLUDED.battery_range,
    charging_state           = EXCLUDED.charging_state,
    charge_limit_soc         = EXCLUDED.charge_limit_soc,
    odometer                 = EXCLUDED.odometer,
    inside_temp              = EXCLUDED.inside_temp,
    outside_temp              = EXCLUDED.outside_temp,
    locked                   = EXCLUDED.locked,
    sentry_mode              = EXCLUDED.sentry_mode,
    car_version              = EXCLUDED.car_version,
    charge_energy_added      = EXCLUDED.charge_energy_added,
    charger_power            = EXCLUDED.charger_power,
    charger_voltage          = EXCLUDED.charger_voltage,
    charger_actual_current   = EXCLUDED.charger_actual_current,
    usable_battery_level     = EXCLUDED.usable_battery_level,
    max_range_charge_counter = EXCLUDED.max_range_charge_counter,
    tpms_pressure_fl         = EXCLUDED.tpms_pressure_fl,
    tpms_pressure_fr         = EXCLUDED.tpms_pressure_fr,
    tpms_pressure_rl         = EXCLUDED.tpms_pressure_rl,
    tpms_pressure_rr         = EXCLUDED.tpms_pressure_rr,
    updated_at                = now();
```

(Fix the two accidental double-space typos — `outside_temp` / `updated_at` — when
transcribing into the real file.)

### `Snapshot.CapturedDate` (`internal/telemetry/telemetry.go`)

```go
// CapturedDate is the calendar date CapturedAt falls on, computed in the
// poller's configured timezone (Config.Location) at write time (design D2 of
// telemetry-dedupe-daily-snapshots). It backs the UNIQUE (account_id,
// tesla_id, captured_date) constraint that collapses repeated same-day
// captures into one row (design D1: latest capture wins). Represented as a
// time.Time normalized to UTC midnight (the pgtype.Date convention) — treat
// it as a plain calendar date, not a timestamp; CapturedAt remains the
// authoritative "when."
CapturedDate time.Time
```

Placed immediately after `CapturedAt` on the `Snapshot` struct. Additive named field —
existing struct literals that do not name it remain compile-compatible (same precedent as
every prior extraction change), though see "Test Blast Radius" below for why compiling is
not the same as behaving correctly for tests that insert multiple snapshots.

### `snapshotFrom` + `dbStore.insertSnapshot` (`internal/telemetry/service.go`)

`snapshotFrom` changes as shown in D2a. `dbStore.insertSnapshot` gains one param:

```go
CapturedDate: dateFrom(s.CapturedDate),
```

using a new small pgtype boundary helper mirroring `timestamptzFrom`:

```go
// dateFrom converts a plain time.Time (already normalized to a calendar date
// by dateOnly) into a valid pgtype.Date at the DB boundary, mirroring
// timestamptzFrom. Lives here so pgtype stays confined to service.go/mapping.go.
func dateFrom(t time.Time) pgtype.Date {
    return pgtype.Date{Time: t, Valid: true}
}
```

`updated_at` is deliberately NOT passed as an `InsertVehicleSnapshotParams` field — the DB
computes it entirely (DEFAULT on insert, explicit `now()` on conflict-update), per D5.

---

## Read Path

**No new read method, no plan change.** `LatestSnapshotsByAccount`, `SnapshotsByVehicleSince`,
and the test-only `ListSnapshotsByVehicle` all currently SELECT the full, explicit column
list matching `telemetrydb.VehicleSnapshot`'s complete column set (this is why sqlc reuses
one shared struct across all three query functions, and why `rowToSnapshot(r
telemetrydb.VehicleSnapshot) Snapshot` is a single, shared mapper). To preserve that
sharing — and avoid sqlc generating a divergent per-query row type that would break
`rowToSnapshot`'s signature — `captured_date` and `updated_at` are appended to the end of
all three SELECT lists (matching the migration's physical column-append order, the same
convention every prior column-adding change in this module already follows).

### `rowToSnapshot` (`internal/telemetry/mapping.go`)

```go
CapturedDate: r.CapturedDate.Time,
```

`updated_at` is selected (for the reason above) but intentionally **not** mapped onto
`Snapshot` — same precedent as `id`, which is selected in every query but never surfaced
on the domain type. Both are DB-mechanics columns; `CapturedDate` is domain-meaningful
("which day is this snapshot for") and is mapped, `updated_at` is not.

---

## Go-Level Seam Summary (single-source, change-locality)

One value — the poller's configured `*time.Location` — flows through exactly one path:
`cmd/poller/main.go` loads it once from `config.PollerTimezone` → both
`telemetry.NewScheduler`'s `loc` param (schedule-time math, unchanged) and
`telemetry.Config.Location` (new — calendar-day math). `service.location()` reads
`Config.Location` with the `time.Local` fallback. `snapshotFrom` is the only place that
calls `dateOnly`. No second, independent timezone-reading path exists anywhere in this
change — this is what keeps D2 from silently drifting into two different "what timezone"
answers.

---

## Scope Boundary

**This change's primary surface is `internal/telemetry`.** The `cmd/poller/main.go` edit
is a **thin wiring change only** (moving an existing `time.LoadLocation` call earlier and
adding one field to an existing struct literal) — zero business logic, consistent with
`ai/go-conventions.md`'s "cmd/ files must stay thin." It is nonetheless **outside
`internal/telemetry`'s module sandbox** (`ai/architecture.md` §"Modules-Root: internal/").
The leader must either (a) explicitly grant `cmd/poller/main.go` to whichever worker
implements this change, or (b) dispatch that one file as a separate, tightly-scoped task.
Flagged here and in proposal.md's "Modules affected" so it is not missed.

`poll_attempts` (design D4) receives **zero** changes in this entire design — no
migration, no query, no Go code touches it.

---

## Test Blast Radius (important — wider than just the append-only test)

The dispatch names one existing test explicitly: `TestStore_SnapshotAppendOnly`
(`internal/telemetry/db_integration_test.go`), which inserts two snapshots for the same
vehicle at different `CapturedAt` times (both "today," an hour apart) and asserts **two**
rows exist. Under the new constraint, if both literals compute to the same
`captured_date`, this test's premise (append-only ⇒ 2 rows) is now false by design — it
must be rewritten, not merely patched.

**But the blast radius is wider.** Every DB integration test file that inserts more than
one `Snapshot` for the same `(account_id, tesla_id)` — regardless of whether the test's
*intent* was same-day duplication or genuinely different days — now depends on
`CapturedDate` being set correctly on each literal, because `CapturedDate` is a new
**required** (`NOT NULL`) column with **no default** (D2: it cannot have a DB default,
it's Go-computed). Any literal that omits it gets the Go zero value (`time.Time{}`,
year 1) — and if a test builds several such literals for the same vehicle, they all
collide on that same zero-value date and silently REPLACE each other instead of
coexisting, breaking tests that assert on multi-row history (range queries, "latest of
several" queries) in a confusing way (fewer rows than expected, not a constraint-violation
error, since the upsert makes a would-be conflict succeed silently).

Affected files (confirmed by grep for `st.insertSnapshot` call sites with `Snapshot{}`
literals):

- `internal/telemetry/db_integration_test.go` — the append-only test (rewrite/replace);
  the two sentry round-trip tests (single-snapshot each, low risk, but must still set
  `CapturedDate` for hygiene/correctness).
- `internal/telemetry/db_read_integration_test.go` — the read-port tests build multiple
  snapshots per vehicle across **intentionally different days** to exercise
  `SnapshotsByVehicleSince`'s window filter and `LatestSnapshotsByAccount`'s "pick the
  newest" behavior. Each literal's `CapturedDate` must be set consistent with its own
  `CapturedAt` (same calendar date, computed the same way `dateOnly` would, e.g. via
  `time.Date(y, m, d, 0, 0, 0, 0, time.UTC)`), or these tests silently lose rows.
- `internal/telemetry/db_sourcea_integration_test.go` — charge-enrichment round-trip
  tests; same audit needed wherever more than one snapshot is inserted per vehicle.
- `internal/telemetry/db_tpms_integration_test.go` — TPMS round-trip tests; same audit.

`reader_test.go` and `service_test.go` use `fakeReadStore`/`fakeStore` (no real DB, no
uniqueness enforcement) — they compile unchanged (additive field) and need **no**
behavioral fix, only optional hygiene if a test wants to assert `CapturedDate` explicitly.

---

## Risks / Trade-offs

- **Same-day re-runs silently discard the prior capture's `raw_data`.** This is D1's
  intended behavior (latest wins), not a bug — but it means a `--once` re-run for
  diagnostic purposes will overwrite, not append, if it lands on a day already captured.
  `updated_at` (D5) is the only trace that a replace happened.
- **The backfill's timezone literal is deployment-specific.** `'America/Bogota'` is
  correct for this project's one known deployment today; a future multi-host or
  redeployed-elsewhere scenario would need this literal re-derived before re-running the
  migration fresh (moot for `goose up` on the existing DB, since the migration runs once
  and is then permanently recorded as applied).
- **`--once` gains a new failure mode.** An invalid `POLLER_TIMEZONE` now fatals `--once`
  where it previously didn't matter. Intentional (D2a), documented as Breaking.
- **Test blast radius is larger than the single named test.** See above — mitigated by
  making it an explicit, itemized task rather than discovering it mid-implementation.

---

## Migration Plan (implementation order for the worker(s))

1. `internal/telemetry/db/migrations/20260805000001_dedupe_vehicle_snapshots_daily.sql` —
   the DDL above (no dependencies).
2. `internal/telemetry/telemetry.go` — `Config.Location` field; `Snapshot.CapturedDate`
   field (no dependencies; pure struct additions).
3. `internal/telemetry/service.go` — `location()` helper, `dateOnly()` helper,
   `snapshotFrom` signature + `CapturedDate` wiring, `dbStore.insertSnapshot`
   `CapturedDate` param, `dateFrom` pgtype helper (depends on step 2).
4. `internal/telemetry/db/query.sql` — `InsertVehicleSnapshot` upsert rewrite; append
   `captured_date, updated_at` to all three read SELECT lists (depends on step 1). Leader
   runs `make sqlc` after this step to regenerate `telemetrydb`.
5. `internal/telemetry/mapping.go` — `rowToSnapshot` `CapturedDate` mapping (depends on
   steps 3, 4).
6. `cmd/poller/main.go` — move `time.LoadLocation` earlier, unconditional; set
   `tcfg.Location` (depends on step 2; **outside module sandbox**, see Scope Boundary).
7. `internal/telemetry/AGENTS.md` — rewrite "Data ownership" (design D5); can run any time
   after step 1's schema is finalized.
8. New offline unit tests (pure `dateOnly`/`location()` logic) — depends on step 3.
9. Update the four DB integration test files per "Test Blast Radius" — depends on steps
   1, 4, 5.
10. Verification: `go build ./...`, `go vet ./...`, `go test ./...`,
    `openspec validate telemetry-dedupe-daily-snapshots --strict`.
