package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
)

// This file covers the two manual-charge-page behaviours added on 2026-08-29:
//
//  1. the create-success NOTICE (ChargesPageData.Notice) the user sees after a
//     record is registered, and
//  2. the "one IN_PROGRESS entry per (vehicle, charged_on)" gateway validation,
//     enforced on BOTH the create POST and the inline-row PUT.
//
// engineWithSession never wires handlers.LanguageMiddleware, so i18n.FromContext
// falls back to Spanish — every assertion below matches the ES catalogue value,
// following the precedent in charges_error_visibility_test.go.

// conflictMsgES is the rendered ES form of i18n.KeyChargesErrorInProgressExists
// for 2026-07-15, spelled out literally so a catalogue edit that changes the
// user-visible sentence has to be a deliberate, visible test change.
const conflictMsgES = "Ya existe una carga en progreso para el 2026-07-15."

// noticeMsgES is the rendered ES form of i18n.KeyChargesNoticeEntryCreated.
const noticeMsgES = "Registro agregado correctamente."

// day parses a "2006-01-02" test date, failing the test on a malformed literal.
func day(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("bad test date %q: %v", s, err)
	}
	return d
}

// chargeForm is the standard valid create/update submission, with status and
// charged_on left to the caller so each test varies only what it is about.
func chargeForm(status, chargedOn string) url.Values {
	return url.Values{
		"csrf_token":        {"tok"},
		"status":            {status},
		"charged_on":        {chargedOn},
		"energy_added_kwh":  {"10.5"},
		"price":             {"5000"},
		"location_kind":     {"HOME"},
		"start_battery_pct": {"50"},
		"end_battery_pct":   {"80"},
		"ended_at":          {chargedOn + "T20:00"},
	}
}

// submitCharge issues form as an authenticated request to method+path.
func submitCharge(h *Handler, uid uuid.UUID, method, path string, form url.Values) *httptest.ResponseRecorder {
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

// TestChargeCreate_SuccessRendersNotice pins requirement 1: after a record is
// registered the user must be TOLD so. The notice rides in on the create
// response's primary #charges-create-form swap, rendered as a success alert.
func TestChargeCreate_SuccessRendersNotice(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	h := newHandlerForCharges(writer, &fakeChargeReader{entries: []charging.Entry{}})

	w := submitCharge(h, uid, http.MethodPost, "/ui/charges/create", chargeForm("IN_PROGRESS", "2026-07-15"))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 on valid create, got %d body=%q", w.Code, w.Body.String()[:min(500, w.Body.Len())])
	}
	body := w.Body.String()
	if !strings.Contains(body, noticeMsgES) {
		t.Errorf("create success must render the saved notice %q; body:\n%s", noticeMsgES, body[:min(1200, len(body))])
	}
	if !strings.Contains(body, "alert-success") {
		t.Error("the notice must render as a SUCCESS alert (alert-success), not an error one")
	}
}

// TestChargesListFragment_RendersNoNotice guards the notice's one-shot nature:
// it belongs to the response for the write that earned it and must not reappear
// on an ordinary list refresh.
func TestChargesListFragment_RendersNoNotice(t *testing.T) {
	uid := uuid.New()
	h := newHandlerForCharges(&fakeChargeWriter{}, &fakeChargeReader{entries: []charging.Entry{}})
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/charges/list", nil)
	req.AddCookie(c)
	r.ServeHTTP(w, req)

	if strings.Contains(w.Body.String(), noticeMsgES) {
		t.Error("the create notice must not survive into a plain list refresh")
	}
}

