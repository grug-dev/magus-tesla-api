package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/layouts"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// historyTestCtx is the language context used by every buildOdometerChart /
// buildBatteryChart call in this file (both now take an explicit ctx —
// design.md D3, RM24-gateway-translate-all-pages). Pinned to English so the
// pre-existing "no snapshot" tooltip substring assertions keep asserting
// against the resolved i18n string rather than a hardcoded literal, mirroring
// tier 2's T6.4 precedent (nav_test.go, nav_header_test.go).
var historyTestCtx = i18n.WithLang(context.Background(), account.LanguageEN)

// --- fakes for the history handler tests ---

// fakeHistoryReader is a test double for telemetry.Reader that records the
// SnapshotsByVehicleBetween call so tests can assert the correct readStart
// (start-1day lookback) and end were passed. LatestSnapshotsByAccount returns
// empty (history tests don't use it). SnapshotsByVehicleSince PANICS — the
// history handler no longer calls it (RM8 tier 2 rewired to Between), so a
// panic catches accidental re-wiring.
type fakeHistoryReader struct {
	historySnaps []telemetry.Snapshot
	historyErr   error
	// Captured Between call args so tests can assert.
	gotAccount    uuid.UUID
	gotTeslaID    int64
	gotStart      time.Time
	gotEnd        time.Time
	betweenCalled bool
}

func (f *fakeHistoryReader) LatestSnapshotsByAccount(_ context.Context, _ uuid.UUID) ([]telemetry.Snapshot, error) {
	return []telemetry.Snapshot{}, nil
}

// SnapshotsByVehicleSince PANICS — the history handler was rewired to Between
// in RM8 tier 2. A panic catches accidental re-wiring.
func (f *fakeHistoryReader) SnapshotsByVehicleSince(context.Context, uuid.UUID, int64, time.Time) ([]telemetry.Snapshot, error) {
	panic("fakeHistoryReader: SnapshotsByVehicleSince is no longer used by the history handler (RM8 tier 2 rewired to Between)")
}

// SnapshotsByVehicleBetween records the call and returns the configured snaps.
func (f *fakeHistoryReader) SnapshotsByVehicleBetween(_ context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) ([]telemetry.Snapshot, error) {
	f.gotAccount = accountID
	f.gotTeslaID = teslaID
	f.gotStart = start
	f.gotEnd = end
	f.betweenCalled = true
	return f.historySnaps, f.historyErr
}

// errTestHistory is a sentinel error for history handler tests.
var errTestHistory = errors.New("test history reader error")

// historyEngine builds a minimal Gin engine with session middleware and the
// history fragment route. Mirrors dashboardEngine / navHeaderEngine.
func historyEngine(h *Handler, uid uuid.UUID, selTeslaID int64, selVIN string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	store := cookie.NewStore([]byte("test-secret"))
	r.Use(sessions.Sessions("test", store))
	r.GET("/_session", func(c *gin.Context) {
		sess := sessions.Default(c)
		sess.Set("uid", uid.String())
		if selTeslaID != 0 {
			sess.Set(sessionTeslaIDKey, selTeslaID)
			sess.Set(sessionVINKey, selVIN)
		}
		_ = sess.Save()
		c.String(http.StatusOK, "ok")
	})
	r.GET("/ui/dashboard/history", h.DashboardHistoryFragment)
	return r
}

// newHandlerForHistory builds a Handler with the given fakeHistoryReader and one
// registered vehicle. Mirrors newHandlerForCharges.
func newHandlerForHistory(reader *fakeHistoryReader, teslaID int64, vin string) *Handler {
	acct := &fakeAccount{
		registered: []account.Vehicle{
			{TeslaID: teslaID, VIN: vin, DisplayName: "Test Vehicle"},
		},
	}
	return New(Deps{
		Account:         acct,
		Tesla:           &fakeTesla{},
		TelemetryReader: reader,
	})
}

// calendarDays returns the inclusive list of UTC-midnight calendar days in
// [start, end], oldest-first.
func calendarDays(start, end time.Time) []time.Time {
	var out []time.Time
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		out = append(out, startOfDay(d))
	}
	return out
}

// snapsForDays builds one snapshot per provided EffectiveDate calendar day
// (oldest-first), with a fixed odometer step and battery level. CapturedAt is
// the next-day nightly-capture morning (EffectiveDate + 1 day — mirrors the real
// telemetry rowToSnapshot mapping), so tests exercise realistic EffectiveDate
// values distinct from CapturedAt.
func snapsForDays(days []time.Time, odometerBase, odometerStep float64, batteryBase int) []telemetry.Snapshot {
	out := make([]telemetry.Snapshot, len(days))
	for i, d := range days {
		out[i] = telemetry.Snapshot{
			OdometerKm:      odometerBase + float64(i)*odometerStep,
			BatteryLevelPct: batteryBase + i,
			BatteryRangeKm:  300,
			CapturedAt:      startOfDay(d).AddDate(0, 0, 1),
			EffectiveDate:   startOfDay(d),
		}
	}
	return out
}

// parseRange builds a gin.Context with the given start/end query params and runs
// parseHistoryRange. Pure-function unit-test helper (no engine, no DB).
func parseRange(start, end string) (time.Time, time.Time, bool) {
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
	return parseHistoryRange(c, browserToday(c))
}

// parseRangeWithTZ is parseRange with a browser_tz cookie — exercises the
// browser-TZ-aware path of parseHistoryRange (the default end / the end<=today
// cap). tz == "" leaves the cookie unset (UTC fallback).
func parseRangeWithTZ(start, end, tz string) (time.Time, time.Time, bool) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	url := "/?"
	if start != "" {
		url += "start=" + start + "&"
	}
	if end != "" {
		url += "end=" + end
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	if tz != "" {
		req.AddCookie(&http.Cookie{Name: "browser_tz", Value: tz})
	}
	c.Request = req
	return parseHistoryRange(c, browserToday(c))
}

// --- parseHistoryRange unit tests (task 6.1, design D1) ---

