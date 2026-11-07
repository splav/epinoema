package server_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/splav/epinoema/internal/api"
	"github.com/splav/epinoema/internal/db"
)

func TestQuery(t *testing.T) {
	e := newEnv(t)
	_, alice := e.User(t, "alice", "agent")
	e.Do(t, "POST", "/v1/notes", alice, api.CreateNote{Body: "заметка Алисы", Tags: []string{"x"}}, nil)

	var res api.QueryResult
	code := e.Do(t, "POST", "/v1/query", alice, api.Query{SQL: "select id, body, tags, created_at from notes;"}, &res)
	if code != 200 || len(res.Rows) != 1 || res.Truncated {
		t.Fatalf("query: %d %+v", code, res)
	}
	var row map[string]any
	json.Unmarshal(res.Rows[0], &row)
	if row["body"] != "заметка Алисы" || row["tags"].([]any)[0] != "x" {
		t.Errorf("row: %s", res.Rows[0])
	}

	e.Do(t, "POST", "/v1/query", alice, api.Query{SQL: "select g from generate_series(1, 1000) g -- комментарий", Limit: 10}, &res)
	if len(res.Rows) != 10 || !res.Truncated {
		t.Errorf("limit: %d rows, truncated=%v", len(res.Rows), res.Truncated)
	}
}

// Каждая атака должна закончиться ошибкой, а чужая заметка — не попасть в ответ.
func TestQueryCannotEscape(t *testing.T) {
	e := newEnv(t)
	_, alice := e.User(t, "alice", "agent")
	bobID, bob := e.User(t, "bob", "user")
	e.Do(t, "POST", "/v1/notes", alice, api.CreateNote{Body: "заметка Алисы"}, nil)
	var secret api.Note
	e.Do(t, "POST", "/v1/notes", bob, api.CreateNote{Body: "СЕКРЕТ Боба"}, &secret)

	db.QueryTimeout = 300 * time.Millisecond
	t.Cleanup(func() { db.QueryTimeout = 5 * time.Second })

	attacks := map[string]string{
		"подмена пользователя": "select set_config('app.user_id', '" + bobID + "', true), (select string_agg(body, ',') from notes)",
		"подмена роли":         "select set_config('role', 'epinoema_app', true), (select string_agg(body, ',') from notes)",
		"через query_to_xml":   "select query_to_xml('select set_config(''app.user_id'', ''" + bobID + "'', true)', false, false, '')",
		"SET перед SELECT":     "set local app.user_id = '" + bobID + "'; select body from notes",
		"RESET ROLE":           "reset role",
		"DO-блок":              "do $$ begin perform set_config('app.user_id', '" + bobID + "', true); end $$",
		"выход из подзапроса":  "select 1) q; select body from notes; select * from (select 1",
		"токены":               "select token_hash from api_tokens",
		"пользователи":         "select handle from users",
		"удаление":             "delete from notes returning body",
		"удаление в CTE":       "with d as (delete from notes returning body) select * from d",
		"блокировка":           "select body from notes for update",
		"зависание":            "select pg_sleep(5)",
	}
	for name, sql := range attacks {
		var res api.QueryResult
		code := e.Do(t, "POST", "/v1/query", alice, api.Query{SQL: sql}, &res)
		if code == 200 {
			t.Errorf("%s: прошло, rows=%s", name, res.Rows)
		}
	}

	// После атак соединения пула чистые: Алиса видит только своё, Боб — своё.
	for _, c := range []struct{ tok, want string }{{alice, "заметка Алисы"}, {bob, "СЕКРЕТ Боба"}} {
		for range 5 {
			var res api.QueryResult
			e.Do(t, "POST", "/v1/query", c.tok, api.Query{SQL: "select string_agg(body, ',') as b from notes"}, &res)
			if len(res.Rows) != 1 || !strings.Contains(string(res.Rows[0]), c.want) || strings.Contains(string(res.Rows[0]), ",") {
				t.Fatalf("после атак: %s", res.Rows)
			}
		}
	}
	if code := e.Do(t, "GET", "/v1/notes/"+secret.ID, bob, nil, nil); code != 200 {
		t.Errorf("заметка Боба пропала: %d", code)
	}
}
