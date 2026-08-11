package handlers

import (
	"context"
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
	"github.com/cristianpena/magus-tesla-api/internal/manualcharge"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// --- fakes for manualcharge.Writer and manualcharge.Reader ---

type fakeChargeWriter struct {
	createEntry manualcharge.Entry
	createErr   error
	updateEntry manualcharge.Entry
	updateErr   error
	deleteErr   error
	deleteCalls int // count of Delete invocations — lets tests assert a rejected write never reached the port
}

func (f *fakeChargeWriter) Create(_ context.Context, e manualcharge.Entry) (manualcharge.Entry, error) {
	if f.createErr != nil {
		return manualcharge.Entry{}, f.createErr
	}
	e.ID = uuid.New()
	e.CreatedAt = time.Now()
	e.UpdatedAt = time.Now()
	f.createEntry = e
	return e, nil
}

func (f *fakeChargeWriter) Update(_ context.Context, e manualcharge.Entry) (manualcharge.Entry, error) {
	if f.updateErr != nil {
		return manualcharge.Entry{}, f.updateErr
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
	entries []manualcharge.Entry
	err     error
}

func (f *fakeChargeReader) ListEntriesByVehicle(_ context.Context, _ uuid.UUID, _ int64, _ int) ([]manualcharge.Entry, error) {
	return f.entries, f.err
}

func (f *fakeChargeReader) ListEntriesByAccount(_ context.Context, _ uuid.UUID, _ int) ([]manualcharge.Entry, error) {
	return f.entries, f.err
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
		Account:            acct,
		Tesla:              &fakeTesla{},
		TelemetryReader:    &fakeReader{},
		ManualChargeWriter: writer,
		ManualChargeReader: reader,
	})
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
	reader := &fakeChargeReader{entries: []manualcharge.Entry{}}
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

// TestChargesListFragment_WithEntries verifies the list fragment returns only the fragment.
func TestChargesListFragment_WithEntries(t *testing.T) {
	uid := uuid.New()
	chargedOn := time.Now()
	entries := []manualcharge.Entry{
		{
			ID:             uuid.New(),
			AccountID:      uid,
			TeslaID:        1001,
			VIN:            "VIN1001",
			ChargedOn:      chargedOn,
			EnergyAddedKWh: 10.0,
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
}

// TestChargesListFragment_ReaderError verifies graceful degradation on reader failure.
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
}

// TestChargesListFragment_EmptyState verifies empty-state message on empty list.
func TestChargesListFragment_EmptyState(t *testing.T) {
	uid := uuid.New()
	reader := &fakeChargeReader{entries: []manualcharge.Entry{}}
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
	if !strings.Contains(body, "No charge entries") {
		t.Errorf("want empty-state message, body=%q", body[:min(500, len(body))])
	}
}

// TestChargeCreate_CSRFMismatch verifies 403 on CSRF token mismatch.
func TestChargeCreate_CSRFMismatch(t *testing.T) {
	uid := uuid.New()
	h := newHandlerForCharges(&fakeChargeWriter{}, &fakeChargeReader{})
	r := engineWithSession(h, uid, "correcttoken")
	c := sessionCookie(r, uid, "correcttoken")

	form := url.Values{
		"csrf_token":       {"wrongtoken"},
		"charged_on":       {"2026-07-15"},
		"energy_added_kwh": {"10.5"},
		"price":            {"5000"},
		"location_kind":    {"HOME"},
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
		Account:            acct,
		Tesla:              &fakeTesla{},
		TelemetryReader:    &fakeReader{},
		ManualChargeWriter: &fakeChargeWriter{},
		ManualChargeReader: &fakeChargeReader{},
	})
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	form := url.Values{
		"csrf_token":        {"tok"},
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
	if !strings.Contains(body, "Please select a vehicle") {
		t.Errorf("want 'Please select a vehicle' in body, got %q", body[:min(500, len(body))])
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
	reader := &fakeChargeReader{entries: []manualcharge.Entry{}}
	h := newHandlerForCharges(writer, reader)
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	form := url.Values{
		"csrf_token":         {"tok"},
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
	if writer.createEntry.EnergyAddedKWh != 10.5 {
		t.Errorf("want energy 10.5 persisted, got %f", writer.createEntry.EnergyAddedKWh)
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
	entries := []manualcharge.Entry{
		{
			ID:             entryID,
			AccountID:      uid,
			TeslaID:        1001,
			VIN:            "VIN1001",
			ChargedOn:      time.Now(),
			EnergyAddedKWh: 15.0,
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

// TestChargeRowUpdate_ValidInput verifies a valid PUT renders the static row on success.
func TestChargeRowUpdate_ValidInput(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	writer := &fakeChargeWriter{}
	h := newHandlerForCharges(writer, &fakeChargeReader{})
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	form := url.Values{
		"csrf_token":        {"tok"},
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
	if !strings.Contains(body, "charge-row-"+id.String()) {
		t.Errorf("want static row with id in update response, body=%q", body[:min(500, len(body))])
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

// TestChargeRowDelete_ValidInput_RendersEmptyRow verifies a valid DELETE returns
// the empty-row fragment (T1.3 — MAG-5 D3). The success body is the empty
// `<tr id="charge-row-<id>"></tr>` that htmx outerHTML-swaps in for the deleted
// row; it must NOT contain a `<td>` (which would be the error-row variant). The
// CSRF token is sent via the X-CSRF-Token HEADER (matching the charge_row.templ
// fix that emits hx-headers carrying X-CSRF-Token — Go's net/http parses DELETE
// request BODIES for no method, so the prior hx-include body path silently
// 403'd; the header path is the fix — see ChargeRowDelete doc comment).
func TestChargeRowDelete_ValidInput_RendersEmptyRow(t *testing.T) {
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
	wantEmpty := `<tr id="charge-row-` + id.String() + `"></tr>`
	if !strings.Contains(body, wantEmpty) {
		t.Errorf("want empty row %q in delete response, got body=%q", wantEmpty, body[:min(500, len(body))])
	}
	// Stronger: an error-row (ChargeRowError) would contain a <td> child; the
	// empty row must not.
	if strings.Contains(body, "<td") {
		t.Errorf("delete success response must NOT contain a <td> (would be the error-row variant), got body=%q",
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
	// manualcharge.Writer.Delete — asserting only the HTTP response left this
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
	if writer.createEntry.EnergyAddedKWh != 0 {
		t.Errorf("no entry should be created on CSRF failure, got energy %f", writer.createEntry.EnergyAddedKWh)
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
	if writer.updateEntry.EnergyAddedKWh != 0 {
		t.Errorf("no entry should be updated on CSRF failure, got energy %f", writer.updateEntry.EnergyAddedKWh)
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

	e := manualcharge.Entry{
		ID:             uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		AccountID:      uuid.New(),
		TeslaID:        1001,
		VIN:            "VIN1001",
		ChargedOn:      time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC),
		EnergyAddedKWh: 12.5,
		Price:          15000.0,
		Currency:       "COP",
		StartedAt:      &start,
		EndedAt:        &end,
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

// --- Sub-task D: required location_kind tests ---

// TestChargeCreate_MissingLocationKind verifies 422 when location_kind is absent.
func TestChargeCreate_MissingLocationKind(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	reader := &fakeChargeReader{entries: []manualcharge.Entry{}}
	h := newHandlerForCharges(writer, reader)
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	form := url.Values{
		"csrf_token":         {"tok"},
		"charged_on":         {"2026-07-15"},
		"energy_added_kwh":   {"10.5"},
		"price":              {"5000"},
		"start_battery_pct":  {"50"},
		"end_battery_pct":    {"80"},
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
	if writer.createEntry.EnergyAddedKWh != 0 {
		t.Errorf("Writer.Create must NOT be called on missing location_kind, got energy %f",
			writer.createEntry.EnergyAddedKWh)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Location is required") {
		t.Errorf("want location_kind error in response, body=%q", body[:min(500, len(body))])
	}
}

// TestChargeCreate_InvalidLocationKind verifies 422 when location_kind has an unrecognized value.
func TestChargeCreate_InvalidLocationKind(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	reader := &fakeChargeReader{entries: []manualcharge.Entry{}}
	h := newHandlerForCharges(writer, reader)
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	form := url.Values{
		"csrf_token":         {"tok"},
		"charged_on":         {"2026-07-15"},
		"energy_added_kwh":   {"10.5"},
		"price":              {"5000"},
		"location_kind":      {"INVALID"},
		"start_battery_pct":  {"50"},
		"end_battery_pct":    {"80"},
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
	if writer.createEntry.EnergyAddedKWh != 0 {
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
		"csrf_token":         {"tok"},
		"charged_on":         {"2026-07-16"},
		"energy_added_kwh":   {"20.0"},
		"price":              {"9000"},
		"start_battery_pct":  {"40"},
		"end_battery_pct":    {"75"},
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
	if writer.updateEntry.EnergyAddedKWh != 0 {
		t.Errorf("Writer.Update must NOT be called on missing location_kind")
	}
}

// TestChargeCreate_ValidLocationKind verifies that a valid location_kind succeeds.
func TestChargeCreate_ValidLocationKind(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	reader := &fakeChargeReader{entries: []manualcharge.Entry{}}
	h := newHandlerForCharges(writer, reader)
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	form := url.Values{
		"csrf_token":         {"tok"},
		"charged_on":         {"2026-07-15"},
		"energy_added_kwh":   {"10.5"},
		"price":              {"5000"},
		"location_kind":      {"HOME"},
		"start_battery_pct":  {"50"},
		"end_battery_pct":    {"80"},
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
		Account:            acct,
		Tesla:              &fakeTesla{},
		TelemetryReader:    &fakeReader{},
		ManualChargeWriter: &fakeChargeWriter{},
		ManualChargeReader: &fakeChargeReader{},
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
		Account:            acct,
		Tesla:              &fakeTesla{},
		TelemetryReader:    &fakeReader{},
		ManualChargeWriter: &fakeChargeWriter{},
		ManualChargeReader: &fakeChargeReader{},
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
		Account:            acct,
		Tesla:              &fakeTesla{},
		TelemetryReader:    reader,
		ManualChargeWriter: &fakeChargeWriter{},
		ManualChargeReader: &fakeChargeReader{},
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
	if want := `placeholder="Latest: 73%"`; !strings.Contains(body, want) {
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
		Account:            acct,
		Tesla:              &fakeTesla{},
		TelemetryReader:    reader,
		ManualChargeWriter: &fakeChargeWriter{},
		ManualChargeReader: &fakeChargeReader{},
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
		Account:            acct,
		Tesla:              &fakeTesla{},
		TelemetryReader:    reader,
		ManualChargeWriter: &fakeChargeWriter{},
		ManualChargeReader: &fakeChargeReader{},
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
	reader := &fakeChargeReader{entries: []manualcharge.Entry{}}
	h := newHandlerForCharges(writer, reader)
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	form := url.Values{
		"csrf_token":         {"tok"},
		"charged_on":         {"2026-07-15"},
		"energy_added_kwh":   {"10.5"},
		"price":              {"5000"},
		"location_kind":      {"HOME"},
		"end_battery_pct":    {"80"},
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
	if writer.createEntry.EnergyAddedKWh != 0 {
		t.Errorf("Writer.Create must NOT be called when start_battery_pct is missing")
	}
	body := w.Body.String()
	if !strings.Contains(body, "Battery percentage is required") {
		t.Errorf("want 'Battery percentage is required' message, got body=%q", body[:min(500, len(body))])
	}
}

// TestChargeCreate_OutOfRangeBatteryPct_Rejected verifies D6: out-of-range
// start_battery_pct (e.g. 101 or -1) is rejected with the out-of-range message
// and the Writer is not called. End battery % mirrors via the same check.
func TestChargeCreate_OutOfRangeBatteryPct_Rejected(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	reader := &fakeChargeReader{entries: []manualcharge.Entry{}}
	h := newHandlerForCharges(writer, reader)
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	for _, tc := range []struct {
		name        string
		start, end  string
		wantInBody  string
	}{
		{"start=101", "101", "80", "Start battery percentage must be an integer between 0 and 100"},
		{"start=-1", "-1", "80", "Start battery percentage must be an integer between 0 and 100"},
		{"end=101", "50", "101", "End battery percentage must be an integer between 0 and 100"},
		{"end=-1", "50", "-1", "End battery percentage must be an integer between 0 and 100"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			form := url.Values{
				"csrf_token":         {"tok"},
				"charged_on":         {"2026-07-15"},
				"energy_added_kwh":   {"10.5"},
				"price":              {"5000"},
				"location_kind":      {"HOME"},
				"start_battery_pct":  {tc.start},
				"end_battery_pct":    {tc.end},
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
			if writer.createEntry.EnergyAddedKWh != 0 {
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
// and ended_at inputs are pre-filled with today's date at UTC midnight
// ("YYYY-MM-DDT00:00") via ChargesPageData.DefaultStartedAt / DefaultEndedAt.
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
	todayDefault := time.Now().UTC().Format("2006-01-02") + "T00:00"
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
	reader := &fakeChargeReader{entries: []manualcharge.Entry{}}
	h := newHandlerForCharges(writer, reader)
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	form := url.Values{
		"csrf_token":         {"tok"},
		"charged_on":         {"2026-07-15"},
		"energy_added_kwh":   {"10.5"},
		"price":              {"5000"},
		"location_kind":      {"HOME"},
		"start_battery_pct":  {"50"},
		"end_battery_pct":    {"80"},
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
	reader := &fakeChargeReader{entries: []manualcharge.Entry{}}
	h := newHandlerForCharges(writer, reader)
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	form := url.Values{
		"csrf_token":         {"tok"},
		"charged_on":         {"2026-07-15"},
		"energy_added_kwh":   {"7.345"},
		"price":              {"5000"},
		"location_kind":      {"HOME"},
		"start_battery_pct":  {"50"},
		"end_battery_pct":    {"80"},
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
	if writer.createEntry.EnergyAddedKWh != 7.345 {
		t.Errorf("want EnergyAddedKWh=7.345 persisted (no server-side rounding — D7), got %v",
			writer.createEntry.EnergyAddedKWh)
	}
}

// TestChargeCreate_NonPositiveEnergy_Rejected verifies the D7 parity contract:
// the energy > 0 check is unchanged. energy=0 and energy=-1 are still rejected.
func TestChargeCreate_NonPositiveEnergy_Rejected(t *testing.T) {
	uid := uuid.New()
	for _, v := range []string{"0", "-1"} {
		writer := &fakeChargeWriter{}
		reader := &fakeChargeReader{entries: []manualcharge.Entry{}}
		h := newHandlerForCharges(writer, reader)
		r := engineWithSession(h, uid, "tok")
		c := sessionCookie(r, uid, "tok")

		form := url.Values{
			"csrf_token":         {"tok"},
			"charged_on":         {"2026-07-15"},
			"energy_added_kwh":   {v},
			"price":              {"5000"},
			"location_kind":      {"HOME"},
			"start_battery_pct":  {"50"},
			"end_battery_pct":    {"80"},
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
		if writer.createEntry.EnergyAddedKWh != 0 {
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
func TestChargeRowDelete_ThenListReflectsRemoval(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	// Pre-delete list carries the entry; post-delete list does not. We exercise
	// both reads against the same fakeReader (the slice is a fixture per-call,
	// so we re-set entries between the two GETs).
	reader := &fakeChargeReader{entries: []manualcharge.Entry{
		{ID: id, AccountID: uid, TeslaID: 1001, VIN: "VIN1001", ChargedOn: time.Now(),
			EnergyAddedKWh: 10.0, Price: 5000.0, Currency: "COP"},
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
