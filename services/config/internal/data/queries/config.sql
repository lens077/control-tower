-- name: ListNamespaceEnvironments :many
-- 走 idx_entry_ns_env 索引扫描,返回 (namespace, environment) 维度的 key 数量。
SELECT namespace, environment, COUNT(*)::int AS key_count
FROM config.entry
GROUP BY namespace, environment
ORDER BY namespace, environment;

-- name: ListEntries :many
SELECT id, namespace, environment, key, format, version, is_secret, description, updated_by, created_at, updated_at
FROM config.entry
WHERE namespace = @namespace
  AND environment = @environment
  AND key LIKE @key_prefix || '%'
ORDER BY key;

-- name: GetEntry :one
SELECT *
FROM config.entry
WHERE namespace = @namespace
  AND environment = @environment
  AND key = @key;

-- name: GetEntryForUpdate :one
SELECT *
FROM config.entry
WHERE namespace = @namespace
  AND environment = @environment
  AND key = @key
FOR UPDATE;

-- name: InsertEntry :one
INSERT INTO config.entry (namespace, environment, key, format, value, version, is_secret, description, updated_by)
VALUES (@namespace, @environment, @key, @format, @value, 1, @is_secret, @description, @updated_by)
RETURNING *;

-- name: UpdateEntry :one
UPDATE config.entry
SET value       = @value,
    format      = @format,
    version     = @version,
    is_secret   = @is_secret,
    description = @description,
    updated_by  = @updated_by,
    updated_at  = now()
WHERE id = @id
RETURNING *;

-- name: DeleteEntry :execrows
DELETE FROM config.entry
WHERE namespace = @namespace
  AND environment = @environment
  AND key = @key;

-- name: InsertRevision :one
INSERT INTO config.revision (entry_id, version, format, value, comment, author)
VALUES (@entry_id, @version, @format, @value, @comment, @author)
RETURNING *;

-- name: ListRevisions :many
SELECT *
FROM config.revision
WHERE entry_id = @entry_id
ORDER BY version DESC;

-- name: GetRevisionByVersion :one
SELECT *
FROM config.revision
WHERE entry_id = @entry_id
  AND version = @version;
