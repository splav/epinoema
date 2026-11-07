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
