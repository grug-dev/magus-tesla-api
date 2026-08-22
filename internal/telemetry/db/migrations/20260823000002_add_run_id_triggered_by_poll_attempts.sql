-- +goose Up
-- poll_attempts gains two columns recording WHICH invocation of app.ProcessVehicleData
-- wrote each attempt, and what triggered that invocation (RM29 tier 7, roadmap D2 as
-- SUPERSEDED below — design.md D1).
--
-- WHY THIS STAYS HERE INSTEAD OF MOVING TO A NEW MODULE. Roadmap D2 said this table
-- moves to internal/app as process_runs, justified by "triggered_by is not something
-- Tesla reported." That purity test is right for vehicle_snapshots (RM29 violation #1,
-- fixed by tier 4) and wrong for this table: poll_attempts never held anything Tesla
-- reported to begin with — attempted_at is our own clock, outcome/reason are our own
-- classification of our own API call. triggered_by is the same kind of fact as every
-- column already here, not a boundary violation (design.md D1).
--
-- GRAIN IS UNCHANGED: one row per (vehicle, run), as this table's own original comment
-- already said (see this file's own header, migration 20260710000002). run_id
-- correlates every vehicle's row from one ProcessVehicleData invocation; a caller
-- wanting run-level facts computes them with GROUP BY run_id over this table — no
-- separate run-level table is added (design.md D2).
--
-- run_id is NULLABLE and PERMANENTLY UNBACKFILLED: a legacy row's run identity was
-- never recorded and cannot be recovered, so it stays NULL rather than being assigned a
-- fabricated value. triggered_by is NOT NULL DEFAULT 'scheduler': every row written
-- before this migration was written by the only entry point that existed — the
-- scheduled nightly poller — so the default backfills every existing row correctly, in
-- one metadata-only ALTER with no data migration and no invented value.
--
-- NO CHECK CONSTRAINT on triggered_by: the typed Go constant (telemetry.TriggeredBy —
-- "scheduler" | "api") guards every value this codebase can ever write; a DB CHECK
-- would only guard a hand-written SQL statement outside Go, which nothing in this
-- project does (mirrors the reasoning charging.SessionMirror's compile-time protection
-- used in RM29 tier 6, design.md D6 there).
--
-- NO INDEX on run_id or triggered_by: nothing reads either column in this tier (see
-- this design's Index Plan). The table gains roughly one row per registered vehicle
-- per night, so adding an index later — if a read ever needs one — costs nothing lost
-- by waiting. Same call as RM29 tier 6's I6 on a comparably speculative surface.
ALTER TABLE poll_attempts
    ADD COLUMN run_id       UUID,
    ADD COLUMN triggered_by TEXT NOT NULL DEFAULT 'scheduler';

COMMENT ON COLUMN poll_attempts.run_id IS
    'Correlates every vehicle''s attempt row from one app.ProcessVehicleData '
    'invocation. Generated once per invocation by internal/app (uuid.New()) and '
    'passed down via telemetry.RunContext (design.md D5). NULL on every row written '
    'before this migration — that run''s identity was never recorded and is not '
    'recoverable; never backfilled, never will be.';
COMMENT ON COLUMN poll_attempts.triggered_by IS
    'What triggered the run that wrote this attempt: scheduler (the nightly poller, '
    'including cmd/poller --once) or api (a future manual re-run, RM29 tier 8, '
    'parked). NOT NULL DEFAULT ''scheduler'' backfills every pre-migration row '
    'correctly, since no non-scheduler entry point existed before this tier. Guarded '
    'by the typed Go constant telemetry.TriggeredBy — no DB CHECK (design.md D1/D7).';

-- +goose Down
ALTER TABLE poll_attempts
    DROP COLUMN triggered_by,
    DROP COLUMN run_id;
