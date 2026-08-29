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

// defaultChargeLimit is the default max number of entries returned for a list call.
const defaultChargeLimit = 100

// ChargePage renders the full Charge log page. It auth-guards, generates a CSRF
// token, builds page data, and renders the full page (initial load path).
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
	d := h.buildChargesPage(c.Request.Context(), uid, csrfToken, filterTeslaID, browserToday(c))
	render(c, http.StatusOK, pages.ChargePage(d))
}

// ChargesListFragment renders only the charges-list fragment (htmx refresh path).
func (h *Handler) ChargesListFragment(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	sess := sessions.Default(c)
	csrfToken, _ := sess.Get(csrfManualChargeKey).(string)
	// Scope the htmx-refreshed list to the selected vehicle context, same as the
	// full ChargePage render, so the list stays consistent with the switcher.
	filterTeslaID := int64(0)
	if sel, ok := h.resolveSelectedVehicle(c.Request.Context(), c, uid); ok {
		filterTeslaID = sel.TeslaID
	}
	d := h.buildChargesPage(c.Request.Context(), uid, csrfToken, filterTeslaID, browserToday(c))
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
	d := h.buildChargesPage(c.Request.Context(), uid, csrfToken, filterTeslaID, browserToday(c))
	renderFragment(c, http.StatusOK, pages.ChargePage(d), "charges-create-form", "charges-list")
}

// ChargeRowStatic renders the static row for one entry (used by cancel-edit path).
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
	render(c, http.StatusOK, fragments.ChargeRow(vm, csrfToken))
}

// ChargeRowEditFragment swaps the static row for an inline edit form.
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

	vm, ok2 := h.fetchEntryVM(c.Request.Context(), uid, id)
	if !ok2 {
		c.String(http.StatusNotFound, i18n.T(c.Request.Context(), i18n.KeyChargesErrorEntryNotFound))
		return
	}
	render(c, http.StatusOK, fragments.ChargeRowEdit(vm, csrfToken, nil))
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
		d := h.buildChargesPage(c.Request.Context(), uid, csrfToken, filterTeslaID, browserToday(c))
		// design.md §D-Values (roadmap D15): overwrite the fresh-load defaults
		// with what the user actually submitted, so a validation failure never
		// discards a value they typed — including charged_on/started_at/ended_at,
		// which have no home on ChargeFormValues (they already have a Default*
		// slot on ChargesPageData for the fresh-load case).
		d.DefaultChargedOn = c.PostForm("charged_on")
		d.DefaultStartedAt = c.PostForm("started_at")
		d.DefaultEndedAt = c.PostForm("ended_at")
		d.FormValues = raw
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
		d := h.buildChargesPage(c.Request.Context(), uid, csrfToken, filterTeslaID, browserToday(c))
		// design.md §D-Values (roadmap D15): same value-preservation treatment on
		// the 500 (writer-error) branch as the 422 (validation-error) branch.
		d.DefaultChargedOn = c.PostForm("charged_on")
		d.DefaultStartedAt = c.PostForm("started_at")
		d.DefaultEndedAt = c.PostForm("ended_at")
		d.FormValues = raw
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
	d := h.buildChargesPage(c.Request.Context(), uid, csrfToken, filterTeslaID, browserToday(c))
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

	entry, raw, validationErrors, ok2 := h.parseChargeForm(c, uid, vehicles)
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
		renderError(c, http.StatusUnprocessableEntity, fragments.ChargeRowEdit(vm, csrfToken, validationErrors))
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
		// the 422 (validation-error) branch above — design.md §D-Values.
		vm := chargeEntryVMFromRawValues(idStr, raw, h.vehicleLabelForSelected(c, uid, vehicles))
		vm.RawChargedOn = c.PostForm("charged_on")
		vm.RawStartedAt = c.PostForm("started_at")
		vm.RawEndedAt = c.PostForm("ended_at")
		renderError(c, http.StatusInternalServerError, fragments.ChargeRowEdit(vm, csrfToken, map[string]string{
			"_top": i18n.T(c.Request.Context(), i18n.KeyChargesErrorCouldNotSaveEntry),
		}))
		return
	}
	h.recalculateAfterChargeWrite(c.Request.Context(), uid, updated.TeslaID, updated.ChargedOn)
	if hadOld && !oldChargedOn.Equal(updated.ChargedOn) {
		h.recalculateAfterChargeWrite(c.Request.Context(), uid, updated.TeslaID, oldChargedOn)
	}
	vm := chargeEntryVMFromEntry(updated, vehicles)
	render(c, http.StatusOK, fragments.ChargeRow(vm, csrfToken))
}

