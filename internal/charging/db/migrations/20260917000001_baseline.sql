-- charging baseline.
--
-- One self-contained file that creates this module's whole schema. It is a
-- verbatim transcription of a pg_dump --schema-only taken on 2026-09-17, after
-- the charging module's migration history was squashed.
--
-- It creates objects and reads nothing, so it depends on no other module and can
-- be applied in any order relative to them. Keep it that way: a migration here
-- must never name another module's schema.
--
-- Existing databases (dev and prod) have this version recorded as applied without
-- it ever running. So a change made HERE reaches new databases only. Anything that
-- must also reach dev and prod belongs in a later, ordinary migration.

-- +goose Up

-- SCHEMA: charging
CREATE SCHEMA charging;

-- TABLE: manual_charge_entries
CREATE TABLE charging.manual_charge_entries (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    created_by_account_id uuid NOT NULL,
    tesla_id bigint NOT NULL,
    vin text NOT NULL,
    charged_on date NOT NULL,
    energy_added_kwh numeric(6,2),
    price numeric(14,2) NOT NULL,
    currency text DEFAULT 'COP'::text NOT NULL,
    started_at timestamp with time zone,
    ended_at timestamp with time zone,
    start_battery_pct smallint,
    end_battery_pct smallint,
    charging_type text,
    location_kind text,
    location_label text,
    notes text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    inferred_capacity_kwh_calc numeric GENERATED ALWAYS AS (
CASE
    WHEN ((start_battery_pct IS NOT NULL) AND (end_battery_pct IS NOT NULL) AND (end_battery_pct > start_battery_pct)) THEN round((energy_added_kwh / (((end_battery_pct - start_battery_pct))::numeric / (100)::numeric)), 3)
    ELSE NULL::numeric
END) STORED,
    status text DEFAULT 'IN_PROGRESS'::text NOT NULL,
    energy_source text DEFAULT 'USER'::text NOT NULL,
    odometer_km integer,
    price_source text DEFAULT 'UNCONFIRMED'::text NOT NULL,
    start_battery_source text,
    CONSTRAINT manual_charge_entries_charging_type_check CHECK ((charging_type = ANY (ARRAY['AC'::text, 'DC'::text]))),
    CONSTRAINT manual_charge_entries_check CHECK (((ended_at IS NULL) OR (started_at IS NULL) OR (ended_at >= started_at))),
    CONSTRAINT manual_charge_entries_end_battery_pct_check CHECK (((end_battery_pct >= 0) AND (end_battery_pct <= 100))),
    CONSTRAINT manual_charge_entries_energy_added_kwh_check CHECK ((energy_added_kwh > (0)::numeric)),
    CONSTRAINT manual_charge_entries_energy_source_check CHECK ((energy_source = ANY (ARRAY['USER'::text, 'ESTIMATED'::text]))),
    CONSTRAINT manual_charge_entries_location_kind_check CHECK ((location_kind = ANY (ARRAY['HOME'::text, 'WORK'::text, 'OTHER'::text]))),
    CONSTRAINT manual_charge_entries_odometer_km_check CHECK ((odometer_km >= 0)),
    CONSTRAINT manual_charge_entries_price_check CHECK ((price >= (0)::numeric)),
    CONSTRAINT manual_charge_entries_price_source_check CHECK ((price_source = ANY (ARRAY['USER'::text, 'UNCONFIRMED'::text]))),
    CONSTRAINT manual_charge_entries_start_battery_pct_check CHECK (((start_battery_pct >= 0) AND (start_battery_pct <= 100))),
    CONSTRAINT manual_charge_entries_start_battery_source_check CHECK ((start_battery_source = ANY (ARRAY['USER'::text, 'ESTIMATED'::text]))),
    CONSTRAINT manual_charge_entries_status_check CHECK ((status = ANY (ARRAY['IN_PROGRESS'::text, 'DONE'::text])))
);

-- TABLE: mirror_watermarks
CREATE TABLE charging.mirror_watermarks (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    source_updated_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    tesla_id bigint NOT NULL
);

