// File consumed.go owns the D13 per-day battery-consumed derivation --
// combining a vehicle's snapshot history with both charging-cost sources
// (Supercharger sessions, manual entries) into a vehicleMetricRow per fetched
// snapshot day: the corrected per-day consumed percentage plus gap detection
// (D5/D5a) and inferred-source classification (D7a) for a day WITH a
// computable predecessor, or the day's raw observations alone (every derived
// field left NULL, flagged forced false) for a day WITHOUT one -- the
// dense-table revision (design.md D9/D10). Fully offline: no I/O, only plain
// telemetry.Snapshot / telemetry.SuperchargerSession / charging.Entry values
// in, []vehicleMetricRow out (mirrors derive.go's zero-I/O style).
package analytics

import (
	"time"

	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// minFlagDistanceKm -- see design.md D-B8.
//
// minFlagDistanceKm is the D5 gap-detection threshold: a day whose consumed
// pct computes to exactly zero is flagged only when the vehicle demonstrably
// drove more than this many km that day. Below it, "zero consumed, zero
// driven" is a plausible parked day, not a data gap.
const minFlagDistanceKm = 10.0

// vehicleMetricRow is deriveVehicleMetrics' output shape: every
// vehicle_metrics column except id/created_at/updated_at (design.md D9/D10).
// Recalculate (recalculate.go, design.md D11) is the only caller of
// deriveVehicleMetrics and the only place this type is converted to
// analyticsdb.UpsertVehicleMetricParams (mapping.go's pg* helpers).
type vehicleMetricRow struct {
	AccountID  uuid.UUID
	TeslaID    int64
	MetricDate time.Time

	// Duplicated raw observations (roadmap D1, D9) -- always populated from
	// the day's own snapshot, independent of predecessor existence.
	BatteryLevelPct int
	OdometerKm      float64
	BatteryRangeKm  float64

	// The five _calc columns tier 4 needs a home for (D9) -- copied verbatim
	// from telemetry.Snapshot's already-computed pointer fields, no
	// re-derivation. nil iff this row's day has no locally-available
	// predecessor snapshot.
	DistanceTraveledKmCalc *float64
	BatteryUsedPctCalc     *int
	KmPerPctCalc           *float64
	EstimatedRangeKmCalc   *float64
	DaysSpannedCalc        *int

	// D13 corrected consumption. ConsumedPct is nil under the identical
	// "no predecessor" condition as BatteryUsedPctCalc -- there is no raw
	// delta to correct. Flagged is NEVER a pointer: it is always false (not
	// unknown) on a predecessor-less row -- the D5/D5a comparison never runs
	// for such a row (design.md D9's dedicated rationale).
	ConsumedPct         *float64
	Flagged             bool
	MissingChargingType telemetry.MissingChargingType // "" maps to SQL NULL (mapping.go)
}

// calendarDay normalizes an already-bare calendar date to this platform's
// date representation: UTC midnight, no time-of-day component (the
// pgtype.Date convention that telemetry.Snapshot.CapturedDate and
// charging.Entry.ChargedOn already arrive in, and the shape of
// ConsumedByDay's own start/end parameters).
//
// This is NOT a timezone conversion and NOT a bucketing decision: it never
// moves a value across a day boundary, it only strips a stray time-of-day
// component. The zone that decides day boundaries is the poller's
// Config.Location -- see effectiveDay below and design.md D-B7
// ("Representation vs. zone").
func calendarDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// effectiveDay returns the calendar day snapshot s DESCRIBES: its own
// CapturedDate minus one calendar day. The nightly poller runs at ~03:30 and
// captures the state accumulated over the PRIOR day, so the row's day is one
// before its capture day.
//
// The zone is the poller's configured Config.Location (roadmap D6, owner
// ruling D18), and it arrives here already applied: telemetry stamps
// CapturedDate on the write path via dateOnly(capturedAt, loc)
// (internal/telemetry/service.go), which is the single place that zone
// decides a snapshot's calendar day. This module therefore needs no
// *time.Location of its own (design.md D-B12).
//
// Deliberately does NOT read s.EffectiveDate: that field is derived as
// CapturedAt.AddDate(0,0,-1) with CapturedAt in UTC (telemetry/mapping.go), so
// its calendar day is a UTC answer, and a bare date cannot be re-zoned after
// the fact (design.md D-B7). AddDate is calendar-day arithmetic, not a 24h
// duration, so DST cannot shift it.
//
// Invariant, asserted by test (m): for any consecutive pair,
// effectiveDay(cur) - effectiveDay(prev) == *cur.DaysSpannedCalc, because
// telemetry derives DaysSpannedCalc from the same two CapturedDate values.
func effectiveDay(s telemetry.Snapshot) time.Time {
	return calendarDay(s.CapturedDate).AddDate(0, 0, -1)
}

// sumSuperchargerPctBetween sums (EndBatteryPct - StartBatteryPct) across
// every session whose ChargeStopDateTime falls in [from, to) -- D12's
// interval rule, generalized to any predecessor/current CapturedAt pair
// (design.md D-B5). Sessions with either percentage NULL (D14: always true
// today) contribute 0 -- their absence is what inferMissingChargingType
// flags below, not something this function should estimate.
func sumSuperchargerPctBetween(sessions []telemetry.SuperchargerSession, from, to time.Time) float64 {
	var total float64
	for _, s := range sessions {
		if s.ChargeStopDateTime.Before(from) || !s.ChargeStopDateTime.Before(to) {
			continue
		}
		if s.StartBatteryPct != nil && s.EndBatteryPct != nil {
			total += float64(*s.EndBatteryPct - *s.StartBatteryPct)
		}
	}
	return total
}

// inferMissingChargingType implements D7a: SUPERCHARGER when a session in
// [from, to) exists with either battery percentage NULL (the exact record
// needing a fill is already known); MANUAL otherwise. Only called once a day
// is already known to be Flagged.
func inferMissingChargingType(sessions []telemetry.SuperchargerSession, from, to time.Time) telemetry.MissingChargingType {
	for _, s := range sessions {
		if s.ChargeStopDateTime.Before(from) || !s.ChargeStopDateTime.Before(to) {
			continue
		}
		if s.StartBatteryPct == nil || s.EndBatteryPct == nil {
			return telemetry.MissingChargingTypeSupercharger
		}
	}
	return telemetry.MissingChargingTypeManual
}

// sumManualPctBetween sums BatteryDelta() across every manual entry whose
// calendarDay(ChargedOn) falls in (fromDay, toDay] -- design.md D-B6's
// exclusive-start/inclusive-end range, generalized from the single-day D12
// rule to cover multi-day spans without a second code path. fromDay/toDay
// are the caller's zoned effectiveDay values, not raw snapshot timestamps.
// Entries with either battery percentage NULL contribute 0 (BatteryDelta()
// returns nil in that case).
func sumManualPctBetween(entries []charging.Entry, fromDay, toDay time.Time) float64 {
	var total float64
	for _, e := range entries {
		day := calendarDay(e.ChargedOn)
		if !day.After(fromDay) || day.After(toDay) {
			continue
		}
		if delta := e.BatteryDelta(); delta != nil {
			total += float64(*delta)
		}
	}
	return total
}

// deriveVehicleMetrics is the pure D9/D10/D13 derivation, fully offline: no
// I/O, only plain telemetry.Snapshot / telemetry.SuperchargerSession /
// charging.Entry values in, []vehicleMetricRow out. snapshots MUST be
// ordered chronologically ascending and MUST include the one-day lookback
// row before start when it exists (D9a; Recalculate, recalculate.go,
// guarantees both, widening every fetch by a day for the zone shift per
// design.md D-B13/D11).
//
// start/end are inclusive bare calendar dates and are compared against each
// row's ZONED effective day (effectiveDay, design.md D-B7), which is why the
// caller may hand this function rows just outside [start, end] -- they are
// filtered here, against the same day definition the emitted MetricDate
// carries.
//
// Loop shape (design.md D10, dense-table revision): iterates EVERY fetched
// index i := 0 to len(snapshots)-1 -- unlike the pre-dense deriveConsumedByDay
// this replaces, which started at i := 1 and so structurally never visited
// snapshots[0] as cur at all. For i == 0 (no local prev available in the
// fetched slice) OR cur.BatteryUsedPctCalc == nil (telemetry itself recorded
// no predecessor for this row, D5a's original signal, kept as a second,
// independent check for defense-in-depth), the emitted row carries only the
// day's raw observations from cur, with flagged forced false and every
// derived/consumed field left nil -- the D5/D5a flag comparison never runs
// for this row (design.md D9's dedicated rationale: a stored 0 would falsely
// flag a vehicle's first day as a suspected charge gap). For every other row,
// the row is computed EXACTLY as the pre-dense derivation computed it
// (unchanged formulas, unchanged charge-event matching against
// prev.CapturedAt/effectiveDay(prev)), additionally carrying
// cur.BatteryLevelPct, cur.OdometerKm, cur.BatteryRangeKm, cur.KmPerPctCalc,
// cur.EstimatedRangeKmCalc, and cur.DaysSpannedCalc copied verbatim (no
// fallback-to-1 -- D9's "copied verbatim, no re-derivation" instruction; the
// old fallback-to-1 local variable is gone, not reproduced here).
func deriveVehicleMetrics(snapshots []telemetry.Snapshot, sessions []telemetry.SuperchargerSession, entries []charging.Entry, start, end time.Time) []vehicleMetricRow {
	out := make([]vehicleMetricRow, 0, len(snapshots))
	for i := 0; i < len(snapshots); i++ {
		cur := snapshots[i]
		day := effectiveDay(cur)
		if day.Before(start) || day.After(end) {
			continue
		}

		if i == 0 || cur.BatteryUsedPctCalc == nil {
			// D5a/D9: no predecessor claim for this row -- persist the day's raw
			// observations only. Every derived/consumed field stays nil (-> SQL
			// NULL). flagged is forced false here, never left to a stray
			// zero-value comparison against distance.
			out = append(out, vehicleMetricRow{
				AccountID:       cur.AccountID,
				TeslaID:         cur.TeslaID,
				MetricDate:      day,
				BatteryLevelPct: cur.BatteryLevelPct,
				OdometerKm:      cur.OdometerKm,
				BatteryRangeKm:  cur.BatteryRangeKm,
				Flagged:         false,
			})
			continue
		}

		prev := snapshots[i-1]

		chargePct := sumSuperchargerPctBetween(sessions, prev.CapturedAt, cur.CapturedAt) +
			sumManualPctBetween(entries, effectiveDay(prev), day)

		consumed := float64(*cur.BatteryUsedPctCalc) + chargePct

		var distanceKm float64
		if cur.DistanceTraveledKmCalc != nil {
			distanceKm = *cur.DistanceTraveledKmCalc
		}

		flagged := consumed < 0 || (consumed == 0 && distanceKm > minFlagDistanceKm)

		var missingType telemetry.MissingChargingType
		if flagged {
			missingType = inferMissingChargingType(sessions, prev.CapturedAt, cur.CapturedAt)
		}

		out = append(out, vehicleMetricRow{
			AccountID:              cur.AccountID,
			TeslaID:                cur.TeslaID,
			MetricDate:             day,
			BatteryLevelPct:        cur.BatteryLevelPct,
			OdometerKm:             cur.OdometerKm,
			BatteryRangeKm:         cur.BatteryRangeKm,
			DistanceTraveledKmCalc: cur.DistanceTraveledKmCalc,
			BatteryUsedPctCalc:     cur.BatteryUsedPctCalc,
			KmPerPctCalc:           cur.KmPerPctCalc,
			EstimatedRangeKmCalc:   cur.EstimatedRangeKmCalc,
			DaysSpannedCalc:        cur.DaysSpannedCalc,
			ConsumedPct:            &consumed,
			Flagged:                flagged,
			MissingChargingType:    missingType,
		})
	}
	return out
}
