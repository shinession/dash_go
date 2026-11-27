//go:build devserver

package hcscan

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"dash_go/internal/core/auth"
	"dash_go/internal/core/config"
	"dash_go/internal/core/server"
	"dash_go/internal/core/shell"
	"dash_go/internal/version"
)

// TestDevServer is a development tool, not a test: it starts the module on a scratch PostgreSQL with demo data so
// the page can be tried in a browser (or driven by Selenium) without the Python application.
//
//	DASH_GO_TEST_PG=postgres://postgres@127.0.0.1:55432/Auth?sslmode=disable \
//	go test -tags devserver -run TestDevServer -timeout 0 ./internal/mods/hc_scan
//
// Open http://127.0.0.1:18750/dev/login (a login as the demo shift leader). DEV_SECONDS (default 900) is how long
// the server runs, DEV_PORT the port. Never point DASH_GO_TEST_PG at a real database: the test creates and drops
// the schema hcscan_it.
func TestDevServer(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	today := ProductionDate(time.Now(), 7)
	seedDemo(t, pool, today)

	signer, err := auth.NewSigner("dev-secret")
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	authSvc := auth.NewService(signer, auth.NewMemoryNonces(), auth.Settings{BasePath: "/dash_go", Log: log})
	svc, err := NewService(Deps{
		Repo: NewPGRepository(pool), Drafts: NewMemoryDrafts(), ChangeLog: NewFileChangeLog(t.TempDir()),
		Perms: &fakePerms{procs: []string{"Slitter", "Winder"}}, Settings: DefaultSettings(),
		Processes: []string{"Slitter", "Winder"}, LeaveList: testLeave, Log: log,
	})
	if err != nil {
		t.Fatal(err)
	}
	sh, err := shell.New(shell.Options{Base: "/dash_go", Version: version.Version, PhotoURLTemplate: config.DefaultPhotoURLTemplate, DashHome: "/ie/", Auth: authSvc, Log: log})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	authSvc.Mount(mux)
	sh.Mount(mux)
	if err := svc.Routes(mux, "/dash_go/hc_scan", authSvc.Require, sh); err != nil {
		t.Fatal(err)
	}
	mux.HandleFunc("GET /dev/login", func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		tok, _ := signer.Sign(auth.Claims{K: auth.KindSSO, EE: "LD01A01", Name: "Lead Er", Iat: now.Unix(), Exp: now.Add(time.Minute).Unix(), N: auth.NewNonce()})
		http.Redirect(w, r, "/dash_go/auth/sso?t="+tok+"&next=/dash_go/hc_scan/", http.StatusSeeOther)
	})

	port := envOr("DEV_PORT", "18750")
	srv := &http.Server{Addr: "127.0.0.1:" + port, Handler: server.Handler(mux, log)}
	go func() { _ = srv.ListenAndServe() }()
	t.Logf("dev server: http://127.0.0.1:%s/dev/login", port)
	secs, _ := strconv.Atoi(envOr("DEV_SECONDS", "900"))
	time.Sleep(time.Duration(secs) * time.Second)
	_ = srv.Shutdown(ctx)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// seedDemo fills the scratch schema with a small Winder shop floor: six machines and a dozen people with every
