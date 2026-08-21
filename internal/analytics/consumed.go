// File consumed.go owns the D13 per-day battery-consumed derivation --
// combining a vehicle's snapshot history with both charging-cost sources
// (Supercharger sessions, manual entries) into a corrected per-day consumed
// percentage, plus gap detection (D5/D5a) and inferred-source classification
// (D7a). Fully offline: no I/O, only plain telemetry.Snapshot /
// telemetry.SuperchargerSession / charging.Entry values in,
// []DayConsumption out (mirrors derive.go's zero-I/O style).
package analytics

import (
	"time"

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

// deriveConsumedByDay is the pure D13 derivation, fully offline: no I/O, only
// plain telemetry.Snapshot / telemetry.SuperchargerSession / charging.Entry
// values in, []DayConsumption out. snapshots MUST be ordered chronologically
// ascending and MUST include the one-day lookback row before start when it
// exists (D9a; ConsumedByDay's caller, reader.go, guarantees both, and widens
// every fetch by a day for the zone shift per design.md D-B13).
//
// start/end are inclusive bare calendar dates and are compared against each
// row's ZONED effective day (effectiveDay, design.md D-B7), which is why the
// caller may hand this function rows just outside [start, end] -- they are
// filtered here, against the same day definition the emitted Date carries.
func deriveConsumedByDay(snapshots []telemetry.Snapshot, sessions []telemetry.SuperchargerSession, entries []charging.Entry, start, end time.Time) []DayConsumption {
	out := make([]DayConsumption, 0, len(snapshots))
	for i := 1; i < len(snapshots); i++ {
		prev, cur := snapshots[i-1], snapshots[i]
		day := effectiveDay(cur)
		if day.Before(start) || day.After(end) {
			continue
		}
		if cur.BatteryUsedPctCalc == nil {
			continue // D5a: no predecessor claim for this row, skip
		}

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

		daysSpanned := 1
		if cur.DaysSpannedCalc != nil {
			daysSpanned = *cur.DaysSpannedCalc
		}

		out = append(out, DayConsumption{
			Date:                day,
			ConsumedPct:         consumed,
			DistanceKm:          distanceKm,
			Flagged:             flagged,
			MissingChargingType: missingType,
			DaysSpanned:         daysSpanned,
		})
	}
	return out
}
