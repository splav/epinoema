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
