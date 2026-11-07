package server

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/splav/epinoema/internal/api"
	"github.com/splav/epinoema/internal/db/dbq"
	"github.com/splav/epinoema/internal/gantt"
)

// ganttChart строит диаграмму по заметкам с датами или зависимостями.
// ?project= и ?tag= ограничивают набор; зависимости от заметок вне набора
// попадают в предупреждения.
func (s *Server) ganttChart(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	tags := r.URL.Query()["tag"]

	var tasks []gantt.Task
	err := s.inTx(r, func(q *dbq.Queries) error {
		p := dbq.GanttNotesParams{}
		if project != "" {
			p.Project = &project
		}
		if len(tags) > 0 {
			p.TagsAny = tags
		}
		notes, err := q.GanttNotes(r.Context(), p)
		if err != nil {
			return err
		}
		ids := make([]string, len(notes))
		index := make(map[string]int, len(notes))
		for i, n := range notes {
			ids[i] = n.ID
			index[n.ID] = i
			t := gantt.Task{ID: n.ID, Title: title(api.Note(n)), Status: n.Status}
			if t.Start, err = dateOf(n.StartOn); err != nil {
				return err
			}
			if t.Due, err = dateOf(n.DueOn); err != nil {
				return err
			}
			tasks = append(tasks, t)
		}
		links, err := q.DependencyLinks(r.Context(), ids)
		for _, l := range links {
			tasks[index[l.SrcID]].DependsOn = append(tasks[index[l.SrcID]].DependsOn, l.DstID)
		}
		return err
	})
	if err != nil {
		s.dbError(w, r, err)
		return
	}

	bars, warnings, err := gantt.Build(tasks, time.Now())
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	res := api.Gantt{Title: project, Bars: bars, Warnings: warnings}
	if res.Title == "" {
		res.Title = "Все проекты"
	}
	if res.Warnings == nil {
		res.Warnings = []string{}
	}
	res.Mermaid = gantt.Mermaid(res.Title, bars)
	writeJSON(w, http.StatusOK, res)
}

func dateOf(d *api.Date) (*time.Time, error) {
	if d == nil {
		return nil, nil
	}
	t, err := d.Time()
	return &t, err
}

// title — заголовок или первая строка текста, до 60 символов.
func title(n api.Note) string {
	s := n.Body
	if n.Title != nil && *n.Title != "" {
		s = *n.Title
	}
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if utf8.RuneCountInString(s) > 60 {
		s = string([]rune(s)[:59]) + "…"
	}
	return s
}