// TestChargeCreate_RejectsSecondInProgressOnSameDate pins requirement 2 on the
// create path: a second IN_PROGRESS entry for a day that already has one is
// rejected as a 422 naming that date, and never reaches charging.Writer.
func TestChargeCreate_RejectsSecondInProgressOnSameDate(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	reader := &fakeChargeReader{entries: []charging.Entry{{
		ID:        uuid.New(),
		AccountID: uid,
		TeslaID:   1001,
		VIN:       "VIN1001",
		ChargedOn: day(t, "2026-07-15"),
		Status:    charging.StatusInProgress,
		Currency:  "COP",
	}}}
	h := newHandlerForCharges(writer, reader)

	w := submitCharge(h, uid, http.MethodPost, "/ui/charges/create", chargeForm("IN_PROGRESS", "2026-07-15"))

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 on a duplicate in-progress charge, got %d", w.Code)
	}
	if writer.createCalls != 0 {
		t.Errorf("rejected create must never reach charging.Writer; createCalls=%d", writer.createCalls)
	}
	if !strings.Contains(w.Body.String(), conflictMsgES) {
		t.Errorf("422 body must name the conflicting date; want %q, got:\n%s",
			conflictMsgES, w.Body.String()[:min(1200, w.Body.Len())])
	}
	if got := w.Header().Get("HX-Error-Fragment"); got != "true" {
		t.Errorf("HX-Error-Fragment = %q, want \"true\" — htmx discards a 422 body without it and the user sees nothing", got)
	}
}

// TestChargeCreate_AllowsInProgressOnADifferentDate pins the rule's SCOPE: it is
// per (vehicle, charged_on), not one in-progress charge per vehicle overall.
func TestChargeCreate_AllowsInProgressOnADifferentDate(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	// The fake reader ignores the requested window and always returns its
	// entries, so an entry dated 2026-07-20 still comes back for the
	// 2026-07-15 lookup — which makes this test the sharper one: only the
	// handler's own same-day scoping can let this write through.
	reader := &fakeChargeReader{entries: []charging.Entry{{
		ID:        uuid.New(),
		AccountID: uid,
		TeslaID:   1001,
		VIN:       "VIN1001",
		ChargedOn: day(t, "2026-07-20"),
		Status:    charging.StatusInProgress,
		Currency:  "COP",
	}}}
	h := newHandlerForCharges(writer, reader)

	w := submitCharge(h, uid, http.MethodPost, "/ui/charges/create", chargeForm("IN_PROGRESS", "2026-07-15"))

	if w.Code != http.StatusOK {
		t.Fatalf("an in-progress charge on a different date must be allowed; got %d body=%q",
			w.Code, w.Body.String()[:min(500, w.Body.Len())])
	}
	if writer.createCalls != 1 {
		t.Errorf("want the write to reach charging.Writer once, createCalls=%d", writer.createCalls)
	}
}

// TestChargeCreate_AllowsDoneOnADateWithAnInProgress pins that only IN_PROGRESS
// submissions are constrained — any number of DONE entries may share a date.
func TestChargeCreate_AllowsDoneOnADateWithAnInProgress(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	reader := &fakeChargeReader{entries: []charging.Entry{{
		ID:        uuid.New(),
		AccountID: uid,
		TeslaID:   1001,
		VIN:       "VIN1001",
		ChargedOn: day(t, "2026-07-15"),
		Status:    charging.StatusInProgress,
		Currency:  "COP",
	}}}
	h := newHandlerForCharges(writer, reader)

	w := submitCharge(h, uid, http.MethodPost, "/ui/charges/create", chargeForm("DONE", "2026-07-15"))

	if w.Code != http.StatusOK {
		t.Fatalf("a DONE charge must not be blocked by an existing in-progress one; got %d body=%q",
			w.Code, w.Body.String()[:min(500, w.Body.Len())])
	}
	if writer.createCalls != 1 {
		t.Errorf("want the write to reach charging.Writer once, createCalls=%d", writer.createCalls)
	}
}

// TestChargeCreate_AllowsInProgressWhenSameDayEntryIsDone is the mirror of the
// test above: an existing DONE entry on the same day constrains nothing.
func TestChargeCreate_AllowsInProgressWhenSameDayEntryIsDone(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	reader := &fakeChargeReader{entries: []charging.Entry{{
		ID:        uuid.New(),
		AccountID: uid,
		TeslaID:   1001,
		VIN:       "VIN1001",
		ChargedOn: day(t, "2026-07-15"),
		Status:    charging.StatusDone,
		Currency:  "COP",
	}}}
	h := newHandlerForCharges(writer, reader)

	w := submitCharge(h, uid, http.MethodPost, "/ui/charges/create", chargeForm("IN_PROGRESS", "2026-07-15"))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 when the same-day entry is DONE, got %d body=%q",
			w.Code, w.Body.String()[:min(500, w.Body.Len())])
	}
	if writer.createCalls != 1 {
		t.Errorf("want the write to reach charging.Writer once, createCalls=%d", writer.createCalls)
	}
}