-- TABLE: monthly_effective_capacity
CREATE TABLE charging.monthly_effective_capacity (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tesla_id bigint NOT NULL,
    effective_period date NOT NULL,
    effective_capacity_kwh double precision,
    candidate_count integer DEFAULT 0 NOT NULL,
    sample_count integer DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT monthly_effective_capacity_period_is_month_start CHECK ((EXTRACT(day FROM effective_period) = (1)::numeric))
);

-- TABLE: supercharger_sessions
CREATE TABLE charging.supercharger_sessions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    vin text NOT NULL,
    tesla_id bigint NOT NULL,
    session_id bigint NOT NULL,
    charge_start_date_time timestamp with time zone NOT NULL,
    charge_stop_date_time timestamp with time zone NOT NULL,
    site_location_name text NOT NULL,
    energy_kwh double precision,
    total_cost double precision,
    currency text,
    is_paid boolean,
    start_battery_pct smallint,
    end_battery_pct smallint,
    battery_pct_source text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    inferred_capacity_kwh_calc numeric GENERATED ALWAYS AS (
CASE
    WHEN ((energy_kwh IS NOT NULL) AND (start_battery_pct IS NOT NULL) AND (end_battery_pct IS NOT NULL) AND (end_battery_pct > start_battery_pct)) THEN round(((energy_kwh)::numeric / (((end_battery_pct - start_battery_pct))::numeric / (100)::numeric)), 3)
    ELSE NULL::numeric
END) STORED,
    status text DEFAULT 'IN_PROGRESS'::text NOT NULL,
    CONSTRAINT supercharger_sessions_battery_pct_source_check CHECK ((battery_pct_source = ANY (ARRAY['user_verified'::text, 'polled'::text]))),
    CONSTRAINT supercharger_sessions_end_battery_pct_check CHECK (((end_battery_pct >= 0) AND (end_battery_pct <= 100))),
    CONSTRAINT supercharger_sessions_pct_source_required CHECK ((((start_battery_pct IS NULL) AND (end_battery_pct IS NULL)) OR (battery_pct_source IS NOT NULL))),
    CONSTRAINT supercharger_sessions_start_battery_pct_check CHECK (((start_battery_pct >= 0) AND (start_battery_pct <= 100))),
    CONSTRAINT supercharger_sessions_status_check CHECK ((status = ANY (ARRAY['IN_PROGRESS'::text, 'DONE_CALCULATED'::text, 'DONE'::text])))
);

-- COMMENT: TABLE manual_charge_entries
-- +goose StatementBegin
COMMENT ON TABLE charging.manual_charge_entries IS 'User-asserted home/work/third-party charge sessions not captured by the Tesla Fleet API. Owned by internal/manualcharge; no other module reads this table directly. Mutable table: full CRUD via Writer port (users correct hand-typed entries). No cross-module FK on account_id or tesla_id (ai/architecture.md §2). No raw_data JSONB column: user-typed data has no vendor payload to preserve (ai/go-conventions.md §persistence, design D5).';
-- +goose StatementEnd

-- COMMENT: COLUMN manual_charge_entries.created_by_account_id
COMMENT ON COLUMN charging.manual_charge_entries.created_by_account_id IS 'Which account typed this entry. Authorship only: no query filters, joins, orders or groups by this column, and none may. Reads are per vehicle, so an entry is visible to every account registered to its car. Renamed from account_id, which used to be this table''s tenant key. NOT NULL and not a foreign key -- a cross-module FK into the account module would couple this module''s migrations to that schema.';