func TestParseHistoryRange_BothAbsent_DefaultSixDayWindow(t *testing.T) {
	start, end, ok := parseRange("", "")
	if !ok {
		t.Fatal("want ok=true for both absent")
	}
	wantEnd := startOfDay(time.Now())
	wantStart := wantEnd.AddDate(0, 0, -historyRangeWindowDays)
	if !end.Equal(wantEnd) {
		t.Errorf("want end=%v, got %v", wantEnd, end)
	}
	if !start.Equal(wantStart) {
		t.Errorf("want start=%v, got %v", wantStart, start)
	}
}

func TestParseHistoryRange_ValidExplicitWindow(t *testing.T) {
	start, end, ok := parseRange("2026-08-03", "2026-08-07")
	if !ok {
		t.Fatal("want ok=true for valid window")
	}
	wantStart := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	if !start.Equal(wantStart) || !end.Equal(wantEnd) {
		t.Errorf("want (%v,%v), got (%v,%v)", wantStart, wantEnd, start, end)
	}
}

func TestParseHistoryRange_MalformedNonISO(t *testing.T) {
	if _, _, ok := parseRange("08-07", "2026-08-07"); ok {
		t.Error("want ok=false for malformed start")
	}
	if _, _, ok := parseRange("2026-08-03", "not-a-date"); ok {
		t.Error("want ok=false for malformed end")
	}
}

func TestParseHistoryRange_MissingPartner(t *testing.T) {
	if _, _, ok := parseRange("2026-08-03", ""); ok {
		t.Error("want ok=false when start present but end absent")
	}
	if _, _, ok := parseRange("", "2026-08-07"); ok {
		t.Error("want ok=false when end present but start absent")
	}
}

func TestParseHistoryRange_EndBeforeStart(t *testing.T) {
	if _, _, ok := parseRange("2026-08-07", "2026-08-03"); ok {
		t.Error("want ok=false when end < start")
	}
}

func TestParseHistoryRange_EndAfterToday(t *testing.T) {
	if _, _, ok := parseRange("2026-08-03", "2099-12-31"); ok {
		t.Error("want ok=false when end > today")
	}
}

func TestParseHistoryRange_WindowOverNinetyDays(t *testing.T) {
	// 2026-08-07 - 2026-05-01 = 98 days, end <= today, but window > 90.
	if _, _, ok := parseRange("2026-05-01", "2026-08-07"); ok {
		t.Error("want ok=false for window > 90 days")
	}
}

// --- browser-TZ tests (gateway-browser-tz-cookie) ---

func TestBrowserLocation_Fallbacks(t *testing.T) {
	// No cookie -> UTC.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if loc := browserLocationFromHeader(r); loc != time.UTC {
		t.Errorf("missing cookie: want UTC, got %v", loc)
	}
	// Valid IANA name -> that location.
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "browser_tz", Value: "America/Bogota"})
	if loc := browserLocationFromHeader(r); loc.String() != "America/Bogota" {
		t.Errorf("valid cookie: want America/Bogota, got %v", loc)
	}
	// Malformed / not-in-IANA name -> UTC.
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "browser_tz", Value: "Not/A/Zone"})
	if loc := browserLocationFromHeader(r); loc != time.UTC {
		t.Errorf("malformed cookie: want UTC, got %v", loc)
	}
	// Empty value -> UTC.
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "browser_tz", Value: ""})
	if loc := browserLocationFromHeader(r); loc != time.UTC {
		t.Errorf("empty cookie: want UTC, got %v", loc)
	}
}

// TestParseHistoryRange_DefaultUsesBrowserToday asserts that with a browser_tz
// cookie, the default-absent window's end == browser-today (in the browser's
// timezone), not UTC today. This proves parseHistoryRange consulted the cookie.
func TestParseHistoryRange_DefaultUsesBrowserToday(t *testing.T) {
	_, end, ok := parseRangeWithTZ("", "", "America/Bogota")
	if !ok {
		t.Fatal("want ok=true for both absent with a TZ cookie")
	}
	// Compute the expected browser-today the same way the helper does — in
	// the test this is a deterministic mirror of what parseHistoryRange did.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.AddCookie(&http.Cookie{Name: "browser_tz", Value: "America/Bogota"})
	wantEnd := browserToday(c)
	if !end.Equal(wantEnd) {
		t.Errorf("default end: want browser-today %v (loc %s), got %v (loc %s)",
			wantEnd, wantEnd.Location(), end, end.Location())
	}
	if end.Location().String() != "America/Bogota" {
		t.Errorf("default end Location: want America/Bogota, got %v", end.Location())
	}
}

// TestParseHistoryRange_EndCapUsesBrowserToday asserts the end<=today cap
// honors the browser's today. A direct UTC request with end=UTC-today is
// accepted (UTC fallback); the same date in a TZ where UTC-today is already
// tomorrow may be rejected — to keep the test deterministic, we compute the
// cap boundary as browserToday+1day for the SAME cookie the handler sees.
func TestParseHistoryRange_EndCapUsesBrowserToday(t *testing.T) {
	// Build the same context the handler will see so the test's "browserToday"
	// is the exact same instant the handler computes.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.AddCookie(&http.Cookie{Name: "browser_tz", Value: "America/Bogota"})
	today := browserToday(c)
	tomorrow := today.AddDate(0, 0, 1).Format("2006-01-02")
	todayStr := today.Format("2006-01-02")

	// end = browser-today → accepted.
	if _, _, ok := parseRangeWithTZ(todayStr, todayStr, "America/Bogota"); !ok {
		t.Errorf("end=today (browser): want ok=true, got false")
	}
	// end = browser-tomorrow → rejected (future in the browser's frame).
	if _, _, ok := parseRangeWithTZ(todayStr, tomorrow, "America/Bogota"); ok {
		t.Errorf("end=browser-tomorrow: want ok=false (future in browser TZ), got true")
	}
}

