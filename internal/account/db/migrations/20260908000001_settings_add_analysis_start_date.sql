-- Adds account.settings.analysis_start_date: the first calendar day
-- (America/Bogota) the platform analyzes an account's vehicle data.
-- RM49-account-add-analysis-start-date tier 1 (MAG-55).
--
-- design.md D1 — why account.settings, not accounts or vehicles. accounts
-- holds identity (email, provider, display name, status);
-- analysis_start_date is policy, not identity. account.settings is the
-- one settled home for per-account policy (RM42 D3) — a second table for
-- policy would reopen "which table do I use?" for every future setting.
-- A per-vehicle column (on vehicles) is more correct once a user connects
-- a second Tesla, but the platform is one-car-per-account today; revisit
-- when real multi-vehicle support lands.
--
-- design.md D2 — strict three-step order, no DEFAULT. The column cannot
-- start NOT NULL: existing rows have no value yet. So: ADD COLUMN
-- (nullable) -> UPDATE ... FROM backfill -> SET NOT NULL. No DEFAULT in
-- either direction — a DEFAULT CURRENT_DATE would resolve in the database
-- session's time zone at write time, which is almost never
-- America/Bogota. That is the same bug this project's America/Bogota
-- rule and make tz-guard exist to prevent on the Go side; future signups
-- instead pass the date explicitly from internal/clock (design.md D5).
--
-- The "AT TIME ZONE 'America/Bogota'" cast is load-bearing, not
-- decorative. created_at is TIMESTAMPTZ (an instant, no zone attached).
-- Casting it straight to DATE would use the database session's zone,
-- which is not America/Bogota by default. Casting through
-- "AT TIME ZONE 'America/Bogota'" first computes the correct calendar
-- day. Example: created_at = 2026-01-15 04:59:00+00 is
-- 2026-01-14 23:59:00 in Bogota (UTC-5), so analysis_start_date must be
-- 2026-01-14 — one day earlier than a naive created_at::date cast would
-- give. Full worked examples: design.md "Test Contract" items 1-3.

-- +goose Up
ALTER TABLE account.settings ADD COLUMN analysis_start_date DATE;

UPDATE account.settings s
SET analysis_start_date = (a.created_at AT TIME ZONE 'America/Bogota')::date
FROM account.accounts a
WHERE a.id = s.account_id;

ALTER TABLE account.settings ALTER COLUMN analysis_start_date SET NOT NULL;

-- +goose Down
ALTER TABLE account.settings DROP COLUMN analysis_start_date;