-- COMMENT: COLUMN manual_charge_entries.inferred_capacity_kwh_calc
COMMENT ON COLUMN charging.manual_charge_entries.inferred_capacity_kwh_calc IS 'Pack capacity in kWh implied by this entry: energy_added_kwh / ((end_battery_pct - start_battery_pct) / 100), rounded to 3 decimals. GENERATED ALWAYS AS ... STORED -- recomputed by the engine on every INSERT and UPDATE, and unwritable by any caller (design D2). NULL when either percentage is absent or when end_battery_pct is not strictly greater than start_battery_pct -- an equal delta would be a division by zero and a negative delta a negative capacity, neither of which is a physical quantity (design D3). Unconstrained NUMERIC because NUMERIC(8,3) would reject a legal max-energy/min-delta row (design D4). NOT indexed: nothing predicates on it (design D6).';

-- COMMENT: COLUMN manual_charge_entries.status
COMMENT ON COLUMN charging.manual_charge_entries.status IS 'Lifecycle state of this user-asserted charge record: IN_PROGRESS (logged at plug-in time, may lack the end-of-session facts) or DONE (complete). The required-field set is a function of this value and lives in Go, in charging.RequiredFieldsFor -- deliberately NOT as a database CHECK (roadmap D5): a CHECK backstop would turn every future change to the skip set into a migration, working against the ticket''s maintainability requirement. Pre-existing rows backfilled to IN_PROGRESS by this column''s DEFAULT, by the user''s explicit choice, so historical entries surface as unreviewed. Not indexed: nothing predicates on it.';

-- COMMENT: COLUMN manual_charge_entries.energy_source
COMMENT ON COLUMN charging.manual_charge_entries.energy_source IS 'Provenance of energy_added_kwh: USER when the value came from the person, ESTIMATED when this module derived it from the pack capacity and the battery delta on write (roadmap D3/D4). Always computed by internal/charging, never accepted from a caller -- the same shape charging.supercharger_sessions.battery_pct_source already uses. It exists so a future per-vehicle capacity average (backlog #18) can filter WHERE energy_source = ''USER'': inferred_capacity_kwh_calc on an ESTIMATED row returns exactly the capacity constant by algebra, so including such rows would seed that average with its own output. This fact CANNOT be reconstructed later -- once 31.00 is stored, a typed value and a derived one are indistinguishable. Not indexed: nothing predicates on it yet.';

-- COMMENT: COLUMN manual_charge_entries.odometer_km
COMMENT ON COLUMN charging.manual_charge_entries.odometer_km IS 'Odometer reading in kilometres observed AT this charge event -- an observation belonging to the event, not current vehicle state, which is why it lives here and not on a vehicle table (roadmap D6). INTEGER, not NUMERIC(10,1): whole kilometres are what the user reads off the dash (the user chose this over the recommended one-decimal type). The _km suffix is mandatory under the project display-unit rule (ai/go-conventions.md). NULL means not recorded. Not indexed: nothing predicates on it.';

-- COMMENT: COLUMN manual_charge_entries.price_source
COMMENT ON COLUMN charging.manual_charge_entries.price_source IS 'Provenance of price: USER when the amount is known to be real (a positive price, or a caller-confirmed zero), UNCONFIRMED when a zero price has not been confirmed as a real free charge (RM51/MAG-58). Always computed by internal/charging, never accepted from a caller -- the same shape energy_source already uses. Historical rows were backfilled by their price at migration time: positive -> USER, zero -> UNCONFIRMED. Not indexed: nothing predicates on it (design.md Index Plan).';

-- COMMENT: COLUMN manual_charge_entries.start_battery_source
COMMENT ON COLUMN charging.manual_charge_entries.start_battery_source IS 'Provenance of start_battery_pct: USER when the person typed it, ESTIMATED when this module derived it from the energy added and the ending percentage. NULL exactly when start_battery_pct is NULL -- a missing percentage has no provenance. Always computed by internal/charging, never accepted from a caller -- the same shape energy_source and price_source already use. Historical rows were backfilled at migration time: every row with a non-NULL start_battery_pct is USER, because no derivation existed before this column. Not indexed: the one query that filters on it already scans a bounded period range with no supporting index of its own.';

