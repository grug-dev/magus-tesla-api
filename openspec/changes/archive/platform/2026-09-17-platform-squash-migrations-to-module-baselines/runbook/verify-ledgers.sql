-- Read every module's version ledger, plus the old shared one.
--
-- Run it after stamping, and again after `make migrate-up`. Expected:
--
--   right after the stamp        each module: filas = 2, maximo = 20260917000001
--   after migrate-up            telemetry:   filas = 3, maximo = 20260917000002
--                               the other three unchanged
--
-- public.goose_db_version must read 55 / 20260915000001 at every point. It is not
-- touched by this change and is the rollback path: the previous image reads it.

            SELECT 'account'   AS modulo, count(*) AS filas, max(version_id) AS maximo FROM account.goose_db_version
  UNION ALL SELECT 'telemetry',           count(*),           max(version_id)          FROM telemetry.goose_db_version
  UNION ALL SELECT 'charging',            count(*),           max(version_id)          FROM charging.goose_db_version
  UNION ALL SELECT 'analytics',           count(*),           max(version_id)          FROM analytics.goose_db_version
  UNION ALL SELECT 'public (old)',        count(*),           max(version_id)          FROM public.goose_db_version
  ORDER BY 1;

-- The deliberate schema change: this must return ZERO rows once 20260917000002 has
-- run. Before it runs, it returns one row.
SELECT column_name
  FROM information_schema.columns
 WHERE table_schema = 'telemetry'
   AND table_name   = 'vehicle_snapshots'
   AND column_name  = 'account_id';
