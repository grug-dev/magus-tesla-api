// charges.go contains the handlers for the manual charge log page and its htmx
// fragment routes (create/edit/delete). All write paths are auth-guarded, CSRF-
// protected, and tenant-ownership-validated before calling manualcharge.Writer.
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
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/fragments"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/pages"
	"github.com/cristianpena/magus-tesla-api/internal/manualcharge"
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
		c.String(http.StatusInternalServerError, "could not start charge log")
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
	d := h.buildChargesPage(c.Request.Context(), uid, csrfToken, filterTeslaID)
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
	d := h.buildChargesPage(c.Request.Context(), uid, csrfToken, filterTeslaID)
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
		c.String(http.StatusInternalServerError, "could not refresh charge log")
		return
	}
	sess := sessions.Default(c)
	sess.Set(csrfManualChargeKey, csrfToken)
	_ = sess.Save()

	filterTeslaID := int64(0)
	if sel, ok := h.resolveSelectedVehicle(c.Request.Context(), c, uid); ok {
		filterTeslaID = sel.TeslaID
	}
	d := h.buildChargesPage(c.Request.Context(), uid, csrfToken, filterTeslaID)
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
		c.String(http.StatusBadRequest, "invalid id")
		return
	}
	sess := sessions.Default(c)
	csrfToken, _ := sess.Get(csrfManualChargeKey).(string)

	vm, ok2 := h.fetchEntryVM(c.Request.Context(), uid, id)
	if !ok2 {
		c.String(http.StatusNotFound, "entry not found")
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
		c.String(http.StatusBadRequest, "invalid id")
		return
	}
	sess := sessions.Default(c)
	csrfToken, _ := sess.Get(csrfManualChargeKey).(string)

	vm, ok2 := h.fetchEntryVM(c.Request.Context(), uid, id)
	if !ok2 {
		c.String(http.StatusNotFound, "entry not found")
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
		c.String(http.StatusInternalServerError, "could not validate vehicle ownership")
		return
	}
	sess := sessions.Default(c)
	csrfToken, _ := sess.Get(csrfManualChargeKey).(string)

	entry, validationErrors, ok2 := h.parseChargeForm(c, uid, vehicles)
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
		d := h.buildChargesPage(c.Request.Context(), uid, csrfToken, filterTeslaID)
		render(c, http.StatusUnprocessableEntity, fragments.ChargeCreateForm(d, validationErrors))
		return
	}

	created, err := h.manualChargeWriter.Create(c.Request.Context(), entry)
	if err != nil {
		log.Printf("gateway: ChargeCreate writer error for account %s: %v", uid, err)
		// Keep the picker on the vehicle the user submitted.
		filterTeslaID := entry.TeslaID
		if filterTeslaID == 0 {
			if sel, ok := h.resolveSelectedVehicle(c.Request.Context(), c, uid); ok {
				filterTeslaID = sel.TeslaID
			}
		}
		d := h.buildChargesPage(c.Request.Context(), uid, csrfToken, filterTeslaID)
		render(c, http.StatusInternalServerError, fragments.ChargeCreateForm(d, map[string]string{
			"_top": "Could not save your entry — please try again.",
		}))
		return
	}

	_ = created
	// Reset form defaults to the submitted vehicle so the user can log another
	// charge for the same car without re-picking.
	filterTeslaID := entry.TeslaID
	if filterTeslaID == 0 {
		if sel, ok := h.resolveSelectedVehicle(c.Request.Context(), c, uid); ok {
			filterTeslaID = sel.TeslaID
		}
	}
	d := h.buildChargesPage(c.Request.Context(), uid, csrfToken, filterTeslaID)
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
		c.String(http.StatusBadRequest, "invalid id")
		return
	}

	vehicles, err := h.acct.RegisteredVehicles(c.Request.Context(), uid)
	if err != nil {
		c.String(http.StatusInternalServerError, "could not validate vehicle ownership")
		return
	}
	sess := sessions.Default(c)
	csrfToken, _ := sess.Get(csrfManualChargeKey).(string)

	entry, validationErrors, ok2 := h.parseChargeForm(c, uid, vehicles)
	if !ok2 {
		if validationErrors == nil {
			return
		}
		vm := chargeEntryVMFromEntry(entry, vehicles)
		render(c, http.StatusUnprocessableEntity, fragments.ChargeRowEdit(vm, csrfToken, validationErrors))
		return
	}
	entry.ID = id
	entry.AccountID = uid

	updated, err := h.manualChargeWriter.Update(c.Request.Context(), entry)
	if err != nil {
		log.Printf("gateway: ChargeRowUpdate writer error for account %s, id %s: %v", uid, id, err)
		vm := chargeEntryVMFromEntry(entry, vehicles)
		render(c, http.StatusInternalServerError, fragments.ChargeRowEdit(vm, csrfToken, map[string]string{
			"_top": "Could not save your entry — please try again.",
		}))
		return
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
		c.String(http.StatusBadRequest, "invalid id")
		return
	}

	if err := h.manualChargeWriter.Delete(c.Request.Context(), uid, id); err != nil {
		log.Printf("gateway: ChargeRowDelete writer error for account %s, id %s: %v", uid, id, err)
		render(c, http.StatusInternalServerError, fragments.ChargeRowError(id.String(), "Could not delete entry — please try again."))
		return
	}
	render(c, http.StatusOK, fragments.ChargeRowEmpty(id.String()))
}

