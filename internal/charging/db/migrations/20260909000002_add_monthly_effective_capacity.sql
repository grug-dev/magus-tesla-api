-- +goose Up
-- monthly_effective_capacity: one measured pack-capacity estimate per vehicle per
-- month (RM52-charging-add-monthly-effective-capacity, MAG-32, roadmap RD1/RD3/
-- RD4/RD5/RD10/RD11). Owned by internal/charging -- no other module may import the
-- generated chargingdb package (ai/architecture.md §2).
--
-- WHY IN charging, NOT analytics (roadmap RD1/RD14, ai/architecture.md §2 "First
-- ask where the fact belongs, not how to break the cycle"). Every input row --
-- manual_charge_entries.inferred_capacity_kwh_calc and
-- supercharger_sessions.inferred_capacity_kwh_calc -- already belongs to charging.
-- charging owns the derivation and the table for the same reason it owns the rows
-- the derivation reads: a module that could become a separate service owns its
-- data AND the facts derived from it. No cross-module port exists for this table.
--
-- NO account_id (roadmap RD5). This is the only table in charging's schema
-- without account scoping, deliberately: it describes a battery pack, not user
-- data. One tesla_id is one car, whoever registered it -- GROUP BY tesla_id
-- (done in Go, not SQL -- see monthly_capacity.go) pools every account's rows
-- automatically, with no extra code.
--
-- NO FK on tesla_id, and NO raw_data JSONB -- the same two reasons every sibling
-- table in this schema already documents: a cross-module FK into the account
-- module's tables would couple this migration to a schema this module does not
-- own (ai/architecture.md §2), and this table stores a computed conclusion, not a
-- vendor payload, so there is nothing lossless to preserve.
--
-- effective_capacity_kwh IS NULLABLE ON PURPOSE (roadmap RD4). NULL means "under
-- minSamples valid rows this period" -- never a guessed number. packCapacityKWh's
-- read (LatestMeasuredCapacity, below) skips NULL rows and reads the newest
-- non-NULL one instead, falling back to a hardcoded 62.0 only when no measured
-- row exists at all for this vehicle. A stored number therefore always means "we
-- measured this."
--
-- candidate_count and sample_count are NOT NULL, DEFAULT 0, both counted at
-- different stages of the same pipeline -- added together at the owner's
-- design gate on 2026-09-10 (roadmap RD11, design.md D1). candidate_count is
-- every RD2-valid record this month, counted BEFORE the RD3 delta gate;
-- sample_count is the subset that survived the gate and actually produced the
-- median. Without candidate_count, "20 small top-ups, none big enough to
-- measure" and "no charging activity at all" would both read as
-- sample_count 0 -- indistinguishable without querying the source tables by
-- hand. A row exists only when candidate_count >= 1 (design.md D2): the job
-- iterates the tesla_ids it actually observed, so a vehicle with zero valid
-- records that month has no row, never a row with candidate_count = 0.
--
-- effective_period is always the FIRST DAY of the month it summarizes (roadmap
-- RD7), enforced by the CHECK below rather than left to caller discipline.
CREATE TABLE charging.monthly_effective_capacity (
    id                     UUID   PRIMARY KEY DEFAULT gen_random_uuid(),
    tesla_id               BIGINT NOT NULL,
    effective_period       DATE   NOT NULL,

    effective_capacity_kwh DOUBLE PRECISION,
    candidate_count        INTEGER NOT NULL DEFAULT 0,
    sample_count           INTEGER NOT NULL DEFAULT 0,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT monthly_effective_capacity_period_is_month_start
        CHECK (EXTRACT(DAY FROM effective_period) = 1),
    UNIQUE (tesla_id, effective_period)
);

COMMENT ON TABLE charging.monthly_effective_capacity IS
    'One measured pack-capacity estimate per vehicle per month '
    '(RM52-charging-add-monthly-effective-capacity, MAG-32). Computed monthly '
    'from this module''s own valid charge records (manual_charge_entries where '
    'energy_source = USER, supercharger_sessions where status = DONE) -- never '
    'from ESTIMATED or DONE_CALCULATED rows, whose numbers were themselves '
    'derived by dividing by this module''s hardcoded 62.0 constant (roadmap '
    'RD2). No account_id: this describes a battery pack, not user data, and one '
    'tesla_id pools every account''s rows. Owned exclusively by '
    'internal/charging; no other module reads this table directly.';

COMMENT ON COLUMN charging.monthly_effective_capacity.effective_capacity_kwh IS
    'The measured pack capacity in kWh for this vehicle and month, or NULL when '
    'fewer than minSamples (a Go constant, currently 3) valid records survived '
    'the minimum-delta gate this period (roadmap RD3/RD4). NULL never means '
    '"guessed" -- packCapacityKWh''s read skips NULL rows and reads the newest '
    'non-NULL one instead, falling back to a hardcoded 62.0 only when no '
    'measured row exists at all for this vehicle.';

COMMENT ON COLUMN charging.monthly_effective_capacity.candidate_count IS
    'How many records passed the RD2 filter this period, counted BEFORE the '
    'RD3 minimum-delta gate (roadmap RD11, added at the design gate on '
    '2026-09-10 -- design.md D1). Always >= 1 when a row exists -- a vehicle '
    'with zero valid records gets no row at all (design.md D2). Compare '
    'against sample_count: when the two differ, every record that did not '
    'make sample_count was dropped by the delta gate, not missing entirely.';

COMMENT ON COLUMN charging.monthly_effective_capacity.sample_count IS
    'How many candidate_count records also survived the minDeltaPct gate this '
    'period -- the exact slice the median was computed over -- whether or not '
    'that count reached minSamples. Written on every run, including a thin '
    'one, so a thin month is visible instead of silent (roadmap RD4).';

-- Index Plan (roadmap RD11): no separate CREATE INDEX. The UNIQUE (tesla_id,
-- effective_period) constraint's own btree serves both operations completely --
-- equality on the leading column (tesla_id) then a backwards range scan on the
-- second (effective_period DESC) for the read below, and the exact conflict
-- target for the upsert:
--
--   SELECT effective_capacity_kwh
--     FROM charging.monthly_effective_capacity
--    WHERE tesla_id = $1 AND effective_capacity_kwh IS NOT NULL
--    ORDER BY effective_period DESC
--    LIMIT 1;
--
--   INSERT INTO charging.monthly_effective_capacity (...) VALUES (...)
--   ON CONFLICT (tesla_id, effective_period) DO UPDATE SET ...;
--
-- Mirrors how vehicle_metrics and charge_gaps each reason about their own
-- UNIQUE constraint (roadmap RD11).

-- +goose Down
DROP TABLE IF EXISTS charging.monthly_effective_capacity;
