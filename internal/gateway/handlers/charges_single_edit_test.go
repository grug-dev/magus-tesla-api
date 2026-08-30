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
	req := httptest.NewRequest(http.MethodGet, "/ui/charges/row/"+id.String()+"/edit", nil)
	if c != nil {
		req.AddCookie(c)
	}
	r.ServeHTTP(w, req)
	return w
}

// TestChargeRowEditFragment_OnlyOneRowEditableAtATime pins the invariant: opening
// an editor renders the WHOLE list with exactly ONE row in edit mode. Previously
// each Edit swapped its own <tr>, so N clicks left N competing forms on screen.
// Counting hx-put occurrences is the direct expression of "one open editor" —
// only ChargeRowEdit emits hx-put.
func TestChargeRowEditFragment_OnlyOneRowEditableAtATime(t *testing.T) {
	uid, idA, idB := uuid.New(), uuid.New(), uuid.New()
	h := newHandlerForCharges(&fakeChargeWriter{}, &fakeChargeReader{entries: twoEntries(uid, idA, idB)})

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
	if !strings.Contains(body, "/ui/charges/row/"+idA.String()) {
		t.Errorf("want row A rendered as the open editor; body=%q", body[:min(1200, len(body))])
	}
	if !strings.Contains(body, "/ui/charges/row/"+idB.String()+"/edit") {
		t.Errorf("want row B still rendered static with its Edit action; body=%q", body[:min(1200, len(body))])
	}
}

// TestChargeRowEditFragment_RendersWholeListRegion pins the mechanism the
// invariant rests on: the response is the #charges-list region, so htmx replaces
// every row at once and a previously-open editor cannot survive the swap.
func TestChargeRowEditFragment_RendersWholeListRegion(t *testing.T) {
	uid, idA, idB := uuid.New(), uuid.New(), uuid.New()
	h := newHandlerForCharges(&fakeChargeWriter{}, &fakeChargeReader{entries: twoEntries(uid, idA, idB)})

	body := getEditFragment(h, uid, idA).Body.String()
	if !strings.Contains(body, `id="charges-list"`) {
		t.Errorf("edit response must be the whole #charges-list region, not a bare row; body=%q", body[:min(800, len(body))])
	}
}

// TestChargeRow_EditButtonTargetsTheWholeList is the markup half of the same
// invariant: if the Edit button ever goes back to targeting its own row, the
// server-side single-editor rule stops being reachable, because each row would
// again swap independently.
func TestChargeRow_EditButtonTargetsTheWholeList(t *testing.T) {
	uid, idA, idB := uuid.New(), uuid.New(), uuid.New()
	h := newHandlerForCharges(&fakeChargeWriter{}, &fakeChargeReader{entries: twoEntries(uid, idA, idB)})
	r := engineWithSession(h, uid, "tok")
	c := sessionCookie(r, uid, "tok")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ui/charges/list", nil)
	req.AddCookie(c)
	r.ServeHTTP(w, req)
	body := w.Body.String()

	editIdx := strings.Index(body, "/edit?start=")
	if editIdx == -1 {
		t.Fatalf("no Edit action rendered in the list; body=%q", body[:min(800, len(body))])
	}
	// Inspect the attributes around the Edit button's hx-get.
	seg := body[max0(editIdx-300):min(editIdx+300, len(body))]
	if !strings.Contains(seg, `hx-target="#charges-list"`) {
		t.Errorf("Edit must target #charges-list so only one editor can be open; surrounding markup was:\n%s", seg)
	}
}

// TestChargeRowEditFragment_UnknownEntryIs404 keeps the pre-existing contract:
// an id that is not in the caller's rendered window is not editable.
func TestChargeRowEditFragment_UnknownEntryIs404(t *testing.T) {
	uid, idA, idB := uuid.New(), uuid.New(), uuid.New()
	h := newHandlerForCharges(&fakeChargeWriter{}, &fakeChargeReader{entries: twoEntries(uid, idA, idB)})

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
