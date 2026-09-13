package handlers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/charging"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/fragments"
)

// --- fakes for the Supercharger Stats handler tests ---

// fakeSessionReader is a test double for charging.SessionReader (renamed from
// fakeSuperchargerReader — RM30-gateway-read-supercharger-stats-from-charging,
// design.md D2). ListSessionsByVehicleBetween mirrors the REAL port's
// contract: it only ever returns sessions whose TeslaID matches the requested
// filter, exactly like the real SQL WHERE tesla_id = $1 clause. Do not drop
// this filtering — it is what proves another vehicle's session never reaches
// the view.
type fakeSessionReader struct {
	sessions []charging.Session
	err      error

	capturedTeslaID int64
	capturedStart   time.Time
	capturedEnd     time.Time
}

func (f *fakeSessionReader) ListSessionsByVehicleBetween(_ context.Context, teslaID int64, start, end time.Time) ([]charging.Session, error) {
	f.capturedTeslaID = teslaID
	f.capturedStart = start
	f.capturedEnd = end
	if f.err != nil {
		return nil, f.err
	}
	out := make([]charging.Session, 0, len(f.sessions))
	for _, s := range f.sessions {
		if s.TeslaID == teslaID {
			out = append(out, s)
		}
	}
	return out, nil
}

// errTestSupercharger is a sentinel error for Supercharger Stats reader tests.
var errTestSupercharger = errors.New("test supercharger reader error")

// newHandlerForSupercharger builds a Handler with the given fake reader and one
// registered vehicle. Mirrors newHandlerForHistory.
func newHandlerForSupercharger(reader *fakeSessionReader, teslaID int64, vin string) *Handler {
	acct := &fakeAccount{
		registered: []account.Vehicle{
			{TeslaID: teslaID, VIN: vin, DisplayName: "Test Vehicle"},
		},
	}
	return New(Deps{
		Account:            acct,
		Tesla:              &fakeTesla{},
		SuperchargerReader: reader,
	})
}

// superchargerEngine builds a minimal Gin engine with session middleware and
// both Supercharger Stats routes. Mirrors historyEngine.
func superchargerEngine(h *Handler, uid uuid.UUID, selTeslaID int64, selVIN string) *gin.Engine {
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
	r.GET("/supercharger-stats", h.SuperchargerStatsPage)
	r.GET("/ui/supercharger-stats", h.SuperchargerStatsFragment)
	return r
}

// ptrF64 is a small pointer helper for building nullable session
// fields. ptrStr already exists in external_charges_test.go — reused here.
func ptrF64(f float64) *float64 { return &f }
func ptrInt(i int) *int         { return &i }

// parseSuperchargerRangeAt builds a gin.Context with the given start/end query
// params and runs parseSuperchargerRange against the given explicit today —
// mirrors history_test.go's parseRange helper, but for
// parseSuperchargerRange's (c, today) signature: today is an explicit
// parameter here, not derived from browserToday(c) (design.md D9a).
func parseSuperchargerRangeAt(start, end string, today time.Time) (time.Time, time.Time, bool) {
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
	return parseSuperchargerRange(c, today)
}

// superchargerRangeURL builds a /ui/supercharger-stats?start=&end= (or
// /supercharger-stats?start=&end=) URL from explicit start/end times,
// computed the SAME WAY the handler computes a window — mirrors
// history_test.go's rule of never hardcoding a date that will go stale.
func superchargerRangeURL(path string, start, end time.Time) string {
	return fmt.Sprintf("%s?start=%s&end=%s", path, start.Format("2006-01-02"), end.Format("2006-01-02"))
}

// --- parseSuperchargerRange unit tests (design.md D1, Test Contract T1-T6, T13-T14) ---

// TestParseSuperchargerRange_BothAbsent_DefaultSixMonthWindow is Test
// Contract T1: both params absent -> the month-aligned 6-month default
// window (design.md D5). For today=2026-08-27: end=2026-08-27,
// start=2026-03-01 (startOfMonth(2026-08-27)=2026-08-01, minus 5 months).
func TestParseSuperchargerRange_BothAbsent_DefaultSixMonthWindow(t *testing.T) {
	today := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	start, end, ok := parseSuperchargerRangeAt("", "", today)
	if !ok {
		t.Fatal("want ok=true for both absent")
	}
	if !end.Equal(today) {
		t.Errorf("want end=today (%v), got %v", today, end)
	}
	wantStart := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if !start.Equal(wantStart) {
		t.Errorf("want start=%v, got %v", wantStart, start)
	}
}

// TestParseSuperchargerRange_ExplicitWindowHonoredUnaligned is Test Contract
// T2: an explicit, valid window is honored exactly as given, NOT rounded to
// a month boundary — month-alignment is a preset/default convenience only
// (design.md D5).
func TestParseSuperchargerRange_ExplicitWindowHonoredUnaligned(t *testing.T) {
	today := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	start, end, ok := parseSuperchargerRangeAt("2026-06-15", "2026-08-20", today)
	if !ok {
		t.Fatal("want ok=true for a valid explicit window")
	}
	wantStart := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	if !start.Equal(wantStart) || !end.Equal(wantEnd) {
		t.Errorf("want (%v,%v), got (%v,%v)", wantStart, wantEnd, start, end)
	}
}

// TestParseSuperchargerRange_EndBeforeStart is Test Contract T3.
func TestParseSuperchargerRange_EndBeforeStart(t *testing.T) {
	today := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	if _, _, ok := parseSuperchargerRangeAt("2026-08-20", "2026-06-15", today); ok {
		t.Error("want ok=false when end < start")
	}
}

// TestParseSuperchargerRange_MalformedNonISO is Test Contract T4.
func TestParseSuperchargerRange_MalformedNonISO(t *testing.T) {
	today := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	if _, _, ok := parseSuperchargerRangeAt("2026-13-40", "2026-08-20", today); ok {
		t.Error("want ok=false for malformed start")
	}
	if _, _, ok := parseSuperchargerRangeAt("2026-06-15", "not-a-date", today); ok {
		t.Error("want ok=false for malformed end")
	}
}

// TestParseSuperchargerRange_MissingPartner is Test Contract T5.
func TestParseSuperchargerRange_MissingPartner(t *testing.T) {
	today := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	if _, _, ok := parseSuperchargerRangeAt("2026-06-15", "", today); ok {
		t.Error("want ok=false when start present but end absent")
	}
	if _, _, ok := parseSuperchargerRangeAt("", "2026-08-20", today); ok {
		t.Error("want ok=false when end present but start absent")
	}
}

// TestParseSuperchargerRange_MaxDaysCap is Test Contract T6: a 401-day window
// is rejected; the SAME start with a 400-day-wide end is accepted
// (design.md D6 — superchargerRangeMaxDays=400).
func TestParseSuperchargerRange_MaxDaysCap(t *testing.T) {
	today := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	if _, _, ok := parseSuperchargerRangeAt("2025-07-01", "2026-08-06", today); ok {
		t.Error("want ok=false for a window wider than the 400-day cap")
	}
	if _, _, ok := parseSuperchargerRangeAt("2025-07-01", "2026-08-05", today); !ok {
		t.Error("want ok=true for a window exactly 400 days wide")
	}
}

// TestParseSuperchargerRange_EndEqualsTodayAccepted is Test Contract T13
// (design.md D9b, inclusive boundary): end == UTC today IS accepted — this
// endpoint does NOT inherit history's browser-yesterday cap.
func TestParseSuperchargerRange_EndEqualsTodayAccepted(t *testing.T) {
	today := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	_, end, ok := parseSuperchargerRangeAt("2026-03-01", "2026-08-27", today)
	if !ok {
		t.Fatal("want ok=true when end == today")
	}
	if !end.Equal(today) {
		t.Errorf("want end=today, got %v", end)
	}
}

