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
	want := "add_note,get_note,link,list_inbox,search,update_meta"
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
