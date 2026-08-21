// service.go — Writer and Reader implementations for the charging module.
//
// This file is the ONLY place in the charging module where the generated
// chargingdb package and pgtype are referenced. All pgtype conversions happen
// here (or in mapping.go) at the DB boundary so that pgtype never leaks into
// public types, interfaces, or tests (ai/go-conventions.md §persistence, design D8).
//
// Structure:
//   - store interface — the narrow persistence seam (unexported, testable with a fake).
//   - dbStore — the production implementation wrapping *chargingdb.Queries.
//   - writerService — implements Writer (Create / Update / Delete).
//   - readerService — implements Reader (ListEntriesByVehicle / ListEntriesByAccount).
//   - rowToEntry — DB→domain mapping, confined to this file.
//   - newWriter / newReader — internal constructors called by the public NewWriter / NewReader.
//
// Compile-time interface assertions ensure the concrete types satisfy their ports.
package charging

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	chargingdb "github.com/cristianpena/magus-tesla-api/internal/charging/db"
)

// Compile-time assertions: writerService must satisfy Writer and readerService must
// satisfy Reader. These fail at compile time if any method is missing or has the
// wrong signature (design T3.6 / T4.6).
var _ Writer = (*writerService)(nil)
var _ Reader = (*readerService)(nil)

// defaultLimit is the server-side default applied when the caller passes limit <= 0.
// Shapes the Reader for the dashboard's access pattern: a page of 100 entries is
// fast (one index scan, small result set) without requiring callers to supply a limit.
const defaultLimit = 100

// --- store interface (the persistence seam) ---

// store is the narrow unexported interface the writer and reader services call through.
// Keeping it unexported and typed entirely in chargingdb terms means pgtype is
// confined to dbStore — it never surfaces in the service logic or public types.
// The seam also enables offline unit testing of higher-level logic by swapping in a
// fake store (no DATABASE_URL required).
type store interface {
	createEntry(ctx context.Context, params chargingdb.CreateEntryParams) (chargingdb.ManualChargeEntry, error)
	updateEntry(ctx context.Context, params chargingdb.UpdateEntryParams) (chargingdb.ManualChargeEntry, error)
	deleteEntry(ctx context.Context, params chargingdb.DeleteEntryParams) error
	listEntriesByVehicle(ctx context.Context, params chargingdb.ListEntriesByVehicleParams) ([]chargingdb.ManualChargeEntry, error)
	listEntriesByAccount(ctx context.Context, params chargingdb.ListEntriesByAccountParams) ([]chargingdb.ManualChargeEntry, error)
	listEntriesByVehicleBetween(ctx context.Context, params chargingdb.ListEntriesByVehicleBetweenParams) ([]chargingdb.ManualChargeEntry, error)
}

// --- dbStore — the production store (ONLY place chargingdb + pgtype are touched) ---

// dbStore is the production store: it delegates to the sqlc-generated *Queries and is
// the single place where pgtype conversions happen. No pgtype value ever leaves this
// struct's methods — they accept/return pure domain types via the store interface above.
type dbStore struct {
	q *chargingdb.Queries
}

func (d *dbStore) createEntry(ctx context.Context, params chargingdb.CreateEntryParams) (chargingdb.ManualChargeEntry, error) {
	return d.q.CreateEntry(ctx, params)
}

func (d *dbStore) updateEntry(ctx context.Context, params chargingdb.UpdateEntryParams) (chargingdb.ManualChargeEntry, error) {
	return d.q.UpdateEntry(ctx, params)
}

func (d *dbStore) deleteEntry(ctx context.Context, params chargingdb.DeleteEntryParams) error {
	return d.q.DeleteEntry(ctx, params)
}

func (d *dbStore) listEntriesByVehicle(ctx context.Context, params chargingdb.ListEntriesByVehicleParams) ([]chargingdb.ManualChargeEntry, error) {
	return d.q.ListEntriesByVehicle(ctx, params)
}

