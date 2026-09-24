-- Seeds the August 2026 gasoline price. This is the first of the
-- one-migration-per-month price loads this module uses going forward --
-- every later month's price takes this exact same shape, not a special case.

-- +goose Up
INSERT INTO reference.fuel_prices (period, currency, price)
VALUES ('2026-08-01', 'COP', 16000.00);

-- +goose Down
DELETE FROM reference.fuel_prices WHERE period = '2026-08-01';
