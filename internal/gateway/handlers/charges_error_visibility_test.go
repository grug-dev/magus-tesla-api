package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/manualcharge"
)

// The guards below cover bugs whose whole nature is that they are quiet — a wrong
// or missing default looks plausible, and a dropped error fragment looks like
// nothing happening at all:
//
//  1. htmx never swaps a 4xx/5xx body, so a handler's carefully re-rendered
//     validation fragment is dropped unless the response opts in via
//     HX-Error-Fragment (see renderError + internal/gateway/static/app.js).
//  2. htmx only runs HTML5 validation when the request is issued BY the form, so
//     moving hx-put onto a button silently disables every `required` in the form.
//  3. A date default that is missing, or computed in UTC rather than the browser's
//     timezone, renders a plausible-looking but wrong day.
//  4. If the shared confirm-dialog wiring drifts, app.js falls back to the native
//     window.confirm() — the action still works, so only the look regresses.

// TestErrorFragmentsCarryOptInHeader asserts that a validation failure returns 422
// with the HX-Error-Fragment header. Without the header htmx discards the body and
// the user sees nothing happen at all — the original MAG-5 edit-row report.
func TestErrorFragmentsCarryOptInHeader(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	h := newHandlerForCharges(&fakeChargeWriter{}, &fakeChargeReader{})
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	// A form with start_battery_pct cleared — exactly the shape that produced the
	// silent 422 on PUT /ui/charges/row/:id.
	form := url.Values{
		"csrf_token":        {"tok"},
		"charged_on":        {"2026-08-03"},
		"energy_added_kwh":  {"0.79"},
		"price":             {"2610.00"},
		"location_kind":     {"HOME"},
		"start_battery_pct": {""},
		"end_battery_pct":   {"100"},
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/ui/charges/row/"+id.String(), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(c)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 on empty start_battery_pct, got %d", w.Code)
	}
	if got := w.Header().Get("HX-Error-Fragment"); got != "true" {
		t.Errorf("HX-Error-Fragment = %q, want \"true\" — htmx will DISCARD this 422 body and the user will see nothing", got)
	}
	// engineWithSession never wires handlers.LanguageMiddleware, so i18n.FromContext
	// falls back to Spanish (KeyChargesErrorBatteryPctRequired's ES value) — assert
	// the resolved-language string, not the pre-existing English literal
	// (RM24-gateway-translate-all-pages, mirroring tier 2's T6.4 precedent).
	if !strings.Contains(w.Body.String(), "El porcentaje de batería es obligatorio.") {
		t.Errorf("422 body must carry the field-level message; got:\n%s", w.Body.String())
	}
}

// TestEditRowIssuesRequestFromForm asserts the edit fragment keeps hx-put on the
// <form> with a submit button, not on a plain button. htmx gates HTML5 validation
// on `elt instanceof HTMLFormElement || hx-validate="true"`, so hx-put on a
// type="button" makes every Required prop in the form inert.
func TestEditRowIssuesRequestFromForm(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	entry := manualcharge.Entry{ID: id, AccountID: uid, TeslaID: 1001, VIN: "VIN1001", Currency: "COP"}
	h := newHandlerForCharges(&fakeChargeWriter{}, &fakeChargeReader{entries: []manualcharge.Entry{entry}})
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/charges/row/"+id.String()+"/edit", nil)
	req.AddCookie(c)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("edit fragment: want 200, got %d", w.Code)
	}
	html := w.Body.String()

	formTag := regexp.MustCompile(`<form[^>]*>`).FindString(html)
	if !strings.Contains(formTag, "hx-put") {
		t.Errorf("hx-put must live on the <form> or htmx skips HTML5 validation; form tag was:\n%s", formTag)
	}
	for _, btn := range regexp.MustCompile(`<button[^>]*>`).FindAllString(html, -1) {
		if strings.Contains(btn, "hx-put") {
			t.Errorf("hx-put must NOT be on a button — it disables form validation:\n%s", btn)
		}
	}
	if !regexp.MustCompile(`<button[^>]*type="submit"`).MatchString(html) {
		t.Errorf("Save must be type=submit so the browser validates before htmx issues the PUT")
	}
}

