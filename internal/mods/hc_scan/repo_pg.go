package hcscan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgRepo reads and writes the Auth database. All SQL is parameterised. Tables whose full column list is not
// needed (t_mes_ee, t_daily_attn_member, t_daily_attn_calendar) are read as JSON documents, so a missing optional
// column gives an empty value instead of a failing query.
type pgRepo struct {
	pool *pgxpool.Pool
}

// NewPGRepository returns the PostgreSQL implementation of Repository.
func NewPGRepository(pool *pgxpool.Pool) Repository { return &pgRepo{pool: pool} }

func (r *pgRepo) Machines(ctx context.Context, processes []string) ([]Machine, error) {
	rows, err := r.pool.Query(ctx, `
SELECT id::text, process, machine, "type", ROUND(COALESCE(budget_head_count, 0)::numeric, 4)::float8, COALESCE(display_seq, 0)::float8
FROM (
    SELECT DISTINCT ON (id) id, process, machine, "type", budget_head_count, display_seq, hidden
    FROM t_mqpd_hc_table
    ORDER BY id, update_time DESC NULLS LAST
) t
WHERE COALESCE(hidden, false) = false AND process = ANY($1)
ORDER BY display_seq NULLS LAST, id`, processes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Machine
	for rows.Next() {
		var m Machine
		var name, typ *string
		if err := rows.Scan(&m.ID, &m.Process, &name, &typ, &m.Budget, &m.Seq); err != nil {
			return nil, err
		}
		m.Name, m.Type = deref(name), deref(typ)
		if strings.TrimSpace(m.Name) == "" {
			m.Name = m.ID
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

const mtrLatestCTE = `
WITH latest AS (
    SELECT DISTINCT ON (row_id) emp_code, checkpoint_key, cim_machine_id, machine, proficiency_level, updated_at, is_hidden
    FROM t_mtr_checkpoint
    WHERE cim_process = ANY($1)
    ORDER BY row_id, updated_at DESC NULLS LAST
), visible AS (
    SELECT * FROM latest WHERE COALESCE(is_hidden, false) = false AND COALESCE(cim_machine_id, '') <> ''
), newest AS (
    SELECT DISTINCT ON (emp_code, checkpoint_key, cim_machine_id) emp_code, cim_machine_id, btrim(machine) AS machine,
           CASE WHEN proficiency_level::text ~ '^\s*[0-9]+(\.[0-9]+)?\s*$' THEN proficiency_level::text::numeric END AS level
    FROM visible
    ORDER BY emp_code, checkpoint_key, cim_machine_id, updated_at DESC NULLS LAST
)`

// MTR reproduces the matrix of _sync_mtr_checkpoint(): the latest version of every checkpoint row, hidden rows
// removed, then the newest row per employee, checkpoint and machine, and the highest level per employee and machine.
func (r *pgRepo) MTR(ctx context.Context, processes []string) (MTRData, error) {
	data := MTRData{Levels: map[string]map[string]int{}, NameToCIM: map[string]string{}}
	rows, err := r.pool.Query(ctx, mtrLatestCTE+`
SELECT emp_code, cim_machine_id, max(machine), max(level)::float8
FROM newest GROUP BY emp_code, cim_machine_id ORDER BY cim_machine_id, emp_code`, processes)
	if err != nil {
		return data, err
	}
	defer rows.Close()
	for rows.Next() {
		var ee, cim string
		var machine *string
		var level *float64
		if err := rows.Scan(&ee, &cim, &machine, &level); err != nil {
			return data, err
		}
		if name := strings.TrimSpace(deref(machine)); name != "" {
			if _, dup := data.NameToCIM[name]; !dup {
				data.NameToCIM[name] = cim
			}
		}
		if level == nil {
			continue
		}
		ee = strings.ToUpper(strings.TrimSpace(ee))
		if data.Levels[ee] == nil {
			data.Levels[ee] = map[string]int{}
		}
		data.Levels[ee][cim] = int(*level + 0.5)
	}
	if err := rows.Err(); err != nil {
		return data, err
	}
	var newest *string
	if err := r.pool.QueryRow(ctx, `SELECT max(updated_at)::text FROM t_mtr_checkpoint`).Scan(&newest); err == nil {
		data.DataTime = parseLooseTime(deref(newest))
	}
	return data, nil
}

const employeeMTRSQL = `
WITH latest AS (
    SELECT DISTINCT ON (row_id) emp_code, checkpoint_key, cim_process, cim_machine_id, machine, proficiency_level, updated_at, is_hidden
    FROM t_mtr_checkpoint
    WHERE upper(btrim(emp_code)) = $1
    ORDER BY row_id, updated_at DESC NULLS LAST
), visible AS (
    SELECT * FROM latest WHERE COALESCE(is_hidden, false) = false AND COALESCE(cim_machine_id, '') <> ''
), newest AS (
    SELECT DISTINCT ON (emp_code, checkpoint_key, cim_process, cim_machine_id)
           COALESCE(btrim(cim_process), '') AS cim_process,
           cim_machine_id,
           btrim(machine) AS machine,
           CASE WHEN proficiency_level::text ~ '^\s*[0-9]+(\.[0-9]+)?\s*$' THEN proficiency_level::text::numeric END AS level
    FROM visible
    ORDER BY emp_code, checkpoint_key, cim_process, cim_machine_id, updated_at DESC NULLS LAST
)
SELECT cim_process, cim_machine_id, max(machine), max(level)::float8
FROM newest
GROUP BY cim_process, cim_machine_id
ORDER BY cim_process, max(level) DESC NULLS LAST, max(machine), cim_machine_id`

// EmployeeMTR returns all visible MTR records (Lv1+) of one employee across every process.
func (r *pgRepo) EmployeeMTR(ctx context.Context, ee string) ([]MTRRecord, error) {
	ee = strings.ToUpper(strings.TrimSpace(ee))
	if ee == "" {
		return []MTRRecord{}, nil
	}
	rows, err := r.pool.Query(ctx, employeeMTRSQL, ee)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MTRRecord{}
	for rows.Next() {
		var proc, cim string
		var machine *string
		var level *float64
		if err := rows.Scan(&proc, &cim, &machine, &level); err != nil {
			return nil, err
		}
		if level == nil {
			continue
		}
		lv := int(*level + 0.5)
		if lv < 1 {
			continue
		}
		out = append(out, MTRRecord{
			Process:   strings.TrimSpace(proc),
			MachineID: strings.TrimSpace(cim),
			Machine:   firstNonEmpty(strings.TrimSpace(deref(machine)), strings.TrimSpace(cim)),
			Level:     lv,
		})
	}
	return out, rows.Err()
}

func (r *pgRepo) CalendarDay(ctx context.Context, date time.Time) (map[string]string, error) {
	var raw []byte
	err := r.pool.QueryRow(ctx, `SELECT to_jsonb(c) FROM t_daily_attn_calendar c WHERE prodn_date = $1 ORDER BY update_time DESC NULLS LAST LIMIT 1`, date).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for k, v := range doc {
		col := strings.ToUpper(k)
		if s, ok := v.(string); ok && hcCalendarColumn(col) {
			out[col] = strings.TrimSpace(s)
		}
	}
	return out, nil
}

func (r *pgRepo) MESSnapshot(ctx context.Context, date time.Time) ([]MESRow, error) {
	// hc_month: the first day of the month of the day after the date, which is the month key of the Python grid
	next := date.AddDate(0, 0, 1)
	hcMonth := time.Date(next.Year(), next.Month(), 1, 0, 0, 0, 0, time.UTC)
	rows, err := r.pool.Query(ctx, `
SELECT to_jsonb(t) FROM t_mes_ee t
WHERE "group" = $2 AND effective_mth = (SELECT max(effective_mth) FROM t_mes_ee WHERE effective_mth <= $1)`, hcMonth, mesGroupCode)
	if err != nil {
		return nil, err
	}
	return collectDocs(rows, mesRowFromDoc)
}

func (r *pgRepo) MESPerson(ctx context.Context, ee string) (*MESRow, error) {
	rows, err := r.pool.Query(ctx, `SELECT to_jsonb(t) FROM t_mes_ee t WHERE upper(ee_code) = $1 ORDER BY effective_mth DESC NULLS LAST LIMIT 1`, strings.ToUpper(ee))
	if err != nil {
		return nil, err
	}
	list, err := collectDocs(rows, mesRowFromDoc)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return &list[0], nil
}

func (r *pgRepo) Members(ctx context.Context, date time.Time) ([]MemberRow, error) {
	rows, err := r.pool.Query(ctx, `
SELECT to_jsonb(t) FROM t_daily_attn_member t
WHERE COALESCE(hidden, false) = false AND effective_date <= $1 AND COALESCE(end_date, DATE '2099-12-31') >= $1`, date)
	if err != nil {
		return nil, err
	}
	return collectDocs(rows, memberRowFromDoc)
}

const dayRecordColumns = `uid, ee_code, prodn_date, "shift", schedule_shift, actual_shift, machine_hc_json::text, remark,
    ROUND(COALESCE(build_in_ot, 0)::numeric, 2)::float8, ROUND(COALESCE(addt_ot, 0)::numeric, 2)::float8, ROUND(COALESCE(total_ot, 0)::numeric, 2)::float8`

func (r *pgRepo) DayRecords(ctx context.Context, date time.Time) ([]DayRecord, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+dayRecordColumns+` FROM t_daily_attn_data WHERE prodn_date = $1 AND COALESCE(hidden, false) = false`, date)
	if err != nil {
		return nil, err
	}
	return scanDayRecords(rows)
}

func (r *pgRepo) History(ctx context.Context, ees []string, from, to time.Time) ([]DayRecord, error) {
	if len(ees) == 0 {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT `+dayRecordColumns+` FROM t_daily_attn_data
WHERE ee_code = ANY($1) AND prodn_date >= $2 AND prodn_date < $3 AND COALESCE(hidden, false) = false`, ees, from, to)
	if err != nil {
		return nil, err
	}
	return scanDayRecords(rows)
}

const upsertDaySQL = `
INSERT INTO t_daily_attn_data (uid, ee_code, prodn_date, "shift", actual_shift, schedule_shift, machine_hc_json, remark, build_in_ot, addt_ot, total_ot, update_time, update_user, hidden)
VALUES ($1, $2, $3::date, $4, $5, $6, $7::jsonb, $8, $9, $10, $11, $12::timestamp, $13, false)
ON CONFLICT (uid) DO UPDATE SET ee_code = EXCLUDED.ee_code, prodn_date = EXCLUDED.prodn_date, "shift" = EXCLUDED."shift",
    actual_shift = EXCLUDED.actual_shift, schedule_shift = EXCLUDED.schedule_shift, machine_hc_json = EXCLUDED.machine_hc_json,
    remark = EXCLUDED.remark, build_in_ot = EXCLUDED.build_in_ot, addt_ot = EXCLUDED.addt_ot, total_ot = EXCLUDED.total_ot,
    update_time = EXCLUDED.update_time, update_user = EXCLUDED.update_user, hidden = EXCLUDED.hidden`

func (r *pgRepo) WriteDay(ctx context.Context, date time.Time, ees []string, user string, plan func(map[string]DayRecord) ([]DayRecord, error)) ([]DayRecord, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	uids := make([]string, len(ees))
	for i, ee := range ees {
		uids[i] = fmt.Sprintf("%s_%s", ee, date.Format("20060102"))
	}
	rows, err := tx.Query(ctx, `SELECT `+dayRecordColumns+`, COALESCE(hidden, false) FROM t_daily_attn_data WHERE uid = ANY($1) ORDER BY uid FOR UPDATE`, uids)
	if err != nil {
		return nil, err
	}
	current := map[string]DayRecord{}
	for rows.Next() {
		rec, hidden, err := scanDayRecordRow(rows, true)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if !hidden {
			current[strings.ToUpper(rec.EE)] = rec
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out, err := plan(current)
	if err != nil {
		return nil, err
	}
	stamp := time.Now().Format("2006-01-02 15:04:05.999999")
	for _, rec := range out {
		payload, err := json.Marshal(rec.Entries)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, upsertDaySQL, rec.UID, rec.EE, rec.Date.Format("2006-01-02"), rec.Shift, rec.Actual, rec.Schedule,
			string(payload), rec.Remark, rec.BuildIn, rec.AddtOT, rec.TotalOT, stamp, user); err != nil {
			return nil, fmt.Errorf("upsert %s: %w", rec.UID, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// ---- row helpers ----

func scanDayRecords(rows pgx.Rows) ([]DayRecord, error) {
	defer rows.Close()
	var out []DayRecord
	for rows.Next() {
		rec, _, err := scanDayRecordRow(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func scanDayRecordRow(rows pgx.Rows, withHidden bool) (DayRecord, bool, error) {
	var rec DayRecord
	var shift, schedule, actual, mhc, remark *string
	var hidden bool
	dest := []any{&rec.UID, &rec.EE, &rec.Date, &shift, &schedule, &actual, &mhc, &remark, &rec.BuildIn, &rec.AddtOT, &rec.TotalOT}
	if withHidden {
		dest = append(dest, &hidden)
	}
	if err := rows.Scan(dest...); err != nil {
		return rec, false, err
	}
	rec.Date = DateOnly(rec.Date)
	rec.EE = strings.ToUpper(strings.TrimSpace(rec.EE))
	rec.Shift, rec.Schedule, rec.Actual, rec.Remark = strings.TrimSpace(deref(shift)), deref(schedule), deref(actual), remark // shift is a fixed width column
	if mhc != nil && strings.TrimSpace(*mhc) != "" {
		if err := json.Unmarshal([]byte(*mhc), &rec.Entries); err != nil {
			return rec, false, fmt.Errorf("machine_hc_json of %s is not valid: %w", rec.UID, err)
		}
	}
	return rec, hidden, nil
}

func collectDocs[T any](rows pgx.Rows, convert func(map[string]any) (T, bool)) ([]T, error) {
	defer rows.Close()
	var out []T
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, err
		}
		if v, ok := convert(doc); ok {
			out = append(out, v)
		}
	}
	return out, rows.Err()
}

func mesRowFromDoc(doc map[string]any) (MESRow, bool) {
	r := MESRow{
		EE: docStr(doc, "ee_code"), Name: docStr(doc, "ee_name"), CostCenter: docStr(doc, "cost_center"), Section: docStr(doc, "section"),
		Division: docStr(doc, "division"), DirectType: docStr(doc, "direct_type"), Shift: docStr(doc, "shift"),
	}
	if t, ok := docDate(doc, "resign_date"); ok {
		r.ResignDate = &t
	}
	r.EffectiveMth, _ = docDate(doc, "effective_mth")
	return r, strings.TrimSpace(r.EE) != ""
}

func memberRowFromDoc(doc map[string]any) (MemberRow, bool) {
	m := MemberRow{
		EE: docStr(doc, "ee_code"), Name: docStr(doc, "ee_name"), Process: docStr(doc, "process"), Direct: docStr(doc, "direct_type"), Shift: docStr(doc, "shift"),
	}
	m.Effective, _ = docDate(doc, "effective_date")
	var ok bool
	if m.End, ok = docDate(doc, "end_date"); !ok {
		m.End = time.Date(2099, 12, 31, 0, 0, 0, 0, time.UTC)
	}
	return m, strings.TrimSpace(m.EE) != ""
}

func docStr(doc map[string]any, key string) string {
	switch v := doc[key].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return ""
}

func docDate(doc map[string]any, key string) (time.Time, bool) {
	s := docStr(doc, key)
	if len(s) < 10 {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02", s[:10])
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func parseLooseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02 15:04:05.999999", "2006-01-02T15:04:05.999999", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
