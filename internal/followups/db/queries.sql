-- name: CreateFollowUp :one
INSERT INTO follow_ups (
    org_id, assigned_to, title, due_at, lead_id, deal_id
) VALUES (
    $1, $2, $3, $4, $5, $6
)
RETURNING *;

-- name: ListFollowUps :many
SELECT * FROM follow_ups
WHERE org_id = sqlc.arg('org_id')
  AND (sqlc.narg('assigned_to')::uuid IS NULL OR assigned_to = sqlc.narg('assigned_to'))
  AND (sqlc.narg('completed')::boolean IS NULL OR (completed_at IS NOT NULL) = sqlc.narg('completed'))
ORDER BY due_at ASC;

-- name: MarkFollowUpCompleted :one
UPDATE follow_ups
SET completed_at = now()
WHERE org_id = $1 AND id = $2
RETURNING *;

-- name: CreateCalendarEvent :one
INSERT INTO calendar_events (
    org_id, google_event_id, title, description, start_at, end_at, meet_link, lead_id, deal_id, created_by
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10
)
RETURNING *;

-- name: ListCalendarEvents :many
SELECT * FROM calendar_events
WHERE org_id = $1
  AND start_at >= $2 AND end_at <= $3
ORDER BY start_at ASC;
