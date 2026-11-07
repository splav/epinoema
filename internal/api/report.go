package api

import (
	"fmt"
	"regexp"
	"slices"
)

// Допустимые значения повторяют CHECK в схеме notes.
var (
	Kinds    = []string{"note", "idea", "decision", "meeting", "project"}
	Statuses = []string{"inbox", "active", "parked", "done", "dropped"}
	Orders   = []string{"updated", "created", "importance"}
)

// Filter — условия одной секции отчёта. Пустое поле — без ограничения.
type Filter struct {
	Kind          []string `json:"kind,omitempty" jsonschema:"типы: note, idea, decision, meeting, project"`
	Status        []string `json:"status,omitempty" jsonschema:"статусы: inbox, active, parked, done, dropped"`
	Project       *string  `json:"project,omitempty" jsonschema:"проект"`
	TagsAny       []string `json:"tags_any,omitempty" jsonschema:"хотя бы один из тегов"`
	ImportanceGte *int16   `json:"importance_gte,omitempty" jsonschema:"важность не ниже, 1–3"`
	StaleDays     *int32   `json:"stale_days,omitempty" jsonschema:"не менялись дольше стольких дней"`
	ChangedDays   *int32   `json:"changed_days,omitempty" jsonschema:"менялись за последние столько дней"`
	Order         string   `json:"order,omitempty" jsonschema:"порядок: updated (по умолчанию), created, importance"`
	Limit         int32    `json:"limit,omitempty" jsonschema:"сколько заметок в секции, по умолчанию 50"`
}

type Section struct {
	Title  string `json:"title"`
	Filter Filter `json:"filter"`
}

// ReportSpec — то, что хранится в reports.spec. Две формы: плоский фильтр
// {"kind":["idea"],"stale_days":14} или {"sections":[{"title":…,"filter":…}]}.
type ReportSpec struct {
	Sections []Section `json:"sections,omitempty" jsonschema:"секции отчёта; если не заданы, весь spec — один фильтр"`
	Filter
}

// Normalized всегда возвращает список секций.
func (s ReportSpec) Normalized() []Section {
	if len(s.Sections) > 0 {
		return s.Sections
	}
	return []Section{{Filter: s.Filter}}
}

func (s ReportSpec) Validate() error {
	if len(s.Sections) > 0 && !s.Filter.isZero() {
		return fmt.Errorf("spec: либо sections, либо фильтр на верхнем уровне")
	}
	for i, sec := range s.Normalized() {
		if err := sec.Filter.Validate(); err != nil {
			if len(s.Sections) > 0 {
				return fmt.Errorf("section %d: %w", i+1, err)
			}
			return err
		}
	}
	return nil
}

func (f Filter) isZero() bool {
	return len(f.Kind) == 0 && len(f.Status) == 0 && f.Project == nil && len(f.TagsAny) == 0 &&
		f.ImportanceGte == nil && f.StaleDays == nil && f.ChangedDays == nil && f.Order == "" && f.Limit == 0
}

func (f Filter) Validate() error {
	for _, k := range f.Kind {
		if !slices.Contains(Kinds, k) {
			return fmt.Errorf("kind %q: допустимы %v", k, Kinds)
		}
	}
	for _, st := range f.Status {
		if !slices.Contains(Statuses, st) {
			return fmt.Errorf("status %q: допустимы %v", st, Statuses)
		}
	}
	if f.Order != "" && !slices.Contains(Orders, f.Order) {
		return fmt.Errorf("order %q: допустимы %v", f.Order, Orders)
	}
	if f.ImportanceGte != nil && (*f.ImportanceGte < 1 || *f.ImportanceGte > 3) {
		return fmt.Errorf("importance_gte: 1–3")
	}
	for name, d := range map[string]*int32{"stale_days": f.StaleDays, "changed_days": f.ChangedDays} {
		if d != nil && (*d < 0 || *d > 3650) {
			return fmt.Errorf("%s: 0–3650", name)
		}
	}
	if f.Limit < 0 || f.Limit > 500 {
		return fmt.Errorf("limit: 1–500")
	}
	return nil
}

var reportName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

func ValidReportName(name string) bool { return reportName.MatchString(name) }

// Report — элемент GET /v1/reports.
type Report struct {
	Name    string     `json:"name"`
	Builtin bool       `json:"builtin"`
	Spec    ReportSpec `json:"spec"`
}

type ReportList struct {
	Reports []Report `json:"reports"`
}

type SectionResult struct {
	Title string `json:"title"`
	Notes []Note `json:"notes"`
}

// ReportResult — ответ GET /v1/reports/{name}/run.
type ReportResult struct {
	Name     string          `json:"name"`
	Sections []SectionResult `json:"sections"`
}
