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
	"github.com/cristianpena/magus-tesla-api/internal/analytics"
	"github.com/cristianpena/magus-tesla-api/internal/clock"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/layouts"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// browserTodayNoCookie mirrors exactly what browserToday(c) computes for a
// request that carries NO browser_tz cookie: midnight in the platform's
// default zone. Since RM35-gateway-adopt-clock (roadmap D1) that fallback is
// clock.Zone() (America/Bogota), not time.UTC.
//
// Tests that compare against a window the HANDLER built must anchor here.
// startOfDay(time.Now()) is UTC midnight, a DIFFERENT INSTANT — five hours
// apart from Bogota midnight — and time.Time.Equal compares instants, not
// calendar dates. Anchoring on startOfDay is what made three tests in this
// file fail the moment the fallback moved.
//
// Tests that PASS their own "today" straight into buildHistoryView or
// dashboardFor are self-consistent and correctly keep using startOfDay: they
// never cross the browserLocation fallback at all.
func browserTodayNoCookie() time.Time {
	return startOfDayIn(time.Now(), clock.Zone())
}

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

// SnapshotsByVehicleUpdatedSince satisfies the telemetry.Reader method added by
// RM29-analytics-add-vehicle-metrics task 1.2. No gateway handler calls it -- it
// serves analytics' recompute watermark -- so a call here would mean a chart
// builder reached for the wrong port. Panic makes that visible.
func (f *fakeHistoryReader) SnapshotsByVehicleUpdatedSince(_ context.Context, _ uuid.UUID, _ int64, _ time.Time) ([]telemetry.Snapshot, error) {
	panic("fakeHistoryReader: SnapshotsByVehicleUpdatedSince must not be called by any gateway handler")
}

// SnapshotPrecedingDay satisfies the telemetry.Reader method added by
// RM29-telemetry-drop-derived-columns task 1.2. Same reasoning as the method above:
// it exists so analytics can fetch the exact predecessor of a day it is recomputing,
// no gateway handler calls it, and a call from here would mean a chart builder
// reached for the wrong port. Panic makes that visible.
func (f *fakeHistoryReader) SnapshotPrecedingDay(_ context.Context, _ uuid.UUID, _ int64, _ time.Time) (*telemetry.Snapshot, error) {
	panic("fakeHistoryReader: SnapshotPrecedingDay must not be called by any gateway handler")
}

// errTestHistory is a sentinel error for history handler tests.
var errTestHistory = errors.New("test history reader error")

// fakeAnalyticsReader is a test double for analytics.Reader, used by the
// buildConsumedChart/buildOdometerChart integration path (buildHistoryView)
// and the Deps forwarding test (design.md Test Contract (o)). ConsumedByDay
// and OdometerDeltaByDay each record their call args so tests can assert
// wiring — the same call-recording-fake shape fakeHistoryReader.betweenCalled
// already uses for TelemetryReader. RecentEfficiency PANICS: design.md's
// "cmd/web wiring" section states the gateway's history fragment never calls
// it (only RecentEfficiency reads analytics.DefaultWindow, and the gateway
// never calls that method) — mirrors fakeHistoryReader's
// SnapshotsByVehicleSince panic guard for an intentionally-unused method.
type fakeAnalyticsReader struct {
	days []analytics.DayConsumption
	err  error

	// snaps, distances, odometerErr back OdometerDeltaByDay
	// (RM29-analytics-add-vehicle-metrics task 5.1/4.3 — the odometer chart's
	// delta/clamp math moved out of the gateway into internal/analytics,
	// roadmap D5). When distances is explicitly set it wins outright (tests
	// that want to control the odometer port directly). Otherwise, when
	// snaps is non-nil, OdometerDeltaByDay DERIVES its return value via
	// distancesFromSnaps(snaps, start, end) using the ACTUAL (start, end)
	// args the call receives — this is how newHandlerForHistory wires the
	// SAME historySnaps fixture a test already builds for the battery chart
	// into the odometer chart's new port, so every pre-existing
	// snapshot-shaped fixture keeps driving both charts with the identical
	// numbers the pre-move gateway code produced, with no test-body changes
	// (characterization, roadmap D10). See distancesFromSnaps's own doc
	// comment for what it does and does not reproduce.
	snaps       []telemetry.Snapshot
	distances   []analytics.DayDistance
	odometerErr error

	gotAccount          uuid.UUID
	gotTeslaID          int64
	gotStart            time.Time
	gotEnd              time.Time
	consumedByDayCalled bool

	gotOdoAccount       uuid.UUID
	gotOdoTeslaID       int64
	gotOdoStart         time.Time
	gotOdoEnd           time.Time
	odometerByDayCalled bool
}

func (f *fakeAnalyticsReader) RecentEfficiency(context.Context, uuid.UUID, int64) (analytics.Efficiency, bool, error) {
	panic("fakeAnalyticsReader: RecentEfficiency is never called by the gateway's history fragment")
}

func (f *fakeAnalyticsReader) ConsumedByDay(_ context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) ([]analytics.DayConsumption, error) {
	f.gotAccount = accountID
	f.gotTeslaID = teslaID
	f.gotStart = start
	f.gotEnd = end
	f.consumedByDayCalled = true
	return f.days, f.err
}

func (f *fakeAnalyticsReader) OdometerDeltaByDay(_ context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) ([]analytics.DayDistance, error) {
	f.gotOdoAccount = accountID
	f.gotOdoTeslaID = teslaID
	f.gotOdoStart = start
	f.gotOdoEnd = end
	f.odometerByDayCalled = true
	if f.odometerErr != nil {
		return nil, f.odometerErr
	}
	if f.distances != nil {
		return f.distances, nil
	}
	if f.snaps != nil {
		return distancesFromSnaps(f.snaps, start, end), nil
	}
	return nil, nil
}

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
//
// Wires a default fakeAnalyticsReader so every pre-existing
// buildHistoryView/DashboardHistoryFragment test that does not care about the
// consumed/odometer charts keeps working: buildHistoryView (RM28 tier 4,
// D-G10; extended to the odometer chart by RM29-analytics-add-vehicle-metrics
// task 5.1) unconditionally calls h.analyticsReader.ConsumedByDay AND
// h.analyticsReader.OdometerDeltaByDay, and a nil analytics.Reader interface
// value would panic on either call — every caller of this helper needs a
// non-nil reader. The default reader's snaps field is wired to
// reader.historySnaps so OdometerDeltaByDay derives its answer from the SAME
// snapshot fixture the test already built for the battery chart
// (distancesFromSnaps — characterization, roadmap D10), with zero additional
// setup for tests that only care about the battery/odometer charts sharing
// one snapshot-shaped fixture. Tests that need to control or observe
// AnalyticsReader directly use newHandlerForHistoryWithAnalytics instead.
func newHandlerForHistory(reader *fakeHistoryReader, teslaID int64, vin string) *Handler {
	return newHandlerForHistoryWithAnalytics(reader, &fakeAnalyticsReader{snaps: reader.historySnaps}, teslaID, vin)
}

// newHandlerForHistoryWithAnalytics mirrors newHandlerForHistory but wires an
// explicit fake analytics.Reader instead of the default empty one, for tests
// that need to control (fixture days/err) or observe (call-recording) the
// consumed-chart port — e.g. the Deps-forwarding test (design.md Test
// Contract (o)).
func newHandlerForHistoryWithAnalytics(historyReader *fakeHistoryReader, analyticsReader analytics.Reader, teslaID int64, vin string) *Handler {
	acct := &fakeAccount{
		registered: []account.Vehicle{
			{TeslaID: teslaID, VIN: vin, DisplayName: "Test Vehicle"},
		},
	}
	return New(Deps{
		Account:         acct,
		Tesla:           &fakeTesla{},
		TelemetryReader: historyReader,
		AnalyticsReader: analyticsReader,
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

// distancesFromSnaps computes the []analytics.DayDistance a real
// analytics.Reader.OdometerDeltaByDay call would have returned for a SIMPLE
// (no multi-day-gap) snapshot fixture, over the FIXED [start, end] window —
// this is exactly the delta+clamp+bucket algorithm the OLD buildOdometerChart
// itself used to run in-process, before roadmap D5 moved it into
// internal/analytics (design.md D6). Kept in THIS TEST FILE ONLY: it is the
// characterization bridge that lets pre-existing snapshot-shaped fixtures
// (snapsForDays, etc.) keep driving both the battery chart (still fed raw
// telemetry.Snapshot) and the odometer chart (now fed the equivalent
// []analytics.DayDistance) with the IDENTICAL numbers the pre-move gateway
// code produced (roadmap D10's characterization bar) — production code no
// longer contains this delta/clamp logic anywhere.
//
// Sparse, matching analytics.Reader's documented contract: a day whose
// immediate calendar predecessor has no bucketed snapshot gets NO entry
// (never a zero-value placeholder), mirroring design.md D13/Fixture C's
// "no computable predecessor -> excluded" rule for the single-gap fixtures
// this test file uses. It does NOT reproduce deriveVehicleMetrics' multi-day-
// span widening (design.md D8) — no gateway test needs to exercise that;
// internal/analytics' own test suite (Wave 4/6 of this change) covers it.
func distancesFromSnaps(snaps []telemetry.Snapshot, start, end time.Time) []analytics.DayDistance {
	byDay := make(map[time.Time]telemetry.Snapshot, len(snaps))
	for _, s := range snaps {
		byDay[effectiveDayUTC(s.EffectiveDate)] = s
	}
	var out []analytics.DayDistance
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		cur, curOK := byDay[d]
		prev, prevOK := byDay[d.AddDate(0, 0, -1)]
		if !curOK || !prevOK {
			continue
		}
		km := cur.OdometerKm - prev.OdometerKm
		if km < 0 {
			km = 0
		}
		out = append(out, analytics.DayDistance{Date: d, KmDriven: km, OdometerKm: cur.OdometerKm})
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

// TestParseHistoryRange_BothAbsent_DefaultSixDayWindow asserts the D11
// default window: end = browser-YESTERDAY (today.AddDate(0,0,-1)), not today
// — the nightly batch captures today's data tomorrow, so an end=today window
// always had an empty last bar (roadmap D11, design.md D-G9, Test Contract
// (j)). Was: end == today.
//
// parseRange("", "") carries no browser_tz cookie, so "today" resolves via
// browserToday's clock.Zone() fallback (America/Bogota) — was UTC before
// RM35-gateway-adopt-clock; wantEnd is recomputed the same way the handler
// derives it (RM35-gateway-adopt-clock, design.md D-gw-2/D-gw-4).
func TestParseHistoryRange_BothAbsent_DefaultSixDayWindow(t *testing.T) {
	start, end, ok := parseRange("", "")
	if !ok {
		t.Fatal("want ok=true for both absent")
	}
	wantEnd := startOfDayIn(time.Now(), clock.Zone()).AddDate(0, 0, -1)
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

// TestBrowserLocation_Fallbacks asserts browserLocationFromHeader's fallback
// (missing / malformed / empty cookie) is the platform default, clock.Zone()
// (America/Bogota) — was time.UTC before RM35-gateway-adopt-clock (roadmap
// D1/D4). A valid cookie still wins unconditionally, unaffected by this tier.
func TestBrowserLocation_Fallbacks(t *testing.T) {
	// No cookie -> platform default (clock.Zone()).
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if loc := browserLocationFromHeader(r); loc != clock.Zone() {
		t.Errorf("missing cookie: want clock.Zone(), got %v", loc)
	}
	// Valid IANA name -> that location.
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "browser_tz", Value: "America/Bogota"})
	if loc := browserLocationFromHeader(r); loc.String() != "America/Bogota" {
		t.Errorf("valid cookie: want America/Bogota, got %v", loc)
	}
	// Malformed / not-in-IANA name -> platform default (clock.Zone()).
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "browser_tz", Value: "Not/A/Zone"})
	if loc := browserLocationFromHeader(r); loc != clock.Zone() {
		t.Errorf("malformed cookie: want clock.Zone(), got %v", loc)
	}
	// Empty value -> platform default (clock.Zone()).
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "browser_tz", Value: ""})
	if loc := browserLocationFromHeader(r); loc != clock.Zone() {
		t.Errorf("empty cookie: want clock.Zone(), got %v", loc)
	}
}

// TestParseHistoryRange_DefaultUsesBrowserYesterday asserts that with a
// browser_tz cookie, the default-absent window's end == browser-YESTERDAY
// (in the browser's timezone), not browser-today — the D11 shift (roadmap
// D11, design.md D-G9): the both-absent default now ends at yesterday,
// matching the preset windows (buildHistoryPresets already used yesterday).
// This proves parseHistoryRange consulted the cookie AND applied the D11
// yesterday shift, not just the cookie. Renamed from
// TestParseHistoryRange_DefaultUsesBrowserToday, which asserted the
// pre-D11 contract (end == browser-today) — not caught by tasks.md's T8.10–
// T8.13 enumeration; found by the owner's `make test` run mid-wave (see
// tasks.md T8.15). The Location-preservation assertion below is orthogonal
// to D11 and is kept unchanged.
func TestParseHistoryRange_DefaultUsesBrowserYesterday(t *testing.T) {
	_, end, ok := parseRangeWithTZ("", "", "America/Bogota")
	if !ok {
		t.Fatal("want ok=true for both absent with a TZ cookie")
	}
	// Compute the expected browser-yesterday the same way the helper does —
	// in the test this is a deterministic mirror of what parseHistoryRange did.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.AddCookie(&http.Cookie{Name: "browser_tz", Value: "America/Bogota"})
	wantEnd := browserToday(c).AddDate(0, 0, -1)
	if !end.Equal(wantEnd) {
		t.Errorf("default end: want browser-yesterday %v (loc %s), got %v (loc %s)",
			wantEnd, wantEnd.Location(), end, end.Location())
	}
	if end.Location().String() != "America/Bogota" {
		t.Errorf("default end Location: want America/Bogota, got %v", end.Location())
	}
}

