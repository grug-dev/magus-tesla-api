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
--
-- price_source is ALSO COMPUTED IN GO (RM51/MAG-58 design.md D2/D3), never accepted
-- from the caller -- the identical precedent energy_source above already sets.
-- service.go's resolvePriceSource computes USER/UNCONFIRMED before binding this
-- param; the query itself has no way to tell the two apart.
--
-- start_battery_source is ALSO COMPUTED IN GO, never accepted from the caller --
-- the identical precedent energy_source above already sets. service.go's
-- resolveStartBatteryPct computes USER/ESTIMATED/NULL before binding this param.
--
-- created_by_account_id records who typed the entry and is never read back as a
-- filter.
INSERT INTO charging.manual_charge_entries (
    created_by_account_id,
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
    odometer_km,
    price_source,
    start_battery_source
) VALUES (
    @created_by_account_id,
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
    @odometer_km,
    @price_source,
    @start_battery_source
)
RETURNING *;

-- name: UpdateEntry :one
-- Update mutable fields of an existing charge entry. The real check is the caller
-- proving vehicle ownership before this query ever runs (a vehicleref.Ref cannot be
-- built without it). WHERE (id, tesla_id) is the second line of defence: it catches a
-- proven-vehicle Ref applied to the wrong row (id typo, stale id, a race), so a right
-- vehicle can never collide with another vehicle's row even by accident.
-- Immutable columns (id, created_by_account_id, tesla_id, vin, created_at) are never
-- touched. updated_at is refreshed to now() on every successful update.
--
-- status, odometer_km: bound as supplied by the caller (RM33 / MAG-18).
--
-- energy_source is COMPUTED IN GO, never accepted from the caller as a stored value's
-- true provenance -- same shape as CreateEntry's @energy_source above, and the same
-- precedent VerifySuperchargerSession's @battery_pct_source sets for charging.supercharger_sessions.
--
-- price_source is ALSO COMPUTED IN GO (RM51/MAG-58 design.md D2/D3), never accepted
-- from the caller -- the identical precedent energy_source above already sets.
-- Recomputed on every Update, never sticky: service.go's resolvePriceSource runs
-- again against the entry's current price and confirmation intent.
--
-- start_battery_source is ALSO COMPUTED IN GO, never accepted from the caller --
-- the identical precedent energy_source above already sets. Recomputed on every
-- Update, never sticky: service.go's resolveStartBatteryPct runs again against
-- the entry's current percentages and (possibly just-resolved) energy.
UPDATE charging.manual_charge_entries
SET
    charged_on            = @charged_on,
    energy_added_kwh      = @energy_added_kwh,
    price                 = @price,
    currency              = @currency,
    started_at            = @started_at,
    ended_at              = @ended_at,
    start_battery_pct     = @start_battery_pct,
    end_battery_pct       = @end_battery_pct,
    charging_type         = @charging_type,
    location_kind         = @location_kind,
    location_label        = @location_label,
    notes                 = @notes,
    status                = @status,
    energy_source         = @energy_source,
    odometer_km           = @odometer_km,
    price_source          = @price_source,
    start_battery_source  = @start_battery_source,
    updated_at            = now()
WHERE id = @id
  AND tesla_id = @tesla_id
RETURNING *;

-- name: DeleteEntry :execrows
-- Delete a charge entry. Same guard as UpdateEntry: the caller already proved vehicle
-- ownership to obtain a Ref, and WHERE (id, tesla_id) is the second line of defence
-- against a proven-vehicle Ref applied to the wrong row. Returns the row count so the
-- caller can tell a real delete from a no-op instead of a silent no-op looking like
-- success.
DELETE FROM charging.manual_charge_entries
WHERE id = @id
  AND tesla_id = @tesla_id;

-- name: ListEntriesByVehicle :many
-- Return entries for a specific vehicle, ordered newest charged day first, limited
-- to limit_count rows. Uses idx_manual_charge_entries_vehicle_time
-- (tesla_id, charged_on DESC): tesla_id prunes to one vehicle, and the DESC column
-- means the ORDER BY is satisfied by the index directly — no sort step required.
-- The read is car-wide: it returns entries typed by any account registered to
-- that car.
SELECT * FROM charging.manual_charge_entries
WHERE tesla_id = @tesla_id
ORDER BY charged_on DESC
LIMIT @limit_count;

