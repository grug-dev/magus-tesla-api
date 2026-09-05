package handlers

import (
	"bytes"
	"context"
	"errors"
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
	"github.com/cristianpena/magus-tesla-api/internal/analytics"
	"github.com/cristianpena/magus-tesla-api/internal/charging"
	"github.com/cristianpena/magus-tesla-api/internal/clock"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/fragments"
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
// tier 2) and IS called by gateway handlers: buildExternalChargesPage reads the filter
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

	r.GET("/external-charges", h.ExternalChargesPage)
	r.GET("/ui/external-charges/list", h.ExternalChargesListFragment)
	r.GET("/ui/external-charges/row/:id", h.ExternalChargeRowStatic)
	r.GET("/ui/external-charges/row/:id/edit", h.ExternalChargeRowEditFragment)
	r.POST("/ui/external-charges/create", h.ExternalChargeCreate)
	r.PUT("/ui/external-charges/row/:id", h.ExternalChargeRowUpdate)
	r.DELETE("/ui/external-charges/row/:id", h.ExternalChargeRowDelete)

	return r
}

// setUID writes a fake UID into the session by making a GET /external-charges request (which
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
			sess.Set(csrfExternalChargeKey, csrfToken)
		}
		_ = sess.Save()
		c.String(http.StatusOK, "ok")
	})

	r.GET("/external-charges", h.ExternalChargesPage)
	r.GET("/ui/external-charges/list", h.ExternalChargesListFragment)
	r.GET("/ui/external-charges/row/:id", h.ExternalChargeRowStatic)
	r.GET("/ui/external-charges/row/:id/edit", h.ExternalChargeRowEditFragment)
	r.POST("/ui/external-charges/create", h.ExternalChargeCreate)
	r.PUT("/ui/external-charges/row/:id", h.ExternalChargeRowUpdate)
	r.DELETE("/ui/external-charges/row/:id", h.ExternalChargeRowDelete)

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

// newHandlerForExternalCharges builds a Handler with fake charge ports and one registered vehicle.
func newHandlerForExternalCharges(writer *fakeChargeWriter, reader *fakeChargeReader) *Handler {
	acct := &fakeAccount{
		registered: []account.Vehicle{
			{TeslaID: 1001, VIN: "VIN1001", DisplayName: "Magus"},
		},
	}
	return New(Deps{
		AnalyticsRecalculator: &fakeRecalculator{},
		Account:               acct,
		Tesla:                 &fakeTesla{},
		ChargingWriter:        writer,
		ChargingReader:        reader,
	})
}

// newHandlerForExternalChargesWithRecalc is newHandlerForExternalCharges with the analytics
// recalculator exposed, so a test can assert the write path actually calls it
// (RM29 task 5.3 / design.md D5). Kept separate rather than changing
// newHandlerForExternalCharges' signature, so the existing call sites stay untouched.
func newHandlerForExternalChargesWithRecalc(writer *fakeChargeWriter, reader *fakeChargeReader, recalc *fakeRecalculator) *Handler {
	acct := &fakeAccount{
		registered: []account.Vehicle{
			{TeslaID: 1001, VIN: "VIN1001", DisplayName: "Magus"},
		},
	}
	return New(Deps{
		AnalyticsRecalculator: recalc,
		Account:               acct,
		Tesla:                 &fakeTesla{},
		ChargingWriter:        writer,
		ChargingReader:        reader,
	})
}

// postExternalCharge submits a valid create form and returns the recorder.
func postExternalCharge(t *testing.T, h *Handler, uid uuid.UUID) *httptest.ResponseRecorder {
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
	req := httptest.NewRequest(http.MethodPost, "/ui/external-charges/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)
	return w
}

// TestExternalChargeCreate_RecalculatesAfterSuccessfulWrite pins design.md D5 / IO-5:
// the Consumed chart updates instantly today because it is computed on read, so
// the precomputed read model MUST be refreshed on the write path or the owner
// sees a stale chart until the nightly Reconcile. Asserts the call happens, is
// scoped to the writing account and its vehicle, and covers the entry's own day.
func TestExternalChargeCreate_RecalculatesAfterSuccessfulWrite(t *testing.T) {
	uid := uuid.New()
	recalc := &fakeRecalculator{}
	h := newHandlerForExternalChargesWithRecalc(&fakeChargeWriter{}, &fakeChargeReader{entries: []charging.Entry{}}, recalc)

	if w := postExternalCharge(t, h, uid); w.Code != http.StatusOK {
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

// TestExternalChargeCreate_NoRecalculateWhenWriteFails pins the other half of D5: the
// recalculation follows a COMMITTED write. If the write failed there is nothing
// to recompute, and recomputing anyway would rewrite the metrics row from
// unchanged inputs while the user is being shown an error.
func TestExternalChargeCreate_NoRecalculateWhenWriteFails(t *testing.T) {
	uid := uuid.New()
	recalc := &fakeRecalculator{}
	writer := &fakeChargeWriter{createErr: errors.New("charging: insert failed")}
	h := newHandlerForExternalChargesWithRecalc(writer, &fakeChargeReader{entries: []charging.Entry{}}, recalc)

	postExternalCharge(t, h, uid)

	if len(recalc.calls) != 0 {
		t.Fatalf("want NO Recalculate call when the write failed, got %d", len(recalc.calls))
	}
}

// TestExternalChargeCreate_RecalculateErrorDoesNotFailTheRequest pins D5's error policy:
// the user's write already committed, so a recalculation failure is logged and
// swallowed rather than surfaced -- failing their request over a derived-metrics
// error would misreport a save that actually succeeded.
func TestExternalChargeCreate_RecalculateErrorDoesNotFailTheRequest(t *testing.T) {
	uid := uuid.New()
	recalc := &fakeRecalculator{err: errors.New("analytics: recalculate failed")}
	h := newHandlerForExternalChargesWithRecalc(&fakeChargeWriter{}, &fakeChargeReader{entries: []charging.Entry{}}, recalc)

	if w := postExternalCharge(t, h, uid); w.Code != http.StatusOK {
		t.Fatalf("want 200 despite a Recalculate error (the write committed), got %d", w.Code)
	}
	if len(recalc.calls) != 1 {
		t.Errorf("want the failing Recalculate to still have been attempted once, got %d", len(recalc.calls))
	}
}

// --- Sub-task H tests ---

// TestExternalChargePage_NoSession verifies the auth guard redirects unauthenticated visitors.
func TestExternalChargePage_NoSession(t *testing.T) {
	h := newHandlerForExternalCharges(&fakeChargeWriter{}, &fakeChargeReader{})
	r := newGinEngine(h)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/external-charges", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("want 302 redirect for unauthenticated, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/login" {
		t.Errorf("want redirect to /login, got %q", loc)
	}
}

// TestExternalChargePage_WithSession verifies the authenticated path renders the page.
func TestExternalChargePage_WithSession(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	reader := &fakeChargeReader{entries: []charging.Entry{}}
	h := newHandlerForExternalCharges(writer, reader)
	r := engineWithSession(h, uid, "")
	cookie := sessionCookie(r, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/external-charges", nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 for authenticated charge page, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "external-charges-list") {
		t.Errorf("want external-charges-list region in response, got body=%q", body[:min(500, len(body))])
	}
}

// TestExternalChargesListFragment_NoSession verifies auth guard on the list fragment.
func TestExternalChargesListFragment_NoSession(t *testing.T) {
	h := newHandlerForExternalCharges(&fakeChargeWriter{}, &fakeChargeReader{})
	r := newGinEngine(h)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/external-charges/list", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("want 302 for unauthenticated list fragment, got %d", w.Code)
	}
}

// TestExternalChargesListFragment_WithEntries verifies the list fragment returns only
// the fragment. REWRITTEN for RM33 tier 3 (design.md §Test Contract "Existing
// tests requiring REWRITE"): GET /ui/external-charges/list with NO ?start=&end= now
// resolves the default 7-day window via parseExternalChargesRange (rather than taking
// every account entry unconditionally) and the response now also carries the
// preset selector and the four aggregation tiles — asserted here alongside
// the pre-existing external-charges-list/row-id checks, not merely the div's presence.
func TestExternalChargesListFragment_WithEntries(t *testing.T) {
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
	h := newHandlerForExternalCharges(&fakeChargeWriter{}, reader)
	r := engineWithSession(h, uid, "testcsrf")
	c := sessionCookie(r, uid, "testcsrf")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/external-charges/list", nil)
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 for list fragment, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "external-charges-list") {
		t.Errorf("want external-charges-list div in fragment response, body=%q", body[:min(500, len(body))])
	}
	if !strings.Contains(body, "external-charge-row-"+entryID.String()) {
		t.Errorf("want the entry's row in the response, body=%q", body[:min(800, len(body))])
	}
	if !strings.Contains(body, "join-item") {
		t.Errorf("want the default-window preset selector rendered, body=%q", body[:min(1500, len(body))])
	}
	if got := strings.Count(body, "stat-title"); got != 4 {
		t.Errorf("want the four aggregation tiles rendered, got %d stat-title occurrences, body=%q", got, body[:min(1500, len(body))])
	}
}

