-- SQL reference for the eventual sqlc migration; the module currently runs the
-- hand-written pgx repository in store.go / convert.go. Keep the two in sync.
--
-- Every statement filters on org_id. A query here without it is a tenant-data
-- leak, not a missing filter.

-- name: ListLeadsBoard :many
-- The whole pipeline, ordered so the client can slice it into columns directly.
SELECT l.*, u.name AS owner_name, u.email AS owner_email
FROM leads l
LEFT JOIN users u ON u.id = l.owner_user_id
WHERE l.org_id = $1
ORDER BY l.stage, l.position, l.id
LIMIT $2;

-- name: GetLead :one
SELECT l.*, u.name AS owner_name, u.email AS owner_email
FROM leads l
LEFT JOIN users u ON u.id = l.owner_user_id
WHERE l.org_id = $1 AND l.id = $2;

-- name: CreateLead :one
-- Appends to the end of its column.
INSERT INTO leads (org_id, first_name, last_name, email, phone, company, source,
                   notes, value, stage, owner_user_id, position)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11,
        COALESCE((SELECT max(position) + 1000 FROM leads WHERE org_id = $1 AND stage = $10), 0))
RETURNING id;

-- name: UpdateLead :execrows
UPDATE leads
SET first_name = $3, last_name = $4, email = $5, phone = $6, company = $7,
    source = $8, notes = $9, value = $10, stage = $11, owner_user_id = $12,
    updated_at = now()
WHERE org_id = $1 AND id = $2;

-- name: DeleteLead :execrows
DELETE FROM leads WHERE org_id = $1 AND id = $2;

-- name: SetLeadStage :execrows
-- First half of a move; the column is renumbered by ReorderStage below.
UPDATE leads SET stage = $3, updated_at = now() WHERE org_id = $1 AND id = $2;

-- name: StageOrder :many
SELECT id FROM leads
WHERE org_id = $1 AND stage = $2 AND id <> $3
ORDER BY position, id;

-- name: ReorderStage :exec
-- Whole-column rewrite: keeps positions exact rather than drifting the way
-- fractional indexing between neighbours does.
UPDATE leads SET position = v.pos
FROM unnest($2::uuid[], $3::float8[]) AS v(id, pos)
WHERE leads.id = v.id AND leads.org_id = $1;

-- name: OwnerInOrg :one
-- Guards owner_user_id: the FK alone would accept another tenant's user.
SELECT EXISTS (SELECT 1 FROM users WHERE org_id = $1 AND id = $2);

-- name: LeadStats :many
SELECT stage, count(*), COALESCE(sum(value), 0)::float8
FROM leads WHERE org_id = $1 GROUP BY stage;

-- name: ClaimLeadForConversion :one
-- `converted_at IS NULL` is the idempotency guard: two concurrent conversions
-- can't both go on to create a deal, because the loser updates zero rows.
UPDATE leads
SET stage = 'won', converted_at = now(), updated_at = now()
WHERE org_id = $1 AND id = $2 AND converted_at IS NULL
RETURNING first_name, last_name, email, phone, company, value, owner_user_id;

-- name: LinkConversion :exec
UPDATE leads SET converted_deal_id = $3, converted_contact_id = $4
WHERE org_id = $1 AND id = $2;