-- name: ListEntriesByVehicles :many
-- Return entries for a caller-supplied set of vehicles, ordered newest charged day
-- first across the whole set, limited to limit_count rows. Uses
-- idx_manual_charge_entries_vehicle_time (tesla_id, charged_on DESC): the index
-- prunes per vehicle, and a multi-vehicle array is expected to add a sort step on
-- top of the per-vehicle index walks.
SELECT * FROM charging.manual_charge_entries
WHERE tesla_id = ANY(@tesla_ids::bigint[])
ORDER BY charged_on DESC
LIMIT @limit_count;

-- name: ListEntriesByVehicleBetween :many
-- Return entries for a specific vehicle whose charged_on falls within
-- [@from_date, @to_date], inclusive of both bounds, ordered newest charged day
-- first. Uses idx_manual_charge_entries_vehicle_time (tesla_id, charged_on DESC)
-- as a single index range scan: tesla_id prunes to the vehicle, charged_on BETWEEN
-- walks the range, and the DESC column order satisfies ORDER BY with no separate
-- sort step. No LIMIT: the caller-supplied [from, to] window is the safety bound,
-- not a row count. The read is car-wide: it returns entries typed by any account
-- registered to that car.
SELECT * FROM charging.manual_charge_entries
WHERE tesla_id = @tesla_id
  AND charged_on BETWEEN @from_date AND @to_date
ORDER BY charged_on DESC;

-- name: ListEntriesByVehicleUpdatedSince :many
-- Return entries for a specific vehicle whose updated_at is at or after @since,
-- ordered newest charged day first. Reuses idx_manual_charge_entries_vehicle_time
-- (tesla_id, charged_on DESC): tesla_id is satisfied as the leading equality
-- predicate in the same range scan the other vehicle-scoped queries use;
-- updated_at >= @since is a residual filter within that scan. This query orders by
-- charged_on, so an updated_at index would force a sort — no new index. The read
-- is car-wide: it returns entries typed by any account registered to that car.
SELECT * FROM charging.manual_charge_entries
WHERE tesla_id = @tesla_id
  AND updated_at >= @since
ORDER BY charged_on DESC;