// TestParseHistoryRange_EndCapUsesBrowserToday_AcrossOffsets is table-driven
// over BOTH negative- and positive-UTC-offset zones, so a future zone is one
// line to add. It exists because the original TestParseHistoryRange_EndCapUsesBrowserToday
// only ever exercised America/Bogota (UTC-5, negative offset), where the old
// `e.After(today)` instant comparison happened to work — leaving the suite
// blind to MAG-7 review finding R1-1: for any POSITIVE-offset zone,
// UTC-midnight-of-D is always a LATER instant than local-midnight-of-D, so
// end=browser-local-today was spuriously rejected with HTTP 400. This test
// FAILS against the pre-fix `e.After(today)` comparison for every
// positive-offset zone below (Pacific/Auckland, Asia/Tokyo) — it only passed
// pre-fix for America/Bogota. Asserts both directions per zone: end=today is
// accepted, end=tomorrow is rejected.
func TestParseHistoryRange_EndCapUsesBrowserToday_AcrossOffsets(t *testing.T) {
	zones := []string{
		"America/Bogota",   // UTC-5 (negative offset — worked even pre-fix)
		"Pacific/Auckland", // UTC+12/+13 (positive offset — the R1-1 bug)
		"Asia/Tokyo",       // UTC+9 (positive offset — the R1-1 bug)
	}
	for _, tz := range zones {
		t.Run(tz, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
			c.Request.AddCookie(&http.Cookie{Name: "browser_tz", Value: tz})
			today := browserToday(c)
			todayStr := today.Format("2006-01-02")
			tomorrowStr := today.AddDate(0, 0, 1).Format("2006-01-02")

			// end = browser-local today → accepted.
			if _, _, ok := parseRangeWithTZ(todayStr, todayStr, tz); !ok {
				t.Errorf("%s: end=browser-local-today: want ok=true, got false", tz)
			}
			// end = browser-local tomorrow → rejected (future in the browser's frame).
			if _, _, ok := parseRangeWithTZ(todayStr, tomorrowStr, tz); ok {
				t.Errorf("%s: end=browser-local-tomorrow: want ok=false (future in browser TZ), got true", tz)
			}
		})
	}
}

// TestParseHistoryRange_NoCookieFallsBackToUTC asserts the UTC fallback: with
// no browser_tz cookie, the default end == UTC-today (the pre-browser-TZ behavior).
func TestParseHistoryRange_NoCookieFallsBackToUTC(t *testing.T) {
	_, end, ok := parseRangeWithTZ("", "", "")
	if !ok {
		t.Fatal("want ok=true for both absent, no TZ cookie")
	}
	wantEnd := startOfDay(time.Now())
	if !end.Equal(wantEnd) {
		t.Errorf("no cookie: want UTC end %v, got %v", wantEnd, end)
	}
	if end.Location() != time.UTC {
		t.Errorf("no cookie: want UTC location, got %v", end.Location())
	}
}

// TestBaseAuth_RendersBrowserTZScript asserts the inline <script> that sets
// the browser_tz cookie is present in the BaseAuth shell's rendered HTML, so
// every authenticated page (dashboard, charges, supercharger, …) carries the
// cookie setter. The script uses Intl.DateTimeFormat().resolvedOptions()
// and persists the IANA name in a 1-year SameSite=Lax cookie.
func TestBaseAuth_RendersBrowserTZScript(t *testing.T) {
	w := httptest.NewRecorder()
	if err := layouts.BaseAuth("Test", "/dashboard").Render(context.Background(), w); err != nil {
		t.Fatalf("BaseAuth render: %v", err)
	}
	body := w.Body.String()
	if !strings.Contains(body, "browser_tz=") {
		t.Errorf("BaseAuth must set the browser_tz cookie; got: %s", body)
	}
	if !strings.Contains(body, "Intl.DateTimeFormat().resolvedOptions().timeZone") {
		t.Errorf("BaseAuth must read the IANA TZ via Intl.DateTimeFormat; got: %s", body)
	}
	if !strings.Contains(body, "SameSite=Lax") {
		t.Errorf("BaseAuth cookie must be SameSite=Lax; got: %s", body)
	}
}

// TestBase_DoesNotRenderBrowserTZScript asserts the anonymous layouts.Base
// shell (used by "/" and "/login") does NOT contain the browser_tz cookie
// script — the spec ("The browser_tz cookie is set on every authenticated
// page load") requires the script to live ONLY in BaseAuth. Guards against a
// future shell refactor accidentally moving the script into the shared/public
// shell (MAG-7 review finding R1-5).
func TestBase_DoesNotRenderBrowserTZScript(t *testing.T) {
	w := httptest.NewRecorder()
	if err := layouts.Base("Test").Render(context.Background(), w); err != nil {
		t.Fatalf("Base render: %v", err)
	}
	body := w.Body.String()
	if strings.Contains(body, "browser_tz=") {
		t.Errorf("anonymous Base shell must NOT set the browser_tz cookie; got: %s", body)
	}
	if strings.Contains(body, "Intl.DateTimeFormat") {
		t.Errorf("anonymous Base shell must NOT contain the Intl.DateTimeFormat script; got: %s", body)
	}
}

// --- labelVerticalFor unit tests (task 2.5 retarget) ---

// TestLabelVerticalFor_OrientationByBarCount asserts the wide/narrow split on
// the fixed [start..end] axis (RM8 design D3): horizontal (false) for 6-bar
// windows, rotated vertical (true) for 14- and 30-bar windows.
func TestLabelVerticalFor_OrientationByBarCount(t *testing.T) {
	tests := []struct {
		numBars int
		want    bool
	}{
		{6, false},
		{13, false},
		{14, true},
		{30, true},
		{90, true},
	}
	for _, tc := range tests {
		got := labelVerticalFor(tc.numBars)
		if got != tc.want {
			t.Errorf("labelVerticalFor(%d): want %v, got %v", tc.numBars, tc.want, got)
		}
	}
}

// --- buildOdometerChart fixed-axis unit tests (task 6.3, design D3) ---

func TestBuildOdometerChart_EmptyWhenFewerThanTwoSnapshots(t *testing.T) {
	start := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	// 0 snapshots.
	c := buildOdometerChart(historyTestCtx, nil, start, end)
	if !c.Empty {
		t.Error("want Empty=true for 0 snapshots")
	}
	// 1 snapshot (only the lookback).
	c = buildOdometerChart(historyTestCtx, []telemetry.Snapshot{{OdometerKm: 1000, EffectiveDate: start.AddDate(0, 0, -1)}}, start, end)
	if !c.Empty {
		t.Error("want Empty=true for 1 snapshot")
	}
}

