-- SQL queries for leads package.
-- Every statement filters on org_id to maintain tenant isolation.

-- name: ListLeadsBoard :many
SELECT l.*, u.name AS owner_name, u.email AS owner_email
FROM leads l
LEFT JOIN users u ON u.id = l.owner_user_id
WHERE l.org_id = $1
ORDER BY l.stage, l.created_at DESC, l.id
LIMIT $2;

-- name: GetLead :one
SELECT l.*, u.name AS owner_name, u.email AS owner_email
FROM leads l
LEFT JOIN users u ON u.id = l.owner_user_id
WHERE l.org_id = $1 AND l.id = $2;

-- name: CreateLead :one
INSERT INTO leads (
    org_id, first_name, last_name, email, phone, company, title, linkedin_url,
    source, notes, value, stage, owner_user_id, contact_id, account_id, follow_up_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16
)
RETURNING id;

-- name: UpdateLead :execrows
UPDATE leads
SET first_name = $3, last_name = $4, email = $5, phone = $6, company = $7,
    title = $8, linkedin_url = $9, source = $10, notes = $11, value = $12,
    stage = $13, owner_user_id = $14, contact_id = $15, account_id = $16,
    follow_up_at = $17, updated_at = now()
WHERE org_id = $1 AND id = $2;

-- name: DeleteLead :execrows
DELETE FROM leads WHERE org_id = $1 AND id = $2;

-- name: SetLeadStage :execrows
UPDATE leads SET stage = $3, updated_at = now() WHERE org_id = $1 AND id = $2;

-- name: OwnerInOrg :one
SELECT EXISTS (SELECT 1 FROM users WHERE org_id = $1 AND id = $2);

-- name: LeadStats :many
SELECT stage, count(*), COALESCE(sum(value), 0)::float8
FROM leads WHERE org_id = $1 GROUP BY stage;

-- name: ClaimLeadForConversion :one
UPDATE leads
SET stage = 'converted', converted_at = now(), updated_at = now()
WHERE org_id = $1 AND id = $2 AND converted_at IS NULL
RETURNING first_name, last_name, email, phone, company, value, owner_user_id;

-- name: LinkConversion :exec
UPDATE leads SET converted_deal_id = $3, converted_contact_id = $4
WHERE org_id = $1 AND id = $2;
