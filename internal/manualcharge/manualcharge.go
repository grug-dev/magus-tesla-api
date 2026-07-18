// Package manualcharge is a new isolated domain module for user-asserted charge
// entries: home/work/third-party charging sessions that Tesla's Fleet API cannot
// capture (no Fleet API call, no OAuth scope, no vehicle wake). Users manually log
// the date, energy added (kWh), cost, and optional metadata (battery before/after,
// timing, charging type, location). The module persists and retrieves these entries,
// enforces multi-tenant data isolation, and computes derived values (cost-per-kWh,
// battery delta, session duration) on read as value-receiver methods on Entry.
//
// Public ports are Writer (Create/Update/Delete) and Reader (list by vehicle / by
// account). No HTML, no Templ, no Tesla adapter — this module is backend-only.
// Constructors (NewWriter / NewReader) are declared here; their bodies live in
// service.go where the manualchargedb generated package may be referenced.
package manualcharge

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Entry is our domain model for one user-asserted charge session (no vendor suffix —
// this is our own type, safe to build logic on, distinct from any external-API DTO;
// see ai/architecture.md §6). Optional fields use *T: nil means the user did not
// supply the value and it is stored as NULL in the database.
//
// pgtype is confined to the DB boundary inside service.go — it never appears here.
// Timestamps map to time.Time; DATE maps to time.Time (midnight UTC).
type Entry struct {
	ID        uuid.UUID
	AccountID uuid.UUID
	TeslaID   int64
	VIN       string

	// Required fields — user must supply on Create.
	ChargedOn       time.Time // DATE column: midnight UTC of the charge day
	EnergyAddedKWh  float64   // kWh added; NUMERIC(6,2) in DB; must be > 0
	Price           float64   // cost in Currency; NUMERIC(14,2) in DB; must be >= 0
	Currency        string    // ISO 4217 code; defaults to 'COP' in DB

	// Optional fields — nil when not supplied by the user (NULL in DB).
	StartedAt       *time.Time // TIMESTAMPTZ: exact session start, when known
	EndedAt         *time.Time // TIMESTAMPTZ: exact session end, when known
	StartBatteryPct *int       // 0–100 inclusive; SMALLINT CHECK in DB
	EndBatteryPct   *int       // 0–100 inclusive; SMALLINT CHECK in DB
	ChargingType    *string    // 'AC' or 'DC'; TEXT CHECK in DB
	LocationKind    *string    // 'HOME', 'WORK', or 'OTHER'; TEXT CHECK in DB
	LocationLabel   *string    // free text, especially useful for 'OTHER'
	Notes           *string    // any user comment

	CreatedAt time.Time
	UpdatedAt time.Time
}

// CostPerKWh returns the effective cost per kilowatt-hour for this entry:
// Price / EnergyAddedKWh. Returns nil when EnergyAddedKWh is zero (defensive
// nil-guard; the DB CHECK constraint prevents zero, but this method is nil-safe
// by convention). Derived on read, never stored (design D2j).
func (e Entry) CostPerKWh() *float64 {
	if e.EnergyAddedKWh == 0 {
		return nil
	}
	v := e.Price / e.EnergyAddedKWh
	return &v
}

// BatteryDelta returns the net change in battery percentage over the session:
// EndBatteryPct - StartBatteryPct. Returns nil when either field is nil (i.e.
// the user did not supply both start and end battery readings). Derived on read,
// never stored (design D2j). No Km()/Kmh() companions — this is a dimensionless
// percentage, not a distance or speed (ai/go-conventions.md).
func (e Entry) BatteryDelta() *int {
	if e.StartBatteryPct == nil || e.EndBatteryPct == nil {
		return nil
	}
	v := *e.EndBatteryPct - *e.StartBatteryPct
	return &v
}

// SessionDuration returns the elapsed time of the charging session: EndedAt - StartedAt.
// Returns nil when either StartedAt or EndedAt is nil (i.e. the user did not supply
// both timestamps). Derived on read, never stored (design D2j). No Km()/Kmh()
// companions — this is a time.Duration, not a distance or speed (ai/go-conventions.md).
func (e Entry) SessionDuration() *time.Duration {
	if e.StartedAt == nil || e.EndedAt == nil {
		return nil
	}
	d := e.EndedAt.Sub(*e.StartedAt)
	return &d
}

// Writer is the full CRUD port for manual charge entries. The gateway calls this after
// validating that the user owns the vehicle (resolved via account.Service — outside
// this module's scope). Create and Update return the stored Entry (with server-assigned
// id, created_at, updated_at) so the gateway can display the result without a second
// round-trip. Delete takes accountID as a required argument so the SQL WHERE clause
// always scopes to the caller's own account — a user cannot delete another tenant's
// entry even with a valid UUID (design D4).
type Writer interface {
	Create(ctx context.Context, e Entry) (Entry, error)
	Update(ctx context.Context, e Entry) (Entry, error)
	Delete(ctx context.Context, accountID uuid.UUID, id uuid.UUID) error
}

// Reader is the read port shaped for dashboard access patterns. Both methods return a
// non-nil empty slice when no entries exist. limit = 0 uses a server default (100).
// Gateway and other callers MUST NOT import manualchargedb directly — all read access
// goes through this interface (ai/architecture.md §2, design D5).
type Reader interface {
	ListEntriesByVehicle(ctx context.Context, accountID uuid.UUID, teslaID int64, limit int) ([]Entry, error)
	ListEntriesByAccount(ctx context.Context, accountID uuid.UUID, limit int) ([]Entry, error)
}

// NewWriter constructs a Writer backed by the given pgxpool. The implementation
// lives in service.go where the manualchargedb generated package is used.
// This is the only publicly exported constructor for the Writer port.
func NewWriter(pool *pgxpool.Pool) Writer {
	return newWriter(pool)
}

// NewReader constructs a Reader backed by the given pgxpool. The implementation
// lives in service.go where the manualchargedb generated package is used.
// This is the only publicly exported constructor for the Reader port.
func NewReader(pool *pgxpool.Pool) Reader {
	return newReader(pool)
}
