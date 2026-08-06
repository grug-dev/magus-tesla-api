// Package telemetry is the platform's first collection + storage domain module
// (tier 3 of openspec/roadmaps/nightly-vehicle-telemetry.md). It captures one
// immutable snapshot of every connected user's vehicles on a nightly schedule,
// storing the raw vehicle_data payload plus extracted typed fields, and records
// the outcome of every collection attempt with per-vehicle isolation.
//
// It consumes the account and tesla PORTS only (never their DB or internals) and
// owns two append-only tables through the module-scoped telemetrydb package.
// Domain types here carry NO vendor suffix — they are our own models, distinct
// from the ...Tesla DTOs the tesla adapter unmarshals (ai/architecture.md §6).
package telemetry

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/tesla"
)

// milesToKm is the exact miles→kilometers factor. Every miles/mph field on a
// domain type exposes a companion Km/Kmh value-receiver method (ai/go-conventions.md
// non-negotiable). The Fleet API only sends miles, so km values are always derived,
// never stored as a column or a struct field.
const milesToKm = 1.609344

// barToPSI is the exact bar→PSI conversion factor. Every bar field on a domain
// type exposes a companion *float64 value-receiver method (ai/go-conventions.md
// non-negotiable). Tesla sends tire pressure in bar; PSI is derived on read,
// never stored as a column or a struct field.
const barToPSI = 14.503773773

// Snapshot is one immutable capture of a vehicle's state — our own domain model
// (no vendor suffix). It carries the owning account id, the vehicle's Tesla id,
// the platform capture time, the extracted typed fields, and the lossless raw
// vehicle_data payload. Distance/range fields are held API-native (miles); the
// kilometre equivalent is derived via the Km() companions below, never a field.
type Snapshot struct {
	AccountID  uuid.UUID
	TeslaID    int64
	CapturedAt time.Time
	// CapturedDate is the calendar date CapturedAt falls on, computed in the
	// poller's configured timezone (Config.Location) at write time (design D2 of
	// telemetry-dedupe-daily-snapshots). It backs the UNIQUE (account_id,
	// tesla_id, captured_date) constraint that collapses repeated same-day
	// captures into one row (design D1: latest capture wins). Represented as a
	// time.Time normalized to UTC midnight (the pgtype.Date convention) — treat
	// it as a plain calendar date, not a timestamp; CapturedAt remains the
	// authoritative "when."
	CapturedDate   time.Time
	BatteryLevel   int
	BatteryRange   float64 // miles — see BatteryRangeKm
	ChargingState  string
	ChargeLimitSoc int
	Odometer       float64 // miles — see OdometerKm
	InsideTemp     float64 // Celsius (Tesla temps are already °C — no conversion)
	OutsideTemp    float64 // Celsius
	Locked         bool
	// SentryMode is a pointer so an absent field (a vehicle that does not report
	// sentry) stays distinguishable from a reported-off sentry: nil = not reported,
	// *false = off, *true = on. It maps to a NULLABLE column (nil↔SQL NULL);
	// collapsing absent into false would lose history (design D1).
	SentryMode *bool
	CarVersion string
	// RawData is the lossless vehicle_data JSON, stored verbatim in the JSONB
	// column so any field not extracted above can be back-filled later.
	// Note: latitude/longitude (raw_data->'drive_state') and fast_charger_type
	// (raw_data->'charge_state') are NOT extracted as typed columns — they were
	// dropped in migration 20260801000001 (unused columns, lossless in raw_data).
	RawData []byte

	// Charge enrichment fields (Source A of RM2-telemetry-add-charging-stats).
	// Nullable: nil when not reported OR when the row predates this extraction
	// (every row written before the 20260716000002 migration). On the write path,
	// snapshotFrom always stores the actual DTO value pointer-wrapped, so a 0 or ""
	// is a truthful reading and is stored as non-NULL (D12, design DSA3).
	// NULL is reserved exclusively for pre-migration rows that were never backfilled.
	// No Km()/Kmh() companions — these fields are kWh, kW, V, A, %:
	// none are distances or speeds (design DSA1, ai/go-conventions.md).
	ChargeEnergyAdded    *float64 // kWh added this charge session; nil = not reported / pre-enrichment
	ChargerPower         *int     // kW; nil = not reported / pre-enrichment
	ChargerVoltage       *int     // V; nil = not reported / pre-enrichment
	ChargerActualCurrent *int     // A; nil = not reported / pre-enrichment
	UsableBatteryLevel   *int     // %; nil = not reported / pre-enrichment

	// MaxRangeChargeCounter is the lifetime count of times the vehicle has been
	// charged to its true 100% Maximum-Battery-Range limit. Nullable so a pre-
	// migration row (before 20260801000001) stays NULL rather than falsely claiming
	// "zero charges to max-range". A real reported 0 is stored as non-nil *0 via
	// pointer-wrap in snapshotFrom (D12/DSA3 convention — same as the 5 Source A
	// fields above). nil = not yet extracted / row predates this extraction.
	MaxRangeChargeCounter *int // count; nil = pre-extraction row or not reported

	// TPMS (tire-pressure monitoring system) pressure fields in bar (API-native).
	// nil when the vehicle did not report TPMS at capture (no sensors, absent
	// reading) OR the row predates this extraction (pre-migration). A truthfully
	// reported 0.0 bar is stored non-NULL (pointer-wrapped via ptr() in snapshotFrom —
	// D12/DSA3 convention). Use the companion PSI() methods for display in PSI.
	// NULL is reserved exclusively for pre-migration rows / not reported.
	TpmsPressureFL *float64 // bar — see TpmsPressureFLPSI
	TpmsPressureFR *float64 // bar — see TpmsPressureFRPSI
	TpmsPressureRL *float64 // bar — see TpmsPressureRLPSI
	TpmsPressureRR *float64 // bar — see TpmsPressureRRPSI
}

