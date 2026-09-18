# Deploying the API to Railway

One Railway service, built from `Dockerfile` at the repo root. Postgres stays on
Supabase. The NATS worker is not deployed — the gateway does not use it.

The web app deploys separately from `autonex-crm-app`. Keep both services in the
**same Railway project and region** so `${{…}}` variable references work between
them and the extra hop between web and API stays cheap.

---

## 1. Prepare Supabase for a remote client

Use the **session-mode pooler** connection string, not the direct host:

```
postgresql://postgres.<ref>:<password>@aws-0-<region>.pooler.supabase.com:5432/postgres?sslmode=require
```

- The username on the pooler is `postgres.<ref>`, **not** plain `postgres`.
  Copying the pooler host while keeping the direct-connection username fails
  authentication.
- The direct host (`db.<ref>.supabase.co`) is IPv6-only and Railway has no IPv6
  egress. Using it fails before any SQL runs, with:

  ```
  error: dial tcp [2406:...]:5432: connect: network is unreachable
  ```

  A bracketed IPv6 address in that message always means the direct host — switch
  to the pooler, nothing else is wrong.
- Port **5432** on the pooler is session mode; use it. Port **6543** is
  transaction mode, which breaks two things at once: pgx's prepared statements
  (needs `&default_query_exec_mode=exec`) and golang-migrate's session-level
  advisory lock, which has no workaround.
- URL-encode `@`, `#`, `:` etc. in the password. golang-migrate uses lib/pq while
  the gateway uses pgx, and lib/pq is the stricter parser of the two — a URL the
  API accepts can still fail in the pre-deploy migration.

`scripts/predeploy.sh` checks for all three of these at deploy time and prints a
named warning rather than a raw driver error.

## 2. Create the service

```bash
npm i -g @railway/cli
railway login
railway init            # creates the project, if it does not exist yet
railway add             # create an empty service, name it "api"
```

In the Railway dashboard, on the **api** service:

- **Settings → Source**: this repository
- **Settings → Config-as-code → Railway Config File**: `railway.json`
- **Settings → Networking**: attach `apidealbridge.autonexai360.com`
- **Settings → Region**: the region closest to your Supabase project
  (`Southeast Asia (Singapore)` for a Mumbai/Singapore Supabase). This is the
  single biggest latency lever you have — every request makes several DB round
  trips.

### Variables

```
DATABASE_URL=<the pooler URL from step 1>
JWT_SECRET=<openssl rand -base64 48>
JWT_ISSUER=go-crm
JWT_ACCESS_TTL=15m
JWT_REFRESH_TTL=720h
APP_TIMEZONE=Asia/Kolkata
WEB_APP_URL=https://dealbridge.autonexai360.com
OIDC_REDIRECT_BASE=https://apidealbridge.autonexai360.com/api/v1/auth/sso
INTEGRATIONS_REDIRECT_BASE=https://apidealbridge.autonexai360.com/api/v1/integrations
GOOGLE_CLIENT_ID=...
GOOGLE_CLIENT_SECRET=...
GITHUB_CLIENT_ID=...
GITHUB_CLIENT_SECRET=...
SSO_ALLOWED_DOMAINS=autonexai360.com
SSO_DEFAULT_ORG_ID=...
GOMEMLIMIT=384MiB
```

- **Set neither `GATEWAY_ADDR` nor `PORT`.** Railway provides `PORT` itself as
  long as you have not defined one, and healthchecks run against that same port;
  the gateway binds it. Defining either by hand is how you end up listening on a
  port nothing routes to ("Application failed to respond").
- **`WEB_APP_URL` must match the web origin exactly** (scheme + host, no trailing
  slash) or CORS silently blocks every authenticated call. Hardcode the real
  domain rather than using `${{web.RAILWAY_PUBLIC_DOMAIN}}` — the two services
  now live in different repositories and the web service may not exist in this
  project when `api` first deploys.
- **`JWT_SECRET` must carry over unchanged** from any previous deployment, or
  every signed-in user is signed out at cutover.
- `GOMEMLIMIT` at ~75% of your plan's memory makes Go's GC back off instead of
  getting OOM-killed under load.

