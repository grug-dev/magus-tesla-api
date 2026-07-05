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

### How to deploy with Netlify (free, ~5 minutes)

1. Create a folder on your machine — e.g. `magus-public-key` (outside this repo).
2. Inside it, create the nested folder structure:
   ```
   magus-public-key/
   └── .well-known/
       └── appspecific/
           └── com.tesla.3p.public-key.pem
   ```
3. Copy `public-key.pem` from Step 2 into that path and rename it `com.tesla.3p.public-key.pem`.
4. Go to [netlify.com](https://netlify.com), sign up or log in.
5. Drag and drop the `magus-public-key` folder onto the Netlify deploy area.
6. Netlify assigns a URL — rename the site to something like `magus-monitor` in site settings.
7. Verify it works by opening this URL in your browser — you should see the raw key content:
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

With your public key live and the domain registered, you need to call the Tesla Fleet API to finalize the key registration.

### 5a — Get a Partner Authentication Token

Make a POST request to the Tesla token endpoint using your app credentials (Client ID + Secret). This is a machine-to-machine token — no user login required.

- **Endpoint:** `https://auth.tesla.com/oauth2/v3/token`
- **Grant type:** `client_credentials`
- **Scope:** `openid`

### 5b — Register the public key

Using the Partner Token from 5a, call:

- **Endpoint:** `POST https://fleet-api.prd.na.vn.cloud.tesla.com/api/1/partner_accounts/public_key`
- **Header:** `Authorization: Bearer <partner_token>`

This tells Tesla's system which public key belongs to your app.

---

## Step 6 — Implement the OAuth 2.0 flow (get your access token)

To access Magus's data, you need a user access token obtained via the standard OAuth 2.0 authorization code flow:

1. Open a browser and navigate to the Tesla authorization URL with your `client_id`, `redirect_uri`, `scope`, and a random `state` value.
2. Log in with your Tesla account and grant consent.
3. Tesla redirects to your `redirect_uri` (e.g. `http://localhost:8080/callback`) with an authorization `code` in the URL.
4. Exchange that `code` for an **access token** and a **refresh token** via a POST to `https://auth.tesla.com/oauth2/v3/token`.
5. Store both tokens securely (in `.env` or a local secrets file — never in git).

### Token rules
- `access_token` — short-lived, used as a Bearer token on all API requests.
- `refresh_token` — single-use, valid for 3 months. Save the new one every time you refresh.
- The previous refresh token stays valid for 24 hours after rotation (safety window).

---

## Step 7 — Pair the virtual key with Magus

Before your app can send **commands** (not just read data), Magus must trust your app's EC public key. This is a one-time step:

1. Use the Partner Token (Step 5a) to call the Fleet API pairing initiation endpoint.
2. Tesla sends a pairing request to the Tesla mobile app on your phone.
3. Approve it from the mobile app or from inside the vehicle.

After pairing, commands signed with your `private-key.pem` will be accepted by Magus.

> Note: Pairing is only required for **commands**. Read-only vehicle data (Step 6) works without it.

---

## Step 8 — Make your first API call

With a valid access token you can now call Fleet API endpoints. Base URL for North America:

```
https://fleet-api.prd.na.vn.cloud.tesla.com
```

### Useful endpoints to start with

| Endpoint | What it returns |
|---|---|
| `GET /api/1/vehicles` | List of your vehicles and their IDs |
| `GET /api/1/vehicles/{id}/vehicle_data` | Full vehicle state (charge, location, climate, etc.) |
| `POST /api/1/vehicles/{id}/wake_up` | Wake a sleeping vehicle |
| `POST /api/1/vehicles/{id}/command/charge_start` | Start charging |

---

## Checklist

| # | Task | Done |
|---|---|---|
| 1 | Credentials secured in `.env` + `.gitignore` | ⬜ |
| 2 | EC key pair generated (`private-key.pem`, `public-key.pem`) | ⬜ |
| 3 | Public key hosted on Netlify at `.well-known` path | ⬜ |
| 4 | Netlify domain added as allowed origin on developer.tesla.com | ⬜ |
| 5 | Public key registered via Partner Token + Fleet API call | ⬜ |
| 6 | OAuth flow implemented — access token + refresh token obtained | ⬜ |
| 7 | Virtual key paired with Magus (for commands) | ⬜ |
| 8 | First API call made successfully | ⬜ |
