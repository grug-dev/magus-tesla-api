# magus-tesla-api

Personal project to consume Tesla Fleet APIs — vehicle data, commands, and telemetry for your own Tesla (Magus).

---

## Roadmap: Getting Access to the Tesla Fleet API

This is a step-by-step guide covering everything you need to do before writing a single line of code.  
You already own a Tesla and have a Tesla account — that covers the most basic prerequisite.

---

### Step 1 — Enable Multi-Factor Authentication on your Tesla account

Tesla requires MFA (two-factor authentication) and a verified email address before you can register a developer app.  
If you haven't done it yet: [tesla.com/account](https://www.tesla.com/account) → Security.

---

### Step 2 — Decide on a domain for hosting your public key

This is the most important infrastructure decision before you register anything.

Tesla requires you to **permanently** host a public key file at:
```
https://<your-domain>/.well-known/appspecific/com.tesla.3p.public-key.pem
```

The path must be at the **root of the domain**, not a subdirectory.

#### Can this GitHub repo (GitHub Pages) host it?

**Not directly as-is.** The default GitHub Pages URL for a project repo like this one would be:
```
https://cristianpena.github.io/magus-tesla-api/.well-known/...
```
That is a **subdirectory path**, not a root domain — Tesla will reject it.

#### Your options (easiest to most complex):

| Option | Cost | Notes |
|--------|------|-------|
| **Netlify free tier** | Free | Deploy a one-file site. You get `https://magus-tesla.netlify.app` — root domain, HTTPS included. Recommended for personal projects. |
| **Vercel free tier** | Free | Same idea as Netlify. `https://magus-tesla.vercel.app`. |
| **fleetkey.cc** | Free | Community tool that generates your key pair AND hosts the public key for you. Zero setup. |
| **GitHub Pages + custom domain** | ~$10/year | Buy a cheap domain (e.g., `magus-tesla.xyz`), point it at this repo's GitHub Pages, then the `.well-known` path works from root. Also gives you a real domain for long-term use. |
| **GitHub user site** | Free | Create a separate repo named `<yourusername>.github.io` — that IS a root domain. But it's a shared namespace with all your other projects. |

**Recommendation:** Start with **Netlify free tier** or **fleetkey.cc**. They require the least setup and give you a valid root HTTPS URL immediately.

---

### Step 3 — Generate your EC key pair (secp256r1)

Tesla uses an **Elliptic Curve key pair** to validate commands sent to your vehicle:

1. Generate a private key using the `secp256r1` curve (also called `prime256v1`).
2. Derive the corresponding PEM-encoded public key from it.
3. **Keep the private key secret and local** — never commit it or host it.
4. Host only the public key at the `.well-known` path on the domain from Step 2.

If you use **fleetkey.cc**, this step is done for you automatically.

---

### Step 4 — Register your app on the Tesla Developer Portal

