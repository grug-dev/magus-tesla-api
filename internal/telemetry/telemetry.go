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

// Snapshot is one immutable capture of a vehicle's state — our own domain model
// (no vendor suffix). It carries the owning account id, the vehicle's Tesla id,
// the platform capture time, the extracted typed fields, and the lossless raw
// vehicle_data payload. Every unit-bearing field is stored in its DISPLAY unit,
// converted exactly once at capture time by calling the tesla adapter's Km()/
// PSI() companions (telemetry-store-display-units design D1/D3, RM7 Decision 1);
// this module holds no conversion constant of its own and exposes no read-time
// conversion method — the field name itself carries the unit.
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
	CapturedDate time.Time
	// EffectiveDate is the calendar day this snapshot REPRESENTS, not the day it
	// was read: CapturedAt − 1 calendar day (the nightly poller runs at ≈03:30
	// local and captures the vehicle's state accumulated over the PRIOR day).
	// Read-derived: there is no matching DB column and no migration — it is
	// computed once, in rowToSnapshot, and never persisted. A write-built
	// Snapshot (insertSnapshot) does not set it, so it stays the zero time.Time
	// on the write path; nothing reads it before storage. It is a full
	// time.Time preserving the time-of-day component (same shape as
	// CapturedAt) — NOT a pre-formatted display string; formatting (e.g.
	// MM-DD) is a caller/gateway concern. Distinct from both CapturedDate
	// (the capture's own calendar day, used only for the dedupe UNIQUE
	// constraint) and CapturedAt (the precise read instant): EffectiveDate is
	// the day the data describes.
	EffectiveDate     time.Time
	BatteryLevelPct   int
	BatteryRangeKm    float64 // km — converted from miles at capture time via tesla.ChargeStateTesla.BatteryRangeKm()
	ChargingState     string
	ChargeLimitSocPct int
	OdometerKm        float64 // km — converted from miles at capture time via tesla.VehicleStateTesla.OdometerKm()
	InsideTempC       float64 // Celsius (Tesla temps are already °C — no conversion)
	OutsideTempC      float64 // Celsius
	Locked            bool
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
	ChargeEnergyAddedKWh  *float64 // kWh added this charge session; nil = not reported / pre-enrichment
	ChargerPowerKW        *int     // kW; nil = not reported / pre-enrichment
	ChargerVoltageV       *int     // V; nil = not reported / pre-enrichment
	ChargerActualCurrentA *int     // A; nil = not reported / pre-enrichment
	UsableBatteryLevelPct *int     // %; nil = not reported / pre-enrichment

	// MaxRangeChargeCounter is the lifetime count of times the vehicle has been
	// charged to its true 100% Maximum-Battery-Range limit. Nullable so a pre-
	// migration row (before 20260801000001) stays NULL rather than falsely claiming
	// "zero charges to max-range". A real reported 0 is stored as non-nil *0 via
	// pointer-wrap in snapshotFrom (D12/DSA3 convention — same as the 5 Source A
	// fields above). nil = not yet extracted / row predates this extraction.
	MaxRangeChargeCounter *int // count; nil = pre-extraction row or not reported

	// TPMS (tire-pressure monitoring system) pressure fields in PSI, converted
	// from the Fleet API's native bar reading exactly once at capture time via
	// tesla.VehicleStateTesla's TpmsPressure*PSI() companions. nil when the
	// vehicle did not report TPMS at capture (no sensors, absent reading) OR the
	// row predates this extraction (pre-migration). A truthfully reported 0.0 PSI
	// is stored non-NULL (pointer-wrapped via ptr() in snapshotFrom — D12/DSA3
	// convention). NULL is reserved exclusively for pre-migration rows / not reported.
	TpmsPressureFLPSI *float64
	TpmsPressureFRPSI *float64
	TpmsPressureRLPSI *float64
	TpmsPressureRRPSI *float64

	// UpdatedAt exposes the existing vehicle_snapshots.updated_at column on the
	// domain type for the first time (RM29-analytics-add-vehicle-metrics, task
	// 1.1). It carries no new DB column and no new write-path behavior: the
	// column already reflects DEFAULT now() on a fresh insert and an explicit
	// now() on a same-day conflict-update (design D5 of
	// telemetry-dedupe-daily-snapshots) — this field simply maps that
	// already-persisted value onto Snapshot so other modules can detect which
	// snapshots changed recently without reading telemetrydb directly.
	UpdatedAt time.Time
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
	// RunID correlates this attempt to the RunContext.RunID of the CollectAll
	// invocation that wrote it (RM29-app-add-process-vehicle-data design D5).
	// Always non-zero on write — every Attempt is built from a real RunContext
	// supplied by the caller (ultimately generated by internal/app), so there is
	// never a legitimate reason to write a zero RunID here. The poll_attempts.run_id
	// COLUMN is nullable (legacy pre-migration rows only, design D7); this Go field
	// carries no such asymmetry on the write path.
	RunID uuid.UUID
	// TriggeredBy correlates this attempt to the RunContext.TriggeredBy of the
	// CollectAll invocation that wrote it (design D5). Always non-empty on write,
	// for the same reason as RunID above.
	TriggeredBy TriggeredBy
}

