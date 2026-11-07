-- Владельца отчёта задаёт и проверяет RLS (политика reports_own).

-- name: ListReports :many
SELECT name, spec FROM reports ORDER BY name;

-- name: GetReport :one
SELECT name, spec FROM reports WHERE name = $1;

-- name: UpsertReport :one
INSERT INTO reports (owner_id, name, spec)
VALUES (app_user_id(), @name, @spec)
ON CONFLICT (owner_id, name) DO UPDATE SET spec = EXCLUDED.spec
RETURNING (xmax = 0)::bool AS created;

-- name: DeleteReport :execrows
DELETE FROM reports WHERE name = $1;
