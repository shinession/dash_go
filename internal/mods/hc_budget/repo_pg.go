package hcbudget

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgRepo reads and writes t_mqpd_hc_table in the Auth database. All SQL is parameterized.
type pgRepo struct {
	pool *pgxpool.Pool
}

// NewPGRepository returns the PostgreSQL implementation of Repository.
func NewPGRepository(pool *pgxpool.Pool) Repository { return &pgRepo{pool: pool} }

const selectProcessRowsSQL = `
SELECT id::text,
       COALESCE(process, ''),
       btrim(COALESCE(cost_center, '')),
       process_code,
       COALESCE("type", ''),
       station_code,
       COALESCE(machine, ''),
       COALESCE(budget_head_count, 0)::real::text::float8,
       COALESCE(category, ''),
       display_seq,
       COALESCE(hidden, false),
       update_time,
       COALESCE(update_user, '')
FROM t_mqpd_hc_table
WHERE process = $1
ORDER BY display_seq ASC NULLS LAST, id ASC`

func (r *pgRepo) ProcessRows(ctx context.Context, process string) ([]Row, error) {
	rows, err := r.pool.Query(ctx, selectProcessRowsSQL, process)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRows(rows)
}

func (r *pgRepo) Apply(ctx context.Context, process string, candidateIDs []string, plan CommitPlan) ([]Row, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock all rows of this process so concurrent edits and sequence updates serialize cleanly.
	lockedRows, err := tx.Query(ctx, selectProcessRowsSQL+` FOR UPDATE`, process)
	if err != nil {
		return nil, err
	}
	existing, err := scanRows(lockedRows)
	lockedRows.Close()
	if err != nil {
		return nil, err
	}

	// Check global uniqueness of any candidate new IDs across the whole table.
	takenIDs := map[string]bool{}
	if len(candidateIDs) > 0 {
		idRows, err := tx.Query(ctx, `SELECT id::text FROM t_mqpd_hc_table WHERE id = ANY($1)`, candidateIDs)
		if err != nil {
			return nil, err
		}
		for idRows.Next() {
			var id string
			if err := idRows.Scan(&id); err != nil {
				idRows.Close()
				return nil, err
			}
			takenIDs[id] = true
		}
		idErr := idRows.Err()
		idRows.Close()
		if idErr != nil {
			return nil, idErr
		}
	}

	inserts, updates, err := plan(existing, takenIDs)
	if err != nil {
		return nil, err
	}

	const insertSQL = `
INSERT INTO t_mqpd_hc_table (
    id, process, cost_center, process_code, "type", station_code,
    machine, budget_head_count, update_time, update_user,
    category, display_seq, hidden
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8::real, $9::timestamp, $10,
    $11, $12, $13
)`

	const updateSQL = `
UPDATE t_mqpd_hc_table
SET machine = $2,
    "type" = $3,
    category = $4,
    budget_head_count = $5::real,
    display_seq = $6,
    hidden = $7,
    update_time = $8::timestamp,
    update_user = $9
WHERE id = $1 AND process = $10`

	written := make([]Row, 0, len(inserts)+len(updates))
	for _, row := range inserts {
		ts := formatPGTimestamp(row.UpdateTime)
		_, err := tx.Exec(ctx, insertSQL,
			row.ID,
			row.Process,
			nullableString(row.CostCenter),
			row.ProcessCode,
			row.Type,
			row.StationCode,
			row.Machine,
			row.BudgetHC,
			ts,
			row.UpdateUser,
			nullableString(row.Category),
			row.DisplaySeq,
			row.Hidden,
		)
		if err != nil {
			return nil, fmt.Errorf("insert %s: %w", row.ID, err)
		}
		written = append(written, row)
	}
	for _, row := range updates {
		ts := formatPGTimestamp(row.UpdateTime)
		tag, err := tx.Exec(ctx, updateSQL,
			row.ID,
			row.Machine,
			row.Type,
			nullableString(row.Category),
			row.BudgetHC,
			row.DisplaySeq,
			row.Hidden,
			ts,
			row.UpdateUser,
			row.Process,
		)
		if err != nil {
			return nil, fmt.Errorf("update %s: %w", row.ID, err)
		}
		if tag.RowsAffected() != 1 {
			return nil, fmt.Errorf("update %s: expected 1 affected row, got %d", row.ID, tag.RowsAffected())
		}
		written = append(written, row)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return written, nil
}

func scanRows(rows pgx.Rows) ([]Row, error) {
	out := []Row{}
	for rows.Next() {
		var row Row
		var procCode, stationCode, displaySeq *int16
		var updateTime *time.Time
		if err := rows.Scan(
			&row.ID,
			&row.Process,
			&row.CostCenter,
			&procCode,
			&row.Type,
			&stationCode,
			&row.Machine,
			&row.BudgetHC,
			&row.Category,
			&displaySeq,
			&row.Hidden,
			&updateTime,
			&row.UpdateUser,
		); err != nil {
			return nil, err
		}
		row.Process = strings.TrimSpace(row.Process)
		row.CostCenter = strings.TrimSpace(row.CostCenter)
		row.Type = strings.TrimSpace(row.Type)
		row.Machine = strings.TrimSpace(row.Machine)
		row.Category = strings.TrimSpace(row.Category)
		row.UpdateUser = strings.TrimSpace(row.UpdateUser)
		if procCode != nil {
			v := int(*procCode)
			row.ProcessCode = &v
		}
		if stationCode != nil {
			v := int(*stationCode)
			row.StationCode = &v
		}
		if displaySeq != nil {
			v := int(*displaySeq)
			row.DisplaySeq = &v
		}
		if updateTime != nil && !updateTime.IsZero() {
			row.UpdateTime = *updateTime
			row.UpdateAt = updateTime.Format("2006-01-02 15:04:05")
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// formatPGTimestamp formats t as naive local timestamp string matching Python datetime.now() and hc_scan/repo_pg.go.
func formatPGTimestamp(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.Format("2006-01-02 15:04:05.999999")
}

func nullableString(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}