// TestParseSuperchargerRange_EndAfterTodayRejected is Test Contract T14
// (design.md D9b, exclusive boundary): end one day after UTC today is
// rejected. The window is kept ~180 days wide (well under the 400-day cap
// asserted by T6) so the cap cannot be what rejects it — otherwise the test
// would pass for the wrong reason. Also asserts a far-future end still
// inside the cap is rejected by the same rule.
func TestParseSuperchargerRange_EndAfterTodayRejected(t *testing.T) {
	today := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	if _, _, ok := parseSuperchargerRangeAt("2026-03-01", "2026-08-28", today); ok {
		t.Error("want ok=false when end is one day after today")
	}
	if _, _, ok := parseSuperchargerRangeAt("2026-03-01", "2026-12-31", today); ok {
		t.Error("want ok=false for a far-future end well inside the 400-day cap")
	}
}

// Test Contract T12 ("SuperchargerRowVM and charging.Session carry no
// CountryCode/BillingType") is a COMPILE-TIME note, not a runtime test
// (design.md) — deliberately not represented as a Test... function here.
// Neither field is reintroduced anywhere in this file.

// --- buildSuperchargerStatsView unit tests (Test Contract T7-T10) ---

// TestBuildSuperchargerStatsView_EmptyWhenZeroSessionsInWindow is Test
// Contract T7: zero sessions in the resolved window renders the empty state
// with all four tile strings at their zero-value/placeholder form.
func TestBuildSuperchargerStatsView_EmptyWhenZeroSessionsInWindow(t *testing.T) {
	reader := &fakeSessionReader{}
	h := newHandlerForSupercharger(reader, 42, "VIN42")
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	v := h.buildSuperchargerStatsView(context.Background(), uuid.New(), 42, start, end, end, "test-csrf")

	if !v.Empty {
		t.Error("want v.Empty=true when the reader returns zero sessions")
	}
	if !v.Chart.Empty {
		t.Error("want v.Chart.Empty=true")
	}
	if len(v.Sessions) != 0 {
		t.Errorf("want len(v.Sessions)=0, got %d", len(v.Sessions))
	}
	if v.Tiles.Sessions != "0" {
		t.Errorf("want Tiles.Sessions=\"0\", got %q", v.Tiles.Sessions)
	}
	if v.Tiles.AvgKWh != "—" {
		t.Errorf("want Tiles.AvgKWh=\"—\", got %q", v.Tiles.AvgKWh)
	}
}

// TestBuildSuperchargerStatsView_ReaderErrorDegradesEmpty is Test Contract
// T8: a reader error degrades to the empty state, never propagates.
func TestBuildSuperchargerStatsView_ReaderErrorDegradesEmpty(t *testing.T) {
	reader := &fakeSessionReader{err: errTestSupercharger}
	h := newHandlerForSupercharger(reader, 42, "VIN42")
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	v := h.buildSuperchargerStatsView(context.Background(), uuid.New(), 42, start, end, end, "test-csrf")

	if !v.Empty {
		t.Error("want v.Empty=true on reader error")
	}
	if !v.Chart.Empty {
		t.Error("want v.Chart.Empty=true on reader error")
	}
}

// TestBuildSuperchargerStatsView_NewestFirstDisplayOrder is Test Contract T9:
// the fake returns three sessions ASCENDING by ChargeStopDateTime (the exact
// order the real port returns), and the handler's single reverse (design.md
// D3) must leave v.Sessions[0] as the MOST recent and v.Sessions[len-1] as
// the OLDEST — proven by asserting the output order is the reverse of the
// fake's input order, not a hardcoded expectation that happens to coincide.
func TestBuildSuperchargerStatsView_NewestFirstDisplayOrder(t *testing.T) {
	reader := &fakeSessionReader{sessions: []charging.Session{
		{SessionID: 1, TeslaID: 42, SiteLocationName: "S1-Jan", ChargeStartDateTime: time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), ChargeStopDateTime: time.Date(2026, 1, 15, 1, 0, 0, 0, time.UTC)},
		{SessionID: 2, TeslaID: 42, SiteLocationName: "S2-Feb", ChargeStartDateTime: time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC), ChargeStopDateTime: time.Date(2026, 2, 15, 1, 0, 0, 0, time.UTC)},
		{SessionID: 3, TeslaID: 42, SiteLocationName: "S3-Mar", ChargeStartDateTime: time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC), ChargeStopDateTime: time.Date(2026, 3, 15, 1, 0, 0, 0, time.UTC)},
	}}
	h := newHandlerForSupercharger(reader, 42, "VIN42")
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)
	v := h.buildSuperchargerStatsView(context.Background(), uuid.New(), 42, start, end, end, "test-csrf")

	if len(v.Sessions) != 3 {
		t.Fatalf("want 3 rows, got %d", len(v.Sessions))
	}
	if v.Sessions[0].SiteLabel != "S3-Mar" {
		t.Errorf("want v.Sessions[0]=S3-Mar (most recent), got %q", v.Sessions[0].SiteLabel)
	}
	if v.Sessions[2].SiteLabel != "S1-Jan" {
		t.Errorf("want v.Sessions[2]=S1-Jan (oldest), got %q", v.Sessions[2].SiteLabel)
	}
}

// TestBuildSuperchargerStatsView_OtherVehicleSessionsNeverAppear proves the
// view shows one vehicle only: the fake filters exactly like the real port's
// SQL WHERE tesla_id = $1 clause. A session can no longer be unattributed —
// tesla_id is NOT NULL — so another vehicle is the case that remains.
func TestBuildSuperchargerStatsView_OtherVehicleSessionsNeverAppear(t *testing.T) {
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	reader := &fakeSessionReader{sessions: []charging.Session{
		{ // another registered vehicle's session.
			SessionID:           1,
			TeslaID:             43,
			SiteLocationName:    "Other Vehicle Site",
			ChargeStartDateTime: start.AddDate(0, 0, 5),
			EnergyKWh:           ptrF64(50),
		},
		{ // attributed to the selected vehicle.
			SessionID:           2,
			TeslaID:             42,
			SiteLocationName:    "Real Site",
			ChargeStartDateTime: start.AddDate(0, 0, 6),
			EnergyKWh:           ptrF64(10),
		},
	}}
	h := newHandlerForSupercharger(reader, 42, "VIN42")
	v := h.buildSuperchargerStatsView(context.Background(), uuid.New(), 42, start, end, end, "test-csrf")

	if v.Empty {
		t.Fatal("want a non-empty view — the attributed session is in the window")
	}
	if len(v.Sessions) != 1 {
		t.Fatalf("want exactly 1 row (the other vehicle's session excluded), got %d", len(v.Sessions))
	}
	if v.Sessions[0].SiteLabel != "Real Site" {
		t.Errorf("want only the attributed session's site, got %q", v.Sessions[0].SiteLabel)
	}
	if v.Tiles.Sessions != "1" {
		t.Errorf("want Tiles.Sessions=1 (unattributed excluded), got %q", v.Tiles.Sessions)
	}
	if v.Tiles.Energy != "10.0 kWh" {
		t.Errorf("want Tiles.Energy=10.0 kWh (unattributed's 50 kWh excluded), got %q", v.Tiles.Energy)
	}
}

// --- buildSuperchargerPresets / D5+D10 regression (Test Contract T11) ---

// TestBuildSuperchargerPresets_ExactValues asserts the first half of Test
// Contract T11: for today=2026-08-27, the 3/6/12-month presets resolve to
// the exact StartStr/EndStr design.md specifies, and the n=6 preset is
// marked Active for the resolved 6-month default window.
func TestBuildSuperchargerPresets_ExactValues(t *testing.T) {
	today := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	start := monthsBackFrom(today, 6)
	end := today
	presets := buildSuperchargerPresets(context.Background(), start, end, today)
	if len(presets) != 3 {
		t.Fatalf("want 3 presets, got %d", len(presets))
	}
	if presets[0].StartStr != "2026-06-01" || presets[0].EndStr != "2026-08-27" {
		t.Errorf("preset[0] (n=3): want start=2026-06-01 end=2026-08-27, got start=%s end=%s", presets[0].StartStr, presets[0].EndStr)
	}
	if presets[1].StartStr != "2026-03-01" {
		t.Errorf("preset[1] (n=6): want start=2026-03-01, got %s", presets[1].StartStr)
	}
	if !presets[1].Active {
		t.Error("preset[1] (n=6) should be Active for the resolved 6-month window")
	}
	if presets[2].StartStr != "2025-09-01" {
		t.Errorf("preset[2] (n=12): want start=2025-09-01, got %s", presets[2].StartStr)
	}
}

