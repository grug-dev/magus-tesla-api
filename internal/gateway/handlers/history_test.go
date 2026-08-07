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
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// --- fakes for the history handler tests ---

// fakeHistoryReader is a test double for telemetry.Reader that records the
// SnapshotsByVehicleSince call so tests can assert the correct since time was passed.
// LatestSnapshotsByAccount returns empty (history tests don't use it).
type fakeHistoryReader struct {
	historySnaps  []telemetry.Snapshot
	historyErr    error
	capturedSince time.Time // set by SnapshotsByVehicleSince so tests can assert
}

func (f *fakeHistoryReader) LatestSnapshotsByAccount(_ context.Context, _ uuid.UUID) ([]telemetry.Snapshot, error) {
	return []telemetry.Snapshot{}, nil
}

func (f *fakeHistoryReader) SnapshotsByVehicleSince(_ context.Context, _ uuid.UUID, _ int64, since time.Time) ([]telemetry.Snapshot, error) {
	f.capturedSince = since
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

// dailySnaps builds N+1 snapshots oldest-first: a fixed odometer step + battery
// level, useful for building chart test inputs.
func dailySnaps(n int, odometerBase float64, odometerStep float64, batteryBase int) []telemetry.Snapshot {
	snaps := make([]telemetry.Snapshot, n+1)
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	for i := range snaps {
		snaps[i] = telemetry.Snapshot{
			OdometerKm:      odometerBase + float64(i)*odometerStep,
			BatteryLevelPct: batteryBase + i,
			BatteryRangeKm:  300,
			CapturedAt:      base.AddDate(0, 0, i),
		}
	}
	return snaps
}

// --- clampHistoryDays unit tests (pure function) ---

func TestClampHistoryDays_Presets(t *testing.T) {
	tests := []struct {
		raw  string
		want int
	}{
		{"6", 6},
		{"14", 14},
		{"30", 30},
		{"", defaultHistoryDays},
		{"abc", defaultHistoryDays},
		{"5", defaultHistoryDays},
		{"7", defaultHistoryDays},
		{"0", defaultHistoryDays},
		{"-6", defaultHistoryDays},
		{"100", defaultHistoryDays},
		{"6.0", defaultHistoryDays},
		{" 6", defaultHistoryDays},
	}
	for _, tc := range tests {
		got := clampHistoryDays(tc.raw)
		if got != tc.want {
			t.Errorf("clampHistoryDays(%q): want %d, got %d", tc.raw, tc.want, got)
		}
	}
}

// --- buildOdometerChart unit tests ---

func TestBuildOdometerChart_EmptyWhenFewerThanTwoSnapshots(t *testing.T) {
	// 0 snapshots.
	c := buildOdometerChart(nil, 6)
	if !c.Empty {
		t.Error("want Empty=true for 0 snapshots")
	}
	// 1 snapshot.
	c = buildOdometerChart([]telemetry.Snapshot{{OdometerKm: 100}}, 6)
	if !c.Empty {
		t.Error("want Empty=true for 1 snapshot")
	}
}

func TestBuildOdometerChart_NDeltas_FromNPlusOnePoints(t *testing.T) {
	// 7 snapshots → 6 delta bars for days=6.
	snaps := dailySnaps(6, 1000, 10, 70)
	c := buildOdometerChart(snaps, 6)
	if c.Empty {
		t.Fatal("want non-empty chart for 7 snapshots with days=6")
	}
	if len(c.Bars) != 6 {
		t.Errorf("want 6 bars, got %d", len(c.Bars))
	}
}

func TestBuildOdometerChart_NegativeDeltaClampedToZero(t *testing.T) {
	snaps := []telemetry.Snapshot{
		{OdometerKm: 1000, CapturedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		{OdometerKm: 900, CapturedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}, // negative
	}
	c := buildOdometerChart(snaps, 6)
	if c.Empty {
		t.Fatal("want 1 bar for 2 snapshots")
	}
	if c.Bars[0].HeightPct != 0 {
		t.Errorf("want HeightPct=0 for negative delta, got %d", c.Bars[0].HeightPct)
	}
}

func TestBuildOdometerChart_TooltipContainsDateAndKeywords(t *testing.T) {
	snaps := []telemetry.Snapshot{
		{OdometerKm: 12000, CapturedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)},
		{OdometerKm: 12100, CapturedAt: time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)},
	}
	c := buildOdometerChart(snaps, 6)
	if c.Empty || len(c.Bars) == 0 {
		t.Fatal("want bars")
	}
	tt := c.Bars[0].Tooltip
	for _, want := range []string{"2026-07-02", "km driven", "odometer"} {
		if !strings.Contains(tt, want) {
			t.Errorf("tooltip missing %q, got: %q", want, tt)
		}
	}
}