// buildChargesPage is the gin-free helper that calls module ports and builds
// ChargesPageData. Decoupled from Gin so it can be called with fake port
// implementations in tests.
func (h *Handler) buildChargesPage(ctx context.Context, uid uuid.UUID, csrfToken string, teslaIDFilter int64) fragments.ChargesPageData {
	vehicles, err := h.acct.RegisteredVehicles(ctx, uid)
	if err != nil {
		log.Printf("gateway: RegisteredVehicles error for account %s: %v", uid, err)
		return fragments.ChargesPageData{
			CSRFToken: csrfToken,
			Error:     "Could not load your vehicles — please try again.",
		}
	}

	// The manual-records page is scoped to the selected vehicle context (the
	// sidebar switcher). When a filter is explicitly passed (non-zero) it wins;
	// otherwise we show all entries by account and pre-select no vehicle — the
	// caller (ChargePage) normally passes the session-selected TeslaID.
	var entries []manualcharge.Entry
	if teslaIDFilter != 0 {
		entries, err = h.manualChargeReader.ListEntriesByVehicle(ctx, uid, teslaIDFilter, defaultChargeLimit)
	} else {
		entries, err = h.manualChargeReader.ListEntriesByAccount(ctx, uid, defaultChargeLimit)
	}
	var pageError string
	if err != nil {
		log.Printf("gateway: manualcharge reader error for account %s: %v", uid, err)
		pageError = "Could not load your entries — please try again."
		entries = nil
	}

	vms := make([]fragments.ChargeEntryVM, 0, len(entries))
	for _, e := range entries {
		vms = append(vms, chargeEntryVMFromEntry(e, vehicles))
	}

	opts, singleVehicle := buildVehicleOptions(vehicles)

	// Re-mark the picker Selected flag to the resolved context vehicle (the
	// filter) instead of buildVehicleOptions' default (first OWNER). With the
	// sidebar switcher driving context, the create form must default to the
	// vehicle the user currently has selected, not the auto-pick.
	if teslaIDFilter != 0 && !singleVehicle {
		for i := range opts {
			opts[i].Selected = opts[i].TeslaID == teslaIDFilter
		}
	}

	// D1: default started_at / ended_at to today's calendar day (UTC midnight) so
	// the optional date fields on the create form render pre-populated. The
	// template emits them verbatim into the input's value attribute — no time
	// math in markup. The fields stay OPTIONAL: parseChargeForm still accepts a
	// cleared field and persists nil StartedAt / EndedAt.
	todayDate := time.Now().UTC().Format("2006-01-02") + "T00:00"

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
					suggestion = fmt.Sprintf("Latest: %d%%", s.BatteryLevelPct)
					break
				}
			}
		}
	}

	return fragments.ChargesPageData{
		Entries:                  vms,
		VehicleOptions:           opts,
		ActiveTeslaID:            teslaIDFilter,
		CSRFToken:                csrfToken,
		EmptyState:               len(vms) == 0 && pageError == "",
		Error:                    pageError,
		SingleVehicle:            singleVehicle,
		DefaultStartedAt:         todayDate,
		DefaultEndedAt:           todayDate,
		StartBatteryPctSuggestion: suggestion,
	}
}

