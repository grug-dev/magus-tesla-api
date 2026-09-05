# Sync proposal — gateway

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `workflows/supercharger-stats-read.md`
Source spec:  `openspec/specs/gateway/spec.md`
Generated:    `2026-09-04`
Status: PENDING REVIEW

> **Routing note.** The `gateway` capability spec is broad, but this archive's delta is entirely
> about the Supercharger Stats page, which resolves to the existing `workflows/` guide. This
> proposal extends that guide only. It creates no second entry and does not move the file.

> **Origin.** `RM41-gateway-add-session-status-column` (roadmap RM41 tier 5, ticket MAG-45),
> archived 2026-09-04. The archive synced +2 added / ~1 modified requirements into the capability
> spec: "Supercharger Stats session status badge", "Supercharger Stats in-progress session
> guidance", and a modification to "Supercharger Stats session table displays battery percentages".

> **This is a NARROW delta on purpose — most of the spec is already in the guide.** Tier 5's own
> task 5.1 updated `workflows/supercharger-stats-read.md` in the same change (component map,
> glossary, and a Status-badge gotcha bullet covering the Kind mapping, the never-`success`/
> `warning` rule, the no-extra-read fact, and the second Alert). Those are deliberately NOT
> repeated below. What remains are three rules the spec states that the guide does not yet carry
> — each one a thing a future agent would otherwise "fix" by adding something the design
> deliberately excluded.

---

## [guide] ## Conventions & gotchas — APPEND

- **This page's status badge deliberately has NO `ui.Dot` beside it, unlike `/external-charges`.** `external_charge_row.templ` pairs its badge with a completeness Dot; the Supercharger row does not, and the spec forbids adding one. The Dot on `/external-charges` signals a *separate* completeness idea from the lifecycle status; here the status already is the completeness signal, so a Dot would restate it in a second colour vocabulary. Do not "finish the mirror" by adding one.
  _Source: spec gateway — Requirement: Supercharger Stats session status badge._
- **`Status` is read-only from the gateway's side — no route may set or change it.** It is always computed by `charging.SessionVerifier.VerifySession`. The inline edit form carries no status field and must never gain one: the gateway supplies the two percentages, and the status follows from them inside `charging`. A gateway-supplied status would let the UI contradict the record.
  _Source: spec gateway — Requirement: Supercharger Stats session status badge._
- **The edit row must span the FULL table width, so its `colspan` tracks the column count.** `SuperchargerRowEdit` renders the whole form in one cell, and `SuperchargerRowError` does the same for errors — both are `colspan="8"` since the Status column landed. Adding or removing a column means updating BOTH, in two different files (`supercharger_row.templ` and `supercharger_row_edit.templ`). RM41 tier 5 nearly shipped with only one of them fixed; the edit row would have rendered one column narrow on every Edit click.
  _Source: spec gateway — Requirement: Supercharger Stats in-progress session guidance & the change's design.md "Finding: a second colspan"._

## [index] ## Workflows — ADD ROWS

| `supercharger status badge` (2nd-column `ui.Badge`; Kind `primary`/`neutral`/`ghost`, never `success`/`warning`; gateway-read-only) | `workflows/supercharger-stats-read.md` |

---

## Ordering note — apply this AFTER `charge-session-log.md`

Both pending proposals target `workflows/supercharger-stats-read.md`. `charge-session-log.md`
carries a Glossary **REPLACE**; this file carries only APPENDs. Applying the REPLACE second is
harmless, but applying it *first* keeps the glossary's final state easy to read against its own
proposal. `apply-sync` with no argument applies every pending proposal in one pass, so this is a
review convenience, not a correctness requirement.

Note that `charge-session-log.md` was itself patched on 2026-09-04 to carry tier 5's status
sentence — see the REGRESSION GUARD note in that file. Its earlier version would have deleted
tier 5's glossary content on apply.
