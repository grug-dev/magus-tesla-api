-- Queries for the manualcharge module. sqlc generates package `manualchargedb` from
-- these against the schema in migrations/. No other module may import manualchargedb
-- (module-scoped DB access — ai/architecture.md §2, ai/go-conventions.md §persistence).
-- Unlike telemetry's append-only tables, manual_charge_entries is mutable: all five
-- operations (create, update, delete, list-by-vehicle, list-by-account) are live CRUD.

-- name: CreateEntry :one
-- Insert a new user-asserted charge entry. All required fields are non-nullable params;
-- optional fields use nullable params (sqlc maps them to pgtype nullable types via the
-- schema column types). RETURNING * hands back the server-assigned id, created_at, and
-- updated_at so the gateway can display the stored entry without a second round-trip.
--
-- status, odometer_km: bound as supplied by the caller (RM33 / MAG-18).
--
-- energy_source is COMPUTED IN GO, never accepted from the caller as a stored value's
-- true provenance -- the same shape VerifySuperchargerSession's @battery_pct_source already
-- uses for charging.supercharger_sessions' human-write channel. service.go computes USER/ESTIMATED
-- before binding this param; the query itself has no way to tell the two apart.
INSERT INTO charging.manual_charge_entries (
    account_id,
    tesla_id,
    vin,
    charged_on,
    energy_added_kwh,
    price,
    currency,
    started_at,
    ended_at,
    start_battery_pct,
    end_battery_pct,
    charging_type,
    location_kind,
    location_label,
    notes,
    status,
    energy_source,
    odometer_km
) VALUES (
    @account_id,
    @tesla_id,
    @vin,
    @charged_on,
    @energy_added_kwh,
    @price,
    @currency,
    @started_at,
    @ended_at,
    @start_battery_pct,
    @end_battery_pct,
    @charging_type,
    @location_kind,
    @location_label,
    @notes,
    @status,
    @energy_source,
    @odometer_km
)
RETURNING *;

-- name: UpdateEntry :one
-- Update mutable fields of an existing charge entry. The WHERE clause scopes to
-- (id, account_id) so a user cannot update another tenant's entry even with a valid
-- UUID — cross-tenant mutation is blocked at the SQL level (design D4).
-- Immutable columns (id, account_id, tesla_id, vin, created_at) are never touched.
-- updated_at is refreshed to now() on every successful update.
--
-- status, odometer_km: bound as supplied by the caller (RM33 / MAG-18).
--
-- energy_source is COMPUTED IN GO, never accepted from the caller as a stored value's
-- true provenance -- same shape as CreateEntry's @energy_source above, and the same
-- precedent VerifySuperchargerSession's @battery_pct_source sets for charging.supercharger_sessions.
UPDATE charging.manual_charge_entries
SET
    charged_on        = @charged_on,
    energy_added_kwh  = @energy_added_kwh,
    price             = @price,
    currency          = @currency,
    started_at        = @started_at,
    ended_at          = @ended_at,
    start_battery_pct = @start_battery_pct,
    end_battery_pct   = @end_battery_pct,
    charging_type     = @charging_type,
    location_kind     = @location_kind,
    location_label    = @location_label,
    notes             = @notes,
    status            = @status,
    energy_source     = @energy_source,
    odometer_km       = @odometer_km,
    updated_at        = now()
WHERE id = @id
  AND account_id = @account_id
RETURNING *;

-- name: DeleteEntry :exec
-- Delete a charge entry scoped to the caller's own account. The double-scope
-- (id AND account_id) means a user cannot delete another tenant's entry even
-- if they somehow obtain a valid entry UUID — cross-tenant deletes are blocked
-- at the SQL level (design D4, intentional double-scope guard).
DELETE FROM charging.manual_charge_entries
WHERE id = @id
  AND account_id = @account_id;

