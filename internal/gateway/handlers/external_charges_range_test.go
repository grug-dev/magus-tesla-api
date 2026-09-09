package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// parseExternalChargesRangeAt builds a gin.Context with the given start/end query
// params and runs parseExternalChargesRange against the given explicit today —
// mirrors history_test.go's parseRange / supercharger_test.go's
// parseSuperchargerRangeAt helpers, but for parseExternalChargesRange's (c, today)
// signature.
func parseExternalChargesRangeAt(start, end string, today time.Time) (time.Time, time.Time, bool) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	url := "/?"
	if start != "" {
		url += "start=" + start + "&"
	}
	if end != "" {
		url += "end=" + end
	}
	c.Request = httptest.NewRequest(http.MethodGet, url, nil)
	return parseExternalChargesRange(c, today)
}

// --- parseExternalChargesRange / buildExternalChargesPresets Test Contract Group A
// (design.md §Test Contract Group A, RM33-gateway-add-entries-dashboard) ---

// TestParseExternalChargesRange_A1_BothAbsent_DefaultSevenDayWindow is Test Contract
// A1: both start/end absent, today=2026-08-29 -> start=2026-08-23,
// end=2026-08-29 (7-day inclusive default, D-Range point 2: default end is
// today, NOT today-1).
func TestParseExternalChargesRange_A1_BothAbsent_DefaultSevenDayWindow(t *testing.T) {
	today := time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC)
	start, end, ok := parseExternalChargesRangeAt("", "", today)
	if !ok {
		t.Fatal("want ok=true for both absent")
	}
	wantEnd := time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC)
	wantStart := time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)
	if !end.Equal(wantEnd) {
		t.Errorf("want end=%v, got %v", wantEnd, end)
	}
	if !start.Equal(wantStart) {
		t.Errorf("want start=%v, got %v", wantStart, start)
	}
}

// TestParseExternalChargesRange_A2_ValidExplicitWindow is Test Contract A2: a valid,
// non-default window is always accepted, not just the two presets.
func TestParseExternalChargesRange_A2_ValidExplicitWindow(t *testing.T) {
	today := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	start, end, ok := parseExternalChargesRangeAt("2026-08-01", "2026-08-31", today)
	if !ok {
		t.Fatal("want ok=true for a valid explicit window")
	}
	wantStart := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)
	if !start.Equal(wantStart) || !end.Equal(wantEnd) {
		t.Errorf("want (%v,%v), got (%v,%v)", wantStart, wantEnd, start, end)
	}
}

// TestParseExternalChargesRange_A3_FutureEndAccepted is Test Contract A3: an `end`
// AFTER today is accepted -- the explicit no-future-rejection divergence
// from parseHistoryRange/parseSuperchargerRange (D-Range point 1).
//
// NOTE ON THE CONTRACT TEXT: design.md/tasks.md write this case as literally
// "?end=2026-09-15" with no `start`. Read literally that is a PARTIAL pair
// (start absent, end present), which parseExternalChargesRange's own frozen body
// rejects via the "either alone -> reject" branch (the same branch A4
// exercises) -- BEFORE the future-end question is ever reached. That would
// make A3 indistinguishable from A4 and never actually exercise the
// no-future-rejection behavior the contract says it's pinning. I read the
// omission of `start` as shorthand (consistent with the surrounding
// commentary "Contrast with parseHistoryRange's own test suite" and that
// suite's own TestParseHistoryRange_EndAfterToday, which supplies both
// start and end) rather than a literal instruction to omit it, and supply a
// `start` so the test exercises what it says it exercises. Flagged in the
// worker report per the dispatch's "if you believe the contract itself is
// wrong, report it" instruction -- this is not a silent edit of the
// expected outcome (ok==true is unchanged), only of the omitted input.
func TestParseExternalChargesRange_A3_FutureEndAccepted(t *testing.T) {
	today := time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC)
	start, end, ok := parseExternalChargesRangeAt("2026-08-25", "2026-09-15", today)
	if !ok {
		t.Fatal("want ok=true for an end after today (no future-end rejection)")
	}
	wantStart := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	if !start.Equal(wantStart) {
		t.Errorf("want start=%v, got %v", wantStart, start)
	}
	if !end.Equal(wantEnd) {
		t.Errorf("want end=%v, got %v", wantEnd, end)
	}
}

