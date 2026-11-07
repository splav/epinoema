// Package mcpserver — MCP-сервер для агента. Это тонкий клиент HTTP API со своим
// токеном: агент проходит ту же авторизацию, RLS и историю правок, что и человек.
package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/splav/epinoema/internal/api"
	"github.com/splav/epinoema/internal/client"
)

// Version попадает в ответ initialize.
var Version = "0.1.0"

// Краткая версия правил из skills/epinoema-inbox/SKILL.md: её получает любой
// MCP-клиент, даже если skill не установлен.
const instructions = `Epinoema — база идей и заметок пользователя.
Правила:
- Записывая мысль пользователя (add_note), сохраняй текст как сказано, не переписывай и не дополняй.
- Разбирая входящие (list_inbox), сначала предложи тип, теги, проект и связи для каждой заметки и дождись подтверждения; только потом вызывай update_meta и link.
- Текст заметки не меняется никогда; важность — только если пользователь сам её назвал.
- Перед тем как придумать новый тег или проект, поищи похожие заметки (search) и переиспользуй существующие.`

const snippetLen = 280

// New регистрирует инструменты поверх клиента API.
func New(c *client.Client) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "epinoema", Version: Version}, &mcp.ServerOptions{Instructions: instructions})
	t := tools{c: c}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "add_note",
		Description: "Записать заметку во входящие. Обязательных полей нет, кроме текста; структуру добавляют позже при разборе.",
	}, t.addNote)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_inbox",
		Description: "Заметки во входящих, новые сверху. Текст обрезан; полный — через get_note.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, t.listInbox)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "search",
		Description: "Полнотекстовый поиск по заголовкам и тексту с учётом русских словоформ и опечаток в заголовках. Поддерживает \"точную фразу\", OR и -исключение.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, t.search)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_note",
		Description: "Заметка целиком вместе со связями.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, t.getNote)
	mcp.AddTool(s, &mcp.Tool{
		Name: "update_meta",
		Description: "Изменить метаданные заметки: заголовок, тип, статус, важность, проект, теги. Меняются только переданные поля. " +
			"Текст заметки этим инструментом не меняется. Каждая правка сохраняется в истории с пометкой, что её сделал агент.",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true},
	}, t.updateMeta)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "link",
		Description: "Связать две заметки. Повторная связь не создаёт дубля.",
		Annotations: &mcp.ToolAnnotations{IdempotentHint: true},
	}, t.link)
	return s
}

type tools struct{ c *client.Client }

// Brief — заметка без полного текста, чтобы списки не съедали контекст агента.
type Brief struct {
	ID         string   `json:"id"`
	Title      *string  `json:"title,omitempty"`
	Snippet    string   `json:"snippet"`
	Kind       string   `json:"kind"`
	Status     string   `json:"status"`
	Importance *int16   `json:"importance,omitempty"`
	Project    *string  `json:"project,omitempty"`
	Tags       []string `json:"tags"`
	CreatedAt  string   `json:"created_at"`
}

type BriefList struct {
	Notes []Brief `json:"notes"`
}

func brief(n api.Note) Brief {
	snippet := n.Body
	if utf8.RuneCountInString(snippet) > snippetLen {
		snippet = string([]rune(snippet)[:snippetLen-1]) + "…"
	}
	return Brief{
		ID: n.ID, Title: n.Title, Snippet: snippet, Kind: n.Kind, Status: n.Status,
		Importance: n.Importance, Project: n.Project, Tags: n.Tags,
		CreatedAt: n.CreatedAt.Format("2006-01-02 15:04"),
	}
}

func briefs(notes []api.Note) BriefList {
	out := BriefList{Notes: make([]Brief, 0, len(notes))}
	for _, n := range notes {
		out.Notes = append(out.Notes, brief(n))
	}
	return out
}

type AddNoteIn struct {
	Body       string   `json:"body" jsonschema:"текст заметки в Markdown, как сказал пользователь"`
	Title      string   `json:"title,omitempty" jsonschema:"заголовок, если пользователь его назвал"`
	Kind       string   `json:"kind,omitempty" jsonschema:"тип: note, idea, decision, meeting, project; по умолчанию note"`
	Project    string   `json:"project,omitempty" jsonschema:"проект"`
	Tags       []string `json:"tags,omitempty" jsonschema:"теги"`
	Importance int16    `json:"importance,omitempty" jsonschema:"важность 1–3, только если её назвал пользователь"`
}

func (t tools) addNote(ctx context.Context, _ *mcp.CallToolRequest, in AddNoteIn) (*mcp.CallToolResult, Brief, error) {
	req := api.CreateNote{Body: in.Body, Kind: in.Kind, Tags: in.Tags}
	if in.Title != "" {
		req.Title = &in.Title
	}
	if in.Project != "" {
		req.Project = &in.Project
	}
	if in.Importance != 0 {
		req.Importance = &in.Importance
	}
	n, err := t.c.CreateNote(ctx, req)
	if err != nil {
		return nil, Brief{}, err
	}
	return nil, brief(n), nil
}

