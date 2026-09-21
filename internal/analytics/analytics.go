// Package analytics is the platform's first derived-metrics module. It owns
// analytics computed FROM other modules' stored data, not the data itself: it
// derives and serves the precomputed vehicle_metrics figures — per-day
// battery-consumed, per-day odometer distance, per-day battery level, and
// each vehicle's latest status — through the Reader port below. It also
// owns the charge_gaps worklist, a live list of vehicle-days whose battery
// math does not add up.
//
// This module owns its own database (see reader.go and recalculate.go for
// the read/write split) plus reads reached through its sibling modules'
// public Reader ports. Domain types here carry NO vendor suffix — they are
// our own models (ai/architecture.md §6).
package analytics

import (
	"context"
	"time"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
	"github.com/cristianpena/magus-tesla-api/internal/vehicleref"
	"github.com/jackc/pgx/v5/pgxpool"
)

// GapReconciliationWindow is the rolling window cmd/poller re-derives and
// reconciles against this package's own charge_gaps ledger every nightly run (D4/D4a,
// D7b) — 30 days: generous enough to catch a manual-entry backfill days after
// the fact, cheap enough to recompute nightly (bounded by ~30 snapshot rows
// and a handful of charge rows per vehicle, per the read-heavy Performance-
// Profile's tolerance for off-hours write-path cost).
const GapReconciliationWindow = 30 * 24 * time.Hour

