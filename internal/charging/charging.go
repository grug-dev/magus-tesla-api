// Package charging is a new isolated domain module for user-asserted charge
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
// service.go where the chargingdb generated package may be referenced.
package charging

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
	ChargedOn      time.Time // DATE column: midnight UTC of the charge day
	EnergyAddedKWh float64   // kWh added; NUMERIC(6,2) in DB; must be > 0
	Price          float64   // cost in Currency; NUMERIC(14,2) in DB; must be >= 0
	Currency       string    // ISO 4217 code; defaults to 'COP' in DB

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

// Reader is the read port shaped for dashboard access patterns. All methods return a
// non-nil empty slice when no entries exist. For ListEntriesByVehicle and
// ListEntriesByAccount, limit = 0 uses a server default (100).
// Gateway and other callers MUST NOT import chargingdb directly — all read access
// goes through this interface (ai/architecture.md §2, design D5).
type Reader interface {
	ListEntriesByVehicle(ctx context.Context, accountID uuid.UUID, teslaID int64, limit int) ([]Entry, error)
	ListEntriesByAccount(ctx context.Context, accountID uuid.UUID, limit int) ([]Entry, error)

	// ListEntriesByVehicleBetween returns entries for a specific vehicle within an
	// account whose charged_on falls within [from, to], inclusive of both bounds
	// (design D5, roadmap D9/D12). Ordered charged_on DESC, matching
	// ListEntriesByVehicle (design D2). Always returns a non-nil empty slice when no
	// rows match (design D4). No limit parameter (design D1, roadmap D9) — the
	// [from, to] window itself bounds the result.
	ListEntriesByVehicleBetween(ctx context.Context, accountID uuid.UUID, teslaID int64, from, to time.Time) ([]Entry, error)

	// ListEntriesByVehicleUpdatedSince returns entries for a specific vehicle within
	// an account whose updated_at is at or after since, inclusive. Ordered
	// charged_on DESC, matching ListEntriesByVehicle and ListEntriesByVehicleBetween.
	// Always returns a non-nil empty slice when no rows match. No limit parameter —
	// the since bound itself limits the result. This port exists for the analytics
	// module's incremental recompute watermark: manual_charge_entries is the one
	// source a user can edit at an arbitrary hour (rather than only at the nightly
	// poll), which is why it gets its own updated-since cursor read
	// (RM29-analytics-add-vehicle-metrics design D3,
	// specs/manual-charge-log/spec.md "List entries by vehicle updated since a given
	// instant").
	ListEntriesByVehicleUpdatedSince(ctx context.Context, accountID uuid.UUID, teslaID int64, since time.Time) ([]Entry, error)
}

// NewWriter constructs a Writer backed by the given pgxpool. The implementation
// lives in service.go where the chargingdb generated package is used.
// This is the only publicly exported constructor for the Writer port.
func NewWriter(pool *pgxpool.Pool) Writer {
	return newWriter(pool)
}

// NewReader constructs a Reader backed by the given pgxpool. The implementation
// lives in service.go where the chargingdb generated package is used.
// This is the only publicly exported constructor for the Reader port.
func NewReader(pool *pgxpool.Pool) Reader {
	return newReader(pool)
}

// SessionMirror is the mirrorable subset of one Supercharger charge session: the
// identity, the time window, and the session facts internal/telemetry collects.
// It deliberately has NO battery-percentage fields — see SessionWriter. This is not
// an oversight: it makes "a nightly poll erases a human's verified reading" a
// compile error rather than a comment a reviewer has to notice (design.md D6).
//
// Field names mirror telemetry.SuperchargerSession's, which mirror the column
// names, so the whole path stays a literal copy (design.md D1).
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
// (cmd/poller today; internal/app after RM29 tier 7). Upsert-only: a session that
// disappears from Tesla's history stays mirrored.
type SessionWriter interface {
	// MirrorSessions upserts every supplied session under accountID, in one
	// transaction. Every entry's AccountID must equal accountID; a single
	// mis-scoped entry rejects the WHOLE call and writes nothing.
	MirrorSessions(ctx context.Context, accountID uuid.UUID, sessions []SessionMirror) error
}

// NewSessionWriter constructs a SessionWriter backed by the given pgxpool. The
// implementation lives in session_writer.go where the chargingdb generated
// package is used. This is the only publicly exported constructor for the
// SessionWriter port.
func NewSessionWriter(pool *pgxpool.Pool) SessionWriter {
	return newSessionWriter(pool)
}

