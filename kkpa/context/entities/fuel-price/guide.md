# reference.fuel_prices — maintenance guide

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.
> All KB links are relative to `kkpa/context/`.

## Glossary

- **Known as:** `fuel price`, `gasoline price`, `precio de la gasolina`, `reference values`
- **Internal name:** `reference.Reader.PricesForMonths` / table `reference.fuel_prices` (module `internal/reference`)

## Component map

Files involved, grouped by layer. Each row: the file's role in this concept.

### Back-end / persistence (`internal/reference`)

| File | Role |
|---|---|
| `internal/reference/reference.go` | The `Reader` port, `MonthPrice`, `NewReader`. No `pgtype` here. |
| `internal/reference/price_reader.go` | `priceReader` — maps `referencedb.PricesForMonthsRow` to `MonthPrice`. The only file that imports `pgtype`. |
| `internal/reference/db/query.sql` | `PricesForMonths` — `generate_series` + `CROSS JOIN LATERAL` newest-at-or-before lookup. |
| `internal/reference/db/migrations/` | Schema-only baseline, then one migration per priced month. |

### Config / tooling

| File | Role |
|---|---|
| `sqlc.yaml` | `referencedb` entry. |
| `Makefile` | `MIGRATION_MODULES` lists `reference`. |
| `internal/config/config.go` | `defaultMigrationModules` lists `reference`. |
| `deploy/docker/Dockerfile` | `COPY` of the migrations into `/migrations/reference`. |

## How maintenance works

- **Storage.** One gasoline price per calendar month, keyed by the first day of the month,
  with the currency it is expressed in. One national price, regular gasoline only.
- **Create / update.** There is no command, form, or process. Each new price is a one-time
  data change, entered by hand. A second price for the same month is rejected.
- **Read.** A caller asks for a range of months in one request. Each month resolves on its
  own: its own price if one exists, otherwise the newest price at or before it. A later
  price never affects an earlier month.
- **Missing months.** A month before every recorded price resolves to no price. It is absent
  from the result — never present with a zero or empty value.

## Conventions & gotchas

- **At most one price per calendar month.** A second price for the same month is rejected,
  and the existing one stays.
  _Source: spec reference — Requirement: A Gasoline Price Is Stored Per Calendar Month._
- **No writer exists on purpose.** Do not add a command, form, or job to change a price.
  Each price is a hand-entered, one-time data change.
  _Source: spec reference — Requirement: A Gasoline Price Is Stored Per Calendar Month._
- **No region, no fuel product.** One price covers the whole country and regular gasoline only.
  _Source: spec reference — Requirement: A Gasoline Price Is Stored Per Calendar Month._
- **Fallback only looks back.** A month with no price uses the newest earlier price, never a
  later one. Adding a new price shifts every later month that fell back past it.
  _Source: spec reference — Requirement: A Month With No Price Resolves To The Newest Earlier Price._
- **Never invent a default price.** A month before every recorded price has no price. The
  caller must not treat that as zero.
  _Source: spec reference — Requirement: A Month With No Price Resolves To The Newest Earlier Price._
- **The range result is sparse.** Months with no resolvable price are absent. A caller must not
  assume one entry per requested month.
  _Source: spec reference — Requirement: A Range Of Months Resolves In One Request._

## Related KB

- Module brief: `internal/reference/AGENTS.md`
- Spec: `openspec/specs/reference/spec.md`
