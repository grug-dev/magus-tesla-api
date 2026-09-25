# reference — Module Agent Identity

Agent-Name: reference

## Doc-Pack (module)

Extends the project base Doc-Pack (`CLAUDE.md` → "Pipeline config") — never replaces it, and
never restates it: the base list lives in `CLAUDE.md` alone, so a copy here cannot drift.
A dispatched worker reads: base pack + this list + this file, before any write.

*(empty — this module needs nothing beyond the base pack. It is backend-only: no HTML, no
Templ, no htmx, no table Tesla data ever touches.)*

## Responsibility

`internal/reference` owns external reference values that belong to no vehicle and no
user — facts the platform needs but never observes from a car or a person. Today it
holds one fact: the price of a gallon of regular gasoline, by calendar month, in COP.

This module:

- Owns the `fuel_prices` table exclusively.
- Stores one national price, one fuel grade — no region, no product split. A future
  regional or per-grade need would add a column later; this module does not carry one
  unused today.
- Provides no way to add or change a price at runtime. Every price is a one-time SQL
  migration, entered by hand. There is no command, no form, no poller.
- Computes and stores nothing derived from the price. A consuming module reads the
  price and does its own arithmetic (a km-per-gallon figure belongs to whichever module
  computes it, not to this one).

## Public Interface

**Port:** `Reader.PricesForMonths(ctx, start, end) ([]MonthPrice, error)`, with a
`NewReader(pool *pgxpool.Pool) Reader` constructor. Signature and the full contract live
in `internal/reference/reference.go` — read it there, it is not copied here.

The result is **sparse**: a requested month with nothing to fall back to (every stored
row is later than it) is simply absent from the result, never a zero-price or
empty-currency entry. A caller must not assume one entry per requested month, and must
never treat absence as a price of zero.

A month with no price of its own resolves to the newest stored price at or before it —
never a later one.

## Allowed Imports / Data Ownership

This module may import:

- `context`, `time`, `fmt`, and other Go standard library packages.
- `github.com/jackc/pgx/v5/pgxpool` — for DB connectivity.
- `github.com/jackc/pgx/v5/pgtype` — ONLY inside `price_reader.go`, the one file that
  talks to the database directly. Never in public types, `reference.go`, or any
  `_test.go` file.
- `internal/reference/db` (package `referencedb`) — ONLY inside `price_reader.go`. The
  generated package is module-private by convention; no other module imports it.

This module MUST NOT import:

- `internal/tesla` — no Fleet API, no Tesla credentials.
- `internal/account` — a gasoline price belongs to no account, so there is nothing to
  scope by user.
- `internal/gateway` — no HTML, no Templ, no handlers.
- Any other module's `db` sub-package.

`internal/reference` is the **sole owner** of `reference.fuel_prices`, in its own
dedicated `reference` Postgres schema. No other module may read or write it directly
(`ai/architecture.md` §2); access goes only through `Reader`.

| Table | Holds | Written by |
|---|---|---|
| `fuel_prices` | One gasoline price per calendar month, in COP | Migrations only — no Go writer exists |

## Testing Notes

This module has no `_test.go` files. Tests were out of scope when it was built. A future test, if this module ever
gains one, must not assert against `pgtype` — assert against `reference.MonthPrice`
domain fields, mirroring every sibling DB-backed module's own rule
(`internal/charging/AGENTS.md` §Testing Notes).
