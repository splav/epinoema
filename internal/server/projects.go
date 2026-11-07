package server

import (
	"net/http"
	"strings"

	"github.com/splav/epinoema/internal/api"
	"github.com/splav/epinoema/internal/db/dbq"
)

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	list := api.ProjectList{Projects: []api.Project{}}
	err := s.inTx(r, func(q *dbq.Queries) error {
		rows, err := q.ListProjects(r.Context())
		for _, row := range rows {
			list.Projects = append(list.Projects, api.Project(row))
		}
		return err
	})
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) renameProject(w http.ResponseWriter, r *http.Request) {
	var req api.RenameProject
	if !decode(w, r, &req) {
		return
	}
	req.From, req.To = strings.TrimSpace(req.From), strings.TrimSpace(req.To)
	switch {
	case req.From == "" || req.To == "":
		writeError(w, http.StatusBadRequest, "from and to are required; to clear a project use PATCH /v1/notes/{id}")
		return
	case req.From == req.To:
		writeError(w, http.StatusBadRequest, "from and to are the same")
		return
	}

	var res api.RenameProjectResult
	err := s.inTx(r, func(q *dbq.Queries) error {
		var err error
		if res.Merged, err = q.ProjectExists(r.Context(), req.To); err != nil {
			return err
		}
		res.Renamed, err = q.RenameProject(r.Context(), dbq.RenameProjectParams{OldName: req.From, NewName: req.To})
		return err
	})
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	if res.Renamed == 0 {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	writeJSON(w, http.StatusOK, res)
}
