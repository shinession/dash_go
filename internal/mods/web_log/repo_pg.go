package weblog

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgLogRepo reads t_log_daily in the Log PostgreSQL database. All SQL is parameterized.
type pgLogRepo struct {
	pool *pgxpool.Pool
}

// NewPGLogRepository returns a PostgreSQL LogRepository backed by pool (Log database).
func NewPGLogRepository(pool *pgxpool.Pool) LogRepository { return &pgLogRepo{pool: pool} }

// pgAdminChecker checks "T_USER_ALL" in the Auth PostgreSQL database.
type pgAdminChecker struct {
	pool *pgxpool.Pool
}

// NewPGAdminChecker returns an AdminChecker backed by pool (Auth database).
func NewPGAdminChecker(pool *pgxpool.Pool) AdminChecker { return &pgAdminChecker{pool: pool} }

// IsUserAdmin checks the newest row of eeCode in "T_USER_ALL" (Auth DB) by "UpdateDate".
// Only activated users with "Type" = 'admin' (case-insensitive) are granted admin access.
func (a *pgAdminChecker) IsUserAdmin(ctx context.Context, eeCode string) (bool, error) {
	if strings.TrimSpace(eeCode) == "" {
		return false, nil
	}
	var userType, activated string
	err := a.pool.QueryRow(ctx, `
SELECT COALESCE("Type"::text, ''), LOWER(COALESCE(activated::text, ''))
FROM "T_USER_ALL"
WHERE upper(btrim(ee_code::text)) = upper(btrim($1))
ORDER BY "UpdateDate" DESC NULLS LAST
LIMIT 1`, eeCode).Scan(&userType, &activated)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if activated != "true" && activated != "t" && activated != "1" {
		return false, nil
	}
	return strings.EqualFold(strings.TrimSpace(userType), "admin"), nil
}

const (
	normHostExpr     = `COALESCE(NULLIF(btrim(host::text), ''), 'prod_eng')`
	normAppExpr      = `COALESCE(NULLIF(btrim(app::text), ''), 'Others')`
	normCategoryExpr = `COALESCE(NULLIF(btrim(category::text), ''), 'others')`
)

// Meta reads available date boundaries, normalized servers, apps, and per-app categories from t_log_daily.
func (r *pgLogRepo) Meta(ctx context.Context) (MetaData, error) {
	var minD, maxD *time.Time
	if err := r.pool.QueryRow(ctx, `SELECT MIN(log_date), MAX(log_date) FROM t_log_daily`).Scan(&minD, &maxD); err != nil {
		return MetaData{}, fmt.Errorf("read date bounds: %w", err)
	}

	hostRows, err := r.pool.Query(ctx, `SELECT DISTINCT `+normHostExpr+` AS h FROM t_log_daily ORDER BY 1`)
	if err != nil {
		return MetaData{}, fmt.Errorf("read hosts: %w", err)
	}
	defer hostRows.Close()
	hosts := []string{}
	for hostRows.Next() {
		var h string
		if err := hostRows.Scan(&h); err != nil {
			return MetaData{}, err
		}
		hosts = append(hosts, h)
	}
	if err := hostRows.Err(); err != nil {
		return MetaData{}, err
	}

	acRows, err := r.pool.Query(ctx, `SELECT DISTINCT `+normAppExpr+` AS a, `+normCategoryExpr+` AS c FROM t_log_daily ORDER BY 1, 2`)
	if err != nil {
		return MetaData{}, fmt.Errorf("read apps and categories: %w", err)
	}
	defer acRows.Close()

	appsSet := map[string]bool{}
	allCatsSet := map[string]bool{}
	catsByApp := map[string][]string{}
	for acRows.Next() {
		var app, cat string
		if err := acRows.Scan(&app, &cat); err != nil {
			return MetaData{}, err
		}
		appsSet[app] = true
		allCatsSet[cat] = true
		catsByApp[app] = append(catsByApp[app], cat)
	}
	if err := acRows.Err(); err != nil {
		return MetaData{}, err
	}

	apps := make([]string, 0, len(appsSet))
	for a := range appsSet {
		apps = append(apps, a)
	}
	sort.Strings(apps)

	allCats := make([]string, 0, len(allCatsSet))
	for c := range allCatsSet {
		allCats = append(allCats, c)
	}
	sort.Strings(allCats)

	meta := MetaData{
		Apps:            apps,
		Hosts:           hosts,
		CategoriesByApp: catsByApp,
		AllCategories:   allCats,
	}
	if minD != nil && !minD.IsZero() {
		meta.MinDate = minD.Format("2006-01-02")
	}
	if maxD != nil && !maxD.IsZero() {
		meta.MaxDate = maxD.Format("2006-01-02")
	}
	return meta, nil
}