// TestParseHistoryRange_EndCapUsesBrowserYesterday asserts the end<=yesterday
// cap (D11) honors the browser's yesterday, not today — the accepted
// boundary moved from end=today to end=yesterday (roadmap D11, design.md
// D-G9: the nightly batch captures today's data tomorrow, so an end=today
// window's last bar was always empty). Renamed from
// TestParseHistoryRange_EndCapUsesBrowserToday; Test Contract (k)/(l).
// end=browser-today is now the REJECTED case (was accepted pre-D11);
// end=browser-yesterday is the newly-accepted boundary. To keep each
// assertion isolated to the cap check alone (not the end>=start check),
// start==end in both cases.
func TestParseHistoryRange_EndCapUsesBrowserYesterday(t *testing.T) {
	// Build the same context the handler will see so the test's "browserToday"
	// is the exact same instant the handler computes.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.AddCookie(&http.Cookie{Name: "browser_tz", Value: "America/Bogota"})
	today := browserToday(c)
	todayStr := today.Format("2006-01-02")
	yesterdayStr := today.AddDate(0, 0, -1).Format("2006-01-02")

	// end = browser-today → now REJECTED post-D11 (Test Contract (k)).
	if _, _, ok := parseRangeWithTZ(todayStr, todayStr, "America/Bogota"); ok {
		t.Errorf("end=today (browser): want ok=false post-D11, got true")
	}
	// end = browser-yesterday → accepted (Test Contract (l)).
	if _, _, ok := parseRangeWithTZ(yesterdayStr, yesterdayStr, "America/Bogota"); !ok {
		t.Errorf("end=yesterday (browser): want ok=true, got false")
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
// pre-fix for America/Bogota.
//
// Updated for D11 (design.md D-G9): the accepted boundary moved from
// end=today to end=yesterday, so this now asserts THREE directions per zone:
// end=yesterday is accepted (the new boundary), end=today is rejected (was
// accepted pre-D11), end=tomorrow stays rejected (future in the browser's
// frame) — preserving the original positive/negative-offset regression
// coverage under the shifted boundary.
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
			yesterdayStr := today.AddDate(0, 0, -1).Format("2006-01-02")
			tomorrowStr := today.AddDate(0, 0, 1).Format("2006-01-02")

			// end = browser-local yesterday → accepted (D11's new boundary).
			if _, _, ok := parseRangeWithTZ(yesterdayStr, yesterdayStr, tz); !ok {
				t.Errorf("%s: end=browser-local-yesterday: want ok=true, got false", tz)
			}
			// end = browser-local today → now REJECTED post-D11 (was accepted pre-D11).
			if _, _, ok := parseRangeWithTZ(todayStr, todayStr, tz); ok {
				t.Errorf("%s: end=browser-local-today: want ok=false post-D11, got true", tz)
			}
			// end = browser-local tomorrow → rejected (future in the browser's frame).
			if _, _, ok := parseRangeWithTZ(todayStr, tomorrowStr, tz); ok {
				t.Errorf("%s: end=browser-local-tomorrow: want ok=false (future in browser TZ), got true", tz)
			}
		})
	}
}

// TestParseHistoryRange_NoCookieFallsBackToPlatformDefault asserts the
// platform-default fallback: with no browser_tz cookie, the default end ==
// platform-default-zone-yesterday (America/Bogota, via clock.Zone()) — was
// UTC-yesterday before RM35-gateway-adopt-clock (roadmap D1/D4). Renamed from
// TestParseHistoryRange_NoCookieFallsBackToUTC, repaired per roadmap D6.
func TestParseHistoryRange_NoCookieFallsBackToPlatformDefault(t *testing.T) {
	_, end, ok := parseRangeWithTZ("", "", "")
	if !ok {
		t.Fatal("want ok=true for both absent, no TZ cookie")
	}
	wantEnd := startOfDayIn(time.Now(), clock.Zone()).AddDate(0, 0, -1)
	if !end.Equal(wantEnd) {
		t.Errorf("no cookie: want platform-default end %v, got %v", wantEnd, end)
	}
	if end.Location() != clock.Zone() {
		t.Errorf("no cookie: want clock.Zone() location, got %v", end.Location())
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

// --- buildOdometerChart fixed-axis unit tests (task 6.3, design D3;
// rewritten as characterization tests by RM29-analytics-add-vehicle-metrics
// task 4.3/5.1, roadmap D5/D10 — buildOdometerChart now takes
// []analytics.DayDistance, already delta-computed and clamped, instead of
// raw []telemetry.Snapshot. Every fixture below was converted from its
// pre-move []telemetry.Snapshot shape via distancesFromSnaps, which
// reproduces the exact delta+clamp+bucket algorithm this function itself
// used to run — so every expected value (HeightPct/Label/Tooltip/Present)
// below is UNCHANGED from before this move, proving the rendering is
// behaviorally identical.) ---

func TestBuildOdometerChart_EmptyWhenNoDistances(t *testing.T) {
	start := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	// nil -- analytics.Reader.OdometerDeltaByDay's SPARSE "nothing computable
	// in this window" result (roadmap D5, design.md D6/D13). Replaces the
	// pre-move "< 2 snapshots" condition, which only existed because this
	// function used to fetch raw snapshot pairs itself; the two conditions
	// are equivalent in effect (a day only ever appeared in the old delta
	// set when both it and its predecessor were present).
	c := buildOdometerChart(historyTestCtx, nil, start, end)
	if !c.Empty {
		t.Error("want Empty=true for nil distances")
	}
	// Empty (non-nil) slice behaves identically.
	c = buildOdometerChart(historyTestCtx, []analytics.DayDistance{}, start, end)
	if !c.Empty {
		t.Error("want Empty=true for empty distances slice")
	}
}

func TestBuildOdometerChart_FixedAxis_FullWindowWithLookback(t *testing.T) {
	start := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC) // 5-day inclusive window
	lookback := start.AddDate(0, 0, -1)                // 08-02 seeds the first delta
	snaps := snapsForDays(append([]time.Time{lookback}, calendarDays(start, end)...), 1000, 10, 70)
	distances := distancesFromSnaps(snaps, start, end)
	c := buildOdometerChart(historyTestCtx, distances, start, end)
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
	distances := distancesFromSnaps(snaps, start, end)
	c := buildOdometerChart(historyTestCtx, distances, start, end)
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
	// 08-06 is ALSO absent (index 3): its own immediate predecessor, 08-05,
	// has no snapshot either, so distancesFromSnaps produces no entry for it
	// (matching the pre-move algorithm exactly — 08-06's delta was never
	// computable against a 2-day-old snapshot). Left unasserted by the
	// original pre-move test; asserted explicitly here since it is real,
	// characterization-identical behavior.
	if c.Bars[3].Present {
		t.Error("08-06 must ALSO be Present=false — its own immediate predecessor (08-05) has no entry")
	}
	// Surrounding bars (08-04, 08-06) keep their own labels — no shift to fill.
	if c.Bars[1].Label != "08-04" || c.Bars[3].Label != "08-06" {
		t.Errorf("surrounding labels must not shift; got %q, %q", c.Bars[1].Label, c.Bars[3].Label)
	}
}

// TestBuildOdometerChart_ZeroKmDistance_StaysPresentAtZeroHeight is the
// post-move successor to the pre-move
// TestBuildOdometerChart_NegativeDeltaClampedToZero: the negative-delta clamp
// itself no longer lives in this function (roadmap D5) — it is applied
// upstream, inside internal/analytics (design.md D13), before
// buildOdometerChart ever sees the value. This test feeds a fixture whose
// KmDriven is ALREADY 0.0 (as if analytics had just clamped a raw -100.0)
// and asserts the SAME rendered output the pre-move code produced for its
// equivalent negative-delta fixture: a zero-height bar that is still
// Present=true (backed by a real day, unlike a missing-predecessor day).
func TestBuildOdometerChart_ZeroKmDistance_StaysPresentAtZeroHeight(t *testing.T) {
	start := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC) // 2-day window
	distances := []analytics.DayDistance{
		{Date: start, KmDriven: 0.0, OdometerKm: 900.0},                     // 08-03 — already clamped upstream (was -100.0)
		{Date: start.AddDate(0, 0, 1), KmDriven: 150.0, OdometerKm: 1050.0}, // 08-04
	}
	c := buildOdometerChart(historyTestCtx, distances, start, end)
	if c.Empty {
		t.Fatal("want non-empty for 2 distances")
	}
	if len(c.Bars) != 2 {
		t.Fatalf("want 2 bars, got %d", len(c.Bars))
	}
	if c.Bars[0].HeightPct != 0 {
		t.Errorf("zero-km bar HeightPct: want 0, got %d", c.Bars[0].HeightPct)
	}
	if !c.Bars[0].Present {
		t.Error("zero-km bar is still a PRESENT bar (backed by a real day, unlike a missing-predecessor day)")
	}
}

