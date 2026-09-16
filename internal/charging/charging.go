// Package charging is a new isolated domain module for user-asserted charge
// entries: home/work/third-party charging sessions that Tesla's Fleet API cannot
// capture (no Fleet API call, no OAuth scope, no vehicle wake). Users manually log
// the date, energy added (kWh), cost, and optional metadata (battery before/after,
// timing, charging type, location, odometer). Every entry carries a lifecycle
// Status (IN_PROGRESS or DONE, MAG-18/RM33), and energy may be omitted and derived
// on write from the pack capacity and the battery delta (design.md D3). A charge
// entry submitted as IN_PROGRESS that already carries every DONE-required fact is
// auto-promoted to DONE on both Create and Update (RM51 design.md D1), and a zero
// price records whether it is a confirmed real amount or an unconfirmed placeholder
// via PriceSource (RM51 design.md D2/D3). The module persists and retrieves these
// entries, enforces multi-tenant data isolation, and computes derived values
// (cost-per-kWh, battery delta, session duration) on read as value-receiver methods
// on Entry.
//
// Public ports are Writer (Create/Update/Delete) and Reader (list by vehicle / by
// account). No HTML, no Templ, no Tesla adapter — this module is backend-only.
// Constructors (NewWriter / NewReader) are declared here; their bodies live in
// service.go where the chargingdb generated package may be referenced.
package charging

