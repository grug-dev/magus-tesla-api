# VPS installation — live progress log

First deploy of `magus-tesla-api` to a Hostinger VPS with Docker.

This file is the **progress log**. Each step is filled in when you finish it.
For the full reference after the deploy, see
[`docs/1-deploy/docker.md`](docker.md).

---

## Facts

Filled in as we go.

| Fact | Value |
|---|---|
| Provider | Hostinger VPS (new, empty) |
| OS | Ubuntu 26.04.1 LTS |
| RAM | 7.7 GB |
| vCPU | 2 |
| Disk | 96 GB (2% used) |
| Swap | none — not needed at this RAM |
| Login user | `root` |
| IP / domain | kept on the server only, never in this repo |

---

## Roadmap

| # | Step | Status |
|---|---|---|
| 1 | Check the server (OS, RAM, disk) | ✅ |
| 2 | Update packages, install `git`, `make`, `curl` | ✅ |
| 3 | Create a non-root sudo user | ✅ |
| 4 | Firewall: allow 22, 80, 443 only | ✅ |
| 5 | ~~Add swap~~ — dropped, 7.7 GB RAM is enough | ➖ |
| 6 | Install Docker + Compose plugin | ✅ |
| 7 | Point a DNS A record at the VPS | ✅ |
| 8 | GitHub deploy key (the repo is private) | ✅ |
| 9 | Clone the repo | ✅ |
| 10 | Create and fill `.env` | ✅ |
| 11 | ~~Tune memory limits~~ — dropped, defaults fit 7.7 GB | ➖ |
| 12 | Add production redirect URIs (Google + Tesla) | ✅ |
| 13 | First deploy (`docker compose up -d --build`) | ✅ |
| 14 | Verify (HTTPS, health, logs) | ✅ |
| 15 | Daily backup cron | ✅ |
| 16 | Tesla domain verification (needs live HTTPS) | ✅ |

---

## Steps

### Step 1 — Check the server ✅

Read-only. Confirms what the box is before installing anything.

```bash
head -2 /etc/os-release; free -h; df -h /; nproc; whoami; curl -4 -s ifconfig.me
```

Result: see the Facts table above.

Two decisions came out of it:

- **No swap.** 7.7 GB is plenty for the Docker build. Step 5 dropped.
- **Default memory limits are fine.** `compose.yaml` limits total about
  2 GB (`db` 1536M, `web` 256M, `poller` 128M, `caddy` 128M). That leaves
  more than 5 GB free. Leave every `*_MEM_LIMIT` line commented in `.env`.
  Step 11 dropped.

Use `curl -4` for the IP. Plain `curl ifconfig.me` may answer with the
IPv6 address, and a DNS **A** record needs the **IPv4** one.


---

### Step 2 — Update the system and install base tools ✅

Run as `root` on a fresh Ubuntu box.

```bash
apt update && DEBIAN_FRONTEND=noninteractive apt upgrade -y
```

```bash
apt install -y git make curl ca-certificates openssl ufw
```

Why each package:

| Package | Used by |
|---|---|
| `git` | cloning the repo (step 9) |
| `make` | `make docker-up`, and the backup cron (step 15) |
| `curl` | installing Docker, and the health check (step 14) |
| `openssl` | generating `SESSION_SECRET` (step 10) |
| `ufw` | the firewall (step 4) |

If `apt upgrade` installs a new kernel, run `reboot` and log back in.

---

### Step 3 — Create the `magus` deploy user ✅

Decision: do not deploy as `root`. A container escape or a bad script then
runs as a normal user, not as the machine owner. Root stays as the fallback
login.

Keep the root session open until 3.4 succeeds.

```bash
adduser magus            # asks for a password — save it, sudo needs it
usermod -aG sudo magus
```

Copy root's SSH key so `magus` can log in the same way:

```bash
mkdir -p /home/magus/.ssh && \
cp /root/.ssh/authorized_keys /home/magus/.ssh/authorized_keys && \
chown -R magus:magus /home/magus/.ssh && \
chmod 700 /home/magus/.ssh && \
chmod 600 /home/magus/.ssh/authorized_keys
```

