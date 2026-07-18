# manualcharge — Module Agent Identity

Agent-Name: manualcharge

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

`internal/manualcharge` is the domain module for **user-asserted charge entries**: home/work/
third-party charging sessions that Tesla's Fleet API cannot attribute to a specific vehicle.
Users manually log the date, energy added (kWh), cost, and optional metadata (battery before/
after, timing, charging type, location). The module stores and retrieves these entries, enforces
multi-tenant data isolation, and computes derived values (cost-per-kWh, battery delta, session
duration) on read as value-receiver methods on `manualcharge.Entry`.

This module:

- Owns the `manual_charge_entries` table exclusively.
- Is isolated from the Tesla Fleet API — it imports no `internal/tesla` package, needs no OAuth
  scope, and wakes no car.
- Exposes CRUD (Writer) and read (Reader) ports as public Go interfaces.
- Computes no HTML, no templates, no htmx fragments — that is the gateway's job (Tier 2).

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
// Both methods return a non-nil empty slice when no entries exist.
// limit = 0 uses a server default (100).
type Reader interface {
    ListEntriesByVehicle(ctx context.Context, accountID uuid.UUID, teslaID int64, limit int) ([]Entry, error)
    ListEntriesByAccount(ctx context.Context, accountID uuid.UUID, limit int) ([]Entry, error)
}

// Constructors — these are the only publicly exported factory functions.
func NewWriter(pool *pgxpool.Pool) Writer
func NewReader(pool *pgxpool.Pool) Reader
```

The gateway (Tier 2 `RM3-gateway-add-manual-charge-ui`) wires these interfaces into `cmd/web`
Deps and calls them from handlers. The gateway never imports `manualchargedb` directly.

---

## Allowed Imports

This module may import:

- `context`, `time`, `math`, `errors`, and other Go standard library packages.
- `github.com/google/uuid` — for `uuid.UUID` primary and tenant keys.
- `github.com/jackc/pgx/v5` and `github.com/jackc/pgx/v5/pgxpool` — for DB connectivity.
- `github.com/jackc/pgx/v5/pgtype` — ONLY inside `service.go` (and `mapping.go` if used) at
  the DB boundary. Never in public types, interfaces, or `manualcharge.go`.
- `internal/manualcharge/db` (package `manualchargedb`) — ONLY inside `service.go`. The
  generated package is module-private by convention; no other module imports it.

This module MUST NOT import:

- `internal/tesla` — no Fleet API, no Tesla credentials, no VehicleService.
- `internal/account` — no token resolution, no OwnedVehicle types.
- `internal/telemetry` — no snapshot or Supercharger types.
- `internal/gateway` — no HTML, no Templ, no handlers.
- Any other module's `db` sub-package.
- `html/template`, `templ`, or any rendering library.

---

## Data Ownership

`internal/manualcharge` is the **sole owner** of the `manual_charge_entries` table.

- No other module may read or write this table directly (ai/architecture.md §2).
- All access goes through the `Writer` and `Reader` public Go interfaces.
- The migration file `internal/manualcharge/db/migrations/20260718000001_add_manual_charge_entries.sql`
  is the single schema source of truth (no separate `schema.sql` — ai/go-conventions.md §persistence).
- sqlc generates `package manualchargedb` into `internal/manualcharge/db/` from `query.sql`
  against the migration directory. Only `service.go` (inside this module) may import it.

---

## Testing Notes

- **Unit tests** (`manualcharge_test.go`): test derived value-receiver methods (`CostPerKWh`,
  `BatteryDelta`, `SessionDuration`) with no DB and no Tesla API. Run offline as part of
  `go test ./...`.
- **Integration tests** (`db_integration_test.go`): DATABASE_URL-gated — call `t.Skip` when
  `DATABASE_URL` is unset so `go test ./...` remains green without a database. Cover full CRUD
  round-trips, ordering guarantees, multi-tenant isolation, and CHECK constraint enforcement.
- **No Tesla API call fires in any test** — this module has no Fleet API dependency; this
  invariant is structural (no import of `internal/tesla`), not just disciplinary.
- The `tesla-exploration` exception (CLAUDE.md) does NOT apply here. Tests for this module are
  welcome and required (no paid-API risk).
- `pgtype` must not appear in any test helper or assertion — test against `manualcharge.Entry`
  domain fields only.
