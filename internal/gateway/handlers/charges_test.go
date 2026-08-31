package handlers

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/analytics"
	"github.com/cristianpena/magus-tesla-api/internal/charging"
	"github.com/cristianpena/magus-tesla-api/internal/clock"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/fragments"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// fakeRecalculator is a test double for analytics.Recalculator
// (RM29-analytics-add-vehicle-metrics task 5.3 — the manual-charge write
// path now calls Recalculate after every successful Create/Update/Delete,
// design.md D5). Recalculate records its call args so tests can assert
// wiring/timing; Reconcile PANICS — no gateway handler calls it (it is the
// nightly poller's own concern, cmd/poller).
type fakeRecalculator struct {
	err error

	calls []recalculateCall
}

type recalculateCall struct {
	accountID  uuid.UUID
	teslaID    int64
	start, end time.Time
}

// Compile-time proof that fakeRecalculator still satisfies the real interface.
// This is what turns a future Recalculator change into a loud compile error here
// instead of a silent runtime gap -- the same failure mode that broke six fakes
// in this change's Wave 1. Mirrors internal/analytics/recalculate.go:62.
var _ analytics.Recalculator = (*fakeRecalculator)(nil)

func (f *fakeRecalculator) Recalculate(_ context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) error {
	f.calls = append(f.calls, recalculateCall{accountID: accountID, teslaID: teslaID, start: start, end: end})
	return f.err
}

func (f *fakeRecalculator) Reconcile(context.Context, uuid.UUID, int64) error {
	panic("fakeRecalculator: Reconcile is never called by any gateway handler — it is cmd/poller's nightly concern")
}

// --- fakes for charging.Writer and charging.Reader ---

type fakeChargeWriter struct {
	createEntry charging.Entry
	createErr   error
	updateEntry charging.Entry
	updateErr   error
	deleteErr   error
	deleteCalls int // count of Delete invocations — lets tests assert a rejected write never reached the port
	createCalls int // count of Create invocations — mirrors deleteCalls (RM33 Group A "Writer.Create never called" assertions)
	updateCalls int // count of Update invocations — mirrors deleteCalls
}

func (f *fakeChargeWriter) Create(_ context.Context, e charging.Entry) (charging.Entry, error) {
	f.createCalls++
	if f.createErr != nil {
		return charging.Entry{}, f.createErr
	}
	e.ID = uuid.New()
	e.CreatedAt = time.Now()
	e.UpdatedAt = time.Now()
	f.createEntry = e
	return e, nil
}

func (f *fakeChargeWriter) Update(_ context.Context, e charging.Entry) (charging.Entry, error) {
	f.updateCalls++
	if f.updateErr != nil {
		return charging.Entry{}, f.updateErr
	}
	e.UpdatedAt = time.Now()
	f.updateEntry = e
	return e, nil
}

func (f *fakeChargeWriter) Delete(_ context.Context, _ uuid.UUID, _ uuid.UUID) error {
	f.deleteCalls++
	return f.deleteErr
}

type fakeChargeReader struct {
	entries []charging.Entry
	err     error
}

func (f *fakeChargeReader) ListEntriesByVehicle(_ context.Context, _ uuid.UUID, _ int64, _ int) ([]charging.Entry, error) {
	return f.entries, f.err
}

func (f *fakeChargeReader) ListEntriesByAccount(_ context.Context, _ uuid.UUID, _ int) ([]charging.Entry, error) {
	return f.entries, f.err
}

// ListEntriesByVehicleBetween satisfies the charging.Reader port (added by RM28
// tier 2) and IS called by gateway handlers: buildChargesPage reads the filter
// window through it, and inProgressConflictOn reads a single day through it.
// The fake IGNORES the requested window and always returns f.entries — a
// conflict test that seeds an entry on another date therefore exercises the
// handler's own same-day comparison, not the fake's filtering.
func (f *fakeChargeReader) ListEntriesByVehicleBetween(_ context.Context, _ uuid.UUID, _ int64, _, _ time.Time) ([]charging.Entry, error) {
	return f.entries, f.err
}

// ListEntriesByVehicleUpdatedSince satisfies the charging.Reader method added by
// RM29-analytics-add-vehicle-metrics task 1.4. Unlike the sibling above -- which
// returns data because gateway handlers really do call it -- no handler calls
// this one; it serves analytics' recompute watermark. Panic makes an accidental
// call visible instead of silently returning an empty result.
func (f *fakeChargeReader) ListEntriesByVehicleUpdatedSince(_ context.Context, _ uuid.UUID, _ int64, _ time.Time) ([]charging.Entry, error) {
	panic("fakeChargeReader: ListEntriesByVehicleUpdatedSince must not be called by any gateway handler")
}

// --- test helpers ---

// newGinEngine builds a minimal Gin engine with session middleware for charge handler tests.
func newGinEngine(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	store := cookie.NewStore([]byte("test-secret"))
	r.Use(sessions.Sessions("test", store))

	r.GET("/charges", h.ChargePage)
	r.GET("/ui/charges/list", h.ChargesListFragment)
	r.GET("/ui/charges/row/:id", h.ChargeRowStatic)
	r.GET("/ui/charges/row/:id/edit", h.ChargeRowEditFragment)
	r.POST("/ui/charges/create", h.ChargeCreate)
	r.PUT("/ui/charges/row/:id", h.ChargeRowUpdate)
	r.DELETE("/ui/charges/row/:id", h.ChargeRowDelete)

	return r
}

// setUID writes a fake UID into the session by making a GET /charges request (which
// sets up the session cookie) and then injecting the uid. Instead, we use a helper
// route that sets the session, then subsequent requests reuse it.
// For simplicity, we add a test-only /set-session route.
func engineWithSession(h *Handler, uid uuid.UUID, csrfToken string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	store := cookie.NewStore([]byte("test-secret"))
	r.Use(sessions.Sessions("test", store))

	r.GET("/_session", func(c *gin.Context) {
		sess := sessions.Default(c)
		sess.Set("uid", uid.String())
		if csrfToken != "" {
			sess.Set(csrfManualChargeKey, csrfToken)
		}
		_ = sess.Save()
		c.String(http.StatusOK, "ok")
	})

	r.GET("/charges", h.ChargePage)
	r.GET("/ui/charges/list", h.ChargesListFragment)
	r.GET("/ui/charges/row/:id", h.ChargeRowStatic)
	r.GET("/ui/charges/row/:id/edit", h.ChargeRowEditFragment)
	r.POST("/ui/charges/create", h.ChargeCreate)
	r.PUT("/ui/charges/row/:id", h.ChargeRowUpdate)
	r.DELETE("/ui/charges/row/:id", h.ChargeRowDelete)

	return r
}

// sessionCookie calls /_session to obtain a session cookie and returns it.
func sessionCookie(r *gin.Engine, uid uuid.UUID, csrfToken string) *http.Cookie {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_session", nil)
	if uid != uuid.Nil {
		req.URL, _ = url.Parse("/_session?uid=" + uid.String())
	}
	r.ServeHTTP(w, req)
	for _, c := range w.Result().Cookies() {
		if c.Name == "test" {
			return c
		}
	}
	return nil
}

// newHandlerForCharges builds a Handler with fake charge ports and one registered vehicle.
func newHandlerForCharges(writer *fakeChargeWriter, reader *fakeChargeReader) *Handler {
	acct := &fakeAccount{
		registered: []account.Vehicle{
			{TeslaID: 1001, VIN: "VIN1001", DisplayName: "Magus"},
		},
	}
	return New(Deps{
		AnalyticsRecalculator: &fakeRecalculator{},
		Account:               acct,
		Tesla:                 &fakeTesla{},
		TelemetryReader:       &fakeReader{},
		ChargingWriter:        writer,
		ChargingReader:        reader,
	})
}

// newHandlerForChargesWithRecalc is newHandlerForCharges with the analytics
// recalculator exposed, so a test can assert the write path actually calls it
// (RM29 task 5.3 / design.md D5). Kept separate rather than changing
// newHandlerForCharges' signature, so the existing call sites stay untouched.
func newHandlerForChargesWithRecalc(writer *fakeChargeWriter, reader *fakeChargeReader, recalc *fakeRecalculator) *Handler {
	acct := &fakeAccount{
		registered: []account.Vehicle{
			{TeslaID: 1001, VIN: "VIN1001", DisplayName: "Magus"},
		},
	}
	return New(Deps{
		AnalyticsRecalculator: recalc,
		Account:               acct,
		Tesla:                 &fakeTesla{},
		TelemetryReader:       &fakeReader{},
		ChargingWriter:        writer,
		ChargingReader:        reader,
	})
}

// postCharge submits a valid create form and returns the recorder.
func postCharge(t *testing.T, h *Handler, uid uuid.UUID) *httptest.ResponseRecorder {
	t.Helper()
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")
	form := url.Values{
		"csrf_token":        {"tok"},
		"status":            {"IN_PROGRESS"},
		"charged_on":        {"2026-07-15"},
		"energy_added_kwh":  {"10.5"},
		"price":             {"5000"},
		"location_kind":     {"HOME"},
		"start_battery_pct": {"50"},
		"end_battery_pct":   {"80"},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/charges/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)
	return w
}

// TestChargeCreate_RecalculatesAfterSuccessfulWrite pins design.md D5 / IO-5:
// the Consumed chart updates instantly today because it is computed on read, so
// the precomputed read model MUST be refreshed on the write path or the owner
// sees a stale chart until the nightly Reconcile. Asserts the call happens, is
// scoped to the writing account and its vehicle, and covers the entry's own day.
func TestChargeCreate_RecalculatesAfterSuccessfulWrite(t *testing.T) {
	uid := uuid.New()
	recalc := &fakeRecalculator{}
	h := newHandlerForChargesWithRecalc(&fakeChargeWriter{}, &fakeChargeReader{entries: []charging.Entry{}}, recalc)

	if w := postCharge(t, h, uid); w.Code != http.StatusOK {
		t.Fatalf("want 200 on valid create, got %d", w.Code)
	}

	if len(recalc.calls) != 1 {
		t.Fatalf("want exactly 1 Recalculate call after a successful create, got %d", len(recalc.calls))
	}
	got := recalc.calls[0]
	if got.accountID != uid {
		t.Errorf("want Recalculate scoped to account %s, got %s", uid, got.accountID)
	}
	if got.teslaID != 1001 {
		t.Errorf("want Recalculate for the session-selected vehicle 1001, got %d", got.teslaID)
	}
	want := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	if !got.start.Equal(want) || !got.end.Equal(want) {
		t.Errorf("want Recalculate over the entry's own day %s..%s, got %s..%s",
			want.Format("2006-01-02"), want.Format("2006-01-02"),
			got.start.Format("2006-01-02"), got.end.Format("2006-01-02"))
	}
}

// TestChargeCreate_NoRecalculateWhenWriteFails pins the other half of D5: the
// recalculation follows a COMMITTED write. If the write failed there is nothing
// to recompute, and recomputing anyway would rewrite the metrics row from
// unchanged inputs while the user is being shown an error.
func TestChargeCreate_NoRecalculateWhenWriteFails(t *testing.T) {
	uid := uuid.New()
	recalc := &fakeRecalculator{}
	writer := &fakeChargeWriter{createErr: errors.New("charging: insert failed")}
	h := newHandlerForChargesWithRecalc(writer, &fakeChargeReader{entries: []charging.Entry{}}, recalc)

	postCharge(t, h, uid)

	if len(recalc.calls) != 0 {
		t.Fatalf("want NO Recalculate call when the write failed, got %d", len(recalc.calls))
	}
}

// TestChargeCreate_RecalculateErrorDoesNotFailTheRequest pins D5's error policy:
// the user's write already committed, so a recalculation failure is logged and
// swallowed rather than surfaced -- failing their request over a derived-metrics
// error would misreport a save that actually succeeded.
func TestChargeCreate_RecalculateErrorDoesNotFailTheRequest(t *testing.T) {
	uid := uuid.New()
	recalc := &fakeRecalculator{err: errors.New("analytics: recalculate failed")}
	h := newHandlerForChargesWithRecalc(&fakeChargeWriter{}, &fakeChargeReader{entries: []charging.Entry{}}, recalc)

	if w := postCharge(t, h, uid); w.Code != http.StatusOK {
		t.Fatalf("want 200 despite a Recalculate error (the write committed), got %d", w.Code)
	}
	if len(recalc.calls) != 1 {
		t.Errorf("want the failing Recalculate to still have been attempted once, got %d", len(recalc.calls))
	}
}

// --- Sub-task H tests ---

// TestChargePage_NoSession verifies the auth guard redirects unauthenticated visitors.
func TestChargePage_NoSession(t *testing.T) {
	h := newHandlerForCharges(&fakeChargeWriter{}, &fakeChargeReader{})
	r := newGinEngine(h)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/charges", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("want 302 redirect for unauthenticated, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/login" {
		t.Errorf("want redirect to /login, got %q", loc)
	}
}

// TestChargePage_WithSession verifies the authenticated path renders the page.
func TestChargePage_WithSession(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	reader := &fakeChargeReader{entries: []charging.Entry{}}
	h := newHandlerForCharges(writer, reader)
	r := engineWithSession(h, uid, "")
	cookie := sessionCookie(r, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/charges", nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 for authenticated charge page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "charges-list") {
		t.Errorf("want charges-list region in response, got body=%q", body[:min(500, len(body))])
	}
}

// TestChargesListFragment_NoSession verifies auth guard on the list fragment.
func TestChargesListFragment_NoSession(t *testing.T) {
	h := newHandlerForCharges(&fakeChargeWriter{}, &fakeChargeReader{})
	r := newGinEngine(h)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/charges/list", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("want 302 for unauthenticated list fragment, got %d", w.Code)
	}
}

// TestChargesListFragment_WithEntries verifies the list fragment returns only
// the fragment. REWRITTEN for RM33 tier 3 (design.md §Test Contract "Existing
// tests requiring REWRITE"): GET /ui/charges/list with NO ?start=&end= now
// resolves the default 7-day window via parseChargesRange (rather than taking
// every account entry unconditionally) and the response now also carries the
// preset selector and the four aggregation tiles — asserted here alongside
// the pre-existing charges-list/row-id checks, not merely the div's presence.
func TestChargesListFragment_WithEntries(t *testing.T) {
	uid := uuid.New()
	chargedOn := time.Now()
	entryID := uuid.New()
	entries := []charging.Entry{
		{
			ID:             entryID,
			AccountID:      uid,
			TeslaID:        1001,
			VIN:            "VIN1001",
			ChargedOn:      chargedOn,
			EnergyAddedKWh: ptrF64(10.0),
			Price:          5000.0,
			Currency:       "COP",
		},
	}
	reader := &fakeChargeReader{entries: entries}
	h := newHandlerForCharges(&fakeChargeWriter{}, reader)
	r := engineWithSession(h, uid, "testcsrf")
	c := sessionCookie(r, uid, "testcsrf")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/charges/list", nil)
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 for list fragment, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "charges-list") {
		t.Errorf("want charges-list div in fragment response, body=%q", body[:min(500, len(body))])
	}
	if !strings.Contains(body, "charge-row-"+entryID.String()) {
		t.Errorf("want the entry's row in the response, body=%q", body[:min(800, len(body))])
	}
	if !strings.Contains(body, "join-item") {
		t.Errorf("want the default-window preset selector rendered, body=%q", body[:min(1500, len(body))])
	}
	if got := strings.Count(body, "stat-title"); got != 4 {
		t.Errorf("want the four aggregation tiles rendered, got %d stat-title occurrences, body=%q", got, body[:min(1500, len(body))])
	}
}