// TriggeredBy identifies what caused one CollectAll (app.ProcessVehicleData) run to
// happen (RM29-app-add-process-vehicle-data design D5). telemetry owns this type
// because it owns the poll_attempts column it fills — mirroring the project's
// existing precedent that the module owning a column owns the Go type that fills it
// (charging.SessionMirror carrying exactly charge_sessions' mirrorable columns, T6
// design D6).
type TriggeredBy string

const (
	// TriggeredByScheduler — the nightly scheduled poller (internal/app's Scheduler),
	// including cmd/poller --once.
	TriggeredByScheduler TriggeredBy = "scheduler"
	// TriggeredByAPI — a future manual re-run via HTTP (RM29 tier 8, parked).
	TriggeredByAPI TriggeredBy = "api"
)

// RunContext identifies one invocation ("run") of CollectAll so every poll_attempts
// row it writes can be correlated back to that run and to what triggered it (design
// D5). internal/app generates a fresh RunID (uuid.New()) once per invocation and
// builds a RunContext to pass down; it consumes this type but declares neither it nor
// its constants — telemetry owns both (see TriggeredBy's doc comment above).
type RunContext struct {
	RunID       uuid.UUID
	TriggeredBy TriggeredBy
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
	// telemetry-dedupe-daily-snapshots). Nil means the platform's default zone,
	// clock.Zone() (America/Bogota) — RM35-telemetry-adopt-clock, roadmap D4.
	// internal/app's Scheduler has the same nil-loc fallback and now resolves it
	// the same way, to clock.Zone() (RM35-app-adopt-clock, tier 5) — so the two
	// agree again on a nil input. cmd/poller sets this Location from
	// config.PollerTimezone via time.LoadLocation — the SAME *time.Location
	// passed to NewScheduler — so the day a snapshot is dated always agrees with
	// the day the scheduler considers "today" for that run.
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
	// TeslaAPICalls is the total number of requests this cycle made to
	// tesla.VehicleService (ListVehicles, WakeUp, VehicleData, ChargingHistory),
	// counted regardless of success or failure by an internal counting decorator
	// (RM36-telemetry-add-poll-runs design D9/D10) — a rejected or failed request
	// still spends a request against Tesla's API and its rate limit.
	TeslaAPICalls int
	// AccountsAttempted is the number of accounts this cycle enumerated vehicles
	// for, set once after the account map is built (RM36 design D10/Go-Level Seam
	// Summary).
	AccountsAttempted int
	// AccountsSucceeded is AccountsAttempted minus AccountsFailed, set once after
	// the account loop completes (roadmap D4's own formula — no per-account
	// "success" increment).
	AccountsSucceeded int
	// AccountsFailed counts accounts that hit one of the two whole-account
	// short-circuits in collectAccount: an AccessTokenFor failure, or the
	// up-front ListVehicles call returning tesla.ErrUnauthorized (roadmap D4). No
	// other failure mode increments this counter.
	AccountsFailed int
	// Duration is the whole run's wall-clock duration, spanning steps 1–3 of
	// app.ProcessVehicleData (start before step 1, end after step 3). CollectAll
	// itself only ever runs step 1, so it cannot measure this — it leaves this
	// field at its zero value on every call, documented here as an intentional
	// "not measured by this call" state (RM36-telemetry-add-poll-runs design D6).
	// Only the caller (internal/app's ProcessVehicleData, tier 2) sets this field
	// on the CycleReport value it already holds, after measuring the full run,
	// before calling the unchanged LogCycle — no signature change to LogCycle.
	Duration time.Duration
}