func (d *dbStore) listEntriesByAccount(ctx context.Context, params chargingdb.ListEntriesByAccountParams) ([]chargingdb.ManualChargeEntry, error) {
	return d.q.ListEntriesByAccount(ctx, params)
}

func (d *dbStore) listEntriesByVehicleBetween(ctx context.Context, params chargingdb.ListEntriesByVehicleBetweenParams) ([]chargingdb.ManualChargeEntry, error) {
	return d.q.ListEntriesByVehicleBetween(ctx, params)
}

// --- writerService — implements Writer ---

// writerService is the unexported concrete Writer implementation. It holds a store
// and maps between domain Entry values and the generated chargingdb param types,
// confining all pgtype usage to the mapping helpers below.
type writerService struct {
	store store
}

// Create persists a new charge entry and returns the stored Entry with server-assigned
// id, created_at, and updated_at. Maps required and optional Entry fields into
// CreateEntryParams at the DB boundary; the row result is mapped back via rowToEntry
// so the caller never sees pgtype (design D4, D8).
func (w *writerService) Create(ctx context.Context, e Entry) (Entry, error) {
	// location_kind is required — reject nil or empty before any pgtype conversion
	// or database call. The domain field stays *string (design D4) for gateway
	// compatibility; the required-ness is enforced here (design D3) and by the DB
	// NOT NULL constraint (design D1). Error prefix follows the "charging: ..."
	// convention used throughout this file.
	if e.LocationKind == nil || *e.LocationKind == "" {
		return Entry{}, errors.New("charging: location_kind is required")
	}

	// Build required NUMERIC params from float64 fields.
	energy, err := numericFromFloat64(e.EnergyAddedKWh)
	if err != nil {
		return Entry{}, fmt.Errorf("charging: encoding energy_added_kwh: %w", err)
	}
	price, err := numericFromFloat64(e.Price)
	if err != nil {
		return Entry{}, fmt.Errorf("charging: encoding price: %w", err)
	}

	params := chargingdb.CreateEntryParams{
		AccountID:      e.AccountID,
		TeslaID:        e.TeslaID,
		Vin:            e.VIN,
		ChargedOn:      dateFromTime(e.ChargedOn),
		EnergyAddedKwh: energy,
		Price:          price,
		Currency:       e.Currency,
		// Nullable fields — nil → invalid pgtype (SQL NULL); non-nil → valid.
		StartedAt:       timestamptzPtrToPg(e.StartedAt),
		EndedAt:         timestamptzPtrToPg(e.EndedAt),
		StartBatteryPct: intPtrToPgInt2(e.StartBatteryPct),
		EndBatteryPct:   intPtrToPgInt2(e.EndBatteryPct),
		ChargingType:    stringPtrToPgText(e.ChargingType),
		// location_kind is NOT NULL in the DB (required field, design D3); the
		// pointer is guaranteed non-nil by the validation above.
		LocationKind:  stringPtrToRequired(e.LocationKind),
		LocationLabel: stringPtrToPgText(e.LocationLabel),
		Notes:         stringPtrToPgText(e.Notes),
	}

	row, err := w.store.createEntry(ctx, params)
	if err != nil {
		return Entry{}, fmt.Errorf("charging: create entry: %w", err)
	}
	return rowToEntry(row)
}

