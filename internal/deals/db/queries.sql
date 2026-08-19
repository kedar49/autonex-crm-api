-- SQL queries for deals package.
-- Every statement filters on org_id.

-- name: ListDealsBoard :many
SELECT d.*, u.name AS owner_name, u.email AS owner_email,
       NULLIF(concat_ws(' ', c.first_name, c.last_name), '') AS contact_name
FROM deals d
LEFT JOIN users u ON u.id = d.owner_user_id
LEFT JOIN contacts c ON c.id = d.contact_id
WHERE d.org_id = $1
ORDER BY d.stage, d.position, d.id
LIMIT $2;

-- name: GetDeal :one
SELECT d.*, u.name AS owner_name, u.email AS owner_email,
       NULLIF(concat_ws(' ', c.first_name, c.last_name), '') AS contact_name
FROM deals d
LEFT JOIN users u ON u.id = d.owner_user_id
LEFT JOIN contacts c ON c.id = d.contact_id
WHERE d.org_id = $1 AND d.id = $2;

-- name: CreateDeal :one
INSERT INTO deals (org_id, title, description, amount, stage, owner_user_id,
                   contact_id, expected_close_date, position)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8,
        COALESCE((SELECT max(position) + 1000 FROM deals WHERE org_id = $1 AND stage = $5), 0))
RETURNING id;

-- name: UpdateDeal :execrows
UPDATE deals
SET title = $3, description = $4, amount = $5, stage = $6,
    owner_user_id = $7, contact_id = $8, expected_close_date = $9,
    updated_at = now()
WHERE org_id = $1 AND id = $2;

-- name: DeleteDeal :execrows
DELETE FROM deals WHERE org_id = $1 AND id = $2;

-- name: SetDealStage :execrows
UPDATE deals SET stage = $3, updated_at = now() WHERE org_id = $1 AND id = $2;

-- name: DealStageOrder :many
SELECT id FROM deals
WHERE org_id = $1 AND stage = $2 AND id <> $3
ORDER BY position, id;

-- name: ReorderDealStage :exec
UPDATE deals SET position = v.pos
FROM (
  SELECT unnest($2::uuid[]) AS id, unnest($3::float8[]) AS pos
) v
WHERE deals.id = v.id AND deals.org_id = $1;

-- name: RefInOrg :one
SELECT EXISTS (SELECT 1 FROM users WHERE org_id = $1 AND id = $2);

-- name: DealStats :many
SELECT stage, count(*), COALESCE(sum(amount), 0)::float8
FROM deals WHERE org_id = $1 GROUP BY stage;
