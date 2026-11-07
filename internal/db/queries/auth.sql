-- name: GetActiveToken :one
-- Вызывается до того, как известен пользователь, поэтому api_tokens и users без RLS.
SELECT t.id, t.user_id, t.actor, t.name, u.handle
FROM api_tokens t
JOIN users u ON u.id = t.user_id
WHERE t.token_hash = $1 AND t.revoked_at IS NULL;

-- name: GetUserByHandle :one
SELECT * FROM users WHERE handle = $1;

-- name: CreateUser :one
INSERT INTO users (handle) VALUES ($1) RETURNING *;

-- name: CreateToken :one
INSERT INTO api_tokens (user_id, actor, name, token_hash)
VALUES ($1, $2, $3, $4)
RETURNING id, user_id, actor, name, created_at;