func TestBuildOdometerChart_FixedAxis_FullWindowWithLookback(t *testing.T) {
	start := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC) // 5-day inclusive window
	lookback := start.AddDate(0, 0, -1)                // 08-02 seeds the first delta
	snaps := snapsForDays(append([]time.Time{lookback}, calendarDays(start, end)...), 1000, 10, 70)
	c := buildOdometerChart(historyTestCtx, snaps, start, end)
	if c.Empty {
		t.Fatal("want non-empty chart")
	}
	if len(c.Bars) != 5 {
		t.Fatalf("want exactly 5 bars (one per day incl end), got %d", len(c.Bars))
	}
	// Labels are 08-03..08-07 (the lookback 08-02 is NOT a displayed bar).
	want := []string{"08-03", "08-04", "08-05", "08-06", "08-07"}
	for i, l := range want {
		if c.Bars[i].Label != l {
			t.Errorf("bar[%d].Label: want %q, got %q", i, l, c.Bars[i].Label)
		}
		if !c.Bars[i].Present {
			t.Errorf("bar[%d]: want Present=true", i)
		}
	}
	// First bar's delta uses the lookback snapshot (odometer 1000 → first window
	// snap 1010 = 10 km). All deltas equal 10 → all bars at 100%.
	if c.Bars[0].HeightPct != 100 {
		t.Errorf("first bar HeightPct: want 100 (max delta), got %d", c.Bars[0].HeightPct)
	}
}

func TestBuildOdometerChart_FixedAxis_MissingDayEmptyLabeledBar(t *testing.T) {
	start := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC) // 5-day window
	// Snaps for 08-02 (lookback), 08-03, 08-04, 08-06, 08-07 — MISSING 08-05.
	days := []time.Time{
		start.AddDate(0, 0, -1), // 08-02 lookback
		start,                   // 08-03
		start.AddDate(0, 0, 1),  // 08-04
		// 08-05 deliberately absent
		start.AddDate(0, 0, 3), // 08-06
		start.AddDate(0, 0, 4), // 08-07
	}
	snaps := snapsForDays(days, 1000, 10, 70)
	c := buildOdometerChart(historyTestCtx, snaps, start, end)
	if c.Empty {
		t.Fatal("want non-empty chart")
	}
	if len(c.Bars) != 5 {
		t.Fatalf("want exactly 5 bars (axis fixed regardless of gaps), got %d", len(c.Bars))
	}
	// 08-05 is the missing-day bar (index 2).
	miss := c.Bars[2]
	if miss.Label != "08-05" {
		t.Errorf("missing-day Label: want 08-05, got %q", miss.Label)
	}
	if miss.Present {
		t.Error("missing-day bar must be Present=false")
	}
	if miss.HeightPct != 0 {
		t.Errorf("missing-day HeightPct: want 0, got %d", miss.HeightPct)
	}
	if !strings.Contains(miss.Tooltip, "no snapshot") {
		t.Errorf("missing-day tooltip should say 'no snapshot', got %q", miss.Tooltip)
	}
	// Surrounding bars (08-04, 08-06) keep their own labels — no shift to fill.
	if c.Bars[1].Label != "08-04" || c.Bars[3].Label != "08-06" {
		t.Errorf("surrounding labels must not shift; got %q, %q", c.Bars[1].Label, c.Bars[3].Label)
	}
}

func TestBuildOdometerChart_NegativeDeltaClampedToZero(t *testing.T) {
	start := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC) // 2-day window
	snaps := []telemetry.Snapshot{
		{OdometerKm: 1000, EffectiveDate: start.AddDate(0, 0, -1)}, // 08-02 lookback
		{OdometerKm: 900, EffectiveDate: start},                    // 08-03 — negative delta (clock skew)
		{OdometerKm: 1050, EffectiveDate: start.AddDate(0, 0, 1)},  // 08-04
	}
	c := buildOdometerChart(historyTestCtx, snaps, start, end)
	if c.Empty {
		t.Fatal("want non-empty for 3 snapshots")
	}
	if len(c.Bars) != 2 {
		t.Fatalf("want 2 bars, got %d", len(c.Bars))
	}
	// First bar (08-03): 900-1000 = -100 → clamped to 0.
	if c.Bars[0].HeightPct != 0 {
		t.Errorf("negative delta bar HeightPct: want 0, got %d", c.Bars[0].HeightPct)
	}
	if !c.Bars[0].Present {
		t.Error("negative-delta bar is still a PRESENT bar (backed by a snapshot)")
	}
}

func TestBuildOdometerChart_TooltipUsesEffectiveDateMMDD(t *testing.T) {
	// CapturedAt and EffectiveDate deliberately differ: the tooltip/label date
	// comes from the axis day (== EffectiveDate calendar day), never from
	// CapturedAt. Proves the source on the fixed axis.
	start := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 1)
	snaps := []telemetry.Snapshot{
		{OdometerKm: 12000, CapturedAt: time.Date(2026, 8, 7, 3, 30, 0, 0, time.UTC), EffectiveDate: time.Date(2026, 8, 6, 3, 30, 0, 0, time.UTC)}, // 08-06 lookback
		{OdometerKm: 12100, CapturedAt: time.Date(2026, 8, 8, 3, 30, 0, 0, time.UTC), EffectiveDate: time.Date(2026, 8, 7, 3, 30, 0, 0, time.UTC)}, // 08-07
		{OdometerKm: 12200, CapturedAt: time.Date(2026, 8, 9, 3, 30, 0, 0, time.UTC), EffectiveDate: time.Date(2026, 8, 8, 3, 30, 0, 0, time.UTC)}, // 08-08
	}
	c := buildOdometerChart(historyTestCtx, snaps, start, end)
	if c.Empty || len(c.Bars) != 2 {
		t.Fatalf("want 2 bars, got %d (empty=%v)", len(c.Bars), c.Empty)
	}
	bar := c.Bars[0]
	if bar.Label != "08-07" {
		t.Errorf("want Label=08-07 (axis day), got %q", bar.Label)
	}
	if !strings.Contains(bar.Tooltip, "08-07") {
		t.Errorf("tooltip must contain 08-07, got %q", bar.Tooltip)
	}
	if strings.Contains(bar.Tooltip, "2026-08-08") || strings.Contains(bar.Tooltip, "08-08") {
		t.Errorf("tooltip must NOT contain the CapturedAt-derived date, got %q", bar.Tooltip)
	}
}

// --- buildBatteryChart fixed-axis unit tests (task 6.4, design D3) ---

