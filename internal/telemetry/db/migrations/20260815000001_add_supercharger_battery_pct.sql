-- +goose Up
-- internal/telemetry — add the human-owned battery-percentage verification/override
-- trio, plus a frozen verification-time snapshot pair, to supercharger_sessions
-- (MAG-14, RM27-telemetry-add-supercharger-battery-pct, tier 1 of RM27 —
-- openspec/roadmaps/RM27-supercharger-battery-percentage.md).
--
-- start_battery_pct / end_battery_pct / battery_pct_source are NOT model output and
-- NOT derived from the Tesla API (GET /api/1/dx/charging/history carries no SOC field
-- of any kind — verified against the real captured response). They exist purely as a
-- human-owned verification/override channel: NO code in this repository writes them
-- as of this migration. The verification UI that will eventually write them is a
-- separate, out-of-scope future change (R7, backlog entry 11). The estimator that
-- computes a value to show when no override exists lives in internal/battery,
-- computes ON READ, and NEVER persists its result here (R4/R6) — this table has no
-- column for an ongoing/refreshed estimate and never will; NULL on the trio IS the
-- "no override, use the live estimate" state (R5).
--
-- start_battery_pct_est / end_battery_pct_est (design D6) are a DIFFERENT thing from
-- an "estimate column" in the sense R6 forbids: they are written EXACTLY ONCE, in the
-- SAME write that sets the trio, capturing what internal/battery's estimator showed
-- AT THAT MOMENT — a permanent, frozen drift log ("model said 82, human said 79").
-- They are NEVER refreshed afterward, including by a later, improved taper model;
-- staleness relative to a newer model is the CORRECT, intended behavior for a dated
-- observation, not a bug. They are NEVER read back into internal/battery's live
-- computation (never a cache). Do NOT "fix" these into a nightly-refreshed pair in a
-- future change — see design.md D6 for the full distinction and why a
-- nightly-refreshed pair has no legal writer under this project's module-ownership
-- rule, while this write-once pair does (gateway -> battery read, gateway ->
-- telemetry write, both legal, no cycle).
--
-- LOAD-BEARING (R3): all FIVE of these columns are DELIBERATELY ABSENT from
-- UpsertSuperchargerSession's INSERT column list and its ON CONFLICT DO UPDATE SET
-- clause (query.sql) — not merely excluded from the UPDATE half. The poller
-- re-upserts a session nightly because Tesla billing state mutates post-session
-- (is_paid, invoices finalize over time); if any of these five were bound as a
-- parameter of that query, a user's future verified value (or its frozen snapshot)
-- would be silently overwritten on the next nightly re-upsert — the concrete bug
-- this design exists to prevent. They belong on the "immutable, never overwritten"
-- side of this table's own header comment, alongside session_id/vin/timestamps —
-- not the "derived, refreshed on conflict" side energy_kwh/total_cost/is_paid/
-- raw_data are on.
ALTER TABLE supercharger_sessions
    ADD COLUMN start_battery_pct     SMALLINT CHECK (start_battery_pct     BETWEEN 0 AND 100),
    ADD COLUMN end_battery_pct       SMALLINT CHECK (end_battery_pct       BETWEEN 0 AND 100),
    ADD COLUMN battery_pct_source    TEXT     CHECK (battery_pct_source IN ('user_verified', 'polled')),
    ADD COLUMN start_battery_pct_est SMALLINT CHECK (start_battery_pct_est BETWEEN 0 AND 100),
    ADD COLUMN end_battery_pct_est   SMALLINT CHECK (end_battery_pct_est   BETWEEN 0 AND 100);

COMMENT ON COLUMN supercharger_sessions.start_battery_pct IS
    'Human-verified/override battery % at charge start (0-100). NULL = no override; '
    'reads fall back to internal/battery''s on-read estimate (R5). Excluded from '
    'UpsertSuperchargerSession''s INSERT and ON CONFLICT DO UPDATE SET -- never '
    'auto-written by the nightly poller (R3).';
COMMENT ON COLUMN supercharger_sessions.end_battery_pct IS
    'Human-verified/override battery % at charge end (0-100). Same NULL convention '
    'and the same R3 write-protection as start_battery_pct.';
COMMENT ON COLUMN supercharger_sessions.battery_pct_source IS
    'Why start/end_battery_pct are set: user_verified (verification UI, out of scope '
    'this tier) or polled (future measured-SOC alternative, logged to the backlog, '
    'not implemented). NULL means no override exists. Never ''estimated'' -- that '
    'state is computed on read by internal/battery and is never persisted here (R6).';
COMMENT ON COLUMN supercharger_sessions.start_battery_pct_est IS
    'FROZEN, write-once snapshot of internal/battery''s live estimate at the moment '
    'start_battery_pct was verified/overridden -- a permanent drift log entry, not a '
    'cache. Written exactly once, in the same write as the trio; NEVER refreshed '
    'again, including by a later improved taper model (staleness here is correct, '
    'not a bug -- design D6). NEVER read back into internal/battery''s live '
    'computation. Excluded from UpsertSuperchargerSession like the trio (R3).';
COMMENT ON COLUMN supercharger_sessions.end_battery_pct_est IS
    'FROZEN, write-once snapshot of internal/battery''s live estimate at the moment '
    'end_battery_pct was verified/overridden. Same write-once, never-refreshed, '
    'never-a-cache, R3-protected semantics as start_battery_pct_est (design D6).';

COMMENT ON TABLE supercharger_sessions IS
    'Tesla-billed Supercharger and DC fast-charging sessions per account. '
    'Covers sessions returned by GET /api/1/dx/charging/history only (no home/AC '
    'charging). The Tesla API itself carries no battery-percentage field; '
    'start_battery_pct/end_battery_pct/battery_pct_source are a human-owned '
    'verification/override channel, and start_battery_pct_est/end_battery_pct_est '
    'are a frozen write-once snapshot of the estimate at verification time (both '
    'added by RM27 tier 1, MAG-14) -- all five excluded from the nightly UPSERT so '
    'a verified value or its snapshot is never silently overwritten (R3). '
    'Owned by internal/telemetry; no other module reads this table directly. '
    'UPSERT on session_id (not append-only): billing state is mutable post-session.';

-- +goose Down
ALTER TABLE supercharger_sessions
    DROP COLUMN IF EXISTS end_battery_pct_est,
    DROP COLUMN IF EXISTS start_battery_pct_est,
    DROP COLUMN IF EXISTS battery_pct_source,
    DROP COLUMN IF EXISTS end_battery_pct,
    DROP COLUMN IF EXISTS start_battery_pct;
-- Down intentionally does not restore the pre-migration COMMENT ON TABLE text --
-- goose Down migrations in this module have never restored superseded comments
-- (see 20260805000001's and 20260806000001's own Down sections for the same
-- precedent); the comment is documentation, not schema, and a Down that ran this
-- migration's Up at all means the "no battery percentage" claim was already known
-- to be stale.