Verify from a second terminal, before closing root:

```bash
ssh magus@<VPS_IP>
sudo whoami              # must print: root
```

From here on every step runs as `magus`, with `sudo` where admin rights
are needed.

---

### Step 4 — Firewall ✅

Allow SSH **before** enabling ufw, or you lock yourself out.

```bash
sudo ufw allow OpenSSH && sudo ufw allow 80/tcp && sudo ufw allow 443/tcp
sudo ufw enable
sudo ufw status verbose
```

| Port | Why |
|---|---|
| 22 | SSH access |
| 80 | Caddy's Let's Encrypt challenge + HTTPS redirect |
| 443 | HTTPS traffic |

Postgres (5432) is never opened. It stays on Docker's private network.

Result: `Status: active`, default `deny (incoming)`, the three ports allowed
on IPv4 and IPv6.

**Known limit:** Docker writes its own firewall rules and they bypass ufw, so
ufw cannot block a Docker-published port. Harmless here — only `caddy`
publishes ports (80/443, which we want open); `db`, `web` and `poller`
publish none.

Hostinger's own hPanel firewall is separate. If you enable it there, allow 80
and 443 there too.

---

### Step 6 — Install Docker + Compose plugin ✅

The convenience script installs Docker Engine and the Compose plugin together.

```bash
curl -fsSL https://get.docker.com | sudo sh
sudo usermod -aG docker magus     # run docker without sudo
newgrp docker                     # apply the group now (or re-login)
sudo systemctl enable --now docker
```

Verify:

```bash
docker --version && docker compose version && docker run --rm hello-world
```

Installed on this VPS: Docker **29.8.0**, Compose **v5.5.1**.

It must be `docker compose` (space), the plugin — not the old
`docker-compose` (hyphen). `deploy/docker/compose.yaml` needs the plugin.

---

### Step 7 — Confirm the domain points at the VPS ✅

Hostinger's default hostname (`srv<NNNNNNN>.hstgr.cloud`) already has a public
A record pointing at the VPS, so there was no record to create.

Get the VPS IPv4:

```bash
curl -4 -s ifconfig.me; echo
```

Check public DNS **from your own machine, not the VPS**:

```bash
dig +short <your-host>.hstgr.cloud A
```

Both must print the same IPv4.

**Do not trust `getent` run on the VPS.** Ubuntu puts the machine's own
hostname in `/etc/hosts` pointing at `127.0.1.1`, so `getent` answers with
loopback and hides the real record. Let's Encrypt uses public DNS, so public
DNS is what must be checked.

That `/etc/hosts` line is harmless for the deploy: Let's Encrypt connects from
outside to the public IP, and a local `curl` still reaches Caddy because Docker
publishes 80/443 on every interface, loopback included.

**Which domain to use.** Hostinger gives both a shared hostname
(`srv<N>.hstgr.cloud`) and, on some plans, a free real domain. Use the **real
domain** for `BASE_DOMAIN` and `BASE_URL`:

| | Shared `*.hstgr.cloud` | Your own domain |
|---|---|---|
| DNS record | Hostinger controls it | You control it |
| Let's Encrypt rate limit | shared with every Hostinger customer | yours alone |

Both must resolve to the same VPS IPv4 — confirm with `dig +short <domain> A`
from your own machine before continuing.

The real domain and IP are deliberately not written in this repo. They live
only in `.env` on the server.

---

### Step 8 — GitHub deploy key ✅

The repo is **private**, so the VPS needs credentials to clone. A deploy key
is scoped to this one repo and never expires — better than a personal access
token, which covers every repo you own and does expire.

```bash
ssh-keygen -t ed25519 -C "magus-vps-deploy" -f ~/.ssh/github_deploy -N ""
cat ~/.ssh/github_deploy.pub
```

No passphrase (`-N ""`) on purpose: the server clones unattended.

Add the printed line at
`https://github.com/grug-dev/magus-tesla-api/settings/keys` →
**Add deploy key**, title `hostinger-vps`, and leave **Allow write access
UNCHECKED**. Read-only means a leaked key cannot push.