// TestBuildSuperchargerChart_SixMonthPresetYieldsExactlySixBars is the second
// half of Test Contract T11 — the D5/D10 regression this design exists to
// prevent: one session per month across all 6 months of the n=6 preset's
// resolved window must render EXACTLY 6 bars, not 5, not 7.
func TestBuildSuperchargerChart_SixMonthPresetYieldsExactlySixBars(t *testing.T) {
	today := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	start := monthsBackFrom(today, 6) // 2026-03-01
	end := today

	sessions := make([]charging.Session, 0, 6)
	for i := 0; i < 6; i++ {
		sessions = append(sessions, charging.Session{
			ChargeStartDateTime: start.AddDate(0, i, 2),
			EnergyKWh:           ptrF64(10),
		})
	}
	c := buildSuperchargerChart(sessions, start, end)
	if c.Empty {
		t.Fatal("want non-empty chart")
	}
	if len(c.Bars) != 6 {
		t.Fatalf("want exactly 6 bars for the 6-month preset (not 5, not 7 — D5/D10), got %d", len(c.Bars))
	}
}

// --- buildSuperchargerTiles unit tests (D7 + D4) — fixture element type is
// charging.Session now; assertions themselves are UNCHANGED (design.md
// "What must NOT change"). ---

func TestBuildSuperchargerTiles_SessionsEnergyAvg(t *testing.T) {
	sessions := []charging.Session{
		{EnergyKWh: ptrF64(10)},
		{EnergyKWh: ptrF64(20)},
		{EnergyKWh: nil}, // still counted in Sessions, skipped in Energy/Avg
	}
	tiles := buildSuperchargerTiles(sessions)
	if tiles.Sessions != "3" {
		t.Errorf("want Sessions=3 (nil-energy session still counted), got %q", tiles.Sessions)
	}
	if tiles.Energy != "30.0 kWh" {
		t.Errorf("want Energy=30.0 kWh (10+20, nil skipped), got %q", tiles.Energy)
	}
	// Avg = 30 / 2 known-energy sessions = 15.0, NOT 30/3.
	if tiles.AvgKWh != "15.0 kWh" {
		t.Errorf("want AvgKWh=15.0 kWh (divided by known-energy count, not total count), got %q", tiles.AvgKWh)
	}
}

func TestBuildSuperchargerTiles_AvgDivideByZeroGuard(t *testing.T) {
	sessions := []charging.Session{
		{EnergyKWh: nil},
		{EnergyKWh: nil},
	}
	tiles := buildSuperchargerTiles(sessions)
	if tiles.AvgKWh != "—" {
		t.Errorf("want AvgKWh=\"—\" when no session has a known EnergyKWh, got %q", tiles.AvgKWh)
	}
	if tiles.Sessions != "2" {
		t.Errorf("want Sessions=2, got %q", tiles.Sessions)
	}
}

// TestBuildSuperchargerTiles_MultiCurrencyNeverSummed covers D4: sessions in
// two different currencies render as two separate cost lines, sorted by
// currency code, never combined into one number.
func TestBuildSuperchargerTiles_MultiCurrencyNeverSummed(t *testing.T) {
	sessions := []charging.Session{
		{TotalCost: ptrF64(40.12), Currency: ptrStr("USD")},
		{TotalCost: ptrF64(58000), Currency: ptrStr("COP")},
		{TotalCost: ptrF64(9.88), Currency: ptrStr("USD")},
	}
	tiles := buildSuperchargerTiles(sessions)
	if len(tiles.CostLines) != 2 {
		t.Fatalf("want 2 cost lines (2 currencies), got %d: %v", len(tiles.CostLines), tiles.CostLines)
	}
	// Sorted by currency code: COP before USD.
	if tiles.CostLines[0] != "58,000.00 COP" {
		t.Errorf("want first line 58,000.00 COP, got %q", tiles.CostLines[0])
	}
	if tiles.CostLines[1] != "50.00 USD" {
		t.Errorf("want second line 50.00 USD (40.12+9.88 summed WITHIN USD only), got %q", tiles.CostLines[1])
	}
	for _, line := range tiles.CostLines {
		if strings.Contains(line, "COP") && strings.Contains(line, "USD") {
			t.Fatalf("a single line must never combine two currencies, got %q", line)
		}
	}
}

// TestBuildSuperchargerTiles_NilCostOrCurrencyExcludedFromCostOnly covers D4:
// a session missing cost or currency is dropped from the cost aggregation but
// still counted in Sessions and Energy.
func TestBuildSuperchargerTiles_NilCostOrCurrencyExcludedFromCostOnly(t *testing.T) {
	sessions := []charging.Session{
		{EnergyKWh: ptrF64(5), TotalCost: nil, Currency: nil},                   // no fee data at all
		{EnergyKWh: ptrF64(7), TotalCost: ptrF64(3.5), Currency: nil},           // cost w/o currency
		{EnergyKWh: ptrF64(9), TotalCost: nil, Currency: ptrStr("USD")},         // currency w/o cost
		{EnergyKWh: ptrF64(11), TotalCost: ptrF64(12), Currency: ptrStr("USD")}, // complete
	}
	tiles := buildSuperchargerTiles(sessions)
	if tiles.Sessions != "4" {
		t.Errorf("want Sessions=4 (all counted regardless of cost data), got %q", tiles.Sessions)
	}
	if tiles.Energy != "32.0 kWh" {
		t.Errorf("want Energy=32.0 kWh (5+7+9+11, all non-nil), got %q", tiles.Energy)
	}
	if len(tiles.CostLines) != 1 {
		t.Fatalf("want exactly 1 cost line (only the complete session), got %d: %v", len(tiles.CostLines), tiles.CostLines)
	}
	if tiles.CostLines[0] != "12.00 USD" {
		t.Errorf("want CostLines=[12.00 USD], got %v", tiles.CostLines)
	}
}

// --- buildSuperchargerChart unit tests (design.md D10 — new (sessions,
// start, end) signature) ---

func TestBuildSuperchargerChart_EmptyWhenSessionsEmpty(t *testing.T) {
	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	c := buildSuperchargerChart(nil, start, end)
	if !c.Empty {
		t.Error("want Empty=true for an empty sessions slice")
	}
	if !c.LabelVertical {
		t.Error("want empty chart labels to be vertical")
	}
}

func TestBuildSuperchargerChart_OneBarPerMonthInWindow(t *testing.T) {
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC) // 3-month window: Mar, Apr, May
	sessions := []charging.Session{
		{ChargeStartDateTime: start.AddDate(0, 0, 2), EnergyKWh: ptrF64(10)}, // month 0 (Mar)
		{ChargeStartDateTime: start.AddDate(0, 2, 2), EnergyKWh: ptrF64(30)}, // month 2 (May) — tallest
	}
	c := buildSuperchargerChart(sessions, start, end)
	if c.Empty {
		t.Fatal("want non-empty chart")
	}
	if len(c.Bars) != 3 {
		t.Fatalf("want 3 bars (one per month in the 3-month window, zero-filled), got %d", len(c.Bars))
	}
	if c.Bars[2].HeightPct != 100 {
		t.Errorf("want the tallest bucket (month 2, 30 kWh) at 100%%, got %d", c.Bars[2].HeightPct)
	}
	// Month 1 (Apr) has zero sessions -> zero-height bar, not skipped.
	if c.Bars[1].HeightPct != 0 {
		t.Errorf("want month 1 (no sessions) at 0%%, got %d", c.Bars[1].HeightPct)
	}
	if c.Bars[0].HeightPct != 33 && c.Bars[0].HeightPct != 34 {
		t.Errorf("want month 0 (10/30=33%%) bar, got %d", c.Bars[0].HeightPct)
	}
	for i, want := range []string{"2026-03", "2026-04", "2026-05"} {
		if c.Bars[i].Label != want {
			t.Errorf("bar %d: want Label=%q, got %q", i, want, c.Bars[i].Label)
		}
	}
	if !c.LabelVertical {
		t.Error("want month labels to be vertical")
	}
	if len(c.YAxisTicks) != 5 {
		t.Fatalf("want 5 y-axis ticks, got %d", len(c.YAxisTicks))
	}
	if c.YAxisTicks[0].Label != "30.0 kWh" || c.YAxisTicks[4].Label != "0.0 kWh" {
		t.Errorf("want 30.0 kWh..0.0 kWh ticks, got %#v", c.YAxisTicks)
	}
}