type ListInboxIn struct {
	Limit int `json:"limit,omitempty" jsonschema:"сколько вернуть, по умолчанию 50"`
}

func (t tools) listInbox(ctx context.Context, _ *mcp.CallToolRequest, in ListInboxIn) (*mcp.CallToolResult, BriefList, error) {
	notes, err := t.c.ListNotes(ctx, "inbox", orDefault(in.Limit, 50))
	if err != nil {
		return nil, BriefList{}, err
	}
	return nil, briefs(notes), nil
}

type SearchIn struct {
	Query string `json:"query" jsonschema:"поисковый запрос"`
	Limit int    `json:"limit,omitempty" jsonschema:"сколько вернуть, по умолчанию 20"`
}

func (t tools) search(ctx context.Context, _ *mcp.CallToolRequest, in SearchIn) (*mcp.CallToolResult, BriefList, error) {
	notes, err := t.c.Search(ctx, in.Query, orDefault(in.Limit, 20))
	if err != nil {
		return nil, BriefList{}, err
	}
	return nil, briefs(notes), nil
}

type GetNoteIn struct {
	ID string `json:"id" jsonschema:"id заметки"`
}

// NoteDetail — api.NoteDetail для MCP: props объявлен объектом, а не json.RawMessage,
// который схема описывает как массив байт.
type NoteDetail struct {
	ID         string         `json:"id"`
	Title      *string        `json:"title,omitempty"`
	Body       string         `json:"body"`
	Kind       string         `json:"kind"`
	Status     string         `json:"status"`
	Importance *int16         `json:"importance,omitempty"`
	Project    *string        `json:"project,omitempty"`
	Tags       []string       `json:"tags"`
	Props      map[string]any `json:"props"`
	CreatedAt  string         `json:"created_at"`
	UpdatedAt  string         `json:"updated_at"`
	Links      []api.Link     `json:"links"`
}

func (t tools) getNote(ctx context.Context, _ *mcp.CallToolRequest, in GetNoteIn) (*mcp.CallToolResult, NoteDetail, error) {
	d, err := t.c.GetNote(ctx, in.ID)
	if err != nil {
		return nil, NoteDetail{}, err
	}
	out := NoteDetail{
		ID: d.ID, Title: d.Title, Body: d.Body, Kind: d.Kind, Status: d.Status,
		Importance: d.Importance, Project: d.Project, Tags: d.Tags,
		CreatedAt: d.CreatedAt.Format(time.RFC3339), UpdatedAt: d.UpdatedAt.Format(time.RFC3339),
		Links: d.Links,
	}
	if err := json.Unmarshal(d.Props, &out.Props); err != nil {
		return nil, NoteDetail{}, err
	}
	return nil, out, nil
}

type UpdateMetaIn struct {
	ID         string    `json:"id" jsonschema:"id заметки"`
	Title      *string   `json:"title,omitempty" jsonschema:"новый заголовок; пустая строка убирает заголовок"`
	Kind       *string   `json:"kind,omitempty" jsonschema:"тип: note, idea, decision, meeting, project"`
	Status     *string   `json:"status,omitempty" jsonschema:"статус: inbox, active, parked, done, dropped"`
	Importance *int16    `json:"importance,omitempty" jsonschema:"важность 1–3; 0 убирает важность"`
	Project    *string   `json:"project,omitempty" jsonschema:"проект; пустая строка убирает проект"`
	Tags       *[]string `json:"tags,omitempty" jsonschema:"полный новый список тегов; заменяет старый"`
}

func (t tools) updateMeta(ctx context.Context, _ *mcp.CallToolRequest, in UpdateMetaIn) (*mcp.CallToolResult, Brief, error) {
	if in.Tags != nil {
		trimmed := make([]string, 0, len(*in.Tags))
		for _, tag := range *in.Tags {
			if tag = strings.TrimSpace(tag); tag != "" {
				trimmed = append(trimmed, tag)
			}
		}
		in.Tags = &trimmed
	}
	n, err := t.c.UpdateMeta(ctx, in.ID, api.UpdateMeta{
		Title: in.Title, Kind: in.Kind, Status: in.Status,
		Importance: in.Importance, Project: in.Project, Tags: in.Tags,
	})
	if err != nil {
		return nil, Brief{}, err
	}
	return nil, brief(n), nil
}

type LinkIn struct {
	SrcID string `json:"src_id" jsonschema:"id заметки, от которой идёт связь"`
	DstID string `json:"dst_id" jsonschema:"id заметки, на которую идёт связь"`
	Kind  string `json:"kind,omitempty" jsonschema:"related (по умолчанию), derived_from — src выросла из dst, supersedes — src заменяет dst"`
}

type LinkOut struct {
	Created bool `json:"created" jsonschema:"false, если такая связь уже была"`
}

func (t tools) link(ctx context.Context, _ *mcp.CallToolRequest, in LinkIn) (*mcp.CallToolResult, LinkOut, error) {
	created, err := t.c.CreateLink(ctx, api.CreateLink{SrcID: in.SrcID, DstID: in.DstID, Kind: in.Kind})
	return nil, LinkOut{Created: created}, err
}

func orDefault(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}