// fetchEntryVM fetches a single entry by listing all account entries and finding
// the one matching id (no GetEntry on the port — design decision D6). Returns
// false if not found.
func (h *Handler) fetchEntryVM(ctx context.Context, uid uuid.UUID, id uuid.UUID) (fragments.ChargeEntryVM, bool) {
	entries, err := h.manualChargeReader.ListEntriesByAccount(ctx, uid, 0)
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

// checkCSRF reads the submitted csrf_token (from form body or hx-csrf-token
// header), compares it to the manualcharge session value via constant-time
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
		c.String(http.StatusForbidden, "invalid csrf token")
		return false
	}
	return true
}

// chargeEntryVMFromEntry maps a manualcharge.Entry and the account's registered
// vehicle list to a ChargeEntryVM. Pre-computes all derived display strings so
// templates do no arithmetic (design.md D6).
func chargeEntryVMFromEntry(e manualcharge.Entry, vehicles []account.Vehicle) fragments.ChargeEntryVM {
	label := vehicleLabelFor(e.TeslaID, vehicles)

	costLabel := ""
	if v := e.CostPerKWh(); v != nil {
		costLabel = fmt.Sprintf("%.2f %s/kWh", *v, e.Currency)
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

	return fragments.ChargeEntryVM{
		ID:              e.ID.String(),
		VehicleLabel:    label,
		ChargedOnLabel:  e.ChargedOn.Format("Mon Jan 2, 2006"),
		EnergyKWh:       fmt.Sprintf("%.2f kWh", e.EnergyAddedKWh),
		PriceLabel:      fmt.Sprintf("%.2f %s", e.Price, e.Currency),
		Currency:        e.Currency,
		CostPerKWhLabel: costLabel,
		BatteryDelta:    batteryDelta,
		DurationLabel:   durationLabel,
		ChargingType:    chargingType,
		LocationKind:    locationKind,
		LocationLabel:   locationLabelStr,
		Notes:           notes,
		RawChargedOn:    e.ChargedOn.Format("2006-01-02"),
		RawEnergyKWh:    strconv.FormatFloat(e.EnergyAddedKWh, 'f', 2, 64),
		RawPrice:        strconv.FormatFloat(e.Price, 'f', 2, 64),
		RawStartedAt:    rawStartedAt,
		RawEndedAt:      rawEndedAt,
		RawStartBatteryPct: rawStartPct,
		RawEndBatteryPct:   rawEndPct,
		TeslaID:         e.TeslaID,
		VIN:             e.VIN,
		VehicleValue:    fmt.Sprintf("%d:%s", e.TeslaID, e.VIN),
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

// buildVehicleOptions converts registered vehicles to form picker options and applies
// the RD4 auto-select rule. It returns the options slice and a singleVehicle flag.
//
// Auto-select rule (RD4):
//  1. Exactly one vehicle → select it; singleVehicle = true (template renders disabled+hidden).
//  2. Multiple vehicles: find first where AccessType == "OWNER" → select it.
//  3. Multiple vehicles, no OWNER → select the first.
//
// Exactly one option has Selected = true when len > 0. Templates must not
// inspect AccessType — the Selected flag is the only presentation signal.
func buildVehicleOptions(vehicles []account.Vehicle) ([]fragments.VehicleOptionVM, bool) {
	if len(vehicles) == 0 {
		return nil, false
	}

	opts := make([]fragments.VehicleOptionVM, 0, len(vehicles))
	for _, v := range vehicles {
		opts = append(opts, fragments.VehicleOptionVM{
			TeslaID:     v.TeslaID,
			VIN:         v.VIN,
			DisplayName: v.DisplayName,
			Value:       fmt.Sprintf("%d:%s", v.TeslaID, v.VIN),
		})
	}

	if len(opts) == 1 {
		opts[0].Selected = true
		return opts, true
	}

	// Multiple vehicles: find the first OWNER (nil-safe pointer deref).
	selectedIdx := 0
	for i, v := range vehicles {
		if v.AccessType != nil && *v.AccessType == "OWNER" {
			selectedIdx = i
			break
		}
	}
	opts[selectedIdx].Selected = true
	return opts, false
}

// parseChargeForm parses and validates the charge form from a Gin context.
// Returns the entry, any validation errors, and whether parsing succeeded.
// On a 403-level ownership failure it writes the response itself and returns
// validationErrors=nil, ok=false so the caller knows to stop.
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
//     manualcharge.Entry.Currency column stays a column the gateway always
//     sends COP down; no service change.
//   - D6: start_battery_pct and end_battery_pct are REQUIRED (empty or non-int /
//     out-of-range -> validation error). The service contract stays nullable
//     (manual-charge-log spec unchanged); the gateway just always sends a non-nil
//     pair. Energy/Price/ChargedOn/LocationKind validation is unchanged.
//   - D1: started_at / ended_at stay OPTIONAL — clearing either still persists
//     nil StartedAt / EndedAt. The today's-date DEFAULT is a UI concern
//     (DefaultStartedAt on ChargesPageData); parseChargeForm still accepts an
//     empty (cleared) field.
//   - D7: energy_added_kwh accepts 3-decimal precision (UI step=0.001). No
//     server-side rounding — strconv.ParseFloat already accepts any precision.
//     The energy <= 0 rejection stays (positive only).
func (h *Handler) parseChargeForm(c *gin.Context, uid uuid.UUID, vehicles []account.Vehicle) (manualcharge.Entry, map[string]string, bool) {
	errs := make(map[string]string)

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
			c.String(http.StatusForbidden, "vehicle not owned by this account")
			return manualcharge.Entry{}, nil, false
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
		errs["_top"] = "Please select a vehicle."
	}

	chargedOnStr := c.PostForm("charged_on")
	var chargedOn time.Time
	if chargedOnStr == "" {
		errs["charged_on"] = "Date is required."
	} else {
		var parseErr error
		chargedOn, parseErr = time.Parse("2006-01-02", chargedOnStr)
		if parseErr != nil {
			errs["charged_on"] = "Invalid date format."
		}
	}

	energyStr := c.PostForm("energy_added_kwh")
	var energy float64
	if energyStr == "" {
		errs["energy_added_kwh"] = "Energy added is required."
	} else {
		var parseErr error
		energy, parseErr = strconv.ParseFloat(energyStr, 64)
		if parseErr != nil || energy <= 0 {
			errs["energy_added_kwh"] = "Energy must be a positive number."
		}
	}

	priceStr := c.PostForm("price")
	var price float64
	if priceStr == "" {
		errs["price"] = "Price is required."
	} else {
		var parseErr error
		price, parseErr = strconv.ParseFloat(priceStr, 64)
		if parseErr != nil || price < 0 {
			errs["price"] = "Price must be a non-negative number."
		}
	}

	// D5: Currency is hardcoded COP — the form's disabled Currency input is for
	// display transparency only; a disabled input is not submitted, so reading
	// c.PostForm("currency") would return "". We always hand "COP" down the
	// manualcharge.Writer port; the column's 'COP' default is now redundant from
	// the gateway's perspective but stays as DB defense-in-depth.
	currency := "COP"

	// location_kind is required (tier 3 made the DB column NOT NULL; the gateway
	// must enforce field-level feedback before calling the service). Valid values:
	// HOME, WORK, OTHER. Any other value (including empty string) is an error.
	locationKindVal := c.PostForm("location_kind")
	var locationKindPtr *string
	if locationKindVal == "HOME" || locationKindVal == "WORK" || locationKindVal == "OTHER" {
		lk := locationKindVal // local copy — avoids any implicit alias
		locationKindPtr = &lk
	} else {
		errs["location_kind"] = "Location is required."
	}

	// D6: start_battery_pct + end_battery_pct are REQUIRED. The 0–100 bound check
	// matches the manual-charge-log spec (BETWEEN 0 AND 100 when present); the
	// gateway applies it unconditionally now (the service still accepts a nullable
	// pair — the gateway just always sends a non-nil pair). No relative-order
	// check (start < end): a partial charge with prior driving can legitimately
	// start above the previous end; the user-asserted entry is the user's truth.
	startPctStr := strings.TrimSpace(c.PostForm("start_battery_pct"))
	var startPct int
	if startPctStr == "" {
		errs["start_battery_pct"] = "Battery percentage is required."
	} else {
		n, perr := strconv.Atoi(startPctStr)
		if perr != nil || n < 0 || n > 100 {
			errs["start_battery_pct"] = "Start battery percentage must be an integer between 0 and 100."
		} else {
			startPct = n
		}
	}

	endPctStr := strings.TrimSpace(c.PostForm("end_battery_pct"))
	var endPct int
	if endPctStr == "" {
		errs["end_battery_pct"] = "Battery percentage is required."
	} else {
		n, perr := strconv.Atoi(endPctStr)
		if perr != nil || n < 0 || n > 100 {
			errs["end_battery_pct"] = "End battery percentage must be an integer between 0 and 100."
		} else {
			endPct = n
		}
	}

	if len(errs) > 0 {
		return manualcharge.Entry{}, errs, false
	}

	entry := manualcharge.Entry{
		AccountID:        uid,
		TeslaID:          teslaID,
		VIN:              vin,
		ChargedOn:        chargedOn,
		EnergyAddedKWh:   energy,
		Price:            price,
		Currency:         currency,
		LocationKind:     locationKindPtr,
		StartBatteryPct:  &startPct,
		EndBatteryPct:    &endPct,
	}

	// D1: started_at / ended_at STAY optional — clearing either still persists a
	// nil pointer (UTC parse only on a non-empty value).
	if v := c.PostForm("started_at"); v != "" {
		if t, err := time.Parse("2006-01-02T15:04", v); err == nil {
			t = t.UTC()
			entry.StartedAt = &t
		}
	}
	if v := c.PostForm("ended_at"); v != "" {
		if t, err := time.Parse("2006-01-02T15:04", v); err == nil {
			t = t.UTC()
			entry.EndedAt = &t
		}
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

	return entry, nil, true
}

// parseVehicleValue splits a combined "{teslaID}:{vin}" form value.
func parseVehicleValue(v string) (int64, string, error) {
	idx := strings.Index(v, ":")
	if idx < 1 {
		return 0, "", fmt.Errorf("invalid vehicle value: %q", v)
	}
	id, err := strconv.ParseInt(v[:idx], 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("invalid tesla id in vehicle value: %w", err)
	}
	vin := v[idx+1:]
	if vin == "" {
		return 0, "", fmt.Errorf("empty vin in vehicle value: %q", v)
	}
	return id, vin, nil
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
