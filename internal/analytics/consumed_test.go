package analytics

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// The consumed tests exercise deriveVehicleMetrics and its helpers fully OFFLINE and
// with zero fakes: all are pure functions over plain telemetry.Snapshot /
// charging.Session / charging.Entry values (design.md D-B1 through D-B13, D9/D10),
// so there is no I/O seam to fake -- mirrors derive_test.go's own zero-fakes style.
// Expected values are hand-computed from design.md's Test Contract (authored before
// this implementation existed), never derived by reading consumed.go itself.
//
// Per the Test-Execution-Policy, these tests are written but NOT run by the worker; go
// vet ./... compiles them as a signature-drift signal. The owner runs
// `go test ./internal/analytics/...` and reports the result.
//
// RM29-telemetry-drop-derived-columns (MAG-26 tier 4, this change) rewrote every test
// below for two reasons: (1) telemetry.Snapshot no longer carries the five _calc
// fields (DistanceTraveledKmCalc, BatteryUsedPctCalc, KmPerPctCalc,
// EstimatedRangeKmCalc, DaysSpannedCalc) -- they are deleted by telemetry's own wave 4
// -- so every fixture below supplies ONLY raw observations (OdometerKm,
// BatteryLevelPct, CapturedDate, CapturedAt) and lets deriveConsumption
// (consumption.go) compute the five figures, the same way production now does; and
// (2) deriveVehicleMetrics gained a leading `preceding *telemetry.Snapshot` parameter
// (design.md D7). Every existing call site below passes `nil` for `preceding`: in
// every one of these fixtures the lookback ("prev") snapshot sits at array index 0
// with an effective day OUTSIDE the test's [start, end] window, so it is filtered by
// the day-bounds check before the prev==nil branch is ever reached -- preceding is
// therefore never consulted, identically to the pre-existing behaviour. Only the new
// Fixture D / Fixture D2 tests below exercise `preceding` for real, matching design.md
// D7/D8b.
//
// D6 retirement note: the pre-existing
// TestDeriveVehicleMetrics_NilBatteryUsedPctCalc_RawObservationsOnly is DELETED, not
// ported. It exercised the OLD, now-removed half of the predecessor-less condition
// (`i == 0 || cur.BatteryUsedPctCalc == nil`) by setting a local array predecessor
// (i >= 1) while independently forcing cur.BatteryUsedPctCalc to nil -- a scenario
// design.md D6 explains is no longer constructible: deriveVehicleMetrics never reads a
// BatteryUsedPctCalc field off cur any more (the field itself is gone), it always
// derives the figure itself whenever a predecessor snapshot is available. D6 states
// this explicitly: "the removed check and the retained one were expected to co-occur
// ... the retained one is now the STRONGER of the two". The one remaining
// "predecessor-less" trigger -- prev == nil -- is fully covered by
// TestDeriveVehicleMetrics_FixtureC below (i == 0, preceding == nil), so no coverage
// is lost.

// day returns a bare calendar date at UTC midnight -- this platform's date representation
// (the pgtype.Date convention; see consumed.go's effectiveDay doc comment).
func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// mustFloat dereferences a *float64 that a given scenario guarantees is non-nil
// (a day WITH a computable predecessor, design.md D9), failing the test immediately
// if it is nil -- every ported (a)-(n) scenario below asserts against a row that has
// a predecessor, so a nil here is the bug under test, not a case to tolerate.
func mustFloat(t *testing.T, v *float64) float64 {
	t.Helper()
	if v == nil {
		t.Fatal("want non-nil *float64, got nil")
	}
	return *v
}

// boolPtr returns a pointer to v -- this package's *bool fixture builder,
// mirroring intPtr (derive_test.go) for the RM38-analytics-add-vehicle-status-columns
// tests below, which need to set telemetry.Snapshot.SentryMode (*bool).
func boolPtr(v bool) *bool { return &v }

// TestDeriveVehicleMetrics_SingleSessionSingleDay_MatchesRoadmapExample covers
// design.md Test Contract (a) -- the roadmap's own verified worked example: one
// Supercharger session inside a single day's window, expect ConsumedPct = 11.
// prev/cur battery levels (22 -> 73) are chosen so deriveConsumption computes the
// same raw BatteryUsedPctCalc (-51) the pre-move fixture hardcoded.
func TestDeriveVehicleMetrics_SingleSessionSingleDay_MatchesRoadmapExample(t *testing.T) {
	d0 := day(2026, 8, 13)                              // prev's effective day (outside the window -- lookback pairing only)
	d1 := day(2026, 8, 14)                              // cur's effective day
	t0 := time.Date(2026, 8, 14, 8, 30, 0, 0, time.UTC) // prev.CapturedAt
	t1 := time.Date(2026, 8, 15, 8, 30, 0, 0, time.UTC) // cur.CapturedAt

	prev := telemetry.Snapshot{CapturedAt: t0, CapturedDate: d0.AddDate(0, 0, 1), BatteryLevelPct: 22}
	cur := telemetry.Snapshot{CapturedAt: t1, CapturedDate: d1.AddDate(0, 0, 1), BatteryLevelPct: 73} // raw delta 22-73 = -51

	sessions := []charging.Session{
		{ChargeStopDateTime: t0.Add(6 * time.Hour), StartBatteryPct: intPtr(18), EndBatteryPct: intPtr(80)},
	}

	got := deriveVehicleMetrics(nil, []telemetry.Snapshot{prev, cur}, sessions, nil, d1, d1)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry, got %d: %+v", len(got), got)
	}
	entry := got[0]

	if !entry.MetricDate.Equal(d1) {
		t.Errorf("MetricDate: want %v, got %v", d1, entry.MetricDate)
	}
	wantConsumed := -51.0 + (80.0 - 18.0) // 11
	if !approxEqual(mustFloat(t, entry.ConsumedPct), wantConsumed) {
		t.Errorf("ConsumedPct: want %v, got %v", wantConsumed, entry.ConsumedPct)
	}
	if entry.Flagged {
		t.Error("want Flagged=false")
	}
}

// TestDeriveVehicleMetrics_TwoSessionsSameDay_SumsBoth_Not5 covers design.md Test
// Contract (b) -- the roadmap's own regression guard: two Supercharger sessions in the
// same day's window must be SUMMED (D13), not collapsed to the latest session's delta
// alone (-25) or to a first-start-to-last-end span (15). The correct result is 5.
func TestDeriveVehicleMetrics_TwoSessionsSameDay_SumsBoth_Not5(t *testing.T) {
	d0 := day(2026, 8, 13)
	d1 := day(2026, 8, 14)
	t0 := time.Date(2026, 8, 14, 8, 30, 0, 0, time.UTC)
	t1 := time.Date(2026, 8, 15, 8, 30, 0, 0, time.UTC)

	prev := telemetry.Snapshot{CapturedAt: t0, CapturedDate: d0.AddDate(0, 0, 1), BatteryLevelPct: 30}
	cur := telemetry.Snapshot{CapturedAt: t1, CapturedDate: d1.AddDate(0, 0, 1), BatteryLevelPct: 75} // raw delta 30-75 = -45

	sessions := []charging.Session{
		{ChargeStopDateTime: t0.Add(4 * time.Hour), StartBatteryPct: intPtr(20), EndBatteryPct: intPtr(50)}, // earlier, +30
		{ChargeStopDateTime: t0.Add(8 * time.Hour), StartBatteryPct: intPtr(60), EndBatteryPct: intPtr(80)}, // later, +20
	}

	got := deriveVehicleMetrics(nil, []telemetry.Snapshot{prev, cur}, sessions, nil, d1, d1)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry, got %d: %+v", len(got), got)
	}

	gotConsumed := mustFloat(t, got[0].ConsumedPct)
	wantConsumed := -45.0 + (30.0 + 20.0) // 5 -- sum of both sessions
	wrongLatestOnly := -45.0 + 20.0       // -25 -- only the later session's delta
	wrongFirstToLastEnd := -45.0 + 60.0   // 15 -- first session's start to last session's end

	if !approxEqual(gotConsumed, wantConsumed) {
		t.Errorf("ConsumedPct: want %v (sum of both sessions), got %v", wantConsumed, gotConsumed)
	}
	if approxEqual(gotConsumed, wrongLatestOnly) {
		t.Errorf("ConsumedPct equals the latest-session-only value (%v) -- not summing every session", wrongLatestOnly)
	}
	if approxEqual(gotConsumed, wrongFirstToLastEnd) {
		t.Errorf("ConsumedPct equals the first-start-to-last-end value (%v) -- not summing every session", wrongFirstToLastEnd)
	}
}

