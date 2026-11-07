-- Видимость обеих заметок проверяют политики links, а не эти запросы.

-- name: CreateLink :execrows
INSERT INTO links (src_id, dst_id, kind)
VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING;

-- name: ListLinks :many
SELECT src_id, dst_id, kind, created_at
FROM links
WHERE src_id = @note_id OR dst_id = @note_id
ORDER BY created_at;

-- name: DependencyLinks :many
SELECT src_id, dst_id FROM links
WHERE kind = 'depends_on' AND src_id = ANY(@ids::uuid[]);
