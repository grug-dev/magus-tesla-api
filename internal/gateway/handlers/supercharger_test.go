package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
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

// --- fakes for the Supercharger Stats handler tests ---

// fakeSuperchargerReader is a test double for telemetry.SuperchargerReader.
// SuperchargerSessionsByVehicle mirrors the REAL port's contract: it only
// ever returns sessions whose TeslaID matches the requested filter, so a
// session with a nil TeslaID (D2 — unattributed, VIN doesn't match any
// registered vehicle) can never be returned to any teslaID filter, exactly
// like the real SQL WHERE tesla_id = $2 clause would exclude a NULL column.
type fakeSuperchargerReader struct {
	sessions []telemetry.SuperchargerSession
	err      error

	capturedAccountID uuid.UUID
	capturedFilterID  int64
	capturedLimit     int
}

func (f *fakeSuperchargerReader) SuperchargerSessionsByAccount(_ context.Context, _ uuid.UUID, _ int) ([]telemetry.SuperchargerSession, error) {
	return f.sessions, f.err
}

func (f *fakeSuperchargerReader) SuperchargerSessionsByVehicle(_ context.Context, accountID uuid.UUID, teslaID int64, limit int) ([]telemetry.SuperchargerSession, error) {
	f.capturedAccountID = accountID
	f.capturedFilterID = teslaID
	f.capturedLimit = limit
	if f.err != nil {
		return nil, f.err
	}
	out := make([]telemetry.SuperchargerSession, 0, len(f.sessions))
	for _, s := range f.sessions {
		if s.TeslaID != nil && *s.TeslaID == teslaID {
			out = append(out, s)
		}
	}
	return out, nil
}

// errTestSupercharger is a sentinel error for Supercharger Stats reader tests.
var errTestSupercharger = errors.New("test supercharger reader error")