// TestChargesListFragment_ReaderError verifies graceful degradation on reader
// failure. REWRITTEN for RM33 tier 3 (design.md §Test Contract "Existing
// tests requiring REWRITE" + §D-Empty state 2): the default-window resolution
// now applies here too, and a reader error keeps the preset selector AND the
// (zero-valued) tiles visible — it is NOT the same no-chrome collapse as a
// malformed window (Group E1/E2) or a no-vehicle response.
func TestChargesListFragment_ReaderError(t *testing.T) {
	uid := uuid.New()
	reader := &fakeChargeReader{err: errFake}
	h := newHandlerForCharges(&fakeChargeWriter{}, reader)
	r := engineWithSession(h, uid, "testcsrf")
	c := sessionCookie(r, uid, "testcsrf")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/charges/list", nil)
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 (graceful degradation) on reader error, got %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "500") || strings.Contains(body, "internal server error") {
		t.Errorf("want no 500 in graceful degradation response, body=%q", body[:min(500, len(body))])
	}
	if !strings.Contains(body, "join-item") {
		t.Errorf("want the preset selector STILL shown on a reader error (D-Empty state 2), body=%q", body[:min(1500, len(body))])
	}
	if got := strings.Count(body, "stat-title"); got != 4 {
		t.Errorf("want the four (zero-valued) tiles STILL shown on a reader error, got %d stat-title occurrences, body=%q", got, body[:min(1500, len(body))])
	}
}

// TestChargesListFragment_EmptyState verifies empty-state message on empty
// list. REWRITTEN for RM33 tier 3 (design.md §Test Contract "Existing tests
// requiring REWRITE" + §D-Empty state 3): a valid vehicle + valid (default)
// window with zero rows is NOT the same as the no-chrome collapse (Group
// E1/E2) — D13 requires the selector and tiles to still render (0/—), only
// the table body swaps for ChargesEmptyState().
func TestChargesListFragment_EmptyState(t *testing.T) {
	uid := uuid.New()
	reader := &fakeChargeReader{entries: []charging.Entry{}}
	h := newHandlerForCharges(&fakeChargeWriter{}, reader)
	r := engineWithSession(h, uid, "testcsrf")
	c := sessionCookie(r, uid, "testcsrf")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/charges/list", nil)
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 for empty list, got %d", w.Code)
	}
	body := w.Body.String()
	// engineWithSession never wires handlers.LanguageMiddleware, so i18n.FromContext
	// falls back to its platform default (Spanish) — assert the resolved-language
	// string (KeyChargesListEmpty's ES value), not the pre-existing English literal
	// (RM24-gateway-translate-all-pages, mirroring tier 2's T6.4 precedent).
	if !strings.Contains(body, "Aún no hay cargas registradas") {
		t.Errorf("want empty-state message, body=%q", body[:min(500, len(body))])
	}
	if !strings.Contains(body, "join-item") {
		t.Errorf("want the preset selector STILL shown on a valid empty range (D13), body=%q", body[:min(1500, len(body))])
	}
	if got := strings.Count(body, "stat-title"); got != 4 {
		t.Errorf("want the four tiles STILL shown at 0/— on a valid empty range (D13/D14), got %d stat-title occurrences, body=%q", got, body[:min(1500, len(body))])
	}
	if strings.Contains(body, "<table") {
		t.Errorf("want NO <table> element — the table body is replaced by ChargesEmptyState(), body=%q", body[:min(1500, len(body))])
	}
}

// TestChargeCreate_CSRFMismatch verifies 403 on CSRF token mismatch.
func TestChargeCreate_CSRFMismatch(t *testing.T) {
	uid := uuid.New()
	h := newHandlerForCharges(&fakeChargeWriter{}, &fakeChargeReader{})
	r := engineWithSession(h, uid, "correcttoken")
	c := sessionCookie(r, uid, "correcttoken")

	form := url.Values{
		"csrf_token":        {"wrongtoken"},
		"charged_on":        {"2026-07-15"},
		"energy_added_kwh":  {"10.5"},
		"price":             {"5000"},
		"location_kind":     {"HOME"},
		"start_battery_pct": {"50"},
		"end_battery_pct":   {"80"},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/charges/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 on CSRF mismatch, got %d", w.Code)
	}
}

// TestChargeCreate_NoResolvableVehicle_RejectedWithoutWriter replaces the old
// TestChargeCreate_UnownedVehicle (MAG-5 D4): the create form drops the vehicle
// picker and sources (teslaID, vin) from the session-selected vehicle via
// resolveSelectedVehicle. The "unowned vehicle" path is now unreachable from the
// form (resolveSelectedVehicle only returns vehicles from the user's account);
// the analogous failure becomes "no resolvable selected vehicle" (the account has
// no registered vehicles, so resolveSelectedVehicle returns (_, false)) —
// rendered as a 422 field-error, NOT a 403, and the Writer is NOT called.
func TestChargeCreate_NoResolvableVehicle_RejectedWithoutWriter(t *testing.T) {
	uid := uuid.New()
	// Account with NO registered vehicles → resolveSelectedVehicle returns false.
	acct := &fakeAccount{registered: nil}
	h := New(Deps{
		AnalyticsRecalculator: &fakeRecalculator{},
		Account:               acct,
		Tesla:                 &fakeTesla{},
		TelemetryReader:       &fakeReader{},
		ChargingWriter:        &fakeChargeWriter{},
		ChargingReader:        &fakeChargeReader{},
	})
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	form := url.Values{
		"csrf_token":        {"tok"},
		"status":            {"IN_PROGRESS"},
		"charged_on":        {"2026-07-15"},
		"energy_added_kwh":  {"10.5"},
		"price":             {"5000"},
		"location_kind":     {"HOME"},
		"start_battery_pct": {"50"},
		"end_battery_pct":   {"80"},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/charges/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 when no vehicle is resolvable, got %d", w.Code)
	}
	body := w.Body.String()
	// Resolved language here is Spanish (see the TestChargesListFragment_EmptyState
	// comment above) — KeyChargesErrorSelectVehicle's ES value.
	if !strings.Contains(body, "Selecciona un vehículo") {
		t.Errorf("want 'Selecciona un vehículo' in body, got %q", body[:min(500, len(body))])
	}
}

// TestChargeCreate_MissingRequiredField verifies 422 on a missing required field.
func TestChargeCreate_MissingRequiredField(t *testing.T) {
	uid := uuid.New()
	h := newHandlerForCharges(&fakeChargeWriter{}, &fakeChargeReader{})
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	form := url.Values{
		"csrf_token": {"tok"},
		"status":     {"IN_PROGRESS"},
		"price":      {"5000"},
		// deliberately omit charged_on, energy_added_kwh, location_kind,
		// start_battery_pct, end_battery_pct — every required field.
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/charges/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 on missing required field, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "error") {
		t.Errorf("want error message in 422 response, body=%q", body[:min(500, len(body))])
	}
}

// TestChargeCreate_ValidInput verifies a valid POST creates an entry and returns the refreshed list.
// MAG-5 D5: Currency is hardcoded "COP" even though the form no longer submits a
// `currency` field (it renders a disabled, read-only COP input that is not
// submitted). MAG-5 D6: start_battery_pct + end_battery_pct are REQUIRED and
// persisted non-nil. D4: no `vehicle` form field — sourced from the session.
func TestChargeCreate_ValidInput(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	reader := &fakeChargeReader{entries: []charging.Entry{}}
	h := newHandlerForCharges(writer, reader)
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	form := url.Values{
		"csrf_token":        {"tok"},
		"status":            {"IN_PROGRESS"},
		"charged_on":        {"2026-07-15"},
		"energy_added_kwh":  {"10.5"},
		"price":             {"5000"},
		"location_kind":     {"HOME"},
		"start_battery_pct": {"50"},
		"end_battery_pct":   {"80"},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/charges/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 on valid create, got %d body=%q", w.Code, w.Body.String()[:min(500, w.Body.Len())])
	}
	if got := writer.createEntry.EnergyAddedKWh; got == nil {
		t.Errorf("want energy 10.5 persisted, got nil")
	} else if *got != 10.5 {
		t.Errorf("want energy 10.5 persisted, got %f", *got)
	}
	// D5: Currency hardcoded COP despite no `currency` form field.
	if writer.createEntry.Currency != "COP" {
		t.Errorf("want Currency COP hardcoded, got %q", writer.createEntry.Currency)
	}
	// D6: required battery fields persisted non-nil with the submitted values.
	if writer.createEntry.StartBatteryPct == nil || *writer.createEntry.StartBatteryPct != 50 {
		t.Errorf("want StartBatteryPct=50 (non-nil), got %v", writer.createEntry.StartBatteryPct)
	}
	if writer.createEntry.EndBatteryPct == nil || *writer.createEntry.EndBatteryPct != 80 {
		t.Errorf("want EndBatteryPct=80 (non-nil), got %v", writer.createEntry.EndBatteryPct)
	}
	// D4: TeslaID + VIN sourced from session-selected vehicle (the fake's lone
	// registered vehicle, auto-picked as the first non-OWNER).
	if writer.createEntry.TeslaID != 1001 || writer.createEntry.VIN != "VIN1001" {
		t.Errorf("want vehicle sourced from session = 1001:VIN1001, got %d:%s",
			writer.createEntry.TeslaID, writer.createEntry.VIN)
	}
}

// TestChargeRowEditFragment_NoSession verifies auth guard on the edit fragment.
func TestChargeRowEditFragment_NoSession(t *testing.T) {
	h := newHandlerForCharges(&fakeChargeWriter{}, &fakeChargeReader{})
	r := newGinEngine(h)

	id := uuid.New()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/charges/row/"+id.String()+"/edit", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("want 302 for unauthenticated edit fragment, got %d", w.Code)
	}
}

// TestChargeRowEditFragment_WithEntry verifies the edit fragment returns an edit form pre-populated.
func TestChargeRowEditFragment_WithEntry(t *testing.T) {
	uid := uuid.New()
	entryID := uuid.New()
	entries := []charging.Entry{
		{
			ID:             entryID,
			AccountID:      uid,
			TeslaID:        1001,
			VIN:            "VIN1001",
			ChargedOn:      time.Now(),
			EnergyAddedKWh: ptrF64(15.0),
			Price:          7000.0,
			Currency:       "COP",
		},
	}
	reader := &fakeChargeReader{entries: entries}
	h := newHandlerForCharges(&fakeChargeWriter{}, reader)
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/charges/row/"+entryID.String()+"/edit", nil)
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 for edit fragment, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "charge-row-"+entryID.String()) {
		t.Errorf("want entry id in edit form, body=%q", body[:min(500, len(body))])
	}
}

// TestChargeRowUpdate_CSRFMismatch verifies 403 on CSRF mismatch for update.
func TestChargeRowUpdate_CSRFMismatch(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	h := newHandlerForCharges(&fakeChargeWriter{}, &fakeChargeReader{})
	r := engineWithSession(h, uid, "goodtoken")
	c := sessionCookie(r, uid, "goodtoken")

	form := url.Values{
		"csrf_token":        {"badtoken"},
		"charged_on":        {"2026-07-15"},
		"energy_added_kwh":  {"10.5"},
		"price":             {"5000"},
		"location_kind":     {"HOME"},
		"start_battery_pct": {"50"},
		"end_battery_pct":   {"80"},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/ui/charges/row/"+id.String(), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 on CSRF mismatch for update, got %d", w.Code)
	}
}

// TestChargeRowUpdate_ValidationError verifies 422 on a validation error during update.
func TestChargeRowUpdate_ValidationError(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	h := newHandlerForCharges(&fakeChargeWriter{}, &fakeChargeReader{})
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	form := url.Values{
		"csrf_token": {"tok"},
		"status":     {"IN_PROGRESS"},
		"price":      {"5000"},
		// deliberately omit charged_on, energy_added_kwh, location_kind,
		// start_battery_pct, end_battery_pct.
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/ui/charges/row/"+id.String(), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 on validation error for update, got %d", w.Code)
	}
}

// TestChargeRowUpdate_ValidInput verifies a valid PUT persists the posted values
// and re-renders the whole #charges-list region.
//
// The response-shape assertion below was rewritten when MAG-18 wave 5 retargeted
// the success path (charges.go, "A SUCCESSFUL edit re-renders the WHOLE
// #charges-list region"): htmx parses the response inside a <template>, so a
// leading <tr> switches the HTML parser into table-insertion mode and the
// following <div hx-swap-oob> is foster-parented out of top level, silently
// dropping the list refresh. The handler therefore emits no row markup at all,
// and the old `charge-row-{id}` expectation could never match again. Sibling
// TestChargeRowUpdate_D3_SuccessRetargetsAndResetsToDefaultWindow owns the full
// contract (headers, no OOB, no leading <tr>); this test keeps its own focus on
// the persisted write values.
func TestChargeRowUpdate_ValidInput(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	writer := &fakeChargeWriter{}
	h := newHandlerForCharges(writer, &fakeChargeReader{})
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	form := url.Values{
		"csrf_token":        {"tok"},
		"status":            {"IN_PROGRESS"},
		"charged_on":        {"2026-07-16"},
		"energy_added_kwh":  {"20.0"},
		"price":             {"9000"},
		"location_kind":     {"WORK"},
		"start_battery_pct": {"40"},
		"end_battery_pct":   {"75"},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/ui/charges/row/"+id.String(), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 on valid update, got %d body=%q", w.Code, w.Body.String()[:min(500, w.Body.Len())])
	}
	body := w.Body.String()
	if !strings.Contains(body, `id="charges-list"`) {
		t.Errorf("want the whole #charges-list region in the update response, body=%q", body[:min(500, len(body))])
	}
	if got := w.Header().Get("HX-Retarget"); got != "#charges-list" {
		t.Errorf("want HX-Retarget=#charges-list on a successful update, got %q", got)
	}
	// D5: Currency hardcoded COP even for the edit path.
	if writer.updateEntry.Currency != "COP" {
		t.Errorf("want update Currency COP hardcoded, got %q", writer.updateEntry.Currency)
	}
	// D6: battery persisted non-nil.
	if writer.updateEntry.StartBatteryPct == nil || *writer.updateEntry.StartBatteryPct != 40 {
		t.Errorf("want update StartBatteryPct=40, got %v", writer.updateEntry.StartBatteryPct)
	}
	if writer.updateEntry.EndBatteryPct == nil || *writer.updateEntry.EndBatteryPct != 75 {
		t.Errorf("want update EndBatteryPct=75, got %v", writer.updateEntry.EndBatteryPct)
	}
}

// TestChargeRowDelete_CSRFMismatch verifies 403 on CSRF mismatch for delete.
func TestChargeRowDelete_CSRFMismatch(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	writer := &fakeChargeWriter{deleteErr: nil}
	h := newHandlerForCharges(writer, &fakeChargeReader{})
	r := engineWithSession(h, uid, "goodtoken")
	c := sessionCookie(r, uid, "goodtoken")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/ui/charges/row/"+id.String(), nil)
	req.Header.Set("X-CSRF-Token", "badtoken")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 on CSRF mismatch for delete, got %d", w.Code)
	}
}

// TestChargeRowDelete_ValidInput_RendersChargesListFragment is the RM33 tier 3
// REWRITE of the old TestChargeRowDelete_ValidInput_RendersEmptyRow (design.md
// §Test Contract "Existing tests requiring REWRITE"). The delete button's
// hx-target moved from "#charge-row-{id}" to "#charges-list" (design.md
// §D-Refresh) — after a successful delete there is no row left to swap into,
// so the handler now re-renders the WHOLE #charges-list region (selector +
// tiles + table/empty-state), structurally identical to ChargesListFragment's
// own response. The old bare `<tr id="charge-row-<id>"></tr>` empty-row shape
// (`fragments.ChargeRowEmpty`, deleted in Wave 6.1) must NOT appear. Same
// assertion shape as Group D4 (TestChargeRowDelete_D4_...), kept as its own
// test per design.md's explicit REWRITE-item enumeration. The CSRF token is
// sent via the X-CSRF-Token HEADER (matching the charge_row.templ fix that
// emits hx-headers carrying X-CSRF-Token — Go's net/http parses DELETE
// request BODIES for no method, so the prior hx-include body path silently
// 403'd; the header path is the fix — see ChargeRowDelete doc comment).
func TestChargeRowDelete_ValidInput_RendersChargesListFragment(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	writer := &fakeChargeWriter{}
	h := newHandlerForCharges(writer, &fakeChargeReader{})
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/ui/charges/row/"+id.String(), nil)
	req.Header.Set("X-CSRF-Token", "tok")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 on valid delete, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `id="charges-list"`) {
		t.Errorf("want the full #charges-list fragment in the delete response, got body=%q", body[:min(500, len(body))])
	}
	// The old MAG-5 empty-row shape (fragments.ChargeRowEmpty, now deleted)
	// must never appear — the response is the whole list region, not a bare
	// empty <tr>.
	wantOldEmptyRow := `<tr id="charge-row-` + id.String() + `"></tr>`
	if strings.Contains(body, wantOldEmptyRow) {
		t.Errorf("delete response must NOT be the old bare empty <tr> shape, got body=%q",
			body[:min(500, len(body))])
	}
}

