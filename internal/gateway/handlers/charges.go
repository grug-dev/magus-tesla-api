// charges.go contains the handlers for the manual charge log page and its htmx
// fragment routes (create/edit/delete). All write paths are auth-guarded, CSRF-
// protected, and tenant-ownership-validated before calling charging.Writer.
// See design.md D1-D10 and AGENTS.md "Exception: user-initiated writes".
package handlers

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/charging"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/fragments"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/pages"
)

// csrfManualChargeKey is the session key for the manual charge CSRF token.
const csrfManualChargeKey = "csrf_manualcharge"

// csrfVehicleSelectKey is the session key for the vehicle-context-switcher CSRF
// token. Issued by NavHeaderFragment, validated by VehicleSelect — both live in
// handlers.go, but the key sits here with its sibling so the CSRF vocabulary has
// one home.
const csrfVehicleSelectKey = "csrf_vehicle_select"

// ChargePage renders the full Charge log page. It auth-guards, generates a CSRF
// token, builds page data, and renders the full page (initial load path).
//
// ChargePage does NOT parse ?start=&end= itself (design.md §D-Range/§Test
// Contract) — the date-filter contract is scoped to GET /ui/charges/list; a
// full page reload has no reason to remember a prior filter click. It always
// uses the default chargesRangeDefaultDays window (defaultChargesWindow).
func (h *Handler) ChargePage(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}

	csrfToken, err := generateCSRFToken()
	if err != nil {
		c.String(http.StatusInternalServerError, i18n.T(c.Request.Context(), i18n.KeyChargesErrorCouldNotStartChargeLog))
		return
	}
	sess := sessions.Default(c)
	sess.Set(csrfManualChargeKey, csrfToken)
	_ = sess.Save()

	// Scope the list + default the create-form picker to the selected vehicle
	// context (sidebar switcher). resolveSelectedVehicle auto-selects the first
	// OWNER vehicle when the session has none, so this page never lands without
	// a context. Falls back to 0 (all vehicles) only when the account has no
	// vehicles — but then there's nothing to log a charge against anyway.
	filterTeslaID := int64(0)
	if sel, ok := h.resolveSelectedVehicle(c.Request.Context(), c, uid); ok {
		filterTeslaID = sel.TeslaID
	}
	today := browserToday(c)
	start, end := defaultChargesWindow(today)
	d := h.buildChargesPage(c.Request.Context(), uid, csrfToken, filterTeslaID, today, start, end)
	render(c, http.StatusOK, pages.ChargePage(d))
}

// ChargesListFragment renders only the charges-list fragment (htmx refresh
// path AND the date-filter endpoint, design.md §D-RM33-10/§Context fact 4 —
// same route, new query contract). Parses ?start=&end= via parseChargesRange;
// on a malformed window it renders the SAME NoFilterChrome empty-state
// fragment at HTTP 400 with NO read against charging.Reader, mirroring the
// platform's own "on 400, render the empty-state placeholder... and return no
// preset selector" convention (internal/gateway/AGENTS.md §"HTTP date-filter
// convention").
func (h *Handler) ChargesListFragment(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	sess := sessions.Default(c)
	csrfToken, _ := sess.Get(csrfManualChargeKey).(string)

	today := browserToday(c)
	start, end, okRange := parseChargesRange(c, today)
	if !okRange {
		d := fragments.ChargesPageData{
			CSRFToken:      csrfToken,
			EmptyState:     true,
			NoFilterChrome: true,
		}
		renderFragmentError(c, http.StatusBadRequest, pages.ChargePage(d), "charges-list")
		return
	}

	// Scope the htmx-refreshed list to the selected vehicle context, same as the
	// full ChargePage render, so the list stays consistent with the switcher.
	filterTeslaID := int64(0)
	if sel, ok := h.resolveSelectedVehicle(c.Request.Context(), c, uid); ok {
		filterTeslaID = sel.TeslaID
	}
	d := h.buildChargesPage(c.Request.Context(), uid, csrfToken, filterTeslaID, today, start, end)
	renderFragment(c, http.StatusOK, pages.ChargePage(d), "charges-list")
}

// ChargesContentFragment renders the whole vehicle-scoped charges content region —
// the create form + the entry list — for the SELECTED vehicle (htmx swap served by
// GET /ui/charges). The #charges-content region subscribes to the "vehicle-changed"
// event the sidebar switcher fires (HX-Trigger on VehicleSelect) and re-fetches this
// so BOTH the list (filtered by the selected TeslaID) and the create form's vehicle
// default follow the newly-selected vehicle without a full page reload. It mirrors
// ChargePage exactly — issue a fresh manual-charge CSRF token (the re-rendered create
// form embeds it) and scope to the resolved vehicle — but emits only the two content
// fragments instead of the full page.
func (h *Handler) ChargesContentFragment(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	csrfToken, err := generateCSRFToken()
	if err != nil {
		c.String(http.StatusInternalServerError, i18n.T(c.Request.Context(), i18n.KeyChargesErrorCouldNotRefreshChargeLog))
		return
	}
	sess := sessions.Default(c)
	sess.Set(csrfManualChargeKey, csrfToken)
	_ = sess.Save()

	filterTeslaID := int64(0)
	if sel, ok := h.resolveSelectedVehicle(c.Request.Context(), c, uid); ok {
		filterTeslaID = sel.TeslaID
	}
	// Not itemized by a Wave 5 sub-task, but this call site must move to the
	// new 7-arg buildChargesPage signature regardless (5.1); a vehicle switch
	// has no filter state to preserve, so it uses the default window, same as
	// ChargePage.
	today := browserToday(c)
	start, end := defaultChargesWindow(today)
	d := h.buildChargesPage(c.Request.Context(), uid, csrfToken, filterTeslaID, today, start, end)
	renderFragment(c, http.StatusOK, pages.ChargePage(d), "charges-create-form", "charges-list")
}

// ChargeRowStatic renders the static row for one entry (used by cancel-edit
// path). Threads the caller's active filter window (?start=&end=, read via
// windowFromQuery — best-effort, never a gate) into fragments.ChargeRow's
// mandatory windowStartStr/windowEndStr params (design.md §D-Refresh, leader
// resolution L3) so a Cancel never silently resets the user's filter.
func (h *Handler) ChargeRowStatic(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.String(http.StatusBadRequest, i18n.T(c.Request.Context(), i18n.KeyChargesErrorInvalidID))
		return
	}
	sess := sessions.Default(c)
	csrfToken, _ := sess.Get(csrfManualChargeKey).(string)

	vm, ok2 := h.fetchEntryVM(c.Request.Context(), uid, id)
	if !ok2 {
		c.String(http.StatusNotFound, i18n.T(c.Request.Context(), i18n.KeyChargesErrorEntryNotFound))
		return
	}
	start, end := windowFromQuery(c, browserToday(c))
	windowStartStr := start.Format("2006-01-02")
	windowEndStr := end.Format("2006-01-02")
	render(c, http.StatusOK, fragments.ChargeRow(vm, csrfToken, windowStartStr, windowEndStr))
}

// ChargeRowEditFragment opens the inline edit form for ONE row. It renders the
// WHOLE #charges-list region with that row — and only that row — in edit mode,
// rather than returning a bare <tr> swapped into #charge-row-{id}.
//
// That is the fix for "N rows, N open edit forms": when the row was the swap
// target, each Edit click was independent, so a user could open every row at
// once and have several competing forms on screen. Making the LIST the unit of
// truth means opening a second row necessarily re-renders the first one closed —
// the invariant is enforced by the server on every render, not by client-side
// bookkeeping that a stray swap could desynchronize. It also needs no new JS:
// the row's Delete button already targets #charges-list this exact way, so this
// mirrors an existing mechanism in the same file instead of inventing one.
//
// Threads the active filter window (?start=&end=, windowFromQuery — best-effort,
// never a gate, design.md §D-Refresh/leader resolution L3) so opening an editor
// never silently resets the user's filter.
func (h *Handler) ChargeRowEditFragment(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.String(http.StatusBadRequest, i18n.T(c.Request.Context(), i18n.KeyChargesErrorInvalidID))
		return
	}
	sess := sessions.Default(c)
	csrfToken, _ := sess.Get(csrfManualChargeKey).(string)

	filterTeslaID := int64(0)
	if sel, ok := h.resolveSelectedVehicle(c.Request.Context(), c, uid); ok {
		filterTeslaID = sel.TeslaID
	}
	today := browserToday(c)
	start, end := windowFromQuery(c, today)
	d := h.buildChargesPage(c.Request.Context(), uid, csrfToken, filterTeslaID, today, start, end)

	// The row must be present in the window just rendered — Edit is only
	// reachable from a row the user can see. Checking the built page costs no
	// extra read (the old bare-row path spent one on fetchEntryVM) and keeps
	// the 404 contract for an id that is not the caller's or no longer exists.
	found := false
	for _, vm := range d.Entries {
		if vm.ID == id.String() {
			found = true
			break
		}
	}
	if !found {
		c.String(http.StatusNotFound, i18n.T(c.Request.Context(), i18n.KeyChargesErrorEntryNotFound))
		return
	}
	d.EditingID = id.String()
	renderFragment(c, http.StatusOK, pages.ChargePage(d), "charges-list")
}

