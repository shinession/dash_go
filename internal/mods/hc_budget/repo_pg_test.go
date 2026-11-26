package hcbudget

import (
	"context"
	_ "embed"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed testdata/schema.sql
var testSchemaSQL string

func testPGPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DASH_GO_TEST_PG")
	if dsn == "" {
		t.Skip("DASH_GO_TEST_PG is not set: the hc_budget PostgreSQL integration tests are skipped")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	if _, err := admin.Exec(ctx, `DROP SCHEMA IF EXISTS hcbudget_it CASCADE; CREATE SCHEMA hcbudget_it`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), `DROP SCHEMA IF EXISTS hcbudget_it CASCADE`) })

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = "hcbudget_it"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, testSchemaSQL); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return pool
}

func TestPGRepositoryReadInsertUpdateAndReorder(t *testing.T) {
	pool := testPGPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
INSERT INTO t_mqpd_hc_table (id, process, cost_center, process_code, "type", station_code, machine, budget_head_count, category, display_seq, hidden, update_time, update_user) VALUES
 ('Winder_515', 'Winder', '2085', 10, 'Routine', 515, 'Winder 515', 0.3, 'Main', 1, false, '2026-11-20 08:00:00', 'Seed'),
 ('Winder_520', 'Winder', '2085', 10, 'Routine', 520, 'Winder 520', 2.0, 'Main', 2, false, '2026-11-20 08:00:00', 'Seed'),
 ('Slitter_105', 'Slitter', '2086', 20, 'Routine', 105, 'Slitter 105', 1.0, 'Main', 1, false, '2026-11-20 08:00:00', 'Seed')`); err != nil {
		t.Fatal(err)
	}

	repo := NewPGRepository(pool)
	rows, err := repo.ProcessRows(ctx, "Winder")
	if err != nil || len(rows) != 2 {
		t.Fatalf("ProcessRows: %d %v", len(rows), err)
	}
	if rows[0].BudgetHC != 0.3 || rows[0].CostCenter != "2085" {
		t.Fatalf("real/char(4) formatting: %+v", rows[0])
	}

	now := time.Date(2026, 11, 27, 10, 0, 0, 0, time.Local)
	written, err := repo.Apply(ctx, "Winder", []string{"Winder_530", "Slitter_105"}, func(existing []Row, taken map[string]bool) ([]Row, []Row, error) {
		if !taken["Slitter_105"] || taken["Winder_530"] {
			t.Fatalf("unexpected taken map: %v", taken)
		}
		ins := []Row{{
			ID: "Winder_530", Process: "Winder", CostCenter: "2085", ProcessCode: intPtr(10),
			Type: TypeNonRoutine, StationCode: intPtr(530), Machine: "Winder 530", BudgetHC: 1.5,
			Category: "Support", DisplaySeq: intPtr(1), Hidden: false, UpdateTime: now, UpdateUser: "Admin",
		}}
		up := existing[0]
		up.DisplaySeq = intPtr(2)
		up.Hidden = true
		up.UpdateTime = now
		up.UpdateUser = "Admin"
		return ins, []Row{up}, nil
	})
	if err != nil || len(written) != 2 {
		t.Fatalf("Apply: %d %v", len(written), err)
	}

	after, err := repo.ProcessRows(ctx, "Winder")
	if err != nil || len(after) != 3 {
		t.Fatalf("after: %d %v", len(after), err)
	}
	if after[0].ID != "Winder_530" || *after[0].DisplaySeq != 1 || after[1].ID != "Winder_515" || !after[1].Hidden {
		t.Fatalf("persisted order/state mismatch: %+v", after)
	}
}
