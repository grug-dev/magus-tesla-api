package analytics

import (
	"testing"
	"time"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// The consumed tests exercise deriveConsumedByDay and its helpers fully OFFLINE and with
// zero fakes: all are pure functions over plain telemetry.Snapshot / SuperchargerSession /
// charging.Entry values (design.md D-B1 through D-B13), so there is no I/O seam to
// fake -- mirrors derive_test.go's own zero-fakes style. Expected values are hand-computed
// from design.md's Test Contract (authored before this implementation existed), never
// derived by reading consumed.go itself.
//
// Per the Test-Execution-Policy, these tests are written but NOT run by the worker; go vet
// ./... compiles them as a signature-drift signal. The owner runs
// `go test ./internal/analytics/...` and reports the result.
//
// Snapshot-fixture convention (design.md Test Contract, binding, changed by D18): every
// telemetry.Snapshot fixture below sets both CapturedAt (the instant) and CapturedDate
// (the zoned capture day, from which effectiveDay derives the bucket day). Where a
// scenario says "effective day D", the fixture's CapturedDate is D + 1 day. EffectiveDate
// is left at its zero value in every fixture EXCEPT scenario (m) / T4.10, which sets it
// deliberately wrong on purpose -- see that test's own comment.

// day returns a bare calendar date at UTC midnight -- this platform's date representation
// (the pgtype.Date convention; see consumed.go's calendarDay doc comment).
func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// TestDeriveConsumedByDay_SingleSessionSingleDay_MatchesRoadmapExample covers design.md
// Test Contract (a) -- the roadmap's own verified worked example: one Supercharger
// session inside a single day's window, expect ConsumedPct = 11.
func TestDeriveConsumedByDay_SingleSessionSingleDay_MatchesRoadmapExample(t *testing.T) {
	d0 := day(2026, 8, 13)                              // prev's effective day
	d1 := day(2026, 8, 14)                              // cur's effective day
	t0 := time.Date(2026, 8, 14, 8, 30, 0, 0, time.UTC) // prev.CapturedAt
	t1 := time.Date(2026, 8, 15, 8, 30, 0, 0, time.UTC) // cur.CapturedAt

	prev := telemetry.Snapshot{CapturedAt: t0, CapturedDate: d0.AddDate(0, 0, 1)}
	cur := telemetry.Snapshot{
		CapturedAt:         t1,
		CapturedDate:       d1.AddDate(0, 0, 1),
		BatteryUsedPctCalc: intPtr(22 - 73), // -51
		DaysSpannedCalc:    intPtr(1),
	}

	sessions := []telemetry.SuperchargerSession{
		{ChargeStopDateTime: t0.Add(6 * time.Hour), StartBatteryPct: intPtr(18), EndBatteryPct: intPtr(80)},
	}

	got := deriveConsumedByDay([]telemetry.Snapshot{prev, cur}, sessions, nil, d0, d1)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry, got %d: %+v", len(got), got)
	}
	entry := got[0]

	if !entry.Date.Equal(d1) {
		t.Errorf("Date: want %v, got %v", d1, entry.Date)
	}
	wantConsumed := -51.0 + (80.0 - 18.0) // 11
	if !approxEqual(entry.ConsumedPct, wantConsumed) {
		t.Errorf("ConsumedPct: want %v, got %v", wantConsumed, entry.ConsumedPct)
	}
	if entry.Flagged {
		t.Error("want Flagged=false")
	}
}

