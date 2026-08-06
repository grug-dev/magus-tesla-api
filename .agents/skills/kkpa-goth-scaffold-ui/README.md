# kkpa-goth-scaffold-ui

A **local** (repo-checked-in) skill that scaffolds **AI-efficient UI** for the
`magus-tesla-api` gateway on the **GOTH stack** — **G**o + **T**empl + **H**tmx — styled with
a **Node-less** standalone Tailwind CLI + **DaisyUI**, and a responsive **drawer**
navigation. It exists so that every UI page is born compliant with the gateway boundary rules
in `ai/architecture.md` / `ai/htmx-conventions.md`, and so that different sessions (and
different `kkpa-dev-harness-pipeline` WORKER agents) produce **consistent** UI instead of
each inventing its own markup, spacing, and colors.

## How it works

The skill has three modes (SKILL.md dispatches top-down; first match wins):

1. **`help`** — Help mode. If args are exactly `help` / `--help` / `-h`, the skill re-reads
   its own docs at runtime and prints usage, then stops. Side-effect-free.
2. **`init`** — one-time foundation. Auto-runs when a scaffold is requested but the
   foundation is missing (no `internal/gateway/static/input.css` + `templates/ui/`). It:
   - downloads the Node-less toolchain (native `tailwindcss` binary — git-ignored — plus the
     committed `daisyui.mjs` / `daisyui-theme.mjs` bundles the binary loads itself);
   - writes `static/input.css` (local `.mjs` plugin form), adds a `make css` target, and
     folds it into `make generate`;
   - rewrites `layouts/base.templ` into the themed DaisyUI **drawer** shell;
   - seeds the `templates/ui/` typed component kit (Card, StatTile, Button, Alert, Badge,
     Table, PageHeader, NavShell);
   - runs `make css` + `make templ` and updates the structure docs.
3. **`scaffold <concept> [module]`** — the repeatable value. Generates one full **vertical
   slice** for a domain concept, mirroring the `charges` gold-standard: view model
   (`fragments/<concept>_vm.go`) → page + swappable fragments → Gin handler
   (`handlers/<concept>.go`) → routes (`gateway.go`) → tests → codegen. It resolves the
   owning module's **interfaces** via CodeGraph and calls only those (never a DB, never a
   vendor DTO), keeps templates logic-free, uses semantic theme tokens (never hex), and shows
   km/kmh for any distance/speed.

### What a "vertical slice" is

A slice is one feature cut top-to-bottom through every gateway layer — from the URL the
browser hits down to the module interface it reads — and nothing more. Using the existing
`charges` slice as the template, `scaffold <concept>` generates these five pieces:

```
Browser hits  GET /charges
     │
     ▼
┌─────────────────────────────────────────────────────────────┐
│ 1. ROUTE       gateway.go   →  registers GET /charges        │
│ 2. HANDLER     handlers/charges.go → auth-check, call module,│
│                                       build the VM, render   │
│ 3. VIEW MODEL  fragments/charges_vm.go → plain display struct│
│ 4. PAGE        pages/charges.templ → HTML using ui/ kit      │
│ 5. FRAGMENTS   fragments/charge_row.templ, _create_form.templ│
│                → the htmx-swappable pieces                    │
└─────────────────────────────────────────────────────────────┘
```

"Mirroring the `charges` gold-standard" means every new slice is laid out and layered
identically to this one, so it inherits the correct boundaries (gateway calls interfaces
not DBs, km not miles, CSRF on writes) and stays consistent across sessions and pipeline
agents. The exact reference files to imitate are listed in `references/charges-slice-pattern.md`.

The design rests on a **3-layer vocabulary** so an AI (or a pipeline agent) always looks a
visual decision up rather than inventing it: **Templ** (typed component boundary) → **DaisyUI**
(component + theme classes, zero JS so htmx swaps stay styled) → **Tailwind** (responsive +
spacing utilities).

Details live in `references/`:
- `daisyui-templ-conventions.md` — Node-less setup, `input.css`, `make css`, drawer shell, theme tokens, wrapper pattern, htmx-swap safety.
- `charges-slice-pattern.md` — the vertical-slice recipe + exact reference files to imitate.
- `component-kit.md` — the `templates/ui/` kit + each component's `Props` contract.

## Example prompts

- `/kkpa-goth-scaffold-ui init` — lay down the Tailwind+DaisyUI foundation, drawer shell, and component kit.
- "Set up the DaisyUI/Tailwind UI foundation for the gateway."
- `/kkpa-goth-scaffold-ui scaffold battery telemetry` — build a battery panel backed by the `telemetry` module.
- "Scaffold a UI page for the charge-session summary."
- "Build the dashboard slice for vehicle location using the account module."
- `/kkpa-goth-scaffold-ui help` — show usage (Help mode).

## Help mode

Running the skill with **`help`**, **`--help`**, or **`-h`** (and nothing else) triggers
Help mode: it re-reads `SKILL.md`, this `README.md`, and `references/` at runtime and
synthesizes current usage (what it does, how to invoke, example invocations, what each mode
does). It writes nothing and performs no scaffolding.

## Scope / non-goals

- **Only** touches `internal/gateway/` (the single place HTML lives). It does not write
  domain-module logic, migrations, or JSON `/api` handlers.
- Assumes the concept has an owning domain module exposing interfaces; if not, it stops and
  asks rather than inventing one.
- Never adds tests for the `tesla-exploration` capability (`internal/tesla/raw.go`,
  `cmd/explore-tesla-api/`) — those hit the live paid Fleet API (see `CLAUDE.md`).