// TestBuildOdometerChart_BucketsOnDateVerbatim_NoEffectiveDayUTC replaces the
// pre-move TestBuildOdometerChart_TooltipUsesEffectiveDateMMDD, whose whole
// point (proving EffectiveDate, not CapturedAt, drives the bucket) no longer
// applies: analytics.DayDistance carries no CapturedAt field at all, so
// there is nothing left to disambiguate against -- Date is now the only
// day-shaped field on the port's own result. Asserts buildOdometerChart
// buckets each entry on its own Date field DIRECTLY (design.md D6, spec.md
// "The odometer chart bucket day is the port's own Date, never re-derived")
// -- never re-derived through effectiveDayUTC or any other re-bucketing
// step. Mirrors TestBuildConsumedChart_BucketsOnDateVerbatim_NoEffectiveDayUTC.
func TestBuildOdometerChart_BucketsOnDateVerbatim_NoEffectiveDayUTC(t *testing.T) {
	d := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	distances := []analytics.DayDistance{{Date: d, KmDriven: 12.5, OdometerKm: 1200.0}}
	c := buildOdometerChart(historyTestCtx, distances, d, d)
	if c.Empty || len(c.Bars) != 1 {
		t.Fatalf("want 1 bar, got %d (empty=%v)", len(c.Bars), c.Empty)
	}
	bar := c.Bars[0]
	if bar.Label != "08-10" {
		t.Errorf("Label: want 08-10, got %q", bar.Label)
	}
	if !bar.Present {
		t.Error("want Present=true")
	}
	if !strings.Contains(bar.Tooltip, "08-10") {
		t.Errorf("tooltip must contain 08-10, got %q", bar.Tooltip)
	}
}

// TestBuildOdometerChart_FixtureA_MatchesDesignTestContract pins design.md's
// Test Contract Fixture A: OdometerDeltaByDay(A,42,2026-08-10,2026-08-10) ==
// []DayDistance{{Date: 2026-08-10, KmDriven: 50.0, OdometerKm: 1050.0}} (the
// clamp is a no-op here, 50.0 >= 0) -- and asserts buildOdometerChart renders
// that fixture with the same HeightPct/Label/Tooltip shape the pre-move
// gateway code rendered for the equivalent live-computed delta (roadmap D10
// characterization; task 4.3's explicit "author against Fixture A/B"
// instruction).
func TestBuildOdometerChart_FixtureA_MatchesDesignTestContract(t *testing.T) {
	d := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	distances := []analytics.DayDistance{{Date: d, KmDriven: 50.0, OdometerKm: 1050.0}}
	c := buildOdometerChart(historyTestCtx, distances, d, d)
	if c.Empty || len(c.Bars) != 1 {
		t.Fatalf("want 1 bar, got %d (empty=%v)", len(c.Bars), c.Empty)
	}
	bar := c.Bars[0]
	if bar.HeightPct != 100 {
		t.Errorf("HeightPct: want 100 (single bar is always the window max), got %d", bar.HeightPct)
	}
	if !bar.Present {
		t.Error("want Present=true")
	}
	if bar.Label != "08-10" {
		t.Errorf("Label: want 08-10, got %q", bar.Label)
	}
	// Pin the EXACT tooltip, not substrings. formatKm has always grouped
	// thousands via commaGroup (format.go, untouched by this change), so the
	// odometer renders "1,050" -- a substring check for "1050" asserts a format
	// the gateway has never produced. Exact-matching is the stronger
	// characterization anyway: it catches spacing, separator and unit drift too.
	const wantTooltip = "08-10 · 50 km driven · odometer 1,050 km"
	if bar.Tooltip != wantTooltip {
		t.Errorf("tooltip: want %q, got %q", wantTooltip, bar.Tooltip)
	}
}

