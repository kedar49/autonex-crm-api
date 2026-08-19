-- name: CreateAuditLog :one
INSERT INTO audit_logs (
    org_id, user_id, action, entity_type, entity_id, changes, ip_address
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
RETURNING *;

-- name: ListAuditLogs :many
SELECT a.*, u.name AS user_name, u.email AS user_email
FROM audit_logs a
LEFT JOIN users u ON u.id = a.user_id
WHERE a.org_id = sqlc.arg('org_id')
  AND (sqlc.narg('entity_type')::text IS NULL OR a.entity_type = sqlc.narg('entity_type'))
  AND (sqlc.narg('entity_id')::uuid IS NULL OR a.entity_id = sqlc.narg('entity_id'))
ORDER BY a.created_at DESC
LIMIT sqlc.arg('limit_val') OFFSET sqlc.arg('offset_val');
