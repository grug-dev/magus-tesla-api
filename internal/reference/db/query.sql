-- name: PricesForMonths :many
-- Returns one row per month in [@start_period, @end_period] that resolves to
-- a stored price: the month's own row when one exists, otherwise the newest
-- row at or before it. generate_series walks every first-of-month between
-- the two (already month-truncated) bounds; the LATERAL subquery behaves as
-- an inner join, so a month with nothing to fall back to -- every stored row
-- is later than it -- drops out of the result instead of returning NULLs.
-- Both bounds are month-truncated here, in SQL, mirroring
-- charging.CapacityForMonth's and analytics.MonthlyMetricsBetween's identical
-- choice to keep month normalization out of Go and away from make tz-guard's
-- reach.
SELECT
    gs.period::date AS period,
    fp.currency,
    fp.price
FROM generate_series(
    date_trunc('month', sqlc.arg(start_period)::date),
    date_trunc('month', sqlc.arg(end_period)::date),
    interval '1 month'
) AS gs(period)
CROSS JOIN LATERAL (
    SELECT currency, price
    FROM reference.fuel_prices
    WHERE period <= gs.period::date
    ORDER BY period DESC
    LIMIT 1
) AS fp
ORDER BY gs.period;