// Reader is the analytics module's public port (ai/go-conventions.md
// interface-first) — the only mandatory contract the gateway and sibling
// modules depend on.
type Reader interface {
	// ConsumedByDay returns the corrected per-day battery-consumed percentage
	// (D13) for the given vehicle over [start, end], both whole calendar days
	// represented as UTC-midnight time.Time, end inclusive (matching this
	// platform's HTTP date-filter convention).
	//
	// Which calendar day a row falls on is decided in the POLLER'S CONFIGURED
	// ZONE (telemetry.Config.Location, roadmap D6/D18), NOT in UTC: the day is
	// the row's own telemetry.Snapshot.CapturedDate minus one day, and
	// CapturedDate was stamped in that zone on the write path. The
	// UTC-midnight bounds above are a REPRESENTATION for a bare date, not a
	// bucketing zone -- see design.md D-B7. Note this differs from
	// internal/gateway/handlers/history.go's effectiveDayUTC, which still
	// buckets the odometer/battery charts in UTC; the mismatch is known and
	// accepted (D-B7). Do NOT re-bucket this method's Date through
	// effectiveDayUTC -- it is already a final bucket key.
	//
	// PRECOMPUTED, not recomputed on read (RM29-analytics-add-vehicle-metrics
	// design.md D-precompute, superseding this port's original "no cache"
	// contract): this method SELECTs the already-derived value from
	// vehicle_metrics, written by Recalculator.Recalculate/Reconcile at write
	// time. A read reflects the LAST recalculation, not the state at read
	// time -- the manual-charge write path (design.md D5) and the nightly
	// Reconcile call are what keep it current, not this method. The result is
	// SPARSE: it contains one entry per calendar day that has a computable
	// value, and NO entry for a day that does not (no vehicle_metrics row
	// exists for that day, OR a row exists but has no computable predecessor
	// -- design.md D13's IS NOT NULL filter excludes it just the same as no
	// row at all). Absence from the returned slice IS the "no data" signal
	// (design.md D-B2) -- callers bucket by Date exactly like
	// internal/gateway/handlers/history.go's existing
	// buildOdometerChart/buildBatteryChart already do for the odometer and
	// battery-level charts.
	//
	// This port performs no window-size validation or capping of its own
	// (mirrors telemetry.Reader.SnapshotsByVehicleBetween's identical
	// stance) -- keeping a window reasonable is the caller's job (the HTTP
	// handler in tier 4, the fixed GapReconciliationWindow in cmd/poller).
	//
	// Scoped by vehicle identity (teslaID) alone -- tesla_id already names one
	// vehicle uniquely, so no account identifier is taken or needed. A caller
	// must already have proven the requesting account owns this vehicle
	// before calling.
	ConsumedByDay(ctx context.Context, teslaID int64, start, end time.Time) ([]DayConsumption, error)

	// OdometerDeltaByDay returns, for the given vehicle and date range, the
	// per-calendar-day distance travelled together with that day's absolute
	// odometer reading (design.md D13, roadmap D5 -- the odometer chart's
	// delta/clamp logic relocated out of internal/gateway/handlers/history.go's
	// buildOdometerChart into this module). SELECTs from vehicle_metrics; a
	// day whose stored distance figure is negative (a clock-skew/odometer-read
	// anomaly) is reported as zero distance, never negative -- the ONLY place
	// this clamp is ever applied; the stored column itself stays raw and
	// unclamped. Same PRECOMPUTED and SPARSE contract as ConsumedByDay above
	// (one entry per day with a computable distance figure; a
	// vehicle_metrics row with no computable predecessor is excluded exactly
	// like ConsumedByDay excludes it, design.md D13). This port performs no
	// window-size validation or capping of its own, mirroring ConsumedByDay.
	//
	// Scoped by vehicle identity (teslaID) alone, same contract as
	// ConsumedByDay above: no account identifier is taken or needed, and a
	// caller must already have proven the requesting account owns this
	// vehicle before calling.
	OdometerDeltaByDay(ctx context.Context, teslaID int64, start, end time.Time) ([]DayDistance, error)

	// BatteryLevelByDay returns, for the given vehicle and date range, the
	// per-calendar-day battery-level percentage and estimated range exactly
	// as observed (RM40-analytics-add-battery-level-read design.md D2). Same
	// four-argument shape and [start, end] whole-calendar-day/UTC-midnight/
	// end-inclusive convention as ConsumedByDay/OdometerDeltaByDay above.
	// SELECTs from vehicle_metrics; PRECOMPUTED, not recomputed on read --
	// identical contract to this port's two siblings.
	//
	// THE ONE DELIBERATE DEPARTURE FROM ITS TWO SIBLINGS (design.md D3): this
	// method's underlying query carries NO "IS NOT NULL" filter, unlike
	// ConsumedByDay/OdometerDeltaByDay's own battery_used_pct_calc/
	// distance_traveled_km_calc filters. Those two filter out a
	// predecessor-less row because their columns are genuinely NULL when no
	// predecessor exists (design.md D9). battery_level_pct/battery_range_km
	// are declared NOT NULL -- raw per-day observations copied verbatim from
	// that day's own telemetry.Snapshot, with NO predecessor requirement at
	// all (exactly like the three other always-populated raw observations
	// vehicle_metrics already carries). So a vehicle's first-ever tracked
	// day, or any day immediately following a capture gap, still gets an
	// entry here even though the SAME day is excluded from ConsumedByDay's
	// and OdometerDeltaByDay's results. This is not an oversight -- omitting
	// the filter is correct, and adding one "for consistency" would silently
	// and wrongly hide real, fully-known data (design.md D3's rejected
	// alternative).
	//
	// SPARSE like its two siblings for the other reason -- a day with NO
	// vehicle_metrics row at all (the recalculator has not processed it yet)
	// yields no entry; absence IS the "no data" signal, never a fabricated
	// zero-valued DayBattery (design.md D6). Returns a non-nil, empty slice
	// (never bare nil) on the happy path with no matching rows.
	//
	// DayBattery.Date is a FINAL bucket key -- vehicle_metrics.metric_date,
	// the row's own already-effective calendar day, read back verbatim. A
	// caller must NEVER re-project it through
	// internal/gateway/handlers/history.go's effectiveDayUTC -- that
	// function converts a raw captured_at timestamp into an effective day, a
	// conversion metric_date has already had applied once, at Recalculate
	// time (design.md D4). Re-applying it a second time would shift the day
	// by one.
	//
	// This port performs no window-size validation or capping of its own,
	// mirroring ConsumedByDay/OdometerDeltaByDay, and issues no lookback of
	// its own (design.md D5) -- unlike the gateway's current
	// telemetry-backed battery read, no captured_at-to-effective-day
	// conversion happens against vehicle_metrics, so no extra day of data is
	// needed to produce an accurate [start, end] result.
	//
	// Scoped by vehicle identity (teslaID) alone, same contract as
	// ConsumedByDay/OdometerDeltaByDay above: no account identifier is taken
	// or needed, and a caller must already have proven the requesting
	// account owns this vehicle before calling.
	BatteryLevelByDay(ctx context.Context, teslaID int64, start, end time.Time) ([]DayBattery, error)

	// LatestMetricsForVehicles returns the latest precomputed vehicle_metrics row
	// for each vehicle in the given set, as VehicleStatus — the analytics-owned
	// equivalent of telemetry.Reader.LatestSnapshotsByVehicles (never
	// telemetry.Snapshot itself, ai/architecture.md §6). "Latest" means the row with
	// the greatest metric_date for that tesla_id — vehicle_metrics' grain is a
	// calendar day, not a capture instant, so this describes each vehicle's own
	// most recently RECALCULATED day, which is typically yesterday (metric_date is
	// the snapshot's effective day, recalculate.go). Exactly one result per vehicle
	// in the set, never a result for a vehicle outside the given set. If the set is
	// empty, or none of its vehicles has a stored vehicle_metrics row, this returns
	// an empty (non-nil) slice and a nil error, same contract as
	// LatestSnapshotsByVehicles. Order of the returned slice is unspecified.
	//
	// Takes []vehicleref.Ref, not a plain []int64: a Ref can only be built by
	// vehicleref.Authorize, vehicleref.All, or a _test.go file, so the caller
	// must already have proven every id in the set belongs to the requesting
	// account before calling — this method takes that proof as its input,
	// it does not perform the check itself.
	LatestMetricsForVehicles(ctx context.Context, refs []vehicleref.Ref) ([]VehicleStatus, error)
}