// TestDeriveVehicleMetrics_NegativeFlagged covers design.md Test Contract (c): a
// negative corrected consumption with no charge events matched must flag, inferring
// MANUAL (no Supercharger session present in the interval).
func TestDeriveVehicleMetrics_NegativeFlagged(t *testing.T) {
	d0 := day(2026, 8, 13)
	d1 := day(2026, 8, 14)
	t0 := time.Date(2026, 8, 14, 8, 30, 0, 0, time.UTC)
	t1 := time.Date(2026, 8, 15, 8, 30, 0, 0, time.UTC)

	prev := telemetry.Snapshot{CapturedAt: t0, CapturedDate: d0.AddDate(0, 0, 1), BatteryLevelPct: 50}
	cur := telemetry.Snapshot{CapturedAt: t1, CapturedDate: d1.AddDate(0, 0, 1), BatteryLevelPct: 60} // raw delta 50-60 = -10

	got := deriveVehicleMetrics(nil, []telemetry.Snapshot{prev, cur}, nil, nil, d1, d1)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry, got %d", len(got))
	}
	entry := got[0]

	if !approxEqual(mustFloat(t, entry.ConsumedPct), -10) {
		t.Errorf("ConsumedPct: want -10, got %v", entry.ConsumedPct)
	}
	if !entry.Flagged {
		t.Error("want Flagged=true")
	}
	if entry.MissingChargingType != MissingChargingTypeManual {
		t.Errorf("MissingChargingType: want MANUAL, got %v", entry.MissingChargingType)
	}
}

// TestDeriveVehicleMetrics_ZeroWithDistanceFlagged covers design.md Test Contract (d):
// a day whose corrected ConsumedPct computes to exactly 0 must flag when the vehicle
// demonstrably drove more than minFlagDistanceKm that day.
func TestDeriveVehicleMetrics_ZeroWithDistanceFlagged(t *testing.T) {
	d0 := day(2026, 8, 13)
	d1 := day(2026, 8, 14)
	t0 := time.Date(2026, 8, 14, 8, 30, 0, 0, time.UTC)
	t1 := time.Date(2026, 8, 15, 8, 30, 0, 0, time.UTC)

	prev := telemetry.Snapshot{CapturedAt: t0, CapturedDate: d0.AddDate(0, 0, 1), BatteryLevelPct: 30, OdometerKm: 1000.0}
	cur := telemetry.Snapshot{CapturedAt: t1, CapturedDate: d1.AddDate(0, 0, 1), BatteryLevelPct: 50, OdometerKm: 1050.0} // raw delta 30-50 = -20; distance 50.0

	entries := []charging.Entry{
		{ChargedOn: d1, StartBatteryPct: intPtr(30), EndBatteryPct: intPtr(50)}, // +20
	}

	got := deriveVehicleMetrics(nil, []telemetry.Snapshot{prev, cur}, nil, entries, d1, d1)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry, got %d", len(got))
	}
	entry := got[0]

	if !approxEqual(mustFloat(t, entry.ConsumedPct), 0) {
		t.Errorf("ConsumedPct: want 0, got %v", entry.ConsumedPct)
	}
	if !entry.Flagged {
		t.Error("want Flagged=true: ConsumedPct=0 with DistanceKm=50.0 > minFlagDistanceKm")
	}
	if entry.MissingChargingType != MissingChargingTypeManual {
		t.Errorf("MissingChargingType: want MANUAL, got %v", entry.MissingChargingType)
	}
}

// TestDeriveVehicleMetrics_ZeroWithLowDistanceNotFlagged covers design.md Test
// Contract (e): same zero-ConsumedPct setup as (d), but at or below minFlagDistanceKm
// must NOT flag. The threshold comparison is strict '>', so exactly 10.0 must NOT flag.
func TestDeriveVehicleMetrics_ZeroWithLowDistanceNotFlagged(t *testing.T) {
	d0 := day(2026, 8, 13)
	d1 := day(2026, 8, 14)
	t0 := time.Date(2026, 8, 14, 8, 30, 0, 0, time.UTC)
	t1 := time.Date(2026, 8, 15, 8, 30, 0, 0, time.UTC)

	cases := []struct {
		name       string
		distanceKm float64
	}{
		{"exactly at threshold (10.0) -- strict '>' means not flagged", 10.0},
		{"well under threshold (5.0)", 5.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prev := telemetry.Snapshot{CapturedAt: t0, CapturedDate: d0.AddDate(0, 0, 1), BatteryLevelPct: 30, OdometerKm: 1000.0}
			cur := telemetry.Snapshot{CapturedAt: t1, CapturedDate: d1.AddDate(0, 0, 1), BatteryLevelPct: 50, OdometerKm: 1000.0 + tc.distanceKm}
			entries := []charging.Entry{
				{ChargedOn: d1, StartBatteryPct: intPtr(30), EndBatteryPct: intPtr(50)}, // +20 -> ConsumedPct = 0
			}

			got := deriveVehicleMetrics(nil, []telemetry.Snapshot{prev, cur}, nil, entries, d1, d1)
			if len(got) != 1 {
				t.Fatalf("want exactly 1 entry, got %d", len(got))
			}
			if got[0].Flagged {
				t.Errorf("want Flagged=false at DistanceKm=%v", tc.distanceKm)
			}
		})
	}
}

