package api

import (
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

const DateLayout = "2006-01-02"

// Date — календарная дата без времени и часового пояса, "YYYY-MM-DD" и в JSON,
// и в Go. pgx читает в неё колонку date (в *string он её не читает).
type Date string

func (d *Date) ScanDate(v pgtype.Date) error {
	if !v.Valid {
		return fmt.Errorf("NULL date into api.Date: use *api.Date")
	}
	if v.InfinityModifier != pgtype.Finite {
		return fmt.Errorf("infinite date is not supported")
	}
	*d = Date(v.Time.Format(DateLayout))
	return nil
}

func (d Date) Time() (time.Time, error) { return time.Parse(DateLayout, string(d)) }
