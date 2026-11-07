package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/splav/epinoema/internal/db"
	"github.com/splav/epinoema/internal/db/dbq"
	"github.com/splav/epinoema/internal/server"
	"github.com/splav/epinoema/internal/token"
)

func runMigrate(args []string) error {
	flag.NewFlagSet("migrate", flag.ExitOnError).Parse(args)
	dsn, err := mustEnv("EPI_ADMIN_DB")
	if err != nil {
		return err
	}
	results, err := db.Migrate(context.Background(), dsn)
	for _, r := range results {
		fmt.Println("applied", r.Source.Path)
	}
	if err == nil && len(results) == 0 {
		fmt.Println("схема актуальна")
	}
	return err
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:7777", "адрес HTTP")
	fs.Parse(args)
	dsn, err := mustEnv("EPI_DB")
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	// Владелец таблиц обходит RLS, а суперпользователь — вообще всё.
	var owner, super bool
	if err := pool.QueryRow(ctx,
		`SELECT pg_has_role(current_user, (SELECT tableowner FROM pg_tables WHERE tablename = 'notes' AND schemaname = 'public'), 'USAGE'),
		        (SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname = current_user)`,
	).Scan(&owner, &super); err != nil {
		return fmt.Errorf("проверка роли: %w (миграции применены?)", err)
	}
	if owner || super {
		return errors.New("EPI_DB подключается под ролью, которая обходит RLS; нужна epinoema_app")
	}
	// Миграции 00003 и 00004, применённые не суперпользователем, проходят
	// с предупреждением и ничего не меняют. Без них агент через query может
	// выдать себя за другого пользователя или оставить блокировки на соединении.
	var leaked []string
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(array_agg(p.oid::regprocedure::text), '{}') FROM pg_proc p
		WHERE p.pronamespace = 'pg_catalog'::regnamespace
		  AND (p.proname IN ('set_config', 'pg_logical_emit_message')
		       OR p.proname LIKE 'pg\_advisory\_%' OR p.proname LIKE 'pg\_try\_advisory\_%')
		  AND has_function_privilege('epinoema_agent_ro', p.oid, 'EXECUTE')`,
	).Scan(&leaked); err != nil {
		return fmt.Errorf("проверка прав агента: %w", err)
	}
	if len(leaked) > 0 {
		return fmt.Errorf("агенту доступны %v: примените миграции суперпользователем (epi migrate)", leaked)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	srv := &http.Server{
		Addr:              *addr,
		Handler:           server.New(pool, log).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()
	log.Info("listening", "addr", *addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func runToken(args []string) error {
	if len(args) == 0 || args[0] != "create" {
		return errors.New("использование: epi token create -user HANDLE [-actor user|agent] [-name cli]")
	}
	fs := flag.NewFlagSet("token create", flag.ExitOnError)
	handle := fs.String("user", "", "handle пользователя; создаётся, если его нет")
	actor := fs.String("actor", "user", "user или agent")
	name := fs.String("name", "cli", "имя клиента")
	fs.Parse(args[1:])
	if *handle == "" {
		return errors.New("нужен -user")
	}
	dsn, err := mustEnv("EPI_DB")
	if err != nil {
		return err
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	var plain string
	err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		q := dbq.New(tx)
		u, err := q.GetUserByHandle(ctx, *handle)
		if errors.Is(err, pgx.ErrNoRows) {
			u, err = q.CreateUser(ctx, *handle)
			if err == nil {
				fmt.Fprintln(os.Stderr, "создан пользователь", *handle)
			}
		}
		if err != nil {
			return err
		}
		var hash []byte
		plain, hash = token.New()
		_, err = q.CreateToken(ctx, dbq.CreateTokenParams{UserID: u.ID, Actor: *actor, Name: *name, TokenHash: hash})
		return err
	})
	if err != nil {
		return err
	}
	// Токен показывается один раз: в базе остаётся только хеш.
	fmt.Println(plain)
	return nil
}
