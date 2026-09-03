-- +goose Up
-- RM39 tier 1 (account-move-to-own-schema, MAG-31): move this module's three tables into a
-- dedicated `account` Postgres schema, additive to existing history (roadmap D1). No table,
-- column, index, or constraint is renamed or altered — this migration is pure namespacing so
-- the modular-monolith boundary (ai/architecture.md §2 "no cross-module database leaks")
-- becomes visible in the database catalog, not just enforced by Go import guards. `ALTER
-- TABLE … SET SCHEMA` is catalog-only (see design.md's index plan). Because migrations run
-- as the app role (Makefile db-setup exports PGUSER=$(APP_ROLE)), CREATE SCHEMA here makes
-- that role the schema owner — no GRANT needed.

CREATE SCHEMA IF NOT EXISTS account;

ALTER TABLE accounts     SET SCHEMA account;
ALTER TABLE tesla_tokens SET SCHEMA account;
ALTER TABLE vehicles     SET SCHEMA account;

-- +goose Down
-- Reverse in the OPPOSITE order of Up: move every table back to public first, then drop the
-- now-empty schema.
ALTER TABLE account.vehicles     SET SCHEMA public;
ALTER TABLE account.tesla_tokens SET SCHEMA public;
ALTER TABLE account.accounts     SET SCHEMA public;

DROP SCHEMA IF EXISTS account;