// QueryTrend aggregates t_log_daily in PostgreSQL for one normalized filter window using a single CTE scan.
// Period and series expressions come strictly from fixed allow-listed SQL expressions; all filter values are bound parameters.
func (r *pgLogRepo) QueryTrend(ctx context.Context, f TrendDBFilter) (TrendRawResult, error) {
	var periodExpr string
	switch f.Mode {
	case ModeDay:
		periodExpr = `log_date::date`
	case ModeWeek:
		periodExpr = `date_trunc('week', log_date::timestamp)::date`
	case ModeMonth:
		periodExpr = `date_trunc('month', log_date::timestamp)::date`
	default:
		return TrendRawResult{}, fmt.Errorf("unsupported mode %q", f.Mode)
	}

	var seriesExpr string
	switch f.EffectiveGroupBy {
	case "app":
		seriesExpr = normAppExpr
	case "category":
		seriesExpr = normCategoryExpr
	case "host":
		seriesExpr = normHostExpr
	default:
		return TrendRawResult{}, fmt.Errorf("unsupported group_by %q", f.EffectiveGroupBy)
	}

	args := []any{f.From.Format("2006-01-02"), f.To.Format("2006-01-02")}
	var where strings.Builder
	where.WriteString(`log_date >= $1::date AND log_date <= $2::date`)
	nextArg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if f.App != "" {
		where.WriteString(` AND ` + normAppExpr + ` = ` + nextArg(f.App))
	}
	if len(f.Hosts) > 0 {
		where.WriteString(` AND ` + normHostExpr + ` = ANY(` + nextArg(f.Hosts) + `::text[])`)
	}
	if len(f.Categories) > 0 {
		where.WriteString(` AND ` + normCategoryExpr + ` = ANY(` + nextArg(f.Categories) + `::text[])`)
	}
	if f.ExcludeIdle {
		where.WriteString(` AND LOWER(` + normCategoryExpr + `) <> 'idle'`)
	}

	query := `
WITH filtered AS (
    SELECT
        ` + periodExpr + ` AS period_start,
        ` + seriesExpr + ` AS series_name,
        ip_addr::text AS ip_addr,
        COALESCE(action_count, 0)::bigint AS action_count
    FROM t_log_daily
    WHERE ` + where.String() + `
)
SELECT 0 AS kind, period_start, series_name, SUM(action_count)::bigint AS actions, 0::bigint AS visitors
FROM filtered
GROUP BY period_start, series_name
UNION ALL
SELECT 1 AS kind, period_start, '' AS series_name, SUM(action_count)::bigint AS actions, COUNT(DISTINCT ip_addr)::bigint AS visitors
FROM filtered
GROUP BY period_start
UNION ALL
SELECT 2 AS kind, NULL::date AS period_start, '' AS series_name, COALESCE(SUM(action_count), 0)::bigint AS actions, COUNT(DISTINCT ip_addr)::bigint AS visitors
FROM filtered
UNION ALL
SELECT 3 AS kind, NULL::date AS period_start, series_name, SUM(action_count)::bigint AS actions, COUNT(DISTINCT ip_addr)::bigint AS visitors
FROM filtered
GROUP BY series_name
ORDER BY kind ASC, period_start ASC NULLS LAST, series_name ASC`

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return TrendRawResult{}, err
	}
	defer rows.Close()

	res := TrendRawResult{
		PeriodSeries: map[string]map[string]int64{},
		PeriodTotals: map[string]PeriodStat{},
		SeriesTotals: map[string]PeriodStat{},
	}
	for rows.Next() {
		var (
			kind        int
			periodStart *time.Time
			seriesName  string
			actions     int64
			visitors    int64
		)
		if err := rows.Scan(&kind, &periodStart, &seriesName, &actions, &visitors); err != nil {
			return TrendRawResult{}, err
		}
		switch kind {
		case 0:
			if periodStart == nil {
				continue
			}
			pk := periodStart.Format("2006-01-02")
			if res.PeriodSeries[pk] == nil {
				res.PeriodSeries[pk] = map[string]int64{}
			}
			res.PeriodSeries[pk][seriesName] = actions
		case 1:
			if periodStart == nil {
				continue
			}
			pk := periodStart.Format("2006-01-02")
			res.PeriodTotals[pk] = PeriodStat{Actions: actions, Visitors: visitors}
		case 2:
			res.Overall = PeriodStat{Actions: actions, Visitors: visitors}
		case 3:
			res.SeriesTotals[seriesName] = PeriodStat{Actions: actions, Visitors: visitors}
		}
	}
	if err := rows.Err(); err != nil {
		return TrendRawResult{}, err
	}
	return res, nil
}