func TestBuildBatteryChart_EmptyWhenNoSnapshots(t *testing.T) {
	start := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	if c := buildBatteryChart(historyTestCtx, nil, start, end); !c.Empty {
		t.Error("want Empty for nil snaps")
	}
	if c := buildBatteryChart(historyTestCtx, []telemetry.Snapshot{}, start, end); !c.Empty {
		t.Error("want Empty for empty snaps")
	}
}

func TestBuildBatteryChart_FixedAxis_FullWindow(t *testing.T) {
	start := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC) // 5-day window
	// Window snaps only (battery chart does not consume the lookback).
	snaps := snapsForDays(calendarDays(start, end), 0, 0, 70)
	c := buildBatteryChart(historyTestCtx, snaps, start, end)
	if c.Empty {
		t.Fatal("want non-empty chart")
	}
	if len(c.Bars) != 5 {
		t.Fatalf("want exactly 5 bars, got %d", len(c.Bars))
	}
	want := []string{"08-03", "08-04", "08-05", "08-06", "08-07"}
	for i, l := range want {
		if c.Bars[i].Label != l {
			t.Errorf("bar[%d].Label: want %q, got %q", i, l, c.Bars[i].Label)
		}
		if c.Bars[i].HeightPct != 70+i {
			t.Errorf("bar[%d].HeightPct: want %d, got %d", i, 70+i, c.Bars[i].HeightPct)
		}
		if !c.Bars[i].Present {
			t.Errorf("bar[%d]: want Present=true", i)
		}
	}
}

func TestBuildBatteryChart_FixedAxis_MissingDayEmptyLabeledBar(t *testing.T) {
	start := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC) // 5-day window
	// Snaps for 08-03, 08-04, 08-06, 08-07 — MISSING 08-05.
	days := []time.Time{start, start.AddDate(0, 0, 1), start.AddDate(0, 0, 3), start.AddDate(0, 0, 4)}
	snaps := snapsForDays(days, 0, 0, 70)
	c := buildBatteryChart(historyTestCtx, snaps, start, end)
	if c.Empty {
		t.Fatal("want non-empty (partial axis is NOT an empty chart)")
	}
	if len(c.Bars) != 5 {
		t.Fatalf("want exactly 5 bars (axis fixed), got %d", len(c.Bars))
	}
	miss := c.Bars[2] // 08-05
	if miss.Label != "08-05" {
		t.Errorf("missing-day Label: want 08-05, got %q", miss.Label)
	}
	if miss.Present {
		t.Error("missing-day bar must be Present=false")
	}
	if miss.HeightPct != 0 {
		t.Errorf("missing-day HeightPct: want 0, got %d", miss.HeightPct)
	}
	if !strings.Contains(miss.Tooltip, "no snapshot") {
		t.Errorf("missing-day tooltip should say 'no snapshot', got %q", miss.Tooltip)
	}
}

func TestBuildBatteryChart_TooltipUsesEffectiveDateMMDD(t *testing.T) {
	start := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	end := start
	snaps := []telemetry.Snapshot{
		{BatteryLevelPct: 80, BatteryRangeKm: 300, CapturedAt: time.Date(2026, 8, 8, 3, 30, 0, 0, time.UTC), EffectiveDate: time.Date(2026, 8, 7, 3, 30, 0, 0, time.UTC)},
	}
	c := buildBatteryChart(historyTestCtx, snaps, start, end)
	if c.Empty || len(c.Bars) != 1 {
		t.Fatalf("want 1 bar, got %d (empty=%v)", len(c.Bars), c.Empty)
	}
	bar := c.Bars[0]
	if bar.Label != "08-07" {
		t.Errorf("want Label=08-07, got %q", bar.Label)
	}
	if !strings.Contains(bar.Tooltip, "08-07") {
		t.Errorf("tooltip must contain 08-07, got %q", bar.Tooltip)
	}
	if strings.Contains(bar.Tooltip, "2026-08-08") {
		t.Errorf("tooltip must NOT contain the CapturedAt morning, got %q", bar.Tooltip)
	}
}

// --- buildHistoryView (end-to-end handler logic + reader) ---

func TestBuildHistoryView_ReaderError_DegradesBothChartsEmpty(t *testing.T) {
	reader := &fakeHistoryReader{historyErr: errTestHistory}
	h := newHandlerForHistory(reader, 42, "VIN42")
	start := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	v := h.buildHistoryView(context.Background(), uuid.New(), 42, start, end, startOfDay(time.Now()))
	if !v.Odometer.Empty {
		t.Error("want Odometer.Empty on reader error")
	}
	if !v.Battery.Empty {
		t.Error("want Battery.Empty on reader error")
	}
	// Presets are still built so the selector is usable despite the read error.
	if len(v.Presets) == 0 {
		t.Error("want presets rendered even on reader error")
	}
}

func TestBuildHistoryView_PassesReadStartLookbackToEndToReader(t *testing.T) {
	reader := &fakeHistoryReader{historySnaps: []telemetry.Snapshot{}}
	h := newHandlerForHistory(reader, 42, "VIN42")
	start := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	_ = h.buildHistoryView(context.Background(), uuid.New(), 42, start, end, startOfDay(time.Now()))
	if !reader.betweenCalled {
		t.Fatal("want SnapshotsByVehicleBetween called")
	}
	// readStart = start - 1 day (the lookback).
	if !reader.gotStart.Equal(start.AddDate(0, 0, -1)) {
		t.Errorf("want gotStart=%v (lookback), got %v", start.AddDate(0, 0, -1), reader.gotStart)
	}
	if !reader.gotEnd.Equal(end) {
		t.Errorf("want gotEnd=%v, got %v", end, reader.gotEnd)
	}
}