Point git at that key:

```bash
cat >> ~/.ssh/config <<'EOF'
Host github.com
  HostName github.com
  User git
  IdentityFile ~/.ssh/github_deploy
  IdentitiesOnly yes
EOF
chmod 600 ~/.ssh/config
```

`IdentitiesOnly yes` forces this key. Without it SSH may offer another key
first and GitHub refuses.

Verify:

```bash
ssh -T git@github.com
```

Success looks like `Hi grug-dev/magus-tesla-api! You've successfully
authenticated, but GitHub does not provide shell access.` The "no shell
access" part is normal, not an error.

---

### Step 9 — Clone the repo ✅

```bash
cd ~ && git clone git@github.com:grug-dev/magus-tesla-api.git && cd magus-tesla-api
```

Repo path on the VPS: **`/home/magus/magus-tesla-api`**. The backup cron in
step 15 needs this exact path.

Verify:

```bash
pwd && git log --oneline -1 && git status --short && git branch --show-current
```

Expect the repo root path, the newest commit, an empty `git status`, and
branch `main`. If `.env` appears in `git status`, stop — it must stay
untracked.

**Run every `docker compose` command from the repo root**, never from
`deploy/docker/`. The compose file resolves its paths against the repo root.
Prefer the `make` wrappers (`make docker-up`), which add the two required
flags for you.

---

### Step 10 — Create and fill `.env` ✅

**Do not use `make env-setup` for a Docker deploy.** It only prompts for the
multi-tenant vars; it never sets `POSTGRES_USER`, `POSTGRES_PASSWORD`,
`POSTGRES_DB` or `BASE_DOMAIN`, and the stack cannot start without them. Copy
the template instead — it lists every variable with its own comment.

```bash
cp .env.example .env && chmod 600 .env
```

`chmod 600` keeps the DB password and session secret readable only by `magus`.

Generate both secrets:

```bash
echo "SESSION_SECRET=$(openssl rand -hex 32)"
echo "POSTGRES_PASSWORD=$(openssl rand -hex 24)"
```

**Use hex, never a hand-typed password.** `POSTGRES_PASSWORD` is embedded in
`DATABASE_URL`, which is a URL, so `@ : / ? # %` break parsing. Hex is only
`0-9a-f`, always URL-safe.

Then `nano .env` and fill in:

| Variable | Value |
|---|---|
| `TESLA_CLIENT_ID` / `TESLA_CLIENT_SECRET` | from developer.tesla.com |
| `TESLA_ACCESS_TOKEN` / `TESLA_REFRESH_TOKEN` | **leave empty** — the gateway does per-user OAuth |
| `DATABASE_URL` | `postgres://<user>:<password>@db:5432/<db>?sslmode=disable` |
| `MAGUS_DB_PASSWORD` | **leave empty** — host-only `make db-setup` path, not Docker |
| `POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB` | creates the DB inside the `db` container |
| `BASE_DOMAIN` | your domain, no scheme |
| `BASE_URL` | `https://` + the same domain |
| `PORT` | `8080` |
| `SESSION_SECRET` | the hex above — changing it later logs everyone out |
| `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET` | Google Cloud Console |
| every `#*_CPU_LIMIT` / `#*_MEM_LIMIT` | **leave commented** — defaults fit 7.7 GB |

File format: no quotes around values, no spaces around `=`.

**The host in `DATABASE_URL` is `db`, not `localhost`.** `db` is the compose
service name on Docker's private network. This is the most common mistake and
it fails at the `migrate` step, which then blocks `web` and `poller`.

Verify without printing any secret:

```bash
grep -nE '^[A-Z_]+=$' .env
grep '^DATABASE_URL=' .env | sed -E 's|//[^:]+:[^@]+@|//USER:PASS@|'
grep -E '^(POSTGRES_USER|POSTGRES_DB|BASE_DOMAIN|BASE_URL|PORT)=' .env
```

Only `TESLA_ACCESS_TOKEN`, `TESLA_REFRESH_TOKEN` and `MAGUS_DB_PASSWORD` may be
empty. The database name ending `DATABASE_URL` must equal `POSTGRES_DB`.

