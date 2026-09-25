-- reference baseline.
--
-- One self-contained file that creates this module's whole schema. It
-- creates objects and reads nothing, so it depends on no other module and
-- can be applied in any order relative to them. Keep it that way: a
-- migration here must never name another module's schema.
--
-- Existing databases (dev and prod) have this version recorded as applied
-- without it ever running. So a change made HERE reaches new databases only.
-- Anything that must also reach dev and prod belongs in a later, ordinary
-- migration.

-- +goose Up

-- SCHEMA: reference
CREATE SCHEMA IF NOT EXISTS reference;

-- TABLE: fuel_prices
CREATE TABLE reference.fuel_prices (
    id         uuid            DEFAULT gen_random_uuid() NOT NULL,
    period     date            NOT NULL,
    currency   text            NOT NULL,
    price      numeric(14,2)   NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,

    CONSTRAINT fuel_prices_pkey PRIMARY KEY (id),
    CONSTRAINT fuel_prices_period_unique UNIQUE (period),
    CONSTRAINT fuel_prices_period_is_month_start CHECK (EXTRACT(day FROM period) = 1)
);

-- COMMENT: TABLE fuel_prices
COMMENT ON TABLE reference.fuel_prices IS 'One gasoline price per calendar month, in COP. Belongs to no vehicle and no user -- it is an external reference value, read to turn charging cost into a km-per-gallon comparison. Every row is entered by hand, in its own migration, never by a running process. Owned exclusively by internal/reference; no other module reads this table directly.';

-- COMMENT: COLUMN fuel_prices.period
COMMENT ON COLUMN reference.fuel_prices.period IS 'First day of the calendar month this price applies to. CHECK-enforced to always be a month start. Always written literally by the migration that seeds it -- there is no runtime writer for this table.';

-- COMMENT: COLUMN fuel_prices.currency
COMMENT ON COLUMN reference.fuel_prices.currency IS 'The currency price is expressed in. COP today and for the foreseeable future. No DEFAULT: every row is written by an explicit migration that always names it.';

-- COMMENT: COLUMN fuel_prices.price
COMMENT ON COLUMN reference.fuel_prices.price IS 'Price of one gallon of regular gasoline, in currency. NUMERIC(14,2), never a unit suffix -- this is the platform monetary exemption, paired with currency instead of a suffix.';

-- COMMENT: COLUMN fuel_prices.created_at
COMMENT ON COLUMN reference.fuel_prices.created_at IS 'Set once, on the row''s first INSERT. This table has no upsert path: every row is written once, by its own seeding migration, and never updated.';

-- COMMENT: COLUMN fuel_prices.updated_at
COMMENT ON COLUMN reference.fuel_prices.updated_at IS 'Present for uniformity with every sibling table. No writer in this module ever changes an existing row, so it equals created_at for the life of a row.';

-- +goose Down
-- +goose StatementBegin
-- Refusing is deliberate. Reversing this file would mean dropping the whole
-- reference schema and every row in it. An empty rollback would be worse:
-- goose would mark the file un-applied while every object still exists, and
-- the next forward run would fail on "relation already exists" -- which
-- stops the web and poller containers, because both wait for the migration
-- step to succeed.
DO $$
BEGIN
    RAISE EXCEPTION 'the reference baseline is not reversible: recreate the database instead of rolling it back';
END
$$;
-- +goose StatementEnd