// TestChargeCreate_ConflictCheckFailsOpenOnReaderError pins the deliberate
// fail-open posture: the rule has no DB constraint behind it, so a transient
// read failure must not cost the user the record they just typed.
func TestChargeCreate_ConflictCheckFailsOpenOnReaderError(t *testing.T) {
	uid := uuid.New()
	writer := &fakeChargeWriter{}
	h := newHandlerForCharges(writer, &fakeChargeReader{err: &fakeError{msg: "boom"}})

	w := submitCharge(h, uid, http.MethodPost, "/ui/charges/create", chargeForm("IN_PROGRESS", "2026-07-15"))

	if w.Code != http.StatusOK {
		t.Fatalf("a reader error must not block the write; got %d body=%q",
			w.Code, w.Body.String()[:min(500, w.Body.Len())])
	}
	if writer.createCalls != 1 {
		t.Errorf("want the write to reach charging.Writer once, createCalls=%d", writer.createCalls)
	}
}

// TestChargeRowUpdate_RejectsInProgressWhenAnotherExistsSameDate closes the
// edit-path bypass: flipping a row to IN_PROGRESS on a day that already has a
// DIFFERENT in-progress entry is rejected the same way a create is.
func TestChargeRowUpdate_RejectsInProgressWhenAnotherExistsSameDate(t *testing.T) {
	uid := uuid.New()
	editedID := uuid.New()
	writer := &fakeChargeWriter{}
	reader := &fakeChargeReader{entries: []charging.Entry{{
		ID:        uuid.New(), // a DIFFERENT row already holds the day
		AccountID: uid,
		TeslaID:   1001,
		VIN:       "VIN1001",
		ChargedOn: day(t, "2026-07-15"),
		Status:    charging.StatusInProgress,
		Currency:  "COP",
	}}}
	h := newHandlerForCharges(writer, reader)

	w := submitCharge(h, uid, http.MethodPut, "/ui/charges/row/"+editedID.String(), chargeForm("IN_PROGRESS", "2026-07-15"))

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 when another row already holds the day's in-progress charge, got %d", w.Code)
	}
	if writer.updateCalls != 0 {
		t.Errorf("rejected update must never reach charging.Writer; updateCalls=%d", writer.updateCalls)
	}
	if !strings.Contains(w.Body.String(), conflictMsgES) {
		t.Errorf("422 body must name the conflicting date; want %q, got:\n%s",
			conflictMsgES, w.Body.String()[:min(1200, w.Body.Len())])
	}
}

// TestChargeRowUpdate_InProgressRowDoesNotConflictWithItself is the exclusion
// this rule lives or dies on: re-saving an entry that is ALREADY the day's
// in-progress charge must not be blocked by its own persisted row.
func TestChargeRowUpdate_InProgressRowDoesNotConflictWithItself(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	writer := &fakeChargeWriter{}
	reader := &fakeChargeReader{entries: []charging.Entry{{
		ID:        id, // the very row being edited
		AccountID: uid,
		TeslaID:   1001,
		VIN:       "VIN1001",
		ChargedOn: day(t, "2026-07-15"),
		Status:    charging.StatusInProgress,
		Currency:  "COP",
	}}}
	h := newHandlerForCharges(writer, reader)

	w := submitCharge(h, uid, http.MethodPut, "/ui/charges/row/"+id.String(), chargeForm("IN_PROGRESS", "2026-07-15"))

	if w.Code != http.StatusOK {
		t.Fatalf("an in-progress row must not conflict with itself; got %d body=%q",
			w.Code, w.Body.String()[:min(500, w.Body.Len())])
	}
	if writer.updateCalls != 1 {
		t.Errorf("want the write to reach charging.Writer once, updateCalls=%d", writer.updateCalls)
	}
}
