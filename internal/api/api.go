// Package api описывает JSON-контракт HTTP API; его используют сервер и клиенты.
package api

import (
	"encoding/json"
	"time"
)

// Note повторяет набор колонок из запросов к notes, поэтому строки sqlc
// конвертируются в него напрямую: api.Note(row).
type Note struct {
	ID         string          `json:"id"`
	Title      *string         `json:"title"`
	Body       string          `json:"body"`
	Kind       string          `json:"kind"`
	Status     string          `json:"status"`
	Importance *int16          `json:"importance"`
	Project    *string         `json:"project"`
	Tags       []string        `json:"tags"`
	Props      json.RawMessage `json:"props"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// CreateNote — тело POST /v1/notes. Нужен хотя бы title или body.
type CreateNote struct {
	Title      *string  `json:"title,omitempty"`
	Body       string   `json:"body"`
	Kind       string   `json:"kind,omitempty"`
	Importance *int16   `json:"importance,omitempty"`
	Project    *string  `json:"project,omitempty"`
	Tags       []string `json:"tags,omitempty"`
}

type NoteList struct {
	Notes []Note `json:"notes"`
}

type Error struct {
	Error string `json:"error"`
}

// UpdateMeta — тело PATCH /v1/notes/{id}. Меняются только переданные поля:
// "" очищает title и project, importance 0 очищает важность, tags заменяют список.
type UpdateMeta struct {
	Title      *string   `json:"title,omitempty"`
	Kind       *string   `json:"kind,omitempty"`
	Status     *string   `json:"status,omitempty"`
	Importance *int16    `json:"importance,omitempty"`
	Project    *string   `json:"project,omitempty"`
	Tags       *[]string `json:"tags,omitempty"`
}

func (m UpdateMeta) Empty() bool {
	return m.Title == nil && m.Kind == nil && m.Status == nil &&
		m.Importance == nil && m.Project == nil && m.Tags == nil
}

// Link — связь между заметками; kind: related, derived_from, supersedes.
type Link struct {
	SrcID     string    `json:"src_id"`
	DstID     string    `json:"dst_id"`
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"created_at"`
}

type CreateLink struct {
	SrcID string `json:"src_id"`
	DstID string `json:"dst_id"`
	Kind  string `json:"kind,omitempty"`
}

// NoteDetail — ответ GET /v1/notes/{id}: заметка и все её видимые связи.
type NoteDetail struct {
	Note
	Links []Link `json:"links"`
}

// Me — ответ GET /v1/me: чей токен и кто он, человек или агент.
type Me struct {
	UserID string `json:"user_id"`
	Handle string `json:"handle"`
	Actor  string `json:"actor"`
}

// Query — тело POST /v1/query: произвольный read-only SELECT для разовых отчётов.
type Query struct {
	SQL   string `json:"sql"`
	Limit int    `json:"limit,omitempty"`
}

// QueryResult — строки как объекты {колонка: значение} в порядке колонок запроса.
type QueryResult struct {
	Rows      []json.RawMessage `json:"rows"`
	Truncated bool              `json:"truncated"`
}

type Project struct {
	Name        string    `json:"name"`
	Notes       int64     `json:"notes"`
	LastUpdated time.Time `json:"last_updated"`
}

type ProjectList struct {
	Projects []Project `json:"projects"`
}

// RenameProject — тело POST /v1/projects/rename. Если проект To уже есть,
// заметки From присоединяются к нему.
type RenameProject struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type RenameProjectResult struct {
	Renamed int64 `json:"renamed"`
	Merged  bool  `json:"merged"`
}
