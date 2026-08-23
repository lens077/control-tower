-- name: InsertMachineToken :one
INSERT INTO config.machine_token (id, service_name, environment, token_hash, allowed_namespaces, note)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListMachineTokens :many
SELECT * FROM config.machine_token
WHERE (sqlc.narg('service_name')::text IS NULL OR service_name = sqlc.narg('service_name'))
  AND (sqlc.narg('environment')::text IS NULL OR environment = sqlc.narg('environment'))
ORDER BY service_name, environment, created_at;

-- name: GetActiveMachineTokenByHash :one
SELECT * FROM config.machine_token
WHERE token_hash = $1 AND NOT disabled;

-- name: IsMachineTokenActive :one
SELECT EXISTS (
  SELECT 1 FROM config.machine_token WHERE id = $1 AND NOT disabled
) AS active;

-- name: RevokeMachineToken :one
UPDATE config.machine_token
SET disabled = TRUE, revoked_at = now()
WHERE id = $1 AND NOT disabled
RETURNING *;

-- name: TouchMachineTokenLastUsed :exec
UPDATE config.machine_token SET last_used_at = now() WHERE id = $1;