// ChargeCreate handles POST /ui/charges/create. It auth-guards, CSRF-checks,
// validates ownership, calls Writer.Create, and on success returns an OOB swap
// to refresh the list plus a reset create form.
func (h *Handler) ChargeCreate(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	if !h.checkCSRF(c) {
		return
	}

	vehicles, err := h.acct.RegisteredVehicles(c.Request.Context(), uid)
	if err != nil {
		c.String(http.StatusInternalServerError, i18n.T(c.Request.Context(), i18n.KeyChargesErrorCouldNotValidateVehicleOwnership))
		return
	}
	sess := sessions.Default(c)
	csrfToken, _ := sess.Get(csrfManualChargeKey).(string)

	entry, raw, validationErrors, ok2 := h.parseChargeForm(c, uid, vehicles)
	// One IN_PROGRESS entry per (vehicle, charged_on): a second in-progress
	// charge for a day that already has one is rejected BEFORE Writer.Create,
	// and reported through the SAME 422 branch as every other validation
	// failure below, so the user's submitted values survive the re-render.
	// excludeID is uuid.Nil here — a create has no row of its own to exempt.
	if ok2 {
		if conflictDate, found := h.inProgressConflictOn(c.Request.Context(), uid, entry, uuid.Nil); found {
			validationErrors = map[string]string{
				"_top": fmt.Sprintf(i18n.T(c.Request.Context(), i18n.KeyChargesErrorInProgressExists), conflictDate),
			}
			ok2 = false
		}
	}
	if !ok2 {
		if validationErrors == nil {
			return
		}
		// Pre-select the vehicle the user just submitted (entry.TeslaID) so the
		// re-rendered create form keeps their pick; fall back to the session-
		// selected context, then to the auto-pick (0) when nothing parsed.
		filterTeslaID := entry.TeslaID
		if filterTeslaID == 0 {
			if sel, ok := h.resolveSelectedVehicle(c.Request.Context(), c, uid); ok {
				filterTeslaID = sel.TeslaID
			}
		}
		// design.md §Wave 5.4: a 422 never renders #charges-list, so it stays on
		// the plain default window — do NOT thread windowFromForm here.
		today := browserToday(c)
		start, end := defaultChargesWindow(today)
		d := h.buildChargesPage(c.Request.Context(), uid, csrfToken, filterTeslaID, today, start, end)
		// design.md §D-Values (roadmap D15): overwrite the fresh-load defaults
		// with what the user actually submitted, so a validation failure never
		// discards a value they typed — including charged_on/started_at/ended_at,
		// which have no home on ChargeFormValues (they already have a Default*
		// slot on ChargesPageData for the fresh-load case).
		d.DefaultChargedOn = c.PostForm("charged_on")
		d.DefaultStartedAt = c.PostForm("started_at")
		d.DefaultEndedAt = c.PostForm("ended_at")
		d.FormValues = raw
		// design.md §D-Fields: the Required* pair must track the SUBMITTED status,
		// not buildChargesPage's fresh-load IN_PROGRESS default.
		applyRawRequiredState(&d, raw)
		renderError(c, http.StatusUnprocessableEntity, fragments.ChargeCreateForm(d, validationErrors))
		return
	}

	created, err := h.chargingWriter.Create(c.Request.Context(), entry)
	if err != nil {
		log.Printf("gateway: ChargeCreate writer error for account %s: %v", uid, err)
		// Keep the picker on the vehicle the user submitted.
		filterTeslaID := entry.TeslaID
		if filterTeslaID == 0 {
			if sel, ok := h.resolveSelectedVehicle(c.Request.Context(), c, uid); ok {
				filterTeslaID = sel.TeslaID
			}
		}
		// design.md §Wave 5.4: same plain-default-window treatment as the 422
		// branch above — a 500 also never renders #charges-list.
		today := browserToday(c)
		start, end := defaultChargesWindow(today)
		d := h.buildChargesPage(c.Request.Context(), uid, csrfToken, filterTeslaID, today, start, end)
		// design.md §D-Values (roadmap D15): same value-preservation treatment on
		// the 500 (writer-error) branch as the 422 (validation-error) branch.
		d.DefaultChargedOn = c.PostForm("charged_on")
		d.DefaultStartedAt = c.PostForm("started_at")
		d.DefaultEndedAt = c.PostForm("ended_at")
		d.FormValues = raw
		// design.md §D-Fields: same recomputation on the 500 branch as the 422 one.
		applyRawRequiredState(&d, raw)
		renderError(c, http.StatusInternalServerError, fragments.ChargeCreateForm(d, map[string]string{
			"_top": i18n.T(c.Request.Context(), i18n.KeyChargesErrorCouldNotSaveEntry),
		}))
		return
	}

	_ = created
	h.recalculateAfterChargeWrite(c.Request.Context(), uid, entry.TeslaID, entry.ChargedOn)

	// Reset form defaults to the submitted vehicle so the user can log another
	// charge for the same car without re-picking.
	filterTeslaID := entry.TeslaID
	if filterTeslaID == 0 {
		if sel, ok := h.resolveSelectedVehicle(c.Request.Context(), c, uid); ok {
			filterTeslaID = sel.TeslaID
		}
	}
	// design.md §D-Include: the SUCCESS path (only) resolves the OOB refresh
	// window from the hx-include'd hidden inputs (windowFromForm), so the
	// #charges-list OOB swap reflects the filter window active at submit time,
	// not the default.
	today := browserToday(c)
	start, end := windowFromForm(c, today)
	d := h.buildChargesPage(c.Request.Context(), uid, csrfToken, filterTeslaID, today, start, end)
	// The one place ChargesPageData.Notice is ever set: the response to the
	// write that earned it. It rides in on the primary #charges-create-form
	// swap and is gone on the next render of any kind.
	d.Notice = i18n.T(c.Request.Context(), i18n.KeyChargesNoticeEntryCreated)
	render(c, http.StatusOK, fragments.ChargeCreateSuccessOOB(d))
}

