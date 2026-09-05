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
	if len(presets) != 2 {
		t.Fatalf("want 2 presets, got %d", len(presets))
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
	if len(presets) != 2 {
		t.Fatalf("want 2 presets, got %d", len(presets))
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