// Collector runs one collection cycle over every registered vehicle across all
// accounts, capturing a snapshot per vehicle and recording every attempt. It is
// the telemetry module's public port; internal/app's Processor and Scheduler
// depend on this interface, never on the concrete implementation or the DB.
type Collector interface {
	// CollectAll runs a single cycle: enumerate all registered vehicles (via the
	// account port), and per vehicle wake-if-needed, fetch, store a snapshot, and
	// record a poll_attempts row. run identifies this invocation (RunContext's
	// RunID/TriggeredBy, design D5 of RM29-app-add-process-vehicle-data) — it is
	// generated fresh by internal/app once per call (uuid.New() for RunID) and
	// threaded straight through to every poll_attempts row this cycle writes;
	// CollectAll never generates or caches a RunContext of its own. Per-vehicle
	// isolation: one vehicle's failure never aborts the cycle. It returns a
	// CycleReport (counts of success/failure by reason) and an error only for a
	// whole-cycle failure (e.g. the account enumeration itself failing) — never for
	// an individual vehicle.
	CollectAll(ctx context.Context, run RunContext) (CycleReport, error)
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
	// Distance and range fields are already in kilometres, converted at capture
	// time — no companion conversion method exists on the returned Snapshot
	// (telemetry-store-display-units design D1/D3).
	SnapshotsByVehicleSince(ctx context.Context, accountID uuid.UUID, teslaID int64, since time.Time) ([]Snapshot, error)

	// SnapshotsByVehicleBetween returns the nightly snapshots captured for the given
	// vehicle (within the given account) whose **EffectiveDate calendar day** falls in
	// the caller-supplied `[start, end]` window inclusive, ordered ascending by
	// EffectiveDate (equivalently ascending by `captured_at`, since EffectiveDate is
	// monotonic in CapturedAt — oldest-first). `start` and `end` are whole UTC-midnight-
	// bounded calendar days; `end` is **inclusive** (Decision #2 of the RM8 roadmap
	// grill-me interview). The port is a clean bounded window: there is **no lookback
	// parameter** — the 1-day lookback the gateway needs for the first odometer delta is
	// a gateway concern, expressed by the caller passing `start - 1 day` as `start`
	// (Decision #4). The port does no validation of the UTC-midnight/inclusive-`end`
	// contract; that is the HTTP layer's job in tier 2.
	//
	// The implementation honors `EffectiveDate ∈ [start, end]` by filtering on the
	// `captured_at` TIMESTAMPTZ column (NOT the poller-zone `captured_date` — see design
	// D1) with bounds derived from the window; that translation is an internal detail of
	// the dbStore implementation (design D5) and never appears in this signature.
	//
	// Returns an empty (non-nil) slice and nil error when the vehicle has no snapshots
	// in the window (parity with SnapshotsByVehicleSince / LatestSnapshotsByAccount — no
	// nil-slice footgun for callers). Reuses the single `rowToSnapshot` mapper, so this
	// method inherits EffectiveDate and every Extracted typed field for free with no
	// per-method duplication. Distance and range fields are already in kilometres,
	// converted at capture time — no companion conversion method exists on the returned
	// Snapshot (telemetry-store-display-units design D1/D3).
	//
	// The account_id AND tesla_id filter provides defense-in-depth tenant isolation
	// (parity with SnapshotsByVehicleSince's D2) even when the gateway already resolves
	// tesla_id from account.RegisteredVehicles(uid). This method is ADDITIVE alongside
	// SnapshotsByVehicleSince (kept unchanged — Decision #4 / design D4); the two serve
	// different access patterns (open lower bound vs bounded window) and deprecation/
	// removal of `Since`, if ever, is a separate change.
	SnapshotsByVehicleBetween(ctx context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) ([]Snapshot, error)

	// SnapshotsByVehicleUpdatedSince returns every stored snapshot for the given
	// vehicle (within the given account) whose UpdatedAt is at or after `since`,
	// without ordering guarantees stronger than the underlying query provides
	// (ordered ascending by updated_at, mirroring SnapshotsByVehicleSince's
	// oldest-first convention). It exists so other modules (internal/analytics'
	// Recalculator, RM29-analytics-add-vehicle-metrics) can detect which
	// snapshots changed recently — including a same-day REPLACE via the
	// existing UPSERT (design D1 of telemetry-dedupe-daily-snapshots) — without
	// importing telemetrydb directly. Returns a non-nil empty slice and nil
	// error when no snapshot for the vehicle has been updated at or after
	// `since` (parity with every other Reader method's empty-result contract —
	// no nil-slice footgun for callers). The account_id AND tesla_id filter
	// provides defense-in-depth tenant isolation, mirroring every other
	// per-vehicle method on this interface. Reuses the existing
	// idx_vehicle_snapshots_vehicle_time (account_id, tesla_id, captured_at)
	// index's leading (account_id, tesla_id) columns as a scan prefix;
	// updated_at is a residual filter within that scan — no new index (verified
	// via EXPLAIN in the DB-integration test, Wave 6 of that change). Reuses
	// the single rowToSnapshot mapper — no per-method duplication.
	SnapshotsByVehicleUpdatedSince(ctx context.Context, accountID uuid.UUID, teslaID int64, since time.Time) ([]Snapshot, error)

	// SnapshotPrecedingDay returns the single most recently captured snapshot for
	// the given vehicle (within the given account) whose CapturedDate is strictly
	// before `day`, or `(nil, nil)` when the vehicle has no earlier snapshot at all
	// (its first-ever capture) — an absent predecessor is a normal answer, never an
	// error. `day` is a bare calendar date, UTC-midnight-normalized — the same
	// representation `Snapshot.CapturedDate` already carries, not a precise capture
	// instant (RM29-telemetry-drop-derived-columns design D2, carries interview
	// outcome I2). A genuine query error is returned as-is and MUST NOT be degraded
	// to "no predecessor" — a transient storage fault must never be mistaken by a
	// caller for "this vehicle has no earlier snapshot" (its only intended caller,
	// internal/analytics' Recalculate, aborts on error rather than silently NULLing
	// a real vehicle's derived figures).
	//
	// The bound is evaluated against the stored `captured_date` calendar day, not
	// against `captured_at`. This is deliberate and load-bearing: a snapshot
	// captured 03:30 local in a UTC+ poller timezone can land on the PREVIOUS UTC
	// calendar day, so bounding on captured_at against a UTC-midnight `day` would
	// wrongly select a row as its own predecessor. Because `captured_date` is
	// already the poller-zone calendar day (stamped once on the write path by
	// `clock.CalendarDay`), the predicate is zone-free at query time, and a same-day
	// re-capture (the "latest capture for a calendar day wins" replace rule) can
	// never select its own about-to-be-replaced row as its own predecessor — the
	// exact guarantee the module's former `dayStart`-bounded `previousSnapshot`
	// seam provided, re-expressed in the schema's own day column.
	//
	// The port reaches the TRUE predecessor however old it is: there is no maximum
	// lookback, no trailing-window limit, and no fixed number of days beyond which
	// the predecessor is reported absent — unlike SnapshotsByVehicleSince/Between,
	// which are bounded windows. This is the exact predecessor a multi-day capture
	// gap needs; a bounded/widened-window alternative was rejected because it would
	// yield a silently wrong delta for any gap exceeding the window (design D2).
	//
	// Reuses the existing idx_vehicle_snapshots_vehicle_time (account_id, tesla_id,
	// captured_at) index as a BACKWARD scan off its two leading equality columns —
	// no new index. `vehicle_snapshots_account_tesla_date_unique` guarantees at most
	// one row per vehicle per calendar day, so at most one row is examined and
	// rejected by the captured_date residual predicate before the match (verified
	// via EXPLAIN in the DB-integration test). The account_id AND tesla_id filter
	// provides defense-in-depth tenant isolation, mirroring every other per-vehicle
	// method on this interface. Reuses the single `rowToSnapshot` mapper — no new
	// mapper, no per-method duplication.
	//
	// This method is the module's former private `previousSnapshot` store seam
	// promoted to the public port, with its bound changed from an instant
	// (`captured_at < before`) to a calendar day (`captured_date < day`) so a
	// caller holding no `*time.Location` (internal/analytics deliberately holds
	// none) can still compute a correct, zone-free bound.
	SnapshotPrecedingDay(ctx context.Context, accountID uuid.UUID, teslaID int64, day time.Time) (*Snapshot, error)
}