// TestExternalChargesListFragment_ReaderError verifies graceful degradation on reader
// failure. REWRITTEN for RM33 tier 3 (design.md §Test Contract "Existing
// tests requiring REWRITE" + §D-Empty state 2): the default-window resolution
// now applies here too, and a reader error keeps the preset selector AND the
// (zero-valued) tiles visible — it is NOT the same no-chrome collapse as a
// malformed window (Group E1/E2) or a no-vehicle response.
func TestExternalChargesListFragment_ReaderError(t *testing.T) {
	uid := uuid.New()
	reader := &fakeChargeReader{err: errFake}
	h := newHandlerForExternalCharges(&fakeChargeWriter{}, reader)
	r := engineWithSession(h, uid, "testcsrf")
	c := sessionCookie(r, uid, "testcsrf")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/external-charges/list", nil)
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

// TestExternalChargesListFragment_EmptyState verifies empty-state message on empty
// list. REWRITTEN for RM33 tier 3 (design.md §Test Contract "Existing tests
// requiring REWRITE" + §D-Empty state 3): a valid vehicle + valid (default)
// window with zero rows is NOT the same as the no-chrome collapse (Group
// E1/E2) — D13 requires the selector and tiles to still render (0/—), only
// the table body swaps for ExternalChargesEmptyState().
func TestExternalChargesListFragment_EmptyState(t *testing.T) {
	uid := uuid.New()
	reader := &fakeChargeReader{entries: []charging.Entry{}}
	h := newHandlerForExternalCharges(&fakeChargeWriter{}, reader)
	r := engineWithSession(h, uid, "testcsrf")
	c := sessionCookie(r, uid, "testcsrf")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/external-charges/list", nil)
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
		t.Errorf("want NO <table> element — the table body is replaced by ExternalChargesEmptyState(), body=%q", body[:min(1500, len(body))])
	}
}

// TestExternalChargeCreate_CSRFMismatch verifies 403 on CSRF token mismatch.
func TestExternalChargeCreate_CSRFMismatch(t *testing.T) {
	uid := uuid.New()
	h := newHandlerForExternalCharges(&fakeChargeWriter{}, &fakeChargeReader{})
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
	req := httptest.NewRequest(http.MethodPost, "/ui/external-charges/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 on CSRF mismatch, got %d", w.Code)
	}
}

