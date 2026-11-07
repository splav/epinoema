package server_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/splav/epinoema/internal/api"
	"github.com/splav/epinoema/internal/db"
)

// note вставляет заметку напрямую, чтобы задать updated_at в прошлом:
// через API его выставляет триггер.
func note(t *testing.T, e *env, userID, body, kind, status string, importance int, project string, ageDays int) {
	t.Helper()
	err := db.InTx(context.Background(), e.Pool, userID, db.ActorUser, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `
			INSERT INTO notes (owner_id, body, kind, status, importance, project, created_at, updated_at)
			VALUES (app_user_id(), $1, $2, $3, nullif($4, 0), nullif($5, ''),
			        now() - make_interval(days => $6), now() - make_interval(days => $6))`,
			body, kind, status, importance, project, ageDays)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func bodies(s api.SectionResult) []string {
	out := []string{}
	for _, n := range s.Notes {
		out = append(out, n.Body)
	}
	return out
}

func TestReviewReport(t *testing.T) {
	e := newEnv(t)
	alice, tok := e.User(t, "alice", "user")
	note(t, e, alice, "новая во входящих", "note", "inbox", 0, "", 0)
	note(t, e, alice, "важная забытая идея", "idea", "active", 3, "epi", 20)
	note(t, e, alice, "неважная забытая идея", "idea", "parked", 1, "", 20)
	note(t, e, alice, "движется на неделе", "idea", "active", 3, "epi", 2)
	note(t, e, alice, "брошена давно", "idea", "dropped", 3, "", 30)

	var res api.ReportResult
	if code := e.Do(t, "GET", "/v1/reports/review/run", tok, nil, &res); code != 200 {
		t.Fatalf("review: %d", code)
	}
	want := [][]string{{"новая во входящих"}, {"важная забытая идея"}, {"движется на неделе"}}
	if len(res.Sections) != 3 {
		t.Fatalf("секций %d, want 3", len(res.Sections))
	}
	for i, w := range want {
		if got := bodies(res.Sections[i]); len(got) != len(w) || got[0] != w[0] {
			t.Errorf("%s: %v, want %v", res.Sections[i].Title, got, w)
		}
	}

	// «Всё по проекту».
	e.Do(t, "GET", "/v1/reports/all/run?project=epi", tok, nil, &res)
	if got := bodies(res.Sections[0]); len(got) != 2 || got[0] != "движется на неделе" {
		t.Errorf("all?project=epi: %v", got)
	}
}

func TestSavedReports(t *testing.T) {
	e := newEnv(t)
	alice, tok := e.User(t, "alice", "user")
	_, bob := e.User(t, "bob", "user")
	note(t, e, alice, "решение", "decision", "done", 0, "", 1)
	note(t, e, alice, "идея", "idea", "active", 0, "", 1)

	// Плоская форма, как в примере из схемы.
	if code := e.Do(t, "PUT", "/v1/reports/decisions", tok, map[string]any{"kind": []string{"decision"}}, nil); code != 201 {
		t.Fatalf("create: %d", code)
	}
	if code := e.Do(t, "PUT", "/v1/reports/decisions", tok, map[string]any{"kind": []string{"decision", "idea"}}, nil); code != 200 {
		t.Errorf("update: %d, want 200", code)
	}
	var res api.ReportResult
	e.Do(t, "GET", "/v1/reports/decisions/run", tok, nil, &res)
	if len(res.Sections) != 1 || len(res.Sections[0].Notes) != 2 {
		t.Errorf("run: %+v", res.Sections)
	}

	var list api.ReportList
	e.Do(t, "GET", "/v1/reports", tok, nil, &list)
	if last := list.Reports[len(list.Reports)-1]; last.Name != "decisions" || last.Builtin {
		t.Errorf("list: %+v", list.Reports)
	}

	for name, c := range map[string]struct {
		path string
		body any
	}{
		"неизвестный kind": {"/v1/reports/x", map[string]any{"kind": []string{"ideas"}}},
		"неизвестное поле": {"/v1/reports/x", map[string]any{"kinds": []string{"idea"}}},
		"обе формы":        {"/v1/reports/x", map[string]any{"kind": []string{"idea"}, "sections": []any{map[string]any{"title": "a", "filter": map[string]any{}}}}},
		"встроенный":       {"/v1/reports/review", map[string]any{}},
		"имя с пробелом":   {"/v1/reports/my%20report", map[string]any{}},
	} {
		if code := e.Do(t, "PUT", c.path, tok, c.body, nil); code != 400 {
			t.Errorf("%s: %d, want 400", name, code)
		}
	}

	if code := e.Do(t, "GET", "/v1/reports/decisions/run", bob, nil, nil); code != 404 {
		t.Errorf("Боб запускает отчёт Алисы: %d, want 404", code)
	}
	if code := e.Do(t, "DELETE", "/v1/reports/decisions", bob, nil, nil); code != 404 {
		t.Errorf("Боб удаляет отчёт Алисы: %d, want 404", code)
	}
	if code := e.Do(t, "DELETE", "/v1/reports/decisions", tok, nil, nil); code != 204 {
		t.Errorf("delete: %d, want 204", code)
	}
	if code := e.Do(t, "DELETE", "/v1/reports/inbox", tok, nil, nil); code != 400 {
		t.Errorf("удаление встроенного: %d, want 400", code)
	}
}