import (
	"context"
	"time"

	"github.com/cristianpena/magus-tesla-api/internal/vehicleref"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Status is the lifecycle state of a manual charge entry (TEXT + CHECK in the DB,
// MAG-18/RM33 design.md D1/D5). The required-field set for storing an Entry is a
// function of its Status — see RequiredFieldsFor.
type Status string

const (
	// StatusInProgress is both the column's DEFAULT and what an empty Status
	// normalizes to (normalizeStatus, design.md D8) — a charge logged at
	// plug-in time, which may lack the end-of-session facts.
	StatusInProgress Status = "IN_PROGRESS"
	// StatusDone is a complete charge record. RequiredFieldsFor additionally
	// requires EndedAt and EndBatteryPct for this status. There is no status
	// transition rule — DONE -> IN_PROGRESS is permitted (design.md D5).
	StatusDone Status = "DONE"
)

// EnergySource is the provenance of Entry.EnergyAddedKWh: EnergySourceUser when
// the value came from the person, EnergySourceEstimated when this module derived
// it from the pack capacity and the battery delta on write (design.md D3/D4).
// ALWAYS COMPUTED BY internal/charging on Create/Update — a value set on the Entry
// passed to Writer is ignored and overwritten, the same shape
// supercharger_sessions.battery_pct_source already uses via SessionVerifier.VerifySession.
type EnergySource string

const (
	EnergySourceUser      EnergySource = "USER"
	EnergySourceEstimated EnergySource = "ESTIMATED"
)

// PriceSource is the provenance of Entry.Price: PriceSourceUser when the amount
// is known to be real (a positive price, or a caller-confirmed zero),
// PriceSourceUnconfirmed when a zero price has not been confirmed as a real
// free charge (RM51 design.md D2/D3). ALWAYS COMPUTED BY internal/charging on
// Create/Update -- a value set on the Entry passed to Writer is ignored and
// overwritten, the same shape EnergySource already uses.
type PriceSource string

const (
	PriceSourceUser        PriceSource = "USER"
	PriceSourceUnconfirmed PriceSource = "UNCONFIRMED"
)

// Field names one field of an Entry whose presence RequiredFieldsFor can evaluate.
// Its string value is the database COLUMN NAME, which is ALSO the gateway's form
// input name and its validation-error map key (handlers/external_charges.go) — so the
// gateway can map a Field straight onto an input with no translation table
// (design.md D5).
type Field string

const (
	FieldChargedOn     Field = "charged_on"
	FieldLocationKind  Field = "location_kind"
	FieldEndedAt       Field = "ended_at"
	FieldEndBatteryPct Field = "end_battery_pct"
)

// Entry is our domain model for one user-asserted charge session (no vendor suffix —
// this is our own type, safe to build logic on, distinct from any external-API DTO;
// see ai/architecture.md §6). Optional fields use *T: nil means the user did not
// supply the value and it is stored as NULL in the database. A submission that is
// IN_PROGRESS but already carries every DONE-required fact is auto-promoted to
// DONE on Create/Update (RM51 design.md D1), and the entry's price provenance
// (PriceSource) is always module-computed (RM51 design.md D3).
//
// pgtype is confined to the DB boundary inside service.go — it never appears here.
// Timestamps map to time.Time; DATE maps to time.Time (midnight UTC).
type Entry struct {
	ID uuid.UUID
	// CreatedByAccountID records which account typed this entry. It is
	// authorship only, recorded once on Create and never touched or checked
	// again: no read filters on it, and no write matches on it.
	CreatedByAccountID uuid.UUID
	// TeslaID is the vehicle this entry belongs to. On Create it is
	// caller-supplied and trusted -- the gateway already checked the vehicle
	// against the account before calling. On Update it is IGNORED: the
	// module overwrites it from the caller's vehicleref.Ref before anything
	// else runs, the same "module owns this value" rule EnergySource and
	// PriceSource already follow -- so a stale or mismatched caller value can
	// never leak into energy derivation or the stored row.
	TeslaID int64
	VIN     string

	// Status is this entry's lifecycle state. The required-field set for
	// Create/Update is a function of Status — see RequiredFieldsFor. An empty
	// Status normalizes to StatusInProgress (design.md D8).
	Status Status

	// Required fields — user must supply on Create.
	ChargedOn time.Time // DATE column: midnight UTC of the charge day
	Price     float64   // cost in Currency; NUMERIC(14,2) in DB; must be >= 0
	Currency  string    // ISO 4217 code; defaults to 'COP' in DB

	// PriceSource is the provenance of Price. ALWAYS COMPUTED BY THIS MODULE on
	// Create/Update -- a value set here is ignored and overwritten, the same
	// shape EnergySource already uses (design.md D3, RM51).
	PriceSource PriceSource

	// PriceConfirmed is a caller-supplied intent, read ONLY when Price == 0:
	// true means the caller confirms a zero price is a real free charge, so it
	// is recorded as PriceSourceUser instead of PriceSourceUnconfirmed. Ignored
	// when Price > 0 -- such a price is always confirmed regardless of this
	// field (design.md D5, RM51). NOT PERSISTED DIRECTLY: it drives PriceSource,
	// which is what gets written and read back, so a round-trip through Reader
	// always returns PriceConfirmed: false on every entry (design.md D3).
	PriceConfirmed bool

	// Optional fields — nil when not supplied by the user (NULL in DB).
	StartedAt       *time.Time // TIMESTAMPTZ: exact session start, when known
	EndedAt         *time.Time // TIMESTAMPTZ: exact session end, when known
	StartBatteryPct *int       // 0–100 inclusive; SMALLINT CHECK in DB
	EndBatteryPct   *int       // 0–100 inclusive; SMALLINT CHECK in DB
	ChargingType    *string    // 'AC' or 'DC'; TEXT CHECK in DB
	LocationKind    *string    // 'HOME', 'WORK', or 'OTHER'; TEXT CHECK in DB
	LocationLabel   *string    // free text, especially useful for 'OTHER'
	Notes           *string    // any user comment

	// EnergyAddedKWh is the energy added, in kWh; NUMERIC(6,2) in DB,
	// CHECK (> 0) when non-NULL (the CHECK evaluates NULL, not false, on a NULL
	// input, so it still rejects 0 and negatives while permitting NULL). nil
	// means not supplied and not derivable — an IN_PROGRESS entry legitimately
	// has no end-of-session facts, so no honest value exists yet (design.md D2).
	// When nil and both battery percentages are present with EndBatteryPct >
	// StartBatteryPct, Writer.Create/Update derives a value on write and sets
	// EnergySource to EnergySourceEstimated (design.md D3). Never a fabricated 0.
	EnergyAddedKWh *float64

	// EnergySource is the provenance of EnergyAddedKWh. ALWAYS COMPUTED BY THIS
	// MODULE on Create/Update — a value set here is ignored and overwritten,
	// the same way InferredCapacityKWhCalc below is ignored on write
	// (design.md D4).
	EnergySource EnergySource

	// OdometerKm is the odometer reading, in kilometres, observed AT this
	// charge event — an observation belonging to the event, not current
	// vehicle state, which is why it lives here and not on a vehicle table
	// (design.md D6). nil means not recorded.
	OdometerKm *int

	// InferredCapacityKWhCalc is the pack capacity in kWh implied by this entry
	// alone: EnergyAddedKWh / ((EndBatteryPct - StartBatteryPct) / 100), rounded to
	// 3 decimals (MAG-25, charging-add-inferred-capacity design.md D7). It is
	// COMPUTED BY THE DATABASE and READ-ONLY: this is a PostgreSQL
	// GENERATED ALWAYS AS (...) STORED column (design.md D2), so a value set here
	// on the struct passed to Writer.Create / Writer.Update is silently ignored —
	// exactly as ID, CreatedAt and UpdatedAt already are — and the database
	// rejects any direct write to the column with
	// `column "inferred_capacity_kwh_calc" can only be updated to DEFAULT`
	// (SQLSTATE 428C9). nil means this record's inputs did not support the
	// formula: a missing StartBatteryPct or EndBatteryPct, or EndBatteryPct not
	// strictly greater than StartBatteryPct (an equal delta is a division by
	// zero, a decreasing one a negative "capacity" — design.md D3). nil is not an
	// error.
	InferredCapacityKWhCalc *float64

	CreatedAt time.Time
	UpdatedAt time.Time
}

// CostPerKWh returns the effective cost per kilowatt-hour for this entry:
// Price / EnergyAddedKWh. Returns nil when EnergyAddedKWh is nil (not supplied
// and not derivable, design.md D2) or when it points at zero (defensive
// nil-guard surviving the *float64 change; the DB CHECK constraint prevents a
// stored zero, but this method stays nil-safe by convention). Derived on read,
// never stored (design D2j).
func (e Entry) CostPerKWh() *float64 {
	if e.EnergyAddedKWh == nil || *e.EnergyAddedKWh == 0 {
		return nil
	}
	v := e.Price / *e.EnergyAddedKWh
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

// Writer is the full CRUD port for manual charge entries. Create keeps the account
// argument on Entry.CreatedByAccountID -- authorship, not a guard: no existing row can
// collide with a new one, so nothing needs proving before a create. Update and Delete
// instead require a vehicleref.Ref, proving the caller's account owns the entry's OWN
// vehicle -- only internal/gateway's authorizeVehicle can build one. Update overwrites
// Entry.TeslaID from the Ref before doing anything else (see the field's own comment),
// so every downstream derivation reads the proven car, never the caller's value.
// Create and Update return the stored Entry (with server-assigned id, created_at,
// updated_at) so the gateway can display the result without a second round-trip. A Ref
// naming the wrong vehicle for a given id matches no row: Update returns an error
// wrapping pgx.ErrNoRows (from RETURNING * finding nothing), and Delete reports the
// same, from a zero row count.
type Writer interface {
	Create(ctx context.Context, e Entry) (Entry, error)
	Update(ctx context.Context, ref vehicleref.Ref, e Entry) (Entry, error)
	Delete(ctx context.Context, ref vehicleref.Ref, id uuid.UUID) error
}

// Reader is the read port shaped for dashboard access patterns. All methods return a
// non-nil empty slice when no entries exist. For ListEntriesByVehicle and
// ListEntriesByVehicles, limit = 0 uses a server default (100). Every read is
// car-wide: it returns entries typed by any account registered to the vehicle asked
// for, never filtered by who typed them.
// Gateway and other callers MUST NOT import chargingdb directly — all read access
// goes through this interface (ai/architecture.md §2, design D5).
type Reader interface {
	ListEntriesByVehicle(ctx context.Context, teslaID int64, limit int) ([]Entry, error)

	// ListEntriesByVehicles returns entries for a caller-supplied set of vehicles,
	// ordered charged_on DESC across the whole set, bounded by limit (0 = server
	// default). The caller supplies the set of vehicles it is entitled to see. An
	// empty or nil slice returns a non-nil empty result — it must never be read as
	// "no filter": a caller supplying no vehicle is entitled to no entry.
	ListEntriesByVehicles(ctx context.Context, teslaIDs []int64, limit int) ([]Entry, error)

	// ListEntriesByVehicleBetween returns entries for a specific vehicle whose
	// charged_on falls within [from, to], inclusive of both bounds. Ordered
	// charged_on DESC, matching ListEntriesByVehicle. Always returns a non-nil
	// empty slice when no rows match. There is no limit parameter: the
	// [from, to] window already bounds the result.
	ListEntriesByVehicleBetween(ctx context.Context, teslaID int64, from, to time.Time) ([]Entry, error)

	// ListEntriesByVehicleUpdatedSince returns entries for a specific vehicle whose
	// updated_at is at or after since, inclusive. Ordered charged_on DESC, matching
	// ListEntriesByVehicle and ListEntriesByVehicleBetween. Always returns a
	// non-nil empty slice when no rows match. No limit parameter — the since bound
	// itself limits the result. This port exists for the analytics module's
	// incremental recompute watermark: manual_charge_entries is the one source a
	// user can edit at an arbitrary hour (rather than only at the nightly poll),
	// which is why it gets its own updated-since cursor read (see
	// specs/manual-charge-log/spec.md "List entries by vehicle updated since a given
	// instant").
	ListEntriesByVehicleUpdatedSince(ctx context.Context, teslaID int64, since time.Time) ([]Entry, error)
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
// Field names mirror telemetry.SuperchargerHistory's, which mirror the column
// names, so the whole path stays a literal copy (design.md D1).
type SessionMirror struct {
	VIN       string
	TeslaID   int64 // a session is only ever mirrored for a registered vehicle
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
	// MirrorSessions upserts every supplied session, in one transaction.
	MirrorSessions(ctx context.Context, sessions []SessionMirror) error
}

// NewSessionWriter constructs a SessionWriter backed by the given pgxpool. The
// implementation lives in session_writer.go where the chargingdb generated
// package is used. This is the only publicly exported constructor for the
// SessionWriter port.
func NewSessionWriter(pool *pgxpool.Pool) SessionWriter {
	return newSessionWriter(pool)
}

// SessionStatus is the lifecycle status of one supercharger_sessions row's
// battery-percentage data (TEXT + CHECK in the DB, RM41-charging-add-session-status,
// MAG-45). ALWAYS COMPUTED by SessionVerifier.VerifySession on every call -- no port
// accepts a Session as input for this field, so there is no way for a caller to set
// it directly.
type SessionStatus string

const (
	// SessionStatusInProgress: at least one of start_battery_pct/end_battery_pct is
	// NULL. This is the status of a session nobody has recorded anything for, AND of
	// a session with only a start percentage recorded and no end percentage --
	// deliberately not a fourth state (design.md "State truth table"): this is what
	// keeps "sessions still in progress" a meaningful worklist for the gateway's own
	// follow-up recommendation (RM41 tier 5) even when a session is half-recorded.
	SessionStatusInProgress SessionStatus = "IN_PROGRESS"
	// SessionStatusDoneCalculated: both percentages are present, AND the
	// VerifySession call that produced this row's CURRENT start_battery_pct derived
	// it via derivedStartBatteryPct (capacity.go) rather than storing a
	// caller-supplied value. This is the one place in the schema that keeps a typed
	// percentage apart from a derived one -- battery_pct_source itself cannot
	// (MAG-36 design.md D1).
	SessionStatusDoneCalculated SessionStatus = "DONE_CALCULATED"
	// SessionStatusDone: both percentages are present, and the CURRENT
	// start_battery_pct was supplied directly by VerifySession's caller.
	SessionStatusDone SessionStatus = "DONE"
)

// Session is the full domain representation of one supercharger_sessions row: identity, the
// session's time window, the session facts internal/telemetry collects, and the three
// charging-owned battery-percentage verification columns, plus (since RM41 tier 4) a
// fourth charging-owned column, Status, computed from them. Read-only counterpart
// to SessionMirror — NOT built by adding fields to it.
//
// SessionMirror stays deliberately percentage-free (RM29 design.md D6): a nightly sync
// that took a Session instead of a SessionMirror would have a field to bind a
// human-verified percentage to, defeating the compile-time protection that is RM29 tier
// 6's central invariant. Session and SessionMirror are separate types for exactly that
// reason, even though Session's first thirteen fields duplicate SessionMirror's eleven
// (RM30-charging-add-session-read-port design.md D4).
//
// Eighteen fields, one per supercharger_sessions column. Field names/types follow this
// module's existing conventions exactly: *T for every nullable column (matching Entry's
// pattern), time.Time for every TIMESTAMPTZ, int64 for BIGINT NOT NULL, *int for
// nullable SMALLINT (matching Entry.StartBatteryPct's identical type), *string for
// nullable TEXT. No pgtype anywhere in this type: ai/architecture.md §2 keeps pgtype
// confined to the files that talk to the database directly, and this type is not one.
type Session struct {
	ID        uuid.UUID
	VIN       string
	TeslaID   int64
	SessionID int64

	ChargeStartDateTime time.Time
	ChargeStopDateTime  time.Time

	SiteLocationName string
	EnergyKWh        *float64 // nil when the session had no kWh fee
	TotalCost        *float64 // nil when the session had no fees
	Currency         *string  // nil when the session had no fees
	IsPaid           *bool    // nil when the session had no fees

	// Charging-owned verification channel (RM29 design.md D1/D5/D6). Never written by
	// the nightly sync — SessionWriter has no field for any of these three.
	StartBatteryPct  *int    // 0-100 inclusive; nil = nothing recorded
	EndBatteryPct    *int    // 0-100 inclusive; nil = nothing recorded
	BatteryPctSource *string // "user_verified" or "polled"; nil iff both percentages are nil

	// Status is this session's lifecycle status -- ALWAYS COMPUTED by
	// SessionVerifier.VerifySession, never settable through any port
	// (RM41-charging-add-session-status, MAG-45). See SessionStatus's own doc
	// comment for the three values and the rule that produces each.
	Status SessionStatus

	// InferredCapacityKWhCalc is the pack capacity in kWh implied by this session
	// alone: EnergyKWh / ((EndBatteryPct - StartBatteryPct) / 100), rounded to 3
	// decimals (MAG-25, charging-add-inferred-capacity design.md D7). It is
	// COMPUTED BY THE DATABASE and READ-ONLY: this is a PostgreSQL
	// GENERATED ALWAYS AS (...) STORED column (design.md D2), so a value set here
	// on a struct is never written by this module — no port takes a Session as
	// input — and the database rejects any direct write to the column with
	// `column "inferred_capacity_kwh_calc" can only be updated to DEFAULT`
	// (SQLSTATE 428C9). nil means this record's inputs did not support the
	// formula: a missing StartBatteryPct or EndBatteryPct, EndBatteryPct not
	// strictly greater than StartBatteryPct (design.md D3), or — for Session
	// only — no kWh fee at all (EnergyKWh == nil). The value recomputes both when
	// the nightly mirror (SessionWriter.MirrorSessions) refreshes EnergyKWh and
	// when SessionVerifier.VerifySession corrects the percentages, without either
	// write path naming this column (design.md D2). nil is not an error.
	InferredCapacityKWhCalc *float64

	CreatedAt time.Time
	UpdatedAt time.Time
}

// SessionReader is the read port over supercharger_sessions (RM30-charging-add-session-read-port
// design.md D3/D5/D6/D7). One method, unchanged by RM31-charging-add-session-read-ports:
// internal/gateway depends on exactly this interface (Deps.SuperchargerReader) and calls
// only ListSessionsByVehicleBetween, so this interface is NOT widened to add the two new
// analytics-facing read shapes — see SuperchargerSessionAnalyticsReader below, which embeds
// this interface instead (RM31 design.md D8). Do not add methods here; a prior wave of this
// same change did, and it broke internal/gateway's fakeSessionReader and failed
// `go vet ./...` repo-wide.
type SessionReader interface {
	// ListSessionsByVehicleBetween returns charge sessions for a specific vehicle
	// whose ChargeStopDateTime falls within the window [from, to].
	//
	// from and to are whole UTC calendar days, to inclusive of its entire day.
	// This is NOT ListEntriesByVehicleBetween's exact-value BETWEEN semantics: to is
	// translated to a half-open upper bound (to+1 calendar day) before the database
	// sees it, so every session that stopped later on the to calendar day is still
	// included.
	//
	// Results are ordered ASCENDING by ChargeStopDateTime (oldest first), matching
	// telemetry's own ordering for the identical access pattern. This is
	// DELIBERATELY THE OPPOSITE of Reader's DESC order — the two
	// `…Between` methods on this module do not share a sort-direction convention,
	// because sort direction here is a property of each table's own index, not a
	// port-family rule. Do NOT "fix" this to DESC to match Reader; doing so would force
	// a sort step on every call.
	//
	// No limit parameter — the [from, to] window itself bounds the result. Always
	// returns a non-nil empty slice when no rows match.
	ListSessionsByVehicleBetween(ctx context.Context, teslaID int64, from, to time.Time) ([]Session, error)
}

// NewSessionReader constructs a SessionReader backed by the given pgxpool. The
// implementation lives in session_reader.go where the chargingdb generated package is
// used. This is the only publicly exported constructor for the SessionReader port.
// Its signature and return type MUST NOT change — cmd/web wires this exact function into
// the gateway's Deps.SuperchargerReader (RM31 design.md D8).
func NewSessionReader(pool *pgxpool.Pool) SessionReader {
	return newSessionReader(pool)
}

// SuperchargerSessionAnalyticsReader is the read port over supercharger_sessions for
// internal/analytics (RM31-charging-add-session-read-ports design.md D8). It is a
// SEPARATE interface from SessionReader, not a widening of it, for the same "one fat
// interface, two callers with different needs" reason RM31 tier 1's D9 made
// SessionVerifier separate from SessionWriter: internal/gateway depends on SessionReader
// and calls only ListSessionsByVehicleBetween, so adding methods to SessionReader itself
// would force every implementer of that interface — including gateway's own test double,
// which has and needs no reason to know about analytics' two extra read shapes — to carry
// methods it never calls. internal/analytics needs all three shapes
// (ListSessionsByVehicleBetween, ListSessionsByVehicleUpdatedSince, ListSessionsByVehicle),
// which is why this interface EMBEDS SessionReader rather than restating its method.
//
// The three methods reachable through this interface do NOT share a single
// sort-direction convention — sort direction is chosen per query against the shared
// idx_supercharger_sessions_vehicle_stop index, not as a port-family rule (design.md D3,
// Context fact 3). Do not "fix" one method's order to match another.
//
// Naming: "Supercharger" was originally the owner's deliberate divergence from this
// module's Session* family (SessionReader, SessionWriter, SessionMirror,
// SessionVerifier), chosen for call-site readability in internal/analytics, where the
// surrounding code is about Supercharger sessions but the package qualifier is charging
// (RM31 design.md D8). RM39-charging-move-to-own-schema (D5b) renamed the underlying
// table itself from charge_sessions to supercharger_sessions, so this is no longer a
// divergence — it agrees with the table. D5b makes the table agree with a name the
// module already chose; it removes a divergence rather than creating one. Do not "tidy"
// this name into the Session* family — that would recreate the mismatch this change just
// resolved.
type SuperchargerSessionAnalyticsReader interface {
	SessionReader

	// ListSessionsByVehicleUpdatedSince returns every session for a specific vehicle
	// whose updated_at is at or after since. Ordered ASCENDING by ChargeStopDateTime —
	// NOT by updated_at itself: this mirrors Reader.ListEntriesByVehicleUpdatedSince's
	// index reasoning in this module, not telemetry.SuperchargerHistoryReader's
	// updated_at-ordering choice. No limit parameter — since itself bounds the result.
	// Always returns a non-nil empty slice when no rows match.
	//
	// This is the mechanism by which a SessionVerifier.VerifySession edit becomes
	// visible to analytics.Recalculator.Reconcile: VerifySession sets updated_at =
	// now() and touches no other timestamp column, and ChargeStartDateTime/
	// ChargeStopDateTime are write-once, so updated_at is the only column that moves
	// when a human verifies a session — this method is the sole path by which that
	// verification reaches a cursor-driven re-derivation (design.md D1).
	ListSessionsByVehicleUpdatedSince(ctx context.Context, teslaID int64, since time.Time) ([]Session, error)

	// ListSessionsByVehicle returns the limit most recent sessions for a specific
	// vehicle, ordered DESCENDING by ChargeStopDateTime — the opposite of
	// ListSessionsByVehicleBetween's ASC. This divergence is deliberate
	// and must not be "corrected" to match the sibling method: a "most recent N"
	// limit-bounded read needs newest-first by construction. limit <= 0 uses the
	// server default (defaultLimit, 100), mirroring Reader.ListEntriesByVehicle's
	// identical contract. Always returns a non-nil empty slice when no rows match.
	ListSessionsByVehicle(ctx context.Context, teslaID int64, limit int) ([]Session, error)
}

// NewSuperchargerSessionAnalyticsReader constructs a SuperchargerSessionAnalyticsReader
// backed by the given pgxpool, returning the same underlying *sessionReader
// NewSessionReader returns — one concrete type satisfies both interfaces, so
// session_reader.go's method implementations are not duplicated (design.md D8). This is
// the only publicly exported constructor for the SuperchargerSessionAnalyticsReader port.
func NewSuperchargerSessionAnalyticsReader(pool *pgxpool.Pool) SuperchargerSessionAnalyticsReader {
	return newSessionReader(pool)
}

// SessionVerifier is the human-write port over supercharger_sessions' verification channel
// (RM31-charging-add-session-verification-port design.md D9). It is a deliberately
// separate interface from SessionWriter, not a method added to it: SessionWriter's own
// doc comment states "The gateway never calls this," and adding a gateway-triggered,
// human-facing method to that interface would make that sentence false and would hand
// the nightly-orchestrator wiring path and the gateway's human-edit wiring path the same
// Go type to depend on — exactly the "one fat interface, two callers with different
// trust models" shape the AI-efficiency "closed, small vocabularies" principle
// (CLAUDE.md §Non-negotiables) argues against (design.md D9).
type SessionVerifier interface {
	// VerifySession updates exactly four columns on one vehicle-scoped
	// supercharger_sessions row — start_battery_pct, end_battery_pct,
	// battery_pct_source, status — plus updated_at. status was added later, alongside
	// the original three columns. No other column is reachable through this method:
	// the underlying query's SET clause names only these four plus updated_at, on
	// purpose, so a future edit that wanted to also touch another column would have
	// to add it to that clause explicitly, as a visible diff. status is ALWAYS
	// COMPUTED by this method from the same startToStore/endBatteryPct/
	// derivation-outcome values used to compute battery_pct_source — never accepted
	// as a parameter; VerifySession's own signature is unchanged by this addition.
	//
	// The two frozen estimate columns formerly named here as columns this
	// method could never reach were later dropped from the table entirely — there is
	// no longer a column to be unreachable from.
	//
	// battery_pct_source is always computed by this method, never supplied by the
	// caller: "user_verified" when either startBatteryPct or endBatteryPct is non-nil,
	// NULL when both are nil. The method takes no source parameter, so "polled" — a
	// documented future value for a measured-SOC path — cannot be written by any
	// caller of this port: there is no parameter to carry it.
	//
	// A partial call (one percentage non-nil, the other nil) is legal — a human
	// correcting a session mid-charge is a real, expected use. Every call supplies both
	// parameters' FINAL values, not a delta: this is not a partial-patch method, exactly
	// like Writer.Update requires every mutable Entry field on every call. A caller
	// wanting to add endBatteryPct to a session that already has startBatteryPct
	// verified must re-supply the existing startBatteryPct value (read via
	// SessionReader) alongside the new endBatteryPct, or that column is overwritten to
	// NULL. Calling VerifySession(ctx, ref, id, nil, nil) clears both percentages AND
	// battery_pct_source to NULL in the same statement: the table's own CHECK allows a
	// null source only when both percentages are null, so this method must clear the
	// source itself, in one statement, or leave the row violating that CHECK.
	//
	// Each non-nil percentage is validated to [0, 100] before the query runs; the
	// database's own SMALLINT CHECK is the backstop, not the error message: this way
	// a caller sees a clear Go error naming the field and value, not a raw Postgres
	// CHECK-violation error.
	//
	// No ordering between startBatteryPct and endBatteryPct is enforced by this
	// method — deliberately, matching the table's own deliberate absence of such a
	// CHECK. A human fixing a mistake corrects one field at a time across two calls,
	// so an intermediate "inverted" pair is a normal step in that workflow, not
	// invalid data. Tesla's own session data does not guarantee the end reading is
	// higher than the start reading either.
	//
	// Derivation of a missing start percentage: when the caller submits
	// startBatteryPct == nil and endBatteryPct != nil, and the session row's
	// energy_kwh is non-NULL, and the algebraic result — start = end -
	// energy_kwh/packCapacityKWh*100, rounded half away from zero (matching both Go's
	// math.Round and PostgreSQL's own numeric rounding mode) — lands in [0, 100],
	// this method now stores the DERIVED value instead of NULL. A caller-supplied
	// startBatteryPct is NEVER recomputed or overridden, under any condition — this
	// is unconditional, not merely the common case, so a person's own typed reading
	// is never silently replaced by a computed guess. When energy_kwh is SQL NULL —
	// a session can legitimately have no kWh fee recorded at all — or the derived
	// result falls outside [0, 100] — the assumed pack capacity does not match every
	// real vehicle, so the maths can legitimately miss the valid range —
	// start_battery_pct is left NULL, silently — no error, no clamp. Every other call
	// shape (both percentages supplied, only start supplied, both nil, an
	// out-of-range caller-supplied value) is unaffected. The battery_pct_source
	// computation above is otherwise unaffected by this derivation: still
	// batteryPctSourceUserVerified when either the (possibly derived) start or the
	// end percentage is non-nil — a derived value is stored under the same
	// provenance as a typed one, not a new source value.
	//
	// id/ref scope the update: WHERE id = @id AND tesla_id = @tesla_id, using
	// ref.TeslaID(). This is the ONLY tenant boundary left on the Supercharger write
	// path. The caller no longer passes a bare id it merely believes is correct — it
	// must hold a vehicleref.Ref, which only internal/vehicleref can construct, and only
	// from a caller-owned vehicle list. A mismatched vehicle still matches zero rows
	// exactly like an unknown id, and a caller still cannot tell "not yours" from "does
	// not exist" — this change moves the ownership proof earlier, it does not change
	// what a mismatch looks like. Zero rows matched surfaces as an error wrapping
	// pgx.ErrNoRows, exactly mirroring Writer.Update's own not-found semantics.
	VerifySession(ctx context.Context, ref vehicleref.Ref, id uuid.UUID, startBatteryPct, endBatteryPct *int) (Session, error)
}

// NewSessionVerifier constructs a SessionVerifier backed by the given pgxpool. The
// implementation lives in session_verifier.go where the chargingdb generated package is
// used. This is the only publicly exported constructor for the SessionVerifier port.
func NewSessionVerifier(pool *pgxpool.Pool) SessionVerifier {
	return newSessionVerifier(pool)
}

// MirrorWatermarkStore is the cursor port for the Supercharger mirror read
// (RM44-platform-add-mirror-watermark, MAG-48). Both methods share ONE
// caller and ONE trust model -- internal/app reads the cursor, bounds its
// telemetry read by it, mirrors, then advances it -- unlike
// SessionWriter/SessionReader/SessionVerifier's three-way split, which
// exists because those ports serve callers with genuinely different trust
// models (nightly sync vs. dashboard read vs. human edit). One port here
// keeps the vocabulary closed rather than splitting for its own sake
// (CLAUDE.md's "do not over-abstract" AI-efficiency rule).
type MirrorWatermarkStore interface {
	// MirrorWatermark returns the stored cursor for teslaID: the highest
	// telemetry updated_at the mirror has already synchronized for that
	// vehicle. No stored row means "epoch" -- the zero time.Time, not an
	// error -- so the caller's first-ever read for this vehicle is
	// unbounded and backfills the vehicle's whole history once. Mirrors
	// analytics.recalculator.watermark's identical
	// "pgx.ErrNoRows -> time.Time{}, nil" translation exactly (design.md
	// D5, copying rather than re-deriving analytics.Recalculator.Reconcile's
	// own rule).
	MirrorWatermark(ctx context.Context, teslaID int64) (time.Time, error)

	// AdvanceMirrorWatermark upserts teslaID's cursor to observed, the
	// highest updated_at the caller actually saw on this run. This method
	// MUST be called only when the caller's bounded telemetry read
	// returned at least one row (roadmap D5) -- it performs no such check
	// itself and trusts the caller completely, mirroring
	// analytics.recalculator.advanceWatermark's identical division of
	// responsibility (Reconcile decides whether to call it; the method
	// itself just upserts). Calling this with observed == time.Time{} (the
	// zero value) on a call the caller should not have made is a caller
	// bug, not a case this method guards against, by design.
	AdvanceMirrorWatermark(ctx context.Context, teslaID int64, observed time.Time) error
}

// NewMirrorWatermarkStore constructs a MirrorWatermarkStore backed by the given
// pgxpool. The implementation lives in mirror_watermark.go where the chargingdb
// generated package is used. This is the only publicly exported constructor for
// the MirrorWatermarkStore port.
func NewMirrorWatermarkStore(pool *pgxpool.Pool) MirrorWatermarkStore {
	return newMirrorWatermarkStore(pool)
}

// MonthlyCapacityReport summarizes one Calculate call: how many distinct
// vehicles it considered, how many got a measured (non-NULL) capacity, and how
// many were left thin (a row was still written, per RD4, but
// effective_capacity_kwh is NULL). cmd/monthly-capacity (tier 3) prints this;
// nothing in this tier consumes it yet.
type MonthlyCapacityReport struct {
	Period        time.Time
	VehiclesFound int
	Measured      int
	Thin          int
}

// MonthlyCapacityCalculator computes and stores the effective pack capacity for
// one or every vehicle, for one calendar month (roadmap Tier 1, RD1/RD3/RD5).
// period must be the first instant of the month to compute; this module does
// not compute "now" or "the previous month" itself and imports no clock -- the
// tier-2 caller (internal/app, via internal/clock, RD7) decides both and passes
// the result in. teslaID nil means every vehicle with at least one valid row
// this period (RD8); non-nil scopes the run to one vehicle.
type MonthlyCapacityCalculator interface {
	Calculate(ctx context.Context, period time.Time, teslaID *int64) (MonthlyCapacityReport, error)
}

// NewMonthlyCapacityCalculator constructs a MonthlyCapacityCalculator backed by
// the given pgxpool. The implementation lives in monthly_capacity.go where the
// chargingdb generated package is used. This is the only publicly exported
// factory function for this port.
func NewMonthlyCapacityCalculator(pool *pgxpool.Pool) MonthlyCapacityCalculator {
	return newMonthlyCapacityCalculator(pool)
}