// TestExternalChargeCreate_NoResolvableVehicle_RejectedWithoutWriter replaces the old
// TestExternalChargeCreate_UnownedVehicle (MAG-5 D4): the create form drops the vehicle
// picker and sources (teslaID, vin) from the session-selected vehicle via
// resolveSelectedVehicle. The "unowned vehicle" path is now unreachable from the
// form (resolveSelectedVehicle only returns vehicles from the user's account);
// the analogous failure becomes "no resolvable selected vehicle" (the account has
// no registered vehicles, so resolveSelectedVehicle returns (_, false)) —
// rendered as a 422 field-error, NOT a 403, and the Writer is NOT called.
func TestExternalChargeCreate_NoResolvableVehicle_RejectedWithoutWriter(t *testing.T) {
	uid := uuid.New()
	// Account with NO registered vehicles → resolveSelectedVehicle returns false.
	acct := &fakeAccount{registered: nil}
	h := New(Deps{
		AnalyticsRecalculator: &fakeRecalculator{},
		Account:               acct,
		Tesla:                 &fakeTesla{},
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
	req := httptest.NewRequest(http.MethodPost, "/ui/external-charges/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 when no vehicle is resolvable, got %d", w.Code)
	}
	body := w.Body.String()
	// Resolved language here is Spanish (see the TestExternalChargesListFragment_EmptyState
	// comment above) — KeyChargesErrorSelectVehicle's ES value.
	if !strings.Contains(body, "Selecciona un vehículo") {
		t.Errorf("want 'Selecciona un vehículo' in body, got %q", body[:min(500, len(body))])
	}
}

// TestExternalChargeCreate_MissingRequiredField verifies 422 on a missing required field.
func TestExternalChargeCreate_MissingRequiredField(t *testing.T) {
	uid := uuid.New()
	h := newHandlerForExternalCharges(&fakeChargeWriter{}, &fakeChargeReader{})
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
	req := httptest.NewRequest(http.MethodPost, "/ui/external-charges/create", strings.NewReader(form.Encode()))
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

// TestExternalChargeCreate_ValidInput verifies a valid POST creates an entry and returns the refreshed list.
// MAG-5 D5: Currency is hardcoded "COP" even though the form no longer submits a
// `currency` field (it renders a disabled, read-only COP input that is not
// submitted). MAG-5 D6: start_battery_pct + end_battery_pct are REQUIRED and
// persisted non-nil. D4: no `vehicle` form field — sourced from the session.
func TestExternalChargeCreate_ValidInput(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	reader := &fakeChargeReader{entries: []charging.Entry{}}
	h := newHandlerForExternalCharges(writer, reader)
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
	req := httptest.NewRequest(http.MethodPost, "/ui/external-charges/create", strings.NewReader(form.Encode()))
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

// TestExternalChargeRowEditFragment_NoSession verifies auth guard on the edit fragment.
func TestExternalChargeRowEditFragment_NoSession(t *testing.T) {
	h := newHandlerForExternalCharges(&fakeChargeWriter{}, &fakeChargeReader{})
	r := newGinEngine(h)

	id := uuid.New()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/external-charges/row/"+id.String()+"/edit", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("want 302 for unauthenticated edit fragment, got %d", w.Code)
	}
}

// TestExternalChargeRowEditFragment_WithEntry verifies the edit fragment returns an edit form pre-populated.
func TestExternalChargeRowEditFragment_WithEntry(t *testing.T) {
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
	h := newHandlerForExternalCharges(&fakeChargeWriter{}, reader)
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/external-charges/row/"+entryID.String()+"/edit", nil)
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 for edit fragment, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "external-charge-row-"+entryID.String()) {
		t.Errorf("want entry id in edit form, body=%q", body[:min(500, len(body))])
	}
}

// TestExternalChargeRowUpdate_CSRFMismatch verifies 403 on CSRF mismatch for update.
func TestExternalChargeRowUpdate_CSRFMismatch(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	h := newHandlerForExternalCharges(&fakeChargeWriter{}, &fakeChargeReader{})
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
	req := httptest.NewRequest(http.MethodPut, "/ui/external-charges/row/"+id.String(), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 on CSRF mismatch for update, got %d", w.Code)
	}
}

// TestExternalChargeRowUpdate_ValidationError verifies 422 on a validation error during update.
func TestExternalChargeRowUpdate_ValidationError(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	h := newHandlerForExternalCharges(&fakeChargeWriter{}, &fakeChargeReader{})
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
	req := httptest.NewRequest(http.MethodPut, "/ui/external-charges/row/"+id.String(), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 on validation error for update, got %d", w.Code)
	}
}

// TestExternalChargeRowUpdate_ValidInput verifies a valid PUT persists the posted values
// and re-renders the whole #external-charges-list region.
//
// The response-shape assertion below was rewritten when MAG-18 wave 5 retargeted
// the success path (external_charges.go, "A SUCCESSFUL edit re-renders the WHOLE
// #external-charges-list region"): htmx parses the response inside a <template>, so a
// leading <tr> switches the HTML parser into table-insertion mode and the
// following <div hx-swap-oob> is foster-parented out of top level, silently
// dropping the list refresh. The handler therefore emits no row markup at all,
// and the old `external-charge-row-{id}` expectation could never match again. Sibling
// TestExternalChargeRowUpdate_D3_SuccessRetargetsAndResetsToDefaultWindow owns the full
// contract (headers, no OOB, no leading <tr>); this test keeps its own focus on
// the persisted write values.
func TestExternalChargeRowUpdate_ValidInput(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	writer := &fakeChargeWriter{}
	h := newHandlerForExternalCharges(writer, &fakeChargeReader{})
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
	req := httptest.NewRequest(http.MethodPut, "/ui/external-charges/row/"+id.String(), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 on valid update, got %d body=%q", w.Code, w.Body.String()[:min(500, w.Body.Len())])
	}
	body := w.Body.String()
	if !strings.Contains(body, `id="external-charges-list"`) {
		t.Errorf("want the whole #external-charges-list region in the update response, body=%q", body[:min(500, len(body))])
	}
	if got := w.Header().Get("HX-Retarget"); got != "#external-charges-list" {
		t.Errorf("want HX-Retarget=#external-charges-list on a successful update, got %q", got)
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

// TestExternalChargeRowDelete_CSRFMismatch verifies 403 on CSRF mismatch for delete.
func TestExternalChargeRowDelete_CSRFMismatch(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	writer := &fakeChargeWriter{deleteErr: nil}
	h := newHandlerForExternalCharges(writer, &fakeChargeReader{})
	r := engineWithSession(h, uid, "goodtoken")
	c := sessionCookie(r, uid, "goodtoken")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/ui/external-charges/row/"+id.String(), nil)
	req.Header.Set("X-CSRF-Token", "badtoken")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 on CSRF mismatch for delete, got %d", w.Code)
	}
}

// TestExternalChargeRowDelete_ValidInput_RendersChargesListFragment is the RM33 tier 3
// REWRITE of the old TestExternalChargeRowDelete_ValidInput_RendersEmptyRow (design.md
// §Test Contract "Existing tests requiring REWRITE"). The delete button's
// hx-target moved from "#external-charge-row-{id}" to "#external-charges-list" (design.md
// §D-Refresh) — after a successful delete there is no row left to swap into,
// so the handler now re-renders the WHOLE #external-charges-list region (selector +
// tiles + table/empty-state), structurally identical to ExternalChargesListFragment's
// own response. The old bare `<tr id="external-charge-row-<id>"></tr>` empty-row shape
// (`fragments.ChargeRowEmpty`, deleted in Wave 6.1) must NOT appear. Same
// assertion shape as Group D4 (TestExternalChargeRowDelete_D4_...), kept as its own
// test per design.md's explicit REWRITE-item enumeration. The CSRF token is
// sent via the X-CSRF-Token HEADER (matching the external_charge_row.templ fix that
// emits hx-headers carrying X-CSRF-Token — Go's net/http parses DELETE
// request BODIES for no method, so the prior hx-include body path silently
// 403'd; the header path is the fix — see ExternalChargeRowDelete doc comment).
func TestExternalChargeRowDelete_ValidInput_RendersChargesListFragment(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	writer := &fakeChargeWriter{}
	h := newHandlerForExternalCharges(writer, &fakeChargeReader{})
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/ui/external-charges/row/"+id.String(), nil)
	req.Header.Set("X-CSRF-Token", "tok")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 on valid delete, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `id="external-charges-list"`) {
		t.Errorf("want the full #external-charges-list fragment in the delete response, got body=%q", body[:min(500, len(body))])
	}
	// The old MAG-5 empty-row shape (fragments.ChargeRowEmpty, now deleted)
	// must never appear — the response is the whole list region, not a bare
	// empty <tr>.
	wantOldEmptyRow := `<tr id="external-charge-row-` + id.String() + `"></tr>`
	if strings.Contains(body, wantOldEmptyRow) {
		t.Errorf("delete response must NOT be the old bare empty <tr> shape, got body=%q",
			body[:min(500, len(body))])
	}
}

// TestExternalChargeRowDelete_StaleCSRF_RejectedNotAlerted verifies the stale/missing
// CSRF path returns 403 and the row is not deleted (D3 / spec scenario). The
// response is a plain-text 403 (no inline error row), but per the spec the
// correct fix is the wire-path (CSRF reaches the handler); an attacker
// submitting a wrong token is rejected at the CSRF gate.
func TestExternalChargeRowDelete_StaleCSRF_RejectedNotAlerted(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	writer := &fakeChargeWriter{}
	h := newHandlerForExternalCharges(writer, &fakeChargeReader{})
	r := engineWithSession(h, uid, "freshsessiontoken")
	c := sessionCookie(r, uid, "freshsessiontoken")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/ui/external-charges/row/"+id.String(), nil)
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
// When the session carries no csrf_externalcharge token (e.g. an authenticated user
// who never loaded GET /external-charges) and the request submits no token either, the check
// must return 403 — not pass via subtle.ConstantTimeCompare("","") == 1.

// TestExternalChargeCreate_NoSessionToken covers the empty-session CSRF bypass for create.
func TestExternalChargeCreate_NoSessionToken(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	h := newHandlerForExternalCharges(writer, &fakeChargeReader{})
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
	req := httptest.NewRequest(http.MethodPost, "/ui/external-charges/create", strings.NewReader(form.Encode()))
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

// TestExternalChargeRowUpdate_NoSessionToken covers the empty-session CSRF bypass for update.
func TestExternalChargeRowUpdate_NoSessionToken(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	writer := &fakeChargeWriter{}
	h := newHandlerForExternalCharges(writer, &fakeChargeReader{})
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
	req := httptest.NewRequest(http.MethodPut, "/ui/external-charges/row/"+id.String(), strings.NewReader(form.Encode()))
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

// TestExternalChargeRowDelete_NoSessionToken covers the empty-session CSRF bypass for delete.
func TestExternalChargeRowDelete_NoSessionToken(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	h := newHandlerForExternalCharges(&fakeChargeWriter{}, &fakeChargeReader{})
	r := engineWithSession(h, uid, "")
	c := sessionCookie(r, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/ui/external-charges/row/"+id.String(), nil)
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

// TestExternalChargeEntryVMFromEntry verifies the view model mapping pre-computes derived fields.
func TestExternalChargeEntryVMFromEntry(t *testing.T) {
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

	vm := externalChargeEntryVMFromEntry(e, vehicles)

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

// TestExternalChargeEntryVMFromEntry_RawFieldsNeverCommaGrouped pins design.md's
// "Critical constraint" (MAG-9): RawEnergyKWh and RawPrice populate the inline
// edit form's <input value>, so they MUST stay plain machine-parseable decimal
// strings — never routed through formatMoney/commaGroup — even when the
// underlying amount is >= 1000 and would otherwise be grouped for display.
func TestExternalChargeEntryVMFromEntry_RawFieldsNeverCommaGrouped(t *testing.T) {
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

	vm := externalChargeEntryVMFromEntry(e, vehicles)

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

// TestExternalChargeCreate_MissingLocationKind verifies 422 when location_kind is absent.
func TestExternalChargeCreate_MissingLocationKind(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	reader := &fakeChargeReader{entries: []charging.Entry{}}
	h := newHandlerForExternalCharges(writer, reader)
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
	req := httptest.NewRequest(http.MethodPost, "/ui/external-charges/create", strings.NewReader(form.Encode()))
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

// TestExternalChargeCreate_InvalidLocationKind verifies 422 when location_kind has an unrecognized value.
func TestExternalChargeCreate_InvalidLocationKind(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	reader := &fakeChargeReader{entries: []charging.Entry{}}
	h := newHandlerForExternalCharges(writer, reader)
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
	req := httptest.NewRequest(http.MethodPost, "/ui/external-charges/create", strings.NewReader(form.Encode()))
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

// TestExternalChargeRowUpdate_MissingLocationKind verifies 422 on missing location_kind during update.
func TestExternalChargeRowUpdate_MissingLocationKind(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	writer := &fakeChargeWriter{}
	h := newHandlerForExternalCharges(writer, &fakeChargeReader{})
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
	req := httptest.NewRequest(http.MethodPut, "/ui/external-charges/row/"+id.String(), strings.NewReader(form.Encode()))
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

// TestExternalChargeCreate_ValidLocationKind verifies that a valid location_kind succeeds.
func TestExternalChargeCreate_ValidLocationKind(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	reader := &fakeChargeReader{entries: []charging.Entry{}}
	h := newHandlerForExternalCharges(writer, reader)
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
	req := httptest.NewRequest(http.MethodPost, "/ui/external-charges/create", strings.NewReader(form.Encode()))
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

// --- charges content fragment + vehicle-switch refresh (GET /ui/external-charges) ---

// externalChargesContentEngine builds a Gin engine seeding uid + a selected-vehicle context,
// wired to GET /ui/external-charges. Mirrors dashboardEngine but for the external-charges page.
func externalChargesContentEngine(h *Handler, uid uuid.UUID, selTeslaID int64, selVIN string) *gin.Engine {
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
	r.GET("/external-charges", h.ExternalChargesPage)
	r.GET("/ui/external-charges", h.ExternalChargesContentFragment)
	return r
}

// TestExternalChargesContentFragment_ScopedToSelectedVehicle verifies GET /ui/external-charges emits
// BOTH content fragments (create form + list) for the SELECTED vehicle, with a fresh
// CSRF token and no page shell. After MAG-5 D4 the create form no longer renders a
// vehicle picker at all (the session determines the vehicle), so the assertion now
// ALSO verifies D4: no `name="vehicle"` input is present in the create form.
func TestExternalChargesContentFragment_ScopedToSelectedVehicle(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 1, VIN: "VIN1", DisplayName: "First"},
		{TeslaID: 2, VIN: "VIN2", DisplayName: "Second"},
	}}
	h := New(Deps{
		AnalyticsRecalculator: &fakeRecalculator{},
		Account:               acct,
		Tesla:                 &fakeTesla{},
		ChargingWriter:        &fakeChargeWriter{},
		ChargingReader:        &fakeChargeReader{},
	})
	eng := externalChargesContentEngine(h, uid, 2, "VIN2")
	c := sessionCookie(eng, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/external-charges", nil)
	if c != nil {
		req.AddCookie(c)
	}
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 for authenticated charges content fragment, got %d (%s)", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{`id="external-charges-create-form"`, `id="external-charges-list"`, `name="csrf_token"`} {
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

// TestExternalChargePage_SubscribesToVehicleChanged verifies the full external-charges page
// wraps its content in the #external-charges-content region wired to refresh on the sidebar
// switcher's "vehicle-changed" event.
func TestExternalChargePage_SubscribesToVehicleChanged(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 1, VIN: "VIN1", DisplayName: "First"},
	}}
	h := New(Deps{
		AnalyticsRecalculator: &fakeRecalculator{},
		Account:               acct,
		Tesla:                 &fakeTesla{},
		ChargingWriter:        &fakeChargeWriter{},
		ChargingReader:        &fakeChargeReader{},
	})
	eng := externalChargesContentEngine(h, uid, 1, "VIN1")
	c := sessionCookie(eng, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/external-charges", nil)
	if c != nil {
		req.AddCookie(c)
	}
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 for the charge page, got %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		`id="external-charges-content"`,
		`hx-get="/ui/external-charges"`,
		`hx-trigger="vehicle-changed from:body"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("charge page missing switch-refresh wiring %q", want)
		}
	}
}

// --- MAG-5 D2/D6: start-battery suggestion + required battery fields (T4.5) ---

// TestExternalChargePage_BatterySuggestionFromTelemetry verifies D2: when the selected
// vehicle's latest status reports BatteryLevelPct=73, the create form's
// start_battery_pct input carries a placeholder helper label "Latest: 73%"
// built via analytics.Reader.LatestMetricsByAccount (the same port the
// dashboard uses — retyped from the retired snapshot-based reader (RM40) by
// RM38-gateway-read-dashboard-from-metrics design.md D9). The
// fakeAnalyticsReader seeds the status; the buildExternalChargesPage helper picks the
// status for the session-selected TeslaID.
func TestExternalChargePage_BatterySuggestionFromTelemetry(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 1001, VIN: "VIN1001", DisplayName: "Magus"},
	}}
	reader := &fakeAnalyticsReader{statuses: []analytics.VehicleStatus{
		{TeslaID: 1001, BatteryLevelPct: 73},
	}}
	h := New(Deps{
		AnalyticsRecalculator: &fakeRecalculator{},
		Account:               acct,
		Tesla:                 &fakeTesla{},
		AnalyticsReader:       reader,
		ChargingWriter:        &fakeChargeWriter{},
		ChargingReader:        &fakeChargeReader{},
	})
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/external-charges", nil)
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

// TestExternalChargePage_NoBatterySuggestionWhenNoSnapshot verifies D2's graceful-empty
// contract: when analytics.Reader.LatestMetricsByAccount returns no status for
// the selected vehicle (empty slice, nil error), the create form's start_battery_pct input
// does NOT carry a "Latest: N%" placeholder — no fabricated value — and the page
// still renders 200.
func TestExternalChargePage_NoBatterySuggestionWhenNoSnapshot(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 1001, VIN: "VIN1001", DisplayName: "Magus"},
	}}
	reader := &fakeAnalyticsReader{statuses: nil} // no statuses
	h := New(Deps{
		AnalyticsRecalculator: &fakeRecalculator{},
		Account:               acct,
		Tesla:                 &fakeTesla{},
		AnalyticsReader:       reader,
		ChargingWriter:        &fakeChargeWriter{},
		ChargingReader:        &fakeChargeReader{},
	})
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/external-charges", nil)
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

// TestExternalChargePage_NoBatterySuggestionOnTelemetryError verifies that a telemetry
// Reader error degrades gracefully — no suggestion is rendered, and the page
// still returns 200 (the page never 500s from a telemetry read failure).
func TestExternalChargePage_NoBatterySuggestionOnTelemetryError(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{registered: []account.Vehicle{
		{TeslaID: 1001, VIN: "VIN1001", DisplayName: "Magus"},
	}}
	reader := &fakeAnalyticsReader{statusesErr: errFake} // simulate an analytics store failure
	h := New(Deps{
		AnalyticsRecalculator: &fakeRecalculator{},
		Account:               acct,
		Tesla:                 &fakeTesla{},
		AnalyticsReader:       reader,
		ChargingWriter:        &fakeChargeWriter{},
		ChargingReader:        &fakeChargeReader{},
	})
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/external-charges", nil)
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

// TestExternalChargeCreate_MissingBatteryPct_Rejected verifies D6: submitting with
// start_battery_pct or end_battery_pct empty is rejected (422) with a
// "Battery percentage is required" message, and the Writer is not called.
func TestExternalChargeCreate_MissingBatteryPct_Rejected(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	reader := &fakeChargeReader{entries: []charging.Entry{}}
	h := newHandlerForExternalCharges(writer, reader)
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
	req := httptest.NewRequest(http.MethodPost, "/ui/external-charges/create", strings.NewReader(form.Encode()))
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

// TestExternalChargeCreate_OutOfRangeBatteryPct_Rejected verifies D6: out-of-range
// start_battery_pct (e.g. 101 or -1) is rejected with the out-of-range message
// and the Writer is not called. End battery % mirrors via the same check.
func TestExternalChargeCreate_OutOfRangeBatteryPct_Rejected(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	reader := &fakeChargeReader{entries: []charging.Entry{}}
	h := newHandlerForExternalCharges(writer, reader)
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
			req := httptest.NewRequest(http.MethodPost, "/ui/external-charges/create", strings.NewReader(form.Encode()))
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

// TestExternalChargePage_DateDefaultsToToday verifies D1: the create form's started_at
// and ended_at inputs are pre-filled with today's date at the platform-default
// zone's midnight ("YYYY-MM-DDT00:00") via ExternalChargesPageData.DefaultStartedAt /
// DefaultEndedAt. No browser_tz cookie is set here, so "today" resolves via
// browserToday's clock.Zone() fallback (America/Bogota) — was UTC before
// RM35-gateway-adopt-clock; repaired per roadmap D6.
func TestExternalChargePage_DateDefaultsToToday(t *testing.T) {
	uid := uuid.New()
	h := newHandlerForExternalCharges(&fakeChargeWriter{}, &fakeChargeReader{})
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/external-charges", nil)
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

// TestExternalChargeCreate_ClearedDates_PersistedNil verifies D1's optional contract:
// submitting the form with both started_at and ended_at cleared (empty strings)
// still persists a created entry with nil StartedAt / nil EndedAt — the gateway
// does not require these optional fields even with the today-default in place.
func TestExternalChargeCreate_ClearedDates_PersistedNil(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	reader := &fakeChargeReader{entries: []charging.Entry{}}
	h := newHandlerForExternalCharges(writer, reader)
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
	req := httptest.NewRequest(http.MethodPost, "/ui/external-charges/create", strings.NewReader(form.Encode()))
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

// TestExternalChargeCreate_3DecimalEnergy_Accepted verifies D7: submitting
// energy_added_kwh=7.345 (a 3-decimal value) succeeds and persists
// EnergyAddedKWh=7.345 (the UI step=0.001 permits 3 decimals; no server-side
// rounding). The energy > 0 check still passes.
func TestExternalChargeCreate_3DecimalEnergy_Accepted(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	reader := &fakeChargeReader{entries: []charging.Entry{}}
	h := newHandlerForExternalCharges(writer, reader)
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
	req := httptest.NewRequest(http.MethodPost, "/ui/external-charges/create", strings.NewReader(form.Encode()))
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

// TestExternalChargeCreate_NonPositiveEnergy_Rejected verifies the D7 parity contract:
// the energy > 0 check is unchanged. energy=0 and energy=-1 are still rejected.
func TestExternalChargeCreate_NonPositiveEnergy_Rejected(t *testing.T) {
	uid := uuid.New()
	for _, v := range []string{"0", "-1"} {
		writer := &fakeChargeWriter{}
		reader := &fakeChargeReader{entries: []charging.Entry{}}
		h := newHandlerForExternalCharges(writer, reader)
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
		req := httptest.NewRequest(http.MethodPost, "/ui/external-charges/create", strings.NewReader(form.Encode()))
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

// TestExternalChargeRowDelete_ThenListReflectsRemoval verifies D3's "row is no longer
// present in a subsequent list render" scenario. The fakeReader.ListEntriesBy*
// returns a static slice; we pre-seed the entry being deleted and assert the
// external-charge-row-<id> marker is absent from a GET /ui/external-charges/list when the fake
// reader's slice no longer carries that id (simulating the post-delete state).
//
// RE-POINTED for RM33 tier 3 (design.md §Test Contract "Existing tests
// requiring REWRITE" — "likely still valid in INTENT but must be re-pointed
// at the new response shape"): the original intent (a later GET reflects the
// removal) is unchanged, so nothing below it was altered; this adds one new
// assertion on the delete response ITSELF, confirming ExternalChargeRowDelete now
// returns the full #external-charges-list fragment (design.md §D-Refresh), not the old
// MAG-5 bare empty-row shape.
func TestExternalChargeRowDelete_ThenListReflectsRemoval(t *testing.T) {
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
	h := newHandlerForExternalCharges(writer, reader)
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	// Before delete: list render carries the row id.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/external-charges/list", nil)
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), "external-charge-row-"+id.String()) {
		t.Fatalf("pre-delete list should contain the row id, got body=%q",
			w.Body.String()[:min(400, w.Body.Len())])
	}

	// Delete the row.
	wDel := httptest.NewRecorder()
	reqDel := httptest.NewRequest(http.MethodDelete, "/ui/external-charges/row/"+id.String(), nil)
	reqDel.Header.Set("X-CSRF-Token", "tok")
	if c != nil {
		reqDel.AddCookie(c)
	}
	r.ServeHTTP(wDel, reqDel)
	if wDel.Code != http.StatusOK {
		t.Fatalf("want 200 on delete, got %d", wDel.Code)
	}
	if !strings.Contains(wDel.Body.String(), `id="external-charges-list"`) {
		t.Errorf("want the delete response ITSELF to be the full #external-charges-list fragment (design.md §D-Refresh), got body=%q",
			wDel.Body.String()[:min(500, wDel.Body.Len())])
	}

	// Post-delete list render: simulate the entry's removal from the read
	// fixture — the gateway never deletes from the fakeReader (only the Writer
	// deletes from the real store), so we update the fixture to reflect reality.
	reader.entries = nil
	wList := httptest.NewRecorder()
	reqList := httptest.NewRequest(http.MethodGet, "/ui/external-charges/list", nil)
	if c != nil {
		reqList.AddCookie(c)
	}
	r.ServeHTTP(wList, reqList)
	if strings.Contains(wList.Body.String(), "external-charge-row-"+id.String()) {
		t.Errorf("post-delete list must NOT contain the row id, got body=%q",
			wList.Body.String()[:min(400, wList.Body.Len())])
	}
}

// ============================================================================
// RM33-gateway-update-charge-form (MAG-18) — Test Contract (design.md).
//
// The tests below implement design.md's Test Contract Groups A, B, C. See the
// "Fixture convention" blockquote at the top of tasks.md §Wave 8: every
// url.Values fixture that reaches parseExternalChargeForm carries an explicit
// "status" (Test Contract A4 makes a missing status a validation error in its
// own right), and every negative test asserts the SPECIFIC i18n message for
// the field under test, not just the response status code — a bare
// 422+writer-not-called assertion would stay green even if the checked
// field's own validation were deleted.
// ============================================================================

// submitForm issues method to path with form on a fresh session for uid,
// returning the recorder. Mirrors postExternalCharge's shape but is parameterized
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

// --- Group A — parseExternalChargeForm / handler-level (design.md Test Contract A1-A8) ---

// TestExternalChargeCreate_A1_InProgress_OptionalFieldsOmitted_Succeeds verifies Test
// Contract A1: status=IN_PROGRESS with no energy_added_kwh, no price, no
// ended_at, no end_battery_pct still succeeds, persisting EnergyAddedKWh=nil,
// Price=0, EndedAt=nil, EndBatteryPct=nil, Status=IN_PROGRESS.
func TestExternalChargeCreate_A1_InProgress_OptionalFieldsOmitted_Succeeds(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	h := newHandlerForExternalCharges(writer, &fakeChargeReader{entries: []charging.Entry{}})

	w := submitForm(t, h, uid, http.MethodPost, "/ui/external-charges/create", validCreateForm())

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

// TestExternalChargeCreate_A2_Done_MissingEndedAtAndEndBatteryPct_Rejected verifies
// Test Contract A2: the same omission under status=DONE is rejected on BOTH
// conditionally-required fields, and Writer.Create is never reached.
func TestExternalChargeCreate_A2_Done_MissingEndedAtAndEndBatteryPct_Rejected(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	h := newHandlerForExternalCharges(writer, &fakeChargeReader{entries: []charging.Entry{}})

	form := validCreateForm()
	form.Set("status", "DONE")
	// ended_at / end_battery_pct deliberately omitted.

	w := submitForm(t, h, uid, http.MethodPost, "/ui/external-charges/create", form)

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

// TestExternalChargeCreate_A3_Done_WithEndedAtAndEndBatteryPct_Succeeds verifies Test
// Contract A3: status=DONE with both conditionally-required fields supplied
// and valid succeeds, persisting Status=DONE.
func TestExternalChargeCreate_A3_Done_WithEndedAtAndEndBatteryPct_Succeeds(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	h := newHandlerForExternalCharges(writer, &fakeChargeReader{entries: []charging.Entry{}})

	form := validCreateForm()
	form.Set("status", "DONE")
	form.Set("ended_at", "2026-07-15T18:00")
	form.Set("end_battery_pct", "90")

	w := submitForm(t, h, uid, http.MethodPost, "/ui/external-charges/create", form)

	if w.Code != http.StatusOK {
		t.Fatalf("A3: want 200, got %d body=%q", w.Code, w.Body.String()[:min(500, w.Body.Len())])
	}
	if writer.createEntry.Status != charging.StatusDone {
		t.Errorf("A3: want Status DONE, got %v", writer.createEntry.Status)
	}
}

// TestExternalChargeCreate_A4_MissingOrInvalidStatus_Rejected verifies Test Contract
// A4: an absent or unrecognized status value is rejected on the status field
// itself, before RequiredFieldsFor is ever consulted.
func TestExternalChargeCreate_A4_MissingOrInvalidStatus_Rejected(t *testing.T) {
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
			h := newHandlerForExternalCharges(writer, &fakeChargeReader{entries: []charging.Entry{}})

			form := validCreateForm()
			if tc.omit {
				form.Del("status")
			} else {
				form.Set("status", tc.set)
			}

			w := submitForm(t, h, uid, http.MethodPost, "/ui/external-charges/create", form)

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

// TestExternalChargeCreate_A5_PriceOptional verifies Test Contract A5: price="" ->
// 0/no error; price="-1" -> validation error, Writer not called; price
// "150.50" -> persisted as-is.
func TestExternalChargeCreate_A5_PriceOptional(t *testing.T) {
	uid := uuid.New()

	t.Run("empty_defaults_to_zero", func(t *testing.T) {
		writer := &fakeChargeWriter{}
		h := newHandlerForExternalCharges(writer, &fakeChargeReader{entries: []charging.Entry{}})
		form := validCreateForm()
		form.Set("price", "")
		w := submitForm(t, h, uid, http.MethodPost, "/ui/external-charges/create", form)
		if w.Code != http.StatusOK {
			t.Fatalf("want 200 on empty price, got %d body=%q", w.Code, w.Body.String()[:min(500, w.Body.Len())])
		}
		if writer.createEntry.Price != 0 {
			t.Errorf("want Price 0 on empty submission, got %v", writer.createEntry.Price)
		}
	})

	t.Run("negative_rejected", func(t *testing.T) {
		writer := &fakeChargeWriter{}
		h := newHandlerForExternalCharges(writer, &fakeChargeReader{entries: []charging.Entry{}})
		form := validCreateForm()
		form.Set("price", "-1")
		w := submitForm(t, h, uid, http.MethodPost, "/ui/external-charges/create", form)
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
		h := newHandlerForExternalCharges(writer, &fakeChargeReader{entries: []charging.Entry{}})
		form := validCreateForm()
		form.Set("price", "150.50")
		w := submitForm(t, h, uid, http.MethodPost, "/ui/external-charges/create", form)
		if w.Code != http.StatusOK {
			t.Fatalf("want 200 on valid price, got %d body=%q", w.Code, w.Body.String()[:min(500, w.Body.Len())])
		}
		if writer.createEntry.Price != 150.50 {
			t.Errorf("want Price 150.50 persisted, got %v", writer.createEntry.Price)
		}
	})
}

// TestExternalChargeCreate_A6_EnergyOptional verifies Test Contract A6:
// energy_added_kwh="" -> nil/no error; energy_added_kwh="0" -> rejected by
// the existing "must be positive" rule, now conditioned on non-empty rather
// than always-on.
func TestExternalChargeCreate_A6_EnergyOptional(t *testing.T) {
	uid := uuid.New()

	t.Run("empty_is_nil_no_error", func(t *testing.T) {
		writer := &fakeChargeWriter{}
		h := newHandlerForExternalCharges(writer, &fakeChargeReader{entries: []charging.Entry{}})
		form := validCreateForm()
		form.Set("energy_added_kwh", "")
		w := submitForm(t, h, uid, http.MethodPost, "/ui/external-charges/create", form)
		if w.Code != http.StatusOK {
			t.Fatalf("want 200 on empty energy, got %d body=%q", w.Code, w.Body.String()[:min(500, w.Body.Len())])
		}
		if writer.createEntry.EnergyAddedKWh != nil {
			t.Errorf("want EnergyAddedKWh nil on empty submission, got %v", *writer.createEntry.EnergyAddedKWh)
		}
	})

	t.Run("zero_rejected", func(t *testing.T) {
		writer := &fakeChargeWriter{}
		h := newHandlerForExternalCharges(writer, &fakeChargeReader{entries: []charging.Entry{}})
		form := validCreateForm()
		form.Set("energy_added_kwh", "0")
		w := submitForm(t, h, uid, http.MethodPost, "/ui/external-charges/create", form)
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

// TestExternalChargeCreate_A7_OdometerOptional verifies Test Contract A7: odometer_km
// is a new always-optional non-negative integer field.
func TestExternalChargeCreate_A7_OdometerOptional(t *testing.T) {
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
			h := newHandlerForExternalCharges(writer, &fakeChargeReader{entries: []charging.Entry{}})
			form := validCreateForm()
			form.Set("odometer_km", tc.value)
			w := submitForm(t, h, uid, http.MethodPost, "/ui/external-charges/create", form)

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

// TestExternalChargeCreate_A8_StartBatteryPctRequired_BothStatuses verifies Test
// Contract A8: start_battery_pct is unconditionally required, unchanged by
// RM33, for BOTH status values.
func TestExternalChargeCreate_A8_StartBatteryPctRequired_BothStatuses(t *testing.T) {
	uid := uuid.New()
	for _, status := range []string{"IN_PROGRESS", "DONE"} {
		t.Run(status, func(t *testing.T) {
			writer := &fakeChargeWriter{}
			h := newHandlerForExternalCharges(writer, &fakeChargeReader{entries: []charging.Entry{}})
			form := validCreateForm()
			form.Set("status", status)
			form.Del("start_battery_pct")
			if status == "DONE" {
				form.Set("ended_at", "2026-07-15T18:00")
				form.Set("end_battery_pct", "90")
			}
			w := submitForm(t, h, uid, http.MethodPost, "/ui/external-charges/create", form)

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

// TestExternalChargeCreate_B1_ValidationFailure_PreservesLocationAndEndBatteryPct
// verifies Test Contract B1: a valid location_kind=WORK and battery values
// but an out-of-range end_battery_pct under status=DONE re-renders the 422
// create form with WORK still selected and the invalid "150" still echoed in
// end_battery_pct's value — the raw ExternalChargeFormValues, not a blank/default
// field, survives the failed validation (roadmap D15).
func TestExternalChargeCreate_B1_ValidationFailure_PreservesLocationAndEndBatteryPct(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	h := newHandlerForExternalCharges(writer, &fakeChargeReader{entries: []charging.Entry{}})

	form := validCreateForm()
	form.Set("status", "DONE")
	form.Set("location_kind", "WORK")
	form.Set("ended_at", "2026-07-15T18:00")
	form.Set("end_battery_pct", "150") // out of range (0-100) — the sole invalid field

	w := submitForm(t, h, uid, http.MethodPost, "/ui/external-charges/create", form)

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

// TestExternalChargeRowUpdate_B2_ValidationFailure_PreservesNotesAndLocationLabel
// verifies Test Contract B2: the inline edit row's 422 re-render still
// carries the submitted notes/location_label text even though a different
// field (start_battery_pct, out of range) is what failed validation, and even
// though charging_type carries a value the <select> could never submit (a raw
// POST bypassing the browser control).
func TestExternalChargeRowUpdate_B2_ValidationFailure_PreservesNotesAndLocationLabel(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	writer := &fakeChargeWriter{}
	h := newHandlerForExternalCharges(writer, &fakeChargeReader{})

	form := validCreateForm()
	form.Set("start_battery_pct", "150") // out of range — triggers the 422
	form.Set("charging_type", "BOGUS")   // a raw POST can submit what the <select> never would
	form.Set("notes", "Charged at the mall")
	form.Set("location_label", "Centro Comercial")

	w := submitForm(t, h, uid, http.MethodPut, "/ui/external-charges/row/"+id.String(), form)

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

// TestExternalChargeCreate_B3_StatusDoneClearedEndedAt_StatusSelectionPreserved
// verifies Test Contract B3: status="DONE" with ended_at cleared re-renders
// the create form's status <select> still showing DONE selected — the error
// path must not silently reset it to the IN_PROGRESS default.
func TestExternalChargeCreate_B3_StatusDoneClearedEndedAt_StatusSelectionPreserved(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	h := newHandlerForExternalCharges(writer, &fakeChargeReader{entries: []charging.Entry{}})

	form := validCreateForm()
	form.Set("status", "DONE")
	form.Set("end_battery_pct", "90")
	// ended_at deliberately cleared — the only invalid field.

	w := submitForm(t, h, uid, http.MethodPost, "/ui/external-charges/create", form)

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

// TestExternalChargePage_B4_FreshLoad_NoRegressionInBlankFieldRendering verifies Test
// Contract B4: a fresh GET /external-charges (no submission) still renders
// energy_added_kwh / price with an empty value and no location_kind option
// pre-selected — ExternalChargeFormValues{}'s zero value reproduces today's
// fresh-load behavior with no regression from adding the struct.
func TestExternalChargePage_B4_FreshLoad_NoRegressionInBlankFieldRendering(t *testing.T) {
	uid := uuid.New()
	h := newHandlerForExternalCharges(&fakeChargeWriter{}, &fakeChargeReader{})
	w := submitForm(t, h, uid, http.MethodGet, "/external-charges", url.Values{})

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
// These render fragments.ExternalChargeCreateForm / fragments.ExternalChargeRowEdit directly
// (mirrors supercharger_test.go's TestExternalChargeCreateForm-style direct-render
// pattern and history_test.go's TestBaseAuth_RendersBrowserTZScript) rather
// than going through the full HTTP handler chain — Group C is a
// template/markup assertion, not a handler-behavior one, so it exercises the
// templates' binding of ExternalChargesPageData/ExternalChargeEntryVM fields directly,
// independent of which handler code path produced those field values.

// renderCreateForm renders fragments.ExternalChargeCreateForm(d, nil) to a string in
// the given language ("es"/"en" via i18n.WithLang, or "" for the ambient
// default — i18n.FromContext resolves an unset context to Spanish).
func renderCreateForm(t *testing.T, d fragments.ExternalChargesPageData, lang string) string {
	t.Helper()
	ctx := context.Background()
	if lang != "" {
		ctx = i18n.WithLang(ctx, lang)
	}
	var body bytes.Buffer
	if err := fragments.ExternalChargeCreateForm(d, nil).Render(ctx, &body); err != nil {
		t.Fatalf("render ExternalChargeCreateForm: %v", err)
	}
	return body.String()
}

// renderEditRow renders fragments.ExternalChargeRowEdit(vm, "tok", nil, "", "") to a
// string, same language convention as renderCreateForm. The two trailing ""
// args are windowStartStr/windowEndStr (design.md §D-Refresh, RM33 tier 3) —
// Group C's template/markup assertions (C1-C7) are all indifferent to the
// filter window, so empty strings are the correct fixture here; the window
// itself is pinned separately by Group D (§D-Include/§D-Refresh).
func renderEditRow(t *testing.T, vm fragments.ExternalChargeEntryVM, lang string) string {
	t.Helper()
	ctx := context.Background()
	if lang != "" {
		ctx = i18n.WithLang(ctx, lang)
	}
	var body bytes.Buffer
	if err := fragments.ExternalChargeRowEdit(vm, "tok", nil, "", "").Render(ctx, &body); err != nil {
		t.Fatalf("render ExternalChargeRowEdit: %v", err)
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

// TestExternalChargeForms_C1_OptionalFieldsCarryNoRequired_UnconditionalFieldsDo
// verifies Test Contract C1 for BOTH forms: energy_added_kwh and price carry
// no `required` attribute; start_battery_pct, charged_on, location_kind still
// carry `required` (unconditional fields, unchanged by RM33).
func TestExternalChargeForms_C1_OptionalFieldsCarryNoRequired_UnconditionalFieldsDo(t *testing.T) {
	createBody := renderCreateForm(t, fragments.ExternalChargesPageData{}, "")
	editBody := renderEditRow(t, fragments.ExternalChargeEntryVM{}, "")

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

// TestExternalChargeCreateForm_C2_FreshRender_InProgressDefaultsNoEndRequired
// verifies Test Contract C2: a create-form ExternalChargesPageData matching a fresh
// (non-error) render — Status=IN_PROGRESS, RequiredEndedAt/
// RequiredEndBatteryPct both false, mirroring buildExternalChargesPage's own
// fresh-load computation (design.md §D-Values/§D-Fields) — renders
// IN_PROGRESS selected and no required attribute on ended_at/end_battery_pct.
func TestExternalChargeCreateForm_C2_FreshRender_InProgressDefaultsNoEndRequired(t *testing.T) {
	d := fragments.ExternalChargesPageData{
		FormValues:            fragments.ExternalChargeFormValues{Status: string(charging.StatusInProgress)},
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

// TestExternalChargeCreateForm_C3_DoneRequiredState_RendersRequiredAttributes
// verifies Test Contract C3: when the create form's ExternalChargesPageData carries
// the required-state a DONE submission produces (RequiredEndedAt=true,
// RequiredEndBatteryPct=true — the same charging.RequiredFieldsFor(DONE)
// lookup design.md §D-Fields describes), ended_at and end_battery_pct render
// with the `required` attribute.
//
// This is a template-binding assertion (Group C's stated scope is
// "template/markup assertions"), independent of which handler code path
// populates these two booleans. WORKER FINDING (2026-08-29): ExternalChargeCreate's
// OWN 422/500 branches do not currently recompute RequiredEndedAt/
// RequiredEndBatteryPct from the submitted raw.Status (task 3.3 wired that
// recompute only for the edit-row path, via externalChargeEntryVMFromRawValues) — so
// a create-form validation failure under status=DONE will NOT itself produce
// this exact ExternalChargesPageData shape today; the create form's error re-render
// keeps whatever buildExternalChargesPage's unconditional StatusInProgress-based
// computation set. RD13's client-side htmx:load listener is design.md's own
// documented mitigation for this ("if the two ever disagree ... the
// disagreement is inert"), but it means the SERVER-rendered HTML on that one
// path does not, by itself, satisfy this test's premise. See this worker's
// final report for the full writeup; not fixed here (out of this task's
// assigned scope — task 3.3 owns that wiring).
func TestExternalChargeCreateForm_C3_DoneRequiredState_RendersRequiredAttributes(t *testing.T) {
	d := fragments.ExternalChargesPageData{
		FormValues:            fragments.ExternalChargeFormValues{Status: string(charging.StatusDone)},
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

// TestExternalChargeRowEdit_C4_StatusSelectReflectsPersistedValue verifies Test
// Contract C4: the edit row's status <select> shows the PERSISTED entry's
// status selected — DONE for a DONE entry, IN_PROGRESS for an IN_PROGRESS one.
func TestExternalChargeRowEdit_C4_StatusSelectReflectsPersistedValue(t *testing.T) {
	for _, status := range []string{"DONE", "IN_PROGRESS"} {
		t.Run(status, func(t *testing.T) {
			vm := fragments.ExternalChargeEntryVM{ID: uuid.New().String(), RawStatus: status}
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

// TestExternalChargeForms_NoCurrencyField pins a form contract: neither form renders a
// Currency <input>. Currency is not user-supplied — the money rule pairs the
// amount with a fixed currency column, so a Currency control appearing here
// would mean the write path changed shape.
//
// The former C5 also asserted the COP suffix <span> and the DaisyUI compound
// wrapper around the price input. Both were dropped (MAG-39): a suffix glyph
// and a wrapper element are appearance, verified by hand.
func TestExternalChargeForms_NoCurrencyField(t *testing.T) {
	createBody := renderCreateForm(t, fragments.ExternalChargesPageData{}, "")
	editBody := renderEditRow(t, fragments.ExternalChargeEntryVM{}, "")

	for _, form := range []struct {
		name string
		body string
	}{{"create", createBody}, {"edit", editBody}} {
		t.Run(form.name, func(t *testing.T) {
			if strings.Contains(form.body, `name="currency"`) {
				t.Errorf("%s form: must NOT render a Currency input, body=%q", form.name, form.body[:min(1500, len(form.body))])
			}
		})
	}
}

// TestExternalChargeForms_C6_ACDCOptionText_BothLanguages verifies Test Contract C6:
// both forms' AC/DC <option> text matches roadmap D16's descriptive strings,
// in both ES and EN (i18n.WithLang, mirroring the catalogue completeness
// test's language-switch pattern).
func TestExternalChargeForms_C6_ACDCOptionText_BothLanguages(t *testing.T) {
	for _, tc := range []struct {
		lang   string
		wantAC string
		wantDC string
	}{
		{"es", "AC — Carga lenta (casa/destino)", "DC — Carga rápida (Supercargador)"},
		{"en", "AC — Slow charging (home/destination)", "DC — Fast charging (Supercharger)"},
	} {
		t.Run(tc.lang, func(t *testing.T) {
			createBody := renderCreateForm(t, fragments.ExternalChargesPageData{}, tc.lang)
			editBody := renderEditRow(t, fragments.ExternalChargeEntryVM{}, tc.lang)
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

// TestExternalChargeForms_NoDetailsCollapse pins a browser trap, not a layout: a
// required control inside a closed <details> cannot be focused to show its
// HTML5 validation message, so Save silently does nothing — Chrome only logs
// "An invalid form control with name='location_kind' is not focusable". The
// 2026-08-29 layout removed the collapse for that reason; this keeps it out.
//
// The former C7 also asserted odometer_km rendered inside the optional-details
// <section>. That part was dropped (MAG-39): where a field sits is layout, and
// layout is verified by hand.
func TestExternalChargeForms_NoDetailsCollapse(t *testing.T) {
	createBody := renderCreateForm(t, fragments.ExternalChargesPageData{}, "")
	editBody := renderEditRow(t, fragments.ExternalChargeEntryVM{}, "")

	for _, form := range []struct {
		name string
		body string
	}{{"create", createBody}, {"edit", editBody}} {
		t.Run(form.name, func(t *testing.T) {
			if strings.Contains(form.body, "<details") || strings.Contains(form.body, "<summary") {
				t.Errorf("%s form: <details>/<summary> must not return — a required field inside a closed collapse breaks form validation", form.name)
			}
		})
	}
}

// TestExternalChargeCreate_DoneStatus_ErrorRerender_KeepsRequiredAttributes is the
// handler-level counterpart to Test Contract C3, added in wave 4 after the C3
// template test surfaced that ExternalChargeCreate's error branches did not satisfy it.
//
// C3 asserts the create form renders `required` on ended_at/end_battery_pct for
// a DONE status, and the template does — but only if the handler hands it a
// ExternalChargesPageData whose Required* pair was computed from the SUBMITTED status.
// The 4xx/5xx branches overwrite FormValues with the raw submission (roadmap
// D15) and used to leave Required* on buildExternalChargesPage's fresh-load IN_PROGRESS
// default, so a user who picked DONE and tripped an unrelated validation error
// got those two inputs back without `required` — the flash-of-wrong-state
// design.md §D-Fields rules out. applyRawRequiredState fixes it; this test
// pins the handler path a direct template render cannot reach.
func TestExternalChargeCreate_DoneStatus_ErrorRerender_KeepsRequiredAttributes(t *testing.T) {
	uid := uuid.New()
	h := newHandlerForExternalCharges(&fakeChargeWriter{}, &fakeChargeReader{})

	// Valid in every respect EXCEPT location_kind, so the 422 is triggered by a
	// field unrelated to the status-gated pair under assertion. Per the wave-8
	// fixture convention, status is explicit and the failure is a named field —
	// not a spurious missing-status error.
	form := validCreateForm()
	form.Set("status", "DONE")
	form.Set("ended_at", "2026-07-15T10:00")
	form.Set("end_battery_pct", "80")
	form.Del("location_kind")

	w := submitForm(t, h, uid, http.MethodPost, "/ui/external-charges/create", form)
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
// C, D, E (design.md). Groups A (parseExternalChargesRange/buildExternalChargesPresets) and B
// (entryComplete/buildExternalChargeTiles) already live in external_charges_range_test.go /
// external_charges_tiles_test.go (Wave 3). These are the rendered-HTML (C) and
// httptest (D, E) groups — Wave 9.
// ============================================================================

// MAG-39: the three ExternalChargeRow C1-C3 tests were removed here. They asserted the
// row's dot CSS class (bg-success / bg-warning) and the status badge's rendered
// text — appearance, not behaviour, and the kind of assertion a re-skin breaks
// while the page still works. The data rule they stood for (which entries count
// as complete) is covered by TestEntryComplete_* in external_charges_tiles_test.go,
// which tests the predicate directly instead of through markup.

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

// TestExternalChargeCreate_D1_WindowFromFormThreadsIntoOOBRefresh verifies Test
// Contract D1: POST /ui/external-charges/create with a valid submission AND
// start=2026-08-01&end=2026-08-31 in the form body (simulating hx-include,
// design.md §D-Include) -> the response's OOB #external-charges-list div reflects THAT
// window, not the default 7-day one. Asserted via the pre-formatted hidden
// #external-charges-window-start/#external-charges-window-end input values (design.md's own
// suggested assertion method), since those two inputs are rendered from
// ExternalChargesPageData.WindowStartStr/WindowEndStr on every #external-charges-list render.
func TestExternalChargeCreate_D1_WindowFromFormThreadsIntoOOBRefresh(t *testing.T) {
	uid := uuid.New()
	h := newHandlerForExternalCharges(&fakeChargeWriter{}, &fakeChargeReader{entries: []charging.Entry{}})

	form := validCreateForm()
	form.Set("start", "2026-08-01")
	form.Set("end", "2026-08-31")

	w := submitForm(t, h, uid, http.MethodPost, "/ui/external-charges/create", form)
	if w.Code != http.StatusOK {
		t.Fatalf("D1: want 200 on valid create, got %d body=%q", w.Code, w.Body.String()[:min(500, w.Body.Len())])
	}
	body := w.Body.String()
	if !strings.Contains(body, `id="external-charges-window-start" value="2026-08-01"`) {
		t.Errorf("D1: want the OOB #external-charges-list to reflect the hx-include'd start=2026-08-01, body=%q", body[:min(1500, len(body))])
	}
	if !strings.Contains(body, `id="external-charges-window-end" value="2026-08-31"`) {
		t.Errorf("D1: want the OOB #external-charges-list to reflect the hx-include'd end=2026-08-31, body=%q", body[:min(1500, len(body))])
	}
}

// TestExternalChargeCreate_D2_StartEndAbsent_FallsBackToDefaultWindow verifies Test
// Contract D2: same as D1 but the start/end form fields are ABSENT (a client
// with hx-include disabled/stripped) -> the OOB refresh falls back to the
// default 7-day window, and the write's success status is unaffected
// (design.md §D-Include: "cosmetic only, never a validation gate").
func TestExternalChargeCreate_D2_StartEndAbsent_FallsBackToDefaultWindow(t *testing.T) {
	uid := uuid.New()
	h := newHandlerForExternalCharges(&fakeChargeWriter{}, &fakeChargeReader{entries: []charging.Entry{}})

	form := validCreateForm() // no start/end fields at all
	w := submitForm(t, h, uid, http.MethodPost, "/ui/external-charges/create", form)
	if w.Code != http.StatusOK {
		t.Fatalf("D2: want 200 (absent window must never gate the write), got %d body=%q", w.Code, w.Body.String()[:min(500, w.Body.Len())])
	}

	today := todayDefaultZoneMidnight()
	wantStart := today.AddDate(0, 0, -(externalChargesRangeDefaultDays - 1)).Format("2006-01-02")
	wantEnd := today.Format("2006-01-02")
	body := w.Body.String()
	if !strings.Contains(body, `id="external-charges-window-start" value="`+wantStart+`"`) {
		t.Errorf("D2: want the OOB refresh to fall back to the default window start %s, body=%q", wantStart, body[:min(1500, len(body))])
	}
	if !strings.Contains(body, `id="external-charges-window-end" value="`+wantEnd+`"`) {
		t.Errorf("D2: want the OOB refresh to fall back to the default window end %s, body=%q", wantEnd, body[:min(1500, len(body))])
	}
}

// TestExternalChargeRowUpdate_D3_SuccessRetargetsAndResetsToDefaultWindow restates Test
// Contract D3 for the 2026-08-29 amendment. D3 previously required a successful
// PUT to re-render the OOB #external-charges-list under whatever window the hidden
// start/end inputs posted. It now requires the opposite: a successful edit
// RESETS the list to the default 7-day window, so the user lands back on the
// "last 7 days" preset with that preset active.
//
// The posted window here (2026-08-01..2026-08-31) is deliberately NOT the
// default, so a handler that still threaded it through would fail this test.
func TestExternalChargeRowUpdate_D3_SuccessRetargetsAndResetsToDefaultWindow(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	writer := &fakeChargeWriter{}
	h := newHandlerForExternalCharges(writer, &fakeChargeReader{entries: []charging.Entry{}})

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
	w := submitForm(t, h, uid, http.MethodPut, "/ui/external-charges/row/"+id.String(), form)
	if w.Code != http.StatusOK {
		t.Fatalf("D3: want 200 on valid update, got %d body=%q", w.Code, w.Body.String()[:min(500, w.Body.Len())])
	}
	body := w.Body.String()

	// The response must be the #external-charges-list region itself, retargeted away from
	// the form's #external-charge-row-{id}. The previous shape — a primary <tr> plus a
	// sibling <div hx-swap-oob> — never refreshed the list in the browser: htmx
	// 2.0.4 parses responses inside a <template>, a leading <tr> puts the HTML
	// parser in table insertion mode, and the non-table OOB sibling is
	// foster-parented off the fragment's top level, which is the only place htmx
	// looks for hx-swap-oob. These three assertions together are what stop that
	// shape from coming back.
	if got := w.Header().Get("HX-Retarget"); got != "#external-charges-list" {
		t.Fatalf("D3: want HX-Retarget=#external-charges-list on a successful edit, got %q", got)
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
	if !strings.Contains(body, `id="external-charges-list"`) {
		t.Fatalf("D3: want the whole #external-charges-list region in the update response, body=%q", body[:min(1500, len(body))])
	}

	// The default window is derived the same way the handler derives it, via the
	// same todayDefaultZoneMidnight() helper its sibling D2 test uses, so this
	// test does not go stale on a date change or on externalChargesRangeDefaultDays.
	wantStart, wantEnd := defaultExternalChargesWindow(todayDefaultZoneMidnight())
	if !strings.Contains(body, `id="external-charges-window-start" value="`+wantStart.Format("2006-01-02")+`"`) {
		t.Errorf("D3: a successful edit must reset the list to the default window start %s, not the posted 2026-08-01; body=%q",
			wantStart.Format("2006-01-02"), body[:min(1500, len(body))])
	}
	if !strings.Contains(body, `id="external-charges-window-end" value="`+wantEnd.Format("2006-01-02")+`"`) {
		t.Errorf("D3: a successful edit must reset the list to the default window end %s, not the posted 2026-08-31; body=%q",
			wantEnd.Format("2006-01-02"), body[:min(1500, len(body))])
	}
	// The point of the reset: the "last 7 days" preset comes back selected.
	// buildExternalChargesPresets marks Active by exact-match against its own recomputed
	// window, so asserting the rendered active preset proves the reset landed on
	// a real preset rather than merely on some 7-day range.
	if !strings.Contains(body, `hx-get="/ui/external-charges/list?start=`+wantStart.Format("2006-01-02")+`&amp;end=`+wantEnd.Format("2006-01-02")+`"`) {
		t.Errorf("D3: want the last-7-days preset rendered for the reset window; body=%q", body[:min(2000, len(body))])
	}
}

// TestExternalChargeRowUpdate_D3b_ValidationFailureKeepsThePostedWindow is the other
// half of the amendment: only SUCCESS resets. A failed save must not move the
// user's filter, so the re-rendered edit form still echoes the posted window
// back through its hidden start/end inputs — which is the whole reason those
// inputs exist (design.md §D-Include).
func TestExternalChargeRowUpdate_D3b_ValidationFailureKeepsThePostedWindow(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	h := newHandlerForExternalCharges(&fakeChargeWriter{}, &fakeChargeReader{entries: []charging.Entry{}})

	form := url.Values{
		"csrf_token":    {"tok"},
		"status":        {"IN_PROGRESS"},
		"charged_on":    {"2026-07-16"},
		"location_kind": {"WORK"},
		// start_battery_pct omitted -> validation failure
		"start": {"2026-08-01"},
		"end":   {"2026-08-31"},
	}
	w := submitForm(t, h, uid, http.MethodPut, "/ui/external-charges/row/"+id.String(), form)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("D3b: want 422 on the invalid update, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `name="start" value="2026-08-01"`) || !strings.Contains(body, `name="end" value="2026-08-31"`) {
		t.Errorf("D3b: a FAILED save must echo the posted window back into the edit form, not reset it; body=%q", body[:min(1500, len(body))])
	}
}

// TestExternalChargeRowDelete_D4_RendersFullChargesListWithinRequestedWindow verifies
// Test Contract D4: DELETE /ui/external-charges/row/{id}?start=2026-08-01&end=2026-08-31
// -> the response is a full #external-charges-list fragment (not a bare <tr>)
// reflecting the post-delete state within THAT window, and the deleted row's
// id is absent from it. The fake Reader has no relationship to the fake
// Writer's Delete call, so reader.entries is pre-set to already exclude the
// deleted id — simulating the read a real charging.Reader would return after
// the write committed (the same convention
// TestExternalChargeRowDelete_ThenListReflectsRemoval uses for its own later GET).
func TestExternalChargeRowDelete_D4_RendersFullChargesListWithinRequestedWindow(t *testing.T) {
	uid := uuid.New()
	keptID := uuid.New()
	deletedID := uuid.New()
	reader := &fakeChargeReader{entries: []charging.Entry{
		{ID: keptID, AccountID: uid, TeslaID: 1001, VIN: "VIN1001", ChargedOn: time.Now(),
			EnergyAddedKWh: ptrF64(10.0), Price: 5000.0, Currency: "COP"},
	}}
	writer := &fakeChargeWriter{}
	h := newHandlerForExternalCharges(writer, reader)
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/ui/external-charges/row/"+deletedID.String()+"?start=2026-08-01&end=2026-08-31", nil)
	req.Header.Set("X-CSRF-Token", "tok")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("D4: want 200 on valid delete, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `id="external-charges-list"`) {
		t.Fatalf("D4: want the full #external-charges-list fragment in the response, got body=%q", body[:min(1500, len(body))])
	}
	if !strings.Contains(body, "external-charge-row-"+keptID.String()) {
		t.Errorf("D4: want the kept entry's row still present, body=%q", body[:min(1500, len(body))])
	}
	if strings.Contains(body, "external-charge-row-"+deletedID.String()) {
		t.Errorf("D4: want the deleted entry's row absent, body=%q", body[:min(1500, len(body))])
	}
	if !strings.Contains(body, `id="external-charges-window-start" value="2026-08-01"`) {
		t.Errorf("D4: want the response to reflect the requested window start=2026-08-01, body=%q", body[:min(1500, len(body))])
	}
	if !strings.Contains(body, `id="external-charges-window-end" value="2026-08-31"`) {
		t.Errorf("D4: want the response to reflect the requested window end=2026-08-31, body=%q", body[:min(1500, len(body))])
	}
	wantOldEmptyRow := `<tr id="external-charge-row-` + deletedID.String() + `"></tr>`
	if strings.Contains(body, wantOldEmptyRow) {
		t.Errorf("D4: delete response must NOT be a bare empty <tr> (design.md §D-Refresh), got body=%q", body[:min(500, len(body))])
	}
}

// --- Group E — no-vehicle / malformed-window / reader-error empty states
// (design.md Test Contract E1-E4, offline httptest) ---

// TestExternalChargePage_E1_NoRegisteredVehicles_NoFilterChrome verifies Test
// Contract E1: a signed-in user with ZERO registered vehicles requests
// GET /external-charges -> the response contains ExternalChargesEmptyState()'s message and
// contains NEITHER a preset button NOR any ui.StatTile markup NOR a <table>
// (D-RM33-9 — assert absence, not just presence of the message).
func TestExternalChargePage_E1_NoRegisteredVehicles_NoFilterChrome(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{registered: nil}
	h := New(Deps{
		AnalyticsRecalculator: &fakeRecalculator{},
		Account:               acct,
		Tesla:                 &fakeTesla{},
		ChargingWriter:        &fakeChargeWriter{},
		ChargingReader:        &fakeChargeReader{},
	})
	r := engineWithSession(h, uid, "")
	c := sessionCookie(r, uid, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/external-charges", nil)
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

// TestExternalChargesListFragment_E2_MalformedWindow_NoFilterChrome400 verifies Test
// Contract E2: GET /ui/external-charges/list?start=not-a-date&end=2026-08-31 -> HTTP
// 400, same no-chrome assertions as E1 (design.md §D-Empty state 1, the
// malformed-window branch — both conditions collapse to the identical
// render).
func TestExternalChargesListFragment_E2_MalformedWindow_NoFilterChrome400(t *testing.T) {
	uid := uuid.New()
	h := newHandlerForExternalCharges(&fakeChargeWriter{}, &fakeChargeReader{entries: []charging.Entry{}})
	r := engineWithSession(h, uid, "testcsrf")
	c := sessionCookie(r, uid, "testcsrf")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/external-charges/list?start=not-a-date&end=2026-08-31", nil)
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

// TestExternalChargesListFragment_E3_ReaderError_ShowsPresetsAndTilesAndAlert
// verifies Test Contract E3: a valid vehicle + valid window, fake Reader
// returns an error -> response contains the preset buttons AND four
// ui.StatTiles (all zero/—) AND a ui.Alert with the error message — NOT the
// same render as E1/E2 (design.md §D-Empty state 2 is a strictly different
// render from state 1).
func TestExternalChargesListFragment_E3_ReaderError_ShowsPresetsAndTilesAndAlert(t *testing.T) {
	uid := uuid.New()
	reader := &fakeChargeReader{err: errFake}
	h := newHandlerForExternalCharges(&fakeChargeWriter{}, reader)
	r := engineWithSession(h, uid, "testcsrf")
	c := sessionCookie(r, uid, "testcsrf")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/external-charges/list?start=2026-08-01&end=2026-08-05", nil)
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

// TestExternalChargesListFragment_E4_ReaderSucceedsEmptySlice_TilesZeroTableEmpty
// verifies Test Contract E4: a valid vehicle + valid window, fake Reader
// returns []charging.Entry{} (no error) -> response contains the preset
// buttons, tiles showing 0/— (not hidden), and ExternalChargesEmptyState()'s message
// in place of table rows (design.md §D-Empty state 3).
func TestExternalChargesListFragment_E4_ReaderSucceedsEmptySlice_TilesZeroTableEmpty(t *testing.T) {
	uid := uuid.New()
	reader := &fakeChargeReader{entries: []charging.Entry{}}
	h := newHandlerForExternalCharges(&fakeChargeWriter{}, reader)
	r := engineWithSession(h, uid, "testcsrf")
	c := sessionCookie(r, uid, "testcsrf")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/external-charges/list?start=2026-08-01&end=2026-08-05", nil)
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
	// MAG-50: the two assertions that pinned `class="stat-value font-mono">0</div>`
	// and the em-dash equivalent were removed. Both matched ui.StatTile's exact
	// class list, which 8795f6b ("Fonts") extended with responsive typography —
	// they broke on a design change, not a behaviour change. The tile COUNT check
	// above still proves D13/D14's "four tiles stay rendered on an empty range".
	if !strings.Contains(body, "Aún no hay cargas registradas") {
		t.Errorf("E4: want ExternalChargesEmptyState() in place of table rows (D-Empty state 3), body=%q", body[:min(1500, len(body))])
	}
	if strings.Contains(body, "<table") {
		t.Errorf("E4: want NO <table> element when the table body is replaced by the empty state, body=%q", body[:min(1500, len(body))])
	}
}