// Recalculator is the analytics module's write-path port
// (ai/go-conventions.md interface-first) -- the Collector-equivalent for
// this module's precomputed read model, vehicle_metrics
// (RM29-analytics-add-vehicle-metrics design.md D11). Implementations live in
// recalculate.go, mirroring Reader's own analytics.go-declares/reader.go-
// implements split. Called by the manual-charge write path (design.md D5,
// interim composition root internal/gateway/handlers/external_charges.go) and the
// nightly poller's per-vehicle reconciliation loop (design.md D7/D8,
// interim composition root cmd/poller) -- both interim arrangements per
// roadmap D-non-goals; a later tier relocates the CALL, not this logic.
type Recalculator interface {
	// Recalculate derives and persists one vehicle_metrics row for every
	// telemetry snapshot in [start, end] whose effective day falls in that
	// range (design.md D9/D10/D11 -- dense: a row is written even for a day
	// with no computable predecessor, carrying only that day's raw
	// observations). Fetches the same 1-day/2-day-widened lookback window
	// ConsumedByDay's reader.go used to fetch live before this tier, UPSERTs
	// every derived row, then deletes any existing vehicle_metrics row in
	// [start, end] whose metric_date is not among the rows just produced
	// (self-healing symmetry with GapWriter.ReconcileWindow's
	// UPSERT+DELETE shape). Idempotent: re-running over an unchanged window
	// produces a byte-identical UPSERT (design.md D4).
	//
	// Scoped by vehicle identity (teslaID) alone -- tesla_id already names
	// one vehicle uniquely, so no account identifier is taken or needed. A
	// caller must already have proven the requesting account owns this
	// vehicle before calling.
	Recalculate(ctx context.Context, teslaID int64, start, end time.Time) error

	// Reconcile reads each of the three independent per-source watermarks
	// (design.md D2/D3; a missing watermark is treated as the epoch, D7, so a
	// vehicle's first-ever Reconcile backfills that source's entire history),
	// queries each source for data updated at or after (cursor -
	// recalcOverlap) (D4's 24h commit-skew guard), derives the union affected
	// date range across every source that returned at least one row (coarse,
	// widened +/-1 day, clamped to yesterday -- D8), calls Recalculate once
	// for that window (skipped entirely when no source returned rows), then
	// advances each source's watermark whose query returned rows to the max
	// UpdatedAt/updated_at observed -- a source with zero returned rows
	// leaves its watermark untouched (Reconcile's own idempotence contract,
	// design.md's Test Contract).
	//
	// Scoped by vehicle identity (teslaID) alone, same contract as
	// Recalculate above: no account identifier is taken or needed, and a
	// caller must already have proven the requesting account owns this
	// vehicle before calling.
	Reconcile(ctx context.Context, teslaID int64) error
}

