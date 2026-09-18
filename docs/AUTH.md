# Authentication

Two credentials, deliberately split.

| | Lifetime | Where the client keeps it | Sent as |
|---|---|---|---|
| **Access token** | `JWT_ACCESS_TTL` (15m) | memory | `Authorization: Bearer <jwt>` |
| **Refresh token** | `JWT_REFRESH_TTL` (30d) | HttpOnly cookie (browser) or secure storage (native) | cookie, or request body |

The access token is short-lived so a leaked one expires on its own. The refresh
token is the real credential, so in a browser it is kept where script cannot
reach it.

## Access token

HS256, signed with `JWT_SECRET`. Claims:

```json
{ "sub": "<user id>", "email": "…", "org": "<org id>", "role": "owner|admin|sales|account_manager|client",
  "iss": "<JWT_ISSUER>", "iat": 0, "exp": 0 }
```

`pkg/middleware.RequireJWT` enforces HS256 explicitly (rejecting `none` and
`RS256` alg-confusion) and **rejects any token without an `org` claim** — every
query in the CRM is org-scoped, so a request that cannot name its tenant has no
business reaching a handler. `role` is optional: it was added after `org`, so an
older token still authenticates but fails role checks.

Clients may decode the payload for display, but must not trust it. The gateway is
the only authority on validity.

## Refresh token

Opaque, single-use, rotated on every `/refresh`. Reusing a spent token is read as
theft and **kills every session for that user**.

That single fact drives the most important client-side rule:

> A client must serialise its refreshes. Two concurrent 401s must produce **one**
> `/refresh` call, not two. The second would spend an already-spent token and log
> the user out everywhere.

The web app does this with an in-flight promise (single-flight) in
`packages/api-client`. Any new client must do the same.

## Browser mode (default)

```
POST /api/v1/auth/login
  → 200 { "token": "<access jwt>", "user": {…} }
  → Set-Cookie: gocrm_refresh=…; HttpOnly; Path=/api/v1/auth; SameSite=Lax; Secure
```

Cookie attributes, and why:

- **HttpOnly** — script cannot read the long-lived credential.
- **`Path=/api/v1/auth`** — it is only ever sent to the four routes that need it,
  not attached to all 14 domain routers.
- **`SameSite=Lax`** — already blocks the cross-site POST a CSRF attempt needs.
- **`Secure`** — inferred from `OIDC_REDIRECT_BASE` starting with `https://`.

### The same-site constraint (read this before changing domains)

`SameSite=Lax` means the cookie is sent on same-site requests only. "Same site"
is decided by the **registrable domain**, not the origin — port and subdomain do
not matter.

| Web | API | Cookie sent? |
|---|---|---|
| `localhost:4321` | `localhost:8080` | yes (same site; port is ignored) |
| `dealbridge.autonexai360.com` | `apidealbridge.autonexai360.com` | yes (shared `autonexai360.com`) |
| `app.vercel.app` | `api.up.railway.app` | **no** |

The third row fails **silently**: login works, then every session dies at the
15-minute mark with no error anywhere. Both services must stay under
`autonexai360.com`. If that ever has to change, the fix is to serve the API
same-origin behind the web app's domain (a path prefix or a reverse proxy), or to
move the browser to token mode as well.

**How to verify after any domain change:** set `JWT_ACCESS_TTL=30s` on a staging
deploy, sign in, wait a minute, click anything. Staying signed in means the
cookie is being sent. Being bounced to the login screen means it is not. This
cannot be reproduced on localhost, where everything is same-site by definition.

## Token mode (native clients)

React Native has no dependable cookie jar across iOS and Android, and `SameSite`
is meaningless outside a browser. A native client opts in with a header:

```
POST /api/v1/auth/login
Header: X-Auth-Mode: token
  → 200 { "token": "<access jwt>", "user": {…}, "refreshToken": "<opaque>" }
  → no Set-Cookie
```

It stores `refreshToken` itself (`expo-secure-store`, Keychain / Keystore — never
`AsyncStorage`) and sends it back in the body:

```
POST /api/v1/auth/refresh   { "refreshToken": "<opaque>" }
POST /api/v1/auth/logout    { "refreshToken": "<opaque>" }
```

Rotation applies identically: the response carries a **new** `refreshToken` and
the old one is spent. Overwrite storage on every refresh, and serialise refreshes
exactly as the browser client does.

Opt-in by header rather than sniffing the User-Agent is deliberate: defaulting to
the body would hand the long-lived credential to script in the browser and undo
the HttpOnly cookie entirely.

`/refresh` also accepts a body token with no header, because a token that arrived
in the body has nowhere else to go back to.

## SSO

```
GET /api/v1/auth/sso/{google|github}
  → 302 to the provider   (sets a short-lived sso_state cookie for CSRF)
  → provider → GET /api/v1/auth/sso/{provider}/callback?code&state
  → 302 to <WEB_APP_URL>/app#token=<access jwt>   (+ refresh cookie)
```

The access token rides the **URL fragment**, which browsers never send to a
server and which therefore never reaches an access log. The web client reads it
once on boot and immediately strips it from the URL with `history.replaceState`
so it does not linger in history or get copy-pasted.

Failures redirect to `<WEB_APP_URL>/app/login?error=<message>` rather than
returning JSON — the user is mid-navigation, not mid-fetch.

Gates: `SSO_ALLOWED_DOMAINS` (comma-separated; empty means any domain the
provider allows) and `SSO_DEFAULT_ORG_ID` (the workspace new SSO users join;
empty gives each signup their own).

SSO is browser-only today. A native client needs `expo-auth-session` plus a
custom-scheme callback, which the gateway does not yet implement.

## CORS

`pkg/middleware.CORS` reflects exactly one origin: `WEB_APP_URL`, compared as a
string after trimming a trailing `/`. `Access-Control-Allow-Credentials: true` is
required for the refresh cookie to cross origins at all.

A scheme mismatch, a `www.` prefix or a stray path in `WEB_APP_URL` fails closed.
Verify with:

```bash
curl -si -X OPTIONS https://<api>/api/v1/leads \
  -H 'Origin: https://dealbridge.autonexai360.com' \
  -H 'Access-Control-Request-Method: GET' | grep -i 'access-control'
```

Native clients send no `Origin` header and are unaffected by CORS.

## Operational notes

- **Rotating `JWT_SECRET` signs everyone out immediately.** Refresh tokens are
  stored server-side and survive, so the next `/refresh` recovers the session —
  but every in-flight access token is dead at once. Rotate deliberately, never as
  a side effect of a deploy.
- `POST /logout` revokes the refresh token **server-side**, so the session is
  dead even if a copy of the cookie survives somewhere.
- A 401 from any endpoint means "refresh once and replay". A 401 from `/refresh`
  itself means the session is over — send the user to the login screen.