// TestDeriveConsumedByDay_TwoSessionsSameDay_SumsBoth_Not5 covers design.md Test Contract
// (b) -- the roadmap's own regression guard: two Supercharger sessions in the same day's
// window must be SUMMED (D13), not collapsed to the latest session's delta alone (-25) or
// to a first-start-to-last-end span (15). The correct result is 5.
func TestDeriveConsumedByDay_TwoSessionsSameDay_SumsBoth_Not5(t *testing.T) {
	d0 := day(2026, 8, 13)
	d1 := day(2026, 8, 14)
	t0 := time.Date(2026, 8, 14, 8, 30, 0, 0, time.UTC)
	t1 := time.Date(2026, 8, 15, 8, 30, 0, 0, time.UTC)

	prev := telemetry.Snapshot{CapturedAt: t0, CapturedDate: d0.AddDate(0, 0, 1)}
	cur := telemetry.Snapshot{
		CapturedAt:         t1,
		CapturedDate:       d1.AddDate(0, 0, 1),
		BatteryUsedPctCalc: intPtr(30 - 75), // -45
		DaysSpannedCalc:    intPtr(1),
	}

	sessions := []telemetry.SuperchargerSession{
		{ChargeStopDateTime: t0.Add(4 * time.Hour), StartBatteryPct: intPtr(20), EndBatteryPct: intPtr(50)}, // earlier, +30
		{ChargeStopDateTime: t0.Add(8 * time.Hour), StartBatteryPct: intPtr(60), EndBatteryPct: intPtr(80)}, // later, +20
	}

	got := deriveConsumedByDay([]telemetry.Snapshot{prev, cur}, sessions, nil, d0, d1)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry, got %d: %+v", len(got), got)
	}

	gotConsumed := got[0].ConsumedPct
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

// TestDeriveConsumedByDay_NegativeFlagged covers design.md Test Contract (c): a negative
// corrected consumption with no charge events matched must flag, inferring MANUAL (no
// Supercharger session present in the interval).
func TestDeriveConsumedByDay_NegativeFlagged(t *testing.T) {
	d0 := day(2026, 8, 13)
	d1 := day(2026, 8, 14)
	t0 := time.Date(2026, 8, 14, 8, 30, 0, 0, time.UTC)
	t1 := time.Date(2026, 8, 15, 8, 30, 0, 0, time.UTC)

	prev := telemetry.Snapshot{CapturedAt: t0, CapturedDate: d0.AddDate(0, 0, 1)}
	cur := telemetry.Snapshot{
		CapturedAt:         t1,
		CapturedDate:       d1.AddDate(0, 0, 1),
		BatteryUsedPctCalc: intPtr(50 - 60), // -10
		DaysSpannedCalc:    intPtr(1),
	}

	got := deriveConsumedByDay([]telemetry.Snapshot{prev, cur}, nil, nil, d0, d1)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry, got %d", len(got))
	}
	entry := got[0]

	if !approxEqual(entry.ConsumedPct, -10) {
		t.Errorf("ConsumedPct: want -10, got %v", entry.ConsumedPct)
	}
	if !entry.Flagged {
		t.Error("want Flagged=true")
	}
	if entry.MissingChargingType != telemetry.MissingChargingTypeManual {
		t.Errorf("MissingChargingType: want MANUAL, got %v", entry.MissingChargingType)
	}
}

// TestDeriveConsumedByDay_ZeroWithDistanceFlagged covers design.md Test Contract (d): a
// day whose corrected ConsumedPct computes to exactly 0 must flag when the vehicle
// demonstrably drove more than minFlagDistanceKm that day.
func TestDeriveConsumedByDay_ZeroWithDistanceFlagged(t *testing.T) {
	d0 := day(2026, 8, 13)
	d1 := day(2026, 8, 14)
	t0 := time.Date(2026, 8, 14, 8, 30, 0, 0, time.UTC)
	t1 := time.Date(2026, 8, 15, 8, 30, 0, 0, time.UTC)

	prev := telemetry.Snapshot{CapturedAt: t0, CapturedDate: d0.AddDate(0, 0, 1)}
	cur := telemetry.Snapshot{
		CapturedAt:             t1,
		CapturedDate:           d1.AddDate(0, 0, 1),
		BatteryUsedPctCalc:     intPtr(-20),
		DistanceTraveledKmCalc: fp(50.0),
		DaysSpannedCalc:        intPtr(1),
	}

	entries := []charging.Entry{
		{ChargedOn: d1, StartBatteryPct: intPtr(30), EndBatteryPct: intPtr(50)}, // +20
	}

	got := deriveConsumedByDay([]telemetry.Snapshot{prev, cur}, nil, entries, d0, d1)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry, got %d", len(got))
	}
	entry := got[0]

	if !approxEqual(entry.ConsumedPct, 0) {
		t.Errorf("ConsumedPct: want 0, got %v", entry.ConsumedPct)
	}
	if !entry.Flagged {
		t.Error("want Flagged=true: ConsumedPct=0 with DistanceKm=50.0 > minFlagDistanceKm")
	}
	if entry.MissingChargingType != telemetry.MissingChargingTypeManual {
		t.Errorf("MissingChargingType: want MANUAL, got %v", entry.MissingChargingType)
	}
}