// TestBuildHistoryView_BothChartsShareFixedAxis is the MAG-7 fix assertion
// (task 6.5): both charts have exactly numDays bars and Bars[i].Label matches
// per index — identical labels by construction. Also asserts the lookback was
// passed to Between.
func TestBuildHistoryView_BothChartsShareFixedAxis(t *testing.T) {
	start := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC) // 6-day inclusive window
	// Full coverage incl. 08-01 lookback.
	days := append([]time.Time{start.AddDate(0, 0, -1)}, calendarDays(start, end)...)
	// Drop 08-05 to force a missing-day bar on both charts.
	mid := start.AddDate(0, 0, 3) // 08-05
	gapped := make([]time.Time, 0, len(days))
	for _, d := range days {
		if d.Equal(mid) {
			continue
		}
		gapped = append(gapped, d)
	}
	snaps := snapsForDays(gapped, 1000, 10, 70)
	reader := &fakeHistoryReader{historySnaps: snaps}
	h := newHandlerForHistory(reader, 42, "VIN42")
	v := h.buildHistoryView(context.Background(), uuid.New(), 42, start, end, startOfDay(time.Now()))

	numDays := int(end.Sub(start).Hours()/24) + 1 // 6
	if len(v.Odometer.Bars) != numDays {
		t.Errorf("odometer bars: want %d, got %d", numDays, len(v.Odometer.Bars))
	}
	if len(v.Battery.Bars) != numDays {
		t.Errorf("battery bars: want %d, got %d", numDays, len(v.Battery.Bars))
	}
	for i := 0; i < numDays; i++ {
		if v.Odometer.Bars[i].Label != v.Battery.Bars[i].Label {
			t.Errorf("index %d: odometer Label %q != battery Label %q (MAG-7 offset not eliminated)",
				i, v.Odometer.Bars[i].Label, v.Battery.Bars[i].Label)
		}
	}
	// Lookback was passed to Between.
	if !reader.gotStart.Equal(start.AddDate(0, 0, -1)) {
		t.Errorf("lookback: want gotStart=%v, got %v", start.AddDate(0, 0, -1), reader.gotStart)
	}
}

func TestBuildHistoryView_PresetsCarryAbsoluteHrefs(t *testing.T) {
	reader := &fakeHistoryReader{historySnaps: []telemetry.Snapshot{}}
	h := newHandlerForHistory(reader, 42, "VIN42")
	end := startOfDay(time.Now()).AddDate(0, 0, -1)
	start := end.AddDate(0, 0, -historyRangeWindowDays)
	v := h.buildHistoryView(context.Background(), uuid.New(), 42, start, end, startOfDay(time.Now()))
	if len(v.Presets) != len(historyPresetDayCounts) {
		t.Fatalf("want %d presets, got %d", len(historyPresetDayCounts), len(v.Presets))
	}
	// The default-window request (yesterday-6 .. yesterday) activates 6-day preset.
	if !v.Presets[0].Active {
		t.Error("6-day preset should be Active for the default window (ending yesterday)")
	}
	for i, p := range v.Presets {
		if p.StartStr == "" || p.EndStr == "" {
			t.Errorf("preset %d: absolute href dates must be non-empty", i)
		}
	}
}

// --- HTTP-level handler tests (task 6.6) ---

func TestDashboardHistoryFragment_AnonymousRedirectsToLogin(t *testing.T) {
	reader := &fakeHistoryReader{}
	h := newHandlerForHistory(reader, 1, "VIN1")
	eng := historyEngine(h, uuid.Nil, 0, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/dashboard/history", nil)
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("want 302 for anonymous, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/login" {
		t.Errorf("want redirect to /login, got %q", loc)
	}
}

func TestDashboardHistoryFragment_DefaultWindowPassedToReader(t *testing.T) {
	uid := uuid.New()
	reader := &fakeHistoryReader{historySnaps: []telemetry.Snapshot{}}
	h := newHandlerForHistory(reader, 42, "VIN42")
	eng := historyEngine(h, uid, 42, "VIN42")
	c := sessionCookie(eng, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/dashboard/history", nil)
	if c != nil {
		req.AddCookie(c)
	}
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	// readStart = startOfDay(now) - 7 (lookback 1 + default 6); end = today.
	wantStart := startOfDay(time.Now()).AddDate(0, 0, -7)
	wantEnd := startOfDay(time.Now())
	if !reader.gotStart.Equal(wantStart) {
		t.Errorf("want gotStart=%v, got %v", wantStart, reader.gotStart)
	}
	if !reader.gotEnd.Equal(wantEnd) {
		t.Errorf("want gotEnd=%v, got %v", wantEnd, reader.gotEnd)
	}
}

// TestDashboardHistoryFragment_400_MalformedStart asserts each of the five 400
// cases (design D1): reader is NOT called and the empty-state placeholder body
// is rendered (no 500, no fabricated bars).
func TestDashboardHistoryFragment_400_Cases(t *testing.T) {
	uid := uuid.New()
	cases := []struct {
		name string
		url  string
	}{
		{"malformed start", "/ui/dashboard/history?start=08-07&end=2026-08-07"},
		{"missing partner", "/ui/dashboard/history?start=2026-08-03"},
		{"end before start", "/ui/dashboard/history?start=2026-08-07&end=2026-08-03"},
		{"end after today", "/ui/dashboard/history?start=2026-08-03&end=2099-12-31"},
		{"window over 90 days", "/ui/dashboard/history?start=2026-05-01&end=2026-08-07"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reader := &fakeHistoryReader{historySnaps: []telemetry.Snapshot{}}
			h := newHandlerForHistory(reader, 42, "VIN42")
			eng := historyEngine(h, uid, 42, "VIN42")
			c := sessionCookie(eng, uid, "")

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tc.url, nil)
			if c != nil {
				req.AddCookie(c)
			}
			eng.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("%s: want 400, got %d", tc.name, w.Code)
			}
			if reader.betweenCalled {
				t.Errorf("%s: reader must NOT be called on a rejected request", tc.name)
			}
			// historyEngine never wires handlers.LanguageMiddleware, so i18n.FromContext
			// falls back to Spanish (KeyHistoryAwaitingSnapshots's ES value) — assert the
			// resolved-language string, not the pre-existing English literal
			// (RM24-gateway-translate-all-pages, mirroring tier 2's T6.4 precedent).
			if !strings.Contains(w.Body.String(), "Esperando los datos nocturnos") {
				t.Errorf("%s: 400 body must contain the empty-state placeholder; got: %s", tc.name, w.Body.String())
			}
		})
	}
}