// TestBuildOdometerChart_FixtureB_ClampAppliedUpstreamRendersZero pins
// design.md's Test Contract Fixture B: the STORED distance_traveled_km_calc
// is -2.0 (raw, unclamped -- analytics never mutates the stored column), but
// OdometerDeltaByDay's read-time clamp (design.md D13) already reports
// KmDriven: 0.0 by the time the gateway ever sees it -- buildOdometerChart
// itself performs NO comparison against zero any more (roadmap D5): it
// renders whatever KmDriven it was given, and 0.0 renders as a zero-height,
// still-PRESENT bar (Fixture B's day has a real snapshot pair, it just
// travelled backward -- unlike a missing-predecessor day, which is absent
// from the slice entirely).
func TestBuildOdometerChart_FixtureB_ClampAppliedUpstreamRendersZero(t *testing.T) {
	d := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	distances := []analytics.DayDistance{{Date: d, KmDriven: 0.0, OdometerKm: 1998.0}}
	c := buildOdometerChart(historyTestCtx, distances, d, d)
	if c.Empty || len(c.Bars) != 1 {
		t.Fatalf("want 1 bar, got %d (empty=%v)", len(c.Bars), c.Empty)
	}
	bar := c.Bars[0]
	if bar.HeightPct != 0 {
		t.Errorf("HeightPct: want 0, got %d", bar.HeightPct)
	}
	if !bar.Present {
		t.Error("want Present=true -- a clamped-to-zero day still has a real snapshot pair behind it")
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

// --- buildConsumedChart unit tests (RM28-gateway-add-consumed-graph tier 4,
// design.md Test Contract, roadmap D10/D19/D20/D21) ---
//
// Assertions below pin design.md's Test Contract, authored BEFORE this
// tier's implementation — where an assertion derived from the implementation
// would disagree with the Test Contract, the Test Contract wins (see the
// dispatch's binding rule). historyTestCtx (English) resolves the i18n
// clauses so tooltip substring assertions check the resolved EN string, not
// the catalogue key.

// TestBuildConsumedChart_NormalDay_RelativeScale — Test Contract (a): two
// normal (non-flagged, non-span) days scale relative to the window's max
// displayed value (D19), not an absolute 0-100 axis.
func TestBuildConsumedChart_NormalDay_RelativeScale(t *testing.T) {
	start := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)
	days := []analytics.DayConsumption{
		{Date: start, ConsumedPct: 11.0},
		{Date: end, ConsumedPct: 20.0},
	}
	c := buildConsumedChart(historyTestCtx, days, start, end)
	if c.Empty {
		t.Fatal("want non-empty chart")
	}
	if len(c.Bars) != 2 {
		t.Fatalf("want 2 bars, got %d", len(c.Bars))
	}
	bar0 := c.Bars[0]
	if !bar0.Present || bar0.MarkerFlagged || bar0.MarkerSpan {
		t.Errorf("bar[0]: want Present=true, MarkerFlagged=false, MarkerSpan=false, got %+v", bar0)
	}
	if bar0.HeightPct != 55 { // round(11/20*100)
		t.Errorf("bar[0].HeightPct: want 55, got %d", bar0.HeightPct)
	}
	wantTooltip := "08-10 · 11.0% consumed"
	if bar0.Tooltip != wantTooltip {
		t.Errorf("bar[0].Tooltip: want %q, got %q", wantTooltip, bar0.Tooltip)
	}
	if c.Bars[1].HeightPct != 100 {
		t.Errorf("bar[1].HeightPct: want 100 (the window max), got %d", c.Bars[1].HeightPct)
	}
}

// TestBuildConsumedChart_FlaggedNonSpan_Manual_HidesValue — Test Contract
// (b): a flagged, non-span MANUAL day renders HeightPct=0 and a tooltip that
// hides the raw value entirely (D10) — must NOT contain "-5" anywhere.
func TestBuildConsumedChart_FlaggedNonSpan_Manual_HidesValue(t *testing.T) {
	d := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)
	days := []analytics.DayConsumption{
		{Date: d, ConsumedPct: -5.0, Flagged: true, MissingChargingType: analytics.MissingChargingTypeManual, DaysSpanned: 1},
	}
	c := buildConsumedChart(historyTestCtx, days, d, d)
	if c.Empty || len(c.Bars) != 1 {
		t.Fatalf("want 1 bar, got %d (empty=%v)", len(c.Bars), c.Empty)
	}
	bar := c.Bars[0]
	if !bar.Present || !bar.MarkerFlagged || bar.MarkerSpan {
		t.Errorf("want Present=true, MarkerFlagged=true, MarkerSpan=false, got %+v", bar)
	}
	if bar.HeightPct != 0 {
		t.Errorf("HeightPct: want 0, got %d", bar.HeightPct)
	}
	wantTooltip := "08-11 · possible missing charge record (manual)"
	if bar.Tooltip != wantTooltip {
		t.Errorf("Tooltip: want %q, got %q", wantTooltip, bar.Tooltip)
	}
	if strings.Contains(bar.Tooltip, "-5") {
		t.Errorf("Tooltip must NOT contain the raw value \"-5\" anywhere (D10): got %q", bar.Tooltip)
	}
}

// TestBuildConsumedChart_FlaggedNonSpan_Supercharger_ZeroWithDistance — Test
// Contract (c): the zero-with-distance flagged case, SUPERCHARGER source.
func TestBuildConsumedChart_FlaggedNonSpan_Supercharger_ZeroWithDistance(t *testing.T) {
	d := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	days := []analytics.DayConsumption{
		{Date: d, ConsumedPct: 0, DistanceKm: 42, Flagged: true, MissingChargingType: analytics.MissingChargingTypeSupercharger, DaysSpanned: 1},
	}
	c := buildConsumedChart(historyTestCtx, days, d, d)
	if c.Empty || len(c.Bars) != 1 {
		t.Fatalf("want 1 bar, got %d (empty=%v)", len(c.Bars), c.Empty)
	}
	bar := c.Bars[0]
	if bar.HeightPct != 0 {
		t.Errorf("HeightPct: want 0, got %d", bar.HeightPct)
	}
	wantTooltip := "08-12 · possible missing charge record (Supercharger)"
	if bar.Tooltip != wantTooltip {
		t.Errorf("Tooltip: want %q, got %q", wantTooltip, bar.Tooltip)
	}
}

// TestBuildConsumedChart_MultiDaySpan_NotFlagged_ShowsRealValue — Test
// Contract (d): a multi-day span, not flagged, shows its REAL value (D20) —
// never a zero-height bar.
func TestBuildConsumedChart_MultiDaySpan_NotFlagged_ShowsRealValue(t *testing.T) {
	d1 := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	days := []analytics.DayConsumption{
		{Date: d1, ConsumedPct: 15.0, Flagged: false, DaysSpanned: 3},
		{Date: d2, ConsumedPct: 20.0, Flagged: false, DaysSpanned: 1}, // sets the window max
	}
	c := buildConsumedChart(historyTestCtx, days, d1, d2)
	if c.Empty || len(c.Bars) != 2 {
		t.Fatalf("want 2 bars, got %d (empty=%v)", len(c.Bars), c.Empty)
	}
	bar := c.Bars[0]
	if !bar.Present || bar.MarkerFlagged || !bar.MarkerSpan {
		t.Errorf("want Present=true, MarkerFlagged=false, MarkerSpan=true, got %+v", bar)
	}
	if bar.HeightPct != 75 { // round(15/20*100)
		t.Errorf("HeightPct: want 75, got %d", bar.HeightPct)
	}
	wantTooltip := "08-13 · 15.0% · covers 3 days"
	if bar.Tooltip != wantTooltip {
		t.Errorf("Tooltip: want %q, got %q", wantTooltip, bar.Tooltip)
	}
}