// situation the page has to show (levels, cross-trained, day off, other shift, leave, pre-assigned).
func seedDemo(t *testing.T, pool *pgxpool.Pool, today time.Time) {
	t.Helper()
	day := func(n int) string { return today.AddDate(0, 0, n).Format("2006-01-02") }
	next := today.AddDate(0, 0, 1)
	months := []string{
		time.Date(next.Year(), next.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02"),
		time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02"),
	}

	seed(t, pool, `
INSERT INTO t_mqpd_hc_table (id, process, machine, "type", budget_head_count, display_seq, hidden, update_time) VALUES
 ('Winder_515', 'Winder', 'Winder_515', 'Routine', 1, 1, false, now()),
 ('Winder_520', 'Winder', 'Winder_520', 'Routine', 2, 2, false, now()),
 ('Winder_525', 'Winder', 'Winder_525', 'Routine', 1, 3, false, now()),
 ('Winder_530', 'Winder', 'Winder_530', 'Routine', 1, 4, false, now()),
 ('Winder_535', 'Winder', 'Winder_535', 'Routine', 1, 5, false, now()),
 ('Winder_9014', 'Winder', 'Material handling', 'Non-routine', NULL, 6, false, now()),
 ('Slitter_105', 'Slitter', 'Slitter_105', 'Routine', 1, 1, false, now())`)

	seed(t, pool, `
INSERT INTO t_mtr_checkpoint (uid, row_id, emp_code, checkpoint_key, cim_process, cim_machine_id, machine, proficiency_level, updated_at, is_hidden) VALUES
 ('t01', 1, 'AB12C01', 'k', 'Winder', 'C515', 'Winder_515', 3, now(), false),
 ('t02', 2, 'AB12C01', 'k', 'Winder', 'C520', 'Winder_520', 2, now(), false),
 ('t03', 3, 'AB12C02', 'k', 'Winder', 'C515', 'Winder_515', 1, now(), false),
 ('t04', 4, 'AB12C02', 'k', 'Winder', 'C520', 'Winder_520', 3, now(), false),
 ('t05', 5, 'AB12C04', 'k', 'Winder', 'C520', 'Winder_520', 1, now(), false),
 ('t06', 6, 'AB12C05', 'k', 'Winder', 'C515', 'Winder_515', 2, now(), false),
 ('t07', 7, 'AB12C08', 'k', 'Winder', 'C525', 'Winder_525', 2, now(), false),
 ('t08', 8, 'AB12C08', 'k', 'Winder', 'C530', 'Winder_530', 3, now(), false),
 ('t09', 9, 'AB12C09', 'k', 'Winder', 'C530', 'Winder_530', 3, now(), false),
 ('t10', 10, 'AB12C06', 'k', 'Winder', 'C525', 'Winder_525', 2, now(), false),
 ('t11', 11, 'AB12C10', 'k', 'Winder', 'C520', 'Winder_520', 2, now(), false),
 ('t12', 12, 'AB12C01', 'k', 'Slitter', 'S105', 'Slitter_105', 2, now(), false),
 ('t13', 13, 'AB12C05', 'k', 'Slitter', 'S105', 'Slitter_105', 3, now(), false)`)

	if months[0] == months[1] {
		months = months[:1] // today and tomorrow are in the same month
	}
	for _, month := range months {
		seed(t, pool, `
INSERT INTO t_mes_ee (uid, ee_code, ee_name, cost_center, section, division, direct_type, shift, effective_mth, "group") VALUES
 ('a1' || $1::text, 'AB12C01', 'Alice Tan', '2085', 'x', 'SP9250', NULL, 'PA', $1::date, 'SP9200'),
 ('a2' || $1::text, 'AB12C02', 'Bob Lim', '2085', 'x', 'SP9250', NULL, 'PA', $1::date, 'SP9200'),
 ('a3' || $1::text, 'AB12C03', 'Carl Ong', '2085', 'x', 'SP9250', NULL, 'PA', $1::date, 'SP9200'),
 ('a4' || $1::text, 'AB12C04', 'Dina Koh', '2085', 'x', 'SP9250', NULL, 'PA', $1::date, 'SP9200'),
 ('a5' || $1::text, 'AB12C05', 'Eve Wong (Slitter)', '2081', 'x', 'SP9250', NULL, 'PA', $1::date, 'SP9200'),
 ('a6' || $1::text, 'AB12C06', 'Finn Goh', '2085', 'x', 'SP9250', NULL, 'PB', $1::date, 'SP9200'),
 ('a7' || $1::text, 'AB12C07', 'Gus Yeo', '2085', 'x', 'SP9250', NULL, 'RA', $1::date, 'SP9200'),
 ('a8' || $1::text, 'AB12C08', 'Hana Lee', '2085', 'x', 'SP9250', NULL, 'PA', $1::date, 'SP9200'),
 ('a9' || $1::text, 'AB12C09', 'Ivy Chan', '2085', 'x', 'SP9250', 'Shift Leader', 'PA', $1::date, 'SP9200'),
 ('b1' || $1::text, 'AB12C10', 'Jon Sim', '2085', 'x', 'SP9250', NULL, 'PA', $1::date, 'SP9200'),
 ('b2' || $1::text, 'AB12C11', 'Kim Ho', '2085', 'x', 'SP9250', NULL, 'PA', $1::date, 'SP9200'),
 ('c1' || $1::text, 'LD01A01', 'Lead Er', '2085', 'x', 'SP9250', 'Shift Leader', 'PA', $1::date, 'SP9200')`, month)
	}

	seed(t, pool, `INSERT INTO t_daily_attn_calendar (prodn_date, "N", "PA", "PB", "PC", "RA", update_time) VALUES
 ($1::date, 'W', 'W', 'PM', 'W', 'O', now()), ($2::date, 'W', 'W', 'PM', 'W', 'O', now())`, day(0), day(1))

	// Bob worked Winder_520 on three of the last days; Kim is on leave all day; Jon has half a day of leave and
	// Hana is already assigned by somebody else
	for i := 1; i <= 3; i++ {
		seed(t, pool, `INSERT INTO t_daily_attn_data (uid, ee_code, prodn_date, "shift", schedule_shift, actual_shift, machine_hc_json, build_in_ot, addt_ot, total_ot, hidden, update_time, update_user)
 VALUES ('AB12C02_' || to_char($1::date, 'YYYYMMDD'), 'AB12C02', $1::date, 'PA', 'W', 'AM', '[{"process":"Winder","machine":"Winder_520","hc":1}]', 1, 0, 1, false, now(), 'demo')`, day(-i))
	}
	seed(t, pool, `INSERT INTO t_daily_attn_data (uid, ee_code, prodn_date, "shift", schedule_shift, actual_shift, machine_hc_json, build_in_ot, addt_ot, total_ot, hidden, update_time, update_user) VALUES
 ('AB12C11_' || to_char($1::date, 'YYYYMMDD'), 'AB12C11', $1::date, 'PA', 'W', 'MCL - Medical Leave', '[{"process":"Winder","machine":"MCL - Medical Leave","hc":1}]', 0, 0, 0, false, now(), 'demo'),
 ('AB12C10_' || to_char($1::date, 'YYYYMMDD'), 'AB12C10', $1::date, 'PA', 'W', 'AM', '[{"process":"Winder","machine":"ANL - Annual Leave","hc":0.5}]', 0, 0, 0, false, now(), 'demo'),
 ('AB12C08_' || to_char($1::date, 'YYYYMMDD'), 'AB12C08', $1::date, 'PA', 'W', 'AM', '[{"process":"Winder","machine":"Winder_525","hc":1}]', 1, 0, 1, false, now(), 'demo')`, day(0))
}