// ChargeRowUpdate handles PUT /ui/charges/row/:id. Saves the edited row and
// swaps back to the static row on success, or re-renders with validation errors.
func (h *Handler) ChargeRowUpdate(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	if !h.checkCSRF(c) {
		return
	}
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.String(http.StatusBadRequest, i18n.T(c.Request.Context(), i18n.KeyChargesErrorInvalidID))
		return
	}

	vehicles, err := h.acct.RegisteredVehicles(c.Request.Context(), uid)
	if err != nil {
		c.String(http.StatusInternalServerError, i18n.T(c.Request.Context(), i18n.KeyChargesErrorCouldNotValidateVehicleOwnership))
		return
	}
	sess := sessions.Default(c)
	csrfToken, _ := sess.Get(csrfManualChargeKey).(string)

	// design.md §D-Include: the hidden start/end inputs charge_row_edit.templ
	// (Wave 4.3) now renders inside the edit form are read here via
	// windowFromForm's exact best-effort fallback shape — never a validation
	// gate. Computed once and reused by every branch below (success and
	// error) so the echoed window is consistent.
	today := browserToday(c)
	start, end := windowFromForm(c, today)
	windowStartStr := start.Format("2006-01-02")
	windowEndStr := end.Format("2006-01-02")

	entry, raw, validationErrors, ok2 := h.parseChargeForm(c, uid, vehicles)
	// Same one-IN_PROGRESS-per-(vehicle, charged_on) rule the create path
	// enforces, so flipping a DONE row back to IN_PROGRESS cannot bypass it.
	// The row being edited is excluded from the scan — an entry that is
	// already IN_PROGRESS must not conflict with itself.
	if ok2 {
		if conflictDate, found := h.inProgressConflictOn(c.Request.Context(), uid, entry, id); found {
			validationErrors = map[string]string{
				"_top": fmt.Sprintf(i18n.T(c.Request.Context(), i18n.KeyChargesErrorInProgressExists), conflictDate),
			}
			ok2 = false
		}
	}
	if !ok2 {
		if validationErrors == nil {
			return
		}
		// design.md §D-Values (roadmap D15): build the re-rendered row's VM from
		// the raw submission, not from the (possibly zero-value) parsed entry —
		// a value that failed validation has no representation in entry's typed
		// fields, so only the raw string survives to be echoed back.
		vm := chargeEntryVMFromRawValues(idStr, raw, h.vehicleLabelForSelected(c, uid, vehicles))
		vm.RawChargedOn = c.PostForm("charged_on")
		vm.RawStartedAt = c.PostForm("started_at")
		vm.RawEndedAt = c.PostForm("ended_at")
		// The validation-ERROR path is UNCHANGED in shape (design.md §D-Refresh)
		// — still fragments.ChargeRowEdit only, no #charges-list touch — only the
		// call site's new windowStartStr/windowEndStr params are added, echoing
		// back whatever was posted.
		renderError(c, http.StatusUnprocessableEntity, fragments.ChargeRowEdit(vm, csrfToken, validationErrors, windowStartStr, windowEndStr))
		return
	}
	entry.ID = id
	entry.AccountID = uid

	// Resolve the PRE-update ChargedOn BEFORE calling Update — once Update
	// commits, the old date is gone; there is no other way to recover it
	// (design.md D5, "Manual Charge Write Path Triggers Analytics
	// Recalculation"). A lookup miss (e.g. the id no longer exists) just
	// means there is no old date to additionally recalculate — the write
	// itself still proceeds and is validated on its own terms below.
	_, oldChargedOn, hadOld := h.fetchEntryTeslaIDAndChargedOn(c.Request.Context(), uid, id)

	updated, err := h.chargingWriter.Update(c.Request.Context(), entry)
	if err != nil {
		log.Printf("gateway: ChargeRowUpdate writer error for account %s, id %s: %v", uid, id, err)
		// Same value-preservation treatment on the 500 (writer-error) branch as
		// the 422 (validation-error) branch above — design.md §D-Values. Same
		// "unchanged shape, only add the window params" treatment as the 422
		// branch — design.md §D-Refresh.
		vm := chargeEntryVMFromRawValues(idStr, raw, h.vehicleLabelForSelected(c, uid, vehicles))
		vm.RawChargedOn = c.PostForm("charged_on")
		vm.RawStartedAt = c.PostForm("started_at")
		vm.RawEndedAt = c.PostForm("ended_at")
		renderError(c, http.StatusInternalServerError, fragments.ChargeRowEdit(vm, csrfToken, map[string]string{
			"_top": i18n.T(c.Request.Context(), i18n.KeyChargesErrorCouldNotSaveEntry),
		}, windowStartStr, windowEndStr))
		return
	}
	h.recalculateAfterChargeWrite(c.Request.Context(), uid, updated.TeslaID, updated.ChargedOn)
	if hadOld && !oldChargedOn.Equal(updated.ChargedOn) {
		h.recalculateAfterChargeWrite(c.Request.Context(), uid, updated.TeslaID, oldChargedOn)
	}
	// A SUCCESSFUL edit re-renders the WHOLE #charges-list region, retargeted
	// away from the form's own #charge-row-{id}. This replaced an earlier
	// "primary <tr> swap + OOB #charges-list refresh" response, which did not
	// refresh the list in the browser at all:
	//
	// htmx 2.0.4 parses a response inside <template class="internal-htmx-wrapper">.
	// Per the HTML parsing spec a <tr> start tag switches the parser into table
	// insertion mode, so a NON-table sibling that follows it — here the
	// <div id="charges-list" hx-swap-oob=...> — goes down the foster-parenting
	// path instead of staying a top-level child of the fragment. htmx only
	// applies hx-swap-oob to top-level children, so the refresh was silently
	// dropped. ChargeCreateSuccessOOB is unaffected because its response is
	// <div>+<div>, with no <tr> to put the parser in table mode — which is
	// exactly why create refreshed the list and edit did not.
	//
	// HX-Retarget/HX-Reswap keep the form's markup honest: hx-target stays
	// #charge-row-{id}, which is right for the 4xx/5xx branches above (they
	// re-render the edit row in place, preserving the user's typed values per
	// design.md §D-Values). Only the success path retargets, so there is one
	// response element, no OOB, and no <tr>/<div> mixing.
	//
	// The window resets to the default 7-day one so the user lands back on the
	// "last 7 days" preset: defaultChargesWindow returns exactly that preset's
	// (today-6, today) range and buildChargesPresets marks it Active by its own
	// exact-match rule — nothing here hardcodes a preset index or label. The
	// posted window still governs the ERROR branches' echo; a failed save must
	// not move the user's filter.
	c.Header("HX-Retarget", "#charges-list")
	c.Header("HX-Reswap", "outerHTML")
	listStart, listEnd := defaultChargesWindow(today)
	list := h.buildChargesPage(c.Request.Context(), uid, csrfToken, updated.TeslaID, today, listStart, listEnd)
	renderFragment(c, http.StatusOK, pages.ChargePage(list), "charges-list")
}

// ChargeRowDelete handles DELETE /ui/charges/row/:id. Deletes the entry and
// re-renders the WHOLE #charges-list region (design.md §D-Refresh) — the
// delete button's hx-target is "#charges-list", not "#charge-row-{id}", since
// after a successful delete the row no longer exists to swap into. This makes
// ChargeRowDelete structurally identical to ChargesListFragment, preceded by
// the actual Writer.Delete call: same buildChargesPage path, same
// renderFragment/renderFragmentError machinery, no new render function.
//
// Root cause of the MAG-5 delete-row alert (T1.1 — root-caused by STATIC analysis;
// live reproduce deferred to T7.4 manual smoke, leader-authorized deviation, see
// progress.json decisions): Go's net/http only parses request bodies for POST,
// PUT, and PATCH (see http.Request.ParseForm switch). The previous delete button
// in fragments.ChargeRow sent the csrf_token via hx-include on a hidden
// #csrf-delete-<id> form input, which travels in the DELETE request BODY. Since
// the body was never parsed into c.request.PostForm, checkCSRFKey's
// c.PostForm("csrf_token") returned "" for DELETE even though the input carried a
// valid token — the header fallback (X-CSRF-Token) was never set either, so the
// constant-time compare failed -> HTTP 403 "invalid csrf token" -> htmx showed
// the user a JS alert() (4xx + text/plain Content-Type triggers htmx's default
// error-alert). CSRF lifecycle was NOT stale: the row's embedded token already
// matched the session's csrf_manualcharge at render time (ChargePage and
// ChargesContentFragment write the same token to the session and the form/rows in
// one render). The fix lives in charge_row.templ: the delete button now sends the
// token on the X-CSRF-Token HEADER via htmx hx-headers, which checkCSRFKey's
// existing header fallback reads — no body parse needed. checkCSRF /
// subtle.ConstantTimeCompare stay fail-closed. On a stale/missing token the
// handler still returns HTTP 403 (intended, defense-in-depth); only the WIRE PATH
// changed.
func (h *Handler) ChargeRowDelete(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	if !h.checkCSRF(c) {
		return
	}
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.String(http.StatusBadRequest, i18n.T(c.Request.Context(), i18n.KeyChargesErrorInvalidID))
		return
	}
	sess := sessions.Default(c)
	csrfToken, _ := sess.Get(csrfManualChargeKey).(string)

	// design.md §D-Refresh/§D-Include: the row's own Delete URL carries
	// ?start=&end= (threaded by charge_row.templ's hx-delete, Wave 4.2), read
	// here via windowFromQuery's best-effort fallback — never a validation
	// gate (leader resolution L2).
	today := browserToday(c)
	start, end := windowFromQuery(c, today)

	filterTeslaID := int64(0)
	if sel, ok := h.resolveSelectedVehicle(c.Request.Context(), c, uid); ok {
		filterTeslaID = sel.TeslaID
	}

	// Resolve the entry's ChargedOn (and TeslaID) BEFORE calling Delete — the
	// Delete port does not return the deleted entry, so this is the only
	// chance to learn which day needs recalculating (design.md D5). A lookup
	// miss just means there is no day to recalculate; the delete still
	// proceeds.
	entryTeslaID, entryChargedOn, hadEntry := h.fetchEntryTeslaIDAndChargedOn(c.Request.Context(), uid, id)

	err = h.chargingWriter.Delete(c.Request.Context(), uid, id)
	d := h.buildChargesPage(c.Request.Context(), uid, csrfToken, filterTeslaID, today, start, end)
	if err != nil {
		log.Printf("gateway: ChargeRowDelete writer error for account %s, id %s: %v", uid, id, err)
		d.Error = i18n.T(c.Request.Context(), i18n.KeyChargesErrorCouldNotDeleteEntry)
		renderFragmentError(c, http.StatusInternalServerError, pages.ChargePage(d), "charges-list")
		return
	}
	if hadEntry {
		h.recalculateAfterChargeWrite(c.Request.Context(), uid, entryTeslaID, entryChargedOn)
	}
	renderFragment(c, http.StatusOK, pages.ChargePage(d), "charges-list")
}