// TestDeriveConsumedByDay_ZeroWithLowDistanceNotFlagged covers design.md Test Contract
// (e): same zero-ConsumedPct setup as (d), but at or below minFlagDistanceKm must NOT
// flag. The threshold comparison is strict '>', so exactly 10.0 must NOT flag.
func TestDeriveConsumedByDay_ZeroWithLowDistanceNotFlagged(t *testing.T) {
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
			prev := telemetry.Snapshot{CapturedAt: t0, CapturedDate: d0.AddDate(0, 0, 1)}
			cur := telemetry.Snapshot{
				CapturedAt:             t1,
				CapturedDate:           d1.AddDate(0, 0, 1),
				BatteryUsedPctCalc:     intPtr(-20),
				DistanceTraveledKmCalc: fp(tc.distanceKm),
				DaysSpannedCalc:        intPtr(1),
			}
			entries := []charging.Entry{
				{ChargedOn: d1, StartBatteryPct: intPtr(30), EndBatteryPct: intPtr(50)}, // +20 -> ConsumedPct = 0
			}

			got := deriveConsumedByDay([]telemetry.Snapshot{prev, cur}, nil, entries, d0, d1)
			if len(got) != 1 {
				t.Fatalf("want exactly 1 entry, got %d", len(got))
			}
			if got[0].Flagged {
				t.Errorf("want Flagged=false at DistanceKm=%v", tc.distanceKm)
			}
		})
	}
}

// TestDeriveConsumedByDay_NilBatteryUsedPctCalc_Skipped covers design.md Test Contract
// (f): a row with no predecessor claim (BatteryUsedPctCalc = nil, e.g. the account's
// first-ever snapshot) must be skipped entirely -- no entry at all, never an entry with a
// zero/flagged placeholder value.
func TestDeriveConsumedByDay_NilBatteryUsedPctCalc_Skipped(t *testing.T) {
	d0 := day(2026, 8, 13)
	d1 := day(2026, 8, 14)
	t0 := time.Date(2026, 8, 14, 8, 30, 0, 0, time.UTC)
	t1 := time.Date(2026, 8, 15, 8, 30, 0, 0, time.UTC)

	prev := telemetry.Snapshot{CapturedAt: t0, CapturedDate: d0.AddDate(0, 0, 1)}
	cur := telemetry.Snapshot{
		CapturedAt:         t1,
		CapturedDate:       d1.AddDate(0, 0, 1),
		BatteryUsedPctCalc: nil,
		DaysSpannedCalc:    intPtr(1),
	}

	got := deriveConsumedByDay([]telemetry.Snapshot{prev, cur}, nil, nil, d0, d1)
	if len(got) != 0 {
		t.Fatalf("want empty slice (no entry for cur's day), got %d entries: %+v", len(got), got)
	}
}

