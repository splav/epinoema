package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
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

	// Для каждой атаки — фрагмент ожидаемой ошибки (варианты через |): атака,
	// упавшая по посторонней причине (опечатка в тесте), не должна считаться отражённой.
	attacks := []struct{ name, sql, want string }{
		{"подмена пользователя", "select set_config('app.user_id', '" + bobID + "', true), (select string_agg(body, ',') from notes)", "permission denied for function set_config"},
		{"подмена роли", "select set_config('role', 'epinoema_app', true), (select string_agg(body, ',') from notes)", "permission denied for function set_config"},
		{"через query_to_xml", "select query_to_xml('select set_config(''app.user_id'', ''" + bobID + "'', true)', false, false, '')", "permission denied for function set_config"},
		{"SET перед SELECT", "set local app.user_id = '" + bobID + "'; select body from notes", "syntax error"},
		{"RESET ROLE", "reset role", "syntax error"},
		{"DO-блок", "do $$ begin perform set_config('app.user_id', '" + bobID + "', true); end $$", "syntax error"},
		{"выход из подзапроса", "select 1) q; select body from notes; select * from (select 1", "syntax error|multiple commands"},
		{"токены", "select token_hash from api_tokens", "permission denied for table api_tokens"},
		{"пользователи", "select handle from users", "permission denied for table users"},
		{"удаление", "delete from notes returning body", "syntax error"},
		{"удаление в CTE", "with d as (delete from notes returning body) select * from d", "must be at the top level"},
		{"блокировка строк", "select body from notes for update", "read-only transaction"},
		{"зависание", "select pg_sleep(5)", "timed out"},
		// Блокировки уровня сессии переживают ROLLBACK и висят на соединении пула;
		// в большом количестве они исчерпывают общую память всего кластера.
		{"advisory lock", "select pg_advisory_lock(42)", "permission denied for function pg_advisory_lock"},
		{"try advisory lock", "select pg_try_advisory_lock(1, 2)", "permission denied for function pg_try_advisory_lock"},
		{"shared advisory lock", "select pg_advisory_lock_shared(42)", "permission denied for function pg_advisory_lock_shared"},
		// Нетранзакционная запись в WAL: ROLLBACK её не отменяет.
		{"запись в WAL", "select pg_logical_emit_message(false, 'x', 'y')", "permission denied for function pg_logical_emit_message"},
	}
	for _, a := range attacks {
		code, msg := queryErr(t, e, alice, a.sql)
		if code == 200 || !containsAny(msg, strings.Split(a.want, "|")) {
			t.Errorf("%s: %d %q, want ошибку с %q", a.name, code, msg, a.want)
		}
	}

	// Строка больше предела не должна целиком приезжать в память сервиса.
	var big api.QueryResult
	if code := e.Do(t, "POST", "/v1/query", alice, api.Query{SQL: "select repeat('x', 5000000) as a"}, &big); code != 200 || len(big.Rows) != 0 || !big.Truncated {
		t.Errorf("огромная строка: %d, rows=%d, truncated=%v", code, len(big.Rows), big.Truncated)
	}

	// Ни одно соединение не держит advisory-блокировок.
	var locks int
	if err := e.Pool.QueryRow(context.Background(), "select count(*) from pg_locks where locktype = 'advisory'").Scan(&locks); err != nil || locks != 0 {
		t.Errorf("advisory-блокировок после атак: %d (err %v)", locks, err)
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

// queryErr выполняет запрос и возвращает код ответа и текст ошибки.
func queryErr(t *testing.T, e *env, tok, sql string) (int, string) {
	t.Helper()
	b, _ := json.Marshal(api.Query{SQL: sql})
	req, _ := http.NewRequest("POST", e.URL+"/v1/query", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var apiErr api.Error
	json.NewDecoder(resp.Body).Decode(&apiErr)
	return resp.StatusCode, apiErr.Error
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
