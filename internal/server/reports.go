package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/splav/epinoema/internal/api"
	"github.com/splav/epinoema/internal/db/dbq"
)

func ptr[T any](v T) *T { return &v }

// Встроенные отчёты есть у каждого пользователя, их нельзя перезаписать.
// Порядок — порядок в списке отчётов.
var builtinOrder = []string{"review", "inbox", "stale", "week", "all"}

var (
	inboxSection = api.Section{Title: "Входящие", Filter: api.Filter{Status: []string{"inbox"}, Order: "created"}}
	staleSection = api.Section{Title: "Важные идеи без движения больше двух недель", Filter: api.Filter{
		Kind: []string{"idea"}, Status: []string{"active", "parked"}, ImportanceGte: ptr[int16](3), StaleDays: ptr[int32](14),
	}}
	weekSection = api.Section{Title: "Изменено за неделю", Filter: api.Filter{
		Status: []string{"active", "parked", "done", "dropped"}, ChangedDays: ptr[int32](7),
	}}

	builtinReports = map[string]api.ReportSpec{
		// Еженедельное ревью одним отчётом — критерий готовности этапа 2.
		"review": {Sections: []api.Section{inboxSection, staleSection, weekSection}},
		"inbox":  {Sections: []api.Section{inboxSection}},
		"stale":  {Sections: []api.Section{staleSection}},
		"week":   {Sections: []api.Section{{Title: "Изменено за неделю", Filter: api.Filter{ChangedDays: ptr[int32](7)}}}},
		"all":    {Sections: []api.Section{{Title: "Все заметки", Filter: api.Filter{Limit: 200}}}},
	}
)

func (s *Server) listReports(w http.ResponseWriter, r *http.Request) {
	list := api.ReportList{Reports: []api.Report{}}
	for _, name := range builtinOrder {
		list.Reports = append(list.Reports, api.Report{Name: name, Builtin: true, Spec: builtinReports[name]})
	}
	err := s.inTx(r, func(q *dbq.Queries) error {
		rows, err := q.ListReports(r.Context())
		for _, row := range rows {
			var spec api.ReportSpec
			if err := json.Unmarshal(row.Spec, &spec); err != nil {
				return err
			}
			list.Reports = append(list.Reports, api.Report{Name: row.Name, Spec: spec})
		}
		return err
	})
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) saveReport(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !api.ValidReportName(name) {
		writeError(w, http.StatusBadRequest, "name: латиница в нижнем регистре, цифры, - и _, до 40 символов")
		return
	}
	if _, ok := builtinReports[name]; ok {
		writeError(w, http.StatusBadRequest, "встроенный отчёт нельзя перезаписать: "+name)
		return
	}
	var spec api.ReportSpec
	if !decode(w, r, &spec) {
		return
	}
	if err := spec.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Сохраняем то, что разобрали, а не исходный текст: так в базе нет мусора.
	raw, _ := json.Marshal(spec)

	var created bool
	err := s.inTx(r, func(q *dbq.Queries) error {
		var err error
		created, err = q.UpsertReport(r.Context(), dbq.UpsertReportParams{Name: name, Spec: raw})
		return err
	})
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	code := http.StatusOK
	if created {
		code = http.StatusCreated
	}
	writeJSON(w, code, api.Report{Name: name, Spec: spec})
}

func (s *Server) deleteReport(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, ok := builtinReports[name]; ok {
		writeError(w, http.StatusBadRequest, "встроенный отчёт нельзя удалить: "+name)
		return
	}
	var n int64
	err := s.inTx(r, func(q *dbq.Queries) error {
		var err error
		n, err = q.DeleteReport(r.Context(), name)
		return err
	})
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	if n == 0 {
		writeError(w, http.StatusNotFound, "report not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// runReport выполняет отчёт. ?project= и ?tag= сужают каждую секцию:
// «всё по проекту» — это /v1/reports/all/run?project=…
func (s *Server) runReport(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	project := r.URL.Query().Get("project")
	tags := r.URL.Query()["tag"]

	res := api.ReportResult{Name: name, Sections: []api.SectionResult{}}
	err := s.inTx(r, func(q *dbq.Queries) error {
		spec, ok := builtinReports[name]
		if !ok {
			row, err := q.GetReport(r.Context(), name)
			if err != nil {
				return err
			}
			if err := json.Unmarshal(row.Spec, &spec); err != nil {
				return err
			}
		}
		for _, sec := range spec.Normalized() {
			f := sec.Filter
			if project != "" {
				f.Project = &project
			}
			if len(tags) > 0 {
				f.TagsAny = tags
			}
			rows, err := q.FilterNotes(r.Context(), filterParams(f))
			if err != nil {
				return err
			}
			out := api.SectionResult{Title: sec.Title, Notes: make([]api.Note, 0, len(rows))}
			for _, row := range rows {
				out.Notes = append(out.Notes, api.Note(row))
			}
			res.Sections = append(res.Sections, out)
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "report not found")
		return
	}
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func filterParams(f api.Filter) dbq.FilterNotesParams {
	p := dbq.FilterNotesParams{
		Project:       f.Project,
		ImportanceGte: f.ImportanceGte,
		StaleDays:     f.StaleDays,
		ChangedDays:   f.ChangedDays,
		OrderBy:       f.Order,
		Lim:           f.Limit,
	}
	// Пустой список в SQL — «ни одного», а в фильтре — «без ограничения».
	if len(f.Kind) > 0 {
		p.Kinds = f.Kind
	}
	if len(f.Status) > 0 {
		p.Statuses = f.Status
	}
	if len(f.TagsAny) > 0 {
		p.TagsAny = f.TagsAny
	}
	if p.Lim == 0 {
		p.Lim = 50
	}
	return p
}
