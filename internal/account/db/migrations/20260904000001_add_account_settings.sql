-- Adds account.settings: one row per account holding every UI preference
-- (language, theme), replacing accounts.language as the platform's only
-- preference storage. RM42-account-add-settings-table tier 1 (MAG-43).
--
-- design.md D1 — typed columns, no key/value (EAV) table: a typed table
-- gives NOT NULL, a real per-field default, and schema discoverability
-- (`\d account.settings` shows both preferences and their defaults
-- directly); an EAV table loses all three and forces a grep of
-- application code to discover what keys even exist.
--
-- design.md D2 — strict order, both directions. Up: CREATE (the table
-- must exist before the backfill can target it) -> INSERT ... SELECT
-- backfill (the column must survive until every value has been copied
-- out of it) -> DROP COLUMN. Down: ADD COLUMN (must exist again before
-- values can be copied into it) -> UPDATE ... FROM (account.settings
-- must still exist when this runs) -> DROP TABLE — the exact reverse.
--
-- design.md D5 — no CHECK constraint on language or theme. Both
-- vocabularies are validated at this module's sole write path
-- (SetLanguage/SetTheme) and re-normalized on every read, in Go, at the
-- DB->domain boundary — mirroring the already-unconstrained `language`
-- column and the still-unconstrained `provider` column on accounts. A
-- CHECK would force a migration ahead of every future vocabulary change.

-- +goose Up
CREATE TABLE account.settings (
    account_id UUID PRIMARY KEY REFERENCES account.accounts(id) ON DELETE CASCADE,
    language   TEXT NOT NULL DEFAULT 'es',
    theme      TEXT NOT NULL DEFAULT 'graphite'
);

INSERT INTO account.settings (account_id, language)
SELECT id, language FROM account.accounts;

ALTER TABLE account.accounts DROP COLUMN language;

-- +goose Down
ALTER TABLE account.accounts ADD COLUMN language TEXT NOT NULL DEFAULT 'es';

UPDATE account.accounts a
SET language = s.language
FROM account.settings s
WHERE s.account_id = a.id;

DROP TABLE account.settings;