-- name: MirrorSuperchargerSession :exec
-- Upsert one Supercharger session's mirrorable subset. Called once per session, in
-- one transaction, by SessionWriter.MirrorSessions.
--
-- LOAD-BEARING: start_battery_pct, end_battery_pct, and battery_pct_source are ABSENT
-- from both the INSERT column list and the ON CONFLICT DO UPDATE SET clause. They
-- are human-owned; the nightly sync must never write, clear or overwrite one. Unlike
-- telemetry.UpsertSuperchargerSession — which relies on this comment alone —
-- charging.SessionMirror has no field for them either, so binding one here would not
-- even compile (RM29-charging-add-charge-sessions design.md D6). Do NOT "complete
-- the pattern" by adding them. The two frozen estimate columns formerly also excluded
-- here were dropped from the table entirely by RM41-charging-drop-estimate-columns —
-- there is no longer a column to guard.
--
-- The session's lifecycle status (added by RM41-charging-add-session-status, MAG-45)
-- is ALSO absent from both the INSERT column list and the ON CONFLICT DO UPDATE SET
-- clause — a freshly-mirrored session has no battery-percentage data yet, so it must
-- start IN_PROGRESS, which is exactly what the column's own DEFAULT provides with no
-- explicit value here. Only SessionVerifier.VerifySession ever writes this column
-- (see VerifySuperchargerSession's own doc comment).
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
-- UPDATED_AT NOW MEANS "THIS ROW'S DATA CHANGED" (RM44-charging-add-change-
-- detecting-mirror, MAG-48). An earlier version of this comment explained why a
-- hand-listed WHERE predicate was rejected: a column added to the SET clause later,
-- but forgotten in the WHERE, would silently stop being caught. The fix below answers
-- that objection instead of repeating it.
--
-- THE RULE: this comparison covers EXACTLY the columns this SET clause writes —
-- energy_kwh, total_cost, currency, is_paid, tesla_id — and nothing else. Every other
-- column is deny-listed below, because this query never refreshes it: comparing a
-- column this query does not write can only ever find a difference that never
-- resolves, which would make updated_at advance every night, forever, for no reason
-- (see design.md "The governing rule" for the live-tested failure this replaced).
--
-- Add a column to this SET clause later -> remove it from the deny-list below.
-- Add a column to the table that this SET clause does not write -> add it to the
-- deny-list. Forgetting either direction is caught by
-- db_mirror_schema_selfcheck_integration_test.go, which fails the moment a live
-- column belongs to neither list.
--
-- tesla_id stays INSIDE the comparison, deliberately: telemetry refreshes it too, and a
-- mirrored column takes exactly its source column's write semantics (the rule this
-- whole query follows). There is no other reason needed -- account_id is gone, so there
-- is no orphan-vehicle case left for this column to recover.
INSERT INTO charging.supercharger_sessions (
    vin, tesla_id, session_id,
    charge_start_date_time, charge_stop_date_time,
    site_location_name, energy_kwh, total_cost, currency, is_paid
) VALUES (
    @vin, @tesla_id, @session_id,
    @charge_start_date_time, @charge_stop_date_time,
    @site_location_name, @energy_kwh, @total_cost, @currency, @is_paid
)
ON CONFLICT (session_id) DO UPDATE SET
    energy_kwh = EXCLUDED.energy_kwh,
    total_cost = EXCLUDED.total_cost,
    currency   = EXCLUDED.currency,
    is_paid    = EXCLUDED.is_paid,
    tesla_id   = EXCLUDED.tesla_id,
    updated_at = CASE
        WHEN to_jsonb(supercharger_sessions.*) - '{id,vin,session_id,charge_start_date_time,charge_stop_date_time,site_location_name,start_battery_pct,end_battery_pct,battery_pct_source,created_at,updated_at,inferred_capacity_kwh_calc,status}'::text[]
             IS DISTINCT FROM
             to_jsonb(EXCLUDED.*) - '{id,vin,session_id,charge_start_date_time,charge_stop_date_time,site_location_name,start_battery_pct,end_battery_pct,battery_pct_source,created_at,updated_at,inferred_capacity_kwh_calc,status}'::text[]
        THEN now()
        ELSE supercharger_sessions.updated_at
    END;