---

### Step 12 — Production redirect URIs 🟡

Both providers reject a login whose redirect URI was not registered first.
The paths are built from `BASE_URL` in `internal/config/config.go:60,66`.

**Google — done.** Cloud Console → APIs & Services → Credentials → your OAuth
client → Authorized redirect URIs → add:

```
https://<your-domain>/auth/google/callback
```

**Tesla — blocked until HTTPS is live.** developer.tesla.com → Allowed
Redirect URIs → `https://<your-domain>/connect/tesla/callback` is rejected
with:

> El dominio no es válido. Asegúrate de que esté registrado ante una autoridad
> certificadora...

Tesla verifies the domain by reaching it over HTTPS with a real certificate.
Caddy has not issued one yet, so there is nothing to verify. This is expected
ordering, not a misconfiguration — it moves to **step 16**, after the deploy.

Keep the existing `http://localhost:8080/...` entries. `make dev` on your Mac
still needs them, and both can coexist.

---

### Step 13 — First deploy 🟡

Validate the compose file before building — it parses `.env` and starts
nothing, so a missing variable costs one second instead of a whole build:

```bash
cd ~/magus-tesla-api
docker compose --project-directory . -f deploy/docker/compose.yaml config > /dev/null && echo "compose OK"
```

Then build and start:

```bash
make docker-up
```

Order: `db` starts and turns healthy → `migrate` applies every migration and
exits `0` → `web` and `poller` start → `caddy` requests the certificate.
`migrate` showing `Exited (0)` is success, not a failure.

**Ignore `/bin/sh: 1: go: not found`.** That is the Makefile's `GOOSE ?=
$(shell ... go env GOPATH)` line evaluating at parse time. The Docker targets
never use goose, and Go is not needed on the VPS — the builder container
compiles everything.

#### Blocker found: no tzdata in the container

The first run failed:

```
service "migrate" didn't complete successfully: exit 2
```

```
panic: internal/clock: failed to load America/Bogota: unknown time zone America/Bogota
        internal/clock/clock.go:27
```

Cause: `deploy/docker/Dockerfile`'s final stages are `alpine:3.20`, which
ships no IANA time zone database. `internal/clock` calls
`time.LoadLocation("America/Bogota")` at package init, so **every** binary
panicked before running any code. `migrate` died first, and `web` and
`poller` never started because they wait for it.

Exit code 2 is the tell: `log.Fatalf` exits 1, so 2 means an unrecovered
panic — a startup crash, not a bad config value.

Fix: one line in `internal/clock/clock.go`, blank-importing the stdlib
tzdata so the binary carries its own copy.

```go
_ "time/tzdata"
```

This was pre-authorized. The file's own comment had named this exact case:
*"if a containerized deploy ever lands, adding `_ "time/tzdata"` above is a
one-line fix, and the panic below guarantees the gap cannot be missed
silently."* Both halves held — the panic surfaced the gap loudly instead of
silently attributing every captured day to UTC.

Rejected alternative: `apk add --no-cache tzdata` in the Dockerfile. It costs
three lines instead of one, adds a few MB per image, and only fixes this one
deploy target — a scratch or distroless image would panic again. Embedding
puts the data in the module that requires it.

After the fix: commit, push, then on the VPS `git pull && make docker-up`.

#### Second blocker: the placeholder was copied literally

```
goose up: failed to initialize: cannot parse
`postgres://magus:xxxxxx@db:5432/magus?sslmode=disable`:
failed to parse as URL (net/url: invalid userinfo)
```

`DATABASE_URL`'s password was the literal text `<POSTGRES_PASSWORD>`. In a URL
userinfo section `net/url` allows only letters, digits and
`- . _ ~ ! $ & ' ( ) * + , ; = % @ :` — so `<` and `>` are rejected.

Exit code 1 (not 2) is the tell: a handled `log.Fatalf`, not a crash.

Find a bad character without printing the secret:

