-- One-off cleanup (MAG-72). Removes orphan telemetry.vehicle_snapshots rows.
--
-- Where they came from: an analytics integration test inserted snapshot rows
-- but deleted only its own analytics rows, so the snapshots stayed. Those runs
-- reached the real dev database because the test helper used to read
-- DATABASE_URL. Both causes are fixed now, so this script is needed once.
--
-- A row is orphan when its tesla_id is not registered in account.vehicles.
--
-- BACK UP THE DATABASE BEFORE RUNNING THIS. It deletes rows and cannot be
-- undone without a backup.

-- Count before deleting.
SELECT count(*) AS orphan_count_before
FROM telemetry.vehicle_snapshots s
WHERE NOT EXISTS (
    SELECT 1 FROM account.vehicles v WHERE v.tesla_id = s.tesla_id
);

DELETE FROM telemetry.vehicle_snapshots s
WHERE NOT EXISTS (
    SELECT 1 FROM account.vehicles v WHERE v.tesla_id = s.tesla_id
);

-- Count after deleting — expect 0.
SELECT count(*) AS orphan_count_after
FROM telemetry.vehicle_snapshots s
WHERE NOT EXISTS (
    SELECT 1 FROM account.vehicles v WHERE v.tesla_id = s.tesla_id
);