// TestDeriveVehicleMetrics_MultiDaySpan_OneEntry covers design.md Test Contract (g) /
// roadmap D8: a multi-day gap between snapshots produces exactly ONE bar, dated the
// later snapshot's own effective day (design.md D-B3), with every charge event inside
// the span summed regardless of which intervening day it falls on. No entry exists for
// any intervening day -- there is no snapshot row for them to attach to.
func TestDeriveVehicleMetrics_MultiDaySpan_OneEntry(t *testing.T) {
	prevDay := day(2026, 8, 10)
	curDay := day(2026, 8, 13)
	t0 := time.Date(2026, 8, 11, 8, 30, 0, 0, time.UTC)
	t1 := time.Date(2026, 8, 14, 8, 30, 0, 0, time.UTC)

	prev := telemetry.Snapshot{CapturedAt: t0, CapturedDate: prevDay.AddDate(0, 0, 1), BatteryLevelPct: 20}
	cur := telemetry.Snapshot{CapturedAt: t1, CapturedDate: curDay.AddDate(0, 0, 1), BatteryLevelPct: 50} // raw delta 20-50 = -30; days spanned 3

	sessions := []charging.Session{
		{ChargeStopDateTime: t0.Add(24 * time.Hour), StartBatteryPct: intPtr(40), EndBatteryPct: intPtr(55)}, // +15
	}
	entries := []charging.Entry{
		{ChargedOn: day(2026, 8, 12), StartBatteryPct: intPtr(20), EndBatteryPct: intPtr(45)}, // +25
	}

	// start is the day AFTER prev's own effective day (2026-08-10) -- excludes prev's
	// i==0 raw-observation row from this window, matching Recalculate's real call
	// shape (D11: the [start, end] arg passed to deriveVehicleMetrics is never
	// widened, only the FETCH is). end stays broad to keep exercising the
	// no-entry-for-intervening-days assertion below.
	start := prevDay.AddDate(0, 0, 1) // 2026-08-11
	end := day(2026, 8, 31)

	got := deriveVehicleMetrics(nil, []telemetry.Snapshot{prev, cur}, sessions, entries, start, end)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry (not one per intervening day), got %d: %+v", len(got), got)
	}
	entry := got[0]

	if !entry.MetricDate.Equal(curDay) {
		t.Errorf("MetricDate: want %v (the row's own effective day, never re-attributed), got %v", curDay, entry.MetricDate)
	}
	if entry.DaysSpannedCalc == nil || *entry.DaysSpannedCalc != 3 {
		t.Errorf("DaysSpannedCalc: want 3, got %v", entry.DaysSpannedCalc)
	}
	wantConsumed := -30.0 + 40.0 // -30 + 15 (Supercharger) + 25 (manual)
	if !approxEqual(mustFloat(t, entry.ConsumedPct), wantConsumed) {
		t.Errorf("ConsumedPct: want %v, got %v", wantConsumed, entry.ConsumedPct)
	}
	if entry.Flagged {
		t.Error("want Flagged=false")
	}

	for _, e := range got {
		if e.MetricDate.Equal(day(2026, 8, 11)) || e.MetricDate.Equal(day(2026, 8, 12)) {
			t.Errorf("unexpected entry for intervening day %v -- multi-day spans must not be special-cased into extra bars", e.MetricDate)
		}
	}
}

// TestSumSuperchargerPctBetween_IntervalBoundary_InclusiveStartExclusiveEnd covers
// design.md Test Contract (h) / roadmap D12: the matching interval is [from, to) --
// lower-inclusive, upper-exclusive -- for both sumSuperchargerPctBetween and
// inferMissingChargingType, which share the identical boundary predicate. Both helpers
// are unchanged by the dense-table revision (design.md D10 -- "every existing helper
// unchanged") and unchanged by this tier's move, so this test is unmodified.
func TestSumSuperchargerPctBetween_IntervalBoundary_InclusiveStartExclusiveEnd(t *testing.T) {
	from := time.Date(2026, 8, 14, 8, 30, 0, 0, time.UTC)
	to := time.Date(2026, 8, 15, 8, 30, 0, 0, time.UTC)

	sessionAtFrom := charging.Session{ChargeStopDateTime: from, StartBatteryPct: intPtr(10), EndBatteryPct: intPtr(30)} // +20, included (lower-inclusive)
	sessionAtTo := charging.Session{ChargeStopDateTime: to, StartBatteryPct: intPtr(40), EndBatteryPct: intPtr(90)}     // +50, excluded (upper-exclusive)

	got := sumSuperchargerPctBetween([]charging.Session{sessionAtFrom, sessionAtTo}, from, to)
	want := 20.0
	if !approxEqual(got, want) {
		t.Errorf("sumSuperchargerPctBetween: want %v (only the session exactly AT 'from'), got %v", want, got)
	}

	// inferMissingChargingType shares the identical [from, to) boundary (design.md D-B5):
	// a NULL-percentage session exactly at 'from' must be seen; one exactly at 'to' must
	// not.
	sessionAtFromNull := charging.Session{ChargeStopDateTime: from, StartBatteryPct: nil, EndBatteryPct: intPtr(30)}
	sessionAtToNull := charging.Session{ChargeStopDateTime: to, StartBatteryPct: nil, EndBatteryPct: intPtr(90)}

	gotType := inferMissingChargingType([]charging.Session{sessionAtToNull, sessionAtFromNull}, from, to)
	if gotType != MissingChargingTypeSupercharger {
		t.Errorf("inferMissingChargingType: want SUPERCHARGER (the in-bound NULL session at 'from' must be seen), got %v", gotType)
	}

	gotTypeOnlyOutOfBound := inferMissingChargingType([]charging.Session{sessionAtToNull}, from, to)
	if gotTypeOnlyOutOfBound != MissingChargingTypeManual {
		t.Errorf("inferMissingChargingType: want MANUAL (the only NULL session present is at 'to', excluded), got %v", gotTypeOnlyOutOfBound)
	}
}

// TestDeriveVehicleMetrics_Stateless_ResolvesOnRecompute covers design.md Test
// Contract (i) / D2: deriveVehicleMetrics is a pure function of its inputs with no
// memory between calls -- the precondition that makes cmd/poller's D7b
// delete-on-resolve reconciliation work for free (a resolved day simply stops
// appearing flagged on the next recompute).
func TestDeriveVehicleMetrics_Stateless_ResolvesOnRecompute(t *testing.T) {
	d0 := day(2026, 8, 13)
	d1 := day(2026, 8, 14)
	t0 := time.Date(2026, 8, 14, 8, 30, 0, 0, time.UTC)
	t1 := time.Date(2026, 8, 15, 8, 30, 0, 0, time.UTC)

	prev := telemetry.Snapshot{CapturedAt: t0, CapturedDate: d0.AddDate(0, 0, 1), BatteryLevelPct: 40}
	cur := telemetry.Snapshot{CapturedAt: t1, CapturedDate: d1.AddDate(0, 0, 1), BatteryLevelPct: 50} // raw delta 40-50 = -10
	snapshots := []telemetry.Snapshot{prev, cur}

	firstCall := deriveVehicleMetrics(nil, snapshots, nil, nil, d1, d1)
	if len(firstCall) != 1 || !firstCall[0].Flagged {
		t.Fatalf("first call: want one flagged entry, got %+v", firstCall)
	}

	// The user backfills the missing manual record; recomputed from the SAME snapshots
	// with no state carried over from the first call (D2).
	resolvingEntries := []charging.Entry{
		{ChargedOn: d1, StartBatteryPct: intPtr(30), EndBatteryPct: intPtr(40)}, // +10
	}
	secondCall := deriveVehicleMetrics(nil, snapshots, nil, resolvingEntries, d1, d1)
	if len(secondCall) != 1 {
		t.Fatalf("second call: want exactly 1 entry, got %d", len(secondCall))
	}
	if secondCall[0].Flagged {
		t.Errorf("second call: want Flagged=false after backfill, got Flagged=true (ConsumedPct=%v) -- suggests memory between calls", secondCall[0].ConsumedPct)
	}
	wantConsumed := 0.0 // -10 + 10
	if !approxEqual(mustFloat(t, secondCall[0].ConsumedPct), wantConsumed) {
		t.Errorf("second call ConsumedPct: want %v, got %v", wantConsumed, secondCall[0].ConsumedPct)
	}
}

