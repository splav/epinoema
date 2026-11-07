// Package testenv поднимает для тестов чистую базу с миграциями и HTTP API,
// подключённый как epinoema_app. Нужна переменная EPI_TEST_ADMIN_DB.
package testenv

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/splav/epinoema/internal/db"
	"github.com/splav/epinoema/internal/db/dbq"
	"github.com/splav/epinoema/internal/server"
	"github.com/splav/epinoema/internal/token"
)

type Env struct {
	Pool *pgxpool.Pool
	URL  string
}

func New(t *testing.T) *Env {
	t.Helper()
	adminDSN := os.Getenv("EPI_TEST_ADMIN_DB")
	if adminDSN == "" {
		t.Skip("EPI_TEST_ADMIN_DB не задана (например postgres://localhost/postgres)")
	}
	ctx := context.Background()

	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 4)
	rand.Read(b)
	name := "epinoema_test_" + hex.EncodeToString(b)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(context.Background())
	})

	u, _ := url.Parse(adminDSN)
	u.Path = "/" + name
	if _, err := db.Migrate(ctx, u.String()); err != nil {
		t.Fatal(err)
	}

	// Роли без LOGIN, поэтому подключаемся админом и переключаемся в epinoema_app:
	// RLS проверяет current_user, так что поведение то же, что у сервиса.
	cfg, err := pgxpool.ParseConfig(u.String())
	if err != nil {
		t.Fatal(err)
	}
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "SET ROLE epinoema_app")
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	srv := httptest.NewServer(server.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	t.Cleanup(srv.Close)
	return &Env{Pool: pool, URL: srv.URL}
}

// User создаёт пользователя (если его нет) и токен; возвращает id пользователя и токен.
func (e *Env) User(t *testing.T, handle, actor string) (string, string) {
	t.Helper()
	ctx := context.Background()
	q := dbq.New(e.Pool)
	u, err := q.GetUserByHandle(ctx, handle)
	if errors.Is(err, pgx.ErrNoRows) {
		u, err = q.CreateUser(ctx, handle)
	}
	if err != nil {
		t.Fatal(err)
	}
	plain, hash := token.New()
	if _, err := q.CreateToken(ctx, dbq.CreateTokenParams{UserID: u.ID, Actor: actor, Name: "test", TokenHash: hash}); err != nil {
		t.Fatal(err)
	}
	return u.ID, plain
}

// Do отправляет запрос к API и декодирует успешный ответ в out; возвращает код ответа.
func (e *Env) Do(t *testing.T, method, path, tok string, body any, out any) int {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.URL+path, r)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode < 300 {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode
}