// design.md T2 / spec.md "A non-empty zero-energy chart has no fabricated axis
// scale": sessions exist, so the chart is NOT Empty, but nothing was charged.
// The bars and their YYYY-MM labels stay; the y-axis must not invent a scale.
func TestBuildSuperchargerChart_ZeroEnergyHasNoYAxisTicks(t *testing.T) {
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC) // 2-month window: Mar, Apr
	sessions := []charging.Session{
		{ChargeStartDateTime: start.AddDate(0, 0, 2), EnergyKWh: ptrF64(0)}, // explicit zero
		{ChargeStartDateTime: start.AddDate(0, 1, 2), EnergyKWh: nil},       // unknown energy
	}
	c := buildSuperchargerChart(sessions, start, end)
	if c.Empty {
		t.Fatal("want a non-empty chart: the sessions exist, only their energy is zero/nil")
	}
	if len(c.Bars) != 2 {
		t.Fatalf("want 2 bars (one per month in the window), got %d", len(c.Bars))
	}
	for i, want := range []string{"2026-03", "2026-04"} {
		if c.Bars[i].Label != want {
			t.Errorf("bar %d: want Label=%q, got %q", i, want, c.Bars[i].Label)
		}
		if c.Bars[i].HeightPct != 0 {
			t.Errorf("bar %d: want 0%% height at a zero maximum, got %d", i, c.Bars[i].HeightPct)
		}
	}
	if len(c.YAxisTicks) != 0 {
		t.Errorf("want no y-axis ticks at a zero maximum, got %#v", c.YAxisTicks)
	}
	if !c.LabelVertical {
		t.Error("want month labels to be vertical")
	}
}

func TestBuildSuperchargerChart_TooltipContainsMonthAndKWh(t *testing.T) {
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)
	sessions := []charging.Session{
		{ChargeStartDateTime: start.AddDate(0, 0, 2), EnergyKWh: ptrF64(12.3)},
	}
	c := buildSuperchargerChart(sessions, start, end)
	if c.Empty || len(c.Bars) == 0 {
		t.Fatal("want bars")
	}
	tt := c.Bars[0].Tooltip
	for _, want := range []string{"Mar 2026", "12.3", "kWh"} {
		if !strings.Contains(tt, want) {
			t.Errorf("tooltip missing %q, got: %q", want, tt)
		}
	}
}

// --- buildSuperchargerRows unit tests — CountryCode/BillingType dropped
// (design.md D4); every other field/assertion UNCHANGED. ---

func TestBuildSuperchargerRows_NilEnergyAndCostRenderDash(t *testing.T) {
	sessions := []charging.Session{
		{
			SiteLocationName: "Site A",
			EnergyKWh:        nil,
			TotalCost:        nil,
			Currency:         nil,
		},
	}
	rows := buildSuperchargerRows(sessions)
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	if rows[0].EnergyLabel != "—" {
		t.Errorf("want EnergyLabel=—, got %q", rows[0].EnergyLabel)
	}
	if rows[0].CostLabel != "—" {
		t.Errorf("want CostLabel=—, got %q", rows[0].CostLabel)
	}
	for _, label := range []string{rows[0].StartBatteryPctLabel, rows[0].EndBatteryPctLabel} {
		if label != "—" {
			t.Errorf("want nil battery value to render em dash, got %q", label)
		}
	}
}

func TestBuildSuperchargerRows_PopulatedFields(t *testing.T) {
	sessions := []charging.Session{
		{
			SiteLocationName:    "Downtown Supercharger",
			ChargeStartDateTime: time.Date(2026, 7, 4, 10, 0, 0, 0, time.UTC),
			EnergyKWh:           ptrF64(23.456),
			TotalCost:           ptrF64(99.9),
			Currency:            ptrStr("MXN"),
			StartBatteryPct:     ptrInt(40),
			EndBatteryPct:       ptrInt(80),
		},
	}
	rows := buildSuperchargerRows(sessions)
	if rows[0].SiteLabel != "Downtown Supercharger" {
		t.Errorf("want SiteLabel, got %q", rows[0].SiteLabel)
	}
	if rows[0].EnergyLabel != "23.46 kWh" {
		t.Errorf("want EnergyLabel=23.46 kWh, got %q", rows[0].EnergyLabel)
	}
	if rows[0].CostLabel != "99.90 MXN" {
		t.Errorf("want CostLabel=99.90 MXN, got %q", rows[0].CostLabel)
	}
	if rows[0].StartBatteryPctLabel != "40%" || rows[0].EndBatteryPctLabel != "80%" {
		t.Errorf("want two formatted battery labels, got %#v", rows[0])
	}
}

// TestBuildSuperchargerRows_CostLabelCommaGrouped covers MAG-9: the session
// CostLabel field (distinct from the CostLines tile field already covered by
// TestBuildSuperchargerTiles_MultiCurrencyNeverSummed) must go through
// formatMoney and comma-group amounts >= 1000.
func TestBuildSuperchargerRows_CostLabelCommaGrouped(t *testing.T) {
	sessions := []charging.Session{
		{
			SiteLocationName: "Big Session",
			TotalCost:        ptrF64(58000),
			Currency:         ptrStr("COP"),
		},
	}
	rows := buildSuperchargerRows(sessions)
	if rows[0].CostLabel != "58,000.00 COP" {
		t.Errorf("want CostLabel=58,000.00 COP (comma-grouped), got %q", rows[0].CostLabel)
	}
}

// --- HTTP-level handler tests ---