// TestDeriveVehicleMetrics_BucketsInPollerZone_NotEffectiveDate covers design.md Test
// Contract (m) -- the SINGLE BINDING REGRESSION TEST for the owner's roadmap D18
// ruling. A nominal 03:30-Bogota fixture passes under EITHER the zoned rule or the
// overruled UTC rule, so it proves nothing about D18. This fixture is instead the D-B7
// scenario-3 edge: cur was captured in the Bogota EVENING (20:00 Aug 13 = 01:00Z Aug
// 14), where the zoned day and the UTC day disagree by exactly one calendar day.
//
// cur.EffectiveDate is deliberately set to the wrong (UTC, overruled) value -- the ONE
// fixture in this file that populates it -- specifically so a regression to reading it
// is caught by the negative assertion below. EffectiveDate is a surviving Snapshot
// field (read-derived, not one of the five dropped _calc columns) -- unaffected by
// this tier. Do NOT weaken either assertion (leader dispatch instruction, design.md
// Test Contract (m)).
func TestDeriveVehicleMetrics_BucketsInPollerZone_NotEffectiveDate(t *testing.T) {
	prev := telemetry.Snapshot{
		CapturedAt:      time.Date(2026, 8, 12, 8, 30, 0, 0, time.UTC), // 03:30 Bogota Aug 12
		CapturedDate:    day(2026, 8, 12),
		BatteryLevelPct: 70,
	}
	cur := telemetry.Snapshot{
		CapturedAt:   time.Date(2026, 8, 14, 1, 0, 0, 0, time.UTC), // 20:00 Bogota Aug 13
		CapturedDate: day(2026, 8, 13),
		// The value internal/telemetry/mapping.go really computes: CapturedAt - 1 day,
		// in UTC -- i.e. the day the OVERRULED D-B7 would have bucketed this row under.
		EffectiveDate:   time.Date(2026, 8, 13, 1, 0, 0, 0, time.UTC),
		BatteryLevelPct: 50, // raw delta 70-50 = 20
	}

	// Window is exactly cur's zoned effective day (2026-08-12), excluding prev's own
	// effective day (2026-08-11) -- call-shape adjustment only, mirrors the (a)-(e)/(i)
	// pattern above.
	wantZonedDate := day(2026, 8, 12)
	got := deriveVehicleMetrics(nil, []telemetry.Snapshot{prev, cur}, nil, nil, wantZonedDate, wantZonedDate)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry, got %d: %+v", len(got), got)
	}
	entry := got[0]

	if !approxEqual(mustFloat(t, entry.ConsumedPct), 20) {
		t.Errorf("ConsumedPct: want 20, got %v", entry.ConsumedPct)
	}
	if entry.Flagged {
		t.Error("want Flagged=false")
	}

	wrongUTCDate := day(2026, 8, 13) // EffectiveDate's own day: the overruled UTC answer
	if !entry.MetricDate.Equal(wantZonedDate) {
		t.Errorf("MetricDate: want %v (CapturedDate - 1 day, the zoned answer), got %v", wantZonedDate, entry.MetricDate)
	}
	if entry.MetricDate.Equal(wrongUTCDate) {
		t.Errorf("MetricDate must NOT equal %v -- that is the overruled UTC EffectiveDate answer; bucketing has regressed to reading EffectiveDate", wrongUTCDate)
	}

	// D-B12 identity: effectiveDay(cur) - effectiveDay(prev) == the row's own
	// DaysSpannedCalc, in whole days. This is a SECOND, independent guard on the same
	// regression: under the overruled UTC rule the same fixtures give 2 days != 1.
	// Asserted against entry.DaysSpannedCalc (the produced row), not against a
	// Snapshot field -- Snapshot no longer carries DaysSpannedCalc (design.md D5/D8).
	if entry.DaysSpannedCalc == nil || *entry.DaysSpannedCalc != 1 {
		t.Fatalf("DaysSpannedCalc: want 1, got %v", entry.DaysSpannedCalc)
	}
	gotSpanDays := int(effectiveDay(cur).Sub(effectiveDay(prev)).Hours() / 24)
	if gotSpanDays != *entry.DaysSpannedCalc {
		t.Errorf("effectiveDay(cur)-effectiveDay(prev): want %d day(s) (matching DaysSpannedCalc), got %d", *entry.DaysSpannedCalc, gotSpanDays)
	}
}

// TestDeriveVehicleMetrics_ZoneShiftedRowAtWindowEnd_Emitted covers design.md Test
// Contract (n) / D-B13: a row whose ZONED effective day lands exactly on the window's
// 'end' -- while its UTC EffectiveDate day would fall one day past it -- must still be
// emitted. This is the derivation half of the widened snapshot fetch; T5.4 in
// reader_test.go asserts the fetch itself widens far enough to bring such a row back.
func TestDeriveVehicleMetrics_ZoneShiftedRowAtWindowEnd_Emitted(t *testing.T) {
	prev := telemetry.Snapshot{
		CapturedAt:      time.Date(2026, 8, 20, 8, 30, 0, 0, time.UTC),
		CapturedDate:    day(2026, 8, 20), // zoned effective day 2026-08-19
		BatteryLevelPct: 55,
	}
	cur := telemetry.Snapshot{
		// 21:00 Bogota Aug 21 = 02:00Z Aug 22.
		CapturedAt:      time.Date(2026, 8, 22, 2, 0, 0, 0, time.UTC),
		CapturedDate:    day(2026, 8, 21), // zoned effective day 2026-08-20 == end
		BatteryLevelPct: 50,               // raw delta 55-50 = 5
	}

	// Window is exactly cur's zoned effective day (2026-08-20), excluding prev's own
	// effective day (2026-08-19) -- call-shape adjustment only, keeping the
	// substantive assertion (end-inclusive emission) unchanged.
	wantDate := day(2026, 8, 20)
	got := deriveVehicleMetrics(nil, []telemetry.Snapshot{prev, cur}, nil, nil, wantDate, wantDate)
	if len(got) != 1 {
		t.Fatalf("want the zone-shifted row to be emitted at the window's upper edge, got %d entries: %+v", len(got), got)
	}
	if !got[0].MetricDate.Equal(wantDate) {
		t.Errorf("MetricDate: want %v (the row's zoned effective day, equal to 'end'), got %v", wantDate, got[0].MetricDate)
	}
}

// --- Fixtures A, B, C (design.md "Test Contract" section, authored BEFORE this
// implementation existed, per ai/go-conventions.md "author expected values up front")
// ---
//
// Every expected value below is copied verbatim from design.md's Test Contract, never
// derived by reading consumed.go. Fixture C is new at the database design gate,
// pinning the dense-table revision's predecessor-less-row representation (D9/D10/D13).
// As of RM29 tier 4, the five _calc figures are no longer set directly on the fixture
// Snapshots (the fields are gone) -- they are computed by deriveConsumption from the
// raw OdometerKm/BatteryLevelPct/CapturedDate values below, and the SAME expected
// numeric values are asserted against the produced row.