```bash
grep '^DATABASE_URL=' .env | sed -E 's|^DATABASE_URL=postgres://[^:]+:||; s|@db:5432.*$||' | tr -d '0-9a-zA-Z' | cat -A
```

A healthy hex password prints just `$`. Anything before the `$` is illegal.

Fix — copy the real password out of `.env` into `DATABASE_URL`, printing
nothing:

```bash
cp .env .env.bak
PW=$(grep '^POSTGRES_PASSWORD=' .env | cut -d= -f2-)
sed -i "s|^DATABASE_URL=.*|DATABASE_URL=postgres://magus:${PW}@db:5432/magus?sslmode=disable|" .env
shred -u .env.bak 2>/dev/null || rm -f .env.bak
```

**No volume wipe was needed.** `db` had created its role from the real
`POSTGRES_PASSWORD` line, which was correct all along — only `DATABASE_URL`
was wrong. Had the password itself been wrong, `POSTGRES_PASSWORD` is read
only on the **first** boot of an empty volume, so fixing `.env` would not
change the existing role; that case needs `ALTER USER` inside the container,
or destroying the volume.

Result — all five services correct:

```
db        Up (healthy)
migrate   Exited (0)      <- success, it runs once and stops
web       Up (healthy)
poller    Up
caddy     Up  0.0.0.0:80->80, 0.0.0.0:443->443
```

---

### Step 14 — Verify ✅

```bash
docker compose --project-directory . -f deploy/docker/compose.yaml ps
docker compose --project-directory . -f deploy/docker/compose.yaml logs migrate
docker compose --project-directory . -f deploy/docker/compose.yaml logs caddy --tail 30
```

Check the `migrate` log, not just its status: it exits `0` even when it finds
nothing to do, so a green status alone does not prove the schema exists. Look
for four `applying /migrations/<module>` lines and
`all 4 migration directories applied successfully`.

From your own machine:

```bash
curl -s -o /dev/null -w '%{http_code}\n' https://<your-domain>/healthz
```

#### Third blocker: `/healthz` answers 404 to HEAD

`web` served real traffic correctly but stayed `Up (unhealthy)`. The log
showed the probe failing:

```
[GIN] ... | 404 | ... | ::1 | HEAD "/healthz"
```

`wget --spider` sends **HEAD**. `internal/gateway/gateway.go:182` registers
`/healthz` with `r.GET` only, and Gin answers a HEAD on a GET-only route with
404. The endpoint was never broken — the probe was.

Fixed in `deploy/docker/compose.yaml` by probing with a GET:

```yaml
test: ["CMD", "wget", "-q", "-O", "/dev/null", "http://localhost:8080/healthz"]
```

**The same trap catches `curl -I`**, which is also a HEAD request.
`docs/0-set-up/deployment.md` §8.9 had it wrong too and was corrected in the
same change. To check from outside, use a GET:

```bash
curl -s -o /dev/null -w '%{http_code}\n' https://<your-domain>/healthz
```

Follow-up, now done: uptime monitors (UptimeRobot and similar) often probe
with HEAD and would have reported the site down. `internal/gateway/gateway.go`
registers `r.HEAD("/healthz", ...)` beside the GET. The handler is unchanged —
Go's HTTP server drops the body for a HEAD response, and the status code is
all a monitor reads.

#### Expected noise

Within minutes of the domain going live, unknown IPs appear in the `web` log
hitting `/` and `/login`. Those are internet scanners finding a new host.
They get the login gate and nothing else. Normal, not an incident.

---

### Step 15 — Daily backup cron ✅

Test by hand first. Never put an untested command in cron — there it fails
silently at night.

```bash
make backup-db && ls -la backups/
```

Create the log file with the right owner. Cron runs as `magus`, and `magus`
cannot write into `/var/log` by default; without this the job fails on
permissions and writes nothing, including no error you would ever see.

```bash
sudo touch /var/log/magus-backup.log && sudo chown magus:magus /var/log/magus-backup.log
```

Then `crontab -e` and add one line — set here to **01:00**:

```
0 1 * * * cd /home/magus/magus-tesla-api && make backup-db >> /var/log/magus-backup.log 2>&1
```