// TestBuildConsumedChart_MultiDaySpanAndFlagged_BothMarkers_ValueShown —
// Test Contract (e), roadmap D21: a day that is BOTH flagged AND a
// multi-day span carries BOTH markers (never one-or-the-other), and the
// tooltip states BOTH facts, value included (D20's value clause is never
// suppressed by a concurrent flag — D-G4's table row 4).
func TestBuildConsumedChart_MultiDaySpanAndFlagged_BothMarkers_ValueShown(t *testing.T) {
	d := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	days := []analytics.DayConsumption{
		{Date: d, ConsumedPct: -3.0, Flagged: true, MissingChargingType: analytics.MissingChargingTypeManual, DaysSpanned: 2},
	}
	c := buildConsumedChart(historyTestCtx, days, d, d)
	if c.Empty || len(c.Bars) != 1 {
		t.Fatalf("want 1 bar, got %d (empty=%v)", len(c.Bars), c.Empty)
	}
	bar := c.Bars[0]
	if !bar.MarkerFlagged {
		t.Error("want MarkerFlagged=true")
	}
	if !bar.MarkerSpan {
		t.Error("want MarkerSpan=true (D21: BOTH markers, not one-or-the-other)")
	}
	if bar.HeightPct != 0 {
		t.Errorf("HeightPct: want 0, got %d", bar.HeightPct)
	}
	wantTooltip := "08-14 · -3.0% · covers 2 days · possible missing charge record (manual)"
	if bar.Tooltip != wantTooltip {
		t.Errorf("Tooltip: want %q, got %q", wantTooltip, bar.Tooltip)
	}
	if !strings.Contains(bar.Tooltip, "-3.0") {
		t.Errorf("Tooltip must contain the real signed value \"-3.0\" (D20 is never suppressed by MarkerFlagged): got %q", bar.Tooltip)
	}
}

// TestBuildConsumedChart_MultiDaySpanAndFlagged_HeightClampIsolatedFromMax —
// Test Contract (f): a two-entry fixture that isolates "HeightPct=0 because
// the value clamps to 0" from "HeightPct=0 because the window max is 0" —
// (e) above cannot distinguish the two causes because its only entry IS the
// max; this fixture adds a second, normal entry that sets a non-zero max.
func TestBuildConsumedChart_MultiDaySpanAndFlagged_HeightClampIsolatedFromMax(t *testing.T) {
	d1 := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	days := []analytics.DayConsumption{
		{Date: d1, ConsumedPct: -3.0, Flagged: true, MissingChargingType: analytics.MissingChargingTypeManual, DaysSpanned: 2},
		{Date: d2, ConsumedPct: 10.0, Flagged: false, DaysSpanned: 1},
	}
	c := buildConsumedChart(historyTestCtx, days, d1, d2)
	if c.Empty || len(c.Bars) != 2 {
		t.Fatalf("want 2 bars, got %d (empty=%v)", len(c.Bars), c.Empty)
	}
	bar0, bar1 := c.Bars[0], c.Bars[1]
	if !bar0.MarkerFlagged || !bar0.MarkerSpan {
		t.Errorf("bar[0]: want MarkerFlagged=true, MarkerSpan=true, got %+v", bar0)
	}
	if bar0.HeightPct != 0 {
		t.Errorf("bar[0].HeightPct: want 0 (clamped to the axis floor; max=10 here, distinct from max=0), got %d", bar0.HeightPct)
	}
	if bar1.MarkerFlagged || bar1.MarkerSpan {
		t.Errorf("bar[1]: want no markers, got %+v", bar1)
	}
	if bar1.HeightPct != 100 {
		t.Errorf("bar[1].HeightPct: want 100 (the window max), got %d", bar1.HeightPct)
	}
}

// TestBuildConsumedChart_NoDataDay_DistinctFromNoSnapshotWording — Test
// Contract (g): a day absent from days, inside a window that DOES have
// entries on other days (so the chart itself is NOT Empty), renders the
// D-G6 "no data" tooltip — distinct wording from buildBatteryChart's
// "no snapshot" tooltip for the SAME label.
func TestBuildConsumedChart_NoDataDay_DistinctFromNoSnapshotWording(t *testing.T) {
	present := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	missing := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	days := []analytics.DayConsumption{{Date: present, ConsumedPct: 5.0}}
	c := buildConsumedChart(historyTestCtx, days, present, missing)
	if c.Empty {
		t.Fatal("want non-empty chart (one day is present)")
	}
	if len(c.Bars) != 2 {
		t.Fatalf("want 2 bars, got %d", len(c.Bars))
	}
	miss := c.Bars[1]
	if miss.Present || miss.MarkerFlagged || miss.MarkerSpan || miss.HeightPct != 0 {
		t.Errorf("no-data bar: want Present=false, no markers, HeightPct=0, got %+v", miss)
	}
	wantTooltip := "08-16 · no data"
	if miss.Tooltip != wantTooltip {
		t.Errorf("Tooltip: want %q, got %q", wantTooltip, miss.Tooltip)
	}
	// Distinct from buildBatteryChart's "no snapshot" wording for the SAME label.
	batteryTooltip := fmt.Sprintf(i18n.T(historyTestCtx, i18n.KeyHistoryNoSnapshotTooltip), "08-16")
	if miss.Tooltip == batteryTooltip {
		t.Errorf("consumed no-data tooltip must differ from battery's no-snapshot tooltip; both were %q", miss.Tooltip)
	}
	if strings.Contains(miss.Tooltip, "no snapshot") {
		t.Errorf("consumed no-data tooltip must not reuse \"no snapshot\" wording: got %q", miss.Tooltip)
	}
}

// TestBuildConsumedChart_EmptyWhenZeroDays — Test Contract (h): Empty fires
// only when days is empty for the ENTIRE window (mirrors buildBatteryChart's
// zero-entries rule, NOT buildOdometerChart's "fewer than 2" rule).
func TestBuildConsumedChart_EmptyWhenZeroDays(t *testing.T) {
	start := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	c := buildConsumedChart(historyTestCtx, nil, start, end)
	if !c.Empty {
		t.Error("want Empty=true for zero days")
	}
	if len(c.Bars) != 0 {
		t.Errorf("want zero bars when Empty, got %d", len(c.Bars))
	}
}

