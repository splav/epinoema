-- Владелец и видимость проверяются RLS, а не условиями в запросах.
-- Колонки перечислены явно, чтобы не тащить search; набор одинаков во всех запросах,
-- поэтому строки конвертируются в api.Note.

-- name: CreateNote :one
INSERT INTO notes (owner_id, title, body, kind, importance, project, tags)
VALUES (app_user_id(), sqlc.narg(title), @body, @kind, sqlc.narg(importance), sqlc.narg(project), @tags)
RETURNING id, title, body, kind, status, importance, project, tags, props, created_at, updated_at;

-- name: GetNote :one
SELECT id, title, body, kind, status, importance, project, tags, props, created_at, updated_at
FROM notes
WHERE id = $1;

-- name: ListNotesByStatus :many
SELECT id, title, body, kind, status, importance, project, tags, props, created_at, updated_at
FROM notes
WHERE status = $1
ORDER BY created_at DESC
LIMIT $2;