// BatteryRangeKm returns the rated range converted from miles to kilometers.
func (s Snapshot) BatteryRangeKm() float64 {
	return s.BatteryRange * milesToKm
}

// OdometerKm returns the odometer reading converted from miles to kilometers.
func (s Snapshot) OdometerKm() float64 {
	return s.Odometer * milesToKm
}

// TpmsPressureFLPSI returns the front-left tire pressure converted from bar to
// PSI. Returns nil when TpmsPressureFL is nil (not reported / pre-migration row).
func (s Snapshot) TpmsPressureFLPSI() *float64 {
	if s.TpmsPressureFL == nil {
		return nil
	}
	return ptr(*s.TpmsPressureFL * barToPSI)
}

// TpmsPressureFRPSI returns the front-right tire pressure converted from bar to
// PSI. Returns nil when TpmsPressureFR is nil (not reported / pre-migration row).
func (s Snapshot) TpmsPressureFRPSI() *float64 {
	if s.TpmsPressureFR == nil {
		return nil
	}
	return ptr(*s.TpmsPressureFR * barToPSI)
}

// TpmsPressureRLPSI returns the rear-left tire pressure converted from bar to
// PSI. Returns nil when TpmsPressureRL is nil (not reported / pre-migration row).
func (s Snapshot) TpmsPressureRLPSI() *float64 {
	if s.TpmsPressureRL == nil {
		return nil
	}
	return ptr(*s.TpmsPressureRL * barToPSI)
}

// TpmsPressureRRPSI returns the rear-right tire pressure converted from bar to
// PSI. Returns nil when TpmsPressureRR is nil (not reported / pre-migration row).
func (s Snapshot) TpmsPressureRRPSI() *float64 {
	if s.TpmsPressureRR == nil {
		return nil
	}
	return ptr(*s.TpmsPressureRR * barToPSI)
}

// Outcome is the result of a single collection attempt on one vehicle.
type Outcome string

const (
	OutcomeSuccess Outcome = "success"
	OutcomeFailure Outcome = "failure"
)

// Reason explains an attempt's outcome. It doubles as the availability /
// sleep-behavior signal recorded for every vehicle in poll_attempts (design D5).
type Reason string

const (
	// ReasonOK — snapshot captured and stored.
	ReasonOK Reason = "ok"
	// ReasonAsleepTimeout — the wake deadline elapsed before the vehicle came online.
	ReasonAsleepTimeout Reason = "asleep-timeout"
	// ReasonUnauthorized — the account has no usable Tesla connection (401 /
	// tesla.ErrUnauthorized, or account.ErrNoTeslaConnection). Not retried.
	ReasonUnauthorized Reason = "unauthorized"
	// ReasonAPIError — any other Tesla/HTTP/decode/store error (retried once).
	ReasonAPIError Reason = "api-error"
)

// Attempt is one recorded (vehicle, run) collection attempt — exactly one is
// written per vehicle per cycle regardless of outcome (design D5).
type Attempt struct {
	AccountID   uuid.UUID
	TeslaID     int64
	AttemptedAt time.Time
	Outcome     Outcome
	Reason      Reason
}