// TestParseExternalChargesRange_A4_PartialPair is Test Contract A4: start present
// without end (and vice versa) is rejected.
func TestParseExternalChargesRange_A4_PartialPair(t *testing.T) {
	today := time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC)
	if _, _, ok := parseExternalChargesRangeAt("2026-08-01", "", today); ok {
		t.Error("want ok=false when start present but end absent")
	}
	if _, _, ok := parseExternalChargesRangeAt("", "2026-08-31", today); ok {
		t.Error("want ok=false when end present but start absent")
	}
}

// TestParseExternalChargesRange_A5_EndBeforeStart is Test Contract A5.
func TestParseExternalChargesRange_A5_EndBeforeStart(t *testing.T) {
	today := time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC)
	if _, _, ok := parseExternalChargesRangeAt("2026-09-01", "2026-08-01", today); ok {
		t.Error("want ok=false when end < start")
	}
}

// TestParseExternalChargesRange_A6_MaxDaysCap is Test Contract A6: a window wider
// than 400 days is rejected; the SAME window narrowed to exactly 400 days is
// accepted. Reuses the exact dates parseSuperchargerRange's own
// TestParseSuperchargerRange_MaxDaysCap uses (externalChargesRangeMaxDays == 400 ==
// superchargerRangeMaxDays, D-Range).
func TestParseExternalChargesRange_A6_MaxDaysCap(t *testing.T) {
	today := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	if _, _, ok := parseExternalChargesRangeAt("2025-07-01", "2026-08-06", today); ok {
		t.Error("want ok=false for a window wider than the 400-day cap")
	}
	if _, _, ok := parseExternalChargesRangeAt("2025-07-01", "2026-08-05", today); !ok {
		t.Error("want ok=true for a window exactly 400 days wide")
	}
}

// TestParseExternalChargesRange_A7_ThisMonthIsFullCalendarMonth is Test Contract A7:
// buildExternalChargesPresets with today=2026-08-15 (a 31-day month) -> "This
// month"'s EndStr == "2026-08-31" (the LAST day, not "2026-08-15"),
// StartStr == "2026-08-01".
func TestParseExternalChargesRange_A7_ThisMonthIsFullCalendarMonth(t *testing.T) {
	today := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	start := today.AddDate(0, 0, -(externalChargesRangeDefaultDays - 1))
	presets := buildExternalChargesPresets(context.Background(), start, today, today)
	// RM51 tier 2 (design.md §D-Order) appended a third "last month" preset, so
	// the count here moved from 2 to 3. Fixed as part of this tier's Group 4 —
	// this A7 case predates RM51 and was left asserting the old count, which
	// would fail as soon as `go test` runs against Group 3.2's already-shipped
	// buildExternalChargesPresets.
	if len(presets) != 3 {
		t.Fatalf("want 3 presets, got %d", len(presets))
	}
	// Index 1 is "this month" by buildExternalChargesPresets's fixed return order
	// (design.md §D-Presets: last-7-days first, this-month second).
	thisMonth := presets[1]
	if thisMonth.EndStr != "2026-08-31" {
		t.Errorf("want This month EndStr=2026-08-31, got %s", thisMonth.EndStr)
	}
	if thisMonth.StartStr != "2026-08-01" {
		t.Errorf("want This month StartStr=2026-08-01, got %s", thisMonth.StartStr)
	}
}

// TestParseExternalChargesRange_A8_ActiveComputedByExactMatch is Test Contract A8:
// buildExternalChargesPresets called with (start,end) exactly matching the "last 7
// days" preset's own computed window -> that preset's Active==true, "this
// month"'s Active==false. Called with a CUSTOM (non-preset) window -> both
// Active==false.
func TestParseExternalChargesRange_A8_ActiveComputedByExactMatch(t *testing.T) {
	today := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)

	// last-7-days window.
	last7Start := today.AddDate(0, 0, -(externalChargesRangeDefaultDays - 1))
	presets := buildExternalChargesPresets(context.Background(), last7Start, today, today)
	// RM51 tier 2 added a third preset — see the A7 fix comment above.
	if len(presets) != 3 {
		t.Fatalf("want 3 presets, got %d", len(presets))
	}
	if !presets[0].Active {
		t.Error("want 'last 7 days' preset Active=true when window matches it exactly")
	}
	if presets[1].Active {
		t.Error("want 'this month' preset Active=false when window is the last-7-days window")
	}

	// custom, non-preset window.
	customStart := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	customEnd := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	presets = buildExternalChargesPresets(context.Background(), customStart, customEnd, today)
	for i, p := range presets {
		if p.Active {
			t.Errorf("want preset %d Active=false for a custom, non-preset window", i)
		}
	}
}