// Session is the full domain representation of one charge_sessions row: identity, the
// session's time window, the session facts internal/telemetry collects, and the five
// charging-owned battery-percentage verification/estimate columns. Read-only counterpart
// to SessionMirror — NOT built by adding fields to it.
//
// SessionMirror stays deliberately percentage-free (RM29 design.md D6): a nightly sync
// that took a Session instead of a SessionMirror would have a field to bind a
// human-verified percentage to, defeating the compile-time protection that is RM29 tier
// 6's central invariant. Session and SessionMirror are separate types for exactly that
// reason, even though Session's first thirteen fields duplicate SessionMirror's eleven
// (RM30-charging-add-session-read-port design.md D4).
//
// Nineteen fields, one per charge_sessions column. Field names/types follow this
// module's existing conventions exactly: *T for every nullable column (matching Entry's
// pattern), time.Time for every TIMESTAMPTZ, int64/*int64 for BIGINT/nullable BIGINT,
// *int for nullable SMALLINT (matching Entry.StartBatteryPct's identical type), *string
// for nullable TEXT. No pgtype anywhere in this type (ai/architecture.md §2, RM29 D6's
// own rule applied to the read side).
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

	// Charging-owned verification channel (RM29 design.md D1/D5/D6). Never written by
	// the nightly sync — SessionWriter has no field for any of these five.
	StartBatteryPct    *int    // 0-100 inclusive; nil = nothing recorded
	EndBatteryPct      *int    // 0-100 inclusive; nil = nothing recorded
	BatteryPctSource   *string // "user_verified" or "polled"; nil iff both percentages are nil
	StartBatteryPctEst *int    // frozen snapshot at verification time; nil = nothing recorded
	EndBatteryPctEst   *int    // frozen snapshot at verification time; nil = nothing recorded

	CreatedAt time.Time
	UpdatedAt time.Time
}

// SessionReader is the read port over charge_sessions (RM30-charging-add-session-read-port
// design.md D3/D5/D6/D7; widened by RM31-charging-add-session-read-ports). The three
// methods on this interface do NOT share a single sort-direction convention — sort
// direction is chosen per query against the shared idx_charge_sessions_vehicle_stop
// index, not as a port-family rule (RM31 design.md D3, Context fact 3). Do not "fix" one
// method's order to match another.
type SessionReader interface {
	// ListSessionsByVehicleBetween returns charge sessions for a specific vehicle
	// within an account whose ChargeStopDateTime falls within the window [from, to].
	//
	// from and to are whole UTC calendar days, to inclusive of its entire day —
	// mirroring telemetry.SuperchargerSessionsByVehicleBetween's end.AddDate(0,0,1)/
	// half-open contract exactly (design.md D5, revised), NOT
	// ListEntriesByVehicleBetween's exact-value BETWEEN semantics: to is translated to
	// a half-open upper bound (to+1 calendar day) before the database sees it, so every
	// session that stopped later on the to calendar day is still included.
	//
	// Results are ordered ASCENDING by ChargeStopDateTime (oldest first), matching
	// telemetry's own ordering for the identical access pattern (design.md D3,
	// strengthened). This is DELIBERATELY THE OPPOSITE of Reader's DESC order — the two
	// `…Between` methods on this module do not share a sort-direction convention,
	// because sort direction here is a property of each table's own index, not a
	// port-family rule. Do NOT "fix" this to DESC to match Reader; doing so would force
	// a sort step on every call.
	//
	// No limit parameter — the [from, to] window itself bounds the result. Always
	// returns a non-nil empty slice when no rows match.
	//
	// A session whose TeslaID is nil (the VIN is not a currently-registered vehicle) is
	// NEVER returned by this method for any teslaID — SQL's NULL = value is neither
	// true nor false, so an orphaned session is definitionally outside a
	// vehicle-scoped read (design.md D6).
	ListSessionsByVehicleBetween(ctx context.Context, accountID uuid.UUID, teslaID int64, from, to time.Time) ([]Session, error)

	// ListSessionsByVehicleUpdatedSince returns every session for a specific vehicle
	// within an account whose updated_at is at or after since. Ordered ASCENDING by
	// ChargeStopDateTime — NOT by updated_at itself (RM31 design.md D1, Context fact 2:
	// this mirrors Reader.ListEntriesByVehicleUpdatedSince's index reasoning in this
	// module, not telemetry.SuperchargerReader's updated_at-ordering choice). No limit
	// parameter — since itself bounds the result. Always returns a non-nil empty slice
	// when no rows match. A session whose TeslaID is nil is never returned, for any
	// teslaID (design.md D1, D4).
	//
	// This is the mechanism by which a SessionVerifier.VerifySession edit becomes
	// visible to analytics.Recalculator.Reconcile: VerifySession sets updated_at =
	// now() and touches no other timestamp column, and ChargeStartDateTime/
	// ChargeStopDateTime are write-once, so updated_at is the only column that moves
	// when a human verifies a session — this method is the sole path by which that
	// verification reaches a cursor-driven re-derivation (design.md D1).
	ListSessionsByVehicleUpdatedSince(ctx context.Context, accountID uuid.UUID, teslaID int64, since time.Time) ([]Session, error)

	// ListSessionsByVehicle returns the limit most recent sessions for a specific
	// vehicle within an account, ordered DESCENDING by ChargeStopDateTime — the
	// opposite of ListSessionsByVehicleBetween's ASC. This divergence is deliberate
	// (design.md D3) and must not be "corrected" to match the sibling method: a "most
	// recent N" limit-bounded read needs newest-first by construction. limit <= 0 uses
	// the server default (defaultLimit, 100), mirroring Reader.ListEntriesByVehicle's
	// identical contract. Always returns a non-nil empty slice when no rows match. A
	// session whose TeslaID is nil is never returned, for any teslaID (design.md D3,
	// D4).
	ListSessionsByVehicle(ctx context.Context, accountID uuid.UUID, teslaID int64, limit int) ([]Session, error)
}