// TestCreateFormDateDefaults asserts the create form's "Date" (charged_on) renders
// pre-filled, agrees with started_at/ended_at, and follows the BROWSER's calendar
// day. charged_on shipped empty while its two siblings were pre-filled; and the
// defaults were computed from time.Now().UTC(), which for a UTC-5 user rolls to
// tomorrow after 19:00 local.
func TestCreateFormDateDefaults(t *testing.T) {
	uid := uuid.New()
	h := newHandlerForCharges(&fakeChargeWriter{}, &fakeChargeReader{})
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	bogota, err := time.LoadLocation("America/Bogota")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	wantDay := time.Now().In(bogota).Format("2006-01-02")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/charges", nil)
	req.AddCookie(c)
	req.AddCookie(&http.Cookie{Name: "browser_tz", Value: "America/Bogota"})
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("charges page: want 200, got %d", w.Code)
	}
	html := w.Body.String()

	valueOf := func(name string) string {
		m := regexp.MustCompile(`<input[^>]*name="` + name + `"[^>]*>`).FindString(html)
		if m == "" {
			t.Fatalf("no input named %q in the create form", name)
		}
		v := regexp.MustCompile(`value="([^"]*)"`).FindStringSubmatch(m)
		if v == nil {
			return ""
		}
		return v[1]
	}

	if got := valueOf("charged_on"); got != wantDay {
		t.Errorf("charged_on value = %q, want %q (the browser's current day)", got, wantDay)
	}
	// The three date fields must agree — they derive from one day value.
	if got := valueOf("started_at"); got != wantDay+"T00:00" {
		t.Errorf("started_at value = %q, want %q", got, wantDay+"T00:00")
	}
	if got := valueOf("ended_at"); got != wantDay+"T00:00" {
		t.Errorf("ended_at value = %q, want %q", got, wantDay+"T00:00")
	}
}

// TestConfirmDialogWiring asserts the three pieces of the shared confirmation modal
// stay connected: the single dialog instance in the layout, and the hx-confirm +
// data-confirm-* attributes on the delete control that drive it. If any drifts,
// app.js silently falls through to the browser's native window.confirm() — the
// delete still works, so nothing breaks loudly; the UI just regresses to the old look.
func TestConfirmDialogWiring(t *testing.T) {
	uid := uuid.New()
	id := uuid.New()
	entry := manualcharge.Entry{
		ID: id, AccountID: uid, TeslaID: 1001, VIN: "VIN1001", Currency: "COP",
		ChargedOn: time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC), EnergyAddedKWh: 0.79,
	}
	h := newHandlerForCharges(&fakeChargeWriter{}, &fakeChargeReader{entries: []manualcharge.Entry{entry}})
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/charges", nil)
	req.AddCookie(c)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("charges page: want 200, got %d", w.Code)
	}
	html := w.Body.String()

	// Exactly one dialog instance — app.js resolves it by id, so a duplicate (e.g.
	// someone mounting it per row or per page as well as in the layout) would make
	// the wrong one open.
	if n := strings.Count(html, `id="confirm-dialog"`); n != 1 {
		t.Errorf(`found %d elements with id="confirm-dialog", want exactly 1 (mounted once in layouts.Base)`, n)
	}
	for _, id := range []string{"confirm-dialog-title", "confirm-dialog-message", "confirm-dialog-cancel"} {
		if !strings.Contains(html, `id="`+id+`"`) {
			t.Errorf("dialog is missing #%s, which app.js populates", id)
		}
	}
	for _, sel := range []string{`data-confirm-ok="default"`, `data-confirm-ok="danger"`} {
		if !strings.Contains(html, sel) {
			t.Errorf("dialog is missing the [%s] confirm button", sel)
		}
	}

	// The delete control must carry the attributes app.js reads.
	delBtn := regexp.MustCompile(`<button[^>]*hx-delete[^>]*>`).FindString(html)
	if delBtn == "" {
		t.Fatal("no delete button rendered on the charge row")
	}
	for _, attr := range []string{"hx-confirm=", `data-confirm-variant="danger"`, "data-confirm-title=", "data-confirm-label="} {
		if !strings.Contains(delBtn, attr) {
			t.Errorf("delete button missing %s — it would fall back to native confirm();\ngot: %s", attr, delBtn)
		}
	}
	// The message should name the actual entry, not be generic.
	if !strings.Contains(delBtn, "Aug 3, 2026") {
		t.Errorf("hx-confirm should identify the entry being deleted; got: %s", delBtn)
	}
}
