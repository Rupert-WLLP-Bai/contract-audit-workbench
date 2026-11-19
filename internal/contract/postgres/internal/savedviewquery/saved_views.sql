-- name: ListSavedViews :many
SELECT view_id, name, filter, columns, row_version, created_at, updated_at
FROM clm_contract_saved_views
WHERE tenant_id = $1 AND owner_user_id = $2
ORDER BY updated_at DESC, view_id DESC;