// TestBuildConsumedChart_BucketsOnDateVerbatim_NoEffectiveDayUTC — Test
// Contract (i), the D-G2 regression guard: bucketing uses
// analytics.DayConsumption.Date VERBATIM, never re-derived via
// effectiveDayUTC. DayConsumption carries no EffectiveDate-shaped field, so
// this only proves the bucket key came from Date directly.
func TestBuildConsumedChart_BucketsOnDateVerbatim_NoEffectiveDayUTC(t *testing.T) {
	d := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	days := []analytics.DayConsumption{{Date: d, ConsumedPct: 11.0}}
	c := buildConsumedChart(historyTestCtx, days, d, d)
	if c.Empty || len(c.Bars) != 1 {
		t.Fatalf("want 1 bar, got %d (empty=%v)", len(c.Bars), c.Empty)
	}
	bar := c.Bars[0]
	if bar.Label != "08-10" {
		t.Errorf("Label: want 08-10, got %q", bar.Label)
	}
	if !bar.Present {
		t.Error("want Present=true")
	}
}

// TestBuildConsumedChart_FlaggedDayNeverDistortsScale_SingleClamp — Test
// Contract (i2), the D-G1 dead-branch regression guard: a flagged
// (non-span) day and a normal day in the SAME window. The flagged day's
// math.Max(0, ConsumedPct) alone keeps it out of the window max — no
// per-state exclusion branch is involved or needed. This test exists to
// fail if a future edit reintroduces an
// `if !markerSpan && markerFlagged { return 0 }`-shaped branch believing it
// is load-bearing (design.md D-G1): it is not, because Flagged implies
// ConsumedPct <= 0 (tier 3 D5) so the general clamp already produces the
// same result.
func TestBuildConsumedChart_FlaggedDayNeverDistortsScale_SingleClamp(t *testing.T) {
	d1 := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)
	days := []analytics.DayConsumption{
		{Date: d1, ConsumedPct: -8.0, Flagged: true, MissingChargingType: analytics.MissingChargingTypeManual, DaysSpanned: 1},
		{Date: d2, ConsumedPct: 12.0, Flagged: false, DaysSpanned: 1},
	}
	c := buildConsumedChart(historyTestCtx, days, d1, d2)
	if c.Empty || len(c.Bars) != 2 {
		t.Fatalf("want 2 bars, got %d (empty=%v)", len(c.Bars), c.Empty)
	}
	bar0, bar1 := c.Bars[0], c.Bars[1]
	if !bar0.MarkerFlagged || bar0.MarkerSpan {
		t.Errorf("bar[0]: want MarkerFlagged=true, MarkerSpan=false, got %+v", bar0)
	}
	if bar0.HeightPct != 0 {
		t.Errorf("bar[0].HeightPct: want 0, got %d", bar0.HeightPct)
	}
	if bar1.HeightPct != 100 {
		t.Errorf("bar[1].HeightPct: want 100 (max=12.0, the flagged day's clamped 0 never raised it), got %d", bar1.HeightPct)
	}
}

// TestHandler_AnalyticsReaderDepsForwarding — design.md Test Contract (o):
// New(Deps{AnalyticsReader: fake}) is the SAME instance buildHistoryView
// calls. There is no dedicated forwarding test for SuperchargerReader or
// ChargingReader in this suite to mirror name-for-name (grepped first,
// per tasks.md T8.14's instruction) — every sibling port is instead verified
// by exercising the handler end-to-end and asserting the fake recorded the
// call, the same call-recording-fake technique fakeHistoryReader.betweenCalled
// already uses for TelemetryReader (see
// TestBuildHistoryView_PassesReadStartLookbackToEndToReader). This test
// mirrors that shape for AnalyticsReader rather than inventing a reflection-
// based "same pointer" check.
func TestHandler_AnalyticsReaderDepsForwarding(t *testing.T) {
	historyReader := &fakeHistoryReader{historySnaps: []telemetry.Snapshot{}}
	analyticsReader := &fakeAnalyticsReader{days: []analytics.DayConsumption{}}
	h := newHandlerForHistoryWithAnalytics(historyReader, analyticsReader, 42, "VIN42")

	start := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	_ = h.buildHistoryView(context.Background(), uuid.New(), 42, start, end, startOfDay(time.Now()))

	if !analyticsReader.consumedByDayCalled {
		t.Fatal("want ConsumedByDay called on the Deps-supplied AnalyticsReader — Handler.analyticsReader must be the same instance New(Deps{AnalyticsReader: ...}) was given")
	}
	if !analyticsReader.gotStart.Equal(start) || !analyticsReader.gotEnd.Equal(end) {
		t.Errorf("want ConsumedByDay called with (start=%v, end=%v), got (%v, %v)", start, end, analyticsReader.gotStart, analyticsReader.gotEnd)
	}
	// OdometerDeltaByDay forwarding — RM29-analytics-add-vehicle-metrics
	// task 5.1 gave the odometer chart its own separate port call on the
	// same Deps-supplied AnalyticsReader; assert it the same way.
	if !analyticsReader.odometerByDayCalled {
		t.Fatal("want OdometerDeltaByDay called on the Deps-supplied AnalyticsReader — Handler.analyticsReader must be the same instance New(Deps{AnalyticsReader: ...}) was given")
	}
	if !analyticsReader.gotOdoStart.Equal(start) || !analyticsReader.gotOdoEnd.Equal(end) {
		t.Errorf("want OdometerDeltaByDay called with (start=%v, end=%v), got (%v, %v)", start, end, analyticsReader.gotOdoStart, analyticsReader.gotOdoEnd)
	}
}

// --- buildHistoryView (end-to-end handler logic + reader) ---

// TestBuildHistoryView_TelemetryReaderError_DegradesBatteryChartOnly is the
// roadmap-D5-era successor to the pre-move
// TestBuildHistoryView_ReaderError_DegradesBothChartsEmpty: since the
// odometer chart moved off telemetry.Reader entirely onto its own
// analytics.Reader.OdometerDeltaByDay port (design.md D6), a
// SnapshotsByVehicleBetween error can now only ever degrade the Battery
// chart — the one chart still fed directly from that read (spec.md "Odometer
// chart data comes exclusively through analytics.Reader"). This fixture
// supplies no snaps for the default fakeAnalyticsReader to derive distances
// from either, so v.Odometer is ALSO empty here — but for an unrelated
// reason (no data), not because of the telemetry error; see
// TestBuildHistoryView_AnalyticsReaderOdometerError_DegradesOdometerChartOnly
// for the real cross-port independence proof.
func TestBuildHistoryView_TelemetryReaderError_DegradesBatteryChartOnly(t *testing.T) {
	reader := &fakeHistoryReader{historyErr: errTestHistory}
	h := newHandlerForHistory(reader, 42, "VIN42")
	start := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	v := h.buildHistoryView(context.Background(), uuid.New(), 42, start, end, startOfDay(time.Now()))
	if !v.Battery.Empty {
		t.Error("want Battery.Empty on telemetry reader error")
	}
	// Presets are still built so the selector is usable despite the read error.
	if len(v.Presets) == 0 {
		t.Error("want presets rendered even on reader error")
	}
}

