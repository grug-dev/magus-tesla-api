# Post-Registration Setup

Steps to follow after completing the Tesla Fleet API app registration and obtaining the Client ID and Client Secret.

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

---

## Step 4 — Add the Netlify domain as an allowed origin

1. Go to [developer.tesla.com](https://developer.tesla.com) → your app settings.
2. Add `https://magus-monitor.netlify.app` as a second **allowed origin** (in addition to the `localhost` one set during registration).
3. Save the changes.

This allows Tesla to verify domain ownership via the public key you hosted in Step 3.

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

---

## Step 6 — OAuth 2.0 flow (get your user access token)

This is handled by the Go program in `cmd/setup/`. It automates all three sub-steps:
opens the browser, catches the callback, exchanges the code, and saves both tokens to `.env`.

### Project structure

```
cmd/setup/main.go              ← entry point — orchestrates steps 1, 6a, 6b, 6c
internal/config/config.go      ← Step 1  — loads .env, saves tokens back
internal/auth/oauth.go         ← Step 6a — builds the Tesla auth URL, opens browser
internal/server/callback.go    ← Step 6b — Gin server that catches the OAuth redirect
internal/auth/token.go         ← Step 6c — exchanges the code for tokens
```

### First-time run (install dependencies)

```bash
go mod tidy
```

### Run the setup

```bash
go run ./cmd/setup
```

What happens automatically:

1. **[Step 1]** Loads `TESLA_CLIENT_ID` and `TESLA_CLIENT_SECRET` from `.env`.
2. **[Step 6a]** Builds the OAuth URL and opens it in your browser. If the browser doesn't open, the URL is printed to the terminal for manual copy/paste.
3. **[Step 6b]** Starts a Gin server on `http://localhost:8080` and waits for Tesla to redirect with the authorization code. Approve the consent screen in the browser — the server catches the code automatically.
4. **[Step 6c]** Exchanges the code for an access token and refresh token, then writes both to `.env`.

After a successful run, `.env` will contain:

```
TESLA_ACCESS_TOKEN=...
TESLA_REFRESH_TOKEN=...
```

### Understanding the two tokens

After `go run ./cmd/setup` completes, two tokens are saved to `.env`. Here is what each one is, where it came from, and when you use it.

---

#### `TESLA_ACCESS_TOKEN`

| Property | Value |
|---|---|
| **Generated by** | Step 6c — the code exchange call to `https://auth.tesla.com/oauth2/v3/token` |
| **What it is** | A short-lived JWT that proves your app is authorized to act on your behalf |
| **How it's used** | Sent on every Fleet API request as `Authorization: Bearer <token>` |
| **Lifetime** | 8 hours (28800 seconds) |
| **What happens when it expires** | Fleet API returns HTTP 401. You need a new one using the refresh token. |

Think of it as a **daily pass** — it gets you through the door, but expires every 8 hours.

---

#### `TESLA_REFRESH_TOKEN`

| Property | Value |
|---|---|
| **Generated by** | Step 6c — same code exchange call, returned alongside the access token |
| **What it is** | A long-lived credential used to get a new access token without re-doing the browser login |
| **How it's used** | Sent to `https://auth.tesla.com/oauth2/v3/token` with `grant_type=refresh_token` to get a fresh access token |
| **Lifetime** | 3 months |
| **Critical rule** | It is **single-use**. Every time you use it, Tesla returns a brand new refresh token. You must save the new one or you will be locked out. |
| **Safety window** | The previous refresh token stays valid for 24 hours after it has been rotated, in case your app failed to save the new one. |

Think of it as the **master key** — you use it once to get a new daily pass, and it replaces itself each time.

---

#### When to re-run `go run ./cmd/setup`

| Situation | What to do |
|---|---|
| Access token expired (8 hours) | Use the refresh token to get a new access token — no browser login needed |
| Refresh token expired (3 months) | Re-run `go run ./cmd/setup` — goes through the full browser login again |
| Refresh token was used but new one wasn't saved | Re-run `go run ./cmd/setup` within 24 hours of last use — old one may still work |
| Both tokens lost or corrupted | Re-run `go run ./cmd/setup` |

> **Implemented:** `cmd/magus` automatically detects a 401 (expired access token), calls `auth.RefreshTokens()`, saves the new token pair to `.env`, and retries — no manual action needed. Re-run `go run ./cmd/setup` only when the refresh token itself expires (every 3 months).

---

## Step 7 — Pair the virtual key with Magus *(skipped)*

> **Skipped for now.** Virtual key pairing is only required to send commands to Magus (lock, unlock, climate, etc.). Read-only data access works without it. If commands are needed in the future, revisit this step.
>
> When ready: use the Partner Token (Step 5a) to call the pairing initiation endpoint → Tesla sends a request to the mobile app → approve it from the app or inside the vehicle.

---

## Step 8 — First API call: fetch Magus's live data

Handled by `cmd/magus/` using the `internal/vehicle/` package.

### Package structure

```
internal/vehicle/client.go    ← authenticated HTTP client for the Fleet API
internal/vehicle/vehicle.go   ← types + List(), Data(), WakeUp() methods
cmd/magus/main.go             ← entry point: loads config, fetches and prints Magus's data
```

### Run it

```bash
go run ./cmd/magus
```

What it does:

1. Loads the access token from `.env`.
2. Calls `GET /api/1/vehicles` to find Magus and check if she is online or asleep.
3. If asleep, calls `POST /api/1/vehicles/{id}/wake_up` and polls until online (up to 60 seconds).
4. Calls `GET /api/1/vehicles/{id}/vehicle_data` to fetch the full snapshot.
5. Prints a summary covering battery, charging, climate, location, and vehicle state.

### Fleet API endpoints used

| Endpoint | What it returns |
|---|---|
| `GET /api/1/vehicles` | Vehicle list with IDs and current state |
| `GET /api/1/vehicles/{id}/vehicle_data` | Full snapshot: charge, climate, location, state |
| `POST /api/1/vehicles/{id}/wake_up` | Wakes a sleeping vehicle before fetching data |

---

## Checklist

| # | Task | Done |
|---|---|---|
| 1 | Credentials secured in `.env` + `.gitignore` | ✅ |
| 2 | EC key pair generated (`private-key.pem`, `public-key.pem`) | ✅ |
| 3 | Public key hosted on Netlify at `.well-known` path | ✅ |
| 4 | Netlify domain added as allowed origin on developer.tesla.com | ✅ |
| 5 | Public key registered via Partner Token + Fleet API call | ✅ |
| 6 | OAuth flow — access token + refresh token saved to `.env` | ✅ |
| 7 | Virtual key paired with Magus (for commands) | ⬜ |
| 8 | First API call made successfully | ⬜ |