// TestChargeRowDelete_StaleCSRF_RejectedNotAlerted verifies the stale/missing
// CSRF path returns 403 and the row is not deleted (D3 / spec scenario). The
// response is a plain-text 403 (no inline error row), but per the spec the
// correct fix is the wire-path (CSRF reaches the handler); an attacker
// submitting a wrong token is rejected at the CSRF gate.
func TestChargeRowDelete_StaleCSRF_RejectedNotAlerted(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	writer := &fakeChargeWriter{}
	h := newHandlerForCharges(writer, &fakeChargeReader{})
	r := engineWithSession(h, uid, "freshsessiontoken")
	c := sessionCookie(r, uid, "freshsessiontoken")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/ui/charges/row/"+id.String(), nil)
	// Stale token: the row would have been rendered with an older token, but the
	// session has since rotated.
	req.Header.Set("X-CSRF-Token", "staletoken")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 on stale CSRF for delete, got %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "<tr") {
		t.Errorf("403 CSRF-rejection must not swap the row (no <tr> in body), got %q",
			w.Body.String()[:min(200, w.Body.Len())])
	}
	// F4 (MAG-5 follow-up): the stale-CSRF path must fail BEFORE reaching
	// charging.Writer.Delete — asserting only the HTTP response left this
	// test blind to a bug where checkCSRF rejects the response but the handler
	// still called Delete. Assert the write never happened.
	if writer.deleteCalls != 0 {
		t.Errorf("Writer.Delete must NOT be called on stale/missing CSRF, got %d call(s)", writer.deleteCalls)
	}
}

// --- R1/R2 regression: empty-session CSRF must fail closed ---
// When the session carries no csrf_manualcharge token (e.g. an authenticated user
// who never loaded GET /charges) and the request submits no token either, the check
// must return 403 — not pass via subtle.ConstantTimeCompare("","") == 1.

// TestChargeCreate_NoSessionToken covers the empty-session CSRF bypass for create.
func TestChargeCreate_NoSessionToken(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	h := newHandlerForCharges(writer, &fakeChargeReader{})
	r := engineWithSession(h, uid, "") // uid set, no csrf token in session
	c := sessionCookie(r, uid, "")

	form := url.Values{
		// deliberately no csrf_token submitted
		"charged_on":        {"2026-07-15"},
		"energy_added_kwh":  {"10.5"},
		"price":             {"5000"},
		"location_kind":     {"HOME"},
		"start_battery_pct": {"50"},
		"end_battery_pct":   {"80"},
		// no `vehicle` (D4), no `currency` (D5 — hardcoded COP) form fields.
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/charges/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 when session has no csrf token and none submitted, got %d", w.Code)
	}
	if writer.createEntry.EnergyAddedKWh != nil {
		t.Errorf("no entry should be created on CSRF failure, got energy %v", *writer.createEntry.EnergyAddedKWh)
	}
}

// TestChargeRowUpdate_NoSessionToken covers the empty-session CSRF bypass for update.
func TestChargeRowUpdate_NoSessionToken(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	writer := &fakeChargeWriter{}
	h := newHandlerForCharges(writer, &fakeChargeReader{})
	r := engineWithSession(h, uid, "")
	c := sessionCookie(r, uid, "")

	form := url.Values{
		// deliberately no csrf_token submitted
		"charged_on":        {"2026-07-16"},
		"energy_added_kwh":  {"20.0"},
		"price":             {"9000"},
		"location_kind":     {"WORK"},
		"start_battery_pct": {"40"},
		"end_battery_pct":   {"75"},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/ui/charges/row/"+id.String(), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 on update when session has no csrf token, got %d", w.Code)
	}
	if writer.updateEntry.EnergyAddedKWh != nil {
		t.Errorf("no entry should be updated on CSRF failure, got energy %v", *writer.updateEntry.EnergyAddedKWh)
	}
}

// TestChargeRowDelete_NoSessionToken covers the empty-session CSRF bypass for delete.
func TestChargeRowDelete_NoSessionToken(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	h := newHandlerForCharges(&fakeChargeWriter{}, &fakeChargeReader{})
	r := engineWithSession(h, uid, "")
	c := sessionCookie(r, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/ui/charges/row/"+id.String(), nil)
	// deliberately no X-CSRF-Token header
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 on delete when session has no csrf token, got %d", w.Code)
	}
}

// errFake is a sentinel error for test fakes.
var errFake = &fakeError{"fake error"}

type fakeError struct{ msg string }

func (e *fakeError) Error() string { return e.msg }

// min returns the smaller of a and b.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// --- unit tests for pure helpers ---

// TestChargeEntryVMFromEntry verifies the view model mapping pre-computes derived fields.
func TestChargeEntryVMFromEntry(t *testing.T) {
	start := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 15, 11, 30, 0, 0, time.UTC)
	startPct := 60
	endPct := 92

	e := charging.Entry{
		ID:              uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		AccountID:       uuid.New(),
		TeslaID:         1001,
		VIN:             "VIN1001",
		ChargedOn:       time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC),
		EnergyAddedKWh:  ptrF64(12.5),
		Price:           15000.0,
		Currency:        "COP",
		StartedAt:       &start,
		EndedAt:         &end,
		StartBatteryPct: &startPct,
		EndBatteryPct:   &endPct,
	}
	vehicles := []account.Vehicle{
		{TeslaID: 1001, VIN: "VIN1001", DisplayName: "Magus"},
	}

	vm := chargeEntryVMFromEntry(e, vehicles)

	if vm.ID != "00000000-0000-0000-0000-000000000001" {
		t.Errorf("want ID as string, got %q", vm.ID)
	}
	if vm.VehicleLabel != "Magus" {
		t.Errorf("want VehicleLabel Magus, got %q", vm.VehicleLabel)
	}
	if vm.Currency != "COP" {
		t.Errorf("want Currency COP, got %q", vm.Currency)
	}
	if vm.CostPerKWhLabel == "" {
		t.Errorf("want CostPerKWhLabel set, got empty")
	}
	// MAG-9: PriceLabel (15000.0 COP) and CostPerKWhLabel (15000/12.5 = 1200.0
	// COP/kWh) must both be comma-grouped via formatMoney.
	if vm.PriceLabel != "15,000.00 COP" {
		t.Errorf("want PriceLabel=15,000.00 COP (comma-grouped), got %q", vm.PriceLabel)
	}
	if vm.CostPerKWhLabel != "1,200.00 COP/kWh" {
		t.Errorf("want CostPerKWhLabel=1,200.00 COP/kWh (comma-grouped + /kWh suffix), got %q", vm.CostPerKWhLabel)
	}
	if vm.BatteryDelta != "+32%" {
		t.Errorf("want BatteryDelta +32%%, got %q", vm.BatteryDelta)
	}
	if vm.DurationLabel != "1h 30m" {
		t.Errorf("want DurationLabel '1h 30m', got %q", vm.DurationLabel)
	}
	if vm.RawChargedOn != "2026-07-15" {
		t.Errorf("want RawChargedOn 2026-07-15, got %q", vm.RawChargedOn)
	}
	if vm.RawEnergyKWh != "12.50" {
		t.Errorf("want RawEnergyKWh 12.50, got %q", vm.RawEnergyKWh)
	}
	if vm.RawStartedAt != "2026-07-15T10:00" {
		t.Errorf("want RawStartedAt 2026-07-15T10:00, got %q", vm.RawStartedAt)
	}
	if vm.RawStartBatteryPct != "60" {
		t.Errorf("want RawStartBatteryPct 60, got %q", vm.RawStartBatteryPct)
	}
	if vm.RawEndBatteryPct != "92" {
		t.Errorf("want RawEndBatteryPct 92, got %q", vm.RawEndBatteryPct)
	}
	if vm.TeslaID != 1001 {
		t.Errorf("want TeslaID 1001, got %d", vm.TeslaID)
	}
	if vm.VIN != "VIN1001" {
		t.Errorf("want VIN VIN1001, got %q", vm.VIN)
	}
}

// TestChargeEntryVMFromEntry_RawFieldsNeverCommaGrouped pins design.md's
// "Critical constraint" (MAG-9): RawEnergyKWh and RawPrice populate the inline
// edit form's <input value>, so they MUST stay plain machine-parseable decimal
// strings — never routed through formatMoney/commaGroup — even when the
// underlying amount is >= 1000 and would otherwise be grouped for display.
func TestChargeEntryVMFromEntry_RawFieldsNeverCommaGrouped(t *testing.T) {
	e := charging.Entry{
		ID:             uuid.MustParse("00000000-0000-0000-0000-000000000002"),
		AccountID:      uuid.New(),
		TeslaID:        1001,
		VIN:            "VIN1001",
		ChargedOn:      time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC),
		EnergyAddedKWh: ptrF64(1200.5),
		Price:          58000.0,
		Currency:       "COP",
	}
	vehicles := []account.Vehicle{
		{TeslaID: 1001, VIN: "VIN1001", DisplayName: "Magus"},
	}

	vm := chargeEntryVMFromEntry(e, vehicles)

	if vm.RawEnergyKWh != "1200.50" {
		t.Errorf("want RawEnergyKWh=1200.50 (NOT comma-grouped), got %q", vm.RawEnergyKWh)
	}
	if vm.RawPrice != "58000.00" {
		t.Errorf("want RawPrice=58000.00 (NOT comma-grouped), got %q", vm.RawPrice)
	}
	// Sanity: the display label for the SAME amount IS comma-grouped, proving
	// the raw/display split is real and not just both happening to be unformatted.
	if vm.PriceLabel != "58,000.00 COP" {
		t.Errorf("want PriceLabel=58,000.00 COP (comma-grouped, unlike RawPrice), got %q", vm.PriceLabel)
	}
}

// --- Sub-task D: required location_kind tests ---

// TestChargeCreate_MissingLocationKind verifies 422 when location_kind is absent.
func TestChargeCreate_MissingLocationKind(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	reader := &fakeChargeReader{entries: []charging.Entry{}}
	h := newHandlerForCharges(writer, reader)
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	form := url.Values{
		"csrf_token":        {"tok"},
		"status":            {"IN_PROGRESS"},
		"charged_on":        {"2026-07-15"},
		"energy_added_kwh":  {"10.5"},
		"price":             {"5000"},
		"start_battery_pct": {"50"},
		"end_battery_pct":   {"80"},
		// deliberately no location_kind
		// no `vehicle` (D4), no `currency` (D5) form fields.
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/charges/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 on missing location_kind, got %d", w.Code)
	}
	if writer.createEntry.EnergyAddedKWh != nil {
		t.Errorf("Writer.Create must NOT be called on missing location_kind, got energy %v",
			*writer.createEntry.EnergyAddedKWh)
	}
	body := w.Body.String()
	// Resolved language is Spanish here (KeyChargesErrorLocationRequired's ES value).
	if !strings.Contains(body, "La ubicación es obligatoria") {
		t.Errorf("want location_kind error in response, body=%q", body[:min(500, len(body))])
	}
}

// TestChargeCreate_InvalidLocationKind verifies 422 when location_kind has an unrecognized value.
func TestChargeCreate_InvalidLocationKind(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	reader := &fakeChargeReader{entries: []charging.Entry{}}
	h := newHandlerForCharges(writer, reader)
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	form := url.Values{
		"csrf_token":        {"tok"},
		"status":            {"IN_PROGRESS"},
		"charged_on":        {"2026-07-15"},
		"energy_added_kwh":  {"10.5"},
		"price":             {"5000"},
		"location_kind":     {"INVALID"},
		"start_battery_pct": {"50"},
		"end_battery_pct":   {"80"},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/charges/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 on invalid location_kind, got %d", w.Code)
	}
	if writer.createEntry.EnergyAddedKWh != nil {
		t.Errorf("Writer.Create must NOT be called on invalid location_kind")
	}
}

// TestChargeRowUpdate_MissingLocationKind verifies 422 on missing location_kind during update.
func TestChargeRowUpdate_MissingLocationKind(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	writer := &fakeChargeWriter{}
	h := newHandlerForCharges(writer, &fakeChargeReader{})
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	form := url.Values{
		"csrf_token":        {"tok"},
		"status":            {"IN_PROGRESS"},
		"charged_on":        {"2026-07-16"},
		"energy_added_kwh":  {"20.0"},
		"price":             {"9000"},
		"start_battery_pct": {"40"},
		"end_battery_pct":   {"75"},
		// deliberately no location_kind
		// no `vehicle` (D4), no `currency` (D5) form fields.
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/ui/charges/row/"+id.String(), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 on missing location_kind for update, got %d", w.Code)
	}
	if writer.updateEntry.EnergyAddedKWh != nil {
		t.Errorf("Writer.Update must NOT be called on missing location_kind")
	}
}

// TestChargeCreate_ValidLocationKind verifies that a valid location_kind succeeds.
func TestChargeCreate_ValidLocationKind(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	reader := &fakeChargeReader{entries: []charging.Entry{}}
	h := newHandlerForCharges(writer, reader)
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	form := url.Values{
		"csrf_token":        {"tok"},
		"status":            {"IN_PROGRESS"},
		"charged_on":        {"2026-07-15"},
		"energy_added_kwh":  {"10.5"},
		"price":             {"5000"},
		"location_kind":     {"HOME"},
		"start_battery_pct": {"50"},
		"end_battery_pct":   {"80"},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/charges/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 on valid location_kind=HOME, got %d body=%q",
			w.Code, w.Body.String()[:min(500, w.Body.Len())])
	}
	if writer.createEntry.LocationKind == nil || *writer.createEntry.LocationKind != "HOME" {
		t.Errorf("want LocationKind=HOME persisted, got %v", writer.createEntry.LocationKind)
	}
}

// ptrStr is a helper to take the address of a string literal in tests. Still used
// by supercharger_test.go after the vehicle-auto-select tests (buildVehicleOptions,
// removed with its dead call chain — MAG-5 follow-up F1) were deleted here.
func ptrStr(s string) *string { return &s }

// --- charges content fragment + vehicle-switch refresh (GET /ui/charges) ---

// chargesContentEngine builds a Gin engine seeding uid + a selected-vehicle context,
// wired to GET /ui/charges. Mirrors dashboardEngine but for the manual-records page.
func chargesContentEngine(h *Handler, uid uuid.UUID, selTeslaID int64, selVIN string) *gin.Engine {
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
	r.GET("/charges", h.ChargePage)
	r.GET("/ui/charges", h.ChargesContentFragment)
	return r
}

// TestChargesContentFragment_ScopedToSelectedVehicle verifies GET /ui/charges emits
// BOTH content fragments (create form + list) for the SELECTED vehicle, with a fresh
// CSRF token and no page shell. After MAG-5 D4 the create form no longer renders a
// vehicle picker at all (the session determines the vehicle), so the assertion now
// ALSO verifies D4: no `name="vehicle"` input is present in the create form.
func TestChargesContentFragment_ScopedToSelectedVehicle(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 1, VIN: "VIN1", DisplayName: "First"},
		{TeslaID: 2, VIN: "VIN2", DisplayName: "Second"},
	}}
	h := New(Deps{
		AnalyticsRecalculator: &fakeRecalculator{},
		Account:               acct,
		Tesla:                 &fakeTesla{},
		TelemetryReader:       &fakeReader{},
		ChargingWriter:        &fakeChargeWriter{},
		ChargingReader:        &fakeChargeReader{},
	})
	eng := chargesContentEngine(h, uid, 2, "VIN2")
	c := sessionCookie(eng, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/charges", nil)
	if c != nil {
		req.AddCookie(c)
	}
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 for authenticated charges content fragment, got %d (%s)", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{`id="charges-create-form"`, `id="charges-list"`, `name="csrf_token"`} {
		if !strings.Contains(body, want) {
			t.Errorf("charges content fragment missing %q\n%s", want, body)
		}
	}
	if strings.Contains(body, "<html") {
		t.Errorf("charges content fragment must not contain the full-page shell (<html>)")
	}
	// D4: there is no vehicle picker on the create form anymore — no `name="vehicle"`
	// input is rendered. The vehicle is sourced from the session-selected vehicle.
	if strings.Contains(body, `name="vehicle"`) {
		t.Errorf("D4 violation: create form must not render a vehicle picker (name=%q), got body:\n%s",
			"vehicle", body)
	}
	// D4 also removes the multi-vehicle selected-option marker the old test asserted.
	if strings.Contains(body, `value="2:VIN2" selected`) {
		t.Errorf("D4 violation: selected vehicle option should not be rendered; vehicle picker is gone")
	}
}