// NewSessionReader constructs a SessionReader backed by the given pgxpool. The
// implementation lives in session_reader.go where the chargingdb generated package is
// used. This is the only publicly exported constructor for the SessionReader port.
func NewSessionReader(pool *pgxpool.Pool) SessionReader {
	return newSessionReader(pool)
}

// SessionVerifier is the human-write port over charge_sessions' verification channel
// (RM31-charging-add-session-verification-port design.md D9). It is a deliberately
// separate interface from SessionWriter, not a method added to it: SessionWriter's own
// doc comment states "The gateway never calls this," and adding a gateway-triggered,
// human-facing method to that interface would make that sentence false and would hand
// the nightly-orchestrator wiring path and the gateway's human-edit wiring path the same
// Go type to depend on — exactly the "one fat interface, two callers with different
// trust models" shape the AI-efficiency "closed, small vocabularies" principle
// (CLAUDE.md §Non-negotiables) argues against (design.md D9).
type SessionVerifier interface {
	// VerifySession updates exactly three columns on one account-scoped charge_sessions
	// row — start_battery_pct, end_battery_pct, battery_pct_source — plus updated_at.
	// No other column is reachable through this method, including
	// start_battery_pct_est/end_battery_pct_est: the underlying query's SET clause
	// names only these three plus updated_at, so the two _est columns are structurally
	// unreachable, not merely undocumented as targets (design.md D1).
	//
	// battery_pct_source is always computed by this method, never supplied by the
	// caller: "user_verified" when either startBatteryPct or endBatteryPct is non-nil,
	// NULL when both are nil. The method takes no source parameter, so "polled" — a
	// documented future value for a measured-SOC path — cannot be written by any caller
	// of this port (design.md D2/D7).
	//
	// A partial call (one percentage non-nil, the other nil) is legal — a human
	// correcting a session mid-charge is a real, expected use. Every call supplies both
	// parameters' FINAL values, not a delta: this is not a partial-patch method, exactly
	// like Writer.Update requires every mutable Entry field on every call. A caller
	// wanting to add endBatteryPct to a session that already has startBatteryPct
	// verified must re-supply the existing startBatteryPct value (read via
	// SessionReader) alongside the new endBatteryPct, or that column is overwritten to
	// NULL (design.md D6). Calling VerifySession(ctx, accountID, id, nil, nil) clears
	// both percentages AND battery_pct_source to NULL in the same statement (design.md
	// D7).
	//
	// Each non-nil percentage is validated to [0, 100] before the query runs; the
	// database's own SMALLINT CHECK is the backstop, not the error message (design.md
	// D3).
	//
	// No ordering between startBatteryPct and endBatteryPct is enforced by this method —
	// deliberately, matching the table's own deliberate absence of such a CHECK
	// (design.md D8).
	//
	// id/accountID scope the update exactly like Writer.Update: WHERE id = @id AND
	// account_id = @account_id. Zero rows matched — whether id does not exist at all or
	// exists under a different account — surfaces as an error wrapping pgx.ErrNoRows,
	// with no distinction made between the two cases, exactly mirroring Writer.Update's
	// own not-found semantics (design.md D5).
	VerifySession(ctx context.Context, accountID uuid.UUID, id uuid.UUID, startBatteryPct, endBatteryPct *int) (Session, error)
}

// NewSessionVerifier constructs a SessionVerifier backed by the given pgxpool. The
// implementation lives in session_verifier.go where the chargingdb generated package is
// used. This is the only publicly exported constructor for the SessionVerifier port.
func NewSessionVerifier(pool *pgxpool.Pool) SessionVerifier {
	return newSessionVerifier(pool)
}
