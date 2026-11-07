package server_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/splav/epinoema/internal/api"
	"github.com/splav/epinoema/internal/db"
)

func TestRenameProject(t *testing.T) {
	e := newEnv(t)
	alice, tok := e.User(t, "alice", "agent")
	_, bob := e.User(t, "bob", "user")
	var a1 api.Note
	e.Do(t, "POST", "/v1/notes", tok, api.CreateNote{Body: "1", Project: ptr("epi")}, &a1)
	e.Do(t, "POST", "/v1/notes", tok, api.CreateNote{Body: "2", Project: ptr("epi")}, nil)
	e.Do(t, "POST", "/v1/notes", tok, api.CreateNote{Body: "3", Project: ptr("epinoema")}, nil)
	e.Do(t, "POST", "/v1/notes", bob, api.CreateNote{Body: "у Боба", Project: ptr("epi")}, nil)

	var list api.ProjectList
	e.Do(t, "GET", "/v1/projects", tok, nil, &list)
	if len(list.Projects) != 2 || list.Projects[0].Name != "epi" || list.Projects[0].Notes != 2 {
		t.Fatalf("projects: %+v", list.Projects)
	}

	var res api.RenameProjectResult
	if code := e.Do(t, "POST", "/v1/projects/rename", tok, api.RenameProject{From: "epi", To: "epinoema"}, &res); code != 200 {
		t.Fatalf("rename: %d", code)
	}
	if res.Renamed != 2 || !res.Merged {
		t.Errorf("rename: %+v, want 2 merged", res)
	}
	e.Do(t, "GET", "/v1/projects", tok, nil, &list)
	if len(list.Projects) != 1 || list.Projects[0].Name != "epinoema" || list.Projects[0].Notes != 3 {
		t.Errorf("после слияния: %+v", list.Projects)
	}

	// У Боба проект с тем же именем не тронут.
	e.Do(t, "GET", "/v1/projects", bob, nil, &list)
	if len(list.Projects) != 1 || list.Projects[0].Name != "epi" {
		t.Errorf("проект Боба: %+v", list.Projects)
	}

	// Каждая заметка получила ревизию с прежним проектом и автором-агентом.
	var actor, old string
	err := db.InTx(context.Background(), e.Pool, alice, db.ActorUser, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(),
			"SELECT actor, snapshot->>'project' FROM revisions WHERE note_id = $1", a1.ID).Scan(&actor, &old)
	})
	if err != nil || actor != "agent" || old != "epi" {
		t.Errorf("ревизия: actor=%q old=%q err=%v", actor, old, err)
	}

	for name, c := range map[string]struct {
		req  api.RenameProject
		code int
	}{
		"нет такого":   {api.RenameProject{From: "nope", To: "x"}, 404},
		"пустое новое": {api.RenameProject{From: "epinoema", To: " "}, 400},
		"то же имя":    {api.RenameProject{From: "epinoema", To: "epinoema"}, 400},
	} {
		if code := e.Do(t, "POST", "/v1/projects/rename", tok, c.req, nil); code != c.code {
			t.Errorf("%s: %d, want %d", name, code, c.code)
		}
	}
}