// TestDashboardHistoryFragment_DaysParamIsIgnored: a request with ?days=6 and
// no start/end falls back to the default window (the days param is REMOVED, not
// honored) — task 6.7.
func TestDashboardHistoryFragment_DaysParamIsIgnored(t *testing.T) {
	uid := uuid.New()
	reader := &fakeHistoryReader{historySnaps: []telemetry.Snapshot{}}
	h := newHandlerForHistory(reader, 42, "VIN42")
	eng := historyEngine(h, uid, 42, "VIN42")
	c := sessionCookie(eng, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/dashboard/history?days=6", nil)
	if c != nil {
		req.AddCookie(c)
	}
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	// Default window readStart (lookback 1 + default 6 = today-7).
	wantStart := startOfDay(time.Now()).AddDate(0, 0, -7)
	if !reader.gotStart.Equal(wantStart) {
		t.Errorf("days param must be ignored; want gotStart=%v, got %v", wantStart, reader.gotStart)
	}
}

func TestDashboardHistoryFragment_ReaderErrorDegradesBothEmpty(t *testing.T) {
	uid := uuid.New()
	reader := &fakeHistoryReader{historyErr: errTestHistory}
	h := newHandlerForHistory(reader, 42, "VIN42")
	eng := historyEngine(h, uid, 42, "VIN42")
	c := sessionCookie(eng, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/dashboard/history", nil)
	if c != nil {
		req.AddCookie(c)
	}
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 (graceful degrade) on reader error, got %d", w.Code)
	}
	// Resolved language is Spanish here (see the 400_Cases comment above).
	if !strings.Contains(w.Body.String(), "Esperando los datos nocturnos") {
		t.Errorf("want empty-state placeholder on reader error; body: %s", w.Body.String())
	}
}

func TestDashboardHistoryFragment_PresetsAreAbsoluteAndDefaultActive(t *testing.T) {
	uid := uuid.New()
	start := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	reader := &fakeHistoryReader{historySnaps: snapsForDays(calendarDays(start, end), 1000, 10, 70)}
	h := newHandlerForHistory(reader, 42, "VIN42")
	eng := historyEngine(h, uid, 42, "VIN42")
	c := sessionCookie(eng, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/dashboard/history?start=2026-08-03&end=2026-08-07", nil)
	if c != nil {
		req.AddCookie(c)
	}
	eng.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	body := w.Body.String()
	// No ?days= anywhere — the preset hrefs are all absolute ?start=&end=.
	if strings.Contains(body, "days=") {
		t.Errorf("body must not contain ?days= anywhere; got: %s", body)
	}
	// Each preset carries an absolute start=/end= href.
	for _, p := range historyPresetDayCounts {
		if !strings.Contains(body, "start=") || !strings.Contains(body, "end=") {
			t.Errorf("body must contain absolute start=/end= hrefs; got: %s", body)
		}
		_ = p
	}
	// Active preset is btn-primary (the requested 5-day window matches none of
	// 6/14/30, so a NON-preset window marks no preset active — all-ghost is the
	// honest state, which still renders ghost buttons but no btn-primary).
	// The default-window request (params absent) instead activates 6 days.
}

func TestDashboardHistoryFragment_DefaultWindowActivatesSixDayPreset(t *testing.T) {
	uid := uuid.New()
	reader := &fakeHistoryReader{historySnaps: []telemetry.Snapshot{}}
	h := newHandlerForHistory(reader, 42, "VIN42")
	eng := historyEngine(h, uid, 42, "VIN42")
	c := sessionCookie(eng, uid, "")

	// The dashboard UI sends explicit params from defaultHistoryHref(): the
	// default 6-day window ending YESTERDAY (today-1), because today's data
	// loads tomorrow. The API default (both-absent → end=today) is unchanged;
	// this test exercises the dashboard's actual self-load href.
	yesterday := startOfDay(time.Now()).AddDate(0, 0, -1)
	start6 := yesterday.AddDate(0, 0, -historyRangeWindowDays)
	href := fmt.Sprintf("/ui/dashboard/history?start=%s&end=%s",
		start6.Format("2006-01-02"), yesterday.Format("2006-01-02"))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, href, nil)
	if c != nil {
		req.AddCookie(c)
	}
	eng.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	body := w.Body.String()
	// Active preset button is btn-primary.
	if !strings.Contains(body, "btn-primary") {
		t.Error("default-window request must mark the 6-day preset with btn-primary")
	}
	// The 6-day preset's href is ?start=<yesterday-6>&end=<yesterday>.
	wantHref := fmt.Sprintf("start=%s&amp;end=%s",
		start6.Format("2006-01-02"), yesterday.Format("2006-01-02"))
	if !strings.Contains(body, wantHref) {
		t.Errorf("default 6-day preset href must contain %q; got: %s", wantHref, body)
	}
	// All three preset labels appear. Resolved language is Spanish here
	// (KeyHistoryDaysPreset's ES value "%d días") — mirrors tier 2's T6.4 precedent.
	for _, p := range historyPresetDayCounts {
		label := fmt.Sprintf("%d días", p)
		if !strings.Contains(body, label) {
			t.Errorf("selector must contain label %q", label)
		}
	}
}

// --- fragment structure / render tests (task 6.7, retargeted) ---

func TestDashboardHistoryFragment_ContainsSVGViewBoxAndTitleTooltips(t *testing.T) {
	uid := uuid.New()
	start := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	reader := &fakeHistoryReader{historySnaps: snapsForDays(append([]time.Time{start.AddDate(0, 0, -1)}, calendarDays(start, end)...), 1000, 10, 70)}
	h := newHandlerForHistory(reader, 42, "VIN42")
	eng := historyEngine(h, uid, 42, "VIN42")
	c := sessionCookie(eng, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/dashboard/history?start=2026-08-03&end=2026-08-07", nil)
	if c != nil {
		req.AddCookie(c)
	}
	eng.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "viewBox") {
		t.Error("fragment must contain responsive <svg viewBox …>")
	}
	if !strings.Contains(body, "<title>") {
		t.Error("fragment must contain <title> tooltip elements inside SVG bars")
	}
}

// TestDashboardHistoryFragment_LabelsRenderedAndVerticalOnlyForNarrowWindows
// (task 6.7, retargeted from the old ?days=N version): httptest with a fixture
// spanning 6/14/30-bar windows. Asserts each bar's MM-DD label appears verbatim
// and the vertical-label CSS class is present ONLY for >= 14 bars.
func TestDashboardHistoryFragment_LabelsRenderedAndVerticalOnlyForNarrowWindows(t *testing.T) {
	uid := uuid.New()
	end := startOfDay(time.Now())
	for _, numBars := range []int{6, 14, 30} {
		start := end.AddDate(0, 0, -(numBars - 1)) // inclusive end → numBars days
		// Full coverage incl. lookback.
		snaps := snapsForDays(append([]time.Time{start.AddDate(0, 0, -1)}, calendarDays(start, end)...), 1000, 10, 70)
		reader := &fakeHistoryReader{historySnaps: snaps}
		h := newHandlerForHistory(reader, 42, "VIN42")
		eng := historyEngine(h, uid, 42, "VIN42")
		c := sessionCookie(eng, uid, "")

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/ui/dashboard/history?start=%s&end=%s", start.Format("2006-01-02"), end.Format("2006-01-02")), nil)
		if c != nil {
			req.AddCookie(c)
		}
		eng.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("numBars=%d: want 200, got %d", numBars, w.Code)
		}
		body := w.Body.String()

		odo := buildOdometerChart(historyTestCtx, snaps, start, end)
		bat := buildBatteryChart(historyTestCtx, snaps, start, end)
		for _, bar := range odo.Bars {
			if !strings.Contains(body, bar.Label) {
				t.Errorf("numBars=%d: odometer label %q missing from body", numBars, bar.Label)
			}
		}
		for _, bar := range bat.Bars {
			if !strings.Contains(body, bar.Label) {
				t.Errorf("numBars=%d: battery label %q missing from body", numBars, bar.Label)
			}
		}

		hasVerticalClass := strings.Contains(body, "[writing-mode:vertical-rl]")
		wantVertical := numBars >= 14
		if hasVerticalClass != wantVertical {
			t.Errorf("numBars=%d: want vertical class present=%v, got %v", numBars, wantVertical, hasVerticalClass)
		}
	}
}