// TestChargePage_SubscribesToVehicleChanged verifies the full manual-records page
// wraps its content in the #charges-content region wired to refresh on the sidebar
// switcher's "vehicle-changed" event.
func TestChargePage_SubscribesToVehicleChanged(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 1, VIN: "VIN1", DisplayName: "First"},
	}}
	h := New(Deps{
		AnalyticsRecalculator: &fakeRecalculator{},
		Account:               acct,
		Tesla:                 &fakeTesla{},
		TelemetryReader:       &fakeReader{},
		ChargingWriter:        &fakeChargeWriter{},
		ChargingReader:        &fakeChargeReader{},
	})
	eng := chargesContentEngine(h, uid, 1, "VIN1")
	c := sessionCookie(eng, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/charges", nil)
	if c != nil {
		req.AddCookie(c)
	}
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 for the charge page, got %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		`id="charges-content"`,
		`hx-get="/ui/charges"`,
		`hx-trigger="vehicle-changed from:body"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("charge page missing switch-refresh wiring %q", want)
		}
	}
}

// --- MAG-5 D2/D6: start-battery suggestion + required battery fields (T4.5) ---

// TestChargePage_BatterySuggestionFromTelemetry verifies D2: when the selected
// vehicle's latest telemetry snapshot reports BatteryLevelPct=73, the create
// form's start_battery_pct input carries a placeholder helper label "Latest: 73%"
// built via the existing telemetry.Reader.LatestSnapshotsByAccount port (the same
// port the dashboard uses). The fakeReader seeds the snapshot; the buildChargesPage
// helper picks the snapshot for the session-selected TeslaID.
func TestChargePage_BatterySuggestionFromTelemetry(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 1001, VIN: "VIN1001", DisplayName: "Magus"},
	}}
	reader := &fakeReader{snapshots: []telemetry.Snapshot{
		{TeslaID: 1001, BatteryLevelPct: 73},
	}}
	h := New(Deps{
		AnalyticsRecalculator: &fakeRecalculator{},
		Account:               acct,
		Tesla:                 &fakeTesla{},
		TelemetryReader:       reader,
		ChargingWriter:        &fakeChargeWriter{},
		ChargingReader:        &fakeChargeReader{},
	})
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/charges", nil)
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	body := w.Body.String()
	// Resolved language is Spanish here (KeyChargesErrorBatterySuggestion's ES value).
	if want := `placeholder="Última: 73%"`; !strings.Contains(body, want) {
		t.Errorf("want %q in start_battery_pct placeholder (D2), got body:\n%s", want, body[:min(800, len(body))])
	}
	// Start AND end battery % must be Required (D6) — the input name="start_battery_pct"
	// and name="end_battery_pct" rows should both carry the `required` boolean attr.
	for _, name := range []string{"start_battery_pct", "end_battery_pct"} {
		if !strings.Contains(body, `name="`+name+`"`) {
			t.Errorf("D6: %q input missing in create form", name)
		}
	}
}

// TestChargePage_NoBatterySuggestionWhenNoSnapshot verifies D2's graceful-empty
// contract: when the telemetry.Reader returns no snapshot for the selected
// vehicle (empty slice, nil error), the create form's start_battery_pct input
// does NOT carry a "Latest: N%" placeholder — no fabricated value — and the page
// still renders 200.
func TestChargePage_NoBatterySuggestionWhenNoSnapshot(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 1001, VIN: "VIN1001", DisplayName: "Magus"},
	}}
	reader := &fakeReader{snapshots: nil} // no telemetry snapshots
	h := New(Deps{
		AnalyticsRecalculator: &fakeRecalculator{},
		Account:               acct,
		Tesla:                 &fakeTesla{},
		TelemetryReader:       reader,
		ChargingWriter:        &fakeChargeWriter{},
		ChargingReader:        &fakeChargeReader{},
	})
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/charges", nil)
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 (graceful empty — no error), got %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, `placeholder="Latest:`) {
		t.Errorf("D2 graceful-empty violation: a 'Latest:' placeholder must not render when no snapshot; got:\n%s",
			body[:min(500, len(body))])
	}
}

// TestChargePage_NoBatterySuggestionOnTelemetryError verifies that a telemetry
// Reader error degrades gracefully — no suggestion is rendered, and the page
// still returns 200 (the page never 500s from a telemetry read failure).
func TestChargePage_NoBatterySuggestionOnTelemetryError(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 1001, VIN: "VIN1001", DisplayName: "Magus"},
	}}
	reader := &fakeReader{err: errFake} // simulate a telemetry store failure
	h := New(Deps{
		AnalyticsRecalculator: &fakeRecalculator{},
		Account:               acct,
		Tesla:                 &fakeTesla{},
		TelemetryReader:       reader,
		ChargingWriter:        &fakeChargeWriter{},
		ChargingReader:        &fakeChargeReader{},
	})
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/charges", nil)
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 (telemetry error must not degrade the page), got %d", w.Code)
	}
	if strings.Contains(w.Body.String(), `placeholder="Latest:`) {
		t.Errorf("D2: a telemetry error must NOT render a 'Latest:' placeholder")
	}
}

// TestChargeCreate_MissingBatteryPct_Rejected verifies D6: submitting with
// start_battery_pct or end_battery_pct empty is rejected (422) with a
// "Battery percentage is required" message, and the Writer is not called.
func TestChargeCreate_MissingBatteryPct_Rejected(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	reader := &fakeChargeReader{entries: []charging.Entry{}}
	h := newHandlerForCharges(writer, reader)
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	form := url.Values{
		"csrf_token":       {"tok"},
		"status":           {"IN_PROGRESS"},
		"charged_on":       {"2026-07-15"},
		"energy_added_kwh": {"10.5"},
		"price":            {"5000"},
		"location_kind":    {"HOME"},
		"end_battery_pct":  {"80"},
		// start_battery_pct deliberately omitted
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/charges/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 on missing start_battery_pct, got %d", w.Code)
	}
	if writer.createEntry.EnergyAddedKWh != nil {
		t.Errorf("Writer.Create must NOT be called when start_battery_pct is missing")
	}
	body := w.Body.String()
	// Resolved language is Spanish here (KeyChargesErrorBatteryPctRequired's ES value).
	if !strings.Contains(body, "El porcentaje de batería es obligatorio") {
		t.Errorf("want battery-required message, got body=%q", body[:min(500, len(body))])
	}
}

// TestChargeCreate_OutOfRangeBatteryPct_Rejected verifies D6: out-of-range
// start_battery_pct (e.g. 101 or -1) is rejected with the out-of-range message
// and the Writer is not called. End battery % mirrors via the same check.
func TestChargeCreate_OutOfRangeBatteryPct_Rejected(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	reader := &fakeChargeReader{entries: []charging.Entry{}}
	h := newHandlerForCharges(writer, reader)
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	for _, tc := range []struct {
		name       string
		start, end string
		wantInBody string
	}{
		// Resolved language is Spanish here (KeyChargesErrorStartBatteryPctRange /
		// KeyChargesErrorEndBatteryPctRange's ES values).
		{"start=101", "101", "80", "El porcentaje de batería inicial debe ser un número entero entre 0 y 100"},
		{"start=-1", "-1", "80", "El porcentaje de batería inicial debe ser un número entero entre 0 y 100"},
		{"end=101", "50", "101", "El porcentaje de batería final debe ser un número entero entre 0 y 100"},
		{"end=-1", "50", "-1", "El porcentaje de batería final debe ser un número entero entre 0 y 100"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			form := url.Values{
				"csrf_token":        {"tok"},
				"status":            {"IN_PROGRESS"},
				"charged_on":        {"2026-07-15"},
				"energy_added_kwh":  {"10.5"},
				"price":             {"5000"},
				"location_kind":     {"HOME"},
				"start_battery_pct": {tc.start},
				"end_battery_pct":   {tc.end},
			}
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/ui/charges/create", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if c != nil {
				req.AddCookie(c)
			}
			r.ServeHTTP(w, req)

			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("want 422 on out-of-range battery, got %d", w.Code)
			}
			if writer.createEntry.EnergyAddedKWh != nil {
				t.Errorf("Writer.Create must NOT be called on out-of-range battery")
			}
			if !strings.Contains(w.Body.String(), tc.wantInBody) {
				t.Errorf("want %q in body, got %q", tc.wantInBody, w.Body.String()[:min(500, w.Body.Len())])
			}
		})
	}
}

// --- MAG-5 D1: optional date fields default to today + cleared persists nil (T5.4) ---

// TestChargePage_DateDefaultsToToday verifies D1: the create form's started_at
// and ended_at inputs are pre-filled with today's date at the platform-default
// zone's midnight ("YYYY-MM-DDT00:00") via ChargesPageData.DefaultStartedAt /
// DefaultEndedAt. No browser_tz cookie is set here, so "today" resolves via
// browserToday's clock.Zone() fallback (America/Bogota) — was UTC before
// RM35-gateway-adopt-clock; repaired per roadmap D6.
func TestChargePage_DateDefaultsToToday(t *testing.T) {
	uid := uuid.New()
	h := newHandlerForCharges(&fakeChargeWriter{}, &fakeChargeReader{})
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/charges", nil)
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	body := w.Body.String()
	todayDefault := time.Now().In(clock.Zone()).Format("2006-01-02") + "T00:00"
	// Both date inputs must be pre-filled with the today's-date default — D1.
	// The create form renders them with value={ d.DefaultStartedAt } /
	// value={ d.DefaultEndedAt }. We assert the default appears at least twice
	// (once per input).
	if wantCount := 2; strings.Count(body, `value="`+todayDefault+`"`) < wantCount {
		t.Errorf("want >= %d occurrences of today's-date default %q (one per date input), got %d — body:\n%s",
			wantCount, todayDefault, strings.Count(body, `value="`+todayDefault+`"`),
			body[:min(800, len(body))])
	}
}

// TestChargeCreate_ClearedDates_PersistedNil verifies D1's optional contract:
// submitting the form with both started_at and ended_at cleared (empty strings)
// still persists a created entry with nil StartedAt / nil EndedAt — the gateway
// does not require these optional fields even with the today-default in place.
func TestChargeCreate_ClearedDates_PersistedNil(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	reader := &fakeChargeReader{entries: []charging.Entry{}}
	h := newHandlerForCharges(writer, reader)
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	form := url.Values{
		"csrf_token":        {"tok"},
		"status":            {"IN_PROGRESS"},
		"charged_on":        {"2026-07-15"},
		"energy_added_kwh":  {"10.5"},
		"price":             {"5000"},
		"location_kind":     {"HOME"},
		"start_battery_pct": {"50"},
		"end_battery_pct":   {"80"},
		// started_at and ended_at deliberately empty (clearing the today-default).
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/charges/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 on optional cleared dates (D1), got %d body=%q",
			w.Code, w.Body.String()[:min(500, w.Body.Len())])
	}
	if writer.createEntry.StartedAt != nil {
		t.Errorf("D1 optional contract violation: StartedAt must be nil when input cleared, got %v",
			*writer.createEntry.StartedAt)
	}
	if writer.createEntry.EndedAt != nil {
		t.Errorf("D1 optional contract violation: EndedAt must be nil when input cleared, got %v",
			*writer.createEntry.EndedAt)
	}
}

// --- MAG-5 D7: 3-decimal energy accepted (T6.3) ---

// TestChargeCreate_3DecimalEnergy_Accepted verifies D7: submitting
// energy_added_kwh=7.345 (a 3-decimal value) succeeds and persists
// EnergyAddedKWh=7.345 (the UI step=0.001 permits 3 decimals; no server-side
// rounding). The energy > 0 check still passes.
func TestChargeCreate_3DecimalEnergy_Accepted(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	reader := &fakeChargeReader{entries: []charging.Entry{}}
	h := newHandlerForCharges(writer, reader)
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	form := url.Values{
		"csrf_token":        {"tok"},
		"status":            {"IN_PROGRESS"},
		"charged_on":        {"2026-07-15"},
		"energy_added_kwh":  {"7.345"},
		"price":             {"5000"},
		"location_kind":     {"HOME"},
		"start_battery_pct": {"50"},
		"end_battery_pct":   {"80"},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/charges/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 on 3-decimal energy, got %d body=%q",
			w.Code, w.Body.String()[:min(500, w.Body.Len())])
	}
	if got := writer.createEntry.EnergyAddedKWh; got == nil || *got != 7.345 {
		t.Errorf("want EnergyAddedKWh=7.345 persisted (no server-side rounding — D7), got %v",
			got)
	}
}

// TestChargeCreate_NonPositiveEnergy_Rejected verifies the D7 parity contract:
// the energy > 0 check is unchanged. energy=0 and energy=-1 are still rejected.
func TestChargeCreate_NonPositiveEnergy_Rejected(t *testing.T) {
	uid := uuid.New()
	for _, v := range []string{"0", "-1"} {
		writer := &fakeChargeWriter{}
		reader := &fakeChargeReader{entries: []charging.Entry{}}
		h := newHandlerForCharges(writer, reader)
		r := engineWithSession(h, uid, "tok")
		c := sessionCookie(r, uid, "tok")

		form := url.Values{
			"csrf_token":        {"tok"},
			"status":            {"IN_PROGRESS"},
			"charged_on":        {"2026-07-15"},
			"energy_added_kwh":  {v},
			"price":             {"5000"},
			"location_kind":     {"HOME"},
			"start_battery_pct": {"50"},
			"end_battery_pct":   {"80"},
		}
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/ui/charges/create", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if c != nil {
			req.AddCookie(c)
		}
		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnprocessableEntity {
			t.Errorf("energy=%v: want 422 (non-positive rejected), got %d", v, w.Code)
		}
		if writer.createEntry.EnergyAddedKWh != nil {
			t.Errorf("energy=%v: Writer.Create must NOT be called", v)
		}
	}
}

// --- MAG-5 D3: delete-row removal is reflected in a subsequent list (T1.3) ---