-- COMMENT: TABLE mirror_watermarks
-- +goose StatementBegin
COMMENT ON TABLE charging.mirror_watermarks IS 'One Supercharger-mirror cursor per account (RM44-platform-add-mirror-watermark, MAG-48). Holds the highest telemetry.supercharger_history.updated_at this module''s nightly mirror has already synchronized for that account. No row yet for an account means "epoch": the next mirror run backfills that account''s whole history once. Owned by internal/charging; no other module reads this table directly.';
-- +goose StatementEnd

-- COMMENT: COLUMN mirror_watermarks.source_updated_at
COMMENT ON COLUMN charging.mirror_watermarks.source_updated_at IS 'The maximum updated_at internal/charging''s mirror has observed from telemetry.supercharger_history for this account, as of its last run. The mirror queries SuperchargerHistoryByAccountUpdatedSince(source_updated_at - 24h) and advances this column only when that query returns at least one row -- a run that returns zero rows leaves this column UNTOUCHED (roadmap D5: advancing it to now() on an empty read would permanently and silently lose any row that commits a moment late).';

-- COMMENT: TABLE monthly_effective_capacity
-- +goose StatementBegin
COMMENT ON TABLE charging.monthly_effective_capacity IS 'One measured pack-capacity estimate per vehicle per month (RM52-charging-add-monthly-effective-capacity, MAG-32). Computed monthly from this module''s own valid charge records (manual_charge_entries where energy_source = USER, supercharger_sessions where status = DONE) -- never from ESTIMATED or DONE_CALCULATED rows, whose numbers were themselves derived by dividing by this module''s hardcoded 62.0 constant (roadmap RD2). No account_id: this describes a battery pack, not user data, and one tesla_id pools every account''s rows. Owned exclusively by internal/charging; no other module reads this table directly.';
-- +goose StatementEnd

-- COMMENT: COLUMN monthly_effective_capacity.effective_capacity_kwh
COMMENT ON COLUMN charging.monthly_effective_capacity.effective_capacity_kwh IS 'The measured pack capacity in kWh for this vehicle and month, or NULL when fewer than minSamples (a Go constant, currently 3) valid records survived the minimum-delta gate this period (roadmap RD3/RD4). NULL never means "guessed" -- packCapacityKWh''s read skips NULL rows and reads the newest non-NULL one instead, falling back to a hardcoded 62.0 only when no measured row exists at all for this vehicle.';

-- COMMENT: COLUMN monthly_effective_capacity.candidate_count
COMMENT ON COLUMN charging.monthly_effective_capacity.candidate_count IS 'How many records passed the RD2 filter this period, counted BEFORE the RD3 minimum-delta gate (roadmap RD11, added at the design gate on 2026-09-10 -- design.md D1). Always >= 1 when a row exists -- a vehicle with zero valid records gets no row at all (design.md D2). Compare against sample_count: when the two differ, every record that did not make sample_count was dropped by the delta gate, not missing entirely.';

-- COMMENT: COLUMN monthly_effective_capacity.sample_count
COMMENT ON COLUMN charging.monthly_effective_capacity.sample_count IS 'How many candidate_count records also survived the minDeltaPct gate this period -- the exact slice the median was computed over -- whether or not that count reached minSamples. Written on every run, including a thin one, so a thin month is visible instead of silent (roadmap RD4).';

-- COMMENT: TABLE supercharger_sessions
-- +goose StatementBegin
COMMENT ON TABLE charging.supercharger_sessions IS 'Tesla Supercharger charge sessions as owned by internal/charging: identity, the session time window, the session facts (site, energy, cost, currency, paid state), and the human-owned battery-percentage verification/estimate columns. Dense — one row per session, verified or not (design D2). Deliberately carries NO country_code, unlatch_date_time, billing_type, vehicle_make_type or raw_data (closed list, design D1). Mirrored from telemetry.supercharger_sessions by the nightly orchestrator through public ports only, in the same cycle that refreshes the source; each mirrored column has exactly its source column''s write semantics, so energy_kwh / total_cost / currency / is_paid / tesla_id are refreshed on every pass and everything else mirrored is write-once. The sync path can never write the five percentage columns (design D6). No other module reads this table directly.';
-- +goose StatementEnd