// defaultChargesWindow returns the standard chargesRangeDefaultDays window
// ending today, with no parsing involved — used by callers that never read
// ?start=&end= themselves (ChargePage, ChargesContentFragment, and
// ChargeCreate/ChargeRowUpdate's non-#charges-list-touching error branches),
// so the chargesRangeDefaultDays math (also in parseChargesRange and
// bestEffortWindow) is not duplicated inline a fourth time.
func defaultChargesWindow(today time.Time) (start, end time.Time) {
	end = today
	start = end.AddDate(0, 0, -(chargesRangeDefaultDays - 1))
	return start, end
}

// buildChargesPage is the gin-free helper that calls module ports and builds
// ChargesPageData. Decoupled from Gin so it can be called with fake port
// implementations in tests.
//
// The frozen 7-arg signature (design.md §Wave 5.1, tasks.md 5.1): today is
// RETAINED alongside start/end — they are different concepts and neither
// substitutes for the other. today is the BROWSER's calendar day
// (browserToday(c)) and drives the create form's D1 date defaults
// (day/todayDate) and the D2 battery suggestion; start/end is the LIST's
// filter window (design.md §D-Range). Deriving the form's default date from
// end would make the "This month" preset pre-fill the create form with the
// last day of the month.
func (h *Handler) buildChargesPage(ctx context.Context, uid uuid.UUID, csrfToken string, teslaIDFilter int64, today, start, end time.Time) fragments.ChargesPageData {
	// design.md §D-RM33-9: no vehicle resolved -> NO read of any kind against
	// charging.Reader (nor account.RegisteredVehicles, which is only needed to
	// map entries the empty-state path never fetches) — just the same
	// no-chrome empty state ChargesListFragment/ChargePage render on a
	// malformed window (design.md §D-Empty state 1: both conditions collapse
	// to the identical render).
	if teslaIDFilter == 0 {
		return fragments.ChargesPageData{
			CSRFToken:      csrfToken,
			EmptyState:     true,
			NoFilterChrome: true,
		}
	}

	vehicles, err := h.acct.RegisteredVehicles(ctx, uid)
	if err != nil {
		log.Printf("gateway: RegisteredVehicles error for account %s: %v", uid, err)
		return fragments.ChargesPageData{
			CSRFToken: csrfToken,
			Error:     i18n.T(ctx, i18n.KeyChargesErrorCouldNotLoadVehicles),
		}
	}

	// design.md §D-Range/§Context fact 1: switched onto
	// ListEntriesByVehicleBetween — [start, end] inclusive of both bounds, no
	// limit parameter, the window itself bounds the result (D13).
	entries, err := h.chargingReader.ListEntriesByVehicleBetween(ctx, uid, teslaIDFilter, start, end)
	var pageError string
	if err != nil {
		log.Printf("gateway: charging reader error for account %s: %v", uid, err)
		pageError = i18n.T(ctx, i18n.KeyChargesErrorCouldNotLoadEntries)
		entries = nil
	}

	// design.md §D-Tiles: computed over the SAME entries slice
	// ListEntriesByVehicleBetween returned, BEFORE the VM-mapping loop below,
	// so the tiles and the table are provably the same data (D13). On a reader
	// error entries is nil, so buildChargeTiles(nil) naturally yields the
	// zero/em-dash tile values (design.md §D-Empty state 2) with no special
	// casing.
	tiles := buildChargeTiles(entries)

	vms := make([]fragments.ChargeEntryVM, 0, len(entries))
	for _, e := range entries {
		vms = append(vms, chargeEntryVMFromEntry(e, vehicles))
	}

	// D1: default charged_on / started_at / ended_at to the user's current calendar
	// day so the create form's date fields render pre-populated. The template emits
	// these verbatim into the input's value attribute — no time math in markup.
	// charged_on is REQUIRED; started_at / ended_at stay OPTIONAL (parseChargeForm
	// still accepts a cleared field and persists nil StartedAt / EndedAt).
	//
	// All three derive from ONE `day` value so the "Date" field can never drift from
	// the "Started/Ended at" fields. `today` is the BROWSER's local day (browserToday),
	// not UTC: for a UTC-5 user, time.Now().UTC() has already rolled to tomorrow after
	// 19:00 local, which would default the form to the wrong date every evening.
	day := today.Format("2006-01-02")
	todayDate := day + "T00:00"

	// D2: build the start_battery_pct suggestion label from the active vehicle's
	// latest telemetry snapshot BatteryLevelPct. Reuses the SAME telemetry.Reader
	// port the dashboard already calls once per render — one batched read, pick the
	// snapshot matching the resolved teslaIDFilter. Graceful empty: a read error or
	// no matching snapshot leaves the suggestion "" and the page still renders (no
	// fabricated value). Log read errors at most; never degrade the page.
	suggestion := ""
	if teslaIDFilter != 0 && h.telemetryReader != nil {
		snaps, snapErr := h.telemetryReader.LatestSnapshotsByAccount(ctx, uid)
		if snapErr != nil {
			log.Printf("gateway: charges suggestion telemetry reader error for account %s: %v", uid, snapErr)
		} else {
			for _, s := range snaps {
				if s.TeslaID == teslaIDFilter {
					suggestion = fmt.Sprintf(i18n.T(ctx, i18n.KeyChargesErrorBatterySuggestion), s.BatteryLevelPct)
					break
				}
			}
		}
	}

	// RM33/design.md §D-Values: the create form's status control always defaults
	// to IN_PROGRESS on a fresh (non-error) render — the error-render path
	// (ChargeCreate's 4xx/5xx branches, task 3.3) overwrites FormValues with the
	// user's actual submission afterward. RequiredEndedAt/RequiredEndBatteryPct
	// are computed from the SAME status so the two never disagree on a fresh
	// load (design.md §D-Fields).
	required := make(map[charging.Field]bool)
	for _, f := range charging.RequiredFieldsFor(charging.StatusInProgress) {
		required[f] = true
	}

	return fragments.ChargesPageData{
		Entries:                   vms,
		CSRFToken:                 csrfToken,
		EmptyState:                len(vms) == 0 && pageError == "",
		Error:                     pageError,
		Presets:                   buildChargesPresets(ctx, start, end, today),
		Tiles:                     tiles,
		WindowStartStr:            start.Format("2006-01-02"),
		WindowEndStr:              end.Format("2006-01-02"),
		DefaultChargedOn:          day,
		DefaultStartedAt:          todayDate,
		DefaultEndedAt:            todayDate,
		StartBatteryPctSuggestion: suggestion,
		FormValues: fragments.ChargeFormValues{
			Status: string(charging.StatusInProgress),
		},
		RequiredEndedAt:       required[charging.FieldEndedAt],
		RequiredEndBatteryPct: required[charging.FieldEndBatteryPct],
	}
}

// bestEffortWindow parses a start/end pair from whichever source the caller
// reads, falling back to the default window. Cosmetic threading only
// (design.md §D-Include) — never a validation gate, in either direction: an
// absent or malformed pair only affects which window a write's post-write
// refresh re-renders, never whether the write itself succeeds. This is
// deliberately NOT parseChargesRange, which REJECTS a malformed window (right
// for the user-driven filter route the caller 400s on; wrong here, where a
// malformed window must never turn a successful write into an error).
func bestEffortWindow(rawStart, rawEnd string, today time.Time) (start, end time.Time) {
	s, errS := time.Parse("2006-01-02", rawStart)
	e, errE := time.Parse("2006-01-02", rawEnd)
	if errS != nil || errE != nil || e.Before(s) {
		return defaultChargesWindow(today)
	}
	return s, e
}

