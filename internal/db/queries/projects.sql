-- Проекты — значения notes.project. Только свои заметки: расшаренные чужие
-- видны через RLS, но проект в них — решение их владельца.

-- name: ListProjects :many
SELECT project::text AS name, count(*) AS notes, max(updated_at)::timestamptz AS last_updated
FROM notes
WHERE project IS NOT NULL AND owner_id = app_user_id()
GROUP BY project
ORDER BY project;

-- name: ProjectExists :one
SELECT EXISTS (SELECT 1 FROM notes WHERE project = @name::text AND owner_id = app_user_id());

-- Каждая заметка получает свою ревизию через триггер, так что переименование
-- откатывается по истории, как любая правка.
-- name: RenameProject :execrows
UPDATE notes SET project = @new_name::text
WHERE project = @old_name::text AND owner_id = app_user_id();
