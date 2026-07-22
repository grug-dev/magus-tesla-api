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
)

// --- fakes for manualcharge.Writer and manualcharge.Reader ---

type fakeChargeWriter struct {
	createEntry manualcharge.Entry
	createErr   error
	updateEntry manualcharge.Entry
	updateErr   error
	deleteErr   error
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
		"vehicle":          {"1001:VIN1001"},
		"charged_on":       {"2026-07-15"},
		"energy_added_kwh": {"10.5"},
		"price":            {"5000"},
		"currency":         {"COP"},
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

// TestChargeCreate_UnownedVehicle verifies 403 when the vehicle is not owned by the user.
func TestChargeCreate_UnownedVehicle(t *testing.T) {
	uid := uuid.New()
	h := newHandlerForCharges(&fakeChargeWriter{}, &fakeChargeReader{})
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	form := url.Values{
		"csrf_token":       {"tok"},
		"vehicle":          {"9999:STRANGER_VIN"},
		"charged_on":       {"2026-07-15"},
		"energy_added_kwh": {"10.5"},
		"price":            {"5000"},
		"currency":         {"COP"},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/charges/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 on unowned vehicle, got %d", w.Code)
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
		"vehicle":    {"1001:VIN1001"},
		"price":      {"5000"},
		"currency":   {"COP"},
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
func TestChargeCreate_ValidInput(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	reader := &fakeChargeReader{entries: []manualcharge.Entry{}}
	h := newHandlerForCharges(writer, reader)
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	form := url.Values{
		"csrf_token":       {"tok"},
		"vehicle":          {"1001:VIN1001"},
		"charged_on":       {"2026-07-15"},
		"energy_added_kwh": {"10.5"},
		"price":            {"5000"},
		"currency":         {"COP"},
		"location_kind":    {"HOME"},
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
		"csrf_token":       {"badtoken"},
		"vehicle":          {"1001:VIN1001"},
		"charged_on":       {"2026-07-15"},
		"energy_added_kwh": {"10.5"},
		"price":            {"5000"},
		"currency":         {"COP"},
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
		"vehicle":    {"1001:VIN1001"},
		"price":      {"5000"},
		"currency":   {"COP"},
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
		"csrf_token":       {"tok"},
		"vehicle":          {"1001:VIN1001"},
		"charged_on":       {"2026-07-16"},
		"energy_added_kwh": {"20.0"},
		"price":            {"9000"},
		"currency":         {"COP"},
		"location_kind":    {"WORK"},
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
}

// TestChargeRowDelete_CSRFMismatch verifies 403 on CSRF mismatch for delete.
func TestChargeRowDelete_CSRFMismatch(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	h := newHandlerForCharges(&fakeChargeWriter{}, &fakeChargeReader{})
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

// TestChargeRowDelete_ValidInput verifies a valid DELETE returns an empty row.
// The CSRF token is passed as the X-CSRF-Token header since some HTTP clients
// (and Gin) may not parse form bodies on DELETE requests.
func TestChargeRowDelete_ValidInput(t *testing.T) {
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
	if !strings.Contains(body, "charge-row-"+id.String()) {
		t.Errorf("want empty row with id in delete response, body=%q", body[:min(500, len(body))])
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
		"vehicle":          {"1001:VIN1001"},
		"charged_on":       {"2026-07-15"},
		"energy_added_kwh": {"10.5"},
		"price":            {"5000"},
		"currency":         {"COP"},
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
		"vehicle":          {"1001:VIN1001"},
		"charged_on":       {"2026-07-16"},
		"energy_added_kwh": {"20.0"},
		"price":            {"9000"},
		"currency":         {"COP"},
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

// TestParseVehicleValue verifies the combined "{teslaID}:{vin}" parser.
func TestParseVehicleValue(t *testing.T) {
	cases := []struct {
		input   string
		wantID  int64
		wantVIN string
		wantErr bool
	}{
		{"1001:VIN1001", 1001, "VIN1001", false},
		{"42:5YJSA1E67MF123456", 42, "5YJSA1E67MF123456", false},
		{"", 0, "", true},
		{"abc:VIN", 0, "", true},
		{"1001:", 0, "", true},
		{"999", 0, "", true},
	}
	for _, tc := range cases {
		id, vin, err := parseVehicleValue(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseVehicleValue(%q): want error, got id=%d vin=%q", tc.input, id, vin)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseVehicleValue(%q): unexpected error: %v", tc.input, err)
			continue
		}
		if id != tc.wantID || vin != tc.wantVIN {
			t.Errorf("parseVehicleValue(%q): want id=%d vin=%q, got id=%d vin=%q",
				tc.input, tc.wantID, tc.wantVIN, id, vin)
		}
	}
}

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

// TestBuildVehicleOptions verifies vehicle picker option generation (legacy two-vehicle case).
func TestBuildVehicleOptions(t *testing.T) {
	owner := "OWNER"
	vehicles := []account.Vehicle{
		{TeslaID: 1001, VIN: "VIN1001", DisplayName: "Magus", AccessType: &owner},
		{TeslaID: 2002, VIN: "VIN2002", DisplayName: "Other"},
	}
	opts, single := buildVehicleOptions(vehicles)
	if len(opts) != 2 {
		t.Fatalf("want 2 options, got %d", len(opts))
	}
	if single {
		t.Errorf("want SingleVehicle false for 2 vehicles, got true")
	}
	// First vehicle is OWNER — should be selected.
	if !opts[0].Selected {
		t.Errorf("want first (OWNER) option Selected=true, got false")
	}
	if opts[1].Selected {
		t.Errorf("want second option Selected=false, got true")
	}
	if opts[0].Value != "1001:VIN1001" {
		t.Errorf("want value 1001:VIN1001, got %q", opts[0].Value)
	}
	if opts[1].Value != "2002:VIN2002" {
		t.Errorf("want value 2002:VIN2002, got %q", opts[1].Value)
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
		"csrf_token":       {"tok"},
		"vehicle":          {"1001:VIN1001"},
		"charged_on":       {"2026-07-15"},
		"energy_added_kwh": {"10.5"},
		"price":            {"5000"},
		"currency":         {"COP"},
		// deliberately no location_kind
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
		"csrf_token":       {"tok"},
		"vehicle":          {"1001:VIN1001"},
		"charged_on":       {"2026-07-15"},
		"energy_added_kwh": {"10.5"},
		"price":            {"5000"},
		"currency":         {"COP"},
		"location_kind":    {"INVALID"},
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
		"csrf_token":       {"tok"},
		"vehicle":          {"1001:VIN1001"},
		"charged_on":       {"2026-07-16"},
		"energy_added_kwh": {"20.0"},
		"price":            {"9000"},
		"currency":         {"COP"},
		// deliberately no location_kind
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
		"csrf_token":       {"tok"},
		"vehicle":          {"1001:VIN1001"},
		"charged_on":       {"2026-07-15"},
		"energy_added_kwh": {"10.5"},
		"price":            {"5000"},
		"currency":         {"COP"},
		"location_kind":    {"HOME"},
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

// --- Sub-task D: vehicle auto-select unit tests ---

// ptrStr is a helper to take the address of a string literal in tests.
func ptrStr(s string) *string { return &s }

// TestBuildVehicleOptions_SingleVehicle: one vehicle → Selected=true, SingleVehicle=true.
func TestBuildVehicleOptions_SingleVehicle(t *testing.T) {
	vehicles := []account.Vehicle{
		{TeslaID: 1001, VIN: "VIN1001", DisplayName: "Magus"},
	}
	opts, single := buildVehicleOptions(vehicles)
	if !single {
		t.Errorf("want SingleVehicle=true for 1 vehicle, got false")
	}
	if len(opts) != 1 {
		t.Fatalf("want 1 option, got %d", len(opts))
	}
	if !opts[0].Selected {
		t.Errorf("want sole vehicle Selected=true, got false")
	}
}

// TestBuildVehicleOptions_MultiVehicleOwnerFirst: second vehicle is OWNER → second Selected.
func TestBuildVehicleOptions_MultiVehicleOwnerFirst(t *testing.T) {
	vehicles := []account.Vehicle{
		{TeslaID: 1001, VIN: "VIN1001", DisplayName: "Driver", AccessType: ptrStr("DRIVER")},
		{TeslaID: 2002, VIN: "VIN2002", DisplayName: "Owner", AccessType: ptrStr("OWNER")},
	}
	opts, single := buildVehicleOptions(vehicles)
	if single {
		t.Errorf("want SingleVehicle=false for 2 vehicles, got true")
	}
	if len(opts) != 2 {
		t.Fatalf("want 2 options, got %d", len(opts))
	}
	if opts[0].Selected {
		t.Errorf("want first (DRIVER) option Selected=false, got true")
	}
	if !opts[1].Selected {
		t.Errorf("want second (OWNER) option Selected=true, got false")
	}
}

// TestBuildVehicleOptions_MultiVehicleNoOwner: no OWNER → first vehicle Selected.
func TestBuildVehicleOptions_MultiVehicleNoOwner(t *testing.T) {
	vehicles := []account.Vehicle{
		{TeslaID: 1001, VIN: "VIN1001", DisplayName: "First"},
		{TeslaID: 2002, VIN: "VIN2002", DisplayName: "Second"},
	}
	opts, single := buildVehicleOptions(vehicles)
	if single {
		t.Errorf("want SingleVehicle=false for 2 vehicles, got true")
	}
	if !opts[0].Selected {
		t.Errorf("want first option Selected=true when no OWNER, got false")
	}
	if opts[1].Selected {
		t.Errorf("want second option Selected=false when no OWNER, got true")
	}
}

// TestBuildVehicleOptions_MultiVehicleFirstOwner: first vehicle is OWNER → first Selected.
func TestBuildVehicleOptions_MultiVehicleFirstOwner(t *testing.T) {
	vehicles := []account.Vehicle{
		{TeslaID: 1001, VIN: "VIN1001", DisplayName: "First", AccessType: ptrStr("OWNER")},
		{TeslaID: 2002, VIN: "VIN2002", DisplayName: "Second"},
	}
	opts, single := buildVehicleOptions(vehicles)
	if single {
		t.Errorf("want SingleVehicle=false for 2 vehicles, got true")
	}
	if !opts[0].Selected {
		t.Errorf("want first (OWNER) option Selected=true, got false")
	}
	if opts[1].Selected {
		t.Errorf("want second option Selected=false, got true")
	}
}

// TestBuildVehicleOptions_NilAccessType: nil AccessType treated as non-OWNER;
// when sole vehicle, still Selected=true with SingleVehicle=true.
func TestBuildVehicleOptions_NilAccessType(t *testing.T) {
	vehicles := []account.Vehicle{
		{TeslaID: 1001, VIN: "VIN1001", DisplayName: "Magus", AccessType: nil},
	}
	opts, single := buildVehicleOptions(vehicles)
	if !single {
		t.Errorf("want SingleVehicle=true for 1 vehicle with nil AccessType, got false")
	}
	if !opts[0].Selected {
		t.Errorf("want sole vehicle Selected=true even with nil AccessType, got false")
	}

	// Multi-vehicle with nil AccessType on all → first selected.
	vehicles2 := []account.Vehicle{
		{TeslaID: 1001, VIN: "VIN1001", DisplayName: "A", AccessType: nil},
		{TeslaID: 2002, VIN: "VIN2002", DisplayName: "B", AccessType: nil},
	}
	opts2, single2 := buildVehicleOptions(vehicles2)
	if single2 {
		t.Errorf("want SingleVehicle=false for 2 vehicles, got true")
	}
	if !opts2[0].Selected {
		t.Errorf("want first option Selected=true when all AccessType=nil, got false")
	}
	if opts2[1].Selected {
		t.Errorf("want second option Selected=false when all AccessType=nil, got true")
	}
}
