# autonex-crm-api

The backend for Autonex DealBridge CRM: a Go modular monolith serving a JSON API
over HTTP, backed by PostgreSQL (Supabase).

Its clients — the web app and the mobile app — live in **`autonex-crm-app`** and
are connected to this service by configuration only. There is no shared code
between the repositories.

```
Browser  ──┐
           ├──► apidealbridge.autonexai360.com  ──►  Supabase Postgres
Mobile   ──┘            (this repo)
```

## Layout

```
cmd/
  gateway/       HTTP API edge — the only process deployed today
  worker/        NATS JetStream consumer (Slack/Google fan-out; not deployed)
  seed/          One-off database seeding
internal/        Domain modules, one package each, handler → service → store
  auth/          JWT, Argon2id, refresh rotation, OIDC/SSO
  leads/ deals/ accounts/ contacts/ quotes/ invoices/ delivery/
  activities/ dashboard/ followups/ dealtasks/ notify/ org/ integrations/ pdf/
pkg/             Shared infrastructure
  config/        Environment loading
  database/      pgx pool, Postgres error mapping
  httpx/         JSON responses, domain-error → status mapping
  middleware/    RequireJWT, role gates, CORS
  mailer/ events/ apperr/ paging/
migrations/      golang-migrate SQL, applied automatically before each deploy
scripts/         predeploy.sh (runs migrations), one-off codemods
docs/            API.md, AUTH.md, ERRORS.md, INTEGRATION.md, DEPLOY.md
```

Each `internal/` module owns its own schema and exposes a `chi` sub-router that
`cmd/gateway/main.go` mounts under `/api/v1/<domain>`. Modules talk to each other
through exported Go functions, not HTTP.

## Stack

Go 1.25 · chi · pgx v5 · golang-migrate · golang-jwt · Argon2id · NATS JetStream ·
PostgreSQL 16 / Supabase · excelize (XLSX import)

## Getting started

```bash
cp .env.example .env         # then fill in DATABASE_URL and JWT_SECRET
go mod download
make migrate-up              # needs golang-migrate on PATH
make run                     # gateway on :8080
```

```bash
curl http://localhost:8080/healthz    # -> ok
```

`make help` lists every target. Common ones: `make test` (race detector),
`make lint` (gofmt + vet), `make build`, `make docker`.

The `.env` file is searched for from the working directory upward, so `go run
./cmd/gateway` works from anywhere in the tree. Real environment variables always
win over `.env` values.

## Documentation

| | |
|---|---|
| [docs/API.md](docs/API.md) | Every endpoint, request shape and response shape |
| [docs/AUTH.md](docs/AUTH.md) | Access/refresh tokens, cookies, SSO, CORS, the same-site constraint |
| [docs/ERRORS.md](docs/ERRORS.md) | Status codes and the error envelope |
| [docs/INTEGRATION.md](docs/INTEGRATION.md) | How a client wires up; worked curl examples |
| [docs/DEPLOY.md](docs/DEPLOY.md) | Railway + Supabase deployment |

`docs/API.md` is the **contract of record**. The client types in
`autonex-crm-app/packages/types` are hand-written against it, so a change here is
a breaking change there until an OpenAPI document exists.

## Connecting to the frontend

Three values, and nothing else:

| Variable | Set on | Value |
|---|---|---|
| `WEB_APP_URL` | this service | `https://dealbridge.autonexai360.com` |
| `PUBLIC_API_URL` | `autonex-crm-app` (web) | `https://apidealbridge.autonexai360.com` |
| `EXPO_PUBLIC_API_URL` | `autonex-crm-app` (mobile) | `https://apidealbridge.autonexai360.com` |

`WEB_APP_URL` is compared as an exact string for CORS and used as the SSO
redirect target — a scheme mismatch or a trailing slash breaks both.

Both domains must stay under `autonexai360.com`: the refresh cookie is
`SameSite=Lax`, so splitting them across registrable domains silently ends every
session after 15 minutes. See
[AUTH.md](docs/AUTH.md#the-same-site-constraint-read-this-before-changing-domains).

## Conventions

- **Layering.** `handler.go` parses and writes HTTP; `service.go` holds the rules;
  `store.go` is the only place SQL lives. Handlers never touch the pool directly.
- **Tenancy.** Every query is scoped by the `org` claim on the access token. A
  record belonging to another organization is reported as `404`, not `403`.
- **Errors.** Domain errors are `errors.Is`-comparable sentinels mapped to status
  codes in `pkg/httpx`. Handlers do not build status codes from strings.
- **Time.** "Overdue" and "due today" are computed in the database against
  `APP_TIMEZONE`, which is set as the session time zone on every connection.
- **Migrations are forward-only in practice.** Add a new numbered pair; never
  edit an applied one. Every migration needs a working `.down.sql` — CI applies
  and reverses the whole sequence on every pull request.
