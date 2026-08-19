-- name: CreateProduct :one
INSERT INTO products (
    org_id, sku, name, description, unit_price, tax_rate, category, is_active
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING *;

-- name: GetProductByID :one
SELECT * FROM products
WHERE org_id = $1 AND id = $2;

-- name: GetProductBySKU :one
SELECT * FROM products
WHERE org_id = $1 AND lower(sku) = lower($2);

-- name: ListProducts :many
SELECT * FROM products
WHERE org_id = sqlc.arg('org_id')
  AND (sqlc.narg('category')::text IS NULL OR category = sqlc.narg('category'))
  AND (sqlc.narg('is_active')::boolean IS NULL OR is_active = sqlc.narg('is_active'))
ORDER BY created_at DESC;

-- name: UpdateProduct :one
UPDATE products
SET sku = COALESCE(sqlc.narg('sku'), sku),
    name = COALESCE(sqlc.narg('name'), name),
    description = COALESCE(sqlc.narg('description'), description),
    unit_price = COALESCE(sqlc.narg('unit_price'), unit_price),
    tax_rate = COALESCE(sqlc.narg('tax_rate'), tax_rate),
    category = COALESCE(sqlc.narg('category'), category),
    is_active = COALESCE(sqlc.narg('is_active'), is_active),
    updated_at = now()
WHERE org_id = sqlc.arg('org_id') AND id = sqlc.arg('id')
RETURNING *;

-- name: DeleteProduct :exec
DELETE FROM products
WHERE org_id = $1 AND id = $2;
