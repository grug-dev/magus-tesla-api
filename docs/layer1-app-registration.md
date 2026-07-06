# Layer 1 — Application Registration ("Magus Monitor" app)

**Authorization layer:** Application level — *"Is this app trusted by Tesla at all?"*

This document covers everything needed to register and configure the **Magus Monitor**
third-party developer app with the Tesla Fleet API. It is a **one-time, app-wide** setup:
you do it once when the app is created, and it is shared by every user and every vehicle that
the app will ever talk to.

Nothing in this layer grants access to a specific car. It only establishes that Tesla
recognizes your app (identified by its Client ID / Secret and its registered public key) as a
legitimate Fleet API partner. Granting a *user's* vehicle data to the app is a separate step —
see **[Layer 2 — User & Vehicle Access](./layer2-user-vehicle-access.md)** for the OAuth login
and vehicle-data phase that comes after this one.

> **Mental model:** Layer 1 produces **app credentials** (Client ID/Secret) and a **registered
> public key**. These answer *"is the app trusted?"* — they never, on their own, read any
> vehicle. The per-user tokens that actually read a car come from Layer 2.

> **Codebase (whole layer):** almost all of Layer 1 is terminal + dashboard + `curl` work with
> **no Go code**. The single Go touchpoint is `internal/config/config.go` → `config.Load()`,
> which reads the `TESLA_CLIENT_ID` / `TESLA_CLIENT_SECRET` you create in Step 1. The `config`
> package is shared with Layer 2 (it also loads/saves user tokens there).

---

## Step 1 — Protect your credentials immediately

Before anything else, make sure your Client ID and Secret never get committed to git.

```bash
echo ".env" >> .gitignore
echo "*.pem" >> .gitignore
echo "private-key*" >> .gitignore
```

Then create a `.env` file manually (not via terminal) with your credentials:

```
TESLA_CLIENT_ID=your_client_id_here
TESLA_CLIENT_SECRET=your_client_secret_here
```

The `.gitignore` above prevents `.env` and key files from ever being committed.

> **Why it matters:** the Client ID/Secret *are* the app's identity to Tesla. Anyone holding
> them can impersonate Magus Monitor when requesting Partner Tokens (Step 5). They are app-level
> secrets — not tied to any user — so they must never leak.

> **Codebase:** `internal/config/config.go` → `config.Load()` reads `TESLA_CLIENT_ID` and
> `TESLA_CLIENT_SECRET` from `.env` into the `Config` struct. This is the only place in the
> project that reads environment variables (a hard project rule), so every other package
> receives these app credentials as function arguments.

---

## Step 2 — Generate the EC key pair

Tesla uses an Elliptic Curve key pair (secp256r1 / prime256v1) to validate commands sent to your vehicle. Generate it with OpenSSL — no code needed, just your terminal:

```bash
# Generate private key
openssl ecparam -name prime256v1 -genkey -noout -out private-key.pem

# Derive the public key from it
openssl ec -in private-key.pem -pubout -out public-key.pem
```

| File | What to do with it |
|---|---|
| `private-key.pem` | Keep local and secret forever. Never commit it. |
| `public-key.pem` | Host this publicly in Step 3. |

> **Why it matters:** this key pair serves two purposes at the app level — (a) it proves you own
> the domain that hosts the public key (Steps 3–4), and (b) the private key will later sign
> vehicle *commands* (lock, climate, charge control). Read-only data access does **not** use it,
> which is why command signing (virtual key pairing) is deferred to Layer 2, Step 7. The key pair
> is generated once and belongs to the app, not to any individual user.

> **Codebase:** none — this is an `openssl` terminal step. The private key is not read by any Go
> code yet; it will be needed only once command-signing is implemented.

---

## Step 3 — Host the public key on Netlify

Tesla requires the public key to be permanently accessible at this exact path on a public HTTPS domain:

```
https://<your-domain>/.well-known/appspecific/com.tesla.3p.public-key.pem
```

### How to deploy with Netlify CLI (free)

The folder structure is already created in this repo under `magus-public-key-netlify/`. The public key has been copied there with the exact filename Tesla requires.

**Important:** Netlify's browser drag-and-drop silently drops hidden folders (`.well-known` starts with a dot). Use the Netlify CLI instead — it reads directly from disk and includes all files.

#### Install the CLI (one-time)

```bash
npm install -g netlify-cli
```

#### Deploy to the `magus-monitor` site

```bash
netlify deploy --dir=magus-public-key-netlify --prod
```