// windowFromForm resolves the active start/end for a write handler's
// post-write OOB refresh, from POSTed form values (design.md §D-Include). See
// bestEffortWindow for the shared fallback contract.
func windowFromForm(c *gin.Context, today time.Time) (start, end time.Time) {
	return bestEffortWindow(c.PostForm("start"), c.PostForm("end"), today)
}

// windowFromQuery is windowFromForm's GET-query sibling (design.md
// §D-Include, leader resolution L2), used by ChargeRowDelete/ChargeRowStatic/
// ChargeRowEditFragment's ?start=&end= query params. Delegates to the SAME
// bestEffortWindow so the "never a gate" behaviour cannot drift between the
// POST and GET sides.
func windowFromQuery(c *gin.Context, today time.Time) (start, end time.Time) {
	return bestEffortWindow(c.Query("start"), c.Query("end"), today)
}

// fetchEntryVM fetches a single entry by listing all account entries and finding
// the one matching id (no GetEntry on the port — design decision D6). Returns
// false if not found.
func (h *Handler) fetchEntryVM(ctx context.Context, uid uuid.UUID, id uuid.UUID) (fragments.ChargeEntryVM, bool) {
	entries, err := h.chargingReader.ListEntriesByAccount(ctx, uid, 0)
	if err != nil {
		return fragments.ChargeEntryVM{}, false
	}
	vehicles, _ := h.acct.RegisteredVehicles(ctx, uid)
	for _, e := range entries {
		if e.ID == id {
			return chargeEntryVMFromEntry(e, vehicles), true
		}
	}
	return fragments.ChargeEntryVM{}, false
}

// fetchEntryTeslaIDAndChargedOn resolves a manual charge entry's stored
// TeslaID and ChargedOn by id, listing all account entries and matching
// (mirrors fetchEntryVM's own no-GetEntry-port shape, design decision D6).
// Returns false if not found. Used by ChargeRowUpdate (to learn the
// PRE-update ChargedOn before it is overwritten) and ChargeRowDelete (to
// learn TeslaID/ChargedOn before the entry is gone entirely — the Delete
// port does not return the deleted entry, design.md D5) so
// recalculateAfterChargeWrite can be called for the affected date.
func (h *Handler) fetchEntryTeslaIDAndChargedOn(ctx context.Context, uid uuid.UUID, id uuid.UUID) (teslaID int64, chargedOn time.Time, ok bool) {
	entries, err := h.chargingReader.ListEntriesByAccount(ctx, uid, 0)
	if err != nil {
		return 0, time.Time{}, false
	}
	for _, e := range entries {
		if e.ID == id {
			return e.TeslaID, e.ChargedOn, true
		}
	}
	return 0, time.Time{}, false
}

// inProgressConflictOn reports whether the account already has an IN_PROGRESS
// manual charge entry for entry's vehicle on entry's ChargedOn day — the
// "one charge in progress per vehicle per date" rule — returning that entry's
// charged_on formatted "2006-01-02" for the user-facing message.
//
// excludeID exempts one entry from the scan: uuid.Nil on the create path
// (nothing to exempt), and the edited row's own id on ChargeRowUpdate, so an
// entry that is already IN_PROGRESS never conflicts with itself.
//
// Scope gate: only an IN_PROGRESS submission can conflict. A DONE entry is
// unconstrained — any number may share a date — so a DONE submission returns
// false without reading anything.
//
// The read is ListEntriesByVehicleBetween(chargedOn, chargedOn) — the same
// port every list render already uses, with both bounds on the single day in
// question, so the check costs one narrowly-bounded read and adds no method to
// charging.Reader. The day comparison is nonetheless re-asserted on each
// returned row rather than trusted to the port's window: correctness of a
// write-blocking rule must not depend on a read port's filtering being exact,
// and the two sides also carry different time components (a form-parsed UTC
// midnight vs. whatever the DATE column round-trips as), so the compare is on
// the calendar day, not the instant.
//
// A reader error FAILS OPEN (logged, returns false, the write proceeds). The
// rule is an application-level convenience with no DB constraint behind it;
// turning a transient read failure into a refusal to save the user's data
// would trade a real loss for a hypothetical duplicate. This mirrors the
// log-and-continue posture buildChargesPage's telemetry suggestion lookup and
// recalculateAfterChargeWrite already take for non-essential follow-ups.
func (h *Handler) inProgressConflictOn(ctx context.Context, uid uuid.UUID, entry charging.Entry, excludeID uuid.UUID) (string, bool) {
	if entry.Status != charging.StatusInProgress || entry.TeslaID == 0 {
		return "", false
	}
	existing, err := h.chargingReader.ListEntriesByVehicleBetween(ctx, uid, entry.TeslaID, entry.ChargedOn, entry.ChargedOn)
	if err != nil {
		log.Printf("gateway: in-progress conflict check reader error for account %s, vehicle %d, date %s: %v",
			uid, entry.TeslaID, entry.ChargedOn.Format("2006-01-02"), err)
		return "", false
	}
	want := entry.ChargedOn.Format("2006-01-02")
	for _, e := range existing {
		if e.Status != charging.StatusInProgress || e.ID == excludeID {
			continue
		}
		if e.ChargedOn.Format("2006-01-02") == want {
			return want, true
		}
	}
	return "", false
}

// recalculateAfterChargeWrite calls the analytics module's recalculation
// port for the given vehicle/date, AFTER a manual charge write (Create,
// Update, or Delete) has already committed successfully — design.md D5
// ("Manual Charge Write Path Triggers Analytics Recalculation"). This keeps
// the precomputed history charts (internal/analytics' vehicle_metrics table)
// current with no separate refresh step, matching today's live-computed
// behavior (roadmap D10 characterization bar).
//
// A Recalculate failure is logged and swallowed, never surfaced to the
// caller: the user's write already committed, so failing their request over
// a derived-metrics recalculation error would be wrong (their data IS
// saved); silently doing nothing would hide a real fault, so it is logged —
// mirrors this file's existing non-fatal-follow-up convention (the charge
// suggestion telemetry lookup in buildChargesPage logs and continues on a
// read error rather than failing the page). The chart falls back to the
// last-recalculated state until the next nightly Reconcile call heals it
// (design.md D7).
//
// T7's app.RecalculateVehicleData relocates this CALL to a new composition
// root, not this logic (design.md D5) — the interim composition root is this
// handler file.
func (h *Handler) recalculateAfterChargeWrite(ctx context.Context, uid uuid.UUID, teslaID int64, chargedOn time.Time) {
	if err := h.analyticsRecalculator.Recalculate(ctx, uid, teslaID, chargedOn, chargedOn); err != nil {
		log.Printf("gateway: analytics recalculate error for account %s, vehicle %d, date %s: %v",
			uid, teslaID, chargedOn.Format("2006-01-02"), err)
	}
}

// checkCSRF reads the submitted csrf_token (from form body or hx-csrf-token
// header), compares it to the charging session value via constant-time
// compare, and writes 403 on mismatch. Returns true if CSRF is valid.
func (h *Handler) checkCSRF(c *gin.Context) bool {
	return h.checkCSRFKey(c, csrfManualChargeKey)
}

// checkCSRFKey is the generic per-form CSRF check. keyed by the session key the
// issuing handler stored the token under (e.g. csrfManualChargeKey,
// csrfVehicleSelectKey). Same fail-closed contract as checkCSRF: an empty session
// token never matches, so a write before the issuing GET was ever loaded is
// rejected. Reads csrf_token from the form body, falling back to the
// X-CSRF-Token header.
func (h *Handler) checkCSRFKey(c *gin.Context, key string) bool {
	sess := sessions.Default(c)
	want, _ := sess.Get(key).(string)
	got := c.PostForm("csrf_token")
	if got == "" {
		got = c.GetHeader("X-CSRF-Token")
	}
	// Fail closed when no token was issued for this session. subtle.ConstantTimeCompare
	// returns 1 for two equal-length equal slices, so comparing "" (no session token,
	// e.g. a write before GET /charges was ever loaded) against "" (no submitted token)
	// would pass and let a tokenless request through. Both sides must be a real token.
	if want == "" || subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
		c.String(http.StatusForbidden, i18n.T(c.Request.Context(), i18n.KeyChargesErrorInvalidCSRFToken))
		return false
	}
	return true
}