// Config carries the collection service's runtime tuning. The wake timeout bounds
// how long an asleep vehicle is polled for online before it is recorded as an
// asleep-timeout failure (design D4). Clock is optional and exists for tests
// (fake clock); when nil the service uses the wall clock.
type Config struct {
	WakeTimeout time.Duration
	// Clock returns the current time; nil means time.Now. Injected in tests so
	// captured_at and timeout math are deterministic without waiting.
	Clock func() time.Time
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

// CycleReport summarizes one collection cycle: how many vehicles were attempted
// and the breakdown of outcomes by reason. It is returned by CollectAll so the
// scheduler / caller can log a run without querying the store.
type CycleReport struct {
	// Attempted is the total number of vehicles processed in the cycle.
	Attempted int
	// Succeeded is the number of vehicles with a stored snapshot (reason ok).
	Succeeded int
	// FailuresByReason counts failures keyed by their Reason (asleep-timeout,
	// unauthorized, api-error).
	FailuresByReason map[Reason]int
	// ChargingSessionsUpserted is the total number of Supercharger sessions
	// successfully upserted across all accounts in this cycle.
	ChargingSessionsUpserted int
	// ChargingFetchFailures is the number of accounts for which the
	// ChargingHistory call failed (network error, 401, etc.). A non-zero value
	// signals partial data for those accounts.
	ChargingFetchFailures int
	// ConfigCaptureFailures is the number of vehicle_config write-back attempts that failed
	// (the account port's SetVehicleConfigIfEmpty call returned an error). A failed write-back
	// never changes the vehicle's Reason and never writes a poll_attempts row — it is retried
	// for free on the next cycle since the registry row is still missing at least one column.
	ConfigCaptureFailures int
}

// Collector runs one collection cycle over every registered vehicle across all
// accounts, capturing a snapshot per vehicle and recording every attempt. It is
// the telemetry module's public port; the scheduler and cmd/poller depend on this
// interface, never on the concrete implementation or the DB.
type Collector interface {
	// CollectAll runs a single cycle: enumerate all registered vehicles (via the
	// account port), and per vehicle wake-if-needed, fetch, store a snapshot, and
	// record a poll_attempts row. Per-vehicle isolation: one vehicle's failure
	// never aborts the cycle. It returns a CycleReport (counts of success/failure
	// by reason) and an error only for a whole-cycle failure (e.g. the account
	// enumeration itself failing) — never for an individual vehicle.
	CollectAll(ctx context.Context) (CycleReport, error)
}

// Reader exposes the telemetry module's stored snapshots for read-only
// consumption by the gateway and other callers. It is the second half of the
// telemetry public port; the first half (Collector) is the write path.
// Callers must never import telemetrydb directly — all access goes through
// this interface.
type Reader interface {
	// LatestSnapshotsByAccount returns the most-recently captured snapshot for
	// each vehicle belonging to the given account. If the account has no stored
	// snapshots it returns an empty (non-nil) slice and a nil error. Order of
	// the returned slice is unspecified.
	LatestSnapshotsByAccount(ctx context.Context, accountID uuid.UUID) ([]Snapshot, error)

	// SnapshotsByVehicleSince returns the nightly snapshots captured for the
	// given vehicle (within the given account) at or after `since`, ordered
	// oldest-first. Returns an empty (non-nil) slice and nil error when no
	// snapshots exist in the window (design D1: caller supplies the window
	// boundary; this port is a pure data accessor). The account_id AND tesla_id
	// filter provides defense-in-depth tenant isolation (D2) even when the
	// gateway already resolves tesla_id from account.RegisteredVehicles(uid).
	// Distance and range fields are miles-native; callers use BatteryRangeKm()
	// / OdometerKm() for metric equivalents.
	SnapshotsByVehicleSince(ctx context.Context, accountID uuid.UUID, teslaID int64, since time.Time) ([]Snapshot, error)
}

// --- Source B: Supercharger sessions ---

// SuperchargerSession is one Tesla-billed Supercharger / DC fast-charging session —
// our own domain model (no vendor suffix, ai/architecture.md §6). It is distinct from
// tesla.ChargingSessionTesla: that is the vendor DTO; this is the mapped, stored
// domain record. Nullable fields use *T where the column allows NULL (energy, cost,
// currency, is_paid, tesla_id, unlatch_date_time). No Km()/Kmh() companions — none
// of these fields are distances or speeds (design DBS5).
type SuperchargerSession struct {
	ID                  uuid.UUID
	SessionID           int64 // Tesla's globally-unique session id
	AccountID           uuid.UUID
	VIN                 string
	TeslaID             *int64 // NULL when VIN not a current registered vehicle
	SiteLocationName    string
	CountryCode         string
	ChargeStartDateTime time.Time
	ChargeStopDateTime  time.Time
	UnlatchDateTime     *time.Time // NULL when not present in response
	BillingType         string
	VehicleMakeType     string
	EnergyKWh           *float64 // derived; NULL when no kWh fee (design DBS2)
	TotalCost           *float64 // derived; NULL when fees empty
	Currency            *string  // derived; NULL when fees empty
	IsPaid              *bool    // derived; NULL when fees empty
	RawData             []byte   // verbatim session JSON (fees[] + invoices[])
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// deriveEnergyKWh sums the usage tiers for fees where lower(uom) == "kwh".
// Returns nil when no fee has uom == "kwh" (time-based billing only, or no fees).
// Design DBS2: energy_kwh derivation in Go at write time, not in SQL.
func deriveEnergyKWh(fees []tesla.ChargingFeeTesla) *float64 {
	var total float64
	found := false
	for _, f := range fees {
		if strings.ToLower(f.UOM) != "kwh" {
			continue
		}
		found = true
		total += f.UsageBase + f.UsageTier1 + f.UsageTier2
		if f.UsageTier3 != nil {
			total += *f.UsageTier3
		}
		if f.UsageTier4 != nil {
			total += *f.UsageTier4
		}
	}
	if !found {
		return nil
	}
	return &total
}

// deriveTotalCost sums totalDue over all fees. Returns nil when fees is empty.
// Design DBS2: total_cost derivation in Go at write time.
func deriveTotalCost(fees []tesla.ChargingFeeTesla) *float64 {
	if len(fees) == 0 {
		return nil
	}
	var total float64
	for _, f := range fees {
		total += f.TotalDue
	}
	return &total
}

// deriveCurrency returns the currencyCode from the first fee. Returns nil when
// fees is empty. Design DBS2: currency is uniform across fees for one session.
func deriveCurrency(fees []tesla.ChargingFeeTesla) *string {
	if len(fees) == 0 {
		return nil
	}
	c := fees[0].CurrencyCode
	return &c
}

// deriveIsPaid returns the logical AND of every fee's isPaid. Returns nil when
// fees is empty (impossible to determine billing status). *false when at least
// one fee is unpaid; *true when all fees are paid. Design DBS2.
func deriveIsPaid(fees []tesla.ChargingFeeTesla) *bool {
	if len(fees) == 0 {
		return nil
	}
	allPaid := true
	for _, f := range fees {
		if !f.IsPaid {
			allPaid = false
			break
		}
	}
	return &allPaid
}

// SuperchargerReader exposes supercharger session data for read-only consumption.
// It is a separate port from Reader (snapshot-centric) to keep concerns distinct
// and to allow the gateway to depend on only the port it needs. Callers MUST NOT
// import telemetrydb (design DBS6).
type SuperchargerReader interface {
	// SuperchargerSessionsByAccount returns all Supercharger sessions for the
	// given account, ordered by charge_start_date_time DESC, limited to limit
	// rows (0 = server default of math.MaxInt32). Returns an empty non-nil
	// slice when no sessions exist.
	SuperchargerSessionsByAccount(ctx context.Context, accountID uuid.UUID, limit int) ([]SuperchargerSession, error)

	// SuperchargerSessionsByVehicle returns Supercharger sessions for the given
	// vehicle within the given account, ordered by charge_start_date_time DESC,
	// limited to limit rows (0 = server default of math.MaxInt32). Returns an
	// empty non-nil slice when no sessions exist.
	SuperchargerSessionsByVehicle(ctx context.Context, accountID uuid.UUID, teslaID int64, limit int) ([]SuperchargerSession, error)
}

// NewSuperchargerReader constructs a SuperchargerReader backed by the telemetry DB pool.
// Implementation is in reader.go. The gateway and other callers depend on the
// SuperchargerReader interface, never on the concrete type or on telemetrydb directly.
func NewSuperchargerReader(pool *pgxpool.Pool) SuperchargerReader {
	return newSuperchargerReaderImpl(pool)
}