-- name: ListEntriesByVehicle :many
-- Return entries for a specific vehicle within an account, ordered newest charged
-- day first, limited to limit_count rows. Uses idx_manual_charge_entries_vehicle_time
-- (account_id, tesla_id, charged_on DESC): account_id prunes to the tenant, tesla_id
-- further narrows to one vehicle, and the DESC column means the ORDER BY is satisfied
-- by the index directly — no sort step required (design D3, Read path 1).
SELECT * FROM charging.manual_charge_entries
WHERE account_id = @account_id
  AND tesla_id = @tesla_id
ORDER BY charged_on DESC
LIMIT @limit_count;

-- name: ListEntriesByAccount :many
-- Return all entries for a given account across all vehicles, ordered newest charged
-- day first, limited to limit_count rows. Uses idx_manual_charge_entries_account_time
-- (account_id, charged_on DESC): account_id is the single WHERE predicate and
-- charged_on DESC matches the ORDER BY, eliminating a sort step (design D3, Read path 2).
SELECT * FROM charging.manual_charge_entries
WHERE account_id = @account_id
ORDER BY charged_on DESC
LIMIT @limit_count;

-- name: ListEntriesByVehicleBetween :many
-- Return entries for a specific vehicle within an account whose charged_on falls
-- within [@from_date, @to_date], inclusive of both bounds, ordered newest charged
-- day first. Uses idx_manual_charge_entries_vehicle_time (account_id, tesla_id,
-- charged_on DESC) as a single index range scan: account_id and tesla_id prune to
-- the tenant and vehicle, charged_on BETWEEN walks the range, and the DESC column
-- order satisfies ORDER BY with no separate sort step (design D3). No LIMIT: the
-- caller-supplied [from, to] window is the safety bound, not a row count
-- (design D1, roadmap D9).
SELECT * FROM charging.manual_charge_entries
WHERE account_id = @account_id
  AND tesla_id = @tesla_id
  AND charged_on BETWEEN @from_date AND @to_date
ORDER BY charged_on DESC;

-- name: ListEntriesByVehicleUpdatedSince :many
-- Return entries for a specific vehicle within an account whose updated_at is at or
-- after @since, ordered newest charged day first. Reuses
-- idx_manual_charge_entries_vehicle_time (account_id, tesla_id, charged_on DESC):
-- account_id and tesla_id are satisfied as leading equality predicates in the same
-- range scan the other vehicle-scoped queries use; updated_at >= @since is a residual
-- filter within that scan (no new index — this table is small and user-write-driven,
-- unlike the append-only, high-volume tables). No LIMIT: @since itself bounds the
-- result (RM29-analytics-add-vehicle-metrics design D3, specs/manual-charge-log/spec.md
-- "List entries by vehicle updated since a given instant").
SELECT * FROM charging.manual_charge_entries
WHERE account_id = @account_id
  AND tesla_id = @tesla_id
  AND updated_at >= @since
ORDER BY charged_on DESC;

