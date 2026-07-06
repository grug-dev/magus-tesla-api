# Post-Registration Setup

The original single walkthrough has been split into two documents, one per **authorization layer**
of the Tesla Fleet API. The two layers answer different questions:

- **Layer 1 — Application level:** *"Is the app trusted by Tesla?"* One-time, app-wide setup
  (Client ID/Secret, EC key pair, public-key hosting, partner-account registration). Produces app
  credentials — never reads a car on its own.
- **Layer 2 — User & vehicle level:** *"Which account consented, and what vehicles come with it?"*
  Per-user OAuth login that produces access/refresh tokens, plus the vehicle-data calls. This is
  where nearly all the Go code lives.

## Documents

- **[Layer 1 — Application Registration](./layer1-app-registration.md)**
  Steps 1–5: create and register the "Magus Monitor" developer app.
- **[Layer 2 — User & Vehicle Access](./layer2-user-vehicle-access.md)**
  Steps 6–8: OAuth login, the two tokens, and fetching Magus's live data.

Each document is a self-contained step-by-step to-do and references the exact package/class in the
codebase that implements each step.
