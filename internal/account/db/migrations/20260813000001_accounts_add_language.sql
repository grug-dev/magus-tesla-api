-- Adds the per-account language preference (design.md D1, RM24-account-add-language-preference).
--
-- NOT NULL DEFAULT 'es': unlike the nullable access_type/vehicle_config precedents (which model
-- "not yet observed from the external API"), a language preference has no such gap — the product
-- default IS 'es' from the moment the row exists, so every existing row can take it truthfully
-- with zero backfill.
--
-- No CHECK constraint: the {es, en} vocabulary is validated in Go at the module's sole write path
-- (Service.SetLanguage) and re-normalized on every read (Service.LanguageFor), mirroring the
-- already-unconstrained `provider` column on this same table. A CHECK would force a migration to
-- ship ahead of the Go code whenever a third locale is added, for safety the single writer already
-- provides.
--
-- No index (design.md D2): every read/write locates its row by primary key `id` or the existing
-- (provider, provider_id) unique constraint — `language` is never a WHERE/JOIN/ORDER BY predicate.

-- +goose Up
ALTER TABLE accounts
    ADD COLUMN language TEXT NOT NULL DEFAULT 'es';

-- +goose Down
ALTER TABLE accounts
    DROP COLUMN IF EXISTS language;
