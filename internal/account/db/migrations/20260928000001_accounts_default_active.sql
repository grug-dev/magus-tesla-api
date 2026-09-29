-- New accounts start Active. Until now a first Google sign-in created an
-- Inactive account (RM34 decision D2, the invite gate), and someone had to
-- activate it by hand before that person could log in.
--
-- Only the column default changes. Accounts that are Inactive today stay
-- Inactive; activate them by hand if you want them in. The login check
-- (rejectIfInactive) is unchanged, so setting an account to Inactive still
-- blocks it.

-- +goose Up
ALTER TABLE account.accounts ALTER COLUMN status SET DEFAULT 'Active';

-- +goose Down
ALTER TABLE account.accounts ALTER COLUMN status SET DEFAULT 'Inactive';
