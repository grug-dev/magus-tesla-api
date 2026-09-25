# Sync proposal — reference

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `entities/fuel-price/guide.md`
Source spec:  `openspec/specs/reference/spec.md`
Generated:    `2026-09-25`
Status: APPLIED 2026-09-25

---

## [guide] ## Glossary — REPLACE

- **Known as:** `fuel price`, `gasoline price`, `precio de la gasolina`, `reference values`
- **Internal name:** `reference.Reader.PricesForMonths` / table `reference.fuel_prices` (module `internal/reference`)

## [guide] ## How maintenance works — REPLACE

- **Storage.** One gasoline price per calendar month, keyed by the first day of the month,
  with the currency it is expressed in. One national price, regular gasoline only.
- **Create / update.** There is no command, form, or process. Each new price is a one-time
  data change, entered by hand. A second price for the same month is rejected.
- **Read.** A caller asks for a range of months in one request. Each month resolves on its
  own: its own price if one exists, otherwise the newest price at or before it. A later
  price never affects an earlier month.
- **Missing months.** A month before every recorded price resolves to no price. It is absent
  from the result — never present with a zero or empty value.

## [guide] ## Conventions & gotchas — APPEND

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

## [index] ## Glossary & routing — entities — ADD ROWS

| `fuel price` | `reference.Reader.PricesForMonths` / `reference.fuel_prices` | entity | `entities/fuel-price/guide.md` |
| `gasoline price` | synonym of `fuel price` | entity | `entities/fuel-price/guide.md` |
| `precio de la gasolina` | synonym of `fuel price` (ES) | entity | `entities/fuel-price/guide.md` |
| `reference values` | module `internal/reference` — external values owned by no vehicle and no user; today only `fuel_prices` | entity | `entities/fuel-price/guide.md` |