// newHandlerForSupercharger builds a Handler with the given fake reader and one
// registered vehicle. Mirrors newHandlerForHistory.
func newHandlerForSupercharger(reader *fakeSuperchargerReader, teslaID int64, vin string) *Handler {
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

// ptrF64 / ptrInt64 are small pointer helpers for building nullable session
// fields. ptrStr already exists in charges_test.go — reused here.
func ptrF64(f float64) *float64 { return &f }
func ptrInt64(i int64) *int64   { return &i }

// --- clampSuperchargerMonths unit tests (pure function) ---

func TestClampSuperchargerMonths_Presets(t *testing.T) {
	tests := []struct {
		raw  string
		want int
	}{
		{"3", 3},
		{"6", 6},
		{"12", 12},
		{"", defaultSuperchargerMonths},
		{"abc", defaultSuperchargerMonths},
		{"0", defaultSuperchargerMonths},
		{"-3", defaultSuperchargerMonths},
		{"9", defaultSuperchargerMonths},
		{"100", defaultSuperchargerMonths},
		{"6.0", defaultSuperchargerMonths},
	}
	for _, tc := range tests {
		got := clampSuperchargerMonths(tc.raw)
		if got != tc.want {
			t.Errorf("clampSuperchargerMonths(%q): want %d, got %d", tc.raw, tc.want, got)
		}
	}
}

// --- buildSuperchargerStatsView unit tests ---

func TestBuildSuperchargerStatsView_CorrectSincePassedToReader(t *testing.T) {
	reader := &fakeSuperchargerReader{}
	h := newHandlerForSupercharger(reader, 42, "VIN42")
	since := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	_ = h.buildSuperchargerStatsView(context.Background(), uuid.New(), 42, 6, since)
	if reader.capturedFilterID != 42 {
		t.Errorf("want teslaID=42 passed to reader, got %d", reader.capturedFilterID)
	}
	if reader.capturedLimit != superchargerReadLimit {
		t.Errorf("want limit=%d passed to reader, got %d", superchargerReadLimit, reader.capturedLimit)
	}
}

func TestBuildSuperchargerStatsView_ReaderErrorDegradesEmpty(t *testing.T) {
	reader := &fakeSuperchargerReader{err: errTestSupercharger}
	h := newHandlerForSupercharger(reader, 42, "VIN42")
	since := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	v := h.buildSuperchargerStatsView(context.Background(), uuid.New(), 42, 6, since)
	if !v.Empty {
		t.Error("want v.Empty=true on reader error")
	}
	if !v.Chart.Empty {
		t.Error("want v.Chart.Empty=true on reader error")
	}
}

func TestBuildSuperchargerStatsView_EmptyWhenZeroSessionsInWindow(t *testing.T) {
	// Session exists but predates the window.
	reader := &fakeSuperchargerReader{sessions: []telemetry.SuperchargerSession{
		{
			TeslaID:             ptrInt64(42),
			ChargeStartDateTime: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	}}
	h := newHandlerForSupercharger(reader, 42, "VIN42")
	since := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	v := h.buildSuperchargerStatsView(context.Background(), uuid.New(), 42, 6, since)
	if !v.Empty {
		t.Error("want v.Empty=true when the only session predates the window")
	}
	if len(v.Sessions) != 0 {
		t.Errorf("want 0 rows, got %d", len(v.Sessions))
	}
}

// TestBuildSuperchargerStatsView_UnattributedSessionsNeverAppear covers D2: a
// session with TeslaID == nil (VIN doesn't match any registered vehicle) must
// never appear in the tiles/table, and the fake's SuperchargerSessionsByVehicle
// enforces that by never returning it to ANY teslaID filter — mirroring the
// real port's WHERE tesla_id = $2 contract. Only one read is made (no second,
// account-wide read to discover it).
func TestBuildSuperchargerStatsView_UnattributedSessionsNeverAppear(t *testing.T) {
	since := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	reader := &fakeSuperchargerReader{sessions: []telemetry.SuperchargerSession{
		{ // unattributed — TeslaID nil, VIN not matched to a registered vehicle
			SessionID:           1,
			TeslaID:             nil,
			SiteLocationName:    "Ghost Site",
			ChargeStartDateTime: since.AddDate(0, 0, 5),
			EnergyKWh:           ptrF64(50),
		},
		{ // attributed to the selected vehicle
			SessionID:           2,
			TeslaID:             ptrInt64(42),
			SiteLocationName:    "Real Site",
			ChargeStartDateTime: since.AddDate(0, 0, 6),
			EnergyKWh:           ptrF64(10),
		},
	}}
	h := newHandlerForSupercharger(reader, 42, "VIN42")
	v := h.buildSuperchargerStatsView(context.Background(), uuid.New(), 42, 6, since)

	if v.Empty {
		t.Fatal("want a non-empty view — the attributed session is in the window")
	}
	if len(v.Sessions) != 1 {
		t.Fatalf("want exactly 1 row (unattributed session excluded), got %d", len(v.Sessions))
	}
	if v.Sessions[0].SiteLabel != "Real Site" {
		t.Errorf("want only the attributed session's site, got %q", v.Sessions[0].SiteLabel)
	}
	if v.Tiles.Sessions != "1" {
		t.Errorf("want Sessions tile = 1 (unattributed excluded), got %q", v.Tiles.Sessions)
	}
	if v.Tiles.Energy != "10.0 kWh" {
		t.Errorf("want Energy tile = 10.0 kWh (unattributed's 50 kWh excluded), got %q", v.Tiles.Energy)
	}
}

// --- buildSuperchargerTiles unit tests (D7 + D4) ---

func TestBuildSuperchargerTiles_SessionsEnergyAvg(t *testing.T) {
	sessions := []telemetry.SuperchargerSession{
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
	sessions := []telemetry.SuperchargerSession{
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
	sessions := []telemetry.SuperchargerSession{
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
	sessions := []telemetry.SuperchargerSession{
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

// --- buildSuperchargerChart unit tests ---

func TestBuildSuperchargerChart_EmptyWhenFilteredEmpty(t *testing.T) {
	since := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	c := buildSuperchargerChart(nil, 6, since)
	if !c.Empty {
		t.Error("want Empty=true for an empty filtered slice")
	}
}

func TestBuildSuperchargerChart_OneBarPerMonthInWindow(t *testing.T) {
	since := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	sessions := []telemetry.SuperchargerSession{
		{ChargeStartDateTime: since.AddDate(0, 0, 2), EnergyKWh: ptrF64(10)}, // month 0 (Mar)
		{ChargeStartDateTime: since.AddDate(0, 2, 2), EnergyKWh: ptrF64(30)}, // month 2 (May) — tallest
	}
	c := buildSuperchargerChart(sessions, 3, since)
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
}

func TestBuildSuperchargerChart_TooltipContainsMonthAndKWh(t *testing.T) {
	since := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	sessions := []telemetry.SuperchargerSession{
		{ChargeStartDateTime: since.AddDate(0, 0, 2), EnergyKWh: ptrF64(12.3)},
	}
	c := buildSuperchargerChart(sessions, 1, since)
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

// --- buildSuperchargerRows unit tests ---

func TestBuildSuperchargerRows_NilEnergyAndCostRenderDash(t *testing.T) {
	sessions := []telemetry.SuperchargerSession{
		{
			SiteLocationName: "Site A",
			CountryCode:      "US",
			BillingType:      "per_kwh",
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
}

func TestBuildSuperchargerRows_PopulatedFields(t *testing.T) {
	sessions := []telemetry.SuperchargerSession{
		{
			SiteLocationName:    "Downtown Supercharger",
			CountryCode:         "MX",
			BillingType:         "per_kwh",
			ChargeStartDateTime: time.Date(2026, 7, 4, 10, 0, 0, 0, time.UTC),
			EnergyKWh:           ptrF64(23.456),
			TotalCost:           ptrF64(99.9),
			Currency:            ptrStr("MXN"),
		},
	}
	rows := buildSuperchargerRows(sessions)
	if rows[0].SiteLabel != "Downtown Supercharger" {
		t.Errorf("want SiteLabel, got %q", rows[0].SiteLabel)
	}
	if rows[0].CountryCode != "MX" {
		t.Errorf("want CountryCode=MX, got %q", rows[0].CountryCode)
	}
	if rows[0].EnergyLabel != "23.46 kWh" {
		t.Errorf("want EnergyLabel=23.46 kWh, got %q", rows[0].EnergyLabel)
	}
	if rows[0].CostLabel != "99.90 MXN" {
		t.Errorf("want CostLabel=99.90 MXN, got %q", rows[0].CostLabel)
	}
	if rows[0].BillingType != "per_kwh" {
		t.Errorf("want BillingType=per_kwh, got %q", rows[0].BillingType)
	}
}

// TestBuildSuperchargerRows_CostLabelCommaGrouped covers MAG-9: the session
// CostLabel field (distinct from the CostLines tile field already covered by
// TestBuildSuperchargerTiles_MultiCurrencyNeverSummed) must go through
// formatMoney and comma-group amounts >= 1000.
func TestBuildSuperchargerRows_CostLabelCommaGrouped(t *testing.T) {
	sessions := []telemetry.SuperchargerSession{
		{
			SiteLocationName: "Big Session",
			CountryCode:      "CO",
			BillingType:      "per_kwh",
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
	reader := &fakeSuperchargerReader{}
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
	reader := &fakeSuperchargerReader{}
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
	reader := &fakeSuperchargerReader{}
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
	reader := &fakeSuperchargerReader{err: errTestSupercharger}
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

func TestSuperchargerStatsFragment_DefaultMonthsIsSix(t *testing.T) {
	uid := uuid.New()
	reader := &fakeSuperchargerReader{}
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
	// Resolved language is Spanish here (KeySuperchargerMonthsPreset's ES value "%d meses").
	if !strings.Contains(w.Body.String(), "6 meses") {
		t.Errorf("want default months=6 reflected in the rendered selector, body:\n%s", w.Body.String())
	}
}

func TestSuperchargerStatsFragment_ValidPresetsAccepted(t *testing.T) {
	uid := uuid.New()
	reader := &fakeSuperchargerReader{}
	h := newHandlerForSupercharger(reader, 42, "VIN42")
	eng := superchargerEngine(h, uid, 42, "VIN42")
	c := sessionCookie(eng, uid, "")

	for _, p := range superchargerMonthPresets {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/ui/supercharger-stats?months=%d", p), nil)
		if c != nil {
			req.AddCookie(c)
		}
		eng.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("months=%d: want 200, got %d", p, w.Code)
		}
	}
}

// --- F.1: render test — SVG/title/active-preset + table/tile single-source-of-truth (D7) ---

// sessionsTileValueRe extracts the rendered Sessions ui.StatTile value, e.g.
// `stat-title">Sesiones</div><div class="stat-value">3</div>` -> "3". Templ
// emits no whitespace between adjacent tags (confirmed in
// templates/ui/stat_tile_templ.go), so the pattern matches the compact output
// verbatim. The label is "Sesiones" (KeySuperchargerSessions' ES value) because
// superchargerEngine never wires handlers.LanguageMiddleware, so i18n.FromContext
// falls back to Spanish (RM24-gateway-translate-all-pages, mirroring tier 2's
// T6.4 precedent — was "Sessions" before this tier translated the tile label).
var sessionsTileValueRe = regexp.MustCompile(`stat-title">Sesiones</div><div class="stat-value">(\d+)</div>`)

// TestSuperchargerStatsFragment_ChartAndSelectorAndTableMatchesSessionsTile
// covers F.1: the fragment contains the responsive <svg viewBox …> chart with
// <title> tooltips, the month selector marks the active preset, and — the D7
// single-source-of-truth check — the number of <tr> rows inside the sessions
// table's <tbody> equals the Sessions tile's own rendered value, both parsed
// independently out of the rendered HTML (not out of the test fixture).
func TestSuperchargerStatsFragment_ChartAndSelectorAndTableMatchesSessionsTile(t *testing.T) {
	uid := uuid.New()
	// Anchor to the SAME window-start formula the handler itself computes for
	// months=6 (startOfMonth(now).AddDate(0, -months+1, 0)) so the fixture
	// dates land inside the live window regardless of what "now" is when the
	// test runs.
	since := startOfMonth(time.Now()).AddDate(0, -6+1, 0)
	reader := &fakeSuperchargerReader{sessions: []telemetry.SuperchargerSession{
		{SessionID: 1, TeslaID: ptrInt64(42), SiteLocationName: "Site A", ChargeStartDateTime: since.AddDate(0, 0, 2), EnergyKWh: ptrF64(10)},
		{SessionID: 2, TeslaID: ptrInt64(42), SiteLocationName: "Site B", ChargeStartDateTime: since.AddDate(0, 1, 2), EnergyKWh: ptrF64(20)},
		{SessionID: 3, TeslaID: ptrInt64(42), SiteLocationName: "Site C", ChargeStartDateTime: since.AddDate(0, 2, 2), EnergyKWh: ptrF64(30)},
	}}
	h := newHandlerForSupercharger(reader, 42, "VIN42")
	eng := superchargerEngine(h, uid, 42, "VIN42")
	c := sessionCookie(eng, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/supercharger-stats?months=6", nil)
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
	// Resolved language is Spanish here (see sessionsTileValueRe's comment above).
	if !strings.Contains(body, "6 meses") {
		t.Error("month selector must render the active 6-month preset label")
	}

	m := sessionsTileValueRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("could not find the rendered Sessions tile value in body:\n%s", body)
	}
	wantRows, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("Sessions tile value %q is not an int: %v", m[1], err)
	}
	if wantRows != 3 {
		t.Fatalf("want Sessions tile = 3 for this fixture, got %d", wantRows)
	}

	tbodyIdx := strings.Index(body, "<tbody>")
	if tbodyIdx == -1 {
		t.Fatal("body missing <tbody>")
	}
	gotRows := strings.Count(body[tbodyIdx:], "<tr>")
	if gotRows != wantRows {
		t.Errorf("table row count (%d) must equal the Sessions tile's own rendered count (%d) — single source of truth (D7)", gotRows, wantRows)
	}
}

// --- F.2: render test — D2 unattributed sessions never appear in the rendered output ---

// TestSuperchargerStatsFragment_UnattributedSessionNeverRendered covers F.2:
// with a fake reader whose SuperchargerSessionsByVehicle honors the real
// port's contract (a session is only returned when its TeslaID matches the
// requested filter — an unattributed, TeslaID == nil session is simply never
// returned to any filter), the rendered fragment must not show that session
// anywhere: not in the table, not in the Sessions/Energy tiles.
func TestSuperchargerStatsFragment_UnattributedSessionNeverRendered(t *testing.T) {
	uid := uuid.New()
	// Same window-anchor rationale as the F.1 render test above.
	since := startOfMonth(time.Now()).AddDate(0, -6+1, 0)
	reader := &fakeSuperchargerReader{sessions: []telemetry.SuperchargerSession{
		{ // unattributed — TeslaID nil, VIN not matched to a registered vehicle.
			SessionID:           1,
			TeslaID:             nil,
			SiteLocationName:    "Ghost Site",
			ChargeStartDateTime: since.AddDate(0, 0, 5),
			EnergyKWh:           ptrF64(999),
		},
		{ // attributed to the selected vehicle.
			SessionID:           2,
			TeslaID:             ptrInt64(42),
			SiteLocationName:    "Real Site",
			ChargeStartDateTime: since.AddDate(0, 0, 6),
			EnergyKWh:           ptrF64(10),
		},
	}}
	h := newHandlerForSupercharger(reader, 42, "VIN42")
	eng := superchargerEngine(h, uid, 42, "VIN42")
	c := sessionCookie(eng, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/supercharger-stats?months=6", nil)
	if c != nil {
		req.AddCookie(c)
	}
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	body := w.Body.String()

	if strings.Contains(body, "Ghost Site") {
		t.Error("unattributed session's site must never appear in the rendered table")
	}
	if !strings.Contains(body, "Real Site") {
		t.Error("the attributed session's site must appear in the rendered table")
	}

	m := sessionsTileValueRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("could not find the rendered Sessions tile value in body:\n%s", body)
	}
	if m[1] != "1" {
		t.Errorf("want Sessions tile = 1 (unattributed session excluded), got %q", m[1])
	}
	if !strings.Contains(body, "10.0 kWh") {
		t.Errorf("want Energy tile to reflect only the attributed session's 10 kWh, body:\n%s", body)
	}
	if strings.Contains(body, "999") {
		t.Error("the unattributed session's 999 kWh must not appear anywhere in the rendered output")
	}
}