// Update replaces the mutable fields of an existing entry scoped to the caller's
// account (WHERE id = @id AND account_id = @account_id). Returns the stored Entry.
// Immutable columns (id, account_id, tesla_id, vin, created_at) are never touched
// (design D4, T4.3).
func (w *writerService) Update(ctx context.Context, e Entry) (Entry, error) {
	// location_kind is required on Update (same constraint as Create — design D3).
	// Reject nil or empty before any pgtype conversion or database call.
	if e.LocationKind == nil || *e.LocationKind == "" {
		return Entry{}, errors.New("charging: location_kind is required")
	}

	energy, err := numericFromFloat64(e.EnergyAddedKWh)
	if err != nil {
		return Entry{}, fmt.Errorf("charging: encoding energy_added_kwh: %w", err)
	}
	price, err := numericFromFloat64(e.Price)
	if err != nil {
		return Entry{}, fmt.Errorf("charging: encoding price: %w", err)
	}

	params := chargingdb.UpdateEntryParams{
		ID:              e.ID,
		AccountID:       e.AccountID,
		ChargedOn:       dateFromTime(e.ChargedOn),
		EnergyAddedKwh:  energy,
		Price:           price,
		Currency:        e.Currency,
		StartedAt:       timestamptzPtrToPg(e.StartedAt),
		EndedAt:         timestamptzPtrToPg(e.EndedAt),
		StartBatteryPct: intPtrToPgInt2(e.StartBatteryPct),
		EndBatteryPct:   intPtrToPgInt2(e.EndBatteryPct),
		ChargingType:    stringPtrToPgText(e.ChargingType),
		// location_kind is NOT NULL in the DB (required field, design D3); the
		// pointer is guaranteed non-nil by the validation above.
		LocationKind:  stringPtrToRequired(e.LocationKind),
		LocationLabel: stringPtrToPgText(e.LocationLabel),
		Notes:         stringPtrToPgText(e.Notes),
	}

	row, err := w.store.updateEntry(ctx, params)
	if err != nil {
		return Entry{}, fmt.Errorf("charging: update entry: %w", err)
	}
	return rowToEntry(row)
}

// Delete removes the entry identified by id, scoped to the caller's accountID.
// The double-scope (id AND account_id) at the SQL level means a user cannot delete
// another tenant's entry even with a valid UUID (design D4).
func (w *writerService) Delete(ctx context.Context, accountID uuid.UUID, id uuid.UUID) error {
	params := chargingdb.DeleteEntryParams{
		ID:        id,
		AccountID: accountID,
	}
	if err := w.store.deleteEntry(ctx, params); err != nil {
		return fmt.Errorf("charging: delete entry: %w", err)
	}
	return nil
}

// --- readerService — implements Reader ---

// readerService is the unexported concrete Reader implementation. It holds a store
// and maps rows back to domain Entry values via rowToEntry (design D5).
type readerService struct {
	store store
}