- `cd` first — `backup-db.sh` builds its compose paths relative to the repo root.
- `2>&1` — errors go to the log too, not only normal output.
- Call `make`, not the script directly. The Makefile does `include .env` +
  `export`, which is how `POSTGRES_USER` and `POSTGRES_DB` reach the script;
  it fails fast without them.

The script writes `backups/magus-YYYY-MM-DD.sql.gz` and deletes dumps older
than 7 days. `backups/` is gitignored — the dumps hold every row in the
database.

Check the next day:

```bash
ls -la backups/ && cat /var/log/magus-backup.log
```

---

### Step 16 — Tesla domain verification ✅

Retried after Caddy issued the certificate, and it was accepted:

```
https://<your-domain>/connect/tesla/callback
```

The earlier rejection — *"El dominio no es válido... registrado ante una
autoridad certificadora"* — was ordering, not misconfiguration. Tesla verifies
the domain over HTTPS, so the certificate must exist first. **Register the
Tesla redirect URI after the first successful deploy, never before.**

---

## Open follow-ups

Nothing here blocks the running deploy.

| # | Item | Status |
|---|---|---|
| F1 | Confirm the rotated Tesla + Google secrets reached the VPS | ⬜ |
| F2 | Check `timedatectl` — backup may run before the poller | ⬜ |
| F3 | Move the Tesla public key off Netlify onto the app domain | ⬜ |
| F4 | Verify the first poller run | ⬜ |
| F5 | SSH hardening (disable root login + password auth) | ⬜ |
| F6 | Log rotation for `/var/log/magus-backup.log` | ⬜ |
| F7 | `HEAD /healthz` returned 404 | ✅ |

### F1 — Rotated secrets on the VPS

`TESLA_CLIENT_SECRET` and `GOOGLE_CLIENT_SECRET` are the same app on your
machine and the VPS, so rotating one means updating both. Env vars are read
when a container is **created**, so editing `.env` alone changes nothing:

```bash
nano .env
docker compose --project-directory . -f deploy/docker/compose.yaml up -d --force-recreate
```

Then sign in once through Google, and once through "Connect your Tesla", to
prove both secrets took.

### F2 — Backup timing vs. the poller

Cron uses the **server** clock, which is UTC on a default Ubuntu. The poller
runs at 03:30 `POLLER_TIMEZONE` (default `America/Bogota`) = 08:30 UTC. A
backup at 01:00 UTC therefore runs *before* the night's collection, so every
dump is a night behind.

```bash
timedatectl | grep "Time zone"
```

If it says UTC, move the cron hour to just after the poller — `30 9 * * *`
gives it an hour of headroom.

### F3 — Move the Tesla public key off Netlify

The EC public key is still served from `magus-monitor.netlify.app` at
`/.well-known/appspecific/com.tesla.3p.public-key.pem`, and redeploying it
needs a separate `netlify deploy` (see `CLAUDE.md` §External Services).

Now that the app has its own domain and its own reverse proxy, the key belongs
on the app's domain: one place to deploy, one certificate, one thing to renew,
and the Netlify site and its CLI step disappear.

Shape of the work: serve the file from `deploy/docker/Caddyfile`, then update
the Tesla partner registration to the app's domain. Not started — Netlify is
still the live source and it works.

### F4 — First poller run

It has not run yet. After 03:30 Bogota:

```bash
tail -100 ~/magus-logs/poller.log
```

### F5 — SSH hardening

Root login and password authentication are still enabled. With the `magus` key
login proven working, both can be turned off in `/etc/ssh/sshd_config`
(`PermitRootLogin no`, `PasswordAuthentication no`). **Keep a second SSH
session open while doing it** — a mistake here locks you out of the VPS.

### F6 — Log rotation

`/var/log/magus-backup.log` grows without limit. A `logrotate` drop-in under
`/etc/logrotate.d/` would cap it. Small file, slow growth — low urgency.

### F7 — `HEAD /healthz` ✅

`internal/gateway/gateway.go` now registers `r.HEAD("/healthz", ...)` beside
the GET, so uptime monitors that probe with HEAD — and `curl -I` — get the
real status instead of 404. The handler is unchanged: Go drops the body for a
HEAD response, and the status code is all a monitor reads.