// --- Source B: Supercharger sessions ---

// SuperchargerHistory is one Tesla-billed Supercharger / DC fast-charging session,
// stored in the telemetry.supercharger_history table (renamed from
// supercharger_sessions by RM39-telemetry-move-to-own-schema tier 4, roadmap D5a/D5c)
// — our own domain model (no vendor suffix, ai/architecture.md §6). It is distinct from
// tesla.ChargingSessionTesla: that is the vendor DTO; this is the mapped, stored
// domain record. Nullable fields use *T where the column allows NULL (energy, cost,
// currency, is_paid, tesla_id, unlatch_date_time). No Km()/Kmh() companions — none
// of these fields are distances or speeds (design DBS5).
type SuperchargerHistory struct {
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

	// --- Human-owned battery-% verification/override channel (RM27, MAG-14) ---
	// NULL = nothing recorded. NEVER auto-written by the nightly poller — omitted from
	// UpsertSuperchargerHistory's INSERT and ON CONFLICT DO UPDATE SET alike (R3/D3),
	// so a nightly re-upsert can never clobber a human-entered value. No writer exists
	// in this repository yet: RM27 shipped the storage only. (The two frozen
	// estimate fields formerly reserved here as a write-once verification-time
	// snapshot pair were dropped from the struct and the table entirely by
	// RM41-telemetry-drop-estimate-columns — there is no longer a reserved pair to
	// describe.)
	StartBatteryPct *int // 0-100 inclusive; SMALLINT CHECK in DB.
	EndBatteryPct   *int // same shape/nullability as StartBatteryPct.
	// BatteryPctSource: "user_verified" | "polled" | NULL. NULL = nothing recorded.
	// NEVER "estimated" — this platform does not compute or store SOC estimates at all
	// (the estimator was descoped from RM27 on 2026-08-15; backlog entry 11).
	BatteryPctSource *string
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

// SuperchargerHistoryReader exposes supercharger session data for read-only consumption.
// It is a separate port from Reader (snapshot-centric) to keep concerns distinct
// and to allow the gateway to depend on only the port it needs. Callers MUST NOT
// import telemetrydb (design DBS6).
type SuperchargerHistoryReader interface {
	// SuperchargerHistoryByAccount returns all Supercharger sessions for the
	// given account, ordered by charge_start_date_time DESC, limited to limit
	// rows (0 = server default of math.MaxInt32). Returns an empty non-nil
	// slice when no sessions exist.
	SuperchargerHistoryByAccount(ctx context.Context, accountID uuid.UUID, limit int) ([]SuperchargerHistory, error)

	// SuperchargerHistoryByVehicle returns Supercharger sessions for the given
	// vehicle within the given account, ordered by charge_start_date_time DESC,
	// limited to limit rows (0 = server default of math.MaxInt32). Returns an
	// empty non-nil slice when no sessions exist.
	SuperchargerHistoryByVehicle(ctx context.Context, accountID uuid.UUID, teslaID int64, limit int) ([]SuperchargerHistory, error)

	// SuperchargerHistoryByVehicleBetween returns Supercharger sessions for
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
	// its own (that is internal/analytics's job, roadmap D12) -- it only
	// answers "which sessions' energy finished landing in this window."
	//
	// Returns a non-nil empty slice and nil error when no sessions exist in
	// the window (parity with SuperchargerHistoryByAccount/ByVehicle's
	// existing empty-result contract, and with Reader.SnapshotsByVehicleBetween's
	// identical convention -- no nil-slice footgun for callers). The
	// account_id AND tesla_id filter provides defense-in-depth tenant
	// isolation, mirroring every other bounded-window method in this module.
	//
	// Purely additive alongside SuperchargerHistoryByAccount/ByVehicle
	// (both unchanged, both remain limit-based for their own "most recent N"
	// access pattern). This method has no limit parameter and no LIMIT-N
	// contract -- the caller-supplied window is the bound, exactly like
	// Reader.SnapshotsByVehicleBetween's own reasoning for why a bounded
	// window makes an unbounded-N limit the caller's job, not this query's.
	SuperchargerHistoryByVehicleBetween(ctx context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) ([]SuperchargerHistory, error)

	// SuperchargerHistoryByVehicleUpdatedSince returns every stored Supercharger
	// session for the given vehicle (within the given account) whose updated_at
	// is at or after `since`, ordered oldest-first by updated_at. It exists so
	// other modules (internal/analytics' Recalculator,
	// RM29-analytics-add-vehicle-metrics) can detect which sessions changed
	// recently — including a billing-state revision on a session weeks old,
	// whose ChargeStartDateTime/ChargeStopDateTime stay unchanged while
	// updated_at refreshes (design DBS3: supercharger_history is not
	// append-only) — without importing telemetrydb directly. Returns a non-nil
	// empty slice and nil error when no session for the vehicle has been
	// updated at or after `since` (parity with every other SuperchargerHistoryReader
	// method's empty-result contract). The account_id AND tesla_id filter
	// provides defense-in-depth tenant isolation, mirroring every other
	// per-vehicle method on this interface. No new index — updated_at is a
	// residual filter within the existing (account_id, tesla_id) scan prefix
	// (verified via EXPLAIN in the DB-integration test, Wave 6 of that
	// change). Reuses the existing rowToSuperchargerHistory mapper — no new
	// field, no new mapper.
	SuperchargerHistoryByVehicleUpdatedSince(ctx context.Context, accountID uuid.UUID, teslaID int64, since time.Time) ([]SuperchargerHistory, error)
}

// NewSuperchargerHistoryReader constructs a SuperchargerHistoryReader backed by the telemetry DB pool.
// Implementation is in reader.go. The gateway and other callers depend on the
// SuperchargerHistoryReader interface, never on the concrete type or on telemetrydb directly.
func NewSuperchargerHistoryReader(pool *pgxpool.Pool) SuperchargerHistoryReader {
	return newLoggingSuperchargerHistoryReader(newSuperchargerHistoryReaderImpl(pool))
}

// --- poll_runs: run-level summary (RM36-telemetry-add-poll-runs) ---

// PollRun is one run-level summary of an app.ProcessVehicleData invocation — our
// own domain model (no vendor suffix, ai/architecture.md §6). Unlike Attempt (one
// row per vehicle per run), a PollRun is written exactly once per run, by the
// caller (internal/app, tier 2), after the whole run has completed — success or
// the step-1 whole-cycle-failure short-circuit alike (design D1/D3/D6). There is
// no read port for this type yet (no Reader-style method — design D5/backlog):
// the only way to observe a PollRun today is the direct SQL a DB-integration test
// runs, or a future gateway read surface.
type PollRun struct {
	RunID       uuid.UUID
	TriggeredBy TriggeredBy
	StartedAt   time.Time
	FinishedAt  time.Time
	// DurationSeconds is FinishedAt.Sub(StartedAt) in seconds, computed and
	// supplied by the caller — this type does not derive it itself.
	DurationSeconds float64

	AccountsAttempted int
	AccountsSucceeded int
	AccountsFailed    int

	VehiclesAttempted     int
	VehiclesSucceeded     int
	FailuresAsleepTimeout int
	FailuresUnauthorized  int
	FailuresAPIError      int

	// TeslaAPICalls is the total number of Tesla Fleet API requests this run
	// spent, counted regardless of success or failure (design D9/D10).
	TeslaAPICalls int

	ChargingSessionsUpserted int
	ChargingFetchFailures    int
	ConfigCaptureFailures    int
}

// RunWriter persists one poll_runs row per app.ProcessVehicleData invocation. It
// is a separate port from Collector/Reader (RM36-telemetry-add-poll-runs design
// D12): its correctness is proven by a DATABASE_URL-gated integration test, not
// by an offline fake, so its implementation talks to telemetrydb.Queries directly
// rather than being routed through the store interface service.go/reader.go
// share (mirroring this module's own SuperchargerHistoryReader and internal/analytics'
// gapWriter — adding this to store would force every existing fake store test
// double to grow a stub method it never calls).
type RunWriter interface {
	// RecordRun persists one poll_runs row. Called exactly once per
	// app.ProcessVehicleData invocation (roadmap D6), after the run's end is
	// measured — on every path, including the step-1 whole-cycle-failure
	// short-circuit, so a failed run still leaves a row (roadmap D1). A second
	// call for the same run.RunID is a caller bug and fails on the PRIMARY
	// KEY (design D11) rather than silently upserting.
	RecordRun(ctx context.Context, run PollRun) error
}

// NewRunWriter constructs a RunWriter backed by a real Postgres pool. Callers
// (internal/app) depend on the RunWriter interface, never on the concrete type
// or on telemetrydb directly. Implementation is in run_writer.go — the forward
// declaration here mirrors NewSuperchargerHistoryReader's own pattern (design D12).
func NewRunWriter(pool *pgxpool.Pool) RunWriter {
	return newLoggingRunWriter(newRunWriter(pool))
}
