package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// QueryTimeout ограничивает произвольный запрос агента. Переменная, а не
// константа, чтобы тесты не ждали секундами.
var QueryTimeout = 5 * time.Second

// Предел ответа: агенту нужен отчёт, а не выгрузка базы.
const maxQueryBytes = 1 << 20

// QueryResult — строки как JSON-объекты в порядке колонок запроса.
type QueryResult struct {
	Rows      []json.RawMessage
	Truncated bool
}

// ReadOnlyQuery выполняет произвольный SELECT от имени пользователя под ролью
// epinoema_agent_ro: только чтение, только содержимое, строки режет RLS.
//
// Защита в несколько слоёв:
//   - запрос вставляется в FROM (...) — там допустим только SELECT, поэтому SET,
//     DO, RESET ROLE и несколько команд подряд не проходят разбор;
//   - расширенный протокол не принимает больше одной команды;
//   - транзакция read-only и всегда откатывается, так что не меняет ни данные,
//     ни настройки соединения из пула;
//   - set_config роли агента не доступен (миграция 00003), поэтому подменить
//     app.user_id или роль внутри SELECT нельзя;
//   - advisory-блокировки и нетранзакционная запись в WAL тоже закрыты (00004):
//     их последствия откат не отменяет.
func ReadOnlyQuery(ctx context.Context, pool *pgxpool.Pool, userID string, actor Actor, sql string, limit int) (QueryResult, error) {
	sql = strings.TrimRight(strings.TrimSpace(sql), "; \t\n")
	if sql == "" {
		return QueryResult{}, fmt.Errorf("empty query")
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return QueryResult{}, err
	}
	defer conn.Release()
	// Страховка поверх миграции 00004: блокировка уровня сессии пережила бы
	// откат и осталась на соединении, которое вернётся в пул.
	defer conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock_all()")

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return QueryResult{}, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))

	if _, err := tx.Exec(ctx,
		`SELECT set_config('app.user_id', $1, true), set_config('app.actor', $2, true),
		        set_config('statement_timeout', $3, true)`,
		userID, string(actor), fmt.Sprint(QueryTimeout.Milliseconds()),
	); err != nil {
		return QueryResult{}, fmt.Errorf("set request context: %w", err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE epinoema_agent_ro"); err != nil {
		return QueryResult{}, fmt.Errorf("switch to agent role: %w", err)
	}

	// Перевод строки перед ")" — чтобы комментарий "--" в конце запроса
	// не закомментировал обёртку. Строку больше предела Postgres заменяет
	// на NULL, чтобы она не приехала в память сервиса целиком.
	wrapped := `SELECT CASE WHEN octet_length(j) > $2 THEN NULL ELSE j END
		FROM (SELECT row_to_json(q)::text AS j FROM (
` + sql + `
) AS q LIMIT $1) AS s`
	rows, err := tx.Query(ctx, wrapped, pgx.QueryExecModeExec, limit+1, maxQueryBytes)
	if err != nil {
		return QueryResult{}, err
	}
	defer rows.Close()

	res := QueryResult{Rows: []json.RawMessage{}}
	size := 0
	for rows.Next() {
		var row *string
		if err := rows.Scan(&row); err != nil {
			return QueryResult{}, err
		}
		if row == nil || len(res.Rows) == limit || size+len(*row) > maxQueryBytes {
			res.Truncated = true
			break
		}
		size += len(*row)
		res.Rows = append(res.Rows, json.RawMessage(*row))
	}
	return res, rows.Err()
}