Go to [developer.tesla.com](https://developer.tesla.com), sign in with your Tesla account, and request app access.

#### Suggested app name

Since your car is named **Magus**, keep the naming consistent and personal:

| Option | Notes |
|--------|-------|
| `Magus Monitor` | Clean, personal, describes the purpose |
| `Magus Fleet` | Sounds more developer-ish |
| `Magus Tesla API` | Matches the repo name exactly |

**Recommendation: `Magus Monitor`** — it's short, clearly personal, and not misleading.

#### Suggested description

> Personal application to monitor and interact with my Tesla vehicle (Magus). Tracks charge status, range, climate, location, and vehicle state through the Fleet API for personal home-automation and data analysis purposes.

#### Suggested "Purpose of Use" (Propósito de uso)

> Personal use only. This application is used exclusively to access and monitor data from my own Tesla vehicle. There are no commercial purposes, no third-party users, and no sharing of vehicle data with external services. The goal is to build a personal dashboard and automation tool for my own vehicle.

#### Scopes to request

Only request what you actually need. For a personal data/monitoring project:

| Scope | What it gives you |
|-------|-------------------|
| `openid` | Required for authentication |
| `offline_access` | Required to get a refresh token (so you don't re-login every hour) |
| `vehicle_device_data` | Read vehicle state, charge, location, climate |
| `vehicle_cmds` | Send commands (lock, climate, wake up) — add only if you need it |
| `vehicle_charging_cmds` | Control charging — add only if needed |

**Start with `openid`, `offline_access`, and `vehicle_device_data`.** You can expand scopes later.

Once approved, Tesla generates a **Client ID** and **Client Secret**. Store the secret in a password manager — it is shown only once.

---

### Step 5 — Register the app and host the public key

Back in the developer portal:

1. Enter your domain (from Step 2) as the `allowed_origins`.
2. Confirm your public key is live at the `.well-known` URL.
3. Submit. Tesla returns a **Partner Authentication Token** for server-to-server calls.

At this point your app is registered and Tesla can verify it owns that domain.

---

### Step 6 — Implement the OAuth 2.0 authorization flow

To access your own vehicle, your app obtains a user access token via OAuth 2.0:

1. Navigate to the Tesla authorization URL with `client_id`, `redirect_uri`, `scope`, and a `state` value.
2. You (the user/owner) log in and grant consent.
3. Tesla redirects back with an authorization `code`.
4. Exchange the `code` for an **access token** + **refresh token**.
5. Use `access_token` as a Bearer token on all Fleet API requests.
6. Store the `refresh_token` — it is **single-use**, valid for 3 months. Rotate it on every use (the previous token stays valid for 24 hours as a safety window).

---

### Step 7 — Pair the virtual key with Magus

Before your app can send commands, your vehicle must trust your app's EC public key. This is the "virtual key pairing" step:

1. Use the Partner Token (Step 5) to call the pairing endpoint on the Fleet API.
2. Tesla sends a pairing request to the Tesla mobile app on your phone.
3. Approve it from the Tesla app — either on your phone or from inside the vehicle.

After pairing, your app's EC private key signs commands and Magus will accept them.

---

### Step 8 — Start making API calls

With a valid access token and a paired virtual key, you can call Fleet API endpoints:

- **Vehicle data** — charge level, range, location, climate, software version, door state
- **Commands** — lock/unlock, start/stop climate, flash lights, honk, open/close charge port
- **Wake up** — wake a sleeping vehicle before sending commands
- **Streaming telemetry** — real-time data pushed from the vehicle (requires the Fleet Telemetry server, more advanced setup)

All North America requests go to:
```
https://fleet-api.prd.na.vn.cloud.tesla.com
```

---

### Pricing note

Tesla Fleet API is **pay-per-use** with a **$10/month credit** applied automatically. For a personal project with one vehicle, this credit typically covers everything:
- Data polling a few times per hour
- ~100 commands/day
- ~2 wake-ups/day

Check the current pricing at [developer.tesla.com](https://developer.tesla.com/docs/fleet-api/announcements) before going into heavy polling.

---

### Summary checklist

| # | Task | Status |
|---|------|--------|
| 1 | Tesla account with MFA + verified email | ✅ |
| 2 | Choose and set up a domain / hosting for the public key | ⬜ |
| 3 | Generate EC key pair (secp256r1) and host the public key | ⬜ |
| 4 | Register app on developer.tesla.com (name, description, scopes) | ⬜ |
| 5 | Set allowed_origins and confirm public key is live | ⬜ |
| 6 | Implement OAuth 2.0 flow and store refresh token | ⬜ |
| 7 | Pair virtual key with Magus via Tesla mobile app | ⬜ |
| 8 | Make your first Fleet API call | ⬜ |

---

### Reference links

- [Tesla Fleet API — Getting Started](https://developer.tesla.com/docs/fleet-api/getting-started/what-is-fleet-api)
- [Authentication Overview](https://developer.tesla.com/docs/fleet-api/authentication/overview)
- [Third-Party Tokens](https://developer.tesla.com/docs/fleet-api/authentication/third-party-tokens)
- [Virtual Key Developer Guide](https://developer.tesla.com/docs/fleet-api/virtual-keys/developer-guide)
- [Partner Endpoints](https://developer.tesla.com/docs/fleet-api/endpoints/partner-endpoints)
- [Announcements — pricing and deprecations](https://developer.tesla.com/docs/fleet-api/announcements)