// ListEntriesByVehicle returns entries for a specific vehicle within an account,
// ordered newest charged-day first (charged_on DESC). limit <= 0 uses the server
// default (100). Always returns a non-nil empty slice when no rows exist (design D5).
// Uses idx_manual_charge_entries_vehicle_time (account_id, tesla_id, charged_on DESC)
// — the ORDER BY is satisfied by the index, no sort step required (design D3).
func (r *readerService) ListEntriesByVehicle(ctx context.Context, accountID uuid.UUID, teslaID int64, limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = defaultLimit
	}

	params := chargingdb.ListEntriesByVehicleParams{
		AccountID:  accountID,
		TeslaID:    teslaID,
		LimitCount: int32(limit),
	}

	rows, err := r.store.listEntriesByVehicle(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("charging: list entries by vehicle: %w", err)
	}

	entries := make([]Entry, 0, len(rows))
	for _, row := range rows {
		e, err := rowToEntry(row)
		if err != nil {
			return nil, fmt.Errorf("charging: mapping entry row: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// ListEntriesByAccount returns all entries for the given account across all vehicles,
// ordered newest charged-day first (charged_on DESC). limit <= 0 uses the server
// default (100). Always returns a non-nil empty slice when no rows exist (design D5).
// Uses idx_manual_charge_entries_account_time (account_id, charged_on DESC) —
// the ORDER BY is satisfied by the index, no sort step required (design D3).
func (r *readerService) ListEntriesByAccount(ctx context.Context, accountID uuid.UUID, limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = defaultLimit
	}

	params := chargingdb.ListEntriesByAccountParams{
		AccountID:  accountID,
		LimitCount: int32(limit),
	}

	rows, err := r.store.listEntriesByAccount(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("charging: list entries by account: %w", err)
	}

	entries := make([]Entry, 0, len(rows))
	for _, row := range rows {
		e, err := rowToEntry(row)
		if err != nil {
			return nil, fmt.Errorf("charging: mapping entry row: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// ListEntriesByVehicleBetween returns entries for a specific vehicle within an
// account whose charged_on falls within [from, to], inclusive of both bounds
// (design D5). Ordered charged_on DESC, matching ListEntriesByVehicle (design D2).
// Always returns a non-nil empty slice when no rows exist (design D4). No limit
// parameter (design D1) — the [from, to] window itself bounds the result.
func (r *readerService) ListEntriesByVehicleBetween(ctx context.Context, accountID uuid.UUID, teslaID int64, from, to time.Time) ([]Entry, error) {
	params := chargingdb.ListEntriesByVehicleBetweenParams{
		AccountID: accountID,
		TeslaID:   teslaID,
		FromDate:  dateFromTime(from),
		ToDate:    dateFromTime(to),
	}

	rows, err := r.store.listEntriesByVehicleBetween(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("charging: list entries by vehicle between: %w", err)
	}

	entries := make([]Entry, 0, len(rows))
	for _, row := range rows {
		e, err := rowToEntry(row)
		if err != nil {
			return nil, fmt.Errorf("charging: mapping entry row: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// --- constructors ---

// newWriter constructs a writerService backed by the given pool. Called by NewWriter
// in charging.go (the public constructor). dbStore is the only place
// chargingdb.New is called, enforcing the module boundary.
func newWriter(pool *pgxpool.Pool) Writer {
	return &writerService{store: &dbStore{q: chargingdb.New(pool)}}
}

// newReader constructs a readerService backed by the given pool. Called by NewReader
// in charging.go (the public constructor).
func newReader(pool *pgxpool.Pool) Reader {
	return &readerService{store: &dbStore{q: chargingdb.New(pool)}}
}

// --- DB→domain mapping ---

// rowToEntry converts a generated chargingdb.ManualChargeEntry row into the domain
// Entry type. This is the single DB→domain mapping boundary: all pgtype conversions
// are confined here so pgtype never appears in public types, method signatures, or
// tests (ai/go-conventions.md §persistence, design D8).
//
// Mapping rules:
//   - ID, AccountID, TeslaID, Vin, Currency: direct (non-nullable, value-compatible).
//   - ChargedOn: pgtype.Date → time.Time via .Time (DATE column, midnight UTC).
//   - CreatedAt, UpdatedAt: pgtype.Timestamptz → time.Time via .Time (required, non-null).
//   - StartedAt, EndedAt: pgtype.Timestamptz → *time.Time (nullable TIMESTAMPTZ).
//   - StartBatteryPct, EndBatteryPct: pgtype.Int2 → *int (nullable SMALLINT).
//   - ChargingType, LocationKind, LocationLabel, Notes: pgtype.Text → *string (nullable TEXT).
//   - EnergyAddedKwh, Price: pgtype.Numeric → float64 via Float64Value() (design D9).
func rowToEntry(r chargingdb.ManualChargeEntry) (Entry, error) {
	// EnergyAddedKwh: NUMERIC → float64 (required column; Float64Value returns a
	// pgtype.Float8 wrapper — use its Float64 field after error check, design D9).
	energyF8, err := r.EnergyAddedKwh.Float64Value()
	if err != nil {
		return Entry{}, fmt.Errorf("charging: reading energy_added_kwh: %w", err)
	}

	// Price: same NUMERIC → float64 path (design D9).
	priceF8, err := r.Price.Float64Value()
	if err != nil {
		return Entry{}, fmt.Errorf("charging: reading price: %w", err)
	}

	return Entry{
		ID:             r.ID,
		AccountID:      r.AccountID,
		TeslaID:        r.TeslaID,
		VIN:            r.Vin,
		ChargedOn:      r.ChargedOn.Time, // pgtype.Date.Time → time.Time
		EnergyAddedKWh: energyF8.Float64,
		Price:          priceF8.Float64,
		Currency:       r.Currency,
		// Nullable TIMESTAMPTZ → *time.Time
		StartedAt: pgTimestamptzToPtr(r.StartedAt),
		EndedAt:   pgTimestamptzToPtr(r.EndedAt),
		// Nullable SMALLINT → *int
		StartBatteryPct: pgInt2ToIntPtr(r.StartBatteryPct),
		EndBatteryPct:   pgInt2ToIntPtr(r.EndBatteryPct),
		// Nullable TEXT → *string
		ChargingType: pgTextToPtr(r.ChargingType),
		// location_kind is NOT NULL in the DB; surfaced as *string for a uniform
		// domain surface (always non-nil on the read path).
		LocationKind:  requiredToStringPtr(r.LocationKind),
		LocationLabel: pgTextToPtr(r.LocationLabel),
		Notes:         pgTextToPtr(r.Notes),
		// Required TIMESTAMPTZ → time.Time
		CreatedAt: r.CreatedAt.Time,
		UpdatedAt: r.UpdatedAt.Time,
	}, nil
}

// --- domain→DB helpers (write path, Entry → pgtype) ---

// numericFromFloat64 converts a float64 to a valid pgtype.Numeric by scanning its
// decimal string representation. This is the correct pgx/v5 path for writing a float64
// into a NUMERIC column: format the float as a decimal string, then Scan it in.
// An error here means the float itself is NaN or ±Inf (which the DB would reject too).
func numericFromFloat64(f float64) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if err := n.Scan(strconv.FormatFloat(f, 'f', -1, 64)); err != nil {
		return pgtype.Numeric{}, err
	}
	return n, nil
}

// dateFromTime converts a time.Time to a valid pgtype.Date for a DATE column.
// The Date stores only the date part; the time of day is ignored by Postgres.
func dateFromTime(t time.Time) pgtype.Date {
	return pgtype.Date{Time: t, Valid: true}
}

// timestamptzPtrToPg maps a *time.Time to a nullable pgtype.Timestamptz.
// nil → invalid (SQL NULL); non-nil → valid with the pointed-to time.
func timestamptzPtrToPg(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{Valid: false}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

// intPtrToPgInt2 maps a *int to a nullable pgtype.Int2 (SMALLINT).
// nil → invalid (SQL NULL); non-nil → valid Int16.
func intPtrToPgInt2(v *int) pgtype.Int2 {
	if v == nil {
		return pgtype.Int2{Valid: false}
	}
	return pgtype.Int2{Int16: int16(*v), Valid: true}
}

// stringPtrToPgText maps a *string to a nullable pgtype.Text.
// nil → invalid (SQL NULL); non-nil → valid with the concrete string value.
func stringPtrToPgText(v *string) pgtype.Text {
	if v == nil {
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: *v, Valid: true}
}

// stringPtrToRequired maps a *string to a plain string for a NOT NULL column.
// Callers must have validated the pointer is non-nil (e.g. location_kind is a
// required field); a nil pointer defensively maps to "", which the column's
// CHECK constraint would then reject at the database.
func stringPtrToRequired(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// --- DB→domain helpers (read path, pgtype → domain) ---

// pgTimestamptzToPtr converts a nullable pgtype.Timestamptz to *time.Time.
// !Valid → nil (SQL NULL); Valid → pointer to the timestamp's time value.
func pgTimestamptzToPtr(v pgtype.Timestamptz) *time.Time {
	if !v.Valid {
		return nil
	}
	t := v.Time
	return &t
}

// pgInt2ToIntPtr converts a nullable pgtype.Int2 to *int.
// !Valid → nil (SQL NULL); Valid → pointer to int(v.Int16).
func pgInt2ToIntPtr(v pgtype.Int2) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int16)
	return &n
}

// pgTextToPtr converts a nullable pgtype.Text to *string.
// !Valid → nil (SQL NULL); Valid → pointer to v.String.
func pgTextToPtr(v pgtype.Text) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

// requiredToStringPtr maps a NOT NULL string column to *string, keeping the
// domain surface uniform with the other nullable text fields. The value is
// always present on the read path, so the returned pointer is never nil.
func requiredToStringPtr(v string) *string {
	return &v
}
