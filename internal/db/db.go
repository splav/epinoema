// Package db открывает соединения с Postgres, применяет встроенные миграции
// и выполняет запросы от имени конкретного пользователя.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // драйвер pgx для database/sql, нужен goose
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrate применяет все миграции. dsn должен указывать на владельца таблиц,
// а не на epinoema_app: миграции создают таблицы, роли и политики.
func Migrate(ctx context.Context, dsn string) ([]*goose.MigrationResult, error) {
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	defer sqlDB.Close()

	dir, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, err
	}
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, dir)
	if err != nil {
		return nil, err
	}
	return p.Up(ctx)
}

// Open создаёт пул и проверяет соединение.
//
// Словарь hunspell грузится в память каждого соединения: около 25 МБ и 200 мс
// на первый поисковый запрос. Поэтому пул по умолчанию маленький (меняется
// параметром pool_max_conns в DSN), а словарь грузится сразу при подключении,
// а не на первом запросе пользователя.
func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(dsn, "pool_max_conns") {
		cfg.MaxConns = 4
	}
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "SELECT to_tsvector('epi_ru', 'прогрев')")
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// Actor — кто делает правку; попадает в revisions.actor.
type Actor string

const (
	ActorUser  Actor = "user"
	ActorAgent Actor = "agent"
)

// InTx выполняет fn в транзакции от имени userID. RLS видит пользователя через
// app.user_id, триггер истории — автора через app.actor. Обе настройки локальны
// для транзакции, поэтому не утекают в другие запросы через пул.
func InTx(ctx context.Context, pool *pgxpool.Pool, userID string, actor Actor, fn func(pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`SELECT set_config('app.user_id', $1, true), set_config('app.actor', $2, true)`,
			userID, string(actor),
		); err != nil {
			return fmt.Errorf("set request context: %w", err)
		}
		return fn(tx)
	})
}
