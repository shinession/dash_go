package hcscan

import (
	"context"
	"time"
)

// Repository is the data access of the module. The PostgreSQL implementation reads the Auth database; the tests
// use an in-memory fake.
type Repository interface {
	// Machines returns the visible rows of t_mqpd_hc_table of the processes, ordered by display_seq.
	Machines(ctx context.Context, processes []string) ([]Machine, error)
	// MTR returns the proficiency matrix of the processes (latest version of every checkpoint row, hidden rows
	// removed, the highest level per employee and machine).
	MTR(ctx context.Context, processes []string) (MTRData, error)
	// EmployeeMTR returns all visible proficiency records (Lv1+) of one employee across every process,
	// using the same latest/visible/highest-level reduction as MTR.
	EmployeeMTR(ctx context.Context, ee string) ([]MTRRecord, error)
	// CalendarDay returns the day type of every shift column on the date, or nil when the calendar has no row.
	CalendarDay(ctx context.Context, date time.Time) (map[string]string, error)
	// MESSnapshot returns the MES employee rows of the month that applies to the date.
	MESSnapshot(ctx context.Context, date time.Time) ([]MESRow, error)
	// MESPerson returns the newest MES row of one employee, or nil.
	MESPerson(ctx context.Context, ee string) (*MESRow, error)
	// Members returns the membership ranges valid on the date.
	Members(ctx context.Context, date time.Time) ([]MemberRow, error)
	// DayRecords returns the visible t_daily_attn_data rows of one date.
	DayRecords(ctx context.Context, date time.Time) ([]DayRecord, error)
	// History returns the visible rows of the employees for dates from <= date < to.
	History(ctx context.Context, ees []string, from, to time.Time) ([]DayRecord, error)
	// WriteDay locks and reads the current rows of the employees on the date, lets plan build the new rows from
	// them and upserts the result in one transaction. It returns the written rows.
	WriteDay(ctx context.Context, date time.Time, ees []string, user string, plan func(current map[string]DayRecord) ([]DayRecord, error)) ([]DayRecord, error)
}

// ChangeLog tells the Python application that t_daily_attn_data changed. The Python readers compare the
// modification time of DailyAttnData.log with the time of their Redis cache, so appending a line refreshes them.
type ChangeLog interface {
	Append(user string, rows []DayRecord) error
}
