// Package server — HTTP API Epinoema. CLI, агент и веб ходят только сюда.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/splav/epinoema/internal/api"
	"github.com/splav/epinoema/internal/db"
	"github.com/splav/epinoema/internal/db/dbq"
	"github.com/splav/epinoema/internal/token"
)

const maxBodyBytes = 1 << 20

type Server struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

func New(pool *pgxpool.Pool, log *slog.Logger) *Server {
	return &Server{pool: pool, log: log}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	authed := http.NewServeMux()
	authed.HandleFunc("GET /v1/me", s.me)
	authed.HandleFunc("POST /v1/notes", s.createNote)
	authed.HandleFunc("GET /v1/notes", s.listNotes)
	authed.HandleFunc("GET /v1/notes/{id}", s.getNote)
	authed.HandleFunc("PATCH /v1/notes/{id}", s.updateMeta)
	authed.HandleFunc("POST /v1/links", s.createLink)
	authed.HandleFunc("GET /v1/search", s.search)
	mux.Handle("/v1/", s.auth(authed))
	return mux
}

// principal — владелец токена запроса.
type principal struct {
	userID string
	handle string
	actor  db.Actor
}

type ctxKey struct{}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plain, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || plain == "" {
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		t, err := dbq.New(s.pool).GetActiveToken(r.Context(), token.Hash(plain))
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}
		if err != nil {
			s.internal(w, r, err)
			return
		}
		p := principal{userID: t.UserID, handle: t.Handle, actor: db.Actor(t.Actor)}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, p)))
	})
}

func principalOf(r *http.Request) principal {
	return r.Context().Value(ctxKey{}).(principal)
}

// inTx выполняет fn от имени пользователя запроса.
func (s *Server) inTx(r *http.Request, fn func(*dbq.Queries) error) error {
	p := principalOf(r)
	return db.InTx(r.Context(), s.pool, p.userID, p.actor, func(tx pgx.Tx) error {
		return fn(dbq.New(tx))
	})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	p := principalOf(r)
	writeJSON(w, http.StatusOK, api.Me{UserID: p.userID, Handle: p.handle, Actor: string(p.actor)})
}

func (s *Server) createNote(w http.ResponseWriter, r *http.Request) {
	var req api.CreateNote
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Body) == "" && (req.Title == nil || strings.TrimSpace(*req.Title) == "") {
		writeError(w, http.StatusBadRequest, "title or body is required")
		return
	}
	if req.Kind == "" {
		req.Kind = "note"
	}
	if req.Tags == nil {
		req.Tags = []string{}
	}

	var note api.Note
	err := s.inTx(r, func(q *dbq.Queries) error {
		row, err := q.CreateNote(r.Context(), dbq.CreateNoteParams{
			Title:      req.Title,
			Body:       req.Body,
			Kind:       req.Kind,
			Importance: req.Importance,
			Project:    req.Project,
			Tags:       req.Tags,
		})
		note = api.Note(row)
		return err
	})
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, note)
}

func (s *Server) listNotes(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "inbox"
	}
	limit, ok := limitParam(w, r, 50)
	if !ok {
		return
	}

	list := api.NoteList{Notes: []api.Note{}}
	err := s.inTx(r, func(q *dbq.Queries) error {
		rows, err := q.ListNotesByStatus(r.Context(), dbq.ListNotesByStatusParams{Status: status, Limit: limit})
		for _, row := range rows {
			list.Notes = append(list.Notes, api.Note(row))
		}
		return err
	})
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) getNote(w http.ResponseWriter, r *http.Request) {
	var detail api.NoteDetail
	err := s.inTx(r, func(q *dbq.Queries) error {
		row, err := q.GetNote(r.Context(), r.PathValue("id"))
		if err != nil {
			return err
		}
		detail.Note = api.Note(row)
		links, err := q.ListLinks(r.Context(), row.ID)
		detail.Links = make([]api.Link, 0, len(links))
		for _, l := range links {
			detail.Links = append(detail.Links, api.Link(l))
		}
		return err
	})
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) updateMeta(w http.ResponseWriter, r *http.Request) {
	var req api.UpdateMeta
	if !decode(w, r, &req) {
		return
	}
	if req.Empty() {
		writeError(w, http.StatusBadRequest, "nothing to update")
		return
	}
	params := dbq.UpdateNoteMetaParams{
		ID:         r.PathValue("id"),
		Title:      req.Title,
		Kind:       req.Kind,
		Status:     req.Status,
		Importance: req.Importance,
		Project:    req.Project,
	}
	if req.Tags != nil {
		params.Tags = *req.Tags
		if params.Tags == nil { // "tags": null не отличить от [] — считаем очисткой
			params.Tags = []string{}
		}
	}

	var note api.Note
	err := s.inTx(r, func(q *dbq.Queries) error {
		row, err := q.UpdateNoteMeta(r.Context(), params)
		note = api.Note(row)
		return err
	})
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, note)
}

func (s *Server) createLink(w http.ResponseWriter, r *http.Request) {
	var req api.CreateLink
	if !decode(w, r, &req) {
		return
	}
	if req.Kind == "" {
		req.Kind = "related"
	}

	var inserted int64
	err := s.inTx(r, func(q *dbq.Queries) error {
		var err error
		inserted, err = q.CreateLink(r.Context(), dbq.CreateLinkParams{SrcID: req.SrcID, DstID: req.DstID, Kind: req.Kind})
		return err
	})
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	code := http.StatusCreated
	if inserted == 0 {
		code = http.StatusOK // такая связь уже есть
	}
	writeJSON(w, code, req)
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeError(w, http.StatusBadRequest, "q is required")
		return
	}
	limit, ok := limitParam(w, r, 20)
	if !ok {
		return
	}

	list := api.NoteList{Notes: []api.Note{}}
	err := s.inTx(r, func(q *dbq.Queries) error {
		rows, err := q.SearchNotes(r.Context(), dbq.SearchNotesParams{Query: query, Lim: limit})
		for _, row := range rows {
			list.Notes = append(list.Notes, api.Note(row))
		}
		return err
	})
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// dbError переводит ошибки базы в ответы. Чужая заметка для RLS не существует,
// поэтому и для клиента это 404, а не 403.
func (s *Server) dbError(w http.ResponseWriter, r *http.Request, err error) {
	var pgErr *pgconn.PgError
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "note not found")
		return
	}
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == "22P02", // id не UUID
			pgErr.Code == "23503", // ссылка на несуществующую заметку
			pgErr.Code == "42501" && strings.Contains(pgErr.Message, "row-level security"):
			writeError(w, http.StatusNotFound, "note not found")
			return
		case pgErr.Code == "23514": // допустимые значения задаёт CHECK в схеме
			writeError(w, http.StatusBadRequest, "invalid value: "+pgErr.ConstraintName)
			return
		}
	}
	s.internal(w, r, err)
}

func (s *Server) internal(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad json: "+err.Error())
		return false
	}
	return true
}

func limitParam(w http.ResponseWriter, r *http.Request, def int32) (int32, bool) {
	v := r.URL.Query().Get("limit")
	if v == "" {
		return def, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 500 {
		writeError(w, http.StatusBadRequest, "limit must be 1..500")
		return 0, false
	}
	return int32(n), true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, api.Error{Error: msg})
}
