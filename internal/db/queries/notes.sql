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

-- Меняются только переданные поля. Пустая строка очищает title и project,
-- importance = 0 очищает важность, tags заменяются целиком.
-- name: UpdateNoteMeta :one
UPDATE notes SET
  title      = CASE WHEN sqlc.narg(title)::text IS NULL THEN title ELSE nullif(sqlc.narg(title)::text, '') END,
  kind       = coalesce(sqlc.narg(kind)::text, kind),
  status     = coalesce(sqlc.narg(status)::text, status),
  importance = CASE WHEN sqlc.narg(importance)::smallint IS NULL THEN importance ELSE nullif(sqlc.narg(importance)::smallint, 0) END,
  project    = CASE WHEN sqlc.narg(project)::text IS NULL THEN project ELSE nullif(sqlc.narg(project)::text, '') END,
  tags       = coalesce(sqlc.narg(tags)::text[], tags)
WHERE id = @id
RETURNING id, title, body, kind, status, importance, project, tags, props, created_at, updated_at;

-- Полнотекстовый поиск по title и body плюс нечёткое совпадение заголовка (pg_trgm),
-- чтобы находились заголовки с опечатками. word_similarity (<%) сравнивает запрос
-- с самым похожим куском заголовка, а не со всем заголовком: иначе длинные
-- заголовки никогда не проходят порог.
-- name: SearchNotes :many
SELECT n.id, n.title, n.body, n.kind, n.status, n.importance, n.project, n.tags, n.props, n.created_at, n.updated_at
FROM notes n, websearch_to_tsquery('epi_ru', @query::text) q
WHERE n.search @@ q OR @query::text <% n.title
ORDER BY greatest(ts_rank(n.search, q), word_similarity(@query::text, coalesce(n.title, ''))) DESC, n.updated_at DESC
LIMIT @lim;