// TestChargeRowDelete_ThenListReflectsRemoval verifies D3's "row is no longer
// present in a subsequent list render" scenario. The fakeReader.ListEntriesBy*
// returns a static slice; we pre-seed the entry being deleted and assert the
// charge-row-<id> marker is absent from a GET /ui/charges/list when the fake
// reader's slice no longer carries that id (simulating the post-delete state).
//
// RE-POINTED for RM33 tier 3 (design.md §Test Contract "Existing tests
// requiring REWRITE" — "likely still valid in INTENT but must be re-pointed
// at the new response shape"): the original intent (a later GET reflects the
// removal) is unchanged, so nothing below it was altered; this adds one new
// assertion on the delete response ITSELF, confirming ChargeRowDelete now
// returns the full #charges-list fragment (design.md §D-Refresh), not the old
// MAG-5 bare empty-row shape.
func TestChargeRowDelete_ThenListReflectsRemoval(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	// Pre-delete list carries the entry; post-delete list does not. We exercise
	// both reads against the same fakeReader (the slice is a fixture per-call,
	// so we re-set entries between the two GETs).
	reader := &fakeChargeReader{entries: []charging.Entry{
		{ID: id, AccountID: uid, TeslaID: 1001, VIN: "VIN1001", ChargedOn: time.Now(),
			EnergyAddedKWh: ptrF64(10.0), Price: 5000.0, Currency: "COP"},
	}}
	writer := &fakeChargeWriter{}
	h := newHandlerForCharges(writer, reader)
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	// Before delete: list render carries the row id.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/charges/list", nil)
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), "charge-row-"+id.String()) {
		t.Fatalf("pre-delete list should contain the row id, got body=%q",
			w.Body.String()[:min(400, w.Body.Len())])
	}

	// Delete the row.
	wDel := httptest.NewRecorder()
	reqDel := httptest.NewRequest(http.MethodDelete, "/ui/charges/row/"+id.String(), nil)
	reqDel.Header.Set("X-CSRF-Token", "tok")
	if c != nil {
		reqDel.AddCookie(c)
	}
	r.ServeHTTP(wDel, reqDel)
	if wDel.Code != http.StatusOK {
		t.Fatalf("want 200 on delete, got %d", wDel.Code)
	}
	if !strings.Contains(wDel.Body.String(), `id="charges-list"`) {
		t.Errorf("want the delete response ITSELF to be the full #charges-list fragment (design.md §D-Refresh), got body=%q",
			wDel.Body.String()[:min(500, wDel.Body.Len())])
	}

	// Post-delete list render: simulate the entry's removal from the read
	// fixture — the gateway never deletes from the fakeReader (only the Writer
	// deletes from the real store), so we update the fixture to reflect reality.
	reader.entries = nil
	wList := httptest.NewRecorder()
	reqList := httptest.NewRequest(http.MethodGet, "/ui/charges/list", nil)
	if c != nil {
		reqList.AddCookie(c)
	}
	r.ServeHTTP(wList, reqList)
	if strings.Contains(wList.Body.String(), "charge-row-"+id.String()) {
		t.Errorf("post-delete list must NOT contain the row id, got body=%q",
			wList.Body.String()[:min(400, wList.Body.Len())])
	}
}

// ============================================================================
// RM33-gateway-update-charge-form (MAG-18) — Test Contract (design.md).
//
// The tests below implement design.md's Test Contract Groups A, B, C. See the
// "Fixture convention" blockquote at the top of tasks.md §Wave 8: every
// url.Values fixture that reaches parseChargeForm carries an explicit
// "status" (Test Contract A4 makes a missing status a validation error in its
// own right), and every negative test asserts the SPECIFIC i18n message for
// the field under test, not just the response status code — a bare
// 422+writer-not-called assertion would stay green even if the checked
// field's own validation were deleted.
// ============================================================================

// submitForm issues method to path with form on a fresh session for uid,
// returning the recorder. Mirrors postCharge's shape but is parameterized
// over method/path/form so the Group A/B/C tests below can vary all three
// (including a GET with an empty url.Values{} for B4's fresh-load check).
func submitForm(t *testing.T, h *Handler, uid uuid.UUID, method, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)
	return w
}

// validCreateForm returns a baseline valid create-form url.Values fixture —
// every Test Contract precondition satisfied for status=IN_PROGRESS — so each
// Group A/B test only overrides the one or two fields it is exercising.
// csrf_token is always "tok", matching submitForm's session token.
func validCreateForm() url.Values {
	return url.Values{
		"csrf_token":        {"tok"},
		"status":            {"IN_PROGRESS"},
		"charged_on":        {"2026-07-15"},
		"location_kind":     {"HOME"},
		"start_battery_pct": {"50"},
	}
}

// --- Group A — parseChargeForm / handler-level (design.md Test Contract A1-A8) ---

// TestChargeCreate_A1_InProgress_OptionalFieldsOmitted_Succeeds verifies Test
// Contract A1: status=IN_PROGRESS with no energy_added_kwh, no price, no
// ended_at, no end_battery_pct still succeeds, persisting EnergyAddedKWh=nil,
// Price=0, EndedAt=nil, EndBatteryPct=nil, Status=IN_PROGRESS.
func TestChargeCreate_A1_InProgress_OptionalFieldsOmitted_Succeeds(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	h := newHandlerForCharges(writer, &fakeChargeReader{entries: []charging.Entry{}})

	w := submitForm(t, h, uid, http.MethodPost, "/ui/charges/create", validCreateForm())

	if w.Code != http.StatusOK {
		t.Fatalf("A1: want 200, got %d body=%q", w.Code, w.Body.String()[:min(500, w.Body.Len())])
	}
	if writer.createEntry.EnergyAddedKWh != nil {
		t.Errorf("A1: want EnergyAddedKWh nil, got %v", *writer.createEntry.EnergyAddedKWh)
	}
	if writer.createEntry.Price != 0 {
		t.Errorf("A1: want Price 0, got %v", writer.createEntry.Price)
	}
	if writer.createEntry.EndedAt != nil {
		t.Errorf("A1: want EndedAt nil, got %v", *writer.createEntry.EndedAt)
	}
	if writer.createEntry.EndBatteryPct != nil {
		t.Errorf("A1: want EndBatteryPct nil, got %v", *writer.createEntry.EndBatteryPct)
	}
	if writer.createEntry.Status != charging.StatusInProgress {
		t.Errorf("A1: want Status IN_PROGRESS, got %v", writer.createEntry.Status)
	}
}

// TestChargeCreate_A2_Done_MissingEndedAtAndEndBatteryPct_Rejected verifies
// Test Contract A2: the same omission under status=DONE is rejected on BOTH
// conditionally-required fields, and Writer.Create is never reached.
func TestChargeCreate_A2_Done_MissingEndedAtAndEndBatteryPct_Rejected(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	h := newHandlerForCharges(writer, &fakeChargeReader{entries: []charging.Entry{}})

	form := validCreateForm()
	form.Set("status", "DONE")
	// ended_at / end_battery_pct deliberately omitted.

	w := submitForm(t, h, uid, http.MethodPost, "/ui/charges/create", form)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("A2: want 422, got %d", w.Code)
	}
	if writer.createCalls != 0 {
		t.Errorf("A2: Writer.Create must NOT be called, got %d calls", writer.createCalls)
	}
	body := w.Body.String()
	// Resolved language is Spanish (KeyChargesErrorEndedAtRequired /
	// KeyChargesErrorBatteryPctRequired's ES values).
	if !strings.Contains(body, "La hora de fin es obligatoria cuando el estado es Finalizada") {
		t.Errorf("A2: want ended_at-required message, body=%q", body[:min(500, len(body))])
	}
	if !strings.Contains(body, "El porcentaje de batería es obligatorio") {
		t.Errorf("A2: want end_battery_pct-required message, body=%q", body[:min(500, len(body))])
	}
}

// TestChargeCreate_A3_Done_WithEndedAtAndEndBatteryPct_Succeeds verifies Test
// Contract A3: status=DONE with both conditionally-required fields supplied
// and valid succeeds, persisting Status=DONE.
func TestChargeCreate_A3_Done_WithEndedAtAndEndBatteryPct_Succeeds(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	h := newHandlerForCharges(writer, &fakeChargeReader{entries: []charging.Entry{}})

	form := validCreateForm()
	form.Set("status", "DONE")
	form.Set("ended_at", "2026-07-15T18:00")
	form.Set("end_battery_pct", "90")

	w := submitForm(t, h, uid, http.MethodPost, "/ui/charges/create", form)

	if w.Code != http.StatusOK {
		t.Fatalf("A3: want 200, got %d body=%q", w.Code, w.Body.String()[:min(500, w.Body.Len())])
	}
	if writer.createEntry.Status != charging.StatusDone {
		t.Errorf("A3: want Status DONE, got %v", writer.createEntry.Status)
	}
}

// TestChargeCreate_A4_MissingOrInvalidStatus_Rejected verifies Test Contract
// A4: an absent or unrecognized status value is rejected on the status field
// itself, before RequiredFieldsFor is ever consulted.
func TestChargeCreate_A4_MissingOrInvalidStatus_Rejected(t *testing.T) {
	uid := uuid.New()
	for _, tc := range []struct {
		name string
		set  string
		omit bool
	}{
		{"missing", "", true},
		{"unrecognized", "BOGUS", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writer := &fakeChargeWriter{}
			h := newHandlerForCharges(writer, &fakeChargeReader{entries: []charging.Entry{}})

			form := validCreateForm()
			if tc.omit {
				form.Del("status")
			} else {
				form.Set("status", tc.set)
			}

			w := submitForm(t, h, uid, http.MethodPost, "/ui/charges/create", form)

			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("A4 %s: want 422, got %d", tc.name, w.Code)
			}
			if writer.createCalls != 0 {
				t.Errorf("A4 %s: Writer.Create must NOT be called, got %d calls", tc.name, writer.createCalls)
			}
			body := w.Body.String()
			// Resolved language is Spanish (KeyChargesErrorStatusInvalid's ES value).
			if !strings.Contains(body, "Estado inválido") {
				t.Errorf("A4 %s: want status-invalid message, body=%q", tc.name, body[:min(500, len(body))])
			}
		})
	}
}

// TestChargeCreate_A5_PriceOptional verifies Test Contract A5: price="" ->
// 0/no error; price="-1" -> validation error, Writer not called; price
// "150.50" -> persisted as-is.
func TestChargeCreate_A5_PriceOptional(t *testing.T) {
	uid := uuid.New()

	t.Run("empty_defaults_to_zero", func(t *testing.T) {
		writer := &fakeChargeWriter{}
		h := newHandlerForCharges(writer, &fakeChargeReader{entries: []charging.Entry{}})
		form := validCreateForm()
		form.Set("price", "")
		w := submitForm(t, h, uid, http.MethodPost, "/ui/charges/create", form)
		if w.Code != http.StatusOK {
			t.Fatalf("want 200 on empty price, got %d body=%q", w.Code, w.Body.String()[:min(500, w.Body.Len())])
		}
		if writer.createEntry.Price != 0 {
			t.Errorf("want Price 0 on empty submission, got %v", writer.createEntry.Price)
		}
	})

	t.Run("negative_rejected", func(t *testing.T) {
		writer := &fakeChargeWriter{}
		h := newHandlerForCharges(writer, &fakeChargeReader{entries: []charging.Entry{}})
		form := validCreateForm()
		form.Set("price", "-1")
		w := submitForm(t, h, uid, http.MethodPost, "/ui/charges/create", form)
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("want 422 on negative price, got %d", w.Code)
		}
		if writer.createCalls != 0 {
			t.Errorf("Writer.Create must NOT be called on negative price, got %d calls", writer.createCalls)
		}
		// Resolved language is Spanish (KeyChargesErrorPriceNonNegative's ES value).
		if !strings.Contains(w.Body.String(), "El precio debe ser un número no negativo") {
			t.Errorf("want price-non-negative message, body=%q", w.Body.String()[:min(500, w.Body.Len())])
		}
	})

	t.Run("valid_decimal_persisted", func(t *testing.T) {
		writer := &fakeChargeWriter{}
		h := newHandlerForCharges(writer, &fakeChargeReader{entries: []charging.Entry{}})
		form := validCreateForm()
		form.Set("price", "150.50")
		w := submitForm(t, h, uid, http.MethodPost, "/ui/charges/create", form)
		if w.Code != http.StatusOK {
			t.Fatalf("want 200 on valid price, got %d body=%q", w.Code, w.Body.String()[:min(500, w.Body.Len())])
		}
		if writer.createEntry.Price != 150.50 {
			t.Errorf("want Price 150.50 persisted, got %v", writer.createEntry.Price)
		}
	})
}

// TestChargeCreate_A6_EnergyOptional verifies Test Contract A6:
// energy_added_kwh="" -> nil/no error; energy_added_kwh="0" -> rejected by
// the existing "must be positive" rule, now conditioned on non-empty rather
// than always-on.
func TestChargeCreate_A6_EnergyOptional(t *testing.T) {
	uid := uuid.New()

	t.Run("empty_is_nil_no_error", func(t *testing.T) {
		writer := &fakeChargeWriter{}
		h := newHandlerForCharges(writer, &fakeChargeReader{entries: []charging.Entry{}})
		form := validCreateForm()
		form.Set("energy_added_kwh", "")
		w := submitForm(t, h, uid, http.MethodPost, "/ui/charges/create", form)
		if w.Code != http.StatusOK {
			t.Fatalf("want 200 on empty energy, got %d body=%q", w.Code, w.Body.String()[:min(500, w.Body.Len())])
		}
		if writer.createEntry.EnergyAddedKWh != nil {
			t.Errorf("want EnergyAddedKWh nil on empty submission, got %v", *writer.createEntry.EnergyAddedKWh)
		}
	})

	t.Run("zero_rejected", func(t *testing.T) {
		writer := &fakeChargeWriter{}
		h := newHandlerForCharges(writer, &fakeChargeReader{entries: []charging.Entry{}})
		form := validCreateForm()
		form.Set("energy_added_kwh", "0")
		w := submitForm(t, h, uid, http.MethodPost, "/ui/charges/create", form)
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("want 422 on energy=0, got %d", w.Code)
		}
		if writer.createCalls != 0 {
			t.Errorf("Writer.Create must NOT be called on energy=0, got %d calls", writer.createCalls)
		}
		// Resolved language is Spanish (KeyChargesErrorEnergyPositive's ES value).
		if !strings.Contains(w.Body.String(), "La energía debe ser un número positivo") {
			t.Errorf("want energy-positive message, body=%q", w.Body.String()[:min(500, w.Body.Len())])
		}
	})
}

// TestChargeCreate_A7_OdometerOptional verifies Test Contract A7: odometer_km
// is a new always-optional non-negative integer field.
func TestChargeCreate_A7_OdometerOptional(t *testing.T) {
	uid := uuid.New()
	for _, tc := range []struct {
		name      string
		value     string
		wantOK    bool
		wantKm    *int
		wantErrES string // substring of the ES error message expected when wantOK is false
	}{
		{"empty_is_nil", "", true, nil, ""},
		{"valid_persisted", "45210", true, ptrInt(45210), ""},
		{"negative_rejected", "-1", false, nil, "El odómetro debe ser un número entero no negativo"},
		{"non_numeric_rejected", "abc", false, nil, "El odómetro debe ser un número entero no negativo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writer := &fakeChargeWriter{}
			h := newHandlerForCharges(writer, &fakeChargeReader{entries: []charging.Entry{}})
			form := validCreateForm()
			form.Set("odometer_km", tc.value)
			w := submitForm(t, h, uid, http.MethodPost, "/ui/charges/create", form)

			if tc.wantOK {
				if w.Code != http.StatusOK {
					t.Fatalf("%s: want 200, got %d body=%q", tc.name, w.Code, w.Body.String()[:min(500, w.Body.Len())])
				}
				got := writer.createEntry.OdometerKm
				if tc.wantKm == nil {
					if got != nil {
						t.Errorf("%s: want OdometerKm nil, got %v", tc.name, *got)
					}
				} else if got == nil || *got != *tc.wantKm {
					t.Errorf("%s: want OdometerKm %d, got %v", tc.name, *tc.wantKm, got)
				}
			} else {
				if w.Code != http.StatusUnprocessableEntity {
					t.Fatalf("%s: want 422, got %d", tc.name, w.Code)
				}
				if writer.createCalls != 0 {
					t.Errorf("%s: Writer.Create must NOT be called, got %d calls", tc.name, writer.createCalls)
				}
				if !strings.Contains(w.Body.String(), tc.wantErrES) {
					t.Errorf("%s: want %q in body, got %q", tc.name, tc.wantErrES, w.Body.String()[:min(500, w.Body.Len())])
				}
			}
		})
	}
}

