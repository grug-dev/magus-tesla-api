-- +goose Up
-- RM51 tier 1 (MAG-58): a manual charge entry gains a price provenance marker,
-- distinguishing a real zero-cost charge from a price nobody entered.
--
-- WHY TWO STATEMENTS, NOT ONE (roadmap RD3) -- mirrors 20260720000001's
-- backfill-then-constrain shape, NOT 20260829000002's single-statement shape.
-- 20260829000002 could use one ADD COLUMN ... DEFAULT ... because its backfill
-- value equalled the column DEFAULT for every existing row. Here it does not: the
-- correct backfill value depends on each row's OWN price (positive -> USER, zero
-- -> UNCONFIRMED), so a DEFAULT alone cannot express it. The ADD COLUMN carries the
-- fail-closed default (UNCONFIRMED) for any future insert that omits the column;
-- a separate UPDATE promotes the rows whose historical price was actually typed.
--
-- WHY TEXT + CHECK, NOT A POSTGRES ENUM (roadmap RD3). Same reasoning
-- 20260829000002 already gave for status/energy_source: this table already models
-- location_kind, charging_type, status and energy_source this way, and an enum's
-- value set is painful to widen later (ALTER TYPE ... ADD VALUE cannot run inside a
-- transaction block before PG12, and a value can never be removed).
--
-- WHY 'UNCONFIRMED' IS THE DEFAULT, NOT 'USER' (roadmap RD3). It fails closed: a
-- future insert that forgets this column then honestly says "nobody vouched for
-- this price" rather than falsely claiming it is real. USER as the default would
-- claim the opposite.
--
-- WHY THE BACKFILL SPLITS ON price > 0, NOT ON WHETHER price WAS "EVER SUPPLIED"
-- (roadmap RD3) -- there is no such flag to split on. A historical positive price
-- was typed by a person, so USER is true. A historical zero was never confirmed one
-- way or the other before this column existed, so UNCONFIRMED is honest -- and it
-- surfaces those old rows as worth a second look, the same spirit as
-- 20260829000002's IN_PROGRESS backfill.
--
-- COST: the ADD COLUMN is METADATA-ONLY on PostgreSQL 11+ -- a non-volatile constant
-- DEFAULT is stored in the catalogue since PG11, not written into every row. The
-- UPDATE rewrites only the rows it touches (every row with price > 0) and takes no
-- stronger a lock than any ordinary UPDATE on this table already takes.
--
-- SCHEMA-QUALIFIED, unlike design.md's literal DDL text: manual_charge_entries has
-- lived in the charging schema since 20260902000003_move_charging_to_own_schema.sql,
-- and every migration filed after that move (20260903000004, 20260906000002)
-- qualifies its table name the same way -- the default search_path ("$user", public)
-- never includes charging, so a bare name here would resolve to a public table that
-- no longer exists and fail at migrate-up time. This is a schema-qualification fix,
-- not a change to any type, default, constraint, or backfill rule design.md D2
-- specifies.

ALTER TABLE charging.manual_charge_entries
    ADD COLUMN price_source TEXT NOT NULL DEFAULT 'UNCONFIRMED'
        CHECK (price_source IN ('USER','UNCONFIRMED'));

UPDATE charging.manual_charge_entries SET price_source = 'USER' WHERE price > 0;

COMMENT ON COLUMN charging.manual_charge_entries.price_source IS
    'Provenance of price: USER when the amount is known to be real (a positive '
    'price, or a caller-confirmed zero), UNCONFIRMED when a zero price has not been '
    'confirmed as a real free charge (RM51/MAG-58). Always computed by '
    'internal/charging, never accepted from a caller -- the same shape energy_source '
    'already uses. Historical rows were backfilled by their price at migration time: '
    'positive -> USER, zero -> UNCONFIRMED. Not indexed: nothing predicates on it '
    '(design.md Index Plan).';

-- +goose Down
-- LOSSY, not guarded -- unlike 20260829000002's DROP NOT NULL restoration, there is
-- no CHECK or NOT NULL this Down could fail to satisfy, so DROP COLUMN always
-- succeeds without a blocking guard.
--
-- The loss is real but bounded. Every price > 0 row's provenance is trivially
-- recomputable by re-running the Up migration's own backfill rule. A ZERO-price row
-- a caller confirmed as real AFTER this Up ran is a different story: once dropped,
-- it is indistinguishable from a row that was always unconfirmed, because no other
-- column carries that fact. Accepted: a schema rollback is not a data rollback, the
-- same acceptance 20260720000001 already recorded for its location_kind backfill.
ALTER TABLE charging.manual_charge_entries DROP COLUMN IF EXISTS price_source;
