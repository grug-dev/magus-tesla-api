// Package battery is the platform's first derived-metrics module. It owns
// analytics computed FROM other modules' stored data, not the data itself: its
// one metric today is a rolling energy-per-kilometre (Wh/km) efficiency figure
// over a fixed window, derived from internal/telemetry's snapshot history plus
// the two charging-cost sources the platform stores (internal/telemetry's
// SuperchargerReader and internal/manualcharge), corrected for pack capacity via
// a small in-package reference table keyed on the vehicle's car_type.
//
// This module owns no database and no store — it is a pure read-side derivation
// reached exclusively through its sibling modules' public Reader ports (see
// reader.go). Domain types here carry NO vendor suffix — they are our own models
// (ai/architecture.md §6). Full design rationale:
// openspec/changes/battery-add-efficiency-metric/design.md.
package battery

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// DefaultWindow is the recommended NewReader window — 30 days, matching the
// dashboard's 30-day history cards (design.md D3). Deployment code may pass a
// different duration for a future analytics page without changing the port.
const DefaultWindow = 30 * 24 * time.Hour

// GapReconciliationWindow is the rolling window cmd/poller re-derives and
// reconciles against telemetry's charge_gaps ledger every nightly run (D4/D4a,
// D7b) — 30 days: generous enough to catch a manual-entry backfill days after
// the fact, cheap enough to recompute nightly (bounded by ~30 snapshot rows
// and a handful of charge rows per vehicle, per the read-heavy Performance-
// Profile's tolerance for off-hours write-path cost).
const GapReconciliationWindow = 30 * 24 * time.Hour

// Reader is the battery module's public port (ai/go-conventions.md
// interface-first) — the only mandatory contract the gateway and sibling
// modules depend on.
type Reader interface {
	// RecentEfficiency returns the rolling energy-per-kilometre for the given
	// vehicle over the window NewReader was constructed with, derived from
	// stored telemetry plus the two charging-cost sources this platform
	// stores. It returns ok=false (no error) when there is not enough data to
	// compute a meaningful value (design.md D-ok) — never a fabricated
	// number.
	RecentEfficiency(ctx context.Context, accountID uuid.UUID, teslaID int64) (Efficiency, bool, error)

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
	// Recomputed on every call -- no cache, no
	// stored state (D2). The result is SPARSE: it contains one entry per
	// calendar day that has a computable value, and NO entry for a day that
	// does not (no snapshot exists for that day, or the day is the vehicle's
	// very first-ever snapshot -- D5a). Absence from the returned slice IS
	// the "no data" signal (design.md D-B2) -- callers bucket by Date exactly
	// like internal/gateway/handlers/history.go's existing
	// buildOdometerChart/buildBatteryChart already do for the odometer and
	// battery-level charts.
	//
	// This port performs no window-size validation or capping of its own
	// (mirrors telemetry.Reader.SnapshotsByVehicleBetween's identical
	// stance) -- keeping a window reasonable is the caller's job (the HTTP
	// handler in tier 4, the fixed GapReconciliationWindow in cmd/poller).
	ConsumedByDay(ctx context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) ([]DayConsumption, error)
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
	// Flagged is true (zero value "" otherwise). Reuses
	// telemetry.MissingChargingType directly -- no duplicate vocabulary
	// (design.md D-B9).
	MissingChargingType telemetry.MissingChargingType
	// DaysSpanned is the row's own DaysSpannedCalc (never re-derived) -- 1
	// for a normal night-to-night poll, >1 signals a multi-day span (D8) a
	// caller may want to mark distinctly.
	DaysSpanned int
}

// Efficiency is one computed rolling-efficiency result — our own domain model,
// no vendor suffix (ai/architecture.md §6). FromKm/ToKm are read directly from
// telemetry.Snapshot.OdometerKm — already km-native at capture time
// (telemetry-store-display-units design D1/D3) — so this module performs no
// unit conversion and no further Km() companion applies here.
type Efficiency struct {
	// WhPerKm is the derived energy-per-kilometre figure in watt-hours,
	// raw and unrounded (design.md D5) — the gateway formats it for display.
	WhPerKm float64
	// FromKm is the odometer reading at the window's start, in km.
	FromKm float64
	// ToKm is the odometer reading at the window's end, in km.
	ToKm float64
	// BatteryDeltaPct is the net state-of-charge change over the window:
	// start − end. Negative means the vehicle net-charged over the window.
	BatteryDeltaPct float64
	// Approximate is true when the vehicle's pack capacity was unknown and
	// the SoC-drift correction term was dropped (design.md D1b) — the result
	// is still a real computed value, never a fabricated one.
	Approximate bool
}
