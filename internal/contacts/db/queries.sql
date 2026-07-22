-- name: GetContact :one
SELECT id, first_name, last_name, email, phone, account_id, created_at
FROM contacts
WHERE id = $1;

-- name: ListContacts :many
SELECT id, first_name, last_name, email, phone, account_id, created_at
FROM contacts
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;