---

## Connecting to the database from your machine

`db` publishes its port on **loopback only**:

```yaml
ports:
  - "127.0.0.1:5432:5432"
```

The `127.0.0.1:` prefix is the whole security model. Written as `"5432:5432"`
Docker publishes on `0.0.0.0` and writes iptables rules that **bypass ufw**,
so the database would be open to the internet with no firewall rule able to
stop it. With the prefix, the port exists only on the VPS's own loopback.

So the connection must travel through SSH. **The database host is always
`localhost`, never the VPS IP.** Putting the VPS IP in the host field is the
one mistake that looks right and always times out — nothing answers on that
address, by design.

Read the password on the VPS without printing the rest of the file:

```bash
grep '^POSTGRES_PASSWORD=' ~/magus-tesla-api/.env | cut -d= -f2-
```

### Option A — a GUI client's own SSH tunnel (used here, with Beekeeper Studio)

No extra terminal. The client logs into the VPS first, then opens the database
connection **from there** — which is why the host is `localhost`.

Main connection fields, evaluated from the VPS:

| Field | Value |
|---|---|
| Host | `localhost` |
| Port | `5432` |
| User | `POSTGRES_USER` from `.env` |
| Password | `POSTGRES_PASSWORD` from `.env` |
| Database | `POSTGRES_DB` from `.env` |

Then enable the client's **SSH Tunnel** section:

| Field | Value |
|---|---|
| SSH Host | the VPS IP |
| SSH Port | `22` |
| SSH User | `magus` |
| Auth | your SSH key, or the `magus` password |

### Option B — a manual tunnel

Terminal 1, left running:

```bash
ssh -L 5433:localhost:5432 magus@<VPS_IP>
```

Then connect with **no** SSH section, host `localhost`, port **5433**. Here
`localhost:5433` is your own machine and `ssh -L` forwards it to the VPS. Port
5433 avoids clashing with a Postgres already running locally on 5432.

Check the tunnel from the command line before blaming the GUI:

```bash
ssh -L 5433:localhost:5432 magus@<VPS_IP> -N &
psql "postgres://<user>:<password>@localhost:5433/<db>?sslmode=disable" -c '\dt'
```

If `psql` lists tables, the tunnel is fine and anything left is a client
field.

Verify the binding on the VPS after any compose change:

```bash
ss -ltnp | grep 5432      # must show 127.0.0.1:5432, never 0.0.0.0:5432
```

---

## Restarting and updating

| You changed | Command (from the repo root) |
|---|---|
| Code | `git pull && make docker-up` |
| `compose.yaml` | `make docker-up` |
| `.env` | `docker compose --project-directory . -f deploy/docker/compose.yaml up -d --force-recreate` |
| Nothing, just bounce one service | `docker compose --project-directory . -f deploy/docker/compose.yaml restart web` |

**`restart` does not apply port, limit or env changes.** It restarts the
process inside the existing container; those settings belong to the container
itself. Use `up -d`.

`make docker-down` stops everything and **keeps** the named volumes, so data
survives. `down -v` deletes them — see `docs/1-deploy/docker.md` §11.

---

## Never print a compose file's resolved config

```bash
docker compose --project-directory . -f deploy/docker/compose.yaml config    # DON'T
```

`config` interpolates `env_file` and prints **every secret in `.env`** —
client secrets, session secret, Tesla tokens, database password — to the
terminal, and into any log or transcript capturing it. This happened during
this deploy and forced a credential rotation.

To check the file is valid, read it, or strip the environment first:

```bash
docker compose --project-directory . -f deploy/docker/compose.yaml config --format json \
  | jq 'del(.services[].environment)'
```

If secrets are ever exposed, rotate `TESLA_CLIENT_SECRET` and
`GOOGLE_CLIENT_SECRET` (shared between your machine and the VPS), revoke the
Tesla tokens and re-run `go run ./cmd/setup`, then regenerate `SESSION_SECRET`
— rotating it logs every user out.