On first run it will open a browser to authenticate with your Netlify account and ask you to select the existing `magus-monitor` site. Subsequent deploys use the saved `.netlify/` config and need no interaction.

#### Verify the deploy succeeded

Open this URL in your browser — you should see the raw PEM key content:

```
https://magus-monitor.netlify.app/.well-known/appspecific/com.tesla.3p.public-key.pem
```

> **Why it matters:** Tesla fetches this public URL to confirm the app controls the domain and to
> obtain the key it will use for command validation. The path and filename are fixed by Tesla —
> `com.tesla.3p.public-key.pem` under `/.well-known/appspecific/` — and must match exactly.

> **Codebase:** none in the Go project — this is a static asset served by Netlify. The source
> lives in this repo under `magus-public-key-netlify/.well-known/appspecific/`. To redeploy, run
> the `netlify deploy` command above.

---

## Step 4 — Add the Netlify domain as an allowed origin

1. Go to [developer.tesla.com](https://developer.tesla.com) → your app settings.
2. Add `https://magus-monitor.netlify.app` as a second **allowed origin** (in addition to the `localhost` one set during registration).
3. Save the changes.

This allows Tesla to verify domain ownership via the public key you hosted in Step 3.

> **Why it matters:** allowed origins are an app-level whitelist. The `localhost` origin lets the
> OAuth redirect land on `http://localhost:8080/callback` during Layer 2 setup; the Netlify origin
> ties the hosted public key to the app. Both belong to the app configuration, not to any user.

> **Codebase:** none — this is a developer.tesla.com dashboard change.

---

## Step 5 — Register the public key with Tesla

With your public key live and the domain registered, you need to call the Tesla Fleet API to finalize the key registration. This is done via two `curl` calls — no code needed.

### 5a — Get a Partner Authentication Token

This is a machine-to-machine token obtained with your app credentials. No user login required.

```bash
curl --request POST \
  --url 'https://auth.tesla.com/oauth2/v3/token' \
  --header 'Content-Type: application/x-www-form-urlencoded' \
  --data 'grant_type=client_credentials' \
  --data 'client_id=YOUR_CLIENT_ID' \
  --data 'client_secret=YOUR_CLIENT_SECRET' \
  --data 'scope=openid'
```

Replace `YOUR_CLIENT_ID` and `YOUR_CLIENT_SECRET` with the values from your `.env` file. The response contains an `access_token` — copy it for the next step.

> **Why `grant_type=client_credentials`:** this is the OAuth "machine-to-machine" flow. The
> resulting **Partner Token** represents *the app itself* — there is no user and no vehicle
> attached to it, so it cannot read car data. Contrast this with Layer 2, Step 6c, which uses
> `grant_type=authorization_code` after a human logs in to produce *user-scoped* tokens.

### 5b — Register the public key

Using the Partner Token from 5a, call:

```bash
curl --request POST \
  --url 'https://fleet-api.prd.na.vn.cloud.tesla.com/api/1/partner_accounts' \
  --header 'Authorization: Bearer PARTNER_TOKEN_FROM_5A' \
  --header 'Content-Type: application/json' \
  --data '{"domain": "magus-monitor.netlify.app"}'
```

Replace `PARTNER_TOKEN_FROM_5A` with the `access_token` from 5a. A successful response means Tesla has linked your public key to your app.

> Note: `/api/1/partner_accounts/public_key` is a GET endpoint to look up an existing key — not for registration. The POST to `/api/1/partner_accounts` is the correct registration call.

> **Codebase:** **not implemented in Go.** Step 5 is a one-time bootstrap done with `curl`. If it
> ever needs to be automated (e.g. to re-register on a new domain), it would belong in a new
> `internal/partner/` package per the project's one-concern-per-package rule — never bolted onto
> `internal/auth` or `internal/vehicle`. It uses the same `TESLA_CLIENT_ID`/`TESLA_CLIENT_SECRET`
> that `config.Load()` already exposes.

---

## Checklist — Layer 1

| # | Task | Done |
|---|---|---|
| 1 | Credentials secured in `.env` + `.gitignore` | ✅ |
| 2 | EC key pair generated (`private-key.pem`, `public-key.pem`) | ✅ |
| 3 | Public key hosted on Netlify at `.well-known` path | ✅ |
| 4 | Netlify domain added as allowed origin on developer.tesla.com | ✅ |
| 5 | Public key registered via Partner Token + Fleet API call | ✅ |

**Next:** the app is now trusted by Tesla. Proceed to
**[Layer 2 — User & Vehicle Access](./layer2-user-vehicle-access.md)** to log in a Tesla account
and start fetching Magus's data.