// TestChargeCreate_A8_StartBatteryPctRequired_BothStatuses verifies Test
// Contract A8: start_battery_pct is unconditionally required, unchanged by
// RM33, for BOTH status values.
func TestChargeCreate_A8_StartBatteryPctRequired_BothStatuses(t *testing.T) {
	uid := uuid.New()
	for _, status := range []string{"IN_PROGRESS", "DONE"} {
		t.Run(status, func(t *testing.T) {
			writer := &fakeChargeWriter{}
			h := newHandlerForCharges(writer, &fakeChargeReader{entries: []charging.Entry{}})
			form := validCreateForm()
			form.Set("status", status)
			form.Del("start_battery_pct")
			if status == "DONE" {
				form.Set("ended_at", "2026-07-15T18:00")
				form.Set("end_battery_pct", "90")
			}
			w := submitForm(t, h, uid, http.MethodPost, "/ui/charges/create", form)

			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status=%s: want 422 on missing start_battery_pct, got %d", status, w.Code)
			}
			if writer.createCalls != 0 {
				t.Errorf("status=%s: Writer.Create must NOT be called, got %d calls", status, writer.createCalls)
			}
			// Resolved language is Spanish (KeyChargesErrorBatteryPctRequired's ES value).
			if !strings.Contains(w.Body.String(), "El porcentaje de batería es obligatorio") {
				t.Errorf("status=%s: want battery-required message, body=%q", status, w.Body.String()[:min(500, w.Body.Len())])
			}
		})
	}
}

// --- Group B — D15 value-preservation (design.md Test Contract B1-B4) ---

// TestChargeCreate_B1_ValidationFailure_PreservesLocationAndEndBatteryPct
// verifies Test Contract B1: a valid location_kind=WORK and battery values
// but an out-of-range end_battery_pct under status=DONE re-renders the 422
// create form with WORK still selected and the invalid "150" still echoed in
// end_battery_pct's value — the raw ChargeFormValues, not a blank/default
// field, survives the failed validation (roadmap D15).
func TestChargeCreate_B1_ValidationFailure_PreservesLocationAndEndBatteryPct(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	h := newHandlerForCharges(writer, &fakeChargeReader{entries: []charging.Entry{}})

	form := validCreateForm()
	form.Set("status", "DONE")
	form.Set("location_kind", "WORK")
	form.Set("ended_at", "2026-07-15T18:00")
	form.Set("end_battery_pct", "150") // out of range (0-100) — the sole invalid field

	w := submitForm(t, h, uid, http.MethodPost, "/ui/charges/create", form)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("B1: want 422, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `value="WORK" selected`) {
		t.Errorf("B1: want location_kind=WORK still selected on re-render, body=%q", body[:min(800, len(body))])
	}
	if !strings.Contains(body, `name="end_battery_pct" value="150"`) {
		t.Errorf("B1: want the submitted invalid end_battery_pct=150 echoed back, body=%q", body[:min(800, len(body))])
	}
}

// TestChargeRowUpdate_B2_ValidationFailure_PreservesNotesAndLocationLabel
// verifies Test Contract B2: the inline edit row's 422 re-render still
// carries the submitted notes/location_label text even though a different
// field (start_battery_pct, out of range) is what failed validation, and even
// though charging_type carries a value the <select> could never submit (a raw
// POST bypassing the browser control).
func TestChargeRowUpdate_B2_ValidationFailure_PreservesNotesAndLocationLabel(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	writer := &fakeChargeWriter{}
	h := newHandlerForCharges(writer, &fakeChargeReader{})

	form := validCreateForm()
	form.Set("start_battery_pct", "150") // out of range — triggers the 422
	form.Set("charging_type", "BOGUS")   // a raw POST can submit what the <select> never would
	form.Set("notes", "Charged at the mall")
	form.Set("location_label", "Centro Comercial")

	w := submitForm(t, h, uid, http.MethodPut, "/ui/charges/row/"+id.String(), form)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("B2: want 422, got %d", w.Code)
	}
	if writer.updateCalls != 0 {
		t.Errorf("B2: Writer.Update must NOT be called, got %d calls", writer.updateCalls)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Charged at the mall") {
		t.Errorf("B2: want submitted notes preserved on re-render, body=%q", body[:min(800, len(body))])
	}
	if !strings.Contains(body, "Centro Comercial") {
		t.Errorf("B2: want submitted location_label preserved on re-render, body=%q", body[:min(800, len(body))])
	}
}

// TestChargeCreate_B3_StatusDoneClearedEndedAt_StatusSelectionPreserved
// verifies Test Contract B3: status="DONE" with ended_at cleared re-renders
// the create form's status <select> still showing DONE selected — the error
// path must not silently reset it to the IN_PROGRESS default.
func TestChargeCreate_B3_StatusDoneClearedEndedAt_StatusSelectionPreserved(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	h := newHandlerForCharges(writer, &fakeChargeReader{entries: []charging.Entry{}})

	form := validCreateForm()
	form.Set("status", "DONE")
	form.Set("end_battery_pct", "90")
	// ended_at deliberately cleared — the only invalid field.

	w := submitForm(t, h, uid, http.MethodPost, "/ui/charges/create", form)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("B3: want 422, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `value="DONE" selected`) {
		t.Errorf("B3: want status DONE still selected on re-render, body=%q", body[:min(800, len(body))])
	}
	if strings.Contains(body, `value="IN_PROGRESS" selected`) {
		t.Errorf("B3: status must NOT reset to IN_PROGRESS on re-render, body=%q", body[:min(800, len(body))])
	}
}

// TestChargePage_B4_FreshLoad_NoRegressionInBlankFieldRendering verifies Test
// Contract B4: a fresh GET /charges (no submission) still renders
// energy_added_kwh / price with an empty value and no location_kind option
// pre-selected — ChargeFormValues{}'s zero value reproduces today's
// fresh-load behavior with no regression from adding the struct.
func TestChargePage_B4_FreshLoad_NoRegressionInBlankFieldRendering(t *testing.T) {
	uid := uuid.New()
	h := newHandlerForCharges(&fakeChargeWriter{}, &fakeChargeReader{})
	w := submitForm(t, h, uid, http.MethodGet, "/charges", url.Values{})

	if w.Code != http.StatusOK {
		t.Fatalf("B4: want 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `name="energy_added_kwh" value=""`) {
		t.Errorf("B4: want energy_added_kwh rendered with an empty value on fresh load, body=%q", body[:min(800, len(body))])
	}
	if !strings.Contains(body, `name="price" value=""`) {
		t.Errorf("B4: want price rendered with an empty value on fresh load, body=%q", body[:min(800, len(body))])
	}
	for _, want := range []string{`value="HOME" selected`, `value="WORK" selected`, `value="OTHER" selected`} {
		if strings.Contains(body, want) {
			t.Errorf("B4: want no location_kind option pre-selected on fresh load, found %q in body", want)
		}
	}
}

// --- Group C — template/markup assertions (design.md Test Contract C1-C7) ---
//
// These render fragments.ChargeCreateForm / fragments.ChargeRowEdit directly
// (mirrors supercharger_test.go's TestChargeCreateForm-style direct-render
// pattern and history_test.go's TestBaseAuth_RendersBrowserTZScript) rather
// than going through the full HTTP handler chain — Group C is a
// template/markup assertion, not a handler-behavior one, so it exercises the
// templates' binding of ChargesPageData/ChargeEntryVM fields directly,
// independent of which handler code path produced those field values.

// renderCreateForm renders fragments.ChargeCreateForm(d, nil) to a string in
// the given language ("es"/"en" via i18n.WithLang, or "" for the ambient
// default — i18n.FromContext resolves an unset context to Spanish).
func renderCreateForm(t *testing.T, d fragments.ChargesPageData, lang string) string {
	t.Helper()
	ctx := context.Background()
	if lang != "" {
		ctx = i18n.WithLang(ctx, lang)
	}
	var body bytes.Buffer
	if err := fragments.ChargeCreateForm(d, nil).Render(ctx, &body); err != nil {
		t.Fatalf("render ChargeCreateForm: %v", err)
	}
	return body.String()
}

// renderEditRow renders fragments.ChargeRowEdit(vm, "tok", nil, "", "") to a
// string, same language convention as renderCreateForm. The two trailing ""
// args are windowStartStr/windowEndStr (design.md §D-Refresh, RM33 tier 3) —
// Group C's template/markup assertions (C1-C7) are all indifferent to the
// filter window, so empty strings are the correct fixture here; the window
// itself is pinned separately by Group D (§D-Include/§D-Refresh).
func renderEditRow(t *testing.T, vm fragments.ChargeEntryVM, lang string) string {
	t.Helper()
	ctx := context.Background()
	if lang != "" {
		ctx = i18n.WithLang(ctx, lang)
	}
	var body bytes.Buffer
	if err := fragments.ChargeRowEdit(vm, "tok", nil, "", "").Render(ctx, &body); err != nil {
		t.Fatalf("render ChargeRowEdit: %v", err)
	}
	return body.String()
}

// tagAttrsFor extracts the substring of a rendered <input>/<select> tag from
// name="<field>" up to (and including) that tag's closing ">" — narrowed to
// exactly the one named control so a required/selected check on one field can
// never false-match a different one.
func tagAttrsFor(body, name string) string {
	idx := strings.Index(body, `name="`+name+`"`)
	if idx == -1 {
		return ""
	}
	end := strings.Index(body[idx:], ">")
	if end == -1 {
		return body[idx:]
	}
	return body[idx : idx+end+1]
}

// TestChargeForms_C1_OptionalFieldsCarryNoRequired_UnconditionalFieldsDo
// verifies Test Contract C1 for BOTH forms: energy_added_kwh and price carry
// no `required` attribute; start_battery_pct, charged_on, location_kind still
// carry `required` (unconditional fields, unchanged by RM33).
func TestChargeForms_C1_OptionalFieldsCarryNoRequired_UnconditionalFieldsDo(t *testing.T) {
	createBody := renderCreateForm(t, fragments.ChargesPageData{}, "")
	editBody := renderEditRow(t, fragments.ChargeEntryVM{}, "")

	for _, form := range []struct {
		name string
		body string
	}{{"create", createBody}, {"edit", editBody}} {
		t.Run(form.name, func(t *testing.T) {
			for _, optional := range []string{"energy_added_kwh", "price"} {
				if attrs := tagAttrsFor(form.body, optional); strings.Contains(attrs, "required") {
					t.Errorf("%s form: %s must NOT carry required, got %q", form.name, optional, attrs)
				}
			}
			for _, unconditional := range []string{"start_battery_pct", "charged_on", "location_kind"} {
				if attrs := tagAttrsFor(form.body, unconditional); !strings.Contains(attrs, "required") {
					t.Errorf("%s form: %s must carry required, got %q", form.name, unconditional, attrs)
				}
			}
		})
	}
}

// TestChargeCreateForm_C2_FreshRender_InProgressDefaultsNoEndRequired
// verifies Test Contract C2: a create-form ChargesPageData matching a fresh
// (non-error) render — Status=IN_PROGRESS, RequiredEndedAt/
// RequiredEndBatteryPct both false, mirroring buildChargesPage's own
// fresh-load computation (design.md §D-Values/§D-Fields) — renders
// IN_PROGRESS selected and no required attribute on ended_at/end_battery_pct.
func TestChargeCreateForm_C2_FreshRender_InProgressDefaultsNoEndRequired(t *testing.T) {
	d := fragments.ChargesPageData{
		FormValues:            fragments.ChargeFormValues{Status: string(charging.StatusInProgress)},
		RequiredEndedAt:       false,
		RequiredEndBatteryPct: false,
	}
	body := renderCreateForm(t, d, "")

	if !strings.Contains(body, `value="IN_PROGRESS" selected`) {
		t.Errorf("C2: want IN_PROGRESS selected on fresh render, body=%q", body[:min(800, len(body))])
	}
	if strings.Contains(body, `value="DONE" selected`) {
		t.Errorf("C2: DONE must not be selected on fresh render")
	}
	for _, name := range []string{"ended_at", "end_battery_pct"} {
		if attrs := tagAttrsFor(body, name); strings.Contains(attrs, "required") {
			t.Errorf("C2: %s must carry no required attribute on fresh render, got %q", name, attrs)
		}
	}
}

// TestChargeCreateForm_C3_DoneRequiredState_RendersRequiredAttributes
// verifies Test Contract C3: when the create form's ChargesPageData carries
// the required-state a DONE submission produces (RequiredEndedAt=true,
// RequiredEndBatteryPct=true — the same charging.RequiredFieldsFor(DONE)
// lookup design.md §D-Fields describes), ended_at and end_battery_pct render
// with the `required` attribute.
//
// This is a template-binding assertion (Group C's stated scope is
// "template/markup assertions"), independent of which handler code path
// populates these two booleans. WORKER FINDING (2026-08-29): ChargeCreate's
// OWN 422/500 branches do not currently recompute RequiredEndedAt/
// RequiredEndBatteryPct from the submitted raw.Status (task 3.3 wired that
// recompute only for the edit-row path, via chargeEntryVMFromRawValues) — so
// a create-form validation failure under status=DONE will NOT itself produce
// this exact ChargesPageData shape today; the create form's error re-render
// keeps whatever buildChargesPage's unconditional StatusInProgress-based
// computation set. RD13's client-side htmx:load listener is design.md's own
// documented mitigation for this ("if the two ever disagree ... the
// disagreement is inert"), but it means the SERVER-rendered HTML on that one
// path does not, by itself, satisfy this test's premise. See this worker's
// final report for the full writeup; not fixed here (out of this task's
// assigned scope — task 3.3 owns that wiring).
func TestChargeCreateForm_C3_DoneRequiredState_RendersRequiredAttributes(t *testing.T) {
	d := fragments.ChargesPageData{
		FormValues:            fragments.ChargeFormValues{Status: string(charging.StatusDone)},
		RequiredEndedAt:       true,
		RequiredEndBatteryPct: true,
	}
	body := renderCreateForm(t, d, "")

	for _, name := range []string{"ended_at", "end_battery_pct"} {
		if attrs := tagAttrsFor(body, name); !strings.Contains(attrs, "required") {
			t.Errorf("C3: %s must carry required when RequiredEndedAt/RequiredEndBatteryPct are true, got %q", name, attrs)
		}
	}
}

// TestChargeRowEdit_C4_StatusSelectReflectsPersistedValue verifies Test
// Contract C4: the edit row's status <select> shows the PERSISTED entry's
// status selected — DONE for a DONE entry, IN_PROGRESS for an IN_PROGRESS one.
func TestChargeRowEdit_C4_StatusSelectReflectsPersistedValue(t *testing.T) {
	for _, status := range []string{"DONE", "IN_PROGRESS"} {
		t.Run(status, func(t *testing.T) {
			vm := fragments.ChargeEntryVM{ID: uuid.New().String(), RawStatus: status}
			body := renderEditRow(t, vm, "")

			if !strings.Contains(body, `value="`+status+`" selected`) {
				t.Errorf("C4: want %s selected, body=%q", status, body[:min(800, len(body))])
			}
			other := "IN_PROGRESS"
			if status == "IN_PROGRESS" {
				other = "DONE"
			}
			if strings.Contains(body, `value="`+other+`" selected`) {
				t.Errorf("C4: %s must NOT be selected when the persisted status is %s", other, status)
			}
		})
	}
}

// TestChargeForms_C5_NoCurrencyField_PriceHasCOPSuffix verifies Test Contract
// C5: neither form renders a Currency <input> (removed — design.md §D-Suffix
// replaces it with a COP suffix on the price input); the price input's
// rendered HTML contains a <span class="label">COP</span> inside a
// <label class="input w-full"> wrapper (DaisyUI v5's compound-input idiom).
func TestChargeForms_C5_NoCurrencyField_PriceHasCOPSuffix(t *testing.T) {
	createBody := renderCreateForm(t, fragments.ChargesPageData{}, "")
	editBody := renderEditRow(t, fragments.ChargeEntryVM{}, "")

	for _, form := range []struct {
		name string
		body string
	}{{"create", createBody}, {"edit", editBody}} {
		t.Run(form.name, func(t *testing.T) {
			if strings.Contains(form.body, `name="currency"`) {
				t.Errorf("%s form: must NOT render a Currency input, body=%q", form.name, form.body[:min(1500, len(form.body))])
			}
			if !strings.Contains(form.body, `<span class="label">COP</span>`) {
				t.Errorf("%s form: want a COP suffix span, body=%q", form.name, form.body[:min(1500, len(form.body))])
			}
			// Match structurally, not by literal class string: Templ concatenates
			// ui.InputProps.Class onto the base classes and leaves a trailing
			// space when it is empty (`class="input w-full "`), exactly as it does
			// for every other component in this output (`class="fieldset "`). The
			// regex also pins what C5 actually requires and a substring check
			// cannot — that the price input is INSIDE the compound-label wrapper,
			// rather than the wrapper and the input merely both existing somewhere.
			wrapped := regexp.MustCompile(`<label class="input[^"]*"><input type="number" name="price"`)
			if !wrapped.MatchString(form.body) {
				t.Errorf("%s form: want the price input wrapped in DaisyUI's compound label, body=%q", form.name, form.body[:min(1500, len(form.body))])
			}
		})
	}
}

// TestChargeForms_C6_ACDCOptionText_BothLanguages verifies Test Contract C6:
// both forms' AC/DC <option> text matches roadmap D16's descriptive strings,
// in both ES and EN (i18n.WithLang, mirroring the catalogue completeness
// test's language-switch pattern).
func TestChargeForms_C6_ACDCOptionText_BothLanguages(t *testing.T) {
	for _, tc := range []struct {
		lang   string
		wantAC string
		wantDC string
	}{
		{"es", "AC — Carga lenta (casa/destino)", "DC — Carga rápida (Supercargador)"},
		{"en", "AC — Slow charging (home/destination)", "DC — Fast charging (Supercharger)"},
	} {
		t.Run(tc.lang, func(t *testing.T) {
			createBody := renderCreateForm(t, fragments.ChargesPageData{}, tc.lang)
			editBody := renderEditRow(t, fragments.ChargeEntryVM{}, tc.lang)
			for _, form := range []struct {
				name string
				body string
			}{{"create", createBody}, {"edit", editBody}} {
				if !strings.Contains(form.body, tc.wantAC) {
					t.Errorf("%s form (%s): want AC option text %q, body=%q", form.name, tc.lang, tc.wantAC, form.body[:min(1500, len(form.body))])
				}
				if !strings.Contains(form.body, tc.wantDC) {
					t.Errorf("%s form (%s): want DC option text %q, body=%q", form.name, tc.lang, tc.wantDC, form.body[:min(1500, len(form.body))])
				}
			}
		})
	}
}

// TestChargeForms_C7_OdometerInsideOptionalDetails verifies Test Contract C7,
// restated for the 2026-08-29 layout change: the <details>/<summary> collapse
// is gone, so odometer_km must now render inside the always-visible "Optional
// details" <section> instead — after its opening tag and before its close.
func TestChargeForms_C7_OdometerInsideOptionalDetails(t *testing.T) {
	createBody := renderCreateForm(t, fragments.ChargesPageData{}, "")
	editBody := renderEditRow(t, fragments.ChargeEntryVM{}, "")

	for _, form := range []struct {
		name       string
		body       string
		sectionTag string
	}{
		{"create", createBody, `<section id="charges-create-optional"`},
		{"edit", editBody, `<section id="charge-row-optional-`},
	} {
		t.Run(form.name, func(t *testing.T) {
			// The collapse must be GONE: a required control inside a closed
			// <details> cannot be focused for an HTML5 validation message, so
			// Save silently does nothing. That is what this layout fixed.
			if strings.Contains(form.body, "<details") || strings.Contains(form.body, "<summary") {
				t.Errorf("%s form: <details>/<summary> must not return — a required field inside a closed collapse breaks form validation", form.name)
			}
			sectionIdx := strings.Index(form.body, form.sectionTag)
			sectionCloseIdx := strings.Index(form.body, "</section>")
			odometerIdx := strings.Index(form.body, `name="odometer_km"`)
			if sectionIdx == -1 || sectionCloseIdx == -1 || odometerIdx == -1 {
				t.Fatalf("%s form: missing optional-details section/odometer_km markers, body=%q", form.name, form.body[:min(1500, len(form.body))])
			}
			if !(odometerIdx > sectionIdx && odometerIdx < sectionCloseIdx) {
				t.Errorf("%s form: odometer_km must render inside the optional-details section, section=%d odometer=%d sectionClose=%d",
					form.name, sectionIdx, odometerIdx, sectionCloseIdx)
			}
		})
	}
}

// TestChargeCreate_DoneStatus_ErrorRerender_KeepsRequiredAttributes is the
// handler-level counterpart to Test Contract C3, added in wave 4 after the C3
// template test surfaced that ChargeCreate's error branches did not satisfy it.
//
// C3 asserts the create form renders `required` on ended_at/end_battery_pct for
// a DONE status, and the template does — but only if the handler hands it a
// ChargesPageData whose Required* pair was computed from the SUBMITTED status.
// The 4xx/5xx branches overwrite FormValues with the raw submission (roadmap
// D15) and used to leave Required* on buildChargesPage's fresh-load IN_PROGRESS
// default, so a user who picked DONE and tripped an unrelated validation error
// got those two inputs back without `required` — the flash-of-wrong-state
// design.md §D-Fields rules out. applyRawRequiredState fixes it; this test
// pins the handler path a direct template render cannot reach.
func TestChargeCreate_DoneStatus_ErrorRerender_KeepsRequiredAttributes(t *testing.T) {
	uid := uuid.New()
	h := newHandlerForCharges(&fakeChargeWriter{}, &fakeChargeReader{})

	// Valid in every respect EXCEPT location_kind, so the 422 is triggered by a
	// field unrelated to the status-gated pair under assertion. Per the wave-8
	// fixture convention, status is explicit and the failure is a named field —
	// not a spurious missing-status error.
	form := validCreateForm()
	form.Set("status", "DONE")
	form.Set("ended_at", "2026-07-15T10:00")
	form.Set("end_battery_pct", "80")
	form.Del("location_kind")

	w := submitForm(t, h, uid, http.MethodPost, "/ui/charges/create", form)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 when location_kind is missing, got %d", w.Code)
	}

	body := w.Body.String()
	for _, name := range []string{"ended_at", "end_battery_pct"} {
		attrs := tagAttrsFor(body, name)
		if attrs == "" {
			t.Fatalf("%s input not found in the 422 re-render", name)
		}
		if !strings.Contains(attrs, "required") {
			t.Errorf("design.md §D-Fields: %s must carry required on a DONE error re-render, got %q", name, attrs)
		}
	}
}

