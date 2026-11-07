// Package gantt строит расписание по датам и зависимостям заметок и рисует его
// как Mermaid или как полосы в терминале. Ничего не знает о базе и HTTP.
package gantt

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const layout = "2006-01-02"

// Task — заметка на входе. Start и Due необязательны.
type Task struct {
	ID        string
	Title     string
	Status    string
	Start     *time.Time
	Due       *time.Time
	DependsOn []string // id задач, после окончания которых начинается эта
}

// Bar — задача в расписании. Даты включительно, в формате YYYY-MM-DD.
type Bar struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Status    string   `json:"status"`
	Start     string   `json:"start"`
	End       string   `json:"end"`
	Scheduled bool     `json:"scheduled"` // начало вычислено, а не задано
	Late      bool     `json:"late"`      // зависимости не дают уложиться в due
	DependsOn []string `json:"depends_on"`
}

// Build расставляет задачи: без даты начала задача начинается на следующий день
// после окончания всех своих зависимостей, а без них — сегодня (или в день due);
// без due длится один день. Цикл зависимостей — ошибка: расписания у него нет.
func Build(tasks []Task, today time.Time) ([]Bar, []string, error) {
	today = day(today)
	byID := make(map[string]*Task, len(tasks))
	for i := range tasks {
		byID[tasks[i].ID] = &tasks[i]
	}

	var warnings []string
	deps := make(map[string][]string, len(tasks))
	indegree := make(map[string]int, len(tasks))
	dependents := map[string][]string{}
	for _, t := range tasks {
		indegree[t.ID] += 0
		for _, d := range t.DependsOn {
			if _, ok := byID[d]; !ok {
				warnings = append(warnings, fmt.Sprintf("«%s» зависит от заметки вне диаграммы (%s)", t.Title, short(d)))
				continue
			}
			deps[t.ID] = append(deps[t.ID], d)
			dependents[d] = append(dependents[d], t.ID)
			indegree[t.ID]++
		}
	}

	// Топологический порядок (Kahn); среди готовых — в порядке входа, чтобы
	// результат не зависел от обхода map.
	var order []string
	ready := []string{}
	for _, t := range tasks {
		if indegree[t.ID] == 0 {
			ready = append(ready, t.ID)
		}
	}
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		for _, next := range dependents[id] {
			if indegree[next]--; indegree[next] == 0 {
				ready = append(ready, next)
			}
		}
	}
	if len(order) < len(tasks) {
		var cycle []string
		for _, t := range tasks {
			if indegree[t.ID] > 0 {
				cycle = append(cycle, "«"+t.Title+"»")
			}
		}
		return nil, nil, fmt.Errorf("цикл зависимостей: %s", strings.Join(cycle, ", "))
	}

	ends := make(map[string]time.Time, len(tasks))
	bars := make([]Bar, 0, len(tasks))
	for _, id := range order {
		t := byID[id]
		b := Bar{ID: t.ID, Title: t.Title, Status: t.Status, DependsOn: deps[id]}
		if b.DependsOn == nil {
			b.DependsOn = []string{}
		}

		var depEnd time.Time
		var lastDep string
		for _, d := range deps[id] {
			if ends[d].After(depEnd) {
				depEnd, lastDep = ends[d], d
			}
		}

		var start time.Time
		switch {
		case t.Start != nil:
			start = day(*t.Start)
			if !depEnd.IsZero() && !start.After(depEnd) {
				warnings = append(warnings, fmt.Sprintf("«%s» начинается %s, а «%s» заканчивается только %s",
					t.Title, start.Format(layout), byID[lastDep].Title, depEnd.Format(layout)))
			}
		case !depEnd.IsZero():
			start, b.Scheduled = depEnd.AddDate(0, 0, 1), true
		case t.Due != nil:
			start, b.Scheduled = day(*t.Due), true
		default:
			start, b.Scheduled = today, true
		}

		end := start
		if t.Due != nil {
			end = day(*t.Due)
			if end.Before(start) {
				b.Late = true
				warnings = append(warnings, fmt.Sprintf("«%s» не успевает к сроку %s: по зависимостям начнётся %s",
					t.Title, end.Format(layout), start.Format(layout)))
				end = start
			}
		}
		ends[id] = end
		b.Start, b.End = start.Format(layout), end.Format(layout)
		bars = append(bars, b)
	}

	sort.SliceStable(bars, func(i, j int) bool { return bars[i].Start < bars[j].Start })
	return bars, warnings, nil
}