// ChargeRowDelete handles DELETE /ui/charges/row/:id. Deletes the entry and
// returns an empty <tr> so htmx outerHTML swap removes the row.
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

	// Resolve the entry's ChargedOn (and TeslaID) BEFORE calling Delete — the
	// Delete port does not return the deleted entry, so this is the only
	// chance to learn which day needs recalculating (design.md D5). A lookup
	// miss just means there is no day to recalculate; the delete still
	// proceeds.
	entryTeslaID, entryChargedOn, hadEntry := h.fetchEntryTeslaIDAndChargedOn(c.Request.Context(), uid, id)

	if err := h.chargingWriter.Delete(c.Request.Context(), uid, id); err != nil {
		log.Printf("gateway: ChargeRowDelete writer error for account %s, id %s: %v", uid, id, err)
		renderError(c, http.StatusInternalServerError, fragments.ChargeRowError(id.String(), i18n.T(c.Request.Context(), i18n.KeyChargesErrorCouldNotDeleteEntry)))
		return
	}
	if hadEntry {
		h.recalculateAfterChargeWrite(c.Request.Context(), uid, entryTeslaID, entryChargedOn)
	}
	render(c, http.StatusOK, fragments.ChargeRowEmpty(id.String()))
}

// buildChargesPage is the gin-free helper that calls module ports and builds
// ChargesPageData. Decoupled from Gin so it can be called with fake port
// implementations in tests.
// today is the user's LOCAL calendar day (browserToday(c)), passed in rather than
// computed here so the helper stays gin-free and testable — the same shape as
// dashboardFor(ctx, uid, teslaID, browserToday(c)).
func (h *Handler) buildChargesPage(ctx context.Context, uid uuid.UUID, csrfToken string, teslaIDFilter int64, today time.Time) fragments.ChargesPageData {
	vehicles, err := h.acct.RegisteredVehicles(ctx, uid)
	if err != nil {
		log.Printf("gateway: RegisteredVehicles error for account %s: %v", uid, err)
		return fragments.ChargesPageData{
			CSRFToken: csrfToken,
			Error:     i18n.T(ctx, i18n.KeyChargesErrorCouldNotLoadVehicles),
		}
	}

	// The manual-records page is scoped to the selected vehicle context (the
	// sidebar switcher). When a filter is explicitly passed (non-zero) it wins;
	// otherwise we show all entries by account and pre-select no vehicle — the
	// caller (ChargePage) normally passes the session-selected TeslaID.
	var entries []charging.Entry
	if teslaIDFilter != 0 {
		entries, err = h.chargingReader.ListEntriesByVehicle(ctx, uid, teslaIDFilter, defaultChargeLimit)
	} else {
		entries, err = h.chargingReader.ListEntriesByAccount(ctx, uid, defaultChargeLimit)
	}
	var pageError string
	if err != nil {
		log.Printf("gateway: charging reader error for account %s: %v", uid, err)
		pageError = i18n.T(ctx, i18n.KeyChargesErrorCouldNotLoadEntries)
		entries = nil
	}

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

	costLabel := ""
	if v := e.CostPerKWh(); v != nil {
		costLabel = formatMoney(*v, e.Currency) + "/kWh"
	}
	batteryDelta := ""
	if d := e.BatteryDelta(); d != nil {
		if *d >= 0 {
			batteryDelta = fmt.Sprintf("+%d%%", *d)
		} else {
			batteryDelta = fmt.Sprintf("%d%%", *d)
		}
	}
	durationLabel := ""
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