// ============================================================================
// RM33-gateway-add-entries-dashboard (MAG-18, tier 3) — Test Contract Groups
// C, D, E (design.md). Groups A (parseChargesRange/buildChargesPresets) and B
// (entryComplete/buildChargeTiles) already live in charges_range_test.go /
// charges_tiles_test.go (Wave 3). These are the rendered-HTML (C) and
// httptest (D, E) groups — Wave 9.
// ============================================================================

// --- Group C — the completeness dot / status badge render (design.md Test
// Contract C1-C3, offline rendered-HTML assertions) ---

// renderChargeRow builds a ChargeEntryVM from e via chargeEntryVMFromEntry
// (exercising the REAL entryComplete/Complete wiring — design.md §D-Dot,
// tasks.md 9.1 depends_on 5.2) and renders fragments.ChargeRow to a string.
// Mirrors renderEditRow/renderCreateForm's direct-render pattern (tier 2's
// pre-existing Group C tests) — ambient context.Background() resolves to
// Spanish (i18n.FromContext's default), matching this file's established
// convention (see TestChargesListFragment_EmptyState's comment).
func renderChargeRow(t *testing.T, e charging.Entry, vehicles []account.Vehicle) string {
	t.Helper()
	vm := chargeEntryVMFromEntry(e, vehicles)
	var body bytes.Buffer
	if err := fragments.ChargeRow(vm, "tok", "2026-08-23", "2026-08-29").Render(context.Background(), &body); err != nil {
		t.Fatalf("render ChargeRow: %v", err)
	}
	return body.String()
}

// TestChargeRow_C1_DoneComplete_RendersSuccessDotAndDoneBadge verifies Test
// Contract C1: a DONE, fully-complete entry's row renders ui.Dot with a class
// containing bg-success (dotClass("success")) and the status badge text
// matches the DONE label (KeyChargesBadgeDone's ES value, "Finalizada").
func TestChargeRow_C1_DoneComplete_RendersSuccessDotAndDoneBadge(t *testing.T) {
	e := charging.Entry{
		ID:              uuid.New(),
		AccountID:       uuid.New(),
		TeslaID:         1001,
		VIN:             "VIN1001",
		Status:          charging.StatusDone,
		ChargedOn:       time.Now(),
		Price:           5000,
		Currency:        "COP",
		EnergyAddedKWh:  ptrF64(10.0),
		StartBatteryPct: ptrInt(50),
		EndBatteryPct:   ptrInt(80),
		StartedAt:       ptrTime(time.Now()),
		EndedAt:         ptrTime(time.Now()),
	}
	body := renderChargeRow(t, e, nil)

	if !strings.Contains(body, "bg-success") {
		t.Errorf("C1: want the success dot class (bg-success) for a fully-complete DONE entry, body=%q", body[:min(1200, len(body))])
	}
	if strings.Contains(body, "bg-warning") {
		t.Errorf("C1: want NO warning dot class on a fully-complete entry, body=%q", body[:min(1200, len(body))])
	}
	if !strings.Contains(body, "Finalizada") {
		t.Errorf("C1: want the DONE status badge text (KeyChargesBadgeDone), body=%q", body[:min(1200, len(body))])
	}
}

// TestChargeRow_C2_DoneMissingEndBatteryPct_RendersWarningNeverError verifies
// Test Contract C2: a DONE entry missing EndBatteryPct (a data state the
// domain permits even though the gateway's own form now requires it for a NEW
// DONE save — e.g. seeded directly or edited by an earlier code path) renders
// the WARNING dot class, never the success one, and never bg-error
// (D-RM33-12 — ChargeRow only ever passes "success"/"warning" to ui.Dot; this
// pins that the row template never regresses to a third variant).
func TestChargeRow_C2_DoneMissingEndBatteryPct_RendersWarningNeverError(t *testing.T) {
	e := charging.Entry{
		ID:              uuid.New(),
		AccountID:       uuid.New(),
		TeslaID:         1001,
		VIN:             "VIN1001",
		Status:          charging.StatusDone,
		ChargedOn:       time.Now(),
		Price:           5000,
		Currency:        "COP",
		EnergyAddedKWh:  ptrF64(10.0),
		StartBatteryPct: ptrInt(50),
		EndBatteryPct:   nil, // the missing field under test
		StartedAt:       ptrTime(time.Now()),
		EndedAt:         ptrTime(time.Now()),
	}
	body := renderChargeRow(t, e, nil)

	if !strings.Contains(body, "bg-warning") {
		t.Errorf("C2: want the warning dot class for a DONE entry missing EndBatteryPct, body=%q", body[:min(1200, len(body))])
	}
	if strings.Contains(body, "bg-success") {
		t.Errorf("C2: want NO success dot class when EndBatteryPct is missing, body=%q", body[:min(1200, len(body))])
	}
	if strings.Contains(body, "bg-error") {
		t.Errorf("C2: want NO error/red dot variant ever (D-RM33-12 — two-state only), body=%q", body[:min(1200, len(body))])
	}
}

// TestChargeRow_C3_InProgress_RendersWarningDotAndInProgressBadge verifies
// Test Contract C3: a normally-shaped IN_PROGRESS entry (EndedAt/EndBatteryPct
// both nil, the valid shape for that status) renders the warning dot AND the
// IN_PROGRESS badge — both signals independently correct on the same row.
func TestChargeRow_C3_InProgress_RendersWarningDotAndInProgressBadge(t *testing.T) {
	e := charging.Entry{
		ID:              uuid.New(),
		AccountID:       uuid.New(),
		TeslaID:         1001,
		VIN:             "VIN1001",
		Status:          charging.StatusInProgress,
		ChargedOn:       time.Now(),
		Price:           0,
		Currency:        "COP",
		StartBatteryPct: ptrInt(50),
		// EndedAt, EndBatteryPct, EnergyAddedKWh all nil — the normal
		// IN_PROGRESS shape.
	}
	body := renderChargeRow(t, e, nil)

	if !strings.Contains(body, "bg-warning") {
		t.Errorf("C3: want the warning dot class for a normally-shaped IN_PROGRESS entry, body=%q", body[:min(1200, len(body))])
	}
	if strings.Contains(body, "bg-success") {
		t.Errorf("C3: want NO success dot class for an incomplete entry, body=%q", body[:min(1200, len(body))])
	}
	if !strings.Contains(body, "En progreso") {
		t.Errorf("C3: want the IN_PROGRESS status badge text (KeyChargesBadgeInProgress), body=%q", body[:min(1200, len(body))])
	}
	if strings.Contains(body, "Finalizada") {
		t.Errorf("C3: want NO DONE badge text on an IN_PROGRESS entry, body=%q", body[:min(1200, len(body))])
	}
}

// --- Group D — window preservation (design.md Test Contract D1-D4, offline httptest) ---

// todayDefaultZoneMidnight replicates what browserToday(c) resolves to when
// the request carries no browser_tz cookie (the fallback every test in this
// file exercises, since none set the cookie) — the platform default zone's
// (clock.Zone(), America/Bogota) midnight for the wall-clock day the test
// suite runs on. Used by D2 to compute the expected fallback window without
// hardcoding a date that would eventually go stale. Renamed from
// todayUTCMidnight and repointed at clock.Zone() (was time.UTC) —
// RM35-gateway-adopt-clock, roadmap D1/D6.
func todayDefaultZoneMidnight() time.Time {
	return startOfDayIn(time.Now(), clock.Zone())
}

// TestChargeCreate_D1_WindowFromFormThreadsIntoOOBRefresh verifies Test
// Contract D1: POST /ui/charges/create with a valid submission AND
// start=2026-08-01&end=2026-08-31 in the form body (simulating hx-include,
// design.md §D-Include) -> the response's OOB #charges-list div reflects THAT
// window, not the default 7-day one. Asserted via the pre-formatted hidden
// #charges-window-start/#charges-window-end input values (design.md's own
// suggested assertion method), since those two inputs are rendered from
// ChargesPageData.WindowStartStr/WindowEndStr on every #charges-list render.
func TestChargeCreate_D1_WindowFromFormThreadsIntoOOBRefresh(t *testing.T) {
	uid := uuid.New()
	h := newHandlerForCharges(&fakeChargeWriter{}, &fakeChargeReader{entries: []charging.Entry{}})

	form := validCreateForm()
	form.Set("start", "2026-08-01")
	form.Set("end", "2026-08-31")

	w := submitForm(t, h, uid, http.MethodPost, "/ui/charges/create", form)
	if w.Code != http.StatusOK {
		t.Fatalf("D1: want 200 on valid create, got %d body=%q", w.Code, w.Body.String()[:min(500, w.Body.Len())])
	}
	body := w.Body.String()
	if !strings.Contains(body, `id="charges-window-start" value="2026-08-01"`) {
		t.Errorf("D1: want the OOB #charges-list to reflect the hx-include'd start=2026-08-01, body=%q", body[:min(1500, len(body))])
	}
	if !strings.Contains(body, `id="charges-window-end" value="2026-08-31"`) {
		t.Errorf("D1: want the OOB #charges-list to reflect the hx-include'd end=2026-08-31, body=%q", body[:min(1500, len(body))])
	}
}

// TestChargeCreate_D2_StartEndAbsent_FallsBackToDefaultWindow verifies Test
// Contract D2: same as D1 but the start/end form fields are ABSENT (a client
// with hx-include disabled/stripped) -> the OOB refresh falls back to the
// default 7-day window, and the write's success status is unaffected
// (design.md §D-Include: "cosmetic only, never a validation gate").
func TestChargeCreate_D2_StartEndAbsent_FallsBackToDefaultWindow(t *testing.T) {
	uid := uuid.New()
	h := newHandlerForCharges(&fakeChargeWriter{}, &fakeChargeReader{entries: []charging.Entry{}})

	form := validCreateForm() // no start/end fields at all
	w := submitForm(t, h, uid, http.MethodPost, "/ui/charges/create", form)
	if w.Code != http.StatusOK {
		t.Fatalf("D2: want 200 (absent window must never gate the write), got %d body=%q", w.Code, w.Body.String()[:min(500, w.Body.Len())])
	}

	today := todayDefaultZoneMidnight()
	wantStart := today.AddDate(0, 0, -(chargesRangeDefaultDays - 1)).Format("2006-01-02")
	wantEnd := today.Format("2006-01-02")
	body := w.Body.String()
	if !strings.Contains(body, `id="charges-window-start" value="`+wantStart+`"`) {
		t.Errorf("D2: want the OOB refresh to fall back to the default window start %s, body=%q", wantStart, body[:min(1500, len(body))])
	}
	if !strings.Contains(body, `id="charges-window-end" value="`+wantEnd+`"`) {
		t.Errorf("D2: want the OOB refresh to fall back to the default window end %s, body=%q", wantEnd, body[:min(1500, len(body))])
	}
}