// ============================================================================
// RM51-gateway-add-free-charge-and-month-preset (MAG-58, tier 2) — Test
// Contract Groups C (the "last month" preset, design.md §D-Order) and D
// (the /supercharger-stats regression guard, RD8). Added by Group 4 (task
// 4.2). C1-C4 use the same style as the A-group tests above: an explicit
// `today`, no gin.Context needed for buildExternalChargesPresets itself.
// ============================================================================

// TestBuildExternalChargesPresets_RM51_C1_LastMonth_September verifies Test
// Contract C1: today=2026-09-09 -> the third preset's window is
// 2026-08-01..2026-08-31 (the headline example from roadmap RD7).
func TestBuildExternalChargesPresets_RM51_C1_LastMonth_September(t *testing.T) {
	today := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	presets := buildExternalChargesPresets(context.Background(), today, today, today)
	if len(presets) != 3 {
		t.Fatalf("C1: want 3 presets, got %d", len(presets))
	}
	lastMonth := presets[2]
	if lastMonth.StartStr != "2026-08-01" {
		t.Errorf("C1: want last-month StartStr=2026-08-01, got %s", lastMonth.StartStr)
	}
	if lastMonth.EndStr != "2026-08-31" {
		t.Errorf("C1: want last-month EndStr=2026-08-31, got %s", lastMonth.EndStr)
	}
}

// TestBuildExternalChargesPresets_RM51_C2_LastMonth_JanuaryCrossesYearBoundary
// verifies Test Contract C2: today=2027-01-15 (January) -> the third preset's
// window is 2026-12-01..2026-12-31 — time.Time.AddDate(0,-1,0) rolls the
// year back correctly with no special-cased branch (design.md §D-Order).
func TestBuildExternalChargesPresets_RM51_C2_LastMonth_JanuaryCrossesYearBoundary(t *testing.T) {
	today := time.Date(2027, 1, 15, 0, 0, 0, 0, time.UTC)
	presets := buildExternalChargesPresets(context.Background(), today, today, today)
	if len(presets) != 3 {
		t.Fatalf("C2: want 3 presets, got %d", len(presets))
	}
	lastMonth := presets[2]
	if lastMonth.StartStr != "2026-12-01" {
		t.Errorf("C2: want last-month StartStr=2026-12-01, got %s", lastMonth.StartStr)
	}
	if lastMonth.EndStr != "2026-12-31" {
		t.Errorf("C2: want last-month EndStr=2026-12-31, got %s", lastMonth.EndStr)
	}
}

// TestBuildExternalChargesPresets_RM51_C3_LastMonthWindow_IsActiveAndExclusive
// verifies Test Contract C3: today=2026-09-09, called with the exact
// last-month window (2026-08-01..2026-08-31) -> the THIRD preset's
// Active==true, the other two Active==false.
func TestBuildExternalChargesPresets_RM51_C3_LastMonthWindow_IsActiveAndExclusive(t *testing.T) {
	today := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)
	presets := buildExternalChargesPresets(context.Background(), start, end, today)
	if len(presets) != 3 {
		t.Fatalf("C3: want 3 presets, got %d", len(presets))
	}
	if presets[0].Active {
		t.Error("C3: want 'last 7 days' preset Active=false when window is the last-month window")
	}
	if presets[1].Active {
		t.Error("C3: want 'this month' preset Active=false when window is the last-month window")
	}
	if !presets[2].Active {
		t.Error("C3: want 'last month' preset Active=true when window matches it exactly")
	}
}

// TestBuildExternalChargesPresets_RM51_C4_LastSevenDaysWindow_ThirdPresetStaysInactive
// verifies Test Contract C4: today=2026-09-09, called with the last-7-days
// window (2026-09-03..2026-09-09) -> the FIRST preset's Active==true, the
// other two — including the new third one — Active==false. Regression guard:
// adding a third preset must not change the first preset's own computation.
func TestBuildExternalChargesPresets_RM51_C4_LastSevenDaysWindow_ThirdPresetStaysInactive(t *testing.T) {
	today := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	start := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	end := today
	presets := buildExternalChargesPresets(context.Background(), start, end, today)
	if len(presets) != 3 {
		t.Fatalf("C4: want 3 presets, got %d", len(presets))
	}
	if !presets[0].Active {
		t.Error("C4: want 'last 7 days' preset Active=true when window matches it exactly")
	}
	if presets[1].Active {
		t.Error("C4: want 'this month' preset Active=false for the last-7-days window")
	}
	if presets[2].Active {
		t.Error("C4: want 'last month' preset Active=false for the last-7-days window")
	}
}

