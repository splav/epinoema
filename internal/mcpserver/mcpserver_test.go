package mcpserver_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/splav/epinoema/internal/client"
	"github.com/splav/epinoema/internal/db"
	"github.com/splav/epinoema/internal/mcpserver"
	"github.com/splav/epinoema/internal/testenv"
)

// session подключает MCP-клиент к серверу через память, а сервер — к настоящему API.
func session(t *testing.T, e *testenv.Env, tok string) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := mcpserver.New(client.New(e.URL, tok)).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// call вызывает инструмент и раскладывает structuredContent в out.
func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, out any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if out != nil && !res.IsError {
		b, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	return res
}

func TestTools(t *testing.T) {
	cs := session(t, testenv.New(t), "")
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	want := "add_note,get_note,link,list_inbox,list_projects,list_reports,query,rename_project,run_report,save_report,search,update_meta"
	if got := strings.Join(names, ","); got != want {
		t.Errorf("tools = %s, want %s", got, want)
	}
	if !strings.Contains(cs.InitializeResult().Instructions, "не меняется никогда") {
		t.Error("в initialize нет правил для агента")
	}
}

// Сценарий этапа 1: агент разбирает входящие, правки помечены как его.
func TestTriage(t *testing.T) {
	e := testenv.New(t)
	userID, human := e.User(t, "alice", "user")
	_, agentTok := e.User(t, "alice", "agent")
	cs := session(t, e, agentTok)

	var old, fresh mcpserver.Brief
	e.Do(t, "POST", "/v1/notes", human, map[string]any{"body": "Поиск по заметкам через pgvector", "kind": "idea", "tags": []string{"поиск"}}, &old)
	call(t, cs, "add_note", map[string]any{"body": "идеи по семантическому поиску " + strings.Repeat("длинный текст ", 40)}, &fresh)

	var inbox mcpserver.BriefList
	call(t, cs, "list_inbox", nil, &inbox)
	if len(inbox.Notes) != 2 || inbox.Notes[0].ID != fresh.ID {
		t.Fatalf("inbox: %+v", inbox.Notes)
	}
	if n := len([]rune(inbox.Notes[0].Snippet)); n > 280 {
		t.Errorf("snippet не обрезан: %d символов", n)
	}

	var found mcpserver.BriefList
	call(t, cs, "search", map[string]any{"query": "поиск"}, &found)
	if len(found.Notes) != 2 {
		t.Errorf("search: %d результатов, want 2", len(found.Notes))
	}

	var updated mcpserver.Brief
	call(t, cs, "update_meta", map[string]any{"id": fresh.ID, "kind": "idea", "status": "active", "tags": []string{" поиск ", ""}}, &updated)
	if updated.Status != "active" || len(updated.Tags) != 1 || updated.Tags[0] != "поиск" {
		t.Errorf("update_meta: %+v", updated)
	}

	var link struct{ Created bool }
	call(t, cs, "link", map[string]any{"src_id": fresh.ID, "dst_id": old.ID}, &link)
	if !link.Created {
		t.Error("link: связь не создана")
	}
	var detail struct {
		Body  string
		Links []struct {
			DstID string `json:"dst_id"`
		}
	}
	call(t, cs, "get_note", map[string]any{"id": fresh.ID}, &detail)
	if len(detail.Links) != 1 || detail.Links[0].DstID != old.ID || !strings.HasPrefix(detail.Body, "идеи") {
		t.Errorf("get_note: %+v", detail)
	}

	// Ошибка API приходит агенту как ошибка инструмента, а не обрывает сессию.
	if res := call(t, cs, "update_meta", map[string]any{"id": fresh.ID, "status": "archived"}, nil); !res.IsError {
		t.Error("неверный статус не вернул ошибку")
	}

	// Правка через MCP записана в историю как правка агента.
	var actor string
	err := db.InTx(context.Background(), e.Pool, userID, db.ActorUser, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(),
			"SELECT actor FROM revisions WHERE note_id = $1 ORDER BY id LIMIT 1", fresh.ID).Scan(&actor)
	})
	if err != nil || actor != "agent" {
		t.Errorf("автор правки: %q (err %v), want agent", actor, err)
	}
}

func TestReportsAndQuery(t *testing.T) {
	e := testenv.New(t)
	_, agentTok := e.User(t, "alice", "agent")
	cs := session(t, e, agentTok)
	call(t, cs, "add_note", map[string]any{"body": "про поиск", "kind": "idea", "project": "epi"}, nil)
	call(t, cs, "add_note", map[string]any{"body": "про отчёты", "kind": "decision", "project": "epi"}, nil)

	var review struct {
		Sections []struct {
			Title string
			Notes []mcpserver.Brief
		}
	}
	call(t, cs, "run_report", map[string]any{"name": "review"}, &review)
	if len(review.Sections) != 3 || len(review.Sections[0].Notes) != 2 {
		t.Errorf("review: %+v", review)
	}

	var saved struct{ Created bool }
	res := call(t, cs, "save_report", map[string]any{"name": "ideas", "spec": map[string]any{"kind": []string{"idea"}}}, &saved)
	if res.IsError || !saved.Created {
		t.Fatalf("save_report: %+v", res.Content)
	}
	var ideas struct {
		Sections []struct{ Notes []mcpserver.Brief }
	}
	call(t, cs, "run_report", map[string]any{"name": "ideas"}, &ideas)
	if len(ideas.Sections) != 1 || len(ideas.Sections[0].Notes) != 1 || ideas.Sections[0].Notes[0].Snippet != "про поиск" {
		t.Errorf("ideas: %+v", ideas)
	}
	if res := call(t, cs, "save_report", map[string]any{"name": "bad", "spec": map[string]any{"kind": []string{"ideas"}}}, nil); !res.IsError {
		t.Error("save_report с неверным kind прошёл")
	}

	var q struct {
		Rows      []map[string]any
		Truncated bool
	}
	call(t, cs, "query", map[string]any{"sql": "select kind, count(*) as n from notes group by kind order by kind;"}, &q)
	if len(q.Rows) != 2 || q.Rows[0]["kind"] != "decision" || q.Rows[0]["n"] != float64(1) {
		t.Errorf("query: %+v", q)
	}
	if res := call(t, cs, "query", map[string]any{"sql": "select token_hash from api_tokens"}, nil); !res.IsError {
		t.Error("query к api_tokens прошёл")
	}
}

func TestProjects(t *testing.T) {
	e := testenv.New(t)
	_, agentTok := e.User(t, "alice", "agent")
	cs := session(t, e, agentTok)
	call(t, cs, "add_note", map[string]any{"body": "a", "project": "old"}, nil)

	var res struct {
		Renamed int
		Merged  bool
	}
	call(t, cs, "rename_project", map[string]any{"from": "old", "to": "new"}, &res)
	if res.Renamed != 1 || res.Merged {
		t.Errorf("rename_project: %+v", res)
	}
	var list struct{ Projects []struct{ Name string } }
	call(t, cs, "list_projects", nil, &list)
	if len(list.Projects) != 1 || list.Projects[0].Name != "new" {
		t.Errorf("list_projects: %+v", list)
	}
}
