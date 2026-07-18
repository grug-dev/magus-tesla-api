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

	d := h.buildChargesPage(c.Request.Context(), uid, csrfToken, 0)
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
	d := h.buildChargesPage(c.Request.Context(), uid, csrfToken, 0)
	renderFragment(c, http.StatusOK, pages.ChargePage(d), "charges-list")
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
		d := h.buildChargesPage(c.Request.Context(), uid, csrfToken, 0)
		render(c, http.StatusUnprocessableEntity, fragments.ChargeCreateForm(d, validationErrors))
		return
	}

	created, err := h.manualChargeWriter.Create(c.Request.Context(), entry)
	if err != nil {
		log.Printf("gateway: ChargeCreate writer error for account %s: %v", uid, err)
		d := h.buildChargesPage(c.Request.Context(), uid, csrfToken, 0)
		render(c, http.StatusInternalServerError, fragments.ChargeCreateForm(d, map[string]string{
			"_top": "Could not save your entry — please try again.",
		}))
		return
	}

	_ = created
	d := h.buildChargesPage(c.Request.Context(), uid, csrfToken, 0)
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

	opts := buildVehicleOptions(vehicles)

	return fragments.ChargesPageData{
		Entries:        vms,
		VehicleOptions: opts,
		ActiveTeslaID:  teslaIDFilter,
		CSRFToken:      csrfToken,
		EmptyState:     len(vms) == 0 && pageError == "",
		Error:          pageError,
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
// header), compares it to the session value via constant-time compare, and
// writes 403 + error fragment on mismatch. Returns true if CSRF is valid.
func (h *Handler) checkCSRF(c *gin.Context) bool {
	sess := sessions.Default(c)
	want, _ := sess.Get(csrfManualChargeKey).(string)
	got := c.PostForm("csrf_token")
	if got == "" {
		got = c.GetHeader("X-CSRF-Token")
	}
	if subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
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

// buildVehicleOptions converts registered vehicles to form picker options.
func buildVehicleOptions(vehicles []account.Vehicle) []fragments.VehicleOptionVM {
	opts := make([]fragments.VehicleOptionVM, 0, len(vehicles))
	for _, v := range vehicles {
		opts = append(opts, fragments.VehicleOptionVM{
			TeslaID:     v.TeslaID,
			VIN:         v.VIN,
			DisplayName: v.DisplayName,
			Value:       fmt.Sprintf("%d:%s", v.TeslaID, v.VIN),
		})
	}
	return opts
}

// parseChargeForm parses and validates the charge form from a Gin context.
// Returns the entry, any validation errors, and whether parsing succeeded.
// On a 403-level ownership failure it writes the response itself and returns
// validationErrors=nil, ok=false so the caller knows to stop.
func (h *Handler) parseChargeForm(c *gin.Context, uid uuid.UUID, vehicles []account.Vehicle) (manualcharge.Entry, map[string]string, bool) {
	errs := make(map[string]string)

	vehicleVal := c.PostForm("vehicle")
	teslaID, vin, vErr := parseVehicleValue(vehicleVal)
	if vErr != nil {
		errs["vehicle"] = "Please select a vehicle."
	} else {
		if !vehicleOwned(teslaID, vin, vehicles) {
			c.String(http.StatusForbidden, "vehicle not owned by this account")
			return manualcharge.Entry{}, nil, false
		}
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

	currency := strings.TrimSpace(c.PostForm("currency"))
	if currency == "" {
		errs["currency"] = "Currency is required."
	}

	if len(errs) > 0 {
		return manualcharge.Entry{}, errs, false
	}

	entry := manualcharge.Entry{
		AccountID:      uid,
		TeslaID:        teslaID,
		VIN:            vin,
		ChargedOn:      chargedOn,
		EnergyAddedKWh: energy,
		Price:          price,
		Currency:       currency,
	}

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
	if v := c.PostForm("start_battery_pct"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 100 {
			entry.StartBatteryPct = &n
		}
	}
	if v := c.PostForm("end_battery_pct"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 100 {
			entry.EndBatteryPct = &n
		}
	}
	if v := c.PostForm("charging_type"); v == "AC" || v == "DC" {
		entry.ChargingType = &v
	}
	if v := c.PostForm("location_kind"); v == "HOME" || v == "WORK" || v == "OTHER" {
		entry.LocationKind = &v
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