func TestBuildOdometerChart_FewerThanNPlusOneSnapshots(t *testing.T) {
	// 4 snapshots → only 3 delta bars (days=6 requested but only 3 available).
	snaps := dailySnaps(3, 1000, 10, 70)
	c := buildOdometerChart(snaps, 6)
	if c.Empty {
		t.Fatal("want non-empty chart — 4 points give 3 valid delta bars")
	}
	if len(c.Bars) != 3 {
		t.Errorf("want 3 bars (data-limited), got %d", len(c.Bars))
	}
}

// --- buildBatteryChart unit tests ---

func TestBuildBatteryChart_EmptyWhenNoSnapshots(t *testing.T) {
	c := buildBatteryChart(nil, 6)
	if !c.Empty {
		t.Error("want Empty for nil snapshots")
	}
	c = buildBatteryChart([]telemetry.Snapshot{}, 6)
	if !c.Empty {
		t.Error("want Empty for empty slice")
	}
}

func TestBuildBatteryChart_NBarsFromLastNSnapshots(t *testing.T) {
	// 10 snapshots → 6 bars for days=6 (uses last 6 oldest-first → last 6).
	snaps := dailySnaps(9, 1000, 10, 60)
	c := buildBatteryChart(snaps, 6)
	if c.Empty {
		t.Fatal("want non-empty chart")
	}
	if len(c.Bars) != 6 {
		t.Errorf("want 6 bars for days=6 with 10 snapshots, got %d", len(c.Bars))
	}
}

func TestBuildBatteryChart_HeightPctEqualsLevel(t *testing.T) {
	snaps := []telemetry.Snapshot{
		{BatteryLevelPct: 75, BatteryRangeKm: 300, CapturedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)},
	}
	c := buildBatteryChart(snaps, 6)
	if c.Empty || len(c.Bars) == 0 {
		t.Fatal("want 1 bar")
	}
	if c.Bars[0].HeightPct != 75 {
		t.Errorf("want HeightPct=75 (== BatteryLevelPct), got %d", c.Bars[0].HeightPct)
	}
}

func TestBuildBatteryChart_TooltipContainsDateLevelAndRange(t *testing.T) {
	snaps := []telemetry.Snapshot{
		{BatteryLevelPct: 82, BatteryRangeKm: 300, CapturedAt: time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC)},
	}
	c := buildBatteryChart(snaps, 6)
	if c.Empty || len(c.Bars) == 0 {
		t.Fatal("want bars")
	}
	tt := c.Bars[0].Tooltip
	for _, want := range []string{"2026-07-03", "82%", "km range"} {
		if !strings.Contains(tt, want) {
			t.Errorf("battery tooltip missing %q, got: %q", want, tt)
		}
	}
}

// --- buildHistoryView unit tests ---

func TestBuildHistoryView_ReaderError_DegradesBothChartsEmpty(t *testing.T) {
	reader := &fakeHistoryReader{historyErr: errTestHistory}
	h := newHandlerForHistory(reader, 42, "VIN42")
	since := time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)
	v := h.buildHistoryView(context.Background(), uuid.New(), 42, 6, since)
	if !v.Odometer.Empty {
		t.Error("want Odometer.Empty on reader error")
	}
	if !v.Battery.Empty {
		t.Error("want Battery.Empty on reader error")
	}
}

func TestBuildHistoryView_CorrectSincePassedToReader(t *testing.T) {
	reader := &fakeHistoryReader{historySnaps: []telemetry.Snapshot{}}
	h := newHandlerForHistory(reader, 42, "VIN42")
	since := time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)
	_ = h.buildHistoryView(context.Background(), uuid.New(), 42, 6, since)
	if !reader.capturedSince.Equal(since) {
		t.Errorf("want capturedSince=%v, got %v", since, reader.capturedSince)
	}
}

func TestBuildHistoryView_OdometerNDeltas_BatteryNBars(t *testing.T) {
	snaps := dailySnaps(6, 1000, 10, 70) // 7 points → 6 deltas & 6 battery bars
	reader := &fakeHistoryReader{historySnaps: snaps}
	h := newHandlerForHistory(reader, 42, "VIN42")
	since := time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)
	v := h.buildHistoryView(context.Background(), uuid.New(), 42, 6, since)

	if v.Odometer.Empty || len(v.Odometer.Bars) != 6 {
		t.Errorf("want 6 odometer bars, got %d (empty=%v)", len(v.Odometer.Bars), v.Odometer.Empty)
	}
	if v.Battery.Empty || len(v.Battery.Bars) != 6 {
		t.Errorf("want 6 battery bars, got %d (empty=%v)", len(v.Battery.Bars), v.Battery.Empty)
	}
}

func TestBuildHistoryView_DaysAndPresets(t *testing.T) {
	reader := &fakeHistoryReader{historySnaps: []telemetry.Snapshot{}}
	h := newHandlerForHistory(reader, 42, "VIN42")
	since := time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)
	v := h.buildHistoryView(context.Background(), uuid.New(), 42, 14, since)
	if v.Days != 14 {
		t.Errorf("want v.Days=14, got %d", v.Days)
	}
	if len(v.Presets) != len(historyDayPresets) {
		t.Errorf("want %d presets, got %d", len(historyDayPresets), len(v.Presets))
	}
}

