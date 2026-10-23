-- name: GetSchemaMarker :one
SELECT value FROM app_metadata WHERE key = 'schema_marker';

-- name: GetAppMetadata :one
SELECT value FROM app_metadata WHERE key = $1;

-- name: UpsertAppMetadata :exec
INSERT INTO app_metadata (key, value)
VALUES ($1, $2)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();

-- name: DeleteAppMetadata :exec
DELETE FROM app_metadata WHERE key = $1;
