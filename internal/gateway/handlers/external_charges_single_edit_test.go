package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
)

// twoEntries seeds two charge entries on the same vehicle so a test can open one
// for editing and assert the other stayed static.
func twoEntries(uid uuid.UUID, idA, idB uuid.UUID) []charging.Entry {
	base := charging.Entry{
		AccountID: uid,
		TeslaID:   1001,
		VIN:       "VIN1001",
		ChargedOn: time.Now(),
		Status:    charging.StatusDone,
		Price:     7000,
		Currency:  "COP",
	}
	a, b := base, base
	a.ID, b.ID = idA, idB
	return []charging.Entry{a, b}
}

// getEditFragment opens the inline editor for one row.
func getEditFragment(h *Handler, uid, id uuid.UUID) *httptest.ResponseRecorder {
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/external-charges/row/"+id.String()+"/edit", nil)
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)
	return w
}

// TestExternalChargeRowEditFragment_OnlyOneRowEditableAtATime pins the invariant: opening
// an editor renders the WHOLE list with exactly ONE row in edit mode. Previously
// each Edit swapped its own <tr>, so N clicks left N competing forms on screen.
// Counting hx-put occurrences is the direct expression of "one open editor" —
// only ExternalChargeRowEdit emits hx-put.
func TestExternalChargeRowEditFragment_OnlyOneRowEditableAtATime(t *testing.T) {
	uid, idA, idB := uuid.New(), uuid.New(), uuid.New()
	h := newHandlerForExternalCharges(&fakeChargeWriter{}, &fakeChargeReader{entries: twoEntries(uid, idA, idB)})

	w := getEditFragment(h, uid, idA)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200 opening the editor, got %d body=%q", w.Code, w.Body.String()[:min(500, w.Body.Len())])
	}
	body := w.Body.String()

	if n := strings.Count(body, "hx-put"); n != 1 {
		t.Errorf("want exactly ONE open edit form in the rendered list, found %d hx-put occurrences", n)
	}
	// The edited row must be the one asked for, and the sibling must still offer
	// its own Edit link — i.e. it rendered static, not as a second form.
	if !strings.Contains(body, "/ui/external-charges/row/"+idA.String()) {
		t.Errorf("want row A rendered as the open editor; body=%q", body[:min(1200, len(body))])
	}
	if !strings.Contains(body, "/ui/external-charges/row/"+idB.String()+"/edit") {
		t.Errorf("want row B still rendered static with its Edit action; body=%q", body[:min(1200, len(body))])
	}
}

// TestExternalChargeRowEditFragment_RendersWholeListRegion pins the mechanism the
// invariant rests on: the response is the #external-charges-list region, so htmx replaces
// every row at once and a previously-open editor cannot survive the swap.
func TestExternalChargeRowEditFragment_RendersWholeListRegion(t *testing.T) {
	uid, idA, idB := uuid.New(), uuid.New(), uuid.New()
	h := newHandlerForExternalCharges(&fakeChargeWriter{}, &fakeChargeReader{entries: twoEntries(uid, idA, idB)})

	body := getEditFragment(h, uid, idA).Body.String()
	if !strings.Contains(body, `id="external-charges-list"`) {
		t.Errorf("edit response must be the whole #external-charges-list region, not a bare row; body=%q", body[:min(800, len(body))])
	}
}

// TestExternalChargeRow_EditButtonTargetsTheWholeList is the markup half of the same
// invariant: if the Edit button ever goes back to targeting its own row, the
// server-side single-editor rule stops being reachable, because each row would
// again swap independently.
func TestExternalChargeRow_EditButtonTargetsTheWholeList(t *testing.T) {
	uid, idA, idB := uuid.New(), uuid.New(), uuid.New()
	h := newHandlerForExternalCharges(&fakeChargeWriter{}, &fakeChargeReader{entries: twoEntries(uid, idA, idB)})
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/external-charges/list", nil)
	req.AddCookie(c)
	r.ServeHTTP(w, req)
	body := w.Body.String()

	editIdx := strings.Index(body, "/edit?start=")
	if editIdx == -1 {
		t.Fatalf("no Edit action rendered in the list; body=%q", body[:min(800, len(body))])
	}
	// Inspect the attributes around the Edit button's hx-get.
	seg := body[max0(editIdx-300):min(editIdx+300, len(body))]
	if !strings.Contains(seg, `hx-target="#external-charges-list"`) {
		t.Errorf("Edit must target #external-charges-list so only one editor can be open; surrounding markup was:\n%s", seg)
	}
}

// TestExternalChargeRowEditFragment_UnknownEntryIs404 keeps the pre-existing contract:
// an id that is not in the caller's rendered window is not editable.
func TestExternalChargeRowEditFragment_UnknownEntryIs404(t *testing.T) {
	uid, idA, idB := uuid.New(), uuid.New(), uuid.New()
	h := newHandlerForExternalCharges(&fakeChargeWriter{}, &fakeChargeReader{entries: twoEntries(uid, idA, idB)})

	if w := getEditFragment(h, uid, uuid.New()); w.Code != http.StatusNotFound {
		t.Errorf("want 404 for an entry that is not in the list, got %d", w.Code)
	}
}

// max0 clamps a negative slice index to 0.
func max0(i int) int {
	if i < 0 {
		return 0
	}
	return i
}