-- name: ListSessionsByVehicleBetween :many
-- Return charge sessions for a specific vehicle whose charge_stop_date_time falls
-- within the whole UTC calendar-day window [@from_time, @to_time], @to_time inclusive
-- of its entire day, ordered oldest-first (ascending charge_stop_date_time —
-- deliberately UNLIKE ListEntriesByVehicleBetween's charged_on DESC, but matching
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
-- Uses idx_supercharger_sessions_vehicle_stop (tesla_id, charge_stop_date_time) as a
-- single ascending index range scan: tesla_id prunes to the vehicle as the leading
-- equality predicate, the half-open charge_stop_date_time range walks the trailing
-- column, and the index's own ASC order satisfies ORDER BY with no separate sort step
-- and no backward scan. A half-open range is exactly as scannable as a
-- closed BETWEEN on a B-tree index — both are a single contiguous leaf-page walk
-- bounded on two sides; only the boundary comparison operator differs.
-- No LIMIT: the caller-supplied [from_time, end_bound) window is the
-- safety bound, matching ListEntriesByVehicleBetween's precedent.
SELECT * FROM charging.supercharger_sessions
WHERE tesla_id = @tesla_id
  AND charge_stop_date_time >= @from_time
  AND charge_stop_date_time <  @end_bound
ORDER BY charge_stop_date_time ASC;

-- name: LockSessionForVerification :one
-- Read vin and energy_kwh for one vehicle-scoped charge session, LOCKING the row (FOR
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
-- WHERE id = @id AND tesla_id = @tesla_id mirrors VerifySuperchargerSession's own scoping
-- exactly; zero rows matched surfaces as pgx.ErrNoRows, wrapped by the caller identically to
-- VerifySuperchargerSession's own not-found case. The vehicle it names is
-- the whole tenant boundary on this write path now.
--
-- tesla_id is also selected: packCapacityKWh needs it to look up the vehicle's own
-- measured capacity before falling back to the default.
SELECT vin, tesla_id, energy_kwh FROM charging.supercharger_sessions
WHERE id = @id
  AND tesla_id = @tesla_id
FOR UPDATE;

-- name: VerifySuperchargerSession :one
-- Update the human-owned verification channel on one vehicle-scoped charge session:
-- start_battery_pct, end_battery_pct, battery_pct_source, and status — plus
-- updated_at. No other column is in this SET clause — this is the mirror image of
-- MirrorSuperchargerSession's protection (that query cannot touch these three; this
-- query cannot touch anything else), by the query's shape, not by a comment a
-- reviewer has to notice (RM31-charging-add-session-verification-port design.md D1).
-- The two frozen estimate columns formerly also named here as columns this SET clause
-- could never reach were dropped from the table entirely by
-- RM41-charging-drop-estimate-columns.
--
-- @battery_pct_source is COMPUTED IN GO (design.md D2/D7), never accepted from a caller:
-- "user_verified" when either percentage is non-nil, NULL when both are nil — satisfying
-- supercharger_sessions_pct_source_required in the same statement that clears or sets the
-- percentages, so no intermediate row state can violate it.
--
-- WHERE id = @id AND tesla_id = @tesla_id is the ONLY tenant boundary left on this write
-- path: the gateway resolves which vehicle it believes owns this session and passes it
-- here, so a wrong vehicle matches zero rows instead of editing someone else's session.
-- Zero rows matched — unknown id or wrong vehicle, indistinguishable — surfaces to the
-- caller as pgx.ErrNoRows, exactly like UpdateEntry's own not-found behavior.
--
-- @status is COMPUTED IN GO by sessionStatusFor (RM41-charging-add-session-status),
-- never accepted from an external caller — the identical shape @battery_pct_source
-- already uses.
UPDATE charging.supercharger_sessions
SET
    start_battery_pct  = @start_battery_pct,
    end_battery_pct    = @end_battery_pct,
    battery_pct_source = @battery_pct_source,
    status             = @status,
    updated_at         = now()
WHERE id = @id
  AND tesla_id = @tesla_id
RETURNING *;

-- name: ListSessionsByVehicleUpdatedSince :many
-- Return charge sessions for a specific vehicle whose updated_at is at or after @since,
-- ordered oldest-first by charge_stop_date_time — NOT by updated_at
-- itself, and NOT ListEntriesByVehicleUpdatedSince's DESC: this table's index is built
-- ASC, so ascending on the index's own trailing column is the order that
-- needs no sort step. Reuses idx_supercharger_sessions_vehicle_stop (tesla_id,
-- charge_stop_date_time) as a single ascending index range scan: tesla_id prunes to the
-- vehicle as the leading equality predicate in the same scan every other vehicle-scoped
-- query on this table already uses; updated_at >= @since is a RESIDUAL filter evaluated
-- per matching row within that scan, not a separately-indexed predicate --
-- this query orders by charge_stop_date_time, so a dedicated updated_at index would
-- serve the predicate and then force a sort, strictly worse than the current plan. No
-- new index: this table receives roughly one row per Supercharger session per vehicle,
-- written nightly, a low-volume, write-driven profile.
--
-- THIS QUERY IS THE ONLY MECHANISM (design.md D1) that carries a
-- SessionVerifier.VerifySession edit into analytics.Recalculator.Reconcile: VerifySession
-- sets updated_at = now() and touches no other timestamp column, and
-- charge_start_date_time/charge_stop_date_time are write-once, so updated_at
-- is the only column that moves when a human verifies a session.
--
-- No LIMIT: @since itself bounds the result, matching ListEntriesByVehicleUpdatedSince's
-- and ListSessionsByVehicleBetween's own precedent.
SELECT * FROM charging.supercharger_sessions
WHERE tesla_id = @tesla_id
  AND updated_at >= @since
ORDER BY charge_stop_date_time ASC;

-- name: ListSessionsByVehicle :many
-- Return the limit_count most recent charge sessions for a specific vehicle, ordered
-- newest-first (descending charge_stop_date_time), limited to @limit_count rows.
--
-- Sort direction is DESC here, DELIBERATELY UNLIKE ListSessionsByVehicleBetween's ASC
-- (design.md D3 of this change -- ListSessionsByVehicleBetween's own doc comment already
-- warns these two Session reads do not share a sort-direction rule). A "most recent N"
-- limit-bounded read needs newest-first by construction, the same reasoning
-- Reader.ListEntriesByVehicle already applies to manual_charge_entries and
-- telemetry.SuperchargerSessionsByVehicle already applies to supercharger_sessions.
--
-- idx_supercharger_sessions_vehicle_stop (tesla_id, charge_stop_date_time) was built ASC,
-- not DESC (for ListSessionsByVehicleBetween's own bounded-window read). This
-- query still needs NO new index: Postgres serves
-- ORDER BY charge_stop_date_time DESC LIMIT @limit_count from the SAME ascending btree
-- via a backward index scan -- a B-tree index is traversable in either direction at
-- identical cost, so tesla_id still prunes the scan to a single contiguous leaf-page
-- range and only the walk direction (and hence the row order handed up) differs.
-- Confirmed by EXPLAIN in the integration test (Test Contract T-11), not merely asserted.
--
-- limit_count is always a positive int32 by the time this query runs: the Go caller
-- clamps a non-positive limit to the module's existing defaultLimit (100) before
-- calling (design.md D3), mirroring ListEntriesByVehicle's identical clamp -- this
-- query itself has no default-handling logic, exactly like ListEntriesByVehicle's own
-- :many query.
SELECT * FROM charging.supercharger_sessions
WHERE tesla_id = @tesla_id
ORDER BY charge_stop_date_time DESC
LIMIT @limit_count;

-- name: GetMirrorWatermark :one
-- Single-row cursor lookup for one vehicle. Returns pgx.ErrNoRows when no
-- watermark exists yet, which charging's MirrorWatermarkStore.MirrorWatermark
-- treats as "epoch": this vehicle has never been mirrored under the bounded
-- read, so the caller backfills its full Supercharger history in one pass.
-- Served entirely by mirror_watermarks_vehicle_unique's own index — no
-- separate CREATE INDEX.
SELECT source_updated_at
FROM charging.mirror_watermarks
WHERE tesla_id = @tesla_id;

-- name: UpsertMirrorWatermark :exec
-- Advance one vehicle's cursor. Called only when the caller's bounded
-- telemetry read returned at least one row, advanced to the max updated_at
-- observed on that run -- a call with zero rows never reaches this query at
-- all (the caller's own responsibility). created_at is DELIBERATELY ABSENT
-- from the SET clause -- it must record when this vehicle's cursor was
-- FIRST created, not the most recent advance, mirroring
-- UpsertVehicleMetricWatermark's identical convention.
INSERT INTO charging.mirror_watermarks (
    tesla_id, source_updated_at
) VALUES (
    @tesla_id, @source_updated_at
)
ON CONFLICT (tesla_id) DO UPDATE SET
    source_updated_at = EXCLUDED.source_updated_at,
    updated_at         = now();

-- name: ListValidManualEntryCapacitiesForPeriod :many
-- RD2's manual_charge_entries branch: only energy_source = 'USER' rows are
-- honest capacity evidence -- an 'ESTIMATED' row's energy was itself derived by
-- dividing by packCapacityKWh, so averaging it feeds the constant back into
-- itself (roadmap RD2, internal/charging/AGENTS.md §Data Ownership).
-- inferred_capacity_kwh_calc IS NOT NULL is the existing GENERATED-column guard
-- (20260829000001): it is non-NULL only when both battery percentages are
-- present AND end_battery_pct > start_battery_pct, so this query never returns
-- a NULL capacity or a NULL percentage -- the caller (monthly_capacity.go) does
-- not need to re-check that. tesla_id is BIGINT NOT NULL on this table (roadmap
-- F5) -- no NULL-skip needed here, unlike the session query below.
--
-- start_battery_source = 'USER' excludes a row whose starting percentage was
-- itself derived from a capacity: its inferred_capacity_kwh_calc comes back
-- equal to the capacity the derivation divided by, so counting it as evidence
-- would feed that same capacity's own measurement back into itself.
SELECT tesla_id,
       inferred_capacity_kwh_calc,
       start_battery_pct,
       end_battery_pct
  FROM charging.manual_charge_entries
 WHERE energy_source = 'USER'
   AND start_battery_source = 'USER'
   AND inferred_capacity_kwh_calc IS NOT NULL
   AND charged_on >= @period_start
   AND charged_on <  @period_end;

-- name: ListValidSessionCapacitiesForPeriod :many
-- RD2's supercharger_sessions branch: only status = 'DONE' rows are honest
-- capacity evidence -- 'DONE_CALCULATED' means derivedStartBatteryPct computed
-- start_battery_pct as endPct - energyKWh/62.0*100, which cancels back to
-- exactly 62.0 in this formula (roadmap RD2); 'IN_PROGRESS' has no complete
-- percentage pair at all, so its inferred_capacity_kwh_calc is already NULL.
-- tesla_id is BIGINT NOT NULL on this table -- no NULL-skip needed, unlike
-- before this table was re-keyed. Supercharger energy is metered by Tesla, so
-- (unlike the manual_charge_entries branch) there is no energy_source-equivalent
-- column to check here.
SELECT tesla_id,
       inferred_capacity_kwh_calc,
       start_battery_pct,
       end_battery_pct
  FROM charging.supercharger_sessions
 WHERE status = 'DONE'
   AND inferred_capacity_kwh_calc IS NOT NULL
   AND charge_stop_date_time >= @period_start
   AND charge_stop_date_time <  @period_end;

-- name: UpsertMonthlyEffectiveCapacity :exec
-- Store one vehicle's monthly estimate (roadmap RD11's write seam, RD9's
-- backfill/re-run path). ON CONFLICT so a re-run of the same period -- a manual
-- backfill, or a corrected earlier run -- updates the existing row instead of
-- erroring or duplicating it. effective_capacity_kwh is bound as a nullable
-- Float8: NULL when the caller's estimate was NULL (RD4's thin-month case),
-- never coerced to a fabricated number. candidate_count and sample_count are
-- both plain, non-nullable integers -- the two-stage count the owner approved
-- at the design gate (design.md D1): candidate_count before the delta gate,
-- sample_count after it.
INSERT INTO charging.monthly_effective_capacity (
    tesla_id, effective_period, effective_capacity_kwh, candidate_count, sample_count
) VALUES (
    @tesla_id, @effective_period, @effective_capacity_kwh, @candidate_count, @sample_count
)
ON CONFLICT (tesla_id, effective_period) DO UPDATE SET
    effective_capacity_kwh = EXCLUDED.effective_capacity_kwh,
    candidate_count        = EXCLUDED.candidate_count,
    sample_count           = EXCLUDED.sample_count,
    updated_at             = now();

-- name: LatestMeasuredCapacity :one
-- packCapacityKWh's own read (roadmap RD11, RD4). Returns the newest row for
-- this vehicle whose effective_capacity_kwh IS NOT NULL -- so a current thin
-- month (NULL) never hides an earlier real measurement; the caller in Go
-- translates pgx.ErrNoRows (no measured row exists at all yet) to the
-- defaultPackCapacityKWh fallback, never an error.
SELECT effective_capacity_kwh
  FROM charging.monthly_effective_capacity
 WHERE tesla_id = @tesla_id
   AND effective_capacity_kwh IS NOT NULL
 ORDER BY effective_period DESC
 LIMIT 1;

-- name: EffectiveCapacityForPeriod :one
-- MonthlyCapacityReader.CapacityForMonth's own read. A DIFFERENT question
-- from LatestMeasuredCapacity above: this returns the row for the EXACT
-- calendar month containing @month, thin or absent included -- never the
-- newest non-NULL row across every month. Callers may pass any instant
-- inside the target month; date_trunc normalizes it to the month's first day
-- in SQL (matching effective_period's own CHECK (day = 1)) rather than in
-- Go, which would need a hand-rolled UTC-midnight construction outside
-- internal/clock. effective_capacity_kwh comes back SQL NULL when the month's
-- evidence was too thin to measure -- :one means "no row" surfaces as
-- pgx.ErrNoRows, which the Go caller translates to found=false, distinct from
-- a found row whose capacity is NULL.
SELECT effective_capacity_kwh
  FROM charging.monthly_effective_capacity
 WHERE tesla_id = @tesla_id
   AND effective_period = date_trunc('month', @month::date)::date;