// DayConsumption is one calendar day's corrected battery-consumed result --
// our own domain model, no vendor suffix (ai/architecture.md §6).
type DayConsumption struct {
	// Date is the calendar day this entry describes -- the underlying
	// telemetry.Snapshot's own CapturedDate minus one calendar day, i.e. that
	// row's effective day computed in the poller's configured zone (roadmap
	// D6/D18, design.md D-B7). NEVER shifted or re-attributed to another row
	// (design.md D-B3, roadmap D1/D17). Represented as UTC midnight because
	// that is this platform's bare-calendar-date representation, NOT because
	// the day boundary is UTC. For a multi-day span
	// (DaysSpanned > 1), this is the single day the one available row
	// represents; every day between the predecessor and this one has no
	// entry at all (design.md D-B2/D-B3, roadmap D8).
	Date time.Time
	// ConsumedPct is battery_used_pct_calc + sum(end_battery_pct -
	// start_battery_pct) across every matched charge event from BOTH
	// sources (roadmap D13). Raw and unrounded; MAY be negative or exactly
	// zero -- callers decide how to render that (design.md D10 is a
	// gateway/tier-4 concern; this port never clamps or hides it).
	ConsumedPct float64
	// DistanceKm is the row's DistanceTraveledKmCalc (0 when telemetry
	// stored NULL for it -- see telemetry.go: only possible when
	// BatteryUsedPctCalc is also NULL, which this port already skips via
	// D5a, so DistanceKm is effectively always populated on an emitted
	// entry).
	DistanceKm float64
	// Flagged is the D5/D5a gap-detection result: true when ConsumedPct < 0,
	// or when it is exactly 0 while DistanceKm > minFlagDistanceKm.
	Flagged bool
	// MissingChargingType is the D7a inferred source, valid only when
	// Flagged is true (zero value "" otherwise). Uses the type above
	// directly -- no duplicate vocabulary (design.md D-B9).
	MissingChargingType MissingChargingType
	// DaysSpanned is the row's own DaysSpannedCalc (never re-derived) -- 1
	// for a normal night-to-night poll, >1 signals a multi-day span (D8) a
	// caller may want to mark distinctly.
	DaysSpanned int
}

// DayDistance is one calendar day's already-anomaly-clamped odometer
// distance result — our own domain model, no vendor suffix
// (ai/architecture.md §6). Backs OdometerDeltaByDay (design.md D13, roadmap
// D5).
type DayDistance struct {
	// Date is the calendar day this entry describes — vehicle_metrics'
	// metric_date for this row, same representation as DayConsumption.Date
	// (UTC-midnight bare calendar date).
	Date time.Time
	// KmDriven is the day's stored distance_traveled_km_calc, floored at
	// zero (roadmap D5's clamp, design.md D13) — a negative stored value (a
	// clock-skew/odometer-read anomaly) is reported as 0, never negative.
	// The stored column itself stays raw and unclamped; this is the ONLY
	// place the clamp is applied.
	KmDriven float64
	// OdometerKm is the day's absolute odometer reading (vehicle_metrics.
	// odometer_km), unclamped — there is nothing to clamp about an absolute
	// reading.
	OdometerKm float64
}

// DayBattery is one calendar day's raw battery-level observation — our own
// domain model, no vendor suffix (ai/architecture.md §6). Backs
// BatteryLevelByDay (RM40-analytics-add-battery-level-read design.md D2).
// Unlike DayConsumption/DayDistance, both fields here are ALWAYS populated
// for a day that has a vehicle_metrics row at all — they carry no
// predecessor requirement (design.md D3).
type DayBattery struct {
	// Date is the calendar day this entry describes — vehicle_metrics'
	// metric_date for this row, same representation as DayConsumption.Date/
	// DayDistance.Date (UTC-midnight bare calendar date). A FINAL bucket key
	// — see BatteryLevelByDay's doc comment for the effectiveDayUTC warning.
	Date time.Time
	// BatteryLevelPct is the day's stored battery_level_pct — a raw
	// per-day observation copied verbatim from that day's own
	// telemetry.Snapshot, NOT NULL regardless of predecessor existence
	// (design.md D3).
	BatteryLevelPct int
	// BatteryRangeKm is the day's stored battery_range_km — same
	// always-populated, no-predecessor-required contract as
	// BatteryLevelPct above.
	BatteryRangeKm float64
}