-- name: MirrorSuperchargerSession :exec
-- Upsert one Supercharger session's mirrorable subset. Called once per session, in
-- one transaction, by SessionWriter.MirrorSessions.
--
-- LOAD-BEARING: start_battery_pct, end_battery_pct, battery_pct_source,
-- start_battery_pct_est and end_battery_pct_est are ABSENT from both the INSERT
-- column list and the ON CONFLICT DO UPDATE SET clause. They are human-owned; the
-- nightly sync must never write, clear or overwrite one. Unlike
-- telemetry.UpsertSuperchargerSession — which relies on this comment alone —
-- charging.SessionMirror has no field for them either, so binding one here would not
-- even compile (design.md D6). Do NOT "complete the pattern" by adding them.
--
-- THE REFRESH SET IS NOT A JUDGEMENT CALL. It is telemetry's own ON CONFLICT DO
-- UPDATE SET, minus raw_data (a column this table does not carry): energy_kwh,
-- total_cost, currency, is_paid, tesla_id, updated_at. Everything else mirrored —
-- charge_start_date_time, charge_stop_date_time, site_location_name, created_at — is
-- write-once at the source, so it is write-once here. site_location_name in
-- particular is NOT refreshed because telemetry does not refresh it, not because a
-- site name was judged unlikely to change (design.md D1's rule: a mirrored column
-- gets exactly its source column's write semantics).
--
-- NO WHERE PREDICATE on the DO UPDATE, deliberately (design.md D6). An earlier draft
-- carried WHERE charge_sessions.tesla_id IS DISTINCT FROM EXCLUDED.tesla_id so an
-- unchanged row was not rewritten. With five refreshable columns that predicate would
-- have to name all five, and a future column added to the SET but forgotten in the
-- WHERE would silently stop advancing updated_at, with nothing in this project able
-- to catch it. Telemetry's own upsert has no such predicate either. Consequence:
-- updated_at here means "the last mirror pass touched this row" — exactly what
-- supercharger_sessions.updated_at means — and is NOT a "this row's data changed"
-- signal on either side.
INSERT INTO charging.supercharger_sessions (
    account_id, vin, tesla_id, session_id,
    charge_start_date_time, charge_stop_date_time,
    site_location_name, energy_kwh, total_cost, currency, is_paid
) VALUES (
    @account_id, @vin, @tesla_id, @session_id,
    @charge_start_date_time, @charge_stop_date_time,
    @site_location_name, @energy_kwh, @total_cost, @currency, @is_paid
)
ON CONFLICT (account_id, session_id) DO UPDATE SET
    energy_kwh = EXCLUDED.energy_kwh,
    total_cost = EXCLUDED.total_cost,
    currency   = EXCLUDED.currency,
    is_paid    = EXCLUDED.is_paid,
    tesla_id   = EXCLUDED.tesla_id,
    updated_at = now();

-- name: ListSessionsByVehicleBetween :many
-- Return charge sessions for a specific vehicle within an account whose
-- charge_stop_date_time falls within the whole UTC calendar-day window
-- [@from_time, @to_time], @to_time inclusive of its entire day, ordered oldest-first
-- (ascending charge_stop_date_time, design.md D3 — deliberately UNLIKE
-- ListEntriesByVehicleBetween's charged_on DESC, but matching
-- telemetry.SuperchargerSessionsByVehicleBetween's ordering exactly).
--
-- @end_bound is @to_time + 1 calendar day, COMPUTED IN GO (design.md D5), exactly
-- mirroring telemetry.SuperchargerSessionsByVehicleBetween's own end-bound translation
-- — do NOT compute it in SQL. The predicate below is therefore half-open
-- (>= ... AND < ...), not BETWEEN: a plain BETWEEN against @to_time's UTC-midnight
-- value would silently drop every session that stopped later that same calendar day,
-- which is exactly the trap the project's ?start=&end= HTTP date-filter convention
-- (internal/gateway/AGENTS.md) exists to prevent.
--
-- Uses idx_supercharger_sessions_vehicle_stop (account_id, tesla_id, charge_stop_date_time)
-- as a single ascending index range scan: account_id and tesla_id prune to the tenant
-- and vehicle as leading equality predicates, the half-open charge_stop_date_time
-- range walks the trailing column, and the index's own ASC order satisfies ORDER BY
-- with no separate sort step and no backward scan (design.md D1). A half-open range is
-- exactly as scannable as a closed BETWEEN on a B-tree index — both are a single
-- contiguous leaf-page walk bounded on two sides; only the boundary comparison
-- operator differs (design.md §"Index proof"). No LIMIT: the caller-supplied
-- [from_time, end_bound) window is the safety bound, matching
-- ListEntriesByVehicleBetween's precedent.
--
-- tesla_id = @tesla_id against a nullable column excludes every row where tesla_id IS
-- NULL (SQL's NULL = value is neither true nor false) — an orphaned session (VIN no
-- longer a currently-registered vehicle) is correctly outside a teslaID-keyed read
-- (design.md D6). This is the same behavior telemetry's own
-- SuperchargerSessionsByVehicleBetween already has over the identical column shape.
SELECT * FROM charging.supercharger_sessions
WHERE account_id = @account_id
  AND tesla_id = @tesla_id
  AND charge_stop_date_time >= @from_time
  AND charge_stop_date_time <  @end_bound
ORDER BY charge_stop_date_time ASC;

-- name: LockSessionForVerification :one
-- Read vin and energy_kwh for one account-scoped charge session, LOCKING the row (FOR
-- UPDATE) for the remainder of the caller's transaction. Called ONLY by VerifySession, and
-- ONLY when it must derive start_battery_pct from energy and the end percentage (design.md
-- D2/D7/D9, MAG-36) -- every other VerifySession call skips this query entirely and runs its
-- single UPDATE outside a transaction, exactly as before this change.
--
-- FOR UPDATE mirrors internal/account's AccessTokenFor and this module's own
-- SessionWriter.MirrorSessions: the read and the later write (VerifySuperchargerSession, called
-- against the SAME transaction) must observe one consistent row, so a concurrent
-- SessionWriter.MirrorSessions refresh of energy_kwh cannot land between this read and that
-- write and leave the derived percentage computed from a value the row no longer holds
-- (design.md D9).
--
-- WHERE id = @id AND account_id = @account_id mirrors VerifySuperchargerSession's own scoping
-- exactly; zero rows matched surfaces as pgx.ErrNoRows, wrapped by the caller identically to
-- VerifySuperchargerSession's own not-found case (design.md D10).
SELECT vin, energy_kwh FROM charging.supercharger_sessions
WHERE id = @id
  AND account_id = @account_id
FOR UPDATE;

-- name: VerifySuperchargerSession :one
-- Update the human-owned verification channel on one account-scoped charge session:
-- start_battery_pct, end_battery_pct, and battery_pct_source — plus updated_at. No other
-- column is in this SET clause, INCLUDING start_battery_pct_est/end_battery_pct_est —
-- this is the mirror image of MirrorSuperchargerSession's protection (that query cannot touch
-- these three; this query cannot touch anything else), by the query's shape, not by a
-- comment a reviewer has to notice (design.md D1).
--
-- @battery_pct_source is COMPUTED IN GO (design.md D2/D7), never accepted from a caller:
-- "user_verified" when either percentage is non-nil, NULL when both are nil — satisfying
-- supercharger_sessions_pct_source_required in the same statement that clears or sets the
-- percentages, so no intermediate row state can violate it.
--
-- WHERE id = @id AND account_id = @account_id mirrors UpdateEntry's scoping exactly
-- (design.md D5/D11): a point lookup on the table's PRIMARY KEY plus its leading tenant
-- column. Zero rows matched — unknown id or wrong account, indistinguishable — surfaces
-- to the caller as pgx.ErrNoRows, exactly like UpdateEntry's own not-found behavior
-- (TestUpdate_CrossAccountIsNoOp is the existing precedent for this shape).
UPDATE charging.supercharger_sessions
SET
    start_battery_pct  = @start_battery_pct,
    end_battery_pct    = @end_battery_pct,
    battery_pct_source = @battery_pct_source,
    updated_at         = now()
WHERE id = @id
  AND account_id = @account_id
RETURNING *;

-- name: ListSessionsByVehicleUpdatedSince :many
-- Return charge sessions for a specific vehicle within an account whose updated_at is at
-- or after @since, ordered oldest-first by charge_stop_date_time (design.md D1) — NOT by
-- updated_at itself, and NOT ListEntriesByVehicleUpdatedSince's DESC: this table's index
-- is built ASC (RM30 D1), so ascending on the index's own trailing column is the order
-- that needs no sort step. Reuses idx_supercharger_sessions_vehicle_stop (account_id, tesla_id,
-- charge_stop_date_time) as a single ascending index range scan: account_id and tesla_id
-- prune to the tenant and vehicle as leading equality predicates in the same scan every
-- other vehicle-scoped query on this table already uses; updated_at >= @since is a
-- RESIDUAL filter evaluated per matching row within that scan, not a separately-indexed
-- predicate (design.md D1) -- the identical reasoning
-- ListEntriesByVehicleUpdatedSince (query.sql:118) already documents for
-- manual_charge_entries's own index. No new index: this table receives roughly one row
-- per Supercharger session per account, written nightly, the same low-volume,
-- write-driven profile that already justified skipping a dedicated updated_at index
-- there.
--
-- THIS QUERY IS THE ONLY MECHANISM (design.md D1) that carries a
-- SessionVerifier.VerifySession edit into analytics.Recalculator.Reconcile once tier 3
-- repoints the source port: VerifySession sets updated_at = now() and touches no other
-- timestamp column, and charge_start_date_time/charge_stop_date_time are write-once
-- (RM29 D1), so updated_at is the only column that moves when a human verifies a
-- session.
--
-- No LIMIT: @since itself bounds the result, matching ListEntriesByVehicleUpdatedSince's
-- and ListSessionsByVehicleBetween's own precedent.
--
-- tesla_id = @tesla_id against a nullable column excludes every row where tesla_id IS
-- NULL (SQL's NULL = value is neither true nor false) -- an orphaned session is
-- correctly outside a teslaID-keyed read (design.md D4, restating RM29 D6/RM30 D6).
SELECT * FROM charging.supercharger_sessions
WHERE account_id = @account_id
  AND tesla_id = @tesla_id
  AND updated_at >= @since
ORDER BY charge_stop_date_time ASC;

-- name: ListSessionsByVehicle :many
-- Return the limit_count most recent charge sessions for a specific vehicle within an
-- account, ordered newest-first (descending charge_stop_date_time), limited to
-- @limit_count rows.
--
-- Sort direction is DESC here, DELIBERATELY UNLIKE ListSessionsByVehicleBetween's ASC
-- (design.md D3 of this change -- ListSessionsByVehicleBetween's own doc comment already
-- warns these two Session reads do not share a sort-direction rule). A "most recent N"
-- limit-bounded read needs newest-first by construction, the same reasoning
-- Reader.ListEntriesByVehicle already applies to manual_charge_entries and
-- telemetry.SuperchargerSessionsByVehicle already applies to supercharger_sessions.
--
-- idx_supercharger_sessions_vehicle_stop (account_id, tesla_id, charge_stop_date_time) was
-- built ASC, not DESC (RM30 D1, for ListSessionsByVehicleBetween's own bounded-window
-- read). This query still needs NO new index: Postgres serves
-- ORDER BY charge_stop_date_time DESC LIMIT @limit_count from the SAME ascending btree
-- via a backward index scan -- a B-tree index is traversable in either direction at
-- identical cost, so account_id/tesla_id still prune the scan to a single contiguous
-- leaf-page range and only the walk direction (and hence the row order handed up)
-- differs (design.md D3, "Index proof" below). Confirmed by EXPLAIN in the integration
-- test (Test Contract T-Order2), not merely asserted.
--
-- limit_count is always a positive int32 by the time this query runs: the Go caller
-- clamps a non-positive limit to the module's existing defaultLimit (100) before
-- calling (design.md D3), mirroring ListEntriesByVehicle's identical clamp -- this
-- query itself has no default-handling logic, exactly like ListEntriesByVehicle's own
-- :many query.
--
-- tesla_id = @tesla_id against a nullable column excludes every row where tesla_id IS
-- NULL, same as every other vehicle-scoped query on this table (design.md D4).
SELECT * FROM charging.supercharger_sessions
WHERE account_id = @account_id
  AND tesla_id = @tesla_id
ORDER BY charge_stop_date_time DESC
LIMIT @limit_count;