Both `dealbridge.autonexai360.com` and `apidealbridge.autonexai360.com` sit under
`autonexai360.com`, which is what keeps the `SameSite=Lax` refresh cookie
working. Do not move either service to a different registrable domain without
reading [AUTH.md](AUTH.md#the-same-site-constraint-read-this-before-changing-domains).

## 3. Register the OAuth callbacks

In the Google and GitHub consoles:

```
https://apidealbridge.autonexai360.com/api/v1/auth/sso/google/callback
https://apidealbridge.autonexai360.com/api/v1/auth/sso/github/callback
https://apidealbridge.autonexai360.com/api/v1/integrations/google/callback
```

The third is a separate consent (calendar access) and is easy to forget. Enable
the Google Calendar API on the project as well.

## 4. Deploy

Connect the service to the repo (**Settings → Source**); Railway builds on push
to `main`.

Migrations run automatically: `railway.json` sets a **pre-deploy command** that
runs `migrate … up` against `$DATABASE_URL` before the new container takes
traffic. If the migration fails the deploy is aborted and the old version keeps
serving. (If the field is ignored by your Railway plan, set the same command in
**Settings → Deploy → Pre-Deploy Command**.)

## 5. Verify

```bash
API=https://apidealbridge.autonexai360.com

curl -i  $API/healthz                                    # -> 200 ok
curl -sH 'Accept-Encoding: gzip' -o /dev/null -w '%{size_download}\n' \
        $API/api/v1/dashboard/summary                    # -> gzipped

# CORS preflight must reflect the web origin and allow credentials
curl -si -X OPTIONS $API/api/v1/leads \
  -H 'Origin: https://dealbridge.autonexai360.com' \
  -H 'Access-Control-Request-Method: GET' | grep -i access-control
```

Then the one test that matters: temporarily set `JWT_ACCESS_TTL=30s`, sign in on
the web app, wait a minute and click something. Staying signed in proves the
refresh cookie crosses between the two domains. Restore the TTL afterwards.

---

## What makes the build and the runtime fast

- Multi-stage Dockerfile ordered so the module download layer only re-runs when
  `go.mod`/`go.sum` change. Source edits rebuild just the final layer.
- `CGO_ENABLED=0 -trimpath -ldflags="-s -w"` on a ~8 MB alpine base: small image,
  fast pull, fast cold start.
- gzip on API responses (`middleware.Compress`), which is where the list
  endpoints spend their bytes.
- The pgx pool is tuned for a pooled remote Postgres (short idle lifetime, health
  checks) in `pkg/database/pool.go`. `maxConns = 8` is the right starting point
  for one replica against Supavisor; raise it only after you see wait time in the
  pool, and keep `numReplicas × maxConns` under your Supabase connection limit.

### Optional: BuildKit cache mounts

`--mount=type=cache` adds partial reuse on top of layer caching — the case where
`go.sum` changed and only a few modules are actually new. Railway's Metal builder
accepts them only in this exact form:

```dockerfile
RUN --mount=type=cache,id=s/<api-service-id>-gomod,target=/go/pkg/mod \
    --mount=type=cache,id=s/<api-service-id>-gobuild,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build ...
```

The service id must be **hardcoded** — the mount flag does no variable expansion,
so `${RAILWAY_SERVICE_ID}` fails with "cache mount ID is not prefixed with cache
key". Copy it from the service's URL. That hardcoding is why it is not the
default: it pins the Dockerfile to one Railway project and breaks plain
`docker build` reuse.

## Scaling

- Horizontal: raise `numReplicas` in `railway.json`. The gateway is stateless
  (JWT + DB), so replicas need no coordination — just watch the total DB
  connection count.
- The pre-deploy migration runs once per deploy regardless of replica count.

## Database lifecycle

`migrations/` holds a **squashed** `000001_init` — the whole schema in one file,
every statement `IF NOT EXISTS`. Two consequences worth knowing before you touch
it:

- **It only applies cleanly to an empty database.** golang-migrate refuses to run
  at all against a database whose `schema_migrations` version is absent from the
  source ("no migration found for version N"), which is what happens to any
  environment migrated before the squash.
- **Never re-squash a schema that is already deployed.** Because every statement
  is `IF NOT EXISTS`, running the init against a populated database silently
  no-ops on existing tables, skips new columns, and *reports success* — the
  failure surfaces later as missing-column errors at runtime. Add changes as new
  numbered migrations instead.

The sequence skips `000012`. golang-migrate does not require contiguous version
numbers, so this is harmless, but it means a migration was dropped at some point
and the numbering is not a count.

Environments must not share a database. Local development and production each get
their own Supabase project; a local `migrate up` against production is how the
schema and the migration history drift apart in the first place.