// TestBuildHistoryView_AnalyticsReaderOdometerError_DegradesOdometerChartOnly
// is the real cross-port independence proof RM29-analytics-add-vehicle-
// metrics task 5.1 introduced (spec.md "Odometer chart data comes
// exclusively through analytics.Reader"): a SUCCESSFUL telemetry read (so
// the Battery chart is populated) alongside a FAILING OdometerDeltaByDay
// call degrades ONLY v.Odometer — the already-populated Battery chart,
// sourced from the separate telemetry.Reader call, must stay intact.
func TestBuildHistoryView_AnalyticsReaderOdometerError_DegradesOdometerChartOnly(t *testing.T) {
	start := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	lookback := start.AddDate(0, 0, -1)
	snaps := snapsForDays(append([]time.Time{lookback}, calendarDays(start, end)...), 1000, 10, 70)
	historyReader := &fakeHistoryReader{historySnaps: snaps}             // succeeds -- populates Battery
	analyticsReader := &fakeAnalyticsReader{odometerErr: errTestHistory} // OdometerDeltaByDay fails
	h := newHandlerForHistoryWithAnalytics(historyReader, analyticsReader, 42, "VIN42")

	v := h.buildHistoryView(context.Background(), uuid.New(), 42, start, end, startOfDay(time.Now()))

	if !v.Odometer.Empty {
		t.Error("want Odometer.Empty=true on OdometerDeltaByDay error")
	}
	if v.Battery.Empty {
		t.Error("want Battery.Empty=false — an OdometerDeltaByDay error must NOT blank the already-populated battery chart")
	}
	if len(v.Battery.Bars) == 0 {
		t.Error("want Battery.Bars non-empty")
	}
}

// TestBuildHistoryView_ConsumedReaderError_LeavesOtherChartsIntact is the
// D-G10 regression guard (tasks.md T8.16, appended by the leader after wave
// 3 returned — design.md D-G10 has no dedicated Test Contract entry, so it
// fell outside the original T8.1-T8.14 enumeration). ConsumedByDay is a
// SEPARATE read against a SEPARATE port from SnapshotsByVehicleBetween — its
// own error must degrade ONLY v.Consumed, never wipe out the
// already-populated v.Odometer/v.Battery (design.md D-G10). This test
// exists to catch a future refactor that moves the consumed read ahead of
// the snapshot read, or folds it into the snapshot error branch, either of
// which would silently blank all three charts with no other test noticing.
func TestBuildHistoryView_ConsumedReaderError_LeavesOtherChartsIntact(t *testing.T) {
	start := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC) // 5-day window
	lookback := start.AddDate(0, 0, -1)
	snaps := snapsForDays(append([]time.Time{lookback}, calendarDays(start, end)...), 1000, 10, 70)
	historyReader := &fakeHistoryReader{historySnaps: snaps} // succeeds — populates the Battery chart
	// snaps also seeds the odometer chart via distancesFromSnaps (see
	// fakeAnalyticsReader's snaps field) so this test still exercises "the
	// two ALREADY-populated charts stay intact" for both siblings, not just
	// Battery.
	analyticsReader := &fakeAnalyticsReader{err: errTestHistory, snaps: snaps} // ConsumedByDay fails
	h := newHandlerForHistoryWithAnalytics(historyReader, analyticsReader, 42, "VIN42")

	v := h.buildHistoryView(context.Background(), uuid.New(), 42, start, end, startOfDay(time.Now()))

	if !v.Consumed.Empty {
		t.Error("want Consumed.Empty=true on ConsumedByDay error")
	}
	if v.Odometer.Empty {
		t.Error("want Odometer.Empty=false — a ConsumedByDay error must NOT blank the already-populated odometer chart (design.md D-G10)")
	}
	if len(v.Odometer.Bars) == 0 {
		t.Error("want Odometer.Bars non-empty")
	}
	if v.Battery.Empty {
		t.Error("want Battery.Empty=false — a ConsumedByDay error must NOT blank the already-populated battery chart (design.md D-G10)")
	}
	if len(v.Battery.Bars) == 0 {
		t.Error("want Battery.Bars non-empty")
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
	// D11: default end = yesterday (design.md D-G9), not today. readStart =
	// yesterday - 7 (lookback 1 + default 6); end = yesterday.
	yesterday := browserTodayNoCookie().AddDate(0, 0, -1)
	wantStart := yesterday.AddDate(0, 0, -7)
	wantEnd := yesterday
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
	// Default window readStart (lookback 1 + default 6 = yesterday-7, D11).
	wantStart := browserTodayNoCookie().AddDate(0, 0, -1).AddDate(0, 0, -7)
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
	// loads tomorrow. Post-D11 (design.md D-G9), the API's both-absent
	// default NOW MATCHES this preset window (both end at yesterday) — this
	// test still exercises the dashboard's explicit self-load href, not the
	// both-absent path (that's TestDashboardHistoryFragment_DefaultWindowPassedToReader).
	yesterday := browserTodayNoCookie().AddDate(0, 0, -1)
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
	// end must be yesterday, not today: D11's cap now REJECTS an explicit
	// end=today request (design.md D-G9) — this test submits an explicit
	// ?end= via the URL, so it must respect the same cap the handler enforces.
	end := startOfDay(time.Now()).AddDate(0, 0, -1)
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

		odo := buildOdometerChart(historyTestCtx, distancesFromSnaps(snaps, start, end), start, end)
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
	// end must be yesterday, not today: D11's cap now REJECTS an explicit
	// end=today request (design.md D-G9) — this test submits an explicit
	// ?end= via the URL, so it must respect the same cap the handler enforces.
	end := startOfDay(time.Now()).AddDate(0, 0, -1)
	start := end.AddDate(0, 0, -13) // 14-day inclusive window ending yesterday
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
	// Anchored on browserTodayNoCookie, not startOfDay: this test drives the real
	// GET /ui/dashboard handler with NO browser_tz cookie, so defaultHistoryHref
	// renders "yesterday" from clock.Zone() (America/Bogota). A UTC-derived
	// expectation only matches outside 00:00-05:00 UTC, where the two calendar
	// dates still coincide - a latent flake, not a stable pass (review R1-1).
	yesterday := browserTodayNoCookie().AddDate(0, 0, -1).Format("2006-01-02")
	if !strings.Contains(body, "end="+yesterday) {
		t.Errorf("dashboard history self-load end= must be yesterday (%s); got: %s", yesterday, body)
	}
	// The Refresh button is GONE (removed by RM8 design D5).
	if strings.Contains(body, ">Refresh<") {
		t.Errorf("dashboard page header must NOT contain a Refresh button; got: %s", body)
	}
}
