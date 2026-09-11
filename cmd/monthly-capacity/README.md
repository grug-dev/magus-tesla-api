# monthly-capacity

On-demand tool that measures one calendar month's effective pack capacity, for one
vehicle or every vehicle. It calls `charging.MonthlyCapacityCalculator` — the same port
the nightly cycle calls once a month — and prints a summary line.

## Local only

This tool is **not** part of the deployed production image. It is run by hand, against
a database an operator can reach directly. There is no plan to add it to the Docker
build.

## Prerequisites

- `DATABASE_URL` set, in `.env` or the real environment. No Tesla credentials needed —
  this tool never calls the Fleet API and wakes no car.

## Running it

```bash
go run ./cmd/monthly-capacity                            # previous month, every vehicle
go run ./cmd/monthly-capacity -period 2026-08             # one month, every vehicle
go run ./cmd/monthly-capacity -tesla-id 123               # previous month, one vehicle
go run ./cmd/monthly-capacity -period 2026-08 -tesla-id 123

# Or via the Makefile
make cmd-monthly-capacity
make cmd-monthly-capacity PERIOD=2026-08 TESLA_ID=123
```

## Flags

| Flag | Default | Meaning |
|------|---------|---------|
| `-period` | previous calendar month | A calendar month, `YYYY-MM` (e.g. `2026-08`). |
| `-tesla-id` | every vehicle | One vehicle's Tesla id. When left out, the tool measures every vehicle with at least one usable record for the period. |

## Output

On success, one line to stdout:

```
monthly capacity: period 2026-08: 3 vehicle(s) found, 2 measured, 1 thin
```

- **`measured`** — vehicles that got a real, non-empty capacity number.
- **`thin`** — vehicles with a row written, but not enough evidence for a number.

Any error (a bad `-period` value, a database problem, or a calculation error) prints to
stderr and the tool exits with code `1`.
