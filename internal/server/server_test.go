package server_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/splav/epinoema/internal/api"
	"github.com/splav/epinoema/internal/db"
	"github.com/splav/epinoema/internal/db/dbq"
	"github.com/splav/epinoema/internal/server"
	"github.com/splav/epinoema/internal/token"
)

// env поднимает чистую базу с миграциями и сервер, подключённый как epinoema_app.
type env struct {
	pool *pgxpool.Pool
	srv  *httptest.Server
}

func newEnv(t *testing.T) *env {
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
	return &env{pool: pool, srv: srv}
}

// user создаёт пользователя и токен, возвращает id пользователя и токен.
func (e *env) user(t *testing.T, handle, actor string) (string, string) {
	t.Helper()
	ctx := context.Background()
	q := dbq.New(e.pool)
	u, err := q.GetUserByHandle(ctx, handle)
	if err == pgx.ErrNoRows {
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

func (e *env) do(t *testing.T, method, path, tok string, body any, out any) int {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, r)
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

func TestAuth(t *testing.T) {
	e := newEnv(t)
	_, tok := e.user(t, "alice", "user")

	if code := e.do(t, "GET", "/v1/notes", "", nil, nil); code != 401 {
		t.Errorf("без токена: %d, want 401", code)
	}
	if code := e.do(t, "GET", "/v1/notes", "epi_wrong", nil, nil); code != 401 {
		t.Errorf("чужой токен: %d, want 401", code)
	}
	if code := e.do(t, "GET", "/v1/notes", tok, nil, nil); code != 200 {
		t.Errorf("валидный токен: %d, want 200", code)
	}

	if _, err := e.pool.Exec(context.Background(),
		"UPDATE api_tokens SET revoked_at = now() WHERE token_hash = $1", token.Hash(tok)); err != nil {
		t.Fatal(err)
	}
	if code := e.do(t, "GET", "/v1/notes", tok, nil, nil); code != 401 {
		t.Errorf("отозванный токен: %d, want 401", code)
	}
}

func TestAddAndInbox(t *testing.T) {
	e := newEnv(t)
	_, tok := e.user(t, "alice", "user")

	var created api.Note
	code := e.do(t, "POST", "/v1/notes", tok, api.CreateNote{Body: "проверить идею с pgvector", Tags: []string{"db"}}, &created)
	if code != 201 {
		t.Fatalf("create: %d", code)
	}
	if created.Status != "inbox" || created.Kind != "note" || string(created.Props) != "{}" {
		t.Errorf("defaults: status=%q kind=%q props=%s", created.Status, created.Kind, created.Props)
	}

	var got api.Note
	if code := e.do(t, "GET", "/v1/notes/"+created.ID, tok, nil, &got); code != 200 || got.Body != created.Body {
		t.Errorf("get: %d %q", code, got.Body)
	}

	var list api.NoteList
	e.do(t, "GET", "/v1/notes?status=inbox", tok, nil, &list)
	if len(list.Notes) != 1 || list.Notes[0].ID != created.ID {
		t.Errorf("inbox: %+v", list.Notes)
	}

	for name, body := range map[string]api.CreateNote{
		"пустая":          {Body: "  "},
		"неизвестный тип": {Body: "x", Kind: "spaceship"},
	} {
		if code := e.do(t, "POST", "/v1/notes", tok, body, nil); code != 400 {
			t.Errorf("%s: %d, want 400", name, code)
		}
	}
	if code := e.do(t, "GET", "/v1/notes/not-a-uuid", tok, nil, nil); code != 404 {
		t.Errorf("кривой id: %d, want 404", code)
	}
}

func TestUsersAreIsolated(t *testing.T) {
	e := newEnv(t)
	_, alice := e.user(t, "alice", "user")
	_, bob := e.user(t, "bob", "user")

	var note api.Note
	e.do(t, "POST", "/v1/notes", alice, api.CreateNote{Body: "секрет Алисы"}, &note)

	if code := e.do(t, "GET", "/v1/notes/"+note.ID, bob, nil, nil); code != 404 {
		t.Errorf("Боб читает заметку Алисы: %d, want 404", code)
	}
	var list api.NoteList
	e.do(t, "GET", "/v1/notes", bob, nil, &list)
	if len(list.Notes) != 0 {
		t.Errorf("Боб видит во входящих: %+v", list.Notes)
	}

	// Без app.user_id запрос не видит ничего, даже под ролью сервиса.
	var n int
	if err := e.pool.QueryRow(context.Background(), "SELECT count(*) FROM notes").Scan(&n); err != nil || n != 0 {
		t.Errorf("без пользователя видно %d заметок (err %v)", n, err)
	}
}

func TestRevisionRecordsActor(t *testing.T) {
	e := newEnv(t)
	userID, tok := e.user(t, "alice", "user")

	var note api.Note
	e.do(t, "POST", "/v1/notes", tok, api.CreateNote{Body: "черновик"}, &note)

	ctx := context.Background()
	err := db.InTx(ctx, e.pool, userID, db.ActorAgent, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "UPDATE notes SET kind = 'idea' WHERE id = $1", note.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	var actor, actorUser, oldKind string
	err = db.InTx(ctx, e.pool, userID, db.ActorUser, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			"SELECT actor, actor_user_id::text, snapshot->>'kind' FROM revisions WHERE note_id = $1", note.ID,
		).Scan(&actor, &actorUser, &oldKind)
	})
	if err != nil {
		t.Fatal(err)
	}
	if actor != "agent" || actorUser != userID || oldKind != "note" {
		t.Errorf("revision: actor=%q user=%q old kind=%q", actor, actorUser, oldKind)
	}
}
