-- Seeds the September 2026 gasoline price. Same shape as the August
-- migration -- one row, its own migration. This row makes September use its
-- own price instead of falling back to August's.

-- +goose Up
INSERT INTO reference.fuel_prices (period, currency, price)
VALUES ('2026-09-01', 'COP', 16331.00);

-- +goose Down
DELETE FROM reference.fuel_prices WHERE period = '2026-09-01';
