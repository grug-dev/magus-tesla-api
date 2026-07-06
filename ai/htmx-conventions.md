# htmx Conventions — magus-tesla-api

> **Status: TBD — stub.** There is no web layer in the repo yet. Populate this file
> when the htmx frontend lands. Referenced from `CLAUDE.md`; any assistant/human
> writing htmx markup must read and follow it once it has content.

Authoritative htmx markup and style conventions for this project. Sibling to
[`go-conventions.md`](./go-conventions.md) (Go code style) and
[`htmx-go-integration.md`](./htmx-go-integration.md) (how htmx talks to the Go API).

---

## Intended scope (what belongs here once the web layer exists)

- `hx-*` attribute conventions — which attributes are allowed/preferred, ordering, readability.
- Template/partial file organization — where fragment templates live and how they're named.
- Progressive-enhancement rules — behavior with JS disabled, when full-page vs partial responses are acceptable.
- Accessibility and semantic-HTML expectations for htmx-driven fragments.
- Client-side asset conventions — where htmx (and any extensions) are loaded from, versioning.
- Naming conventions for `id`/`hx-target` hooks so markup and handlers stay in sync.

---

_Nothing to enforce yet. Do not treat this file as active guidance until it is populated._
