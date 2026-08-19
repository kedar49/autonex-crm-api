-- SQL reference for the eventual sqlc migration; the module currently runs the
-- hand-written pgx repository in store.go. Keep the two in sync.
--
-- Every statement filters on org_id. A query here without it is a tenant-data
-- leak, not a missing filter.

-- name: ListContacts :many
SELECT id, first_name, last_name, email, phone, account_id, created_at
FROM contacts
WHERE org_id = $1
ORDER BY created_at DESC, id
LIMIT $2 OFFSET $3;

-- name: CountContacts :one
SELECT count(*) FROM contacts WHERE org_id = $1;

-- name: GetContact :one
SELECT id, first_name, last_name, email, phone, account_id, created_at
FROM contacts
WHERE org_id = $1 AND id = $2;

-- name: CreateContact :one
INSERT INTO contacts (org_id, first_name, last_name, email, phone, account_id)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, first_name, last_name, email, phone, account_id, created_at;

-- name: UpdateContact :one
UPDATE contacts
SET first_name = $3, last_name = $4, email = $5, phone = $6, account_id = $7
WHERE org_id = $1 AND id = $2
RETURNING id, first_name, last_name, email, phone, account_id, created_at;

-- name: DeleteContact :execrows
DELETE FROM contacts WHERE org_id = $1 AND id = $2;

-- name: AccountInOrg :one
-- Guards a contact's account_id: the FK alone would accept another tenant's account.
SELECT EXISTS (SELECT 1 FROM accounts WHERE org_id = $1 AND id = $2);
