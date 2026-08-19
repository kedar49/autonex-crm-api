-- name: UpsertIntegration :one
INSERT INTO integration_connections (
    org_id, provider, encrypted_tokens, metadata
) VALUES (
    $1, $2, $3, $4
)
ON CONFLICT (org_id, provider) DO UPDATE
SET encrypted_tokens = EXCLUDED.encrypted_tokens,
    metadata = EXCLUDED.metadata,
    updated_at = now()
RETURNING *;

-- name: GetIntegration :one
SELECT * FROM integration_connections
WHERE org_id = $1 AND provider = $2;

-- name: DeleteIntegration :exec
DELETE FROM integration_connections
WHERE org_id = $1 AND provider = $2;

-- name: MapSlackChannel :one
INSERT INTO slack_channels (
    org_id, entity_type, entity_id, channel_id
) VALUES (
    $1, $2, $3, $4
)
RETURNING *;

-- name: GetSlackChannel :one
SELECT * FROM slack_channels
WHERE org_id = $1 AND entity_type = $2 AND entity_id = $3;
