# charging — Module Agent Identity

Agent-Name: charging

## Doc-Pack (module)

No module-specific docs beyond the base pack. Every worker dispatched to this module reads
the base doc-pack declared in `CLAUDE.md` § Pipeline config:

- `CLAUDE.md`
- `ai/architecture.md`
- `ai/go-conventions.md`

No htmx, no template, no Templ conventions apply here — this module is backend-only (no HTML).
If a future task touches the gateway integration (wiring this module's ports into `cmd/web` Deps),
that work belongs in the `gateway` module and its agent, not here.

---

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
- **Mirrored Supercharger charge sessions** (`charge_sessions`, RM29 tier 6,
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

This module:

- Owns the `manual_charge_entries` table exclusively.
- Owns the `charge_sessions` table exclusively.
- Is isolated from the Tesla Fleet API — it imports no `internal/tesla` package, needs no OAuth
  scope, and wakes no car.
- Exposes CRUD (Writer) and read (Reader) ports for manual entries, and both a
  `SessionWriter` (mirroring) and a `SessionReader` (windowed per-vehicle reads) port for
  Supercharger sessions — all public Go interfaces. `charge_sessions` gained its reader in
  RM30 tier 1 (RM30-charging-add-session-read-port), superseding RM29 tier 6's design.md D9
  note that no consumer needed one.
- Computes no HTML, no templates, no htmx fragments — that is the gateway's job (Tier 2).

This module was renamed from `manualcharge` in RM29 tier 2, and gained `charge_sessions`
in RM29 tier 6 — a scope this module did not have when it was named `manualcharge`.

---

## Public Interface

```go
// Writer is the CRUD port. The gateway calls this after validating the user owns
// the vehicle (resolved via account.Service — outside this module's scope).
type Writer interface {
    Create(ctx context.Context, e Entry) (Entry, error)
    Update(ctx context.Context, e Entry) (Entry, error)
    Delete(ctx context.Context, accountID uuid.UUID, id uuid.UUID) error
}

// Reader is the read port shaped for dashboard access patterns.
// All methods return a non-nil empty slice when no entries exist.
// For ListEntriesByVehicle and ListEntriesByAccount, limit = 0 uses a server default (100).
type Reader interface {
    ListEntriesByVehicle(ctx context.Context, accountID uuid.UUID, teslaID int64, limit int) ([]Entry, error)
    ListEntriesByAccount(ctx context.Context, accountID uuid.UUID, limit int) ([]Entry, error)

    // ListEntriesByVehicleBetween returns entries for a specific vehicle within an
    // account whose charged_on falls within [from, to], inclusive of both bounds.
    // Ordered charged_on DESC, matching ListEntriesByVehicle. No limit parameter —
    // the [from, to] window itself bounds the result.
    ListEntriesByVehicleBetween(ctx context.Context, accountID uuid.UUID, teslaID int64, from, to time.Time) ([]Entry, error)
}

// Constructors — these are the only publicly exported factory functions.
func NewWriter(pool *pgxpool.Pool) Writer
func NewReader(pool *pgxpool.Pool) Reader
```

The gateway (Tier 2 `RM3-gateway-add-manual-charge-ui`) wires these interfaces into `cmd/web`
Deps and calls them from handlers. The gateway never imports `chargingdb` directly.

### The Supercharger mirror port (RM29 tier 6)

```go
// SessionMirror is the mirrorable subset of one Supercharger charge session: the
// identity, the time window, and the session facts internal/telemetry collects.
// It deliberately has NO battery-percentage fields.
type SessionMirror struct {
    AccountID uuid.UUID
    VIN       string
    TeslaID   *int64 // nil when the VIN is not a currently-registered vehicle
    SessionID int64

    ChargeStartDateTime time.Time
    ChargeStopDateTime  time.Time

    SiteLocationName string
    EnergyKWh        *float64 // nil when the session had no kWh fee
    TotalCost        *float64 // nil when the session had no fees
    Currency         *string  // nil when the session had no fees
    IsPaid           *bool    // nil when the session had no fees
}

// SessionWriter is the synchronization port called by the nightly orchestrator
// (internal/app since RM29 tier 7). Upsert-only: a session that disappears from Tesla's history
// stays mirrored.
type SessionWriter interface {
    // MirrorSessions upserts every supplied session under accountID, in one
    // transaction. Every entry's AccountID must equal accountID; a single
    // mis-scoped entry rejects the WHOLE call and writes nothing.
    MirrorSessions(ctx context.Context, accountID uuid.UUID, sessions []SessionMirror) error
}

// NewSessionWriter — the only publicly exported factory function for this port.
func NewSessionWriter(pool *pgxpool.Pool) SessionWriter
```

**`SessionMirror` has NO field for `start_battery_pct`, `end_battery_pct`,
`battery_pct_source`, `start_battery_pct_est` or `end_battery_pct_est` — by design, and
this is the single most important invariant in this file.** The five battery-percentage
columns are absent from `SessionMirror` and absent from the `MirrorChargeSession` SQL
query entirely (`db/query.sql`), so the nightly sync path has no field and no column to
bind one to even if a future edit tried — a human's verified reading is protected by a
**compile error**, not by a comment a reviewer has to notice (design.md D6). Do not
"complete" `SessionMirror` by adding these fields; the future verification UI (backlog
item 11) writes them directly, never through this port.

**The refresh set `MirrorChargeSession`'s `ON CONFLICT DO UPDATE SET` touches is
telemetry's own conflict set, minus `raw_data`** (a column `charge_sessions` does not
carry): `energy_kwh, total_cost, currency, is_paid, tesla_id, updated_at`. This is not five
independent judgement calls — it is one rule applied mechanically: *a mirrored column gets
exactly the write semantics its source column has* (design.md D1). Concretely,
`site_location_name` is **not** refreshed on a re-mirror, and the reason is purely
structural: `telemetry`'s own upsert never refreshes `site_location_name` either, so
neither does this one — **not** because a site name was judged unlikely to change. Apply
the same reasoning before adding any future mirrored column: check telemetry's conflict
clause first, and mirror it exactly.

The gateway and any other future caller of `SessionWriter` never import `chargingdb`
directly, exactly as for `Writer`/`Reader` above.

### The Supercharger session read port (RM30-charging-add-session-read-port)

```go
// Session is the full domain representation of one charge_sessions row: identity, the
// session's time window, the session facts internal/telemetry collects, and the five
// charging-owned battery-percentage verification/estimate columns. Read-only counterpart
// to SessionMirror — NOT built by widening it: SessionMirror stays deliberately
// percentage-free (RM29 design.md D6) so the nightly sync path has no field to bind a
// human-verified percentage to, even by mistake. Session and SessionMirror are
// distinct types for exactly that reason, even though Session's first thirteen fields
// duplicate SessionMirror's eleven (design.md D4).
type Session struct {
    ID        uuid.UUID
    AccountID uuid.UUID
    VIN       string
    TeslaID   *int64 // nil when the VIN is not a currently-registered vehicle
    SessionID int64

    ChargeStartDateTime time.Time
    ChargeStopDateTime  time.Time

    SiteLocationName string
    EnergyKWh        *float64 // nil when the session had no kWh fee
    TotalCost        *float64 // nil when the session had no fees
    Currency         *string  // nil when the session had no fees
    IsPaid           *bool    // nil when the session had no fees

    // Charging-owned verification channel — never written by the nightly sync.
    StartBatteryPct    *int
    EndBatteryPct      *int
    BatteryPctSource   *string
    StartBatteryPctEst *int
    EndBatteryPctEst   *int

    CreatedAt time.Time
    UpdatedAt time.Time
}

// SessionReader is the read port over charge_sessions. One method, shaped like
// Reader.ListEntriesByVehicleBetween's bounded-per-vehicle-window pattern but NOT
// identical to it: ascending order (not descending) and whole-UTC-calendar-day,
// half-open bound semantics (not exact-value BETWEEN) — see design.md D3/D5.
type SessionReader interface {
    // ListSessionsByVehicleBetween returns sessions for a specific vehicle within an
    // account whose ChargeStopDateTime falls within [from, to], to inclusive of its
    // entire UTC calendar day (to is translated to a half-open upper bound in Go).
    // Ordered ASCENDING by ChargeStopDateTime — the opposite of Reader's DESC order,
    // matching telemetry's own ordering for the identical access pattern; do not
    // "correct" this to DESC. No limit parameter; always a non-nil empty slice on no
    // match. A session whose TeslaID is nil is never returned, for any teslaID.
    ListSessionsByVehicleBetween(ctx context.Context, accountID uuid.UUID, teslaID int64, from, to time.Time) ([]Session, error)
}

// NewSessionReader — the only publicly exported factory function for this port.
func NewSessionReader(pool *pgxpool.Pool) SessionReader
```

The gateway and any other future caller of `SessionReader` never import `chargingdb`
directly, exactly as for `Writer`/`Reader`/`SessionWriter` above.

### The Supercharger session verification port (RM31-charging-add-session-verification-port)

```go
// SessionVerifier is the human-write port over charge_sessions' verification channel
// (design.md D9). It is a deliberately separate interface from SessionWriter, not a
// method added to it — SessionWriter's own doc comment states "The gateway never
// calls this," and adding a gateway-triggered, human-facing method to that interface
// would make that sentence false and would hand the nightly-orchestrator wiring path
// and the gateway's human-edit wiring path the same Go type to depend on: the "one
// fat interface, two callers with different trust models" shape the AI-efficiency
// "closed, small vocabularies" principle (CLAUDE.md §Non-negotiables) argues against.
type SessionVerifier interface {
    // VerifySession updates exactly three columns on one account-scoped
    // charge_sessions row — start_battery_pct, end_battery_pct, battery_pct_source —
    // plus updated_at. No other column is reachable through this method, including
    // start_battery_pct_est/end_battery_pct_est: the underlying query's SET clause
    // names only these three plus updated_at (design.md D1).
    //
    // battery_pct_source is always computed by this method, never supplied by the
    // caller: "user_verified" when either startBatteryPct or endBatteryPct is
    // non-nil, NULL when both are nil. The method takes no source parameter, so
    // "polled" cannot be written by any caller of this port (design.md D2/D7).
    //
    // A partial call (one percentage non-nil, the other nil) is legal. Every call
    // supplies both parameters' FINAL values, not a delta — a caller wanting to add
    // one field to an already-verified session must re-supply the other field's
    // current value (read via SessionReader) or it is overwritten to NULL
    // (design.md D6). Calling VerifySession(ctx, accountID, id, nil, nil) clears
    // both percentages AND battery_pct_source to NULL in the same statement
    // (design.md D7).
    //
    // Each non-nil percentage is validated to [0, 100] before the query runs; the
    // database's own SMALLINT CHECK is the backstop, not the error message
    // (design.md D3). No ordering between startBatteryPct and endBatteryPct is
    // enforced, deliberately (design.md D8).
    //
    // id/accountID scope the update exactly like Writer.Update: WHERE id = @id AND
    // account_id = @account_id. Zero rows matched — unknown id or wrong account,
    // indistinguishable — surfaces as an error wrapping pgx.ErrNoRows, with no
    // NotFound special-casing (design.md D5).
    VerifySession(ctx context.Context, accountID uuid.UUID, id uuid.UUID, startBatteryPct, endBatteryPct *int) (Session, error)
}

// NewSessionVerifier — the only publicly exported factory function for this port.
func NewSessionVerifier(pool *pgxpool.Pool) SessionVerifier
```

`SessionVerifier` is this module's third narrow, single-purpose interface over
`charge_sessions` (alongside `SessionWriter` and `SessionReader`) — one port per access
pattern (batch write, read, human write), not one port per table, consistent with how
`manual_charge_entries` already splits `Writer`/`Reader` (design.md D9). The gateway and
any other future caller never import `chargingdb` directly, exactly as for
`Writer`/`Reader`/`SessionWriter`/`SessionReader` above. This port ships with no caller
in this tier — `cmd/web`/`internal/gateway` wiring is deferred to
`RM31-gateway-add-session-battery-edit` (tier 4).

---

## Allowed Imports

This module may import:

- `context`, `time`, `math`, `errors`, and other Go standard library packages.
- `github.com/google/uuid` — for `uuid.UUID` primary and tenant keys.
- `github.com/jackc/pgx/v5` and `github.com/jackc/pgx/v5/pgxpool` — for DB connectivity.
- `github.com/jackc/pgx/v5/pgtype` — ONLY inside `service.go` and `session_writer.go` at
  the DB boundary. Never in public types, interfaces, `charging.go`, or any `_test.go`
  file. `session_writer.go` gained this allowance in RM29 tier 6 for the same reason
  `service.go` has it: it is the one file translating `charging.SessionMirror`'s plain Go
  `*T` fields into `chargingdb.MirrorChargeSessionParams`' nullable pgtype fields.
- `internal/charging/db` (package `chargingdb`) — ONLY inside `service.go` and
  `session_writer.go`. The generated package is module-private by convention; no other
  module imports it.

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

## Units convention

Platform-wide unit rule: `openspec/specs/unit-of-measure/spec.md` / `ai/go-conventions.md`
§Coding Rules — display units, unit-suffixed column names, converted once on write. This table
is **compliant**: `energy_added_kwh`, `start_battery_pct`, `end_battery_pct` already carry their
unit suffix. `price` is the platform's named monetary exemption — it takes no suffix and is
paired with the `currency` column instead of a unit.

## Data Ownership

`internal/charging` is the **sole owner** of two tables.

### `manual_charge_entries`

- No other module may read or write this table directly (ai/architecture.md §2).
- All access goes through the `Writer` and `Reader` public Go interfaces.
- The migration file `internal/charging/db/migrations/20260718000001_add_manual_charge_entries.sql`
  is the single schema source of truth (no separate `schema.sql` — ai/go-conventions.md §persistence).
- sqlc generates `package chargingdb` into `internal/charging/db/` from `query.sql`
  against the migration directory. Only `service.go` and `session_writer.go` (inside
  this module) may import it.

### `charge_sessions` (RM29 tier 6, RM29-charging-add-charge-sessions)

- No other module may read or write this table directly. Access goes through the
  `SessionWriter` port (write) and, since RM30-charging-add-session-read-port, the
  `SessionReader` port (read) — see §Public Interface above.
- The migration file
  `internal/charging/db/migrations/20260823000001_add_charge_sessions.sql` is the single
  schema source of truth, including its one-time backfill of every Supercharger session
  already collected (guarded so it is a no-op on a `charging`-only database — design.md
  D8a).
- `internal/telemetry.supercharger_sessions` **keeps its own copy** of all five
  battery-percentage columns until a separate, deferred contract change drops them
  (design.md D9 spells out that change's six parts — it is not a one-line `ALTER`).
  Until then, `start_battery_pct` / `end_battery_pct` / `battery_pct_source` /
  `start_battery_pct_est` / `end_battery_pct_est` genuinely exist in two tables across
  the module boundary, and only `charging.charge_sessions`'s copy is ever written to by
  anything in this repository (telemetry's copy has no writer at all — see
  `internal/telemetry`'s own docs).
- Column-by-column:
  - `account_id`, `vin`, `session_id`, `charge_start_date_time`, `charge_stop_date_time`,
    `site_location_name` — mirrored, **write-once**: telemetry never refreshes these
    either, so this table doesn't (design.md D1's rule).
  - `tesla_id`, `energy_kwh`, `total_cost`, `currency`, `is_paid` — mirrored,
    **refreshed on every nightly pass** (telemetry's own `ON CONFLICT DO UPDATE SET`
    refreshes them too — fees settle, invoices finalize).
  - `start_battery_pct`, `end_battery_pct`, `battery_pct_source` — **charging-owned**,
    never mirrored, never written by the nightly sync (see §Public Interface above for
    why that is a compile error, not a discipline). Since
    RM31-charging-add-session-verification-port, these three (and only these three) are
    writable through `SessionVerifier.VerifySession` — a human-triggered write, never
    the nightly sync. `battery_pct_source` is always computed by that port, never
    supplied by a caller (design.md D2/D7 of that change).
  - `start_battery_pct_est`, `end_battery_pct_est` — **charging-owned**, never mirrored,
    and still unwritable by any code path in this repository. Roadmap Decision 3
    (RM31): no estimator exists yet, so these stay permanently `NULL` until one is
    built — `SessionVerifier`'s query structurally cannot reach them either (design.md
    D1 of that change).
  - Deliberately **not** carried, and the list is closed: `country_code`,
    `unlatch_date_time`, `billing_type`, `vehicle_make_type`, `raw_data` (design.md D1).
- sqlc generates the `ChargeSession` model and the `MirrorChargeSession`,
  `ListSessionsByVehicleBetween`, and `VerifyChargeSession` queries into the same
  `chargingdb` package as `manual_charge_entries`'s queries. Only `session_writer.go`
  may call `MirrorChargeSession`, only `session_reader.go` may call
  `ListSessionsByVehicleBetween`, and only `session_verifier.go` may call
  `VerifyChargeSession` (inside this module).

---

## Testing Notes

- **Unit tests** (`charging_test.go`): test derived value-receiver methods (`CostPerKWh`,
  `BatteryDelta`, `SessionDuration`) with no DB and no Tesla API. Run offline as part of
  `go test ./...`.
- **Integration tests** (`db_integration_test.go`, `db_session_integration_test.go`,
  `db_backfill_integration_test.go`, `db_session_reader_integration_test.go`,
  `db_session_verifier_integration_test.go`): cover full CRUD round-trips, ordering
  guarantees, multi-tenant isolation, CHECK constraint enforcement, the Supercharger
  session mirror (`SessionWriter.MirrorSessions`), the one-time backfill, the
  `charge_sessions` read port (`SessionReader.ListSessionsByVehicleBetween`,
  RM30-charging-add-session-read-port), and — since
  RM31-charging-add-session-verification-port — the `charge_sessions` verification
  write port (`SessionVerifier.VerifySession`, `db_session_verifier_integration_test.go`,
  design.md Test Contract T1-T9). Reads still assert only against `charging.Session`
  domain fields or direct SQL column values — never `pgtype`, in this file or any other.
  The test database is provisioned by `testdb_test.go`:
    - When `DATABASE_URL` is set, that managed Postgres is used (CI with a service container,
      or a local DB you've already provisioned).
    - Otherwise `TestMain` starts a disposable `postgres:16-alpine` container via
      `testcontainers-go` and shares one `*pgxpool.Pool` across the whole package. No
      `createdb`/`make migrate-up` step is required; `make check`/`go test ./...` runs the
      integration tests green with zero manual DB setup as long as Docker is running locally.
    - **Since RM29 tier 6, TWO migration DIRECTORIES are applied, in this order:**
      `../telemetry/db/migrations` first, then this module's own `db/migrations` second, via
      `testdb.ProvisionDirs` (not the single-directory `testdb.Provision` this package used
      before). **Why:** the `charge_sessions` migration ships a backfill that reads
      `telemetry.supercharger_sessions`, and `db_backfill_integration_test.go` seeds that
      table and needs it to already exist before the backfill statement runs. This is a
      **path dependency on a migration directory, not a Go import** — no `_test.go` file in
      this package imports `internal/telemetry` (see §Allowed Imports). Same pattern
      `internal/analytics` already uses (`ai/go-conventions.md` §Testing: "more than one
      module's tables → `ProvisionDirs`").
    - Migrations are applied programmatically via the `github.com/pressly/goose/v3` Go API;
      `goose.NewProvider` records applied versions in `goose_db_version`, so re-running against
      a managed DB (`DATABASE_URL` set) is a no-op.
    - Production impact is NONE: the testcontainers/goose imports live only in `_test.go`
      files and are never compiled into the deployed binary; no Docker daemon is required in
      production.
- **The backfill test extracts its statement from the shipped migration at runtime**
  (`db_backfill_integration_test.go`), slicing the file read through the package's existing
  `//go:embed db/migrations/*.sql` between the `-- BACKFILL-BEGIN` / `-- BACKFILL-END`
  sentinel comments, rather than duplicating the SQL as a Go const — so the test can never
  drift from what actually ships to production (design.md D8c).
- **No Tesla API call fires in any test** — this module has no Fleet API dependency; this
  invariant is structural (no import of `internal/tesla`), not just disciplinary.
- The `tesla-exploration` exception (CLAUDE.md) does NOT apply here. Tests for this module are
  welcome and required (no paid-API risk).
- `pgtype` must not appear in any test helper or assertion — test against `charging.Entry` /
  `charging.SessionMirror` / `charging.Session` domain fields and raw SQL column values
  only. `db_session_integration_test.go` (write-path tests, predating the reader) reads
  rows back with direct SQL, scanning nullable columns into plain Go `*T` fields (pgx v5
  supports NULL-into-pointer-to-pointer scanning natively) — never into a
  `chargingdb.ChargeSession` (which is all `pgtype`).
  `db_session_reader_integration_test.go` (RM30-charging-add-session-read-port) instead
  asserts against `SessionReader.ListSessionsByVehicleBetween`'s returned
  `charging.Session` values directly — the port's own domain mapping already keeps
  `pgtype` out, so no direct-SQL read-back is needed there.
  `db_session_verifier_integration_test.go` (RM31-charging-add-session-verification-port)
  asserts against `SessionVerifier.VerifySession`'s returned `charging.Session` for the
  columns it changes, and reuses `db_session_integration_test.go`'s existing
  `fetchChargeSession` direct-SQL helper (plain Go `*T` fields, never `pgtype`) for the
  structural bit-identical-column proof (T8).