// TestDashboardHistoryFragment_LabelsMatchViewModelVerbatim_NoLongDateFormat
// (task 6.7): the logic-free-template invariant. The labels the handler
// pre-computed on the view model must appear verbatim in the rendered HTML,
// and the long-form Go date layout "2006-01-02" must never appear.
func TestDashboardHistoryFragment_LabelsMatchViewModelVerbatim_NoLongDateFormat(t *testing.T) {
	uid := uuid.New()
	end := startOfDay(time.Now())
	start := end.AddDate(0, 0, -13) // 14-day inclusive window ending today
	snaps := snapsForDays(append([]time.Time{start.AddDate(0, 0, -1)}, calendarDays(start, end)...), 1000, 10, 60)
	reader := &fakeHistoryReader{historySnaps: snaps}
	h := newHandlerForHistory(reader, 42, "VIN42")
	eng := historyEngine(h, uid, 42, "VIN42")
	c := sessionCookie(eng, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/ui/dashboard/history?start=%s&end=%s", start.Format("2006-01-02"), end.Format("2006-01-02")), nil)
	if c != nil {
		req.AddCookie(c)
	}
	eng.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	body := w.Body.String()

	v := h.buildHistoryView(context.Background(), uid, 42, start, end, startOfDay(time.Now()))
	if len(v.Odometer.Bars) == 0 || len(v.Battery.Bars) == 0 {
		t.Fatal("want non-empty odometer and battery bars for this fixture")
	}
	for _, bar := range v.Odometer.Bars {
		if !strings.Contains(body, bar.Label) {
			t.Errorf("odometer bar Label %q not found verbatim in body", bar.Label)
		}
	}
	for _, bar := range v.Battery.Bars {
		if !strings.Contains(body, bar.Label) {
			t.Errorf("battery bar Label %q not found verbatim in body", bar.Label)
		}
	}
	// The absolute preset hrefs are formatted "YYYY-MM-DD" (e.g. "2026-08-09")
	// — those do NOT contain the literal Go layout string "2006-01-02". A
	// literal "2006-01-02" in the body would indicate the template called
	// time.Format with the layout, which the logic-free invariant forbids.
	if strings.Contains(body, "2006-01-02") {
		t.Error("body must not contain the Go long-date layout \"2006-01-02\" — the template must never format dates itself")
	}
}

// --- dashboard page structural test (task 6.8) ---

func TestDashboard_HistoryRegionInsideDashboardContent(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 1, VIN: "VIN1", DisplayName: "Test"},
	}}
	reader := &fakeReader{snapshots: []telemetry.Snapshot{
		{TeslaID: 1, CapturedAt: time.Now().Add(-time.Hour), BatteryLevelPct: 80, OdometerKm: 1000},
	}}
	h := newHandlerWithReader(acct, fakeTesla{}, reader)
	eng := dashboardEngine(h, uid, 1, "VIN1", "")
	c := sessionCookie(eng, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/dashboard", nil)
	if c != nil {
		req.AddCookie(c)
	}
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 for dashboard fragment, got %d", w.Code)
	}
	body := w.Body.String()
	// #dashboard-history is present (the self-loading history region).
	if !strings.Contains(body, `id="dashboard-history"`) {
		t.Error("dashboard fragment must contain #dashboard-history region")
	}
	// It carries hx-trigger="load" so it self-fetches on render.
	if !strings.Contains(body, `hx-trigger="load"`) {
		t.Error("#dashboard-history must carry hx-trigger=\"load\" for self-fetch")
	}
	// The self-load hx-get is a server-rendered absolute ?start=&end= ending at
	// YESTERDAY (today-1) because today's data loads tomorrow — NOT ?days=6.
	if !strings.Contains(body, "start=") || !strings.Contains(body, "end=") {
		t.Errorf("dashboard history self-load must carry absolute start=/end=; got: %s", body)
	}
	if strings.Contains(body, "days=6") {
		t.Errorf("dashboard history self-load must NOT use ?days=6; got: %s", body)
	}
	yesterday := startOfDay(time.Now()).AddDate(0, 0, -1).Format("2006-01-02")
	if !strings.Contains(body, "end="+yesterday) {
		t.Errorf("dashboard history self-load end= must be yesterday (%s); got: %s", yesterday, body)
	}
	// The Refresh button is GONE (removed by RM8 design D5).
	if strings.Contains(body, ">Refresh<") {
		t.Errorf("dashboard page header must NOT contain a Refresh button; got: %s", body)
	}
}