// --- HTTP-level handler tests (E.1 + E.2) ---

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

func TestDashboardHistoryFragment_AuthenticatedReturns200(t *testing.T) {
	uid := uuid.New()
	reader := &fakeHistoryReader{historySnaps: dailySnaps(6, 1000, 10, 70)}
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
		t.Fatalf("want 200 for authenticated request, got %d", w.Code)
	}
}

func TestDashboardHistoryFragment_DefaultDaysIsApplied(t *testing.T) {
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
	// Default days=6 → since ≈ startOfDay(today) − 6 days.
	wantSince := startOfDay(time.Now()).AddDate(0, 0, -defaultHistoryDays)
	diff := reader.capturedSince.Sub(wantSince)
	if diff < 0 {
		diff = -diff
	}
	if diff > 24*time.Hour {
		t.Errorf("want since≈%v (today-6), got %v (diff=%v)", wantSince, reader.capturedSince, diff)
	}
}

func TestDashboardHistoryFragment_InvalidDaysFallsBackToDefault(t *testing.T) {
	uid := uuid.New()
	reader := &fakeHistoryReader{historySnaps: []telemetry.Snapshot{}}
	h := newHandlerForHistory(reader, 42, "VIN42")
	eng := historyEngine(h, uid, 42, "VIN42")
	c := sessionCookie(eng, uid, "")

	for _, bad := range []string{"99", "abc", "0", "-3"} {
		reader.capturedSince = time.Time{}
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/ui/dashboard/history?days="+bad, nil)
		if c != nil {
			req.AddCookie(c)
		}
		eng.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("days=%q: want 200, got %d", bad, w.Code)
		}
		// Confirm the default days=6 was used (since ≈ today-6).
		wantSince := startOfDay(time.Now()).AddDate(0, 0, -defaultHistoryDays)
		diff := reader.capturedSince.Sub(wantSince)
		if diff < 0 {
			diff = -diff
		}
		if diff > 24*time.Hour {
			t.Errorf("days=%q: want default since≈%v, got %v", bad, wantSince, reader.capturedSince)
		}
	}
}

func TestDashboardHistoryFragment_ValidPresetsAreAccepted(t *testing.T) {
	uid := uuid.New()
	reader := &fakeHistoryReader{historySnaps: []telemetry.Snapshot{}}
	h := newHandlerForHistory(reader, 42, "VIN42")
	eng := historyEngine(h, uid, 42, "VIN42")
	c := sessionCookie(eng, uid, "")

	for _, p := range historyDayPresets {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/ui/dashboard/history?days=%d", p), nil)
		if c != nil {
			req.AddCookie(c)
		}
		eng.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("days=%d: want 200, got %d", p, w.Code)
		}
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
	body := w.Body.String()
	if !strings.Contains(body, "Awaiting nightly snapshots") {
		t.Errorf("want empty-state placeholder on reader error; body: %s", body)
	}
}

// --- fragment structure / render tests (E.2) ---

func TestDashboardHistoryFragment_ContainsSVGViewBoxAndTitleTooltips(t *testing.T) {
	uid := uuid.New()
	snaps := dailySnaps(6, 1000, 10, 70)
	reader := &fakeHistoryReader{historySnaps: snaps}
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
	body := w.Body.String()
	// Responsive SVG with viewBox.
	if !strings.Contains(body, "viewBox") {
		t.Error("fragment must contain responsive <svg viewBox …>")
	}
	// Native browser tooltip via <title>.
	if !strings.Contains(body, "<title>") {
		t.Error("fragment must contain <title> tooltip elements inside SVG bars")
	}
}

func TestDashboardHistoryFragment_SelectorMarksActivePreset(t *testing.T) {
	uid := uuid.New()
	snaps := dailySnaps(6, 1000, 10, 70)
	reader := &fakeHistoryReader{historySnaps: snaps}
	h := newHandlerForHistory(reader, 42, "VIN42")
	eng := historyEngine(h, uid, 42, "VIN42")
	c := sessionCookie(eng, uid, "")

	// Request with days=14.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/dashboard/history?days=14", nil)
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
		t.Error("selector must mark the active preset with btn-primary")
	}
	// All three preset labels appear.
	for _, p := range historyDayPresets {
		label := fmt.Sprintf("%d days", p)
		if !strings.Contains(body, label) {
			t.Errorf("selector must contain label %q", label)
		}
	}
}

func TestDashboard_HistoryRegionInsideDashboardContent(t *testing.T) {
	// This is a structural test: render the full dashboard page and confirm that
	// #dashboard-history is present inside #dashboard-content and carries
	// hx-trigger="load" (the self-load marker).
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
}
