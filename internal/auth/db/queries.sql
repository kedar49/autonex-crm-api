-- SQL reference for the eventual sqlc migration; the module currently runs the
-- hand-written pgx repository in store.go. Keep the two in sync.

-- name: GetUserByEmail :one
SELECT id, email, org_id, password_hash, auth_provider, provider_user_id, created_at
FROM users
WHERE email = $1;

-- name: GetUserByProvider :one
SELECT id, email, org_id, password_hash, auth_provider, provider_user_id, created_at
FROM users
WHERE auth_provider = $1 AND provider_user_id = $2;

-- name: CreateOrganization :one
INSERT INTO organizations (name)
VALUES ($1)
RETURNING id;

-- name: CreateUser :one
-- Runs in the same transaction as CreateOrganization: users.org_id is NOT NULL
-- and a user without an organization could not reach any CRM data.
INSERT INTO users (email, org_id, password_hash, auth_provider, provider_user_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, email, org_id, password_hash, auth_provider, provider_user_id, created_at;