// TestDeriveVehicleMetrics_FixtureA covers design.md's Test Contract Fixture A -- a
// plain day, no charge events.
func TestDeriveVehicleMetrics_FixtureA(t *testing.T) {
	accountID := uuid.New()
	const teslaID = int64(42)

	prevDay := day(2026, 8, 10)
	curDay := day(2026, 8, 11)

	prev := telemetry.Snapshot{
		AccountID:       accountID,
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 8, 10, 3, 30, 0, 0, time.UTC),
		CapturedDate:    prevDay,
		OdometerKm:      1000.0,
		BatteryLevelPct: 80,
		BatteryRangeKm:  300.0,
	}
	cur := telemetry.Snapshot{
		AccountID:       accountID,
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 8, 11, 3, 30, 0, 0, time.UTC),
		CapturedDate:    curDay,
		OdometerKm:      1050.0,
		BatteryLevelPct: 65,
		BatteryRangeKm:  280.0,
	}

	// Recalculate(A, 42, 2026-08-10, 2026-08-10). effectiveDay = CapturedDate - 1,
	// so prev (CapturedDate 08-10) lands on 08-09, outside this window, and cur
	// (CapturedDate 08-11) lands on 08-10, inside it -- only cur's row is produced.
	start := day(2026, 8, 10)
	end := day(2026, 8, 10)

	got := deriveVehicleMetrics(nil, []telemetry.Snapshot{prev, cur}, nil, nil, start, end)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry, got %d: %+v", len(got), got)
	}
	entry := got[0]

	if entry.AccountID != accountID {
		t.Errorf("AccountID: want %v, got %v", accountID, entry.AccountID)
	}
	if entry.TeslaID != teslaID {
		t.Errorf("TeslaID: want %d, got %d", teslaID, entry.TeslaID)
	}
	wantDate := day(2026, 8, 10)
	if !entry.MetricDate.Equal(wantDate) {
		t.Errorf("MetricDate: want %v, got %v", wantDate, entry.MetricDate)
	}
	if entry.BatteryLevelPct != 65 {
		t.Errorf("BatteryLevelPct: want 65, got %d", entry.BatteryLevelPct)
	}
	if entry.OdometerKm != 1050.0 {
		t.Errorf("OdometerKm: want 1050.0, got %v", entry.OdometerKm)
	}
	if entry.BatteryRangeKm != 280.0 {
		t.Errorf("BatteryRangeKm: want 280.0, got %v", entry.BatteryRangeKm)
	}
	if !approxEqual(mustFloat(t, entry.DistanceTraveledKmCalc), 50.0) {
		t.Errorf("DistanceTraveledKmCalc: want 50.0, got %v", entry.DistanceTraveledKmCalc)
	}
	if entry.BatteryUsedPctCalc == nil || *entry.BatteryUsedPctCalc != 15 {
		t.Errorf("BatteryUsedPctCalc: want 15, got %v", entry.BatteryUsedPctCalc)
	}
	if !approxEqual(mustFloat(t, entry.KmPerPctCalc), 50.0/15.0) {
		t.Errorf("KmPerPctCalc: want %v, got %v", 50.0/15.0, entry.KmPerPctCalc)
	}
	if !approxEqual(mustFloat(t, entry.EstimatedRangeKmCalc), 50.0/15.0*100) {
		t.Errorf("EstimatedRangeKmCalc: want %v, got %v", 50.0/15.0*100, entry.EstimatedRangeKmCalc)
	}
	if entry.DaysSpannedCalc == nil || *entry.DaysSpannedCalc != 1 {
		t.Errorf("DaysSpannedCalc: want 1, got %v", entry.DaysSpannedCalc)
	}
	if !approxEqual(mustFloat(t, entry.ConsumedPct), 15.0) {
		t.Errorf("ConsumedPct: want 15.0, got %v", entry.ConsumedPct)
	}
	if entry.Flagged {
		t.Error("want Flagged=false")
	}
	if entry.MissingChargingType != "" {
		t.Errorf("MissingChargingType: want \"\" (NULL), got %v", entry.MissingChargingType)
	}
}

// TestDeriveVehicleMetrics_FixtureB covers design.md's Test Contract Fixture B --
// negative odometer delta + flagged/missing-charge day. This test only asserts the
// RAW, unclamped stored value deriveVehicleMetrics produces (any read-side clamp is a
// Reader-level concern, tested in reader_test.go).
func TestDeriveVehicleMetrics_FixtureB(t *testing.T) {
	accountID := uuid.New()
	const teslaID = int64(42)

	prevDay := day(2026, 8, 12)
	curDay := day(2026, 8, 13)

	prev := telemetry.Snapshot{
		AccountID:       accountID,
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 8, 12, 3, 30, 0, 0, time.UTC),
		CapturedDate:    prevDay,
		OdometerKm:      2000.0,
		BatteryLevelPct: 40,
	}
	cur := telemetry.Snapshot{
		AccountID:       accountID,
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 8, 13, 3, 30, 0, 0, time.UTC),
		CapturedDate:    curDay,
		OdometerKm:      1998.0, // 1998.0 - 2000.0 = -2.0, stored RAW, unclamped
		BatteryLevelPct: 85,     // 40 - 85 = -45
	}

	start := day(2026, 8, 12)
	end := day(2026, 8, 12)

	got := deriveVehicleMetrics(nil, []telemetry.Snapshot{prev, cur}, nil, nil, start, end)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry, got %d: %+v", len(got), got)
	}
	entry := got[0]

	if entry.AccountID != accountID {
		t.Errorf("AccountID: want %v, got %v", accountID, entry.AccountID)
	}
	if entry.TeslaID != teslaID {
		t.Errorf("TeslaID: want %d, got %d", teslaID, entry.TeslaID)
	}
	wantDate := day(2026, 8, 12)
	if !entry.MetricDate.Equal(wantDate) {
		t.Errorf("MetricDate: want %v, got %v", wantDate, entry.MetricDate)
	}
	if entry.BatteryLevelPct != 85 {
		t.Errorf("BatteryLevelPct: want 85, got %d", entry.BatteryLevelPct)
	}
	if entry.OdometerKm != 1998.0 {
		t.Errorf("OdometerKm: want 1998.0, got %v", entry.OdometerKm)
	}
	if !approxEqual(mustFloat(t, entry.DistanceTraveledKmCalc), -2.0) {
		t.Errorf("DistanceTraveledKmCalc: want -2.0 (raw, unclamped), got %v", entry.DistanceTraveledKmCalc)
	}
	if entry.BatteryUsedPctCalc == nil || *entry.BatteryUsedPctCalc != -45 {
		t.Errorf("BatteryUsedPctCalc: want -45, got %v", entry.BatteryUsedPctCalc)
	}
	if entry.KmPerPctCalc != nil {
		t.Errorf("KmPerPctCalc: want nil (divisor -45 <= 0), got %v", *entry.KmPerPctCalc)
	}
	if entry.EstimatedRangeKmCalc != nil {
		t.Errorf("EstimatedRangeKmCalc: want nil (same guard), got %v", *entry.EstimatedRangeKmCalc)
	}
	if entry.DaysSpannedCalc == nil || *entry.DaysSpannedCalc != 1 {
		t.Errorf("DaysSpannedCalc: want 1, got %v", entry.DaysSpannedCalc)
	}
	if !approxEqual(mustFloat(t, entry.ConsumedPct), -45.0) {
		t.Errorf("ConsumedPct: want -45.0 (no matched charge event), got %v", entry.ConsumedPct)
	}
	if !entry.Flagged {
		t.Error("want Flagged=true (ConsumedPct < 0)")
	}
	if entry.MissingChargingType != MissingChargingTypeManual {
		t.Errorf("MissingChargingType: want MANUAL (no Supercharger session matched at all), got %v", entry.MissingChargingType)
	}
}

