# Sync proposal — gateway

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `architecture/account-activation-gate.md`
Source spec:  `openspec/specs/gateway/spec.md`
Generated:    2026-08-30
Status: PENDING REVIEW

---

<!--
Scope of this proposal — read before applying.

Derived from the gateway capability spec after archiving RM34 tier 2
(`RM34-gateway-block-inactive-login`, Linear MAG-33). The delta ADDED
`Requirement: Inactive Account Is Blocked At Login` and MODIFIED `Requirement: Google Sign-In`
so that a session is established only for an Active account.

WHY A NEW ARCHITECTURE TOPIC, and why not a use case:

- Nothing in the KB covers login, sessions, or the account-status gate today — INDEX.md resolves
  no term for `login`, `sign in`, `session`, or `inactive account`. So this is a brand-new entry,
  not an update-in-place.
- The invariant is CROSS-MODULE, which is what `architecture/` is for: the account module hides
  Inactive rows from reads (RM34 tier 1) and the gateway refuses them entry at login (tier 2).
  A reader arriving with "why can this user not log in" needs both halves in one place.
- A `use-case/gateway/google-sign-in.md` was considered and rejected FOR NOW: `from-spec` may not
  emit `## Flow`, `## Entry point`, or `## Database`, and those three sections are most of a
  use-case file's value. Applying this proposal there would create a skeleton whose useful half is
  permanently empty. If the login path is worth a proper use-case file, curate it with
  `/kkpa-context-curate use-case GET /auth/google/callback`, which does the real trace behind the
  human gate.
- The account-side sync (tier 1's `account` and `account-vehicle-registry` capability specs) is
  NOT staged yet. When it is, it should target this SAME file and append the read-filtering half.

`## Component map` is not emitted, as always for `from-spec` without `--with-filemap` — the spec
carries behavior, not file paths. The applied guide will therefore have an empty Component map
until someone curates it; that is expected and intentional.
-->

## [guide] ## Glossary — REPLACE

- **Known as:** `account activation gate`, `inactive account`, `account status`, `blocked login`, `deactivated account`
- **Internal name:** `account.Account.Status` (`Active` / `Inactive`) — enforced at login by the gateway and at read time by the account module

## [guide] ## How maintenance works — APPEND

- **The gate has two independent halves; changing one does not change the other.** The account module hides Inactive rows from its reads, and the gateway refuses an Inactive account a session at Google login. Neither half is a fallback for the other: an account deactivated mid-session keeps its cookie working until it expires, because the login check runs once at callback time and sessions are stateless.
- **Adding a new place that must respect the gate:** decide which half it belongs to. A new READ of accounts, vehicles or Tesla tokens filters on Active inside the account module (never in the caller). A new ENTRY point that establishes identity performs the status check in the gateway, after the account is resolved and strictly before any session value is written.
- **Changing the blocked page's wording or contact address:** both are catalogue keys in `internal/gateway/i18n/catalog.go`, ES and EN, and the address is deliberately hardcoded there. There is no config key to change and no route to the page — it is rendered directly from the OAuth callback.

## [guide] ## Conventions & gotchas — APPEND

- **A non-Active account must be refused BEFORE any session identity is written** — the status check runs after the account is provisioned/resolved and strictly before the first session set. Ordering is the whole security property: a check placed after a session write leaves a usable session behind on the refusal path. _Source: spec gateway — Requirement: Inactive Account Is Blocked At Login._
- **The gate is fail-closed: anything that is not exactly `Active` is refused** — including an empty or unrecognised status, not just the literal `Inactive`. Never rewrite the check as "reject when Inactive"; a status value nobody anticipated would then grant access. _Source: spec gateway — Requirement: Inactive Account Is Blocked At Login._
- **The refusal is HTTP 403 with a real rendered page, not a redirect or a bare string** — the visitor is authenticated but not authorized, and they must be able to read the contact address and act on it. _Source: spec gateway — Requirement: Inactive Account Is Blocked At Login._
- **The contact address is hardcoded in the translation catalogue, never configuration** — one support address does not earn a config lookup. Both languages must carry it non-empty, like every other user-facing string. _Source: spec gateway — Requirement: Inactive Account Is Blocked At Login._
- **The blocked page has exactly ONE render site and no route** — it is rendered inline from the OAuth callback. Do not add a `/account-blocked` route "for completeness": an unauthenticated visitor could then read it directly, and it would become a second place the refusal logic has to be kept correct. _Source: spec gateway — Requirement: Inactive Account Is Blocked At Login._
- **An Inactive account never reaches the language-sync step either** — the pre-login `lang` cookie is persisted to the account only on the Active path. A refused login must leave the account row untouched, so nothing about the visitor's rejected attempt is written. _Source: spec gateway — Requirement: Google Sign-In, Scenario: A callback resolving an Inactive account never reaches the language sync or session steps._
- **Account provisioning stays UNFILTERED while every other account read filters on Active** — the upsert that resolves a Google identity cannot filter by status, because a status predicate cannot suppress an `INSERT … ON CONFLICT` conflict target; filtering it would only make it lie about what it wrote. The authorization verdict therefore belongs to the gateway, not to the query. _Source: spec gateway — Requirement: Google Sign-In (provision or resolve, then check status)._
- **New accounts default to Inactive, so a fresh Google sign-in is refused by design** — this is the platform's invite gate, not a bug report. Activating an account is a manual database operation. _Source: spec gateway — Requirement: Inactive Account Is Blocked At Login; RM34 roadmap decision D2._

## [index] ## Architecture topics — ADD ROWS

| `account activation gate` (the cross-module Active/Inactive rule: the account module hides Inactive rows from reads, the gateway refuses them a session at login) | `architecture/account-activation-gate.md` |
| `inactive account` | synonym of `account activation gate` → `architecture/account-activation-gate.md` |
| `account status` | `account.Account.Status` (`Active` / `Inactive`) → `architecture/account-activation-gate.md` |
| `blocked login` | synonym of `account activation gate` → `architecture/account-activation-gate.md` |
| `deactivated account` | synonym of `account activation gate` → `architecture/account-activation-gate.md` |