func TestSuperchargerStatsPage_AnonymousRedirectsToLogin(t *testing.T) {
	reader := &fakeSessionReader{}
	h := newHandlerForSupercharger(reader, 1, "VIN1")
	eng := superchargerEngine(h, uuid.Nil, 0, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/supercharger-stats", nil)
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("want 302 for anonymous, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/login" {
		t.Errorf("want redirect to /login, got %q", loc)
	}
}

func TestSuperchargerStatsFragment_AnonymousRedirectsToLogin(t *testing.T) {
	reader := &fakeSessionReader{}
	h := newHandlerForSupercharger(reader, 1, "VIN1")
	eng := superchargerEngine(h, uuid.Nil, 0, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/supercharger-stats", nil)
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("want 302 for anonymous, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/login" {
		t.Errorf("want redirect to /login, got %q", loc)
	}
}

func TestSuperchargerStatsPage_NoRegisteredVehicleShowsEmptyState(t *testing.T) {
	uid := uuid.New()
	reader := &fakeSessionReader{}
	acct := &fakeAccount{registered: nil} // nothing registered
	h := New(Deps{Account: acct, Tesla: &fakeTesla{}, SuperchargerReader: reader})
	eng := superchargerEngine(h, uid, 0, "")
	c := sessionCookie(eng, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/supercharger-stats", nil)
	if c != nil {
		req.AddCookie(c)
	}
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 (empty state, not an error) for no vehicle, got %d", w.Code)
	}
	body := w.Body.String()
	// superchargerEngine never wires handlers.LanguageMiddleware, so
	// i18n.FromContext falls back to Spanish (KeySuperchargerEmpty's ES value) —
	// assert the resolved-language string, not the pre-existing English literal
	// (RM24-gateway-translate-all-pages, mirroring tier 2's T6.4 precedent).
	if !strings.Contains(body, "No hay sesiones de Supercharger") {
		t.Errorf("want the empty-state message, body:\n%s", body)
	}
}

func TestSuperchargerStatsFragment_ReaderErrorDegradesNo500(t *testing.T) {
	uid := uuid.New()
	reader := &fakeSessionReader{err: errTestSupercharger}
	h := newHandlerForSupercharger(reader, 42, "VIN42")
	eng := superchargerEngine(h, uid, 42, "VIN42")
	c := sessionCookie(eng, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/supercharger-stats", nil)
	if c != nil {
		req.AddCookie(c)
	}
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 (graceful degrade) on reader error, got %d", w.Code)
	}
	// Resolved language is Spanish here (see the NoRegisteredVehicle comment above).
	if !strings.Contains(w.Body.String(), "No hay sesiones de Supercharger") {
		t.Errorf("want empty-state placeholder on reader error; body: %s", w.Body.String())
	}
}

func TestSuperchargerStatsFragment_RendersBatteryValues(t *testing.T) {
	uid := uuid.New()
	reader := &fakeSessionReader{sessions: []charging.Session{{
		TeslaID:             42,
		SiteLocationName:    "Battery Site",
		ChargeStartDateTime: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
		ChargeStopDateTime:  time.Date(2026, 8, 1, 11, 0, 0, 0, time.UTC),
		StartBatteryPct:     ptrInt(40),
		EndBatteryPct:       ptrInt(80),
	}}}
	h := newHandlerForSupercharger(reader, 42, "VIN42")
	eng := superchargerEngine(h, uid, 42, "VIN42")
	c := sessionCookie(eng, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/supercharger-stats", nil)
	if c != nil {
		req.AddCookie(c)
	}
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	body := w.Body.String()
	// MAG-39: the two header strings ("Batería inicial" / "Batería final") were
	// dropped from this assertion — column heading copy is layout. The battery
	// VALUES stay: they prove the reader's data reaches the rendered row.
	for _, want := range []string{"40%", "80%"} {
		if !strings.Contains(body, want) {
			t.Errorf("want rendered supercharger table to contain %q", want)
		}
	}
	if strings.Contains(body, "País") {
		t.Error("Country must remain absent from the Supercharger table")
	}
}

func TestSuperchargerStatsContent_RendersEnglishHeadersAndNilBatteryValues(t *testing.T) {
	var body bytes.Buffer
	err := fragments.SuperchargerStatsContent(fragments.SuperchargerStatsView{
		Sessions: []fragments.SuperchargerRowVM{{
			DateLabel:            "Fri Aug 1, 2026",
			SiteLabel:            "Nil Battery Site",
			EnergyLabel:          "—",
			CostLabel:            "—",
			StartBatteryPctLabel: "—",
			EndBatteryPctLabel:   "—",
		}},
	}).Render(i18n.WithLang(context.Background(), "en"), &body)
	if err != nil {
		t.Fatalf("render Supercharger Stats content: %v", err)
	}
	for _, want := range []string{"Start battery", "End battery"} {
		if !strings.Contains(body.String(), want) {
			t.Errorf("want English rendered header %q", want)
		}
	}
	if strings.Count(body.String(), "—") < 4 {
		t.Errorf("want the four nil cells (energy, cost, start battery, end battery) to render em dashes, body: %s", body.String())
	}
}

func TestSuperchargerStatsFragment_DefaultMonthsIsSix(t *testing.T) {
	uid := uuid.New()
	reader := &fakeSessionReader{}
	h := newHandlerForSupercharger(reader, 42, "VIN42")
	eng := superchargerEngine(h, uid, 42, "VIN42")
	c := sessionCookie(eng, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/supercharger-stats", nil) // both start/end absent -> default window
	if c != nil {
		req.AddCookie(c)
	}
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	// Resolved language is Spanish here (KeySuperchargerMonthsPreset's ES value "%d meses").
	if !strings.Contains(w.Body.String(), "6 meses") {
		t.Errorf("want default months=6 reflected in the rendered selector, body:\n%s", w.Body.String())
	}
}

// TestSuperchargerStatsFragment_ValidPresetsAccepted replaces the old
// ?months=N sweep with the ?start=&end= equivalent: for each preset month
// count, the URL is built from monthsBackFrom(today, n)..today — the exact
// window the handler itself would compute for that preset — never a
// hardcoded date.
func TestSuperchargerStatsFragment_ValidPresetsAccepted(t *testing.T) {
	uid := uuid.New()
	reader := &fakeSessionReader{}
	h := newHandlerForSupercharger(reader, 42, "VIN42")
	eng := superchargerEngine(h, uid, 42, "VIN42")
	c := sessionCookie(eng, uid, "")

	today := startOfDay(time.Now().UTC())
	for _, n := range superchargerMonthPresets {
		start := monthsBackFrom(today, n)
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, superchargerRangeURL("/ui/supercharger-stats", start, today), nil)
		if c != nil {
			req.AddCookie(c)
		}
		eng.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("n=%d months: want 200, got %d", n, w.Code)
		}
	}
}

// TestSuperchargerStatsFragment_EndEqualsTodayAccepted is the HTTP-level
// half of Test Contract T13: an explicit window whose end is UTC today is
// accepted (200), not rejected.
func TestSuperchargerStatsFragment_EndEqualsTodayAccepted(t *testing.T) {
	uid := uuid.New()
	reader := &fakeSessionReader{}
	h := newHandlerForSupercharger(reader, 42, "VIN42")
	eng := superchargerEngine(h, uid, 42, "VIN42")
	c := sessionCookie(eng, uid, "")

	today := startOfDay(time.Now().UTC())
	start := monthsBackFrom(today, 6)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, superchargerRangeURL("/ui/supercharger-stats", start, today), nil)
	if c != nil {
		req.AddCookie(c)
	}
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 when end == today, got %d", w.Code)
	}
}

// TestSuperchargerStatsFragment_FutureEndRejectedWithNoSelector is the
// HTTP-level half of Test Contract T14: end one day after UTC today is
// rejected with HTTP 400, the empty-state placeholder, and NO preset
// selector rendered (design.md D1/D9b — mirrors DashboardHistoryFragment's
// malformed-request branch). The window is kept ~180 days wide so the
// 400-day cap cannot be what rejects it.
func TestSuperchargerStatsFragment_FutureEndRejectedWithNoSelector(t *testing.T) {
	uid := uuid.New()
	reader := &fakeSessionReader{}
	h := newHandlerForSupercharger(reader, 42, "VIN42")
	eng := superchargerEngine(h, uid, 42, "VIN42")
	c := sessionCookie(eng, uid, "")

	today := startOfDay(time.Now().UTC())
	future := today.AddDate(0, 0, 1)
	start := today.AddDate(0, 0, -180)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, superchargerRangeURL("/ui/supercharger-stats", start, future), nil)
	if c != nil {
		req.AddCookie(c)
	}
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for a future end, got %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "btn-primary") || strings.Contains(body, "join-item") {
		t.Errorf("no preset selector should render on a 400 response, body:\n%s", body)
	}
	if !strings.Contains(body, "No hay sesiones de Supercharger") {
		t.Errorf("want the empty-state placeholder on a 400 response, body:\n%s", body)
	}
}

// --- F.1: render test — SVG/title/active-preset + table/tile single-source-of-truth (D7) ---

// MAG-50: sessionsTileValueRe was removed. It anchored on ui.StatTile's exact
// rendered class list (`stat-title">…</div><div class="stat-value…`), which
// 8795f6b ("Fonts") extended with responsive typography — the regex broke on a
// design change, not a behaviour change. Its removal also drops the D7
// "table rows == the tile's own rendered value" cross-check in the test below;
// that row count is now compared against the fixture directly.