// TestDeriveVehicleMetrics_FixtureC covers design.md's Test Contract Fixture C -- a
// vehicle's true first-ever snapshot (no predecessor at all), the dense-table
// revision's own scenario (D9/D10), now also RM29 tier 4's ONLY remaining
// "predecessor-less" trigger (design.md D6): preceding == nil at i == 0. A row IS
// written -- with every derived/consumed field NULL, flagged == false (NOT true, NOT
// left to a stray zero-value comparison), and missing_charging_type == "" (SQL NULL).
func TestDeriveVehicleMetrics_FixtureC(t *testing.T) {
	accountID := uuid.New()
	const teslaID = int64(42)

	cur := telemetry.Snapshot{
		AccountID:       accountID,
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 8, 5, 3, 30, 0, 0, time.UTC),
		CapturedDate:    day(2026, 8, 5),
		OdometerKm:      500.0,
		BatteryLevelPct: 90,
		BatteryRangeKm:  320.0,
	}

	// snapshots is the single-element slice telemetry.SnapshotsByVehicleBetween
	// would return for this fetch (design.md: "returns exactly this one row --
	// nothing exists before it") -- cur is index 0. preceding is explicitly nil,
	// matching design.md's "SnapshotPrecedingDay(A, 42, 2026-08-05) returns (nil,
	// nil)" -- the vehicle's true first-ever capture.
	start := day(2026, 8, 4)
	end := day(2026, 8, 4)

	got := deriveVehicleMetrics(nil, []telemetry.Snapshot{cur}, nil, nil, start, end)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry (dense table -- a row is written even with no predecessor, design.md D9), got %d: %+v", len(got), got)
	}
	entry := got[0]

	if entry.AccountID != accountID {
		t.Errorf("AccountID: want %v, got %v", accountID, entry.AccountID)
	}
	if entry.TeslaID != teslaID {
		t.Errorf("TeslaID: want %d, got %d", teslaID, entry.TeslaID)
	}
	wantDate := day(2026, 8, 4)
	if !entry.MetricDate.Equal(wantDate) {
		t.Errorf("MetricDate: want %v, got %v", wantDate, entry.MetricDate)
	}
	if entry.BatteryLevelPct != 90 {
		t.Errorf("BatteryLevelPct: want 90, got %d", entry.BatteryLevelPct)
	}
	if entry.OdometerKm != 500.0 {
		t.Errorf("OdometerKm: want 500.0, got %v", entry.OdometerKm)
	}
	if entry.BatteryRangeKm != 320.0 {
		t.Errorf("BatteryRangeKm: want 320.0, got %v", entry.BatteryRangeKm)
	}
	if entry.DistanceTraveledKmCalc != nil {
		t.Errorf("DistanceTraveledKmCalc: want nil, got %v", *entry.DistanceTraveledKmCalc)
	}
	if entry.BatteryUsedPctCalc != nil {
		t.Errorf("BatteryUsedPctCalc: want nil, got %v", *entry.BatteryUsedPctCalc)
	}
	if entry.KmPerPctCalc != nil {
		t.Errorf("KmPerPctCalc: want nil, got %v", *entry.KmPerPctCalc)
	}
	if entry.EstimatedRangeKmCalc != nil {
		t.Errorf("EstimatedRangeKmCalc: want nil, got %v", *entry.EstimatedRangeKmCalc)
	}
	if entry.DaysSpannedCalc != nil {
		t.Errorf("DaysSpannedCalc: want nil, got %v", *entry.DaysSpannedCalc)
	}
	if entry.ConsumedPct != nil {
		t.Errorf("ConsumedPct: want nil, got %v", *entry.ConsumedPct)
	}
	if entry.Flagged {
		t.Error("want Flagged=false (NOT NULL -- never true, never left to a stray zero-value comparison, design.md D9)")
	}
	if entry.MissingChargingType != "" {
		t.Errorf("MissingChargingType: want \"\" (maps to SQL NULL), got %v", entry.MissingChargingType)
	}
}

// --- Fixture D, D2 (design.md "Test Contract" section) -- new at RM29 tier 4: the
// case telemetry.Reader.SnapshotPrecedingDay and the `preceding` parameter exist for
// (design.md D2/D7). ---

// TestDeriveVehicleMetrics_FixtureD_UsesPrecedingSnapshot covers design.md's Test
// Contract Fixture D -- a seven-day capture gap. The fetched snapshot slice contains
// ONLY the current row; the true predecessor (seven days earlier) arrives solely
// through the `preceding` parameter, exactly as Recalculate supplies it via
// SnapshotPrecedingDay (design.md D7). An implementation that omits/ignores
// `preceding` falls into the prev == nil branch and produces an ALL-NIL row with an
// empty-looking result -- the negative assertion at the end catches exactly that.
func TestDeriveVehicleMetrics_FixtureD_UsesPrecedingSnapshot(t *testing.T) {
	accountID := uuid.New()
	const teslaID = int64(42)

	preceding := telemetry.Snapshot{
		AccountID:       accountID,
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 8, 1, 3, 30, 0, 0, time.UTC),
		CapturedDate:    day(2026, 8, 1),
		OdometerKm:      1000.0,
		BatteryLevelPct: 90,
		BatteryRangeKm:  350.0,
	}
	cur := telemetry.Snapshot{
		AccountID:       accountID,
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 8, 8, 3, 30, 0, 0, time.UTC),
		CapturedDate:    day(2026, 8, 8),
		OdometerKm:      1210.0,
		BatteryLevelPct: 55,
		BatteryRangeKm:  220.0,
	}

	start := day(2026, 8, 7)
	end := day(2026, 8, 7)

	got := deriveVehicleMetrics(&preceding, []telemetry.Snapshot{cur}, nil, nil, start, end)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry (the multi-day gap must still be visible), got %d: %+v", len(got), got)
	}
	entry := got[0]

	wantDate := day(2026, 8, 7)
	if !entry.MetricDate.Equal(wantDate) {
		t.Errorf("MetricDate: want %v, got %v", wantDate, entry.MetricDate)
	}
	if entry.BatteryLevelPct != 55 {
		t.Errorf("BatteryLevelPct: want 55, got %d", entry.BatteryLevelPct)
	}
	if entry.OdometerKm != 1210.0 {
		t.Errorf("OdometerKm: want 1210.0, got %v", entry.OdometerKm)
	}
	if entry.BatteryRangeKm != 220.0 {
		t.Errorf("BatteryRangeKm: want 220.0, got %v", entry.BatteryRangeKm)
	}
	if !approxEqual(mustFloat(t, entry.DistanceTraveledKmCalc), 210.0) {
		t.Errorf("DistanceTraveledKmCalc: want 210.0 (the true total across the gap, never averaged), got %v", entry.DistanceTraveledKmCalc)
	}
	if entry.BatteryUsedPctCalc == nil || *entry.BatteryUsedPctCalc != 35 {
		t.Errorf("BatteryUsedPctCalc: want 35, got %v", entry.BatteryUsedPctCalc)
	}
	if entry.DaysSpannedCalc == nil || *entry.DaysSpannedCalc != 7 {
		t.Errorf("DaysSpannedCalc: want 7 -- NOT 1 -- got %v", entry.DaysSpannedCalc)
	}
	if !approxEqual(mustFloat(t, entry.KmPerPctCalc), 6.0) {
		t.Errorf("KmPerPctCalc: want 6.0, got %v", entry.KmPerPctCalc)
	}
	if !approxEqual(mustFloat(t, entry.EstimatedRangeKmCalc), 600.0) {
		t.Errorf("EstimatedRangeKmCalc: want 600.0, got %v", entry.EstimatedRangeKmCalc)
	}
	if !approxEqual(mustFloat(t, entry.ConsumedPct), 35.0) {
		t.Errorf("ConsumedPct: want 35.0 (no charge events in the gap), got %v", entry.ConsumedPct)
	}
	if entry.Flagged {
		t.Error("want Flagged=false")
	}
	if entry.MissingChargingType != "" {
		t.Errorf("MissingChargingType: want \"\" (NULL), got %v", entry.MissingChargingType)
	}
}