// VehicleStatus is the latest precomputed vehicle_metrics row for one vehicle
// — our own domain model, no vendor or sibling-module suffix
// (ai/architecture.md §6). Backs LatestMetricsForVehicles. Never
// telemetry.Snapshot and never an alias of it: this module maps
// telemetry-sourced values into vehicle_metrics once, at Recalculate-time,
// and VehicleStatus is built from that stored row, not a live pass-through.
type VehicleStatus struct {
	TeslaID         int64
	BatteryLevelPct int
	BatteryRangeKm  float64
	OdometerKm      float64
	// InsideTempC, OutsideTempC, Locked, SentryMode, CarVersion,
	// ChargingState, ChargeLimitSocPct, CapturedAt are all pointer-typed
	// because the underlying vehicle_metrics columns are nullable (design D2)
	// — nil means either "predates the RM38 migration" or, for SentryMode
	// only, possibly "not reported this capture" (the column's own
	// ambiguous-NULL caveat, design D2/D8). No fabricated default is ever
	// substituted for a nil value.
	InsideTempC       *float64
	OutsideTempC      *float64
	Locked            *bool
	SentryMode        *bool
	CarVersion        *string
	ChargingState     *string
	ChargeLimitSocPct *int
	CapturedAt        *time.Time
	// MaxRangeChargeCounter is the vehicle's LIFETIME count of charges to its
	// true 100% Maximum-Battery-Range limit, copied verbatim from the day's own
	// telemetry.Snapshot — monotonic across rows, never a per-day delta. Pointer
	// for the same reason as the fields above, but with a wider ambiguous NULL
	// (see the column comment on max_range_charge_counter): nil means either
	// "the vehicle did not report it" or "the row predates the column". A
	// reported 0 is a real value — never collapse it to nil.
	MaxRangeChargeCounter *int
	// TpmsPressureFLPSI/FR/RL/RR are the four tire-pressure raw observations
	// (RM50-analytics-add-tire-pressure-columns), copied verbatim from the day's own
	// telemetry.Snapshot -- already PSI, no conversion. Pointer for the same reason as
	// the fields above: nil means the vehicle did not report TPMS at capture, OR this
	// row predates the RM50 migration and was not touched by its one-off backfill
	// (design.md "NULL meaning").
	TpmsPressureFLPSI *float64
	TpmsPressureFRPSI *float64
	TpmsPressureRLPSI *float64
	TpmsPressureRRPSI *float64
	// DistanceTraveledKmCalc/ConsumedPct are the same two _calc columns
	// ConsumedByDay/OdometerDeltaByDay already read, newly exposed on this "latest
	// row" port (RM50-analytics-add-tire-pressure-columns design.md Part B). Pointer
	// because both are nullable: nil on a predecessor-less day (design.md D9), exactly
	// as documented on DayConsumption.ConsumedPct/DayDistance.KmDriven above.
	DistanceTraveledKmCalc *float64
	ConsumedPct            *float64
	// KmPerPctCalc is the day's driving efficiency in kilometres per battery
	// percent -- distance_traveled_km_calc divided by consumed_pct, computed by
	// deriveConsumption, not here. Pointer because nullable, under a STRICTER
	// rule than its two siblings above: nil on a predecessor-less day AND nil
	// whenever that day's consumed_pct is <= 0, because the division has no
	// meaning then (the divisor guard in consumption.go). Never substitute 0.
	//
	// MAG-81 changed the divisor from battery_used_pct_calc (the RAW battery
	// drop) to consumed_pct (that same drop plus the day's recorded charges).
	// A day the vehicle both drove and charged ends with a fuller pack, so the
	// raw figure was negative and this field was NULL even though the day had
	// a perfectly good efficiency. Now it is NULL only when the day's charge
	// was never recorded at all -- which is the case charge_gaps flags.
	KmPerPctCalc *float64
	// DistanceTraveledKmDeltaCalc/ConsumedPctDeltaCalc/KmPerPctDeltaCalc are
	// the three travel-progress day-over-day deltas: this day's value of the
	// named figure minus the value recalculation built for the row
	// immediately before it in the same pass. Pointer because nullable: nil
	// on a predecessor-less day, nil when either side of the subtraction is
	// itself nil, and additionally nil when this day was the first one a
	// given recalculation pass considered -- there is no earlier row in
	// that same pass to subtract from yet, even if an earlier day exists in
	// storage. A later pass whose window reaches that earlier day fills the
	// value in.
	DistanceTraveledKmDeltaCalc *float64
	ConsumedPctDeltaCalc        *float64
	KmPerPctDeltaCalc           *float64
	// TpmsPressureFLPSIDeltaCalc/FR/RL/RR are the four tyre-pressure
	// day-over-day deltas, one per wheel, in PSI: this day's raw reading
	// minus the previous day's. Pointer because nullable: nil when this day
	// has no predecessor at all, OR when either day's own raw wheel reading
	// is itself nil -- the same nil rule DistanceTraveledKmCalc/ConsumedPct
	// above already follow, NOT the raw-observation rule the four
	// TpmsPressure*PSI fields above follow. This delta partly reflects
	// ambient air temperature change (about 1 PSI per 5.5 degrees C), not
	// only a genuine pressure change -- accepted, not a defect. Never apply
	// a threshold or a target-pressure comparison to it.
	TpmsPressureFLPSIDeltaCalc *float64
	TpmsPressureFRPSIDeltaCalc *float64
	TpmsPressureRLPSIDeltaCalc *float64
	TpmsPressureRRPSIDeltaCalc *float64
}

