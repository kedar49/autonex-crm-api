# go-CRM API Reference

Base URL: `https://apidealbridge.autonexai360.com` · all paths prefixed `/api/v1`
Local: `http://localhost:8080`

See also: [AUTH.md](AUTH.md) (tokens, cookies, SSO) · [ERRORS.md](ERRORS.md) ·
[INTEGRATION.md](INTEGRATION.md) (wiring a client up)

**Auth** — every endpoint requires `Authorization: Bearer <accessToken>` except those
marked **public**. The token comes from `/auth/login`, `/auth/register` or `/auth/refresh`.

**Errors** — `{ "error": "message" }` with `400` validation, `401` no/invalid token,
`403` forbidden, `404` not found, `409` conflict, `428` precondition (connect Google),
`500` server. Deletes return `204` with no body.

**Pagination** — list endpoints take `?limit=` (default 25, max 100) and `?offset=`,
and return `{ items, total, limit, offset }`.

---

## Auth — `/auth`

| Method | Path | Body / Params | Returns |
| --- | --- | --- | --- |
| POST | `/register` **public** | `email`, `password` (≥8), `name?` | `{ token, user }` · `409` if email exists |
| POST | `/login` **public** | `email`, `password` | `{ token, user }` · `401` on bad credentials |
| POST | `/refresh` **public** | — (reads `refresh_token` cookie) | `{ token, user }` · `401` if no session |
| POST | `/logout` **public** | — | `204`, clears the cookie |
| GET | `/sso/{provider}` **public** | `provider` = `google` \| `github` | `302` to the provider |
| GET | `/sso/{provider}/callback` **public** | `code`, `state` (from provider) | `302` to `<web>/app#token=…`, or `/app/login?error=…` |
| GET | `/me` | — | `user` |

`user`: `id`, `email`, `name`, `orgId`, `authProvider`

**Client modes.** By default the refresh token is delivered as an HttpOnly cookie
(`gocrm_refresh`, `Path=/api/v1/auth`, `SameSite=Lax`) and never appears in a
response body — that is the browser path. A native client sends
`X-Auth-Mode: token` on `/register`, `/login` and `/refresh`; the response then
carries `refreshToken` in the JSON body and **no cookie is set**. Such a client
posts `{ "refreshToken": "..." }` to `/refresh` and `/logout`. See
[AUTH.md](AUTH.md).

SSO is restricted by `SSO_ALLOWED_DOMAINS`; new SSO users join `SSO_DEFAULT_ORG_ID`.

---

## Leads — `/leads`

| Method | Path | Body / Params | Returns |
| --- | --- | --- | --- |
| GET | `/` | `filter?`, `limit?`, `offset?` | `{ items, total, limit, offset, counts, stages }` |
| POST | `/` | `Lead` input | `Lead` |
| GET | `/{id}` | — | `Lead` |
| PUT | `/{id}` | `Lead` input | `Lead` |
| DELETE | `/{id}` | — | `204` |
| POST | `/{id}/advance` | `toStage`, `followUpAt?`, `clearFollowUp?`, `note?`, `meetingAt?`, `meetingMinutes?` | `{ lead, meeting? }` · `428` if Google not connected |
| POST | `/{id}/convert` | `dealTitle?`, `amount?`, `expectedCloseDate?`, `callNotes?`, `dealStage?` | conversion result |

**Input** — `firstName`, `lastName?`, `title?`, `email?`, `phone?`, `linkedinUrl?`,
`company?`, `accountId?`, `contactId?`, `source?`, `notes?`, `value?`, `stage`,
`ownerUserId?`, `followUpAt?`

**Lead** — `id`, `firstName`, `lastName`, `title`, `email`, `phone`, `linkedinUrl`,
`source`, `notes`, `value`, `stage`, `accountId`, `accountName`, `accountIndustry`,
`company`, `contactId`, `ownerUserId`, `ownerName`, `ownerEmail`, `followUpAt`,
`overdue`, `dueToday`, `lastContactedAt`, `convertedAt`, `convertedDealId`,
`convertedContactId`, `createdAt`, `updatedAt`

`filter` accepts a stage name, or `overdue` / `due_today` / `open`.
`stage`: `new`, `initial count`, `deck sent`, `call scheduled`, `call done`,
`proposal sent`, `closed`, `not interested`

`meeting` (present only when `meetingAt` was sent): `googleEventId`, `meetLink`,
`title`, `startAt`, `endAt`

---

## Deals — `/deals`

| Method | Path | Body / Params | Returns |
| --- | --- | --- | --- |
| GET | `/` | — | `{ stages, deals }` (whole board) |
| POST | `/` | `Deal` input | `Deal` |
| GET | `/{id}` | — | `Deal` |
| PUT | `/{id}` | `Deal` input | `Deal` |
| DELETE | `/{id}` | — | `204` |
| PATCH | `/{id}/move` | `stage`, `index` | `Deal` · emails the org |