// Mermaid возвращает диаграмму в синтаксисе Mermaid gantt.
func Mermaid(title string, bars []Bar) string {
	var sb strings.Builder
	sb.WriteString("gantt\n")
	if title != "" {
		fmt.Fprintf(&sb, "    title %s\n", clean(title))
	}
	sb.WriteString("    dateFormat YYYY-MM-DD\n    axisFormat %d.%m\n")
	if title != "" {
		fmt.Fprintf(&sb, "    section %s\n", clean(title))
	}
	for i, b := range bars {
		var tags []string
		if b.Late {
			tags = append(tags, "crit")
		}
		switch b.Status {
		case "done":
			tags = append(tags, "done")
		case "active":
			tags = append(tags, "active")
		}
		tags = append(tags, fmt.Sprintf("t%d", i+1))
		end, _ := time.Parse(layout, b.End)
		// В Mermaid дата окончания не входит в задачу, у нас — входит.
		fmt.Fprintf(&sb, "    %s :%s, %s, %s\n", clean(b.Title), strings.Join(tags, ", "), b.Start, end.AddDate(0, 0, 1).Format(layout))
	}
	return sb.String()
}

// Text рисует полосы для терминала шириной примерно width символов.
func Text(bars []Bar, width int) string {
	if len(bars) == 0 {
		return "нет задач с датами или зависимостями\n"
	}
	const labelW = 28
	first, _ := time.Parse(layout, bars[0].Start)
	last := first
	for _, b := range bars {
		s, _ := time.Parse(layout, b.Start)
		e, _ := time.Parse(layout, b.End)
		if s.Before(first) {
			first = s
		}
		if e.After(last) {
			last = e
		}
	}
	days := int(last.Sub(first).Hours()/24) + 1
	cols := width - labelW - 2
	if cols < 10 {
		cols = 10
	}
	per := int(math.Ceil(float64(days) / float64(cols))) // дней в символе
	if per < 1 {
		per = 1
	}
	cols = (days + per - 1) / per

	var sb strings.Builder
	// Шкала: дата под каждой десятой позицией.
	scale := []rune(strings.Repeat(" ", cols+6))
	for c := 0; c < cols; c += 10 {
		label := []rune(first.AddDate(0, 0, c*per).Format("02.01"))
		copy(scale[c:], label)
	}
	fmt.Fprintf(&sb, "%s  %s\n", pad("", labelW), strings.TrimRight(string(scale), " "))

	for _, b := range bars {
		s, _ := time.Parse(layout, b.Start)
		e, _ := time.Parse(layout, b.End)
		from := int(s.Sub(first).Hours()/24) / per
		to := int(e.Sub(first).Hours()/24) / per
		fill := "█"
		if b.Status == "done" {
			fill = "░"
		}
		line := strings.Repeat(" ", from) + strings.Repeat(fill, to-from+1)
		note := " " + dm(s)
		if e.After(s) {
			note += "–" + dm(e)
		}
		if b.Scheduled {
			note += " авто"
		}
		if b.Late {
			note += " ⚠ срок"
		}
		fmt.Fprintf(&sb, "%s  %s%s\n", pad(b.Title, labelW), line, note)
	}
	if per > 1 {
		fmt.Fprintf(&sb, "%s  один символ — %d дн.\n", pad("", labelW), per)
	}
	return sb.String()
}

func day(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC) }

func dm(t time.Time) string { return t.Format("02.01") }

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// clean убирает из названия символы, которые Mermaid читает как разметку.
func clean(s string) string {
	s = strings.NewReplacer(":", " ", "#", "", ";", ",", "\n", " ").Replace(s)
	return strings.TrimSpace(s)
}

func pad(s string, w int) string {
	if utf8.RuneCountInString(s) > w {
		return string([]rune(s)[:w-1]) + "…"
	}
	return s + strings.Repeat(" ", w-utf8.RuneCountInString(s))
}
