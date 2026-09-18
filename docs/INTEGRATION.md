# Integrating a client

This service has two clients: the Astro/React web app and the Expo mobile app,
both in `autonex-crm-app`. They connect through configuration only — there is no
shared code and no build-time dependency between the repositories.

## The three values that connect the repos

| Value | Set on | Must be |
|---|---|---|
| `PUBLIC_API_URL` | web app | this service's public origin |
| `EXPO_PUBLIC_API_URL` | mobile app | this service's public origin |
| `WEB_APP_URL` | **this service** | the web app's public origin, exactly |

Production:

```
# autonex-crm-app (web)     PUBLIC_API_URL = https://apidealbridge.autonexai360.com
# autonex-crm-api           WEB_APP_URL    = https://dealbridge.autonexai360.com
```

`WEB_APP_URL` is compared as a string for CORS and used as the SSO redirect
target, so a scheme mismatch, a `www.` prefix or a trailing path breaks both.

`PUBLIC_API_URL` is inlined into the web client bundle by Vite **at build time**.
Changing it requires a redeploy of the web service, not a restart.

## OAuth callbacks

Register **four** URLs — two providers × the SSO base, plus the separate
integrations base for Google Calendar. Google matches redirect URIs exactly.

```
https://apidealbridge.autonexai360.com/api/v1/auth/sso/google/callback
https://apidealbridge.autonexai360.com/api/v1/auth/sso/github/callback
https://apidealbridge.autonexai360.com/api/v1/integrations/google/callback
http://localhost:8080/api/v1/auth/sso/google/callback          # local dev
```

Signing in and connecting a calendar are separate consents, which is why
`OIDC_REDIRECT_BASE` and `INTEGRATIONS_REDIRECT_BASE` are separate variables.
Enable the Google Calendar API on the project or the integrations flow fails at
the token exchange.

## Minimum viable client

Four things, in order:

1. **Base URL** from config, trailing slashes trimmed.
2. **One fetch chokepoint.** Every call goes through a single function that
   attaches `Authorization: Bearer`, sets `Accept: application/json`, and
   normalises `{ "error": … }` into a typed error. Do not scatter `fetch` calls.
3. **Single-flight refresh on 401.** One refresh at a time — see
   [AUTH.md](AUTH.md). This is a correctness requirement, not an optimisation.
4. **Resource modules** holding one `BASE = "/api/v1/<domain>"` constant each.

Two details that bite:

- Do **not** set `Content-Type` when the body is `FormData`. The browser must set
  it itself so the multipart boundary is included; a boundary-less header makes
  every upload fail to parse server-side.
- `204` responses have no body. Parsing them as JSON throws.

## Worked example

```bash
API=http://localhost:8080

# 1. Sign in (browser mode: refresh token lands in the cookie jar)
curl -sc /tmp/jar -X POST "$API/api/v1/auth/login" \
  -H 'Content-Type: application/json' \
  -d '{"email":"you@autonexai360.com","password":"…"}' | tee /tmp/login.json
TOKEN=$(jq -r .token /tmp/login.json)

# 2. An authenticated call
curl -s "$API/api/v1/leads?limit=5" -H "Authorization: Bearer $TOKEN" | jq '.total, .items[0].id'

# 3. Refresh (rotates the token; the cookie jar is updated in place)
curl -sb /tmp/jar -c /tmp/jar -X POST "$API/api/v1/auth/refresh" | jq -r .token

# 4. Native mode: refresh token in the body, no cookie anywhere
curl -s -X POST "$API/api/v1/auth/login" \
  -H 'Content-Type: application/json' -H 'X-Auth-Mode: token' \
  -d '{"email":"you@autonexai360.com","password":"…"}' | jq -r .refreshToken
```

## Health and readiness

```
GET /healthz → 200 "ok"
```

No auth, no database round-trip — it reports that the process is up and serving,
which is what a load balancer needs. It is not a database readiness probe.

## Conventions worth knowing before writing a client

- **Pagination.** List endpoints take `?limit=` (default 25, max 100) and
  `?offset=`, and return `{ items, total, limit, offset }`. Some also return
  `counts` and `stages` for funnel display.
- **Dates** are ISO-8601 strings. "Overdue" and "due today" are computed
  server-side against `APP_TIMEZONE`, not against the client's clock — do not
  recompute them locally or the two will disagree across a timezone boundary.
- **Money** is a number, not a string, and not in minor units.
- **Nullability** is meaningful: `null` means cleared, an absent key on a `PUT`
  means unchanged.

Full endpoint reference: [API.md](API.md). Error semantics: [ERRORS.md](ERRORS.md).

## Known gaps

- **SSO is browser-only.** A native client needs `expo-auth-session` and a
  custom-scheme callback, which the gateway does not implement yet.
- **No OpenAPI document.** Client types are hand-written in
  `autonex-crm-app/packages/types` and mirror this documentation rather than
  being generated from it. Until that changes, `docs/API.md` is the contract of
  record and a change here is a breaking change there.