// ============================================================================
// RM51-gateway-add-free-charge-and-month-preset (MAG-58, tier 2) — Test
// Contract Group B, cases B5-B7 (design.md §D-Echo). Added by Group 4 (task
// 4.3). These open the inline editor via the SAME getEditFragment helper the
// no-error-path tests above already use — a normal (non-error) row open, not
// a validation failure, so RawPriceConfirmed is derived from the persisted
// entry (externalChargeEntryVMFromEntry), not from a raw form echo.
// ============================================================================

// TestExternalChargeRowEditFragment_RM51_B5_ConfirmedFreeCharge_BoxChecked
// verifies Test Contract B5: an existing entry stored with Price==0,
// PriceSource==PriceSourceUser (the tier 1 "confirmed free charge" shape) ->
// opening its edit fragment renders price_confirmed with the checked
// attribute (design.md §D-Echo — the edit row shows the TRUE current state).
func TestExternalChargeRowEditFragment_RM51_B5_ConfirmedFreeCharge_BoxChecked(t *testing.T) {
	uid, id := uuid.New(), uuid.New()
	entry := charging.Entry{
		ID: id, AccountID: uid, TeslaID: 1001, VIN: "VIN1001",
		ChargedOn: time.Now(), Status: charging.StatusDone,
		Price: 0, PriceSource: charging.PriceSourceUser, Currency: "COP",
	}
	h := newHandlerForExternalCharges(&fakeChargeWriter{}, &fakeChargeReader{entries: []charging.Entry{entry}})

	w := getEditFragment(h, uid, id)
	if w.Code != http.StatusOK {
		t.Fatalf("B5: want 200 opening the editor, got %d body=%q", w.Code, w.Body.String()[:min(500, w.Body.Len())])
	}
	attrs := tagAttrsFor(w.Body.String(), "price_confirmed")
	if attrs == "" {
		t.Fatalf("B5: price_confirmed input not found, body=%q", w.Body.String()[:min(1500, w.Body.Len())])
	}
	if !strings.Contains(attrs, "checked") {
		t.Errorf("B5: want price_confirmed checked for a confirmed free charge (Price==0, PriceSource==USER), got %q", attrs)
	}
}

// TestExternalChargeRowEditFragment_RM51_B6_UnconfirmedZeroPrice_BoxUnchecked
// verifies Test Contract B6: an existing entry stored with Price==0,
// PriceSource==PriceSourceUnconfirmed -> opening its edit fragment renders
// price_confirmed with NO checked attribute (design.md §D-Echo — never
// fabricate a confirmation nobody gave).
func TestExternalChargeRowEditFragment_RM51_B6_UnconfirmedZeroPrice_BoxUnchecked(t *testing.T) {
	uid, id := uuid.New(), uuid.New()
	entry := charging.Entry{
		ID: id, AccountID: uid, TeslaID: 1001, VIN: "VIN1001",
		ChargedOn: time.Now(), Status: charging.StatusDone,
		Price: 0, PriceSource: charging.PriceSourceUnconfirmed, Currency: "COP",
	}
	h := newHandlerForExternalCharges(&fakeChargeWriter{}, &fakeChargeReader{entries: []charging.Entry{entry}})

	w := getEditFragment(h, uid, id)
	if w.Code != http.StatusOK {
		t.Fatalf("B6: want 200 opening the editor, got %d body=%q", w.Code, w.Body.String()[:min(500, w.Body.Len())])
	}
	attrs := tagAttrsFor(w.Body.String(), "price_confirmed")
	if attrs == "" {
		t.Fatalf("B6: price_confirmed input not found, body=%q", w.Body.String()[:min(1500, w.Body.Len())])
	}
	if strings.Contains(attrs, "checked") {
		t.Errorf("B6: want price_confirmed NOT checked for an unconfirmed zero price, got %q", attrs)
	}
}

// TestExternalChargeRowEditFragment_RM51_B7_PositivePrice_BoxUnchecked
// verifies Test Contract B7: an existing entry stored with Price==8000.00
// (any PriceSource) -> opening its edit fragment renders price_confirmed
// with NO checked attribute (design.md §D-Echo — a positive-price entry
// never shows a stale/misleading "confirmed free" state).
func TestExternalChargeRowEditFragment_RM51_B7_PositivePrice_BoxUnchecked(t *testing.T) {
	uid, id := uuid.New(), uuid.New()
	entry := charging.Entry{
		ID: id, AccountID: uid, TeslaID: 1001, VIN: "VIN1001",
		ChargedOn: time.Now(), Status: charging.StatusDone,
		Price: 8000.00, PriceSource: charging.PriceSourceUser, Currency: "COP",
	}
	h := newHandlerForExternalCharges(&fakeChargeWriter{}, &fakeChargeReader{entries: []charging.Entry{entry}})

	w := getEditFragment(h, uid, id)
	if w.Code != http.StatusOK {
		t.Fatalf("B7: want 200 opening the editor, got %d body=%q", w.Code, w.Body.String()[:min(500, w.Body.Len())])
	}
	attrs := tagAttrsFor(w.Body.String(), "price_confirmed")
	if attrs == "" {
		t.Fatalf("B7: price_confirmed input not found, body=%q", w.Body.String()[:min(1500, w.Body.Len())])
	}
	if strings.Contains(attrs, "checked") {
		t.Errorf("B7: want price_confirmed NOT checked for a positive-price entry, got %q", attrs)
	}
}