// TestDeriveVehicleMetrics_FixtureD2_ChargeInsideTheGap covers design.md's Test
// Contract Fixture D2 -- D8b's proof. Same gap as Fixture D, plus one manual charge
// entry dated INSIDE the gap (four days before the un-widened fetch would have
// started). BatteryUsedPctCalc is unaffected by charging (still 35, the raw delta),
// but ConsumedPct must include the matched charge: 35 + 20 = 55. An implementation
// that widened the predecessor lookup (Fixture D) but not the charge-source fetches
// would miss this entry and wrongly report ConsumedPct 35.0 here.
func TestDeriveVehicleMetrics_FixtureD2_ChargeInsideTheGap(t *testing.T) {
	accountID := uuid.New()
	const teslaID = int64(42)

	preceding := telemetry.Snapshot{
		AccountID:       accountID,
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 8, 1, 3, 30, 0, 0, time.UTC),
		CapturedDate:    day(2026, 8, 1),
		OdometerKm:      1000.0,
		BatteryLevelPct: 90,
		BatteryRangeKm:  350.0,
	}
	cur := telemetry.Snapshot{
		AccountID:       accountID,
		TeslaID:         teslaID,
		CapturedAt:      time.Date(2026, 8, 8, 3, 30, 0, 0, time.UTC),
		CapturedDate:    day(2026, 8, 8),
		OdometerKm:      1210.0,
		BatteryLevelPct: 55,
		BatteryRangeKm:  220.0,
	}

	// Deriveation's charge-matching window for this row is
	// (effectiveDay(preceding), effectiveDay(cur)] = (2026-07-31, 2026-08-07] --
	// design.md D-B6 -- which THIS entry's day (2026-08-04) falls inside, well
	// short of the un-widened fetch's own start (2026-08-06). This test exercises
	// deriveVehicleMetrics directly (the pure function), so it is not itself proof
	// that Recalculate's fetch widens far enough to have retrieved this entry in
	// production -- that half of D8b is pinned by recalculate_test.go (task 3.3).
	entries := []charging.Entry{
		{ChargedOn: day(2026, 8, 4), StartBatteryPct: intPtr(30), EndBatteryPct: intPtr(50)}, // +20
	}

	start := day(2026, 8, 7)
	end := day(2026, 8, 7)

	got := deriveVehicleMetrics(&preceding, []telemetry.Snapshot{cur}, nil, entries, start, end)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry, got %d: %+v", len(got), got)
	}
	entry := got[0]

	if entry.BatteryUsedPctCalc == nil || *entry.BatteryUsedPctCalc != 35 {
		t.Errorf("BatteryUsedPctCalc: want 35 (unaffected by charging), got %v", entry.BatteryUsedPctCalc)
	}
	if !approxEqual(mustFloat(t, entry.ConsumedPct), 55.0) {
		t.Errorf("ConsumedPct: want 55.0 (35 raw + 20 charged inside the gap), got %v", entry.ConsumedPct)
	}
	if entry.Flagged {
		t.Error("want Flagged=false")
	}
	if !approxEqual(mustFloat(t, entry.DistanceTraveledKmCalc), 210.0) {
		t.Errorf("DistanceTraveledKmCalc: want 210.0, got %v", entry.DistanceTraveledKmCalc)
	}
	if entry.DaysSpannedCalc == nil || *entry.DaysSpannedCalc != 7 {
		t.Errorf("DaysSpannedCalc: want 7, got %v", entry.DaysSpannedCalc)
	}
}

// --- RM38-analytics-add-vehicle-status-columns (task 4.1) -- Fixtures RM38-A
// and RM38-B, design.md's Test Contract. Both extend Fixture A/C's shape
// exactly (same accountID/teslaID convention, same predecessor/current day
// pair, teslaID 42) per design.md's own instruction, rather than inventing
// new fixtures. Expected values are copied verbatim from design.md's Test
// Contract tables, never derived by reading consumed.go.

// TestDeriveVehicleMetrics_FixtureRM38A_StatusColumnsPopulated covers
// design.md's Test Contract Fixture RM38-A -- a normal day, predecessor
// exists, all eight new status columns populated from cur's own snapshot.
// prev is deliberately given the OPPOSITE value on every one of the eight
// fields so a leak from prev instead of cur would be caught by the dedicated
// negative assertion at the end (design D3: "copy from cur, never prev").
func TestDeriveVehicleMetrics_FixtureRM38A_StatusColumnsPopulated(t *testing.T) {
	accountID := uuid.New()
	const teslaID = int64(42)

	prevDay := day(2026, 8, 10)
	curDay := day(2026, 8, 11)

	prev := telemetry.Snapshot{
		AccountID:         accountID,
		TeslaID:           teslaID,
		CapturedAt:        time.Date(2026, 8, 10, 3, 30, 0, 0, time.UTC),
		CapturedDate:      prevDay,
		OdometerKm:        1000.0,
		BatteryLevelPct:   80,
		BatteryRangeKm:    300.0,
		Locked:            false,
		SentryMode:        boolPtr(true),
		CarVersion:        "2026.20.1",
		InsideTempC:       10.0,
		OutsideTempC:      5.0,
		ChargingState:     "Charging",
		ChargeLimitSocPct: 100,
	}
	cur := telemetry.Snapshot{
		AccountID:         accountID,
		TeslaID:           teslaID,
		CapturedAt:        time.Date(2026, 8, 11, 3, 31, 0, 0, time.UTC),
		CapturedDate:      curDay,
		OdometerKm:        1050.0,
		BatteryLevelPct:   65,
		BatteryRangeKm:    280.0,
		Locked:            true,
		SentryMode:        boolPtr(false),
		CarVersion:        "2026.28.4",
		InsideTempC:       21.5,
		OutsideTempC:      18.0,
		ChargingState:     "Disconnected",
		ChargeLimitSocPct: 80,
	}

	start := day(2026, 8, 10)
	end := day(2026, 8, 10)

	got := deriveVehicleMetrics(nil, []telemetry.Snapshot{prev, cur}, nil, nil, start, end)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry, got %d: %+v", len(got), got)
	}
	entry := got[0]

	if entry.Locked == nil || *entry.Locked != true {
		t.Errorf("Locked: want true, got %v", entry.Locked)
	}
	if entry.SentryMode == nil || *entry.SentryMode != false {
		t.Errorf("SentryMode: want false (not NULL -- a real reported value), got %v", entry.SentryMode)
	}
	if entry.CarVersion == nil || *entry.CarVersion != "2026.28.4" {
		t.Errorf("CarVersion: want 2026.28.4, got %v", entry.CarVersion)
	}
	if entry.InsideTempC == nil || !approxEqual(*entry.InsideTempC, 21.5) {
		t.Errorf("InsideTempC: want 21.5, got %v", entry.InsideTempC)
	}
	if entry.OutsideTempC == nil || !approxEqual(*entry.OutsideTempC, 18.0) {
		t.Errorf("OutsideTempC: want 18.0, got %v", entry.OutsideTempC)
	}
	if entry.ChargingState == nil || *entry.ChargingState != "Disconnected" {
		t.Errorf("ChargingState: want Disconnected, got %v", entry.ChargingState)
	}
	if entry.ChargeLimitSocPct == nil || *entry.ChargeLimitSocPct != 80 {
		t.Errorf("ChargeLimitSocPct: want 80, got %v", entry.ChargeLimitSocPct)
	}
	wantCapturedAt := time.Date(2026, 8, 11, 3, 31, 0, 0, time.UTC)
	if entry.CapturedAt == nil || !entry.CapturedAt.Equal(wantCapturedAt) {
		t.Errorf("CapturedAt: want %v, got %v", wantCapturedAt, entry.CapturedAt)
	}

	// Regression guard for design D3: the eight new columns must come from
	// cur, never prev -- prev is deliberately the opposite of cur on every
	// field above, so any leak surfaces here.
	if entry.Locked != nil && *entry.Locked == prev.Locked {
		t.Error("Locked equals prev's value -- the eight new columns must copy from cur only (design D3)")
	}
	if entry.CarVersion != nil && *entry.CarVersion == prev.CarVersion {
		t.Error("CarVersion equals prev's value -- the eight new columns must copy from cur only (design D3)")
	}
}

