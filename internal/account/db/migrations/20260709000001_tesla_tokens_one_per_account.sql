-- +goose Up
-- Collapse tesla_tokens to one connection per account. Reconnecting now replaces the
-- stored tokens in place (UpsertTeslaToken) instead of appending a row, so the table
-- must hold at most one row per account_id.
--
-- First drop any accumulated duplicates, keeping the most-recently-updated row per
-- account, then enforce it with a UNIQUE constraint. The constraint's implicit index
-- supersedes the old idx_tesla_tokens_account_id.

DELETE FROM tesla_tokens t
USING tesla_tokens newer
WHERE t.account_id = newer.account_id
  AND (newer.updated_at, newer.id) > (t.updated_at, t.id);

DROP INDEX IF EXISTS idx_tesla_tokens_account_id;
ALTER TABLE tesla_tokens ADD CONSTRAINT tesla_tokens_account_id_key UNIQUE (account_id);

-- +goose Down
ALTER TABLE tesla_tokens DROP CONSTRAINT IF EXISTS tesla_tokens_account_id_key;
CREATE INDEX idx_tesla_tokens_account_id ON tesla_tokens (account_id);