// TestDeriveConsumedByDay_MultiDaySpan_OneEntry covers design.md Test Contract (g) /
// roadmap D8: a multi-day gap between snapshots produces exactly ONE bar, dated the
// later snapshot's own effective day (design.md D-B3), with every charge event inside the
// span summed regardless of which intervening day it falls on. No entry exists for any
// intervening day -- there is no snapshot row for them to attach to.
func TestDeriveConsumedByDay_MultiDaySpan_OneEntry(t *testing.T) {
	prevDay := day(2026, 8, 10)
	curDay := day(2026, 8, 13)
	t0 := time.Date(2026, 8, 11, 8, 30, 0, 0, time.UTC)
	t1 := time.Date(2026, 8, 14, 8, 30, 0, 0, time.UTC)

	prev := telemetry.Snapshot{CapturedAt: t0, CapturedDate: prevDay.AddDate(0, 0, 1)}
	cur := telemetry.Snapshot{
		CapturedAt:         t1,
		CapturedDate:       curDay.AddDate(0, 0, 1),
		BatteryUsedPctCalc: intPtr(-30),
		DaysSpannedCalc:    intPtr(3),
	}

	sessions := []telemetry.SuperchargerSession{
		{ChargeStopDateTime: t0.Add(24 * time.Hour), StartBatteryPct: intPtr(40), EndBatteryPct: intPtr(55)}, // +15
	}
	entries := []charging.Entry{
		{ChargedOn: day(2026, 8, 12), StartBatteryPct: intPtr(20), EndBatteryPct: intPtr(45)}, // +25
	}

	start := day(2026, 8, 1)
	end := day(2026, 8, 31)

	got := deriveConsumedByDay([]telemetry.Snapshot{prev, cur}, sessions, entries, start, end)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry (not one per intervening day), got %d: %+v", len(got), got)
	}
	entry := got[0]

	if !entry.Date.Equal(curDay) {
		t.Errorf("Date: want %v (the row's own effective day, never re-attributed), got %v", curDay, entry.Date)
	}
	if entry.DaysSpanned != 3 {
		t.Errorf("DaysSpanned: want 3, got %d", entry.DaysSpanned)
	}
	wantConsumed := -30.0 + 40.0 // -30 + 15 (Supercharger) + 25 (manual)
	if !approxEqual(entry.ConsumedPct, wantConsumed) {
		t.Errorf("ConsumedPct: want %v, got %v", wantConsumed, entry.ConsumedPct)
	}
	if entry.Flagged {
		t.Error("want Flagged=false")
	}

	for _, e := range got {
		if e.Date.Equal(day(2026, 8, 11)) || e.Date.Equal(day(2026, 8, 12)) {
			t.Errorf("unexpected entry for intervening day %v -- multi-day spans must not be special-cased into extra bars", e.Date)
		}
	}
}

// TestSumSuperchargerPctBetween_IntervalBoundary_InclusiveStartExclusiveEnd covers
// design.md Test Contract (h) / roadmap D12: the matching interval is [from, to) --
// lower-inclusive, upper-exclusive -- for both sumSuperchargerPctBetween and
// inferMissingChargingType, which share the identical boundary predicate.
func TestSumSuperchargerPctBetween_IntervalBoundary_InclusiveStartExclusiveEnd(t *testing.T) {
	from := time.Date(2026, 8, 14, 8, 30, 0, 0, time.UTC)
	to := time.Date(2026, 8, 15, 8, 30, 0, 0, time.UTC)

	sessionAtFrom := telemetry.SuperchargerSession{ChargeStopDateTime: from, StartBatteryPct: intPtr(10), EndBatteryPct: intPtr(30)} // +20, included (lower-inclusive)
	sessionAtTo := telemetry.SuperchargerSession{ChargeStopDateTime: to, StartBatteryPct: intPtr(40), EndBatteryPct: intPtr(90)}     // +50, excluded (upper-exclusive)

	got := sumSuperchargerPctBetween([]telemetry.SuperchargerSession{sessionAtFrom, sessionAtTo}, from, to)
	want := 20.0
	if !approxEqual(got, want) {
		t.Errorf("sumSuperchargerPctBetween: want %v (only the session exactly AT 'from'), got %v", want, got)
	}

	// inferMissingChargingType shares the identical [from, to) boundary (design.md D-B5):
	// a NULL-percentage session exactly at 'from' must be seen; one exactly at 'to' must
	// not.
	sessionAtFromNull := telemetry.SuperchargerSession{ChargeStopDateTime: from, StartBatteryPct: nil, EndBatteryPct: intPtr(30)}
	sessionAtToNull := telemetry.SuperchargerSession{ChargeStopDateTime: to, StartBatteryPct: nil, EndBatteryPct: intPtr(90)}

	gotType := inferMissingChargingType([]telemetry.SuperchargerSession{sessionAtToNull, sessionAtFromNull}, from, to)
	if gotType != telemetry.MissingChargingTypeSupercharger {
		t.Errorf("inferMissingChargingType: want SUPERCHARGER (the in-bound NULL session at 'from' must be seen), got %v", gotType)
	}

	gotTypeOnlyOutOfBound := inferMissingChargingType([]telemetry.SuperchargerSession{sessionAtToNull}, from, to)
	if gotTypeOnlyOutOfBound != telemetry.MissingChargingTypeManual {
		t.Errorf("inferMissingChargingType: want MANUAL (the only NULL session present is at 'to', excluded), got %v", gotTypeOnlyOutOfBound)
	}
}