// --- charge_gaps ledger (RM28-telemetry-add-charge-gap-storage, MAG-15;
// relocated here from internal/telemetry by RM29-analytics-own-charge-gaps,
// MAG-26 tier 5 — this module both computes AND stores the ledger now, so
// the vocabulary below describes a single owner, not a two-module split) ---

// MissingChargingType identifies which charge source a flagged charge_gaps
// day is attributed to (design D-Table6, roadmap D7a). Inferred by this
// module at detection time -- never user-chosen.
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
// this module's own derivation could not fully account for the day's
// battery change from stored charge records, meaning a charge record is
// missing or incomplete. Our own domain model, no vendor suffix
// (ai/architecture.md §6): internal/analytics both computes it and stores
// it through the GapWriter port below. It carries no AccountID: tesla_id
// already names one vehicle uniquely, and the table itself stores no
// account_id, so a field nothing validates, stores, or could read back
// would only invite a caller to assume it does something.
type ChargeGap struct {
	TeslaID int64
	VIN     string
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
	// which ai/go-conventions.md forbids. sqlc generates GapDate on the
	// analyticsdb row struct; the single mapping seam translates it, exactly as
	// this module already translates every other db row into a domain type.
	Date time.Time
	// MissingChargingType is which charge source is suspected missing for
	// this day, inferred at detection time (D7a).
	MissingChargingType MissingChargingType
}

// GapWriter is analytics' write port for the charge_gaps ledger (D3 of the
// RM28 roadmap). cmd/poller is its only caller: after Recalculator derives
// each day's consumption for a vehicle over a window and flags the days
// whose math does not add up (roadmap D5/D5a), the poller calls
// ReconcileWindow once per vehicle per nightly run with the FULL flagged set
// computed for that window.
type GapWriter interface {
	// ReconcileWindow makes charge_gaps agree with flagged for exactly the
	// vehicle-day range [start, end] inclusive (whole calendar days -- see
	// ChargeGap.Date): every day present in flagged is upserted (inserted, or
	// updated in place if its MissingChargingType or VIN changed since the
	// last run); every existing charge_gaps row for teslaID whose date falls
	// in [start, end] but has NO matching entry in flagged is deleted. Days
	// outside [start, end] are never read or touched, even if this vehicle
	// has older or newer flagged days stored elsewhere -- reconciliation is
	// scoped to exactly the window the caller just recomputed, never the
	// vehicle's whole history.
	//
	// flagged may be empty: every previously-flagged day in the window has
	// resolved, and every existing row in the window is deleted, none
	// re-inserted -- the normal steady state once a user fixes a missing
	// charge entry.
	//
	// Every element of flagged MUST carry the SAME teslaID as this call's own
	// argument; ReconcileWindow returns an error, and writes nothing, if one
	// does not (a mis-scoped entry is a caller bug, not data to silently
	// accept). Every element's Date MUST fall within [start, end];
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
	ReconcileWindow(ctx context.Context, teslaID int64, start, end time.Time, flagged []ChargeGap) error
}