// TestSuperchargerStatsFragment_ChartAndSelectorAndTable covers F.1: the fragment
// contains the responsive <svg viewBox …> chart with <title> tooltips, the month
// selector marks the active preset, and the sessions table's <tbody> holds one <tr>
// per session. Anchored on the default (both start/end absent) 6-month window,
// computed via monthsBackFrom, not a hardcoded date.
//
// MAG-50 renamed this from …TableMatchesSessionsTile. The old name described the D7
// single-source-of-truth check — <tr> count == the Sessions tile's OWN rendered
// value, both parsed independently out of the HTML — which went away with
// sessionsTileValueRe (see its note above). The row count now comes from the
// fixture, so the name no longer claims a comparison the test does not make.
func TestSuperchargerStatsFragment_ChartAndSelectorAndTable(t *testing.T) {
	uid := uuid.New()
	today := startOfDay(time.Now().UTC())
	start := monthsBackFrom(today, superchargerRangeDefaultMonths)
	reader := &fakeSessionReader{sessions: []charging.Session{
		{SessionID: 1, TeslaID: 42, SiteLocationName: "Site A", ChargeStartDateTime: start.AddDate(0, 0, 2), EnergyKWh: ptrF64(10)},
		{SessionID: 2, TeslaID: 42, SiteLocationName: "Site B", ChargeStartDateTime: start.AddDate(0, 1, 2), EnergyKWh: ptrF64(20)},
		{SessionID: 3, TeslaID: 42, SiteLocationName: "Site C", ChargeStartDateTime: start.AddDate(0, 2, 2), EnergyKWh: ptrF64(30)},
	}}
	h := newHandlerForSupercharger(reader, 42, "VIN42")
	eng := superchargerEngine(h, uid, 42, "VIN42")
	c := sessionCookie(eng, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/supercharger-stats", nil) // default window (6 months)
	if c != nil {
		req.AddCookie(c)
	}
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	body := w.Body.String()

	if !strings.Contains(body, "viewBox") {
		t.Error("fragment must contain the responsive <svg viewBox …> chart")
	}
	if !strings.Contains(body, "<title>") {
		t.Error("fragment must contain <title> tooltip elements inside the SVG bars")
	}
	if !strings.Contains(body, "btn-primary") {
		t.Error("month selector must mark the active preset with btn-primary")
	}
	// Resolved language is Spanish here: superchargerEngine never wires
	// handlers.LanguageMiddleware, so i18n.FromContext falls back to Spanish
	// (RM24-gateway-translate-all-pages). This explanation used to live on
	// sessionsTileValueRe, removed by MAG-50.
	if !strings.Contains(body, "6 meses") {
		t.Error("month selector must render the active 6-month preset label")
	}

	// MAG-50: wantRows used to be parsed out of the rendered Sessions tile, which
	// made this a tile-vs-table cross-check (D7). The tile lookup is gone, so the
	// expectation comes from the fixture above (three sessions, all attributed).
	const wantRows = 3

	tbodyIdx := strings.Index(body, "<tbody>")
	if tbodyIdx == -1 {
		t.Fatal("body missing <tbody>")
	}
	// Count the row-IDENTITY marker, not a bare "<tr>": supercharger_row.templ:35
	// renders the static row as `<tr id="supercharger-row-{vm.ID}">` (htmx needs
	// the stable id to target the outerHTML row swap), so a bare "<tr>" count went
	// stale at 0 the moment that id was added — the row markup changed, not the
	// D7 invariant it is meant to check. Coupled to that shape: if
	// supercharger_row.templ's <tr id=…> prefix ever changes, update this string
	// to match.
	//
	// Inflation risk: supercharger_row_edit.templ:41 (the edit row) and
	// supercharger_row.templ:65 (SuperchargerRowError) both reuse the SAME
	// "supercharger-row-" id prefix. Neither can appear here: this fragment is
	// SuperchargerStatsFragment -> buildSuperchargerRows -> SuperchargerRow only
	// (the STATIC row) — the edit/error variants are rendered exclusively by the
	// row-level handlers (SuperchargerRowEditFragment / SuperchargerRowUpdate),
	// never by this page/fragment handler — so the marker cannot be inflated by
	// either variant in this test.
	gotRows := strings.Count(body[tbodyIdx:], `<tr id="supercharger-row-`)
	if gotRows != wantRows {
		t.Errorf("table row count (%d) must equal the Sessions tile's own rendered count (%d) — single source of truth (D7)", gotRows, wantRows)
	}
}

// --- F.2: render test — D2/D8 unattributed sessions never appear in the rendered output ---

// TestSuperchargerStatsFragment_OtherVehicleSessionNeverRendered covers F.2:
// with a fake reader whose ListSessionsByVehicleBetween honors the real
// port's contract (a session is only returned when its TeslaID matches the
// requested filter), the rendered fragment must not show another vehicle's
// session anywhere: not in the table, not in the Sessions/Energy tiles.
func TestSuperchargerStatsFragment_OtherVehicleSessionNeverRendered(t *testing.T) {
	uid := uuid.New()
	today := startOfDay(time.Now().UTC())
	start := monthsBackFrom(today, superchargerRangeDefaultMonths)
	reader := &fakeSessionReader{sessions: []charging.Session{
		{ // another registered vehicle's session.
			SessionID:           1,
			TeslaID:             43,
			SiteLocationName:    "Other Vehicle Site",
			ChargeStartDateTime: start.AddDate(0, 0, 5),
			EnergyKWh:           ptrF64(999),
		},
		{ // attributed to the selected vehicle.
			SessionID:           2,
			TeslaID:             42,
			SiteLocationName:    "Real Site",
			ChargeStartDateTime: start.AddDate(0, 0, 6),
			EnergyKWh:           ptrF64(10),
		},
	}}
	h := newHandlerForSupercharger(reader, 42, "VIN42")
	eng := superchargerEngine(h, uid, 42, "VIN42")
	c := sessionCookie(eng, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/supercharger-stats", nil)
	if c != nil {
		req.AddCookie(c)
	}
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	body := w.Body.String()

	if strings.Contains(body, "Other Vehicle Site") {
		t.Error("unattributed session's site must never appear in the rendered table")
	}
	if !strings.Contains(body, "Real Site") {
		t.Error("the attributed session's site must appear in the rendered table")
	}

	// MAG-50: the "Sessions tile reads 1" assertion was removed with
	// sessionsTileValueRe (see its note above). D2/D8 — the unattributed session
	// never reaches the rendered output — stays covered by the Ghost Site /
	// Real Site / 10.0 kWh / no-999 assertions around this line.
	if !strings.Contains(body, "10.0 kWh") {
		t.Errorf("want Energy tile to reflect only the attributed session's 10 kWh, body:\n%s", body)
	}
	if strings.Contains(body, "999") {
		t.Error("the unattributed session's 999 kWh must not appear anywhere in the rendered output")
	}
}

// --- Wave 5 (RM31-gateway-add-session-battery-edit): SuperchargerRowUpdate /
// SuperchargerRowStatic / SuperchargerRowEditFragment tests, per design.md's
// Test Contract T1-T7. fakeSessionVerifier is a NEW test double
// (charging.SessionVerifier did not exist as a gateway dependency before this
// tier); fakeRecalculator (external_charges_test.go, same package) is reused as-is —
// its Recalculate call-recording shape already fits this tier's assertions
// without modification.

// verifySessionCall records one VerifySession invocation's arguments, so
// tests can assert both the call COUNT and the exact percentages passed
// through (design.md T1/T2/T5's "VerifySession is/is not called" assertions).
type verifySessionCall struct {
	teslaID          int64
	id               uuid.UUID
	startPct, endPct *int
}

// fakeSessionVerifier is a test double for charging.SessionVerifier
// (RM31-gateway-add-session-battery-edit). result is returned on every
// successful call; err, when set, is returned instead (and result ignored) —
// mirrors fakeChargeWriter's result/err shape.
type fakeSessionVerifier struct {
	result charging.Session
	err    error

	calls []verifySessionCall
}

// Compile-time proof fakeSessionVerifier still satisfies the real interface —
// the same "loud compile error over silent runtime gap" reasoning
// fakeRecalculator's own compile-time assertion documents (external_charges_test.go).
var _ charging.SessionVerifier = (*fakeSessionVerifier)(nil)

func (f *fakeSessionVerifier) VerifySession(_ context.Context, teslaID int64, id uuid.UUID, startBatteryPct, endBatteryPct *int) (charging.Session, error) {
	f.calls = append(f.calls, verifySessionCall{teslaID: teslaID, id: id, startPct: startBatteryPct, endPct: endBatteryPct})
	if f.err != nil {
		return charging.Session{}, f.err
	}
	return f.result, nil
}

// newHandlerForSuperchargerRow builds a Handler wired for the row-level
// Supercharger handler tests: SuperchargerReader (fetchSuperchargerRowVM's
// list-and-match resolve, D1), SuperchargerVerifier (VerifySession), and
// AnalyticsRecalculator (recalculateAfterSessionVerify) — mirrors
// newHandlerForExternalChargesWithRecalc's write-plus-recalculate wiring shape, one
// registered vehicle so resolveSelectedVehicle auto-selects it with no
// explicit session selection needed.
func newHandlerForSuperchargerRow(reader *fakeSessionReader, verifier *fakeSessionVerifier, recalc *fakeRecalculator, teslaID int64, vin string) *Handler {
	acct := &fakeAccount{
		registered: []account.Vehicle{
			{TeslaID: teslaID, VIN: vin, DisplayName: "Test Vehicle"},
		},
	}
	return New(Deps{
		Account:               acct,
		Tesla:                 &fakeTesla{},
		SuperchargerReader:    reader,
		SuperchargerVerifier:  verifier,
		AnalyticsRecalculator: recalc,
	})
}

// superchargerRowEngine builds a minimal Gin engine with session middleware
// and the three row-level Supercharger routes (SuperchargerRowStatic,
// SuperchargerRowEditFragment, SuperchargerRowUpdate). issueCSRF is a BOOL,
// not a string, because T6 needs a session where csrf_supercharger was NEVER
// set at all — distinct from an issued-but-empty value — so the /_session
// helper must be able to skip the sess.Set call entirely, not just set "".
func superchargerRowEngine(h *Handler, uid uuid.UUID, csrfToken string, issueCSRF bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	store := cookie.NewStore([]byte("test-secret"))
	r.Use(sessions.Sessions("test", store))
	r.GET("/_session", func(c *gin.Context) {
		sess := sessions.Default(c)
		sess.Set("uid", uid.String())
		if issueCSRF {
			sess.Set(csrfSuperchargerKey, csrfToken)
		}
		_ = sess.Save()
		c.String(http.StatusOK, "ok")
	})
	r.GET("/ui/supercharger-stats/row/:id", h.SuperchargerRowStatic)
	r.GET("/ui/supercharger-stats/row/:id/edit", h.SuperchargerRowEditFragment)
	r.PATCH("/ui/supercharger-stats/row/:id", h.SuperchargerRowUpdate)
	return r
}

// --- T1 ---

// TestSuperchargerRowUpdate_AbsentKeyIs400 is Test Contract T1: a PATCH body
// containing only start_battery_pct (end_battery_pct key entirely absent)
// gets HTTP 400 and calls VerifySession zero times.
func TestSuperchargerRowUpdate_AbsentKeyIs400(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	verifier := &fakeSessionVerifier{}
	reader := &fakeSessionReader{}
	recalc := &fakeRecalculator{}
	h := newHandlerForSuperchargerRow(reader, verifier, recalc, 42, "VIN42")
	r := superchargerRowEngine(h, uid, "tok", true)
	c := sessionCookie(r, uid, "tok")

	form := url.Values{
		"csrf_token":        {"tok"},
		"start_battery_pct": {"50"},
		// end_battery_pct deliberately absent — not even an empty value.
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/ui/supercharger-stats/row/"+id.String(), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 when end_battery_pct key is absent, got %d body=%q", w.Code, w.Body.String())
	}
	if len(verifier.calls) != 0 {
		t.Errorf("want zero VerifySession calls on a malformed body, got %d", len(verifier.calls))
	}
}

// --- T2 ---

// TestSuperchargerRowUpdate_BothEmptyClearsBothPercentages is Test Contract
// T2: both keys present with empty values calls VerifySession(nil, nil)
// exactly once; on the fake returning a session with both percentages (and
// BatteryPctSource) nil, the response is 200 rendering the static row with
// both battery cells as "—". EnergyKWh/TotalCost/Currency fields are
// all set to non-nil fixture values so the em-dash count is attributable
// ONLY to the two cleared percentages, not incidental zero-values.
func TestSuperchargerRowUpdate_BothEmptyClearsBothPercentages(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	verifier := &fakeSessionVerifier{result: charging.Session{
		ID:                  id,
		TeslaID:             42,
		SiteLocationName:    "Clear Site",
		ChargeStartDateTime: time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC),
		ChargeStopDateTime:  time.Date(2026, 6, 1, 11, 0, 0, 0, time.UTC),
		EnergyKWh:           ptrF64(20),
		TotalCost:           ptrF64(50),
		Currency:            ptrStr("USD"),
		StartBatteryPct:     nil,
		EndBatteryPct:       nil,
		BatteryPctSource:    nil,
	}}
	reader := &fakeSessionReader{}
	recalc := &fakeRecalculator{}
	h := newHandlerForSuperchargerRow(reader, verifier, recalc, 42, "VIN42")
	r := superchargerRowEngine(h, uid, "tok", true)
	c := sessionCookie(r, uid, "tok")

	form := url.Values{
		"csrf_token":        {"tok"},
		"start_battery_pct": {""},
		"end_battery_pct":   {""},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/ui/supercharger-stats/row/"+id.String(), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 for a clearing update, got %d body=%q", w.Code, w.Body.String())
	}
	if len(verifier.calls) != 1 {
		t.Fatalf("want exactly 1 VerifySession call, got %d", len(verifier.calls))
	}
	if call := verifier.calls[0]; call.startPct != nil || call.endPct != nil {
		t.Errorf("want VerifySession called with (nil, nil), got (%v, %v)", call.startPct, call.endPct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Clear Site") {
		t.Fatalf("want the static row rendered, body=%q", body)
	}
	if got := strings.Count(body, "—"); got != 2 {
		t.Errorf("want exactly 2 em dashes (the cleared start/end battery cells), got %d, body=%q", got, body)
	}
}