**Input** — `title`, `description?`, `remark?`, `amount`, `stage`, `ownerUserId?`,
`contactId?`, `accountId?`, `leadId?`, `expectedCloseDate?`, `locationIds?`

**Deal** — `id`, `title`, `description`, `amount`, `stage`, `ownerUserId`, `ownerName`,
`ownerEmail`, `contactId`, `contactName`, `accountId`, `expectedCloseDate`, `position`,
`remark`, `locationIds`, `createdAt`, `updatedAt`

`locationIds` are ids from the deal's account under
[`/accounts/{id}/locations`](#locations-sites--accountsidlocations) — the sites
this deal delivers to. A deal can name several. Sending the field replaces the
whole set; omitting it leaves the existing links alone.

`stage`: `discovery`, `site_assessment`, `quote_sent`, `negotiation`, `delivery`,
`post_delivery`, `won`

> `index` is accepted but not persisted — there is no position column, so ordering
> within a column is derived from creation time.

---

## Accounts — `/accounts`

| Method | Path | Body / Params | Returns |
| --- | --- | --- | --- |
| GET | `/` | `limit?`, `offset?` | paged `Account` |
| POST | `/` | `Account` input | `Account` |
| GET | `/{id}` | — | `Account` |
| PUT | `/{id}` | `Account` input | `Account` |
| DELETE | `/{id}` | — | `204` · `409` if contacts or deals reference it |
| GET | `/{id}/profile` | — | `{ account, profile, deals, quotes, invoices, contacts, leads }` |
| PUT | `/{id}/profile` | profile input | full profile payload |

**Input** — `name`, `website?`, `industry?`, `phone?`, `notes?`, `ownerUserId?`

**Account** — `id`, `name`, `website`, `industry`, `phone`, `notes`, `ownerUserId`,
`ownerName`, `ownerEmail`, `createdAt`, `updatedAt`, `contactCount`, `dealCount`

**Profile input** — account fields plus `tagline?`, `description?`, `primaryColor?`,
`bannerUrl?`, `plantLocations?`, `aiDetections?`, `hardwareSpecs?`, `amcStatus?`

### Locations (sites) — `/accounts/{id}/locations`

A company's physical sites. A deal names one or more of them, so a site is never
hard-deleted while work still points at it — it is archived instead.

| Method | Path | Body / Params | Returns |
| --- | --- | --- | --- |
| GET | `/{id}/locations` | `includeArchived?` = `true` | `{ items: Location[] }`, live sites first |
| POST | `/{id}/locations` | `LocationInput` | `Location` · `201` · `409` on a duplicate name |
| PUT | `/{id}/locations/{locationId}` | `LocationInput` | `Location` · `404` · `409` |
| DELETE | `/{id}/locations/{locationId}` | `archived?` = `false` to restore | `Location` · `200`, **not** `204` |

**LocationInput** — `name` (required, ≤200 chars), `city?`, `address?`,
`spocName?`, `spocPhone?`

**Location** — `id`, `accountId`, `name`, `city`, `address`, `spocName`,
`spocPhone`, `position`, `archivedAt`, `dealCount`

Three things that differ from the rest of this API:

- `DELETE` **archives** and returns the updated `Location` with `200`. It does
  not return `204` and it does not remove the row. Pass `?archived=false` to the
  same route to restore.
- `dealCount` counts live deals linked through `deal_locations`, so the UI can
  show the consequence before someone archives a site.
- Location names are unique per company; a clash is `409`, not `400`.

---

## Contacts — `/contacts`

| Method | Path | Body / Params | Returns |
| --- | --- | --- | --- |
| GET | `/` | `limit?`, `offset?` | paged `Contact` |
| POST | `/` | `firstName`, `lastName?`, `email?`, `phone?`, `accountId?` | `Contact` |
| GET | `/{id}` | — | `Contact` |
| PUT | `/{id}` | same as POST | `Contact` |
| DELETE | `/{id}` | — | `204` |

**Contact** — `id`, `firstName`, `lastName`, `email`, `phone`, `accountId`, `createdAt`

---

## Quotes — `/quotes`

| Method | Path | Body / Params | Returns |
| --- | --- | --- | --- |
| GET | `/` | `status?`, `limit?`, `offset?` | paged `Quote` |
| POST | `/` | `Quote` input | `Quote` |
| GET | `/{id}` | — | `Quote` with `items` |
| GET | `/{id}/pdf` | — | `text/html` · purchase-order layout, not the proposal |
| PUT | `/{id}` | `Quote` input | `Quote` · draft only |
| DELETE | `/{id}` | — | `204` · draft only |
| POST | `/{id}/status` | `status` | `Quote` |

**Input** — `title?`, `accountId` (required), `contactId?`, `dealId?`,
`ownerUserId` (required), `notes?`, `validUntil?`, `items[]`, `template?`,
`proposal?`

**Document templates** — `template` turns a quote into a structured proposal;
send it with `proposal` (arbitrary JSON, max 1 MB, stored on `quote_proposals`).
The only accepted value is `vigil_technocommercial`. Money always stays in
`items[]`, never in `proposal`, so the totals are the same everywhere.

**Item** — `description`, `quantity`, `unitPrice`, `discountPercent?`, `taxPercent?`

**Quote** — `id`, `number`, `title`, `status`, `currency`, `accountId`, `accountName`,
`contactId`, `contactName`, `dealId`, `dealTitle`, `ownerUserId`, `ownerName`,
`ownerEmail`, `notes`, `validUntil`, `subtotal`, `discountTotal`, `taxTotal`, `total`,
`sentAt`, `acceptedAt`, `declinedAt`, `createdAt`, `updatedAt`, `items`, `itemCount`,
`template` (null for a plain quote), `proposal` (GET `/{id}` only)

`status`: `draft` → `sent` → `approved` \| `rejected` \| `expired`. Totals are always
derived from the line items; a client-supplied total is never accepted.

---

## Invoices — `/invoices`

| Method | Path | Body / Params | Returns |
| --- | --- | --- | --- |
| GET | `/` | `status?` (incl. `overdue`), `limit?`, `offset?` | paged `Invoice` |
| POST | `/` | `Invoice` input | `Invoice` |
| POST | `/from-quote` | `quoteId`, `dueDate?` | `Invoice` · quote must be `approved` |
| GET | `/{id}` | — | `Invoice` with `items`, `payments` |
| GET | `/{id}/pdf` | — | `application/pdf` (GST format) |
| PUT | `/{id}` | `Invoice` input | `Invoice` · draft only |
| DELETE | `/{id}` | — | `204` · draft only |
| POST | `/{id}/status` | `status` | `Invoice` |
| POST | `/{id}/payments` | `amount`, `paidOn?`, `method?`, `reference?`, `note?` | `Invoice` · auto-settles when covered |

**Input** — `title?`, `accountId` (required), `contactId?`, `dealId?`, `ownerUserId?`,
`notes?`, `issueDate?`, `dueDate?`, `items[]`

**Invoice** — `id`, `number`, `title`, `status`, `currency`, `quoteId`, `quoteNumber`,
`accountId`, `accountName`, `contactId`, `contactName`, `dealId`, `dealTitle`,
`ownerUserId`, `ownerName`, `ownerEmail`, `notes`, `issueDate`, `dueDate`, `subtotal`,
`discountTotal`, `taxTotal`, `total`, `amountPaid`, `balance`, `overdue`, `sentAt`,
`paidAt`, `voidedAt`, `createdAt`, `updatedAt`, `items`, `payments`, `itemCount`

`status`: `draft`, `sent`, `paid`, `overdue`, `void`. `balance` and `overdue` are
derived, never stored.

---

## Activities — `/activities`

| Method | Path | Body / Params | Returns |
| --- | --- | --- | --- |
| GET | `/` | `leadId?`, `dealId?`, `accountId?`, `contactId?`, `quoteId?`, `invoiceId?`, `limit?` | `Activity[]`, newest first |
| POST | `/` | `Activity` input | `Activity` |
| PUT | `/{id}` | `Activity` input | `Activity` · not `system` entries |
| DELETE | `/{id}` | — | `204` · not `system` entries |

**Input** — `kind`, `subject`, `body?`, `occurredAt?`, `durationMinutes?`, plus exactly
one of `leadId` / `dealId` / `accountId` / `contactId` / `quoteId` / `invoiceId`

**Activity** — the input fields plus `id`, `leadName`, `dealTitle`, `accountName`,
`contactName`, `createdBy`, `authorName`, `authorEmail`, `createdAt`

`kind`: `note`, `call`, `email`, `meeting`, `system`

---

## Delivery — `/delivery`

| Method | Path | Body / Params | Returns |
| --- | --- | --- | --- |
| GET | `/` | — | `{ items, total }` |
| POST | `/` | `Row` input | `Row` |
| PUT | `/{id}` | `Row` input | `Row` |
| DELETE | `/{id}` | — | `204` |
| POST | `/reorder` | `{ ids: [] }` — full order | `204` |
| POST | `/import/preview` | `multipart/form-data` file (CSV or XLSX) | `Preview` — nothing is written |
| POST | `/import/commit` | `{ rows: [Row input] }` | `{ created, updated }` |

**Input** — `client`, `products?`, `locations?`, `totalCameras?`, `status?`,
`implementationDate?`, `currentStages?`, `keyContacts?`, `nextSteps?`, `notes?`

**Row** — the input fields plus `id`, `position`, `updatedBy`, `updatedByName`,
`createdAt`, `updatedAt`

**Preview** — `rows[]`, `errors[]`, `ignoredColumns[]`, `created`, `updated`, `unchanged`.
Each row: `sheetRow`, `action`, `values`, `existingId`, `changes`.

Import is two-phase on purpose: preview validates and diffs the sheet without writing,
then commit applies exactly the rows you send back.

---

## Dashboard — `/dashboard`

| Method | Path | Body / Params | Returns |
| --- | --- | --- | --- |
| GET | `/` | — | `Summary` |

**Summary** — `contacts`, `members`, `leads`, `deals`, `quotes`, `invoices`,
`attention[]`, `recent[]`

Each pipeline is `{ total, open, won, stages[] }` where `open`/`won` are **values**, not
counts. `attention` items: `kind`, `id`, `label`, `detail`, `days`, `amount`.

---

## Organization — `/org`

| Method | Path | Body / Params | Returns |
| --- | --- | --- | --- |
| GET | `/` | — | `{ id, name, currency }` |
| PATCH | `/` | `name?`, `currency?` | organization |
| GET | `/members` | — | `Member[]` |
| GET | `/invitations` | — | `Invitation[]` |
| POST | `/invitations` | `email` | `{ inviteUrl, token }` |
| DELETE | `/invitations/{id}` | — | `204` |
| POST | `/invitations/accept` **public** | `token`, `password` | `{ token, user }` |

**Member** — `id`, `email`, `name`, `authProvider`, `createdAt`
**Invitation** — `id`, `email`, `expiresAt`, `createdAt`, `acceptedAt`

---

## Notifications — `/notifications`

| Method | Path | Body / Params | Returns |
| --- | --- | --- | --- |
| GET | `/` | `limit?`, `offset?` | `{ items, unreadCount }` |
| PATCH | `/{id}/read` | — | `204` |
| POST | `/read-all` | — | `204` |
| POST | `/subscribe` | `endpoint`, `p256dh`, `auth`, `userAgent?` | `201` — web push |
| DELETE | `/unsubscribe` | `endpoint` | `204` |
| POST | `/devices` | `token`, `platform?`, `deviceName?` | `204` — native push |
| DELETE | `/devices` | `token` | `204` |
| GET | `/stream` | — | `text/event-stream` (SSE, live delivery) |

**Item** — `id`, `orgId`, `userId`, `type`, `title`, `body`, `actionUrl`, `priority`,
`isRead`, `createdAt`

### Native push — `/devices`

`/subscribe` and `/devices` are two different transports and do not share a row.
`/subscribe` takes a **W3C Web Push** subscription (an endpoint plus an
encryption keypair) which only a browser can produce. `/devices` takes a single
opaque **Expo push token**, which is what a phone has.

`token` must look like `ExponentPushToken[…]`; anything else is `400`. The token
identifies the **installation, not the person** — registering a token already on
file moves it to the calling user, so a colleague signing in on a shared handset
takes over its notifications rather than both receiving them.

The app re-registers on every launch; the call is idempotent. A token Expo
reports as `DeviceNotRegistered` is deleted server-side on the next send, so an
uninstalled app stops costing a request per notification.

Every notification recorded for a user is pushed to their devices, carrying
`data.notificationId`, `data.type`, `data.actionUrl` and `data.priority` so the
app can route a tap without another round-trip, plus a `badge` set to the live
unread count.

Delivery is best-effort: the notification is durable once recorded and `GET /`
will return it regardless, so a push failure is logged and never fails the
action that triggered it. `EXPO_ACCESS_TOKEN` is optional and only needed when
the Expo project has enhanced security enabled.

---

## Integrations — `/integrations`

| Method | Path | Body / Params | Returns |
| --- | --- | --- | --- |
| GET | `/` | — | `Connection[]` |
| POST | `/google/connect` | — | `{ authUrl }` — navigate the browser to it |
| GET | `/google/callback` **public** | `code`, `state` | `302` to `<web>/app/team?connected=google` |
| DELETE | `/google` | — | `204` |

**Connection** — `provider`, `providerAccountId`, `scope`, `expiresAt`, `connectedAt`

Connecting grants `calendar.events` with offline access, which is what lets
`POST /leads/{id}/advance` create a Google Meet.

---

## Health

| Method | Path | Returns |
| --- | --- | --- |
| GET | `/healthz` **public** | `200 ok` — no `/api/v1` prefix |

---

## Not mounted

`internal/audit`, `internal/followups` and `internal/products` have handlers but are
not registered in `cmd/gateway/main.go`, so they are unreachable. Their routes are
omitted above.