// TestDeriveVehicleMetrics_FixtureRM38B_StatusColumnsPopulatedWithoutPredecessor
// covers design.md's Test Contract Fixture RM38-B -- a vehicle's first-ever
// snapshot (no predecessor at all, extending Fixture C's shape). This is the
// test that would FAIL if a future edit folded the eight new fields into the
// five _calc columns' nil-on-no-predecessor branch (design D3's own stated
// regression risk): it asserts the eight new fields are non-nil on the SAME
// row where the five _calc columns/ConsumedPct are nil and Flagged is false.
func TestDeriveVehicleMetrics_FixtureRM38B_StatusColumnsPopulatedWithoutPredecessor(t *testing.T) {
	accountID := uuid.New()
	const teslaID = int64(42)

	cur := telemetry.Snapshot{
		AccountID:         accountID,
		TeslaID:           teslaID,
		CapturedAt:        time.Date(2026, 8, 5, 3, 30, 15, 0, time.UTC),
		CapturedDate:      day(2026, 8, 5),
		OdometerKm:        500.0,
		BatteryLevelPct:   90,
		BatteryRangeKm:    320.0,
		Locked:            false,
		SentryMode:        nil, // the vehicle genuinely did not report sentry this capture (design D2/D8)
		CarVersion:        "2026.28.4",
		InsideTempC:       19.0,
		OutsideTempC:      14.0,
		ChargingState:     "Charging",
		ChargeLimitSocPct: 90,
	}

	start := day(2026, 8, 4)
	end := day(2026, 8, 4)

	got := deriveVehicleMetrics(nil, []telemetry.Snapshot{cur}, nil, nil, start, end)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry (dense table -- a row is written even with no predecessor, design.md D9), got %d: %+v", len(got), got)
	}
	entry := got[0]

	// The eight new columns: populated even though this row has no predecessor.
	if entry.Locked == nil || *entry.Locked != false {
		t.Errorf("Locked: want false (NOT NULL -- populated even on a predecessor-less row, design D3), got %v", entry.Locked)
	}
	if entry.SentryMode != nil {
		t.Errorf("SentryMode: want nil (the vehicle genuinely did not report sentry -- design D2/D8's 'not reported' reading), got %v", *entry.SentryMode)
	}
	if entry.CarVersion == nil || *entry.CarVersion != "2026.28.4" {
		t.Errorf("CarVersion: want 2026.28.4, got %v", entry.CarVersion)
	}
	if entry.InsideTempC == nil || !approxEqual(*entry.InsideTempC, 19.0) {
		t.Errorf("InsideTempC: want 19.0, got %v", entry.InsideTempC)
	}
	if entry.OutsideTempC == nil || !approxEqual(*entry.OutsideTempC, 14.0) {
		t.Errorf("OutsideTempC: want 14.0, got %v", entry.OutsideTempC)
	}
	if entry.ChargingState == nil || *entry.ChargingState != "Charging" {
		t.Errorf("ChargingState: want Charging, got %v", entry.ChargingState)
	}
	if entry.ChargeLimitSocPct == nil || *entry.ChargeLimitSocPct != 90 {
		t.Errorf("ChargeLimitSocPct: want 90, got %v", entry.ChargeLimitSocPct)
	}
	wantCapturedAt := time.Date(2026, 8, 5, 3, 30, 15, 0, time.UTC)
	if entry.CapturedAt == nil || !entry.CapturedAt.Equal(wantCapturedAt) {
		t.Errorf("CapturedAt: want %v, got %v", wantCapturedAt, entry.CapturedAt)
	}

	// The five _calc columns / consumed_pct / flagged: UNCHANGED D9 behavior
	// -- this IS the regression guard design D3 exists for (see doc comment
	// above). A future edit that mistakenly gated the eight new fields on
	// prev == nil the same way these five are gated would fail the block
	// above, not this one; this block guards the opposite mistake (someone
	// "fixing" these five to also populate from cur unconditionally).
	if entry.DistanceTraveledKmCalc != nil {
		t.Errorf("DistanceTraveledKmCalc: want nil, got %v", *entry.DistanceTraveledKmCalc)
	}
	if entry.BatteryUsedPctCalc != nil {
		t.Errorf("BatteryUsedPctCalc: want nil, got %v", *entry.BatteryUsedPctCalc)
	}
	if entry.KmPerPctCalc != nil {
		t.Errorf("KmPerPctCalc: want nil, got %v", *entry.KmPerPctCalc)
	}
	if entry.EstimatedRangeKmCalc != nil {
		t.Errorf("EstimatedRangeKmCalc: want nil, got %v", *entry.EstimatedRangeKmCalc)
	}
	if entry.DaysSpannedCalc != nil {
		t.Errorf("DaysSpannedCalc: want nil, got %v", *entry.DaysSpannedCalc)
	}
	if entry.ConsumedPct != nil {
		t.Errorf("ConsumedPct: want nil, got %v", *entry.ConsumedPct)
	}
	if entry.Flagged {
		t.Error("want Flagged=false (D9, unchanged by this tier)")
	}
	if entry.MissingChargingType != "" {
		t.Errorf("MissingChargingType: want \"\" (NULL), got %v", entry.MissingChargingType)
	}
}