-- COMMENT: COLUMN supercharger_sessions.tesla_id
COMMENT ON COLUMN charging.supercharger_sessions.tesla_id IS 'Currently-registered vehicle id, refreshed on every sync and set NULL when the VIN is not a currently-registered vehicle of the account — the same contract telemetry.supercharger_sessions.tesla_id carries. Resolution is inherited from telemetry, never recomputed here: internal/charging may not import internal/account (design D3).';

-- COMMENT: COLUMN supercharger_sessions.site_location_name
COMMENT ON COLUMN charging.supercharger_sessions.site_location_name IS 'Supercharger site name as Tesla reported it. Write-once: absent from telemetry''s ON CONFLICT DO UPDATE SET, therefore absent from ours (design D1''s rule).';

-- COMMENT: COLUMN supercharger_sessions.energy_kwh
COMMENT ON COLUMN charging.supercharger_sessions.energy_kwh IS 'kWh delivered, derived by telemetry from the session''s fees. NULL when the session had no kWh fee. REFRESHED on every mirror pass — telemetry recomputes it nightly as fees settle.';

-- COMMENT: COLUMN supercharger_sessions.total_cost
-- +goose StatementBegin
COMMENT ON COLUMN charging.supercharger_sessions.total_cost IS 'Total charged for the session, in the currency column''s currency. Monetary amount: no unit suffix by the platform money exemption, paired with currency instead. NULL when the session had no fees. REFRESHED on every mirror pass. DOUBLE PRECISION is copied from telemetry to keep the backfill a literal copy; float is a questionable type for money and converging with manual_charge_entries.price NUMERIC(14,2) will have to reconcile the two.';
-- +goose StatementEnd

-- COMMENT: COLUMN supercharger_sessions.currency
COMMENT ON COLUMN charging.supercharger_sessions.currency IS 'ISO 4217 code for total_cost. NULL when the session had no fees. REFRESHED on every mirror pass.';

-- COMMENT: COLUMN supercharger_sessions.is_paid
COMMENT ON COLUMN charging.supercharger_sessions.is_paid IS 'Whether every fee on the session is settled. NULL when the session had no fees. REFRESHED on every mirror pass — this is the column that most visibly changes after a session ends.';

-- COMMENT: COLUMN supercharger_sessions.start_battery_pct
COMMENT ON COLUMN charging.supercharger_sessions.start_battery_pct IS 'Human-verified battery % at charge start (0-100). NULL = nothing recorded. Never written by the nightly sync — the sync port has no field for it (design D6).';

-- COMMENT: COLUMN supercharger_sessions.end_battery_pct
COMMENT ON COLUMN charging.supercharger_sessions.end_battery_pct IS 'Human-verified battery % at charge end (0-100). Same NULL convention and the same sync-path protection as start_battery_pct.';

-- COMMENT: COLUMN supercharger_sessions.battery_pct_source
COMMENT ON COLUMN charging.supercharger_sessions.battery_pct_source IS 'Provenance of start/end_battery_pct: user_verified (a human entered them) or polled (a future measured-SOC path, not implemented). Required whenever either percentage is set (supercharger_sessions_pct_source_required). Never ''estimated'' — an estimate is computed on read and is never persisted here.';

-- COMMENT: COLUMN supercharger_sessions.inferred_capacity_kwh_calc
COMMENT ON COLUMN charging.supercharger_sessions.inferred_capacity_kwh_calc IS 'Pack capacity in kWh implied by this session: energy_kwh / ((end_battery_pct - start_battery_pct) / 100), rounded to 3 decimals. Same GENERATED ALWAYS AS ... STORED mechanism, same guard, and the same unconstrained NUMERIC type as manual_charge_entries.inferred_capacity_kwh_calc -- the two expressions differ only in the energy IS NOT NULL guard (energy_kwh is nullable here) and the ::NUMERIC cast (energy_kwh is DOUBLE PRECISION here), both forced by the source column (design D4). NULL additionally whenever energy_kwh is NULL, i.e. the session had no kWh fee. This column recomputes when the nightly mirror refreshes energy_kwh AND when a human corrects the percentages through SessionVerifier.VerifySession -- neither write path names this column, and neither has to (design D2).';