// TestSuperchargerRowUpdate_DecreasingOrderAccepted covers the accepted-
// decreasing-order case (design.md D7b — end_battery_pct < start_battery_pct
// is NOT rejected; there is deliberately no ordering validation).
func TestSuperchargerRowUpdate_DecreasingOrderAccepted(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	verifier := &fakeSessionVerifier{result: charging.Session{
		ID:                  id,
		TeslaID:             42,
		ChargeStartDateTime: time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC),
		ChargeStopDateTime:  time.Date(2026, 6, 1, 11, 0, 0, 0, time.UTC),
		StartBatteryPct:     ptrInt(80),
		EndBatteryPct:       ptrInt(20),
	}}
	reader := &fakeSessionReader{}
	recalc := &fakeRecalculator{}
	h := newHandlerForSuperchargerRow(reader, verifier, recalc, 42, "VIN42")
	r := superchargerRowEngine(h, uid, "tok", true)
	c := sessionCookie(r, uid, "tok")

	form := url.Values{
		"csrf_token":        {"tok"},
		"start_battery_pct": {"80"},
		"end_battery_pct":   {"20"}, // end < start
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/ui/supercharger-stats/row/"+id.String(), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 for a decreasing-order update (no ordering validation, D7b), got %d body=%q", w.Code, w.Body.String())
	}
	if len(verifier.calls) != 1 {
		t.Fatalf("want exactly 1 VerifySession call, got %d", len(verifier.calls))
	}
	call := verifier.calls[0]
	if call.startPct == nil || *call.startPct != 80 || call.endPct == nil || *call.endPct != 20 {
		t.Errorf("want VerifySession called with (80, 20), got (%v, %v)", call.startPct, call.endPct)
	}
}

// --- T3 ---