// chargeEntryVMFromEntry maps a charging.Entry and the account's registered
// vehicle list to a ChargeEntryVM. Pre-computes all derived display strings so
// templates do no arithmetic (design.md D6).
func chargeEntryVMFromEntry(e charging.Entry, vehicles []account.Vehicle) fragments.ChargeEntryVM {
	label := vehicleLabelFor(e.TeslaID, vehicles)

	// D14 extends the "—" empty-placeholder convention (already applied to
	// EnergyKWh) to CostPerKWhLabel, BatteryDelta, and DurationLabel.
	costLabel := "—"
	if v := e.CostPerKWh(); v != nil {
		costLabel = formatMoney(*v, e.Currency) + "/kWh"
	}
	batteryDelta := "—"
	if d := e.BatteryDelta(); d != nil {
		if *d >= 0 {
			batteryDelta = fmt.Sprintf("+%d%%", *d)
		} else {
			batteryDelta = fmt.Sprintf("%d%%", *d)
		}
	}
	// BatteryRange (D11/D14): "22% -> 70%"-shaped when both StartBatteryPct and
	// EndBatteryPct are present, else "—". Rendered ALONGSIDE BatteryDelta, not
	// in place of it.
	batteryRange := "—"
	if e.StartBatteryPct != nil && e.EndBatteryPct != nil {
		batteryRange = fmt.Sprintf("%d%% → %d%%", *e.StartBatteryPct, *e.EndBatteryPct)
	}
	durationLabel := "—"
	if dur := e.SessionDuration(); dur != nil {
		h2 := int(dur.Hours())
		m := int(dur.Minutes()) % 60
		if h2 > 0 {
			durationLabel = fmt.Sprintf("%dh %dm", h2, m)
		} else {
			durationLabel = fmt.Sprintf("%dm", m)
		}
	}

	chargingType := ""
	if e.ChargingType != nil {
		chargingType = *e.ChargingType
	}
	locationKind := ""
	if e.LocationKind != nil {
		locationKind = *e.LocationKind
	}
	locationLabelStr := ""
	if e.LocationLabel != nil {
		locationLabelStr = *e.LocationLabel
	}
	notes := ""
	if e.Notes != nil {
		notes = *e.Notes
	}

	rawStartedAt := ""
	if e.StartedAt != nil {
		rawStartedAt = e.StartedAt.UTC().Format("2006-01-02T15:04")
	}
	rawEndedAt := ""
	if e.EndedAt != nil {
		rawEndedAt = e.EndedAt.UTC().Format("2006-01-02T15:04")
	}
	rawStartPct := ""
	if e.StartBatteryPct != nil {
		rawStartPct = strconv.Itoa(*e.StartBatteryPct)
	}
	rawEndPct := ""
	if e.EndBatteryPct != nil {
		rawEndPct = strconv.Itoa(*e.EndBatteryPct)
	}
	rawOdometerKm := ""
	if e.OdometerKm != nil {
		rawOdometerKm = strconv.Itoa(*e.OdometerKm)
	}

	// RM33/design.md §D-Fields: RequiredEndedAt/RequiredEndBatteryPct are
	// computed here (handler), never in the template, from
	// charging.RequiredFieldsFor(e.Status) — the single source of truth.
	required := make(map[charging.Field]bool)
	for _, f := range charging.RequiredFieldsFor(e.Status) {
		required[f] = true
	}

	// EnergyKWh is display text (RM33 tier 1, design.md D2/D11): an IN_PROGRESS
	// entry may not know its energy yet, so a nil EnergyAddedKWh renders the
	// project-wide empty placeholder "—" (roadmap D14), not a fabricated
	// "0.00 kWh". Mirrors dashStat's "—" fallback (templates/pages/dashboard.go).
	energyLabel := "—"
	if e.EnergyAddedKWh != nil {
		energyLabel = fmt.Sprintf("%.2f kWh", *e.EnergyAddedKWh)
	}
	// RawEnergyKWh feeds a number input's value attribute, not display text —
	// an em-dash there would be an invalid/confusing input value, so a nil
	// energy is the empty string, matching every other Raw* field on this VM
	// (RawStartedAt, RawEndedAt, RawStartBatteryPct, ...) for an absent value.
	rawEnergyKWh := ""
	if e.EnergyAddedKWh != nil {
		rawEnergyKWh = strconv.FormatFloat(*e.EnergyAddedKWh, 'f', 2, 64)
	}

	return fragments.ChargeEntryVM{
		ID:                 e.ID.String(),
		VehicleLabel:       label,
		ChargedOnLabel:     e.ChargedOn.Format("Mon Jan 2, 2006"),
		EnergyKWh:          energyLabel,
		PriceLabel:         formatMoney(e.Price, e.Currency),
		Currency:           e.Currency,
		CostPerKWhLabel:    costLabel,
		BatteryDelta:       batteryDelta,
		BatteryRange:       batteryRange,
		DurationLabel:      durationLabel,
		ChargingType:       chargingType,
		LocationKind:       locationKind,
		LocationLabel:      locationLabelStr,
		Notes:              notes,
		RawChargedOn:       e.ChargedOn.Format("2006-01-02"),
		RawEnergyKWh:       rawEnergyKWh,
		RawPrice:           strconv.FormatFloat(e.Price, 'f', 2, 64),
		RawStartedAt:       rawStartedAt,
		RawEndedAt:         rawEndedAt,
		RawStartBatteryPct: rawStartPct,
		RawEndBatteryPct:   rawEndPct,
		RawOdometerKm:      rawOdometerKm,
		TeslaID:            e.TeslaID,
		VIN:                e.VIN,

		Status:    string(e.Status),
		RawStatus: string(e.Status),

		// Complete (design.md §D-Dot) is the handler-computed completeness
		// signal driving the completeness dot's colour — computed via
		// entryComplete(e) (handlers/charges_tiles.go), never in the template.
		Complete: entryComplete(e),

		RequiredEndedAt:       required[charging.FieldEndedAt],
		RequiredEndBatteryPct: required[charging.FieldEndBatteryPct],
	}
}

// vehicleLabelFor returns the DisplayName for the given teslaID, or the string
// form of the teslaID if not found in the registered vehicles list.
func vehicleLabelFor(teslaID int64, vehicles []account.Vehicle) string {
	for _, v := range vehicles {
		if v.TeslaID == teslaID {
			return v.DisplayName
		}
	}
	return strconv.FormatInt(teslaID, 10)
}

// vehicleLabelForSelected resolves the session-selected vehicle's display
// label — the "vehicle context the handler already has independent of
// parsing" (design.md §D-Values) used to populate a raw-values-sourced
// ChargeEntryVM on a parseChargeForm failure, where entry.TeslaID cannot be
// trusted (parseChargeForm returns a zero-value Entry on validation
// failure). Mirrors the same resolveSelectedVehicle call ChargeCreate's
// error branches already make for their filterTeslaID fallback. Empty
// string if no vehicle can be resolved.
func (h *Handler) vehicleLabelForSelected(c *gin.Context, uid uuid.UUID, vehicles []account.Vehicle) string {
	if sel, ok := h.resolveSelectedVehicle(c.Request.Context(), c, uid); ok {
		return vehicleLabelFor(sel.TeslaID, vehicles)
	}
	return ""
}

// applyRawRequiredState recomputes a ChargesPageData's RequiredEndedAt /
// RequiredEndBatteryPct pair from the status the user actually submitted, so an
// error re-render of the create form agrees with the status it is rendering.
//
// Without this, the 4xx/5xx branches overwrite FormValues with `raw` (roadmap
// D15) while leaving the Required* pair on buildChargesPage's fresh-load
// IN_PROGRESS default — a user who picks DONE and trips an unrelated validation
// error gets ended_at/end_battery_pct back without their `required` attribute.
// design.md §D-Fields requires the served HTML to carry the correct `required`
// state "for the status being rendered", with no flash-of-wrong-state before JS
// runs, so relying on RD13's htmx:load listener to correct it client-side is not
// sufficient. This is the create-form counterpart of the same recomputation
// chargeEntryVMFromRawValues already does for the inline edit row.
//
// Unknown/absent statuses fail closed to DONE (the strictest required set),
// matching chargeEntryVMFromRawValues and charging.RequiredFieldsFor.
func applyRawRequiredState(d *fragments.ChargesPageData, raw fragments.ChargeFormValues) {
	status := charging.Status(raw.Status)
	if status != charging.StatusInProgress && status != charging.StatusDone {
		status = charging.StatusDone
	}
	required := make(map[charging.Field]bool)
	for _, f := range charging.RequiredFieldsFor(status) {
		required[f] = true
	}
	d.RequiredEndedAt = required[charging.FieldEndedAt]
	d.RequiredEndBatteryPct = required[charging.FieldEndBatteryPct]
}