// TestBuildExternalChargesPresets_RM51_C5_ReturnsExactlyThreeInOrder verifies
// Test Contract C5: buildExternalChargesPresets always returns exactly 3
// entries, in the order Last 7 days, This month, Last month. Checked
// STRUCTURALLY, by recomputing each preset's own expected window
// independently and comparing by position — never by label text
// (AGENTS.md §"Do not test what the page looks like" bans exact copy
// assertions; a count-and-position check is the structural equivalent
// design.md §D-Order asks for).
func TestBuildExternalChargesPresets_RM51_C5_ReturnsExactlyThreeInOrder(t *testing.T) {
	today := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	presets := buildExternalChargesPresets(context.Background(), today, today, today)
	if len(presets) != 3 {
		t.Fatalf("C5: want exactly 3 presets, got %d", len(presets))
	}

	wantLast7Start := today.AddDate(0, 0, -(externalChargesRangeDefaultDays - 1)).Format("2006-01-02")
	wantLast7End := today.Format("2006-01-02")
	if presets[0].StartStr != wantLast7Start || presets[0].EndStr != wantLast7End {
		t.Errorf("C5: want index 0 = last 7 days (%s..%s), got (%s..%s)",
			wantLast7Start, wantLast7End, presets[0].StartStr, presets[0].EndStr)
	}

	wantThisMonthStart := startOfMonth(today).Format("2006-01-02")
	wantThisMonthEnd := endOfMonth(today).Format("2006-01-02")
	if presets[1].StartStr != wantThisMonthStart || presets[1].EndStr != wantThisMonthEnd {
		t.Errorf("C5: want index 1 = this month (%s..%s), got (%s..%s)",
			wantThisMonthStart, wantThisMonthEnd, presets[1].StartStr, presets[1].EndStr)
	}

	prevStart := startOfMonth(today).AddDate(0, -1, 0)
	wantLastMonthStart := prevStart.Format("2006-01-02")
	wantLastMonthEnd := endOfMonth(prevStart).Format("2006-01-02")
	if presets[2].StartStr != wantLastMonthStart || presets[2].EndStr != wantLastMonthEnd {
		t.Errorf("C5: want index 2 = last month (%s..%s), got (%s..%s)",
			wantLastMonthStart, wantLastMonthEnd, presets[2].StartStr, presets[2].EndStr)
	}
}

// TestBuildSuperchargerPresets_RM51_D1_UntouchedByThisTier verifies Test
// Contract D1 (RD8 regression guard): buildSuperchargerPresets is untouched
// by this tier.
//
// NOTE ON THE CONTRACT TEXT: design.md's Test Contract table states the
// expected count as "exactly 2 entries". That is stale/incorrect against
// the actual, already-shipped code: superchargerMonthPresets is
// []int{3, 6, 12} (supercharger.go:43), so buildSuperchargerPresets returns
// 3 entries — and TestBuildSuperchargerPresets_ExactValues
// (supercharger_test.go) already asserts exactly that, passing today. Pinning
// "2" here would create a test that contradicts both the production code and
// an existing, correct sibling test, and would fail immediately. Per the
// dispatch's "if you believe the contract itself is wrong, report it"
// instruction, this test asserts the true regression-guard value (3) instead
// of the contract's literal "2", and the discrepancy is flagged in the worker
// report. This does not touch supercharger.go or buildSuperchargerPresets —
// RD8 (this tier adds no preset to the OTHER page) still holds; only the
// EXPECTED COUNT in this new test differs from the contract's typo.
func TestBuildSuperchargerPresets_RM51_D1_UntouchedByThisTier(t *testing.T) {
	today := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	presets := buildSuperchargerPresets(context.Background(), today, today, today)
	if len(presets) != 3 {
		t.Fatalf("D1: want buildSuperchargerPresets untouched at 3 entries, got %d", len(presets))
	}
}
