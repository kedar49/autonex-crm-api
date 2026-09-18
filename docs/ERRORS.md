# Errors

Every failure returns the same envelope:

```json
{ "error": "human-readable message" }
```

There is no error code field and no nested detail object. The status carries the
category; the message is meant to be shown to a user.

## Status codes

| Status | Meaning | What a client should do |
|---|---|---|
| `200` | OK | — |
| `201` | Created | — |
| `204` | No content (deletes, logout) | Do not parse a body; there is none |
| `400` | Validation — a field is missing or malformed | Show the message against the form |
| `401` | No token, invalid token, or expired session | Refresh **once** and replay; on a second 401, sign out |
| `403` | Authenticated but not permitted (role, or SSO domain) | Show the message; do not retry |
| `404` | Not found, or belongs to another organization | Treat as not found — see the note below |
| `409` | Conflict (duplicate email, concurrent edit) | Show the message; a retry will not help |
| `428` | Precondition required — a third-party connection is missing | Prompt the user to connect Google, then retry |
| `500` | Server error | Show a generic message; log and retry later |

## Notes that are not obvious from the table

**`401` is routine, not exceptional.** Access tokens live 15 minutes, so a normal
session produces a 401 roughly every quarter hour. The client is expected to
absorb it: refresh once, replay the request, show the user nothing. Only a 401
from `/auth/refresh` itself is a real sign-out. See [AUTH.md](AUTH.md).

**`404` hides cross-tenant access.** Every query is scoped by the `org` claim, so
a record that exists but belongs to another organization is reported as missing
rather than forbidden. This is intentional: a `403` would confirm the id exists.
Do not present "you do not have permission" on a 404.

**`428` is the Google Calendar case.** Advancing a lead to a stage that books a
meeting requires a connected Google account. The gateway returns `428` rather
than `400` so the client can tell "you gave me bad input" apart from "you need to
connect something first, then this exact request will work". The correct
handling is a connect prompt followed by a retry of the same request.

**Transport failures are not API errors.** A server that is down, a DNS failure
or a blocked CORS preflight produce no JSON at all. Clients should normalise
these to a distinct sentinel (the web client uses `ApiError` with `status: 0`)
rather than reporting them as a server error, because the remedy is different.

**`500` messages are generic on purpose.** Handlers log the underlying error
server-side and return a short message. Do not parse `error` strings to branch
on — match on status.