// TestDeriveConsumedByDay_Stateless_ResolvesOnRecompute covers design.md Test Contract
// (i) / D2: deriveConsumedByDay is a pure function of its inputs with no memory between
// calls -- the precondition that makes cmd/poller's D7b delete-on-resolve reconciliation
// work for free (a resolved day simply stops appearing flagged on the next recompute).
func TestDeriveConsumedByDay_Stateless_ResolvesOnRecompute(t *testing.T) {
	d0 := day(2026, 8, 13)
	d1 := day(2026, 8, 14)
	t0 := time.Date(2026, 8, 14, 8, 30, 0, 0, time.UTC)
	t1 := time.Date(2026, 8, 15, 8, 30, 0, 0, time.UTC)

	prev := telemetry.Snapshot{CapturedAt: t0, CapturedDate: d0.AddDate(0, 0, 1)}
	cur := telemetry.Snapshot{
		CapturedAt:         t1,
		CapturedDate:       d1.AddDate(0, 0, 1),
		BatteryUsedPctCalc: intPtr(-10),
		DaysSpannedCalc:    intPtr(1),
	}
	snapshots := []telemetry.Snapshot{prev, cur}

	firstCall := deriveConsumedByDay(snapshots, nil, nil, d0, d1)
	if len(firstCall) != 1 || !firstCall[0].Flagged {
		t.Fatalf("first call: want one flagged entry, got %+v", firstCall)
	}

	// The user backfills the missing manual record; recomputed from the SAME snapshots
	// with no state carried over from the first call (D2).
	resolvingEntries := []charging.Entry{
		{ChargedOn: d1, StartBatteryPct: intPtr(30), EndBatteryPct: intPtr(40)}, // +10
	}
	secondCall := deriveConsumedByDay(snapshots, nil, resolvingEntries, d0, d1)
	if len(secondCall) != 1 {
		t.Fatalf("second call: want exactly 1 entry, got %d", len(secondCall))
	}
	if secondCall[0].Flagged {
		t.Errorf("second call: want Flagged=false after backfill, got Flagged=true (ConsumedPct=%v) -- suggests memory between calls", secondCall[0].ConsumedPct)
	}
	wantConsumed := 0.0 // -10 + 10
	if !approxEqual(secondCall[0].ConsumedPct, wantConsumed) {
		t.Errorf("second call ConsumedPct: want %v, got %v", wantConsumed, secondCall[0].ConsumedPct)
	}
}