// chargeEntryVMFromRawValues builds a ChargeEntryVM directly from the raw
// submitted POST values (design.md §D-Values, roadmap D15) for the inline
// edit row's 4xx/5xx re-render — used INSTEAD OF chargeEntryVMFromEntry so a
// value that failed validation (with no representation in charging.Entry's
// typed fields) is still echoed back to the user, not silently discarded on
// re-render. id and vehicleLabel are the untouched context the handler
// already has independent of parsing (chargeEntryVMFromEntry's non-error
// path sources these from a persisted charging.Entry instead). The caller
// additionally sets RawChargedOn/RawStartedAt/RawEndedAt from c.PostForm —
// mirroring ChargesPageData.DefaultChargedOn/DefaultStartedAt/DefaultEndedAt's
// same treatment in ChargeCreate's error branches — since ChargeFormValues
// carries no charged_on/started_at/ended_at fields (design.md §D-Values).
//
// RequiredEndedAt/RequiredEndBatteryPct are computed from raw.Status via
// charging.RequiredFieldsFor, falling back to the strictest (DONE) set when
// raw.Status fails to parse as a recognized charging.Status — the same
// fail-closed posture charging.RequiredFieldsFor documents for its own
// unrecognized-status default case (internal/charging/validation.go).
func chargeEntryVMFromRawValues(id string, raw fragments.ChargeFormValues, vehicleLabel string) fragments.ChargeEntryVM {
	status := charging.Status(raw.Status)
	if status != charging.StatusInProgress && status != charging.StatusDone {
		status = charging.StatusDone
	}
	required := make(map[charging.Field]bool)
	for _, f := range charging.RequiredFieldsFor(status) {
		required[f] = true
	}

	return fragments.ChargeEntryVM{
		ID:                    id,
		VehicleLabel:          vehicleLabel,
		RawEnergyKWh:          raw.EnergyAddedKWh,
		RawPrice:              raw.Price,
		LocationKind:          raw.LocationKind,
		RawStartBatteryPct:    raw.StartBatteryPct,
		RawEndBatteryPct:      raw.EndBatteryPct,
		ChargingType:          raw.ChargingType,
		LocationLabel:         raw.LocationLabel,
		Notes:                 raw.Notes,
		RawOdometerKm:         raw.OdometerKm,
		Status:                raw.Status,
		RawStatus:             raw.Status,
		RequiredEndedAt:       required[charging.FieldEndedAt],
		RequiredEndBatteryPct: required[charging.FieldEndBatteryPct],
	}
}

