package weblog

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestPGRepositoryAgainstScratchPostgres exercises SQL queries against a real scratch PostgreSQL when
// DASH_GO_TEST_PG is set (creates and drops schema weblog_it).
func TestPGRepositoryAgainstScratchPostgres(t *testing.T) {
	dsn := os.Getenv("DASH_GO_TEST_PG")
	if dsn == "" {
		t.Skip("set DASH_GO_TEST_PG to a scratch PostgreSQL connection string to run PG integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET search_path TO weblog_it`)
		return err
	}

	adminPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer adminPool.Close()

	if _, err := adminPool.Exec(ctx, `DROP SCHEMA IF EXISTS weblog_it CASCADE; CREATE SCHEMA weblog_it`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(), `DROP SCHEMA IF EXISTS weblog_it CASCADE`)
	})

	schemaSQL, err := os.ReadFile("testdata/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adminPool.Exec(ctx, `SET search_path TO weblog_it; `+string(schemaSQL)); err != nil {
		t.Fatal(err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// Seed t_log_daily including NULL and empty hosts, repeated IPs across days/hosts, and idle rows.
	seed := `
INSERT INTO t_log_daily (uid, log_date, ip_addr, app, category, host, action_count, update_time) VALUES
  ('u1', '2026-11-16', '10.0.0.1/32', 'IE', 'tab1', NULL, 100, '2026-11-16 12:00:00'),
  ('u2', '2026-11-16', '10.0.0.1/32', 'IE', 'idle', '', 40, '2026-11-16 12:00:00'),
  ('u3', '2026-11-17', '10.0.0.1/32', 'QDB', 'tab2', 'sgmescimai', 200, '2026-11-17 12:00:00'),
  ('u4', '2026-11-17', '10.0.0.2/32', 'Gitea', 'git', 'sgmescimemg', 80, '2026-11-17 12:00:00'),
  ('u5', '2026-11-25', '10.0.0.2/32', 'IE', 'tab9', 'sgmescimai', 300, '2026-11-25 12:00:00');

INSERT INTO "T_USER_ALL" (ee_code, "Name", "Type", activated, "UpdateDate") VALUES
  ('AD01', 'Old Row', 'user', true, '2026-01-01 00:00:00'),
  ('AD01', 'Admin Now', 'admin', true, '2026-11-01 00:00:00'),
  ('AD02', 'Disabled Admin', 'admin', false, '2026-11-01 00:00:00'),
  ('US01', 'Regular', 'user', true, '2026-11-01 00:00:00');
`
	if _, err := pool.Exec(ctx, seed); err != nil {
		t.Fatal(err)
	}

	repo := NewPGLogRepository(pool)
	meta, err := repo.Meta(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if meta.MinDate != "2026-11-16" || meta.MaxDate != "2026-11-25" {
		t.Errorf("meta bounds = %s..%s", meta.MinDate, meta.MaxDate)
	}
	if len(meta.Hosts) != 3 || meta.Hosts[0] != "prod_eng" {
		t.Errorf("meta hosts = %v, want prod_eng normalized from NULL/empty", meta.Hosts)
	}

	from, _ := parseDate("2026-11-16")
	to, _ := parseDate("2026-11-29")
	raw, err := repo.QueryTrend(ctx, TrendDBFilter{
		Mode:             ModeWeek,
		From:             from,
		To:               to,
		EffectiveGroupBy: "host",
		ExcludeIdle:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Week 2026-11-16: 100 (prod_eng) + 200 (sgmescimai) + 80 (sgmescimemg) = 380, distinct IPs = 2.
	w1 := raw.PeriodTotals["2026-11-16"]
	if w1.Actions != 380 || w1.Visitors != 2 {
		t.Errorf("week 1 = %+v, want actions=380 visitors=2", w1)
	}
	if raw.PeriodSeries["2026-11-16"]["prod_eng"] != 100 {
		t.Errorf("prod_eng week 1 = %d, want 100", raw.PeriodSeries["2026-11-16"]["prod_eng"])
	}
	if raw.Overall.Actions != 680 || raw.Overall.Visitors != 2 {
		t.Errorf("overall = %+v, want actions=680 visitors=2", raw.Overall)
	}

	checker := NewPGAdminChecker(pool)
	for ee, want := range map[string]bool{"AD01": true, "ad01": true, "AD02": false, "US01": false, "MISSING": false} {
		got, err := checker.IsUserAdmin(ctx, ee)
		if err != nil || got != want {
			t.Errorf("IsUserAdmin(%q) = (%v, %v), want (%v, nil)", ee, got, err, want)
		}
	}
}