-- COMMENT: COLUMN supercharger_sessions.status
COMMENT ON COLUMN charging.supercharger_sessions.status IS 'Lifecycle status of this session''s battery-percentage data, auto-computed by SessionVerifier.VerifySession on every call, never accepted from a caller: IN_PROGRESS when either start_battery_pct or end_battery_pct is NULL, DONE_CALCULATED when both are present and this call derived start_battery_pct via derivedStartBatteryPct rather than storing a caller-supplied value, DONE when both are present and start_battery_pct was supplied directly. Every row that existed before this migration was backfilled to DONE_CALCULATED unconditionally -- an owner decision about data provenance the stored percentages themselves cannot show, not a recompute of this rule against their actual values (see RM41-charging-add-session-status design.md "Rationale"). Not indexed: no read query in this tier filters, orders, or joins by it.';

-- CONSTRAINT: manual_charge_entries manual_charge_entries_pkey
ALTER TABLE ONLY charging.manual_charge_entries
    ADD CONSTRAINT manual_charge_entries_pkey PRIMARY KEY (id);

-- CONSTRAINT: mirror_watermarks mirror_watermarks_pkey
ALTER TABLE ONLY charging.mirror_watermarks
    ADD CONSTRAINT mirror_watermarks_pkey PRIMARY KEY (id);

-- CONSTRAINT: mirror_watermarks mirror_watermarks_vehicle_unique
ALTER TABLE ONLY charging.mirror_watermarks
    ADD CONSTRAINT mirror_watermarks_vehicle_unique UNIQUE (tesla_id);

-- CONSTRAINT: monthly_effective_capacity monthly_effective_capacity_pkey
ALTER TABLE ONLY charging.monthly_effective_capacity
    ADD CONSTRAINT monthly_effective_capacity_pkey PRIMARY KEY (id);

-- CONSTRAINT: monthly_effective_capacity monthly_effective_capacity_tesla_id_effective_period_key
ALTER TABLE ONLY charging.monthly_effective_capacity
    ADD CONSTRAINT monthly_effective_capacity_tesla_id_effective_period_key UNIQUE (tesla_id, effective_period);

-- CONSTRAINT: supercharger_sessions supercharger_sessions_pkey
ALTER TABLE ONLY charging.supercharger_sessions
    ADD CONSTRAINT supercharger_sessions_pkey PRIMARY KEY (id);

-- CONSTRAINT: supercharger_sessions supercharger_sessions_session_id_unique
ALTER TABLE ONLY charging.supercharger_sessions
    ADD CONSTRAINT supercharger_sessions_session_id_unique UNIQUE (session_id);

-- INDEX: idx_manual_charge_entries_vehicle_time
CREATE INDEX idx_manual_charge_entries_vehicle_time ON charging.manual_charge_entries USING btree (tesla_id, charged_on DESC);

-- INDEX: idx_supercharger_sessions_vehicle_stop
CREATE INDEX idx_supercharger_sessions_vehicle_stop ON charging.supercharger_sessions USING btree (tesla_id, charge_stop_date_time);

-- +goose Down
-- +goose StatementBegin
-- Refusing is deliberate. Reversing this file would mean dropping the whole
-- charging schema and every row in it. An empty rollback would be worse: goose
-- would mark the file un-applied while every object still exists, and the next
-- forward run would fail on "relation already exists" -- which stops the web and
-- poller containers, because both wait for the migration step to succeed.
DO $$
BEGIN
    RAISE EXCEPTION 'the charging baseline is not reversible: recreate the database instead of rolling it back';
END
$$;
-- +goose StatementEnd