// parseChargeForm parses and validates the charge form from a Gin context.
// Returns the entry, the raw submitted form values (for a 4xx/5xx re-render —
// roadmap D15, design.md §D-Values), any validation errors, and whether parsing
// succeeded. On a 403-level ownership failure it writes the response itself and
// returns validationErrors=nil, ok=false so the caller knows to stop.
//
// raw is built from c.PostForm(...) calls FIRST, before any parsing, so it is
// populated identically on every return path (success and every failure) —
// design.md §D-Values.
//
// MAG-5 form changes (D1/D4/D5/D6/D7) baked in here:
//   - D4: vehicle is sourced from the session-selected vehicle via
//     resolveSelectedVehicle (the same call ChargePage/List/ContentFragment make),
//     NOT from a `vehicle` form field. The entries list is already scoped to the
//     selected vehicle, so the create form is implicitly for that vehicle. The
//     tenant-ownership check (vehicleOwned) is preserved as defense-in-depth —
//     resolveSelectedVehicle only returns vehicles from the user's registered
//     list, so this check is a belt-and-suspenders guard.
//   - D5: Currency is HARDCODED "COP" — the form's disabled Currency input is
//     for display transparency only (a disabled input is not submitted, so
//     reading c.PostForm("currency") would always be ""). The
//     charging.Entry.Currency column stays a column the gateway always
//     sends COP down; no service change.
//   - D6: start_battery_pct is REQUIRED (empty or non-int / out-of-range ->
//     validation error), unconditionally, unchanged by RM33. end_battery_pct's
//     required-ness is now conditioned on RequiredFieldsFor(status) — see
//     RM33/D-Fields below.
//   - D1: started_at / ended_at stay OPTIONAL — clearing either still persists
//     nil StartedAt / EndedAt. The today's-date DEFAULT is a UI concern
//     (DefaultStartedAt on ChargesPageData); parseChargeForm still accepts an
//     empty (cleared) field. ended_at's REQUIRED-ness (RM33) is separate from
//     whether it parses when present — see RM33/D-Fields below.
//   - D7: energy_added_kwh accepts 3-decimal precision (UI step=0.001). No
//     server-side rounding — strconv.ParseFloat already accepts any precision.
//     The energy <= 0 rejection stays (positive only) when non-empty.
//
// RM33 (MAG-18) additions baked in here, design.md §D-Fields/§D-Values:
//   - status is parsed and validated FIRST (IN_PROGRESS/DONE only); an invalid
//     status short-circuits before RequiredFieldsFor is ever called, matching
//     the "reject before any data is written" contract charging.Writer follows.
//   - charging.RequiredFieldsFor(entry.Status) is the single source of truth for
//     whether ended_at / end_battery_pct are required — the gateway never
//     hardcodes that rule itself (roadmap D5's shape, applied at this call site).
//   - energy_added_kwh and price are now genuinely optional (empty ->
//     nil / 0, no error); odometer_km is a new always-optional non-negative int.
func (h *Handler) parseChargeForm(c *gin.Context, uid uuid.UUID, vehicles []account.Vehicle) (charging.Entry, fragments.ChargeFormValues, map[string]string, bool) {
	errs := make(map[string]string)

	// design.md §D-Values: build raw from c.PostForm(...) BEFORE any other
	// parsing, so it is populated on every return path (success and failure).
	raw := fragments.ChargeFormValues{
		Status:          c.PostForm("status"),
		EnergyAddedKWh:  c.PostForm("energy_added_kwh"),
		Price:           c.PostForm("price"),
		LocationKind:    c.PostForm("location_kind"),
		StartBatteryPct: c.PostForm("start_battery_pct"),
		EndBatteryPct:   c.PostForm("end_battery_pct"),
		ChargingType:    c.PostForm("charging_type"),
		LocationLabel:   c.PostForm("location_label"),
		Notes:           c.PostForm("notes"),
		OdometerKm:      c.PostForm("odometer_km"),
	}

	// D4: source (teslaID, vin) from the session-selected vehicle, not a form field.
	var teslaID int64
	var vin string
	if sel, ok := h.resolveSelectedVehicle(c.Request.Context(), c, uid); ok {
		teslaID = sel.TeslaID
		vin = sel.VIN
		// Defense-in-depth: confirm the resolved vehicle still belongs to the
		// calling account (resolveSelectedVehicle already picks from the
		// account's list, so this is a belt-and-suspenders guard).
		if !vehicleOwned(teslaID, vin, vehicles) {
			c.String(http.StatusForbidden, i18n.T(c.Request.Context(), i18n.KeyChargesErrorVehicleNotOwned))
			return charging.Entry{}, raw, nil, false
		}
	} else {
		// No resolvable selected vehicle (account has no registered vehicles or
		// the session selection is stale). Reach the failure path earlier — parity
		// with the old ownership-403 path but reached at validation time, without
		// calling the Writer. Rendered via fragments.ChargeCreateForm as a 422.
		// The form has no vehicle field anymore (D4 removed it), so the error is
		// surfaced via the top-of-form Alert (_top key) — the only guaranteed-
		// visible surface when there is no matching ui.Field to render the per-
		// field error slot.
		errs["_top"] = i18n.T(c.Request.Context(), i18n.KeyChargesErrorSelectVehicle)
	}

	// RM33/design.md §D-Fields: status is parsed FIRST. An invalid status
	// short-circuits before RequiredFieldsFor is ever called.
	var status charging.Status
	switch raw.Status {
	case string(charging.StatusInProgress):
		status = charging.StatusInProgress
	case string(charging.StatusDone):
		status = charging.StatusDone
	default:
		errs["status"] = i18n.T(c.Request.Context(), i18n.KeyChargesErrorStatusInvalid)
	}

	// required is the single source of truth for which of ended_at /
	// end_battery_pct this submission must carry — imported from charging, never
	// re-derived (design.md §D-Fields). Only computed once status parsed
	// successfully; an invalid status already recorded its own error above and
	// required stays nil (both lookups below default to "not required" in that
	// case, which is fine — the status error alone fails validation).
	var required map[charging.Field]bool
	if status != "" {
		required = make(map[charging.Field]bool)
		for _, f := range charging.RequiredFieldsFor(status) {
			required[f] = true
		}
	}

	chargedOnStr := c.PostForm("charged_on")
	var chargedOn time.Time
	if chargedOnStr == "" {
		errs["charged_on"] = i18n.T(c.Request.Context(), i18n.KeyChargesErrorDateRequired)
	} else {
		var parseErr error
		chargedOn, parseErr = time.Parse("2006-01-02", chargedOnStr)
		if parseErr != nil {
			errs["charged_on"] = i18n.T(c.Request.Context(), i18n.KeyChargesErrorInvalidDateFormat)
		}
	}

	// energy_added_kwh is always optional now (roadmap D7 territory extended by
	// RM33/design.md §D-Fields — never part of RequiredFieldsFor's domain).
	// Empty -> nil, no error. Non-empty -> parse and validate > 0 as before.
	var energy *float64
	if raw.EnergyAddedKWh != "" {
		v, parseErr := strconv.ParseFloat(raw.EnergyAddedKWh, 64)
		if parseErr != nil || v <= 0 {
			errs["energy_added_kwh"] = i18n.T(c.Request.Context(), i18n.KeyChargesErrorEnergyPositive)
		} else {
			energy = &v
		}
	}

	// price is always optional now (roadmap D7 — empty -> 0, no error).
	var price float64
	if raw.Price != "" {
		v, parseErr := strconv.ParseFloat(raw.Price, 64)
		if parseErr != nil || v < 0 {
			errs["price"] = i18n.T(c.Request.Context(), i18n.KeyChargesErrorPriceNonNegative)
		} else {
			price = v
		}
	}

	// D5: Currency is hardcoded COP — the form's disabled Currency input is for
	// display transparency only; a disabled input is not submitted, so reading
	// c.PostForm("currency") would return "". We always hand "COP" down the
	// charging.Writer port; the column's 'COP' default is now redundant from
	// the gateway's perspective but stays as DB defense-in-depth.
	currency := "COP"

	// location_kind is required (tier 3 made the DB column NOT NULL; the gateway
	// must enforce field-level feedback before calling the service). Valid values:
	// HOME, WORK, OTHER. Any other value (including empty string) is an error.
	// Unconditionally required — not gated by RequiredFieldsFor's map lookup
	// since it is unconditionally in both the IN_PROGRESS and DONE sets.
	var locationKindPtr *string
	if raw.LocationKind == "HOME" || raw.LocationKind == "WORK" || raw.LocationKind == "OTHER" {
		lk := raw.LocationKind // local copy — avoids any implicit alias
		locationKindPtr = &lk
	} else {
		errs["location_kind"] = i18n.T(c.Request.Context(), i18n.KeyChargesErrorLocationRequired)
	}

	// start_battery_pct stays UNCONDITIONALLY required — it has no
	// charging.Field constant (not in RequiredFieldsFor's domain), unchanged by
	// RM33 (design.md §D-Fields). The 0–100 bound check matches the
	// manual-charge-log spec (BETWEEN 0 AND 100 when present). No relative-order
	// check (start < end): a partial charge with prior driving can legitimately
	// start above the previous end; the user-asserted entry is the user's truth.
	startPctStr := strings.TrimSpace(raw.StartBatteryPct)
	var startPct int
	if startPctStr == "" {
		errs["start_battery_pct"] = i18n.T(c.Request.Context(), i18n.KeyChargesErrorBatteryPctRequired)
	} else {
		n, perr := strconv.Atoi(startPctStr)
		if perr != nil || n < 0 || n > 100 {
			errs["start_battery_pct"] = i18n.T(c.Request.Context(), i18n.KeyChargesErrorStartBatteryPctRange)
		} else {
			startPct = n
		}
	}

	// end_battery_pct's required-ness is now gated by RequiredFieldsFor(status)
	// (design.md §D-Fields) — required only when charging.FieldEndBatteryPct is
	// in the set for the submitted status (DONE), not unconditionally as before.
	endPctStr := strings.TrimSpace(raw.EndBatteryPct)
	var endPct *int
	if endPctStr == "" {
		if required[charging.FieldEndBatteryPct] {
			errs["end_battery_pct"] = i18n.T(c.Request.Context(), i18n.KeyChargesErrorBatteryPctRequired)
		}
	} else {
		n, perr := strconv.Atoi(endPctStr)
		if perr != nil || n < 0 || n > 100 {
			errs["end_battery_pct"] = i18n.T(c.Request.Context(), i18n.KeyChargesErrorEndBatteryPctRange)
		} else {
			endPct = &n
		}
	}

	// odometer_km is new (RM33) and always optional. Empty -> nil. Non-empty ->
	// parse as integer, validate >= 0.
	var odometerKm *int
	if raw.OdometerKm != "" {
		n, perr := strconv.Atoi(raw.OdometerKm)
		if perr != nil || n < 0 {
			errs["odometer_km"] = i18n.T(c.Request.Context(), i18n.KeyChargesErrorOdometerInvalid)
		} else {
			odometerKm = &n
		}
	}

	// ended_at's required-ness is gated by RequiredFieldsFor(status) (design.md
	// §D-Fields) — required only when charging.FieldEndedAt is in the set for
	// the submitted status (DONE). Checked here (raw presence only) so it
	// participates in the same len(errs)>0 gate as every other field; the
	// actual time.Parse of a present value happens below, alongside the
	// started_at/ended_at chronology check.
	endedAtStr := c.PostForm("ended_at")
	if endedAtStr == "" && required[charging.FieldEndedAt] {
		errs["ended_at"] = i18n.T(c.Request.Context(), i18n.KeyChargesErrorEndedAtRequired)
	}

	if len(errs) > 0 {
		return charging.Entry{}, raw, errs, false
	}

	entry := charging.Entry{
		AccountID:       uid,
		TeslaID:         teslaID,
		VIN:             vin,
		ChargedOn:       chargedOn,
		Status:          status,
		EnergyAddedKWh:  energy,
		Price:           price,
		Currency:        currency,
		LocationKind:    locationKindPtr,
		StartBatteryPct: &startPct,
		EndBatteryPct:   endPct,
		OdometerKm:      odometerKm,
	}

	// D1: started_at / ended_at STAY optional — clearing either still persists a
	// nil pointer (UTC parse only on a non-empty value). ended_at's presence
	// requirement (RM33) was already validated above (in the same len(errs)>0
	// gate as every other field) via endedAtStr; this block only parses it.
	if v := c.PostForm("started_at"); v != "" {
		if t, err := time.Parse("2006-01-02T15:04", v); err == nil {
			t = t.UTC()
			entry.StartedAt = &t
		}
	}
	if endedAtStr != "" {
		if t, err := time.Parse("2006-01-02T15:04", endedAtStr); err == nil {
			t = t.UTC()
			entry.EndedAt = &t
		}
	}
	// Session time sanity — mirrors the manual_charge_entries table-level CHECK
	// (ended_at >= started_at when both are given) so a swapped/typo'd pair is a
	// 422 field error, not a 500 from the DB constraint.
	if entry.StartedAt != nil && entry.EndedAt != nil && entry.EndedAt.Before(*entry.StartedAt) {
		errs["ended_at"] = i18n.T(c.Request.Context(), i18n.KeyChargesErrorEndBeforeStart)
		return entry, raw, errs, false
	}
	if v := c.PostForm("charging_type"); v == "AC" || v == "DC" {
		entry.ChargingType = &v
	}
	if v := c.PostForm("location_label"); v != "" {
		entry.LocationLabel = &v
	}
	if v := c.PostForm("notes"); v != "" {
		entry.Notes = &v
	}

	return entry, raw, nil, true
}

// vehicleOwned returns true if the (teslaID, vin) pair belongs to the user's
// registered vehicles list. Tenant-ownership check (design.md D4 rule 2).
func vehicleOwned(teslaID int64, vin string, vehicles []account.Vehicle) bool {
	for _, v := range vehicles {
		if v.TeslaID == teslaID && v.VIN == vin {
			return true
		}
	}
	return false
}

// generateCSRFToken returns a hex-encoded 16-byte random token for CSRF protection
// as specified in design.md D5 (16 bytes → 32 hex chars).
func generateCSRFToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
