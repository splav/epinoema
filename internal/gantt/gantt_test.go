package gantt

import (
	"strings"
	"testing"
	"time"
)

func d(s string) *time.Time {
	t, err := time.Parse(layout, s)
	if err != nil {
		panic(err)
	}
	return &t
}

var today = *d("2026-11-07")

func byTitle(bars []Bar) map[string]Bar {
	m := map[string]Bar{}
	for _, b := range bars {
		m[b.Title] = b
	}
	return m
}

func TestScheduleAfterDependencies(t *testing.T) {
	bars, warnings, err := Build([]Task{
		{ID: "c", Title: "запуск", DependsOn: []string{"a", "b"}},
		{ID: "a", Title: "схема", Start: d("2026-11-10"), Due: d("2026-11-12")},
		{ID: "b", Title: "API", Start: d("2026-11-10"), Due: d("2026-11-14")},
		{ID: "e", Title: "без дат"},
		{ID: "f", Title: "только срок", Due: d("2026-11-20")},
	}, today)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("err=%v warnings=%v", err, warnings)
	}
	m := byTitle(bars)
	// Запуск — на следующий день после самой поздней зависимости.
	if b := m["запуск"]; b.Start != "2026-11-15" || b.End != "2026-11-15" || !b.Scheduled {
		t.Errorf("запуск: %+v", b)
	}
	if b := m["без дат"]; b.Start != "2026-11-07" || !b.Scheduled {
		t.Errorf("без дат: %+v", b)
	}
	if b := m["только срок"]; b.Start != "2026-11-20" || b.End != "2026-11-20" {
		t.Errorf("только срок: %+v", b)
	}
	if bars[0].Title != "без дат" {
		t.Errorf("порядок по началу: первая %q", bars[0].Title)
	}
}

func TestChainPropagates(t *testing.T) {
	bars, _, _ := Build([]Task{
		{ID: "a", Title: "a", Start: d("2026-11-10"), Due: d("2026-11-11")},
		{ID: "b", Title: "b", Due: d("2026-11-14"), DependsOn: []string{"a"}},
		{ID: "c", Title: "c", DependsOn: []string{"b"}},
	}, today)
	m := byTitle(bars)
	if m["b"].Start != "2026-11-12" || m["b"].End != "2026-11-14" || m["c"].Start != "2026-11-15" {
		t.Errorf("цепочка: b=%+v c=%+v", m["b"], m["c"])
	}
}

func TestLateAndConflicts(t *testing.T) {
	bars, warnings, err := Build([]Task{
		{ID: "a", Title: "долгая", Start: d("2026-11-10"), Due: d("2026-11-20")},
		{ID: "b", Title: "срочная", Due: d("2026-11-15"), DependsOn: []string{"a"}},
		{ID: "c", Title: "рано", Start: d("2026-11-12"), DependsOn: []string{"a"}},
		{ID: "x", Title: "внешняя", DependsOn: []string{"not-in-set-123"}},
	}, today)
	if err != nil {
		t.Fatal(err)
	}
	m := byTitle(bars)
	if b := m["срочная"]; !b.Late || b.Start != "2026-11-21" || b.End != "2026-11-21" {
		t.Errorf("срочная: %+v", b)
	}
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{"«срочная» не успевает к сроку 2026-11-15", "«рано» начинается 2026-11-12", "«внешняя» зависит от заметки вне диаграммы (not-in-s)"} {
		if !strings.Contains(joined, want) {
			t.Errorf("нет предупреждения %q в:\n%s", want, joined)
		}
	}
}

func TestCycle(t *testing.T) {
	_, _, err := Build([]Task{
		{ID: "a", Title: "a", DependsOn: []string{"b"}},
		{ID: "b", Title: "b", DependsOn: []string{"a"}},
		{ID: "c", Title: "c"},
	}, today)
	if err == nil || !strings.Contains(err.Error(), "«a», «b»") || strings.Contains(err.Error(), "«c»") {
		t.Errorf("цикл: %v", err)
	}
}

func TestMermaid(t *testing.T) {
	bars, _, _ := Build([]Task{
		{ID: "a", Title: "Схема: v2 #1", Status: "done", Start: d("2026-11-10"), Due: d("2026-11-12")},
		{ID: "b", Title: "API", Status: "active", Due: d("2026-11-11"), DependsOn: []string{"a"}},
	}, today)
	got := Mermaid("epi", bars)
	for _, want := range []string{
		"gantt\n    title epi\n    dateFormat YYYY-MM-DD",
		"    Схема  v2 1 :done, t1, 2026-11-10, 2026-11-13\n", // конец в Mermaid не включается
		"    API :crit, active, t2, 2026-11-13, 2026-11-14\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("нет %q в:\n%s", want, got)
		}
	}
}

func TestText(t *testing.T) {
	bars, _, _ := Build([]Task{
		{ID: "a", Title: "первая", Start: d("2026-11-10"), Due: d("2026-11-12")},
		{ID: "b", Title: "вторая", DependsOn: []string{"a"}},
	}, today)
	got := Text(bars, 80)
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], strings.Repeat(" ", 30)+"10.11") {
		t.Fatalf("Text:\n%s", got)
	}
	if !strings.Contains(lines[1], "███ 10.11–12.11") || !strings.Contains(lines[2], "   █ 13.11 авто") {
		t.Errorf("полосы:\n%s", got)
	}
}