// NewGapWriter constructs a GapWriter backed by a real Postgres pool. Callers
// (cmd/poller) depend on the GapWriter interface, never on the concrete type
// or on analyticsdb directly. Implementation is in gap_writer.go — declared
// here rather than there so this file compiles before that one is parsed.
// The returned value is wrapped so every call through the port is logged.
func NewGapWriter(pool *pgxpool.Pool) GapWriter {
	return newLoggingGapWriter(newGapWriter(pool))
}

// --- vehicle_monthly_metrics (one precomputed row per vehicle per month) ---

// EndingBatteryDist counts charge events by their ending battery
// percentage, across five fixed 20-point ranges: 0-20, 20-40, 40-60, 60-80,
// and 80-100.
type EndingBatteryDist struct {
	Bucket0To20   int `json:"0-20"`
	Bucket20To40  int `json:"20-40"`
	Bucket40To60  int `json:"40-60"`
	Bucket60To80  int `json:"60-80"`
	Bucket80To100 int `json:"80-100"`
}

// VehicleMonthlyMetrics is one precomputed month for one vehicle -- the row
// analytics.vehicle_monthly_metrics stores under (TeslaID, Period). Every
// numeric field holds a real number, never a sentinel: an empty month
// reports zero everywhere, and each bucket's own DayCount is what tells a
// genuine zero apart from a month with nothing to compute (see the table's
// own column comments in the migration for the full contract).
//
// Every field holds a real figure as of this port.
type VehicleMonthlyMetrics struct {
	TeslaID int64
	Period  time.Time // first day of the month

	AllDistanceKm   float64
	AllConsumedPct  float64
	AllKmPerPctCalc float64
	AllDayCount     int

	WeekdayDistanceKm   float64
	WeekdayConsumedPct  float64
	WeekdayKmPerPctCalc float64
	WeekdayDayCount     int

	WeekendDistanceKm   float64
	WeekendConsumedPct  float64
	WeekendKmPerPctCalc float64
	WeekendDayCount     int

	CapacityKWh      float64
	CapacityMeasured bool

	Currency string

	ExtACEnergyKWh         float64
	ExtACCost              float64
	ExtACEntryCount        int
	ExtACEndingBatteryDist EndingBatteryDist

	ExtDCEnergyKWh         float64
	ExtDCCost              float64
	ExtDCEntryCount        int
	ExtDCEndingBatteryDist EndingBatteryDist

	SCEnergyKWh         float64
	SCCost              float64
	SCSessionCount      int
	SCEndingBatteryDist EndingBatteryDist

	CreatedAt time.Time
	UpdatedAt time.Time
}

// MonthlySyncer derives and stores one vehicle_monthly_metrics row for one
// vehicle and one calendar month, from this module's own vehicle_metrics
// table plus the pack capacity charging measured for that month. Running it
// again for the same (teslaID, period) rewrites the row -- this is a sync,
// not an append, so any caller may re-run one month at any time to pick up
// a later edit to that month's telemetry or charging data.
type MonthlySyncer interface {
	// SyncMonth computes and upserts the row for teslaID and the calendar
	// month containing period -- only period's year and calendar month
	// matter; any day within that month gives the same result, matching
	// charging.MonthlyCapacityReader.CapacityForMonth's identical
	// any-day-in-month contract. Returns the row as stored.
	//
	// Every field reflects this month's real figures.
	SyncMonth(ctx context.Context, teslaID int64, period time.Time) (VehicleMonthlyMetrics, error)
}

// NewMonthlySyncer constructs a MonthlySyncer over the analytics module's
// own database pool plus the three sibling ports it reads: the pack capacity,
// the external charges, and the Supercharger sessions for the month. The
// implementation lives in monthly_sync.go. The returned value logs its
// single method -- see query_log.go.
func NewMonthlySyncer(pool *pgxpool.Pool, capacity charging.MonthlyCapacityReader, charges charging.Reader, supercharger charging.SuperchargerSessionAnalyticsReader) MonthlySyncer {
	return newLoggingMonthlySyncer(newMonthlySyncer(pool, capacity, charges, supercharger))
}
