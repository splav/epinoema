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
	authed.HandleFunc("POST /v1/notes", s.createNote)
	authed.HandleFunc("GET /v1/notes", s.listNotes)
	authed.HandleFunc("GET /v1/notes/{id}", s.getNote)
	mux.Handle("/v1/", s.auth(authed))
	return mux
}

// principal — владелец токена запроса.
type principal struct {
	userID string
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
		p := principal{userID: t.UserID, actor: db.Actor(t.Actor)}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, p)))
	})
}

// inTx выполняет fn от имени пользователя запроса.
func (s *Server) inTx(r *http.Request, fn func(*dbq.Queries) error) error {
	p := r.Context().Value(ctxKey{}).(principal)
	return db.InTx(r.Context(), s.pool, p.userID, p.actor, func(tx pgx.Tx) error {
		return fn(dbq.New(tx))
	})
}

func (s *Server) createNote(w http.ResponseWriter, r *http.Request) {
	var req api.CreateNote
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad json: "+err.Error())
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
		// Допустимые kind и importance задаёт CHECK в схеме; нарушение — ошибка клиента.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23514" {
			writeError(w, http.StatusBadRequest, "invalid value: "+pgErr.ConstraintName)
			return
		}
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, note)
}

func (s *Server) listNotes(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "inbox"
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			writeError(w, http.StatusBadRequest, "limit must be 1..500")
			return
		}
		limit = n
	}

	list := api.NoteList{Notes: []api.Note{}}
	err := s.inTx(r, func(q *dbq.Queries) error {
		rows, err := q.ListNotesByStatus(r.Context(), dbq.ListNotesByStatusParams{
			Status: status,
			Limit:  int32(limit),
		})
		for _, row := range rows {
			list.Notes = append(list.Notes, api.Note(row))
		}
		return err
	})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) getNote(w http.ResponseWriter, r *http.Request) {
	var note api.Note
	err := s.inTx(r, func(q *dbq.Queries) error {
		row, err := q.GetNote(r.Context(), r.PathValue("id"))
		note = api.Note(row)
		return err
	})
	var pgErr *pgconn.PgError
	switch {
	// Чужая заметка для RLS не существует, поэтому и для клиента это 404.
	case errors.Is(err, pgx.ErrNoRows),
		errors.As(err, &pgErr) && pgErr.Code == "22P02": // id не UUID
		writeError(w, http.StatusNotFound, "note not found")
	case err != nil:
		s.internal(w, r, err)
	default:
		writeJSON(w, http.StatusOK, note)
	}
}

func (s *Server) internal(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, api.Error{Error: msg})
}
