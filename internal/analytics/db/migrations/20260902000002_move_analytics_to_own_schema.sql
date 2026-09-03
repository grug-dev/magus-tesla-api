-- +goose Up
-- RM39 tier 2 (analytics-move-to-own-schema, MAG-31): move this module's three tables
-- into a dedicated `analytics` Postgres schema, additive to existing history (roadmap
-- D1). No table, column, index, or constraint is renamed or altered — this migration is
-- pure namespacing so the modular-monolith boundary (ai/architecture.md §2 "no
-- cross-module database leaks") becomes visible in the database catalog, not just
-- enforced by Go import guards. `ALTER TABLE … SET SCHEMA` is catalog-only: no row,
-- index, or constraint is rewritten (see design.md's index plan, mirroring tier 1's
-- proof for account). Because migrations run as the app role (Makefile db-setup exports
-- PGUSER=$(APP_ROLE)), CREATE SCHEMA here makes that role the schema owner — no GRANT
-- needed.
--
-- vehicle_metric_watermarks.source is UNTOUCHED by this migration: its stored values
-- ('vehicle_snapshots', 'charge_sessions', 'manual_charge_entries') and its CHECK
-- constraint are DATA, naming other modules' tables by convention, not references this
-- migration's own DDL needs to resolve — schema-qualifying data would corrupt it, not
-- namespace it (roadmap tier 2 note, D8 note).

CREATE SCHEMA IF NOT EXISTS analytics;

ALTER TABLE vehicle_metrics           SET SCHEMA analytics;
ALTER TABLE vehicle_metric_watermarks SET SCHEMA analytics;
ALTER TABLE charge_gaps               SET SCHEMA analytics;

-- +goose Down
-- Reverse in the OPPOSITE order of Up: move every table back to public first, then drop
-- the now-empty schema. A non-empty schema cannot be dropped without CASCADE, and this
-- ordering means CASCADE is never needed — DROP SCHEMA only ever runs against an empty
-- schema.
ALTER TABLE analytics.charge_gaps               SET SCHEMA public;
ALTER TABLE analytics.vehicle_metric_watermarks SET SCHEMA public;
ALTER TABLE analytics.vehicle_metrics           SET SCHEMA public;

DROP SCHEMA IF EXISTS analytics;