// TestDeriveConsumedByDay_BucketsInPollerZone_NotEffectiveDate covers design.md Test
// Contract (m) -- the SINGLE BINDING REGRESSION TEST for the owner's roadmap D18 ruling.
// A nominal 03:30-Bogota fixture passes under EITHER the zoned rule or the overruled UTC
// rule, so it proves nothing about D18. This fixture is instead the D-B7 scenario-3 edge:
// cur was captured in the Bogota EVENING (20:00 Aug 13 = 01:00Z Aug 14), where the zoned
// day and the UTC day disagree by exactly one calendar day.
//
// cur.EffectiveDate is deliberately set to the wrong (UTC, overruled) value -- the ONE
// fixture in this file that populates it -- specifically so a regression to reading it
// is caught by the negative assertion below. Do NOT weaken either assertion (leader
// dispatch instruction, design.md Test Contract (m)).
func TestDeriveConsumedByDay_BucketsInPollerZone_NotEffectiveDate(t *testing.T) {
	prev := telemetry.Snapshot{
		CapturedAt:   time.Date(2026, 8, 12, 8, 30, 0, 0, time.UTC), // 03:30 Bogota Aug 12
		CapturedDate: day(2026, 8, 12),
	}
	cur := telemetry.Snapshot{
		CapturedAt:   time.Date(2026, 8, 14, 1, 0, 0, 0, time.UTC), // 20:00 Bogota Aug 13
		CapturedDate: day(2026, 8, 13),
		// The value internal/telemetry/mapping.go:119 really computes: CapturedAt - 1 day,
		// in UTC -- i.e. the day the OVERRULED D-B7 would have bucketed this row under.
		EffectiveDate:      time.Date(2026, 8, 13, 1, 0, 0, 0, time.UTC),
		BatteryUsedPctCalc: intPtr(20),
		DaysSpannedCalc:    intPtr(1),
	}

	got := deriveConsumedByDay([]telemetry.Snapshot{prev, cur}, nil, nil, day(2026, 8, 1), day(2026, 8, 31))
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry, got %d: %+v", len(got), got)
	}
	entry := got[0]

	if !approxEqual(entry.ConsumedPct, 20) {
		t.Errorf("ConsumedPct: want 20, got %v", entry.ConsumedPct)
	}
	if entry.Flagged {
		t.Error("want Flagged=false")
	}

	wantZonedDate := day(2026, 8, 12) // CapturedDate - 1 day: the zoned answer
	wrongUTCDate := day(2026, 8, 13)  // EffectiveDate's own day: the overruled UTC answer
	if !entry.Date.Equal(wantZonedDate) {
		t.Errorf("Date: want %v (CapturedDate - 1 day, the zoned answer), got %v", wantZonedDate, entry.Date)
	}
	if entry.Date.Equal(wrongUTCDate) {
		t.Errorf("Date must NOT equal %v -- that is the overruled UTC EffectiveDate answer; bucketing has regressed to reading EffectiveDate", wrongUTCDate)
	}

	// D-B12 identity: effectiveDay(cur) - effectiveDay(prev) == *cur.DaysSpannedCalc, in
	// whole days. This is a SECOND, independent guard on the same regression: under the
	// overruled UTC rule the same fixtures give 2 days != 1.
	gotSpanDays := int(effectiveDay(cur).Sub(effectiveDay(prev)).Hours() / 24)
	if gotSpanDays != *cur.DaysSpannedCalc {
		t.Errorf("effectiveDay(cur)-effectiveDay(prev): want %d day(s) (matching DaysSpannedCalc), got %d", *cur.DaysSpannedCalc, gotSpanDays)
	}
}

// TestDeriveConsumedByDay_ZoneShiftedRowAtWindowEnd_Emitted covers design.md Test
// Contract (n) / D-B13: a row whose ZONED effective day lands exactly on the window's
// 'end' -- while its UTC EffectiveDate day would fall one day past it -- must still be
// emitted. This is the derivation half of the widened snapshot fetch; T5.4 in
// reader_test.go asserts the fetch itself widens far enough to bring such a row back.
func TestDeriveConsumedByDay_ZoneShiftedRowAtWindowEnd_Emitted(t *testing.T) {
	start := day(2026, 8, 1)
	end := day(2026, 8, 20)

	prev := telemetry.Snapshot{
		CapturedAt:   time.Date(2026, 8, 20, 8, 30, 0, 0, time.UTC),
		CapturedDate: day(2026, 8, 20), // zoned effective day 2026-08-19
	}
	cur := telemetry.Snapshot{
		// 21:00 Bogota Aug 21 = 02:00Z Aug 22.
		CapturedAt:         time.Date(2026, 8, 22, 2, 0, 0, 0, time.UTC),
		CapturedDate:       day(2026, 8, 21), // zoned effective day 2026-08-20 == end
		BatteryUsedPctCalc: intPtr(5),
		DaysSpannedCalc:    intPtr(1),
	}

	got := deriveConsumedByDay([]telemetry.Snapshot{prev, cur}, nil, nil, start, end)
	if len(got) != 1 {
		t.Fatalf("want the zone-shifted row to be emitted at the window's upper edge, got %d entries: %+v", len(got), got)
	}
	wantDate := day(2026, 8, 20)
	if !got[0].Date.Equal(wantDate) {
		t.Errorf("Date: want %v (the row's zoned effective day, equal to 'end'), got %v", wantDate, got[0].Date)
	}
}
