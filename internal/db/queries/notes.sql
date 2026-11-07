-- Владелец и видимость проверяются RLS, а не условиями в запросах.
-- Колонки перечислены явно, чтобы не тащить search; набор одинаков во всех запросах,
-- поэтому строки конвертируются в api.Note.

-- name: CreateNote :one
INSERT INTO notes (owner_id, title, body, kind, importance, project, tags, start_on, due_on)
VALUES (app_user_id(), sqlc.narg(title), @body, @kind, sqlc.narg(importance), sqlc.narg(project), @tags,
        sqlc.narg(start_on)::text::date, sqlc.narg(due_on)::text::date)
RETURNING id, title, body, kind, status, importance, project, tags, props, start_on, due_on, created_at, updated_at;

-- name: GetNote :one
SELECT id, title, body, kind, status, importance, project, tags, props, start_on, due_on, created_at, updated_at
FROM notes
WHERE id = $1;

-- name: ListNotesByStatus :many
SELECT id, title, body, kind, status, importance, project, tags, props, start_on, due_on, created_at, updated_at
FROM notes
WHERE status = $1
ORDER BY created_at DESC
LIMIT $2;

-- Меняются только переданные поля. Пустая строка очищает title и project,
-- importance = 0 очищает важность, tags заменяются целиком; пустая строка
-- очищает и даты.
-- name: UpdateNoteMeta :one
UPDATE notes SET
  title      = CASE WHEN sqlc.narg(title)::text IS NULL THEN title ELSE nullif(sqlc.narg(title)::text, '') END,
  kind       = coalesce(sqlc.narg(kind)::text, kind),
  status     = coalesce(sqlc.narg(status)::text, status),
  importance = CASE WHEN sqlc.narg(importance)::smallint IS NULL THEN importance ELSE nullif(sqlc.narg(importance)::smallint, 0) END,
  project    = CASE WHEN sqlc.narg(project)::text IS NULL THEN project ELSE nullif(sqlc.narg(project)::text, '') END,
  tags       = coalesce(sqlc.narg(tags)::text[], tags),
  start_on   = CASE WHEN sqlc.narg(start_on)::text IS NULL THEN start_on ELSE nullif(sqlc.narg(start_on)::text, '')::date END,
  due_on     = CASE WHEN sqlc.narg(due_on)::text IS NULL THEN due_on ELSE nullif(sqlc.narg(due_on)::text, '')::date END
WHERE id = @id
RETURNING id, title, body, kind, status, importance, project, tags, props, start_on, due_on, created_at, updated_at;

-- Полнотекстовый поиск по title и body плюс нечёткое совпадение заголовка (pg_trgm),
-- чтобы находились заголовки с опечатками. word_similarity (<%) сравнивает запрос
-- с самым похожим куском заголовка, а не со всем заголовком: иначе длинные
-- заголовки никогда не проходят порог.
-- name: SearchNotes :many
SELECT n.id, n.title, n.body, n.kind, n.status, n.importance, n.project, n.tags, n.props, n.start_on, n.due_on, n.created_at, n.updated_at
FROM notes n, websearch_to_tsquery('epi_ru', @query::text) q
WHERE n.search @@ q OR @query::text <% n.title
ORDER BY greatest(ts_rank(n.search, q), word_similarity(@query::text, coalesce(n.title, ''))) DESC, n.updated_at DESC
LIMIT @lim;

-- Фильтр сохранённых отчётов. NULL в параметре — условие не применяется.
-- name: FilterNotes :many
SELECT id, title, body, kind, status, importance, project, tags, props, start_on, due_on, created_at, updated_at
FROM notes
WHERE (sqlc.narg(kinds)::text[] IS NULL OR kind = ANY(sqlc.narg(kinds)::text[]))
  AND (sqlc.narg(statuses)::text[] IS NULL OR status = ANY(sqlc.narg(statuses)::text[]))
  AND (sqlc.narg(project)::text IS NULL OR project = sqlc.narg(project)::text)
  AND (sqlc.narg(tags_any)::text[] IS NULL OR tags && sqlc.narg(tags_any)::text[])
  AND (sqlc.narg(importance_gte)::smallint IS NULL OR importance >= sqlc.narg(importance_gte)::smallint)
  AND (sqlc.narg(stale_days)::int IS NULL OR updated_at < now() - make_interval(days => sqlc.narg(stale_days)::int))
  AND (sqlc.narg(changed_days)::int IS NULL OR updated_at >= now() - make_interval(days => sqlc.narg(changed_days)::int))
ORDER BY
  CASE WHEN @order_by::text = 'importance' THEN importance END DESC NULLS LAST,
  CASE WHEN @order_by::text = 'created' THEN created_at END DESC,
  updated_at DESC
LIMIT @lim;

-- Кандидаты на диаграмму Ганта: заметки проекта с датами или с зависимостями.
-- name: GanttNotes :many
SELECT n.id, n.title, n.body, n.kind, n.status, n.importance, n.project, n.tags, n.props, n.start_on, n.due_on, n.created_at, n.updated_at
FROM notes n
WHERE n.status <> 'dropped'
  AND (sqlc.narg(project)::text IS NULL OR n.project = sqlc.narg(project)::text)
  AND (sqlc.narg(tags_any)::text[] IS NULL OR n.tags && sqlc.narg(tags_any)::text[])
  AND (n.start_on IS NOT NULL OR n.due_on IS NOT NULL
       OR EXISTS (SELECT 1 FROM links l WHERE l.kind = 'depends_on' AND (l.src_id = n.id OR l.dst_id = n.id)))
ORDER BY coalesce(n.start_on, n.due_on), n.created_at
LIMIT 500;

-- Короткие id из вывода CLI. Две строки — чтобы отличить однозначный префикс.
-- name: ResolveNotePrefix :many
SELECT id FROM notes WHERE id::text LIKE @prefix::text || '%' ORDER BY id LIMIT 2;