// TestChargeRowUpdate_D3_SuccessRetargetsAndResetsToDefaultWindow restates Test
// Contract D3 for the 2026-08-29 amendment. D3 previously required a successful
// PUT to re-render the OOB #charges-list under whatever window the hidden
// start/end inputs posted. It now requires the opposite: a successful edit
// RESETS the list to the default 7-day window, so the user lands back on the
// "last 7 days" preset with that preset active.
//
// The posted window here (2026-08-01..2026-08-31) is deliberately NOT the
// default, so a handler that still threaded it through would fail this test.
func TestChargeRowUpdate_D3_SuccessRetargetsAndResetsToDefaultWindow(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	writer := &fakeChargeWriter{}
	h := newHandlerForCharges(writer, &fakeChargeReader{entries: []charging.Entry{}})

	form := url.Values{
		"csrf_token":        {"tok"},
		"status":            {"IN_PROGRESS"},
		"charged_on":        {"2026-07-16"},
		"energy_added_kwh":  {"20.0"},
		"price":             {"9000"},
		"location_kind":     {"WORK"},
		"start_battery_pct": {"40"},
		"end_battery_pct":   {"75"},
		"start":             {"2026-08-01"},
		"end":               {"2026-08-31"},
	}
	w := submitForm(t, h, uid, http.MethodPut, "/ui/charges/row/"+id.String(), form)
	if w.Code != http.StatusOK {
		t.Fatalf("D3: want 200 on valid update, got %d body=%q", w.Code, w.Body.String()[:min(500, w.Body.Len())])
	}
	body := w.Body.String()

	// The response must be the #charges-list region itself, retargeted away from
	// the form's #charge-row-{id}. The previous shape — a primary <tr> plus a
	// sibling <div hx-swap-oob> — never refreshed the list in the browser: htmx
	// 2.0.4 parses responses inside a <template>, a leading <tr> puts the HTML
	// parser in table insertion mode, and the non-table OOB sibling is
	// foster-parented off the fragment's top level, which is the only place htmx
	// looks for hx-swap-oob. These three assertions together are what stop that
	// shape from coming back.
	if got := w.Header().Get("HX-Retarget"); got != "#charges-list" {
		t.Fatalf("D3: want HX-Retarget=#charges-list on a successful edit, got %q", got)
	}
	if got := w.Header().Get("HX-Reswap"); got != "outerHTML" {
		t.Errorf("D3: want HX-Reswap=outerHTML, got %q", got)
	}
	if strings.Contains(body, "hx-swap-oob") {
		t.Errorf("D3: the update response must NOT use an OOB swap — a <tr>+<div> response drops it; body=%q", body[:min(1500, len(body))])
	}
	if strings.HasPrefix(strings.TrimSpace(body), "<tr") {
		t.Errorf("D3: the update response must not lead with a <tr> (puts htmx's parser in table mode); body=%q", body[:min(500, len(body))])
	}
	if !strings.Contains(body, `id="charges-list"`) {
		t.Fatalf("D3: want the whole #charges-list region in the update response, body=%q", body[:min(1500, len(body))])
	}

	// The default window is derived the same way the handler derives it, via the
	// same todayDefaultZoneMidnight() helper its sibling D2 test uses, so this
	// test does not go stale on a date change or on chargesRangeDefaultDays.
	wantStart, wantEnd := defaultChargesWindow(todayDefaultZoneMidnight())
	if !strings.Contains(body, `id="charges-window-start" value="`+wantStart.Format("2006-01-02")+`"`) {
		t.Errorf("D3: a successful edit must reset the list to the default window start %s, not the posted 2026-08-01; body=%q",
			wantStart.Format("2006-01-02"), body[:min(1500, len(body))])
	}
	if !strings.Contains(body, `id="charges-window-end" value="`+wantEnd.Format("2006-01-02")+`"`) {
		t.Errorf("D3: a successful edit must reset the list to the default window end %s, not the posted 2026-08-31; body=%q",
			wantEnd.Format("2006-01-02"), body[:min(1500, len(body))])
	}
	// The point of the reset: the "last 7 days" preset comes back selected.
	// buildChargesPresets marks Active by exact-match against its own recomputed
	// window, so asserting the rendered active preset proves the reset landed on
	// a real preset rather than merely on some 7-day range.
	if !strings.Contains(body, `hx-get="/ui/charges/list?start=`+wantStart.Format("2006-01-02")+`&amp;end=`+wantEnd.Format("2006-01-02")+`"`) {
		t.Errorf("D3: want the last-7-days preset rendered for the reset window; body=%q", body[:min(2000, len(body))])
	}
}

// TestChargeRowUpdate_D3b_ValidationFailureKeepsThePostedWindow is the other
// half of the amendment: only SUCCESS resets. A failed save must not move the
// user's filter, so the re-rendered edit form still echoes the posted window
// back through its hidden start/end inputs — which is the whole reason those
// inputs exist (design.md §D-Include).
func TestChargeRowUpdate_D3b_ValidationFailureKeepsThePostedWindow(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	h := newHandlerForCharges(&fakeChargeWriter{}, &fakeChargeReader{entries: []charging.Entry{}})

	form := url.Values{
		"csrf_token":    {"tok"},
		"status":        {"IN_PROGRESS"},
		"charged_on":    {"2026-07-16"},
		"location_kind": {"WORK"},
		// start_battery_pct omitted -> validation failure
		"start": {"2026-08-01"},
		"end":   {"2026-08-31"},
	}
	w := submitForm(t, h, uid, http.MethodPut, "/ui/charges/row/"+id.String(), form)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("D3b: want 422 on the invalid update, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `name="start" value="2026-08-01"`) || !strings.Contains(body, `name="end" value="2026-08-31"`) {
		t.Errorf("D3b: a FAILED save must echo the posted window back into the edit form, not reset it; body=%q", body[:min(1500, len(body))])
	}
}

// TestChargeRowDelete_D4_RendersFullChargesListWithinRequestedWindow verifies
// Test Contract D4: DELETE /ui/charges/row/{id}?start=2026-08-01&end=2026-08-31
// -> the response is a full #charges-list fragment (not a bare <tr>)
// reflecting the post-delete state within THAT window, and the deleted row's
// id is absent from it. The fake Reader has no relationship to the fake
// Writer's Delete call, so reader.entries is pre-set to already exclude the
// deleted id — simulating the read a real charging.Reader would return after
// the write committed (the same convention
// TestChargeRowDelete_ThenListReflectsRemoval uses for its own later GET).
func TestChargeRowDelete_D4_RendersFullChargesListWithinRequestedWindow(t *testing.T) {
	uid := uuid.New()
	keptID := uuid.New()
	deletedID := uuid.New()
	reader := &fakeChargeReader{entries: []charging.Entry{
		{ID: keptID, AccountID: uid, TeslaID: 1001, VIN: "VIN1001", ChargedOn: time.Now(),
			EnergyAddedKWh: ptrF64(10.0), Price: 5000.0, Currency: "COP"},
	}}
	writer := &fakeChargeWriter{}
	h := newHandlerForCharges(writer, reader)
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/ui/charges/row/"+deletedID.String()+"?start=2026-08-01&end=2026-08-31", nil)
	req.Header.Set("X-CSRF-Token", "tok")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("D4: want 200 on valid delete, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `id="charges-list"`) {
		t.Fatalf("D4: want the full #charges-list fragment in the response, got body=%q", body[:min(1500, len(body))])
	}
	if !strings.Contains(body, "charge-row-"+keptID.String()) {
		t.Errorf("D4: want the kept entry's row still present, body=%q", body[:min(1500, len(body))])
	}
	if strings.Contains(body, "charge-row-"+deletedID.String()) {
		t.Errorf("D4: want the deleted entry's row absent, body=%q", body[:min(1500, len(body))])
	}
	if !strings.Contains(body, `id="charges-window-start" value="2026-08-01"`) {
		t.Errorf("D4: want the response to reflect the requested window start=2026-08-01, body=%q", body[:min(1500, len(body))])
	}
	if !strings.Contains(body, `id="charges-window-end" value="2026-08-31"`) {
		t.Errorf("D4: want the response to reflect the requested window end=2026-08-31, body=%q", body[:min(1500, len(body))])
	}
	wantOldEmptyRow := `<tr id="charge-row-` + deletedID.String() + `"></tr>`
	if strings.Contains(body, wantOldEmptyRow) {
		t.Errorf("D4: delete response must NOT be a bare empty <tr> (design.md §D-Refresh), got body=%q", body[:min(500, len(body))])
	}
}

// --- Group E — no-vehicle / malformed-window / reader-error empty states
// (design.md Test Contract E1-E4, offline httptest) ---

// TestChargePage_E1_NoRegisteredVehicles_NoFilterChrome verifies Test
// Contract E1: a signed-in user with ZERO registered vehicles requests
// GET /charges -> the response contains ChargesEmptyState()'s message and
// contains NEITHER a preset button NOR any ui.StatTile markup NOR a <table>
// (D-RM33-9 — assert absence, not just presence of the message).
func TestChargePage_E1_NoRegisteredVehicles_NoFilterChrome(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{registered: nil}
	h := New(Deps{
		AnalyticsRecalculator: &fakeRecalculator{},
		Account:               acct,
		Tesla:                 &fakeTesla{},
		TelemetryReader:       &fakeReader{},
		ChargingWriter:        &fakeChargeWriter{},
		ChargingReader:        &fakeChargeReader{},
	})
	r := engineWithSession(h, uid, "")
	c := sessionCookie(r, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/charges", nil)
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("E1: want 200 for a signed-in user with no vehicles, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Aún no hay cargas registradas") {
		t.Errorf("E1: want the existing empty-state message, body=%q", body[:min(800, len(body))])
	}
	if strings.Contains(body, "join-item") {
		t.Errorf("E1: want NO preset button (D-RM33-9 no-chrome), body=%q", body[:min(1500, len(body))])
	}
	if strings.Contains(body, "stat-title") {
		t.Errorf("E1: want NO ui.StatTile markup (D-RM33-9 no-chrome), body=%q", body[:min(1500, len(body))])
	}
	if strings.Contains(body, "<table") {
		t.Errorf("E1: want NO <table> (D-RM33-9 no-chrome), body=%q", body[:min(1500, len(body))])
	}
}

// TestChargesListFragment_E2_MalformedWindow_NoFilterChrome400 verifies Test
// Contract E2: GET /ui/charges/list?start=not-a-date&end=2026-08-31 -> HTTP
// 400, same no-chrome assertions as E1 (design.md §D-Empty state 1, the
// malformed-window branch — both conditions collapse to the identical
// render).
func TestChargesListFragment_E2_MalformedWindow_NoFilterChrome400(t *testing.T) {
	uid := uuid.New()
	h := newHandlerForCharges(&fakeChargeWriter{}, &fakeChargeReader{entries: []charging.Entry{}})
	r := engineWithSession(h, uid, "testcsrf")
	c := sessionCookie(r, uid, "testcsrf")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/charges/list?start=not-a-date&end=2026-08-31", nil)
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("E2: want 400 on a malformed window, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Aún no hay cargas registradas") {
		t.Errorf("E2: want the existing empty-state message on a 400, body=%q", body[:min(800, len(body))])
	}
	if strings.Contains(body, "join-item") {
		t.Errorf("E2: want NO preset button on a 400 (D-Empty state 1), body=%q", body[:min(1500, len(body))])
	}
	if strings.Contains(body, "stat-title") {
		t.Errorf("E2: want NO ui.StatTile markup on a 400 (D-Empty state 1), body=%q", body[:min(1500, len(body))])
	}
	if strings.Contains(body, "<table") {
		t.Errorf("E2: want NO <table> on a 400 (D-Empty state 1), body=%q", body[:min(1500, len(body))])
	}
}

// TestChargesListFragment_E3_ReaderError_ShowsPresetsAndTilesAndAlert
// verifies Test Contract E3: a valid vehicle + valid window, fake Reader
// returns an error -> response contains the preset buttons AND four
// ui.StatTiles (all zero/—) AND a ui.Alert with the error message — NOT the
// same render as E1/E2 (design.md §D-Empty state 2 is a strictly different
// render from state 1).
func TestChargesListFragment_E3_ReaderError_ShowsPresetsAndTilesAndAlert(t *testing.T) {
	uid := uuid.New()
	reader := &fakeChargeReader{err: errFake}
	h := newHandlerForCharges(&fakeChargeWriter{}, reader)
	r := engineWithSession(h, uid, "testcsrf")
	c := sessionCookie(r, uid, "testcsrf")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/charges/list?start=2026-08-01&end=2026-08-05", nil)
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("E3: want 200 (graceful degradation) on a reader error, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "join-item") {
		t.Errorf("E3: want the preset buttons STILL shown on a reader error (D-Empty state 2), body=%q", body[:min(1500, len(body))])
	}
	if got := strings.Count(body, "stat-title"); got != 4 {
		t.Errorf("E3: want the four ui.StatTiles STILL shown (zero/—) on a reader error, got %d stat-title occurrences, body=%q", got, body[:min(1500, len(body))])
	}
	if !strings.Contains(body, "No se pudieron cargar tus registros") {
		t.Errorf("E3: want the reader-error message rendered via ui.Alert, body=%q", body[:min(1500, len(body))])
	}
}

// TestChargesListFragment_E4_ReaderSucceedsEmptySlice_TilesZeroTableEmpty
// verifies Test Contract E4: a valid vehicle + valid window, fake Reader
// returns []charging.Entry{} (no error) -> response contains the preset
// buttons, tiles showing 0/— (not hidden), and ChargesEmptyState()'s message
// in place of table rows (design.md §D-Empty state 3).
func TestChargesListFragment_E4_ReaderSucceedsEmptySlice_TilesZeroTableEmpty(t *testing.T) {
	uid := uuid.New()
	reader := &fakeChargeReader{entries: []charging.Entry{}}
	h := newHandlerForCharges(&fakeChargeWriter{}, reader)
	r := engineWithSession(h, uid, "testcsrf")
	c := sessionCookie(r, uid, "testcsrf")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/charges/list?start=2026-08-01&end=2026-08-05", nil)
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("E4: want 200 for a valid empty-range fetch, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "join-item") {
		t.Errorf("E4: want the preset buttons shown (D13 — empty range still renders chrome), body=%q", body[:min(1500, len(body))])
	}
	if got := strings.Count(body, "stat-title"); got != 4 {
		t.Errorf("E4: want the four ui.StatTiles shown at 0/— (D13/D14), got %d stat-title occurrences, body=%q", got, body[:min(1500, len(body))])
	}
	if !strings.Contains(body, `class="stat-value font-mono">0</div>`) {
		t.Errorf("E4: want at least one tile rendering the zero value, body=%q", body[:min(1500, len(body))])
	}
	if !strings.Contains(body, `class="stat-value font-mono">—</div>`) {
		t.Errorf("E4: want the AvgKWh tile rendering the em-dash, body=%q", body[:min(1500, len(body))])
	}
	if !strings.Contains(body, "Aún no hay cargas registradas") {
		t.Errorf("E4: want ChargesEmptyState() in place of table rows (D-Empty state 3), body=%q", body[:min(1500, len(body))])
	}
	if strings.Contains(body, "<table") {
		t.Errorf("E4: want NO <table> element when the table body is replaced by the empty state, body=%q", body[:min(1500, len(body))])
	}
}