// TestSuperchargerRowUpdate_RecalculateWindowFromChargeStopDateTime is Test
// Contract T3: a verified session whose ChargeStopDateTime is
// 2026-03-15T00:20:00Z (20 minutes after UTC midnight) triggers
// Recalculate(2026-03-14T00:00:00Z, 2026-03-16T00:00:00Z) — computed from
// ChargeStopDateTime ALONE, ignoring ChargeStartDateTime, which the fixture
// deliberately sets to a DIFFERENT calendar day (2026-03-14).
func TestSuperchargerRowUpdate_RecalculateWindowFromChargeStopDateTime(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	stop := time.Date(2026, 3, 15, 0, 20, 0, 0, time.UTC)
	start := time.Date(2026, 3, 14, 23, 0, 0, 0, time.UTC) // different calendar day than stop
	verifier := &fakeSessionVerifier{result: charging.Session{
		ID:                  id,
		TeslaID:             42,
		ChargeStartDateTime: start,
		ChargeStopDateTime:  stop,
		StartBatteryPct:     ptrInt(40),
		EndBatteryPct:       ptrInt(80),
	}}
	reader := &fakeSessionReader{}
	recalc := &fakeRecalculator{}
	h := newHandlerForSuperchargerRow(reader, verifier, recalc, 42, "VIN42")
	r := superchargerRowEngine(h, uid, "tok", true)
	c := sessionCookie(r, uid, "tok")

	form := url.Values{
		"csrf_token":        {"tok"},
		"start_battery_pct": {"40"},
		"end_battery_pct":   {"80"},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/ui/supercharger-stats/row/"+id.String(), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%q", w.Code, w.Body.String())
	}
	if len(recalc.calls) != 1 {
		t.Fatalf("want exactly 1 Recalculate call, got %d", len(recalc.calls))
	}
	call := recalc.calls[0]
	wantFrom := time.Date(2026, 3, 14, 0, 0, 0, 0, time.UTC)
	wantTo := time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC)
	if !call.start.Equal(wantFrom) || !call.end.Equal(wantTo) {
		t.Errorf("want Recalculate(%v, %v) — day-1..day+1 from ChargeStopDateTime alone, got (%v, %v)", wantFrom, wantTo, call.start, call.end)
	}
	if call.teslaID != 42 {
		t.Errorf("want teslaID=42, got %d", call.teslaID)
	}
}

// --- T5 ---

// TestSuperchargerRowUpdate_OutOfRangeFieldErrorIs422 is Test Contract T5: an
// out-of-range start_battery_pct (101) with a valid end_battery_pct (50)
// gets HTTP 422, HX-Error-Fragment: true, a FIELD-level error (not a
// top-of-form alert), the submitted raw values echoed back verbatim, and
// zero VerifySession calls.
func TestSuperchargerRowUpdate_OutOfRangeFieldErrorIs422(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	verifier := &fakeSessionVerifier{}
	reader := &fakeSessionReader{}
	recalc := &fakeRecalculator{}
	h := newHandlerForSuperchargerRow(reader, verifier, recalc, 42, "VIN42")
	r := superchargerRowEngine(h, uid, "tok", true)
	c := sessionCookie(r, uid, "tok")

	form := url.Values{
		"csrf_token":        {"tok"},
		"start_battery_pct": {"101"},
		"end_battery_pct":   {"50"},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/ui/supercharger-stats/row/"+id.String(), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 for an out-of-range field, got %d body=%q", w.Code, w.Body.String())
	}
	if got := w.Header().Get("HX-Error-Fragment"); got != "true" {
		t.Errorf("HX-Error-Fragment = %q, want \"true\" — htmx would otherwise discard this 422 body", got)
	}
	body := w.Body.String()
	// ES catalogue value — superchargerRowEngine wires no LanguageMiddleware, so
	// i18n.FromContext falls back to Spanish (mirrors this file's existing
	// resolved-language assertions, e.g. TestSuperchargerStatsFragment_RendersBatteryValues).
	if !strings.Contains(body, "El porcentaje de batería inicial debe ser un número entero entre 0 y 100.") {
		t.Errorf("want the start-range field error message rendered, body=%q", body)
	}
	if strings.Contains(body, "El porcentaje de batería final debe ser un número entero entre 0 y 100.") {
		t.Errorf("end_battery_pct (50) is valid — its error message must not render, body=%q", body)
	}
	if !strings.Contains(body, `value="101"`) {
		t.Errorf("want the submitted raw start value 101 echoed back (not reset), body=%q", body)
	}
	if !strings.Contains(body, `value="50"`) {
		t.Errorf("want the submitted raw end value 50 echoed back, body=%q", body)
	}
	if len(verifier.calls) != 0 {
		t.Errorf("want zero VerifySession calls on a validation failure, got %d", len(verifier.calls))
	}
}

// --- T6 ---

// TestSuperchargerRowUpdate_NoTokenEverIssuedIs403 is Test Contract T6: a
// session that never had csrf_supercharger set at all (a client that reached
// PATCH without first loading GET /supercharger-stats or
// GET /ui/supercharger-stats in this session) gets HTTP 403 via the
// existing fail-closed checkCSRFKey path, and zero VerifySession calls.
func TestSuperchargerRowUpdate_NoTokenEverIssuedIs403(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	verifier := &fakeSessionVerifier{}
	reader := &fakeSessionReader{}
	recalc := &fakeRecalculator{}
	h := newHandlerForSuperchargerRow(reader, verifier, recalc, 42, "VIN42")
	r := superchargerRowEngine(h, uid, "", false) // issueCSRF=false — key never set
	c := sessionCookie(r, uid, "")

	form := url.Values{
		"csrf_token":        {"whatever"},
		"start_battery_pct": {"50"},
		"end_battery_pct":   {"80"},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/ui/supercharger-stats/row/"+id.String(), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 when no csrf_supercharger token was ever issued, got %d", w.Code)
	}
	if len(verifier.calls) != 0 {
		t.Errorf("want zero VerifySession calls on CSRF rejection, got %d", len(verifier.calls))
	}
}

// TestSuperchargerRowUpdate_StaleTokenRejected covers the SAME 403 outcome
// as T6 above, via a DISTINCT fixture: a token WAS issued ("freshtoken"),
// unlike T6's never-issued session, but the submitted token is stale/
// mismatched ("staletoken"). Both paths reach checkCSRFKey's fail-closed
// compare, but from different starting session states.
func TestSuperchargerRowUpdate_StaleTokenRejected(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	verifier := &fakeSessionVerifier{}
	reader := &fakeSessionReader{}
	recalc := &fakeRecalculator{}
	h := newHandlerForSuperchargerRow(reader, verifier, recalc, 42, "VIN42")
	r := superchargerRowEngine(h, uid, "freshtoken", true)
	c := sessionCookie(r, uid, "freshtoken")

	form := url.Values{
		"csrf_token":        {"staletoken"},
		"start_battery_pct": {"50"},
		"end_battery_pct":   {"80"},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/ui/supercharger-stats/row/"+id.String(), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 for a stale/mismatched csrf token, got %d", w.Code)
	}
	if len(verifier.calls) != 0 {
		t.Errorf("want zero VerifySession calls on CSRF rejection, got %d", len(verifier.calls))
	}
}

// --- T7 ---

// TestSuperchargerRowEditFragment_NotInWindowIs404 is Test Contract T7: a
// GET .../row/:id/edit request whose ?start=&end= window contains sessions
// but none matching id resolves as 404 (D1's inherited failure mode).
func TestSuperchargerRowEditFragment_NotInWindowIs404(t *testing.T) {
	uid := uuid.New()
	id := uuid.New() // no fixture session carries this id
	reader := &fakeSessionReader{sessions: []charging.Session{
		{
			ID:                  uuid.New(),
			TeslaID:             42,
			SiteLocationName:    "Other Session",
			ChargeStartDateTime: time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC),
			ChargeStopDateTime:  time.Date(2026, 6, 1, 11, 0, 0, 0, time.UTC),
		},
	}}
	verifier := &fakeSessionVerifier{}
	recalc := &fakeRecalculator{}
	h := newHandlerForSuperchargerRow(reader, verifier, recalc, 42, "VIN42")
	r := superchargerRowEngine(h, uid, "tok", true)
	c := sessionCookie(r, uid, "tok")

	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, superchargerRangeURL("/ui/supercharger-stats/row/"+id.String()+"/edit", start, end), nil)
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404 when id is not in the resolved window, got %d body=%q", w.Code, w.Body.String())
	}
}
