

# AI Coding Instructions: Go Modular Monolith + UI Gateway Architecture

Moving toward a Modular Monolith where your core services expose clean JSON APIs, wrapped by a dedicated UI Gateway layer for htmx.

You must strictly adhere to the architectural boundaries, layer separation, and Agent-per-Module concepts defined below.

---

## 1. Core Architectural Pillars

1. **Modular Monolith:** The codebase is divided into completely isolated domain modules inside `internal/`. Each module manages its own data, business logic, and exposes **pure JSON APIs and strict public Go Interfaces**. The API convention for this is `/api/{version}`
2. **UI Gateway Layer:** A standalone presentation layer (`internal/fe-gateway/`) that handles incoming htmx/web requests. This gateway exposes the APIs that will be consumed by HTMX (Frontend) that will include HTML content in their responses. The APIs that will be displayed on the UI pages. It should have its own prefix on the APIs. (`/agw`) It communicates with domain modules via Interfaces (Local communication) to get the data that will be injected into server-side templates, and returns **pure HTML fragments** to the client.

The UI Gateway's Job: This lightweight layer acts as a translator. It receives an htmx request, calls the corresponding interface of the module to get the data, pipes that JSON data into your Go templates (html/template or Templ), and streams the resulting HTML fragment back to the browser.


3. **Agent per Module:** Certain modules contain an autonomous `Agent` layer. This agent is a domain expert that uses module-specific tools and contexts to implement features in that module. It's like a subagent responsible of maintaining the codebase for the module-specific based on the general rules and conventions. It's like an expert developer assigned to the module, and it will be manage by a leader coordinator/orchestator when implementing a task.
4. The `tesla` domain module will act as an `adapter`. This module will be responsible of hitting tesla APIs for any user and it will return the data to any module. It should expose interfaces to achive that.

---

## 2. Strict Boundary Rules (How to be Smart with Boundaries)

When writing or refactoring code, you must enforce these separation invariants:

* **NO HTML inside Core Modules:** Never import HTML templates, `html/template`, `Templ`, or return raw HTML strings inside any module outside of `internal/gateway/`. Core modules return pure data structures (structs/JSON).
* **NO Cross-Module Database Leaks:** Module A (`internal/tesla/`) cannot query Module B's (`internal/battery/`) database tables, repositories, or private structs. Cross-module communication must happen strictly via interfaces (Local calls) or explicit interface boundaries.
* **Gateway is a Orchestrator, Not a Database Client:** The UI Gateway is completely decoupled from database access. It can *only* fetch data by calling the public JSON/Interface APIs exposed by the domain modules.
* **Agent Sandboxing:** An Agent belonging to a specific module can *only* be injected with tools, data queries, and system contexts scoped to its parent module. It has zero visibility into other domains.

---

## 3. Directory Layout Blueprint

Always locate files and maintain dependencies according to this pattern:


{{This is a suggestion, but you can change it based on the desription below}}

```text
├── internal/
│   ├── gateway/            # THE UI GATEWAY LAYER
│   │   ├── handlers/       # Receives htmx, coordinates APIs, renders HTML
│   │   └── templates/      # Go HTML templates (layouts, pages, htmx fragments)
│   │
│   # --- CORE DOMAIN MODULES (Strict Boundaries) ---
│   ├── tesla/              
│   │   ├── client.go       # Adapter logic to fetch tesla data and transform it to dtos. The dto models on this module, should have its own suffix to distinct of the real dtos of the project
│   │ 
│   │
│   ├── battery/            
│   │   ├── service.go      # Core battery math/logic
│   │   ├── agent.go        # Isolated AI Agent Layer for Battery Analytics
│   │   └── api.go          # Exposes JSON endpoints / Public Go API