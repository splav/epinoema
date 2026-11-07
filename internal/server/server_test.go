package server_test

import (
	"context"
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/splav/epinoema/internal/api"
	"github.com/splav/epinoema/internal/db"
	"github.com/splav/epinoema/internal/testenv"
	"github.com/splav/epinoema/internal/token"
)

func newEnv(t *testing.T) *testenv.Env { return testenv.New(t) }

func urlq(s string) string { return url.QueryEscape(s) }

func TestAuth(t *testing.T) {
	e := newEnv(t)
	_, tok := e.User(t, "alice", "user")

	if code := e.Do(t, "GET", "/v1/notes", "", nil, nil); code != 401 {
		t.Errorf("без токена: %d, want 401", code)
	}
	if code := e.Do(t, "GET", "/v1/notes", "epi_wrong", nil, nil); code != 401 {
		t.Errorf("чужой токен: %d, want 401", code)
	}
	if code := e.Do(t, "GET", "/v1/notes", tok, nil, nil); code != 200 {
		t.Errorf("валидный токен: %d, want 200", code)
	}

	if _, err := e.Pool.Exec(context.Background(),
		"UPDATE api_tokens SET revoked_at = now() WHERE token_hash = $1", token.Hash(tok)); err != nil {
		t.Fatal(err)
	}
	if code := e.Do(t, "GET", "/v1/notes", tok, nil, nil); code != 401 {
		t.Errorf("отозванный токен: %d, want 401", code)
	}
}

func TestAddAndInbox(t *testing.T) {
	e := newEnv(t)
	_, tok := e.User(t, "alice", "user")

	var created api.Note
	code := e.Do(t, "POST", "/v1/notes", tok, api.CreateNote{Body: "проверить идею с pgvector", Tags: []string{"db"}}, &created)
	if code != 201 {
		t.Fatalf("create: %d", code)
	}
	if created.Status != "inbox" || created.Kind != "note" || string(created.Props) != "{}" {
		t.Errorf("defaults: status=%q kind=%q props=%s", created.Status, created.Kind, created.Props)
	}

	var got api.Note
	if code := e.Do(t, "GET", "/v1/notes/"+created.ID, tok, nil, &got); code != 200 || got.Body != created.Body {
		t.Errorf("get: %d %q", code, got.Body)
	}

	var list api.NoteList
	e.Do(t, "GET", "/v1/notes?status=inbox", tok, nil, &list)
	if len(list.Notes) != 1 || list.Notes[0].ID != created.ID {
		t.Errorf("inbox: %+v", list.Notes)
	}

	for name, body := range map[string]api.CreateNote{
		"пустая":          {Body: "  "},
		"неизвестный тип": {Body: "x", Kind: "spaceship"},
	} {
		if code := e.Do(t, "POST", "/v1/notes", tok, body, nil); code != 400 {
			t.Errorf("%s: %d, want 400", name, code)
		}
	}
	if code := e.Do(t, "GET", "/v1/notes/not-a-uuid", tok, nil, nil); code != 404 {
		t.Errorf("кривой id: %d, want 404", code)
	}
}

func TestUsersAreIsolated(t *testing.T) {
	e := newEnv(t)
	_, alice := e.User(t, "alice", "user")
	_, bob := e.User(t, "bob", "user")

	var note api.Note
	e.Do(t, "POST", "/v1/notes", alice, api.CreateNote{Body: "секрет Алисы"}, &note)

	if code := e.Do(t, "GET", "/v1/notes/"+note.ID, bob, nil, nil); code != 404 {
		t.Errorf("Боб читает заметку Алисы: %d, want 404", code)
	}
	var list api.NoteList
	e.Do(t, "GET", "/v1/notes", bob, nil, &list)
	if len(list.Notes) != 0 {
		t.Errorf("Боб видит во входящих: %+v", list.Notes)
	}

	// Без app.user_id запрос не видит ничего, даже под ролью сервиса.
	var n int
	if err := e.Pool.QueryRow(context.Background(), "SELECT count(*) FROM notes").Scan(&n); err != nil || n != 0 {
		t.Errorf("без пользователя видно %d заметок (err %v)", n, err)
	}
}

func TestRevisionRecordsActor(t *testing.T) {
	e := newEnv(t)
	userID, tok := e.User(t, "alice", "user")

	var note api.Note
	e.Do(t, "POST", "/v1/notes", tok, api.CreateNote{Body: "черновик"}, &note)

	ctx := context.Background()
	err := db.InTx(ctx, e.Pool, userID, db.ActorAgent, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "UPDATE notes SET kind = 'idea' WHERE id = $1", note.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	var actor, actorUser, oldKind string
	err = db.InTx(ctx, e.Pool, userID, db.ActorUser, func(tx pgx.Tx) error {
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
