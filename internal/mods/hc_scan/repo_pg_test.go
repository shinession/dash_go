package hcscan

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"dash_go/internal/core/auth"
)

// These tests run the real SQL against a throw-away PostgreSQL with a synthetic schema (testdata/schema.sql).
// They are skipped unless DASH_GO_TEST_PG is set to a connection string, for example
//
//	postgres://postgres@127.0.0.1:55432/Auth?sslmode=disable
//
// The tests create and drop their own schema "hcscan_it"; point them at a scratch server only.

//go:embed testdata/schema.sql
var testSchemaSQL string

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DASH_GO_TEST_PG")
	if dsn == "" {
		t.Skip("DASH_GO_TEST_PG is not set: the PostgreSQL integration tests are skipped")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	if _, err := admin.Exec(ctx, `DROP SCHEMA IF EXISTS hcscan_it CASCADE; CREATE SCHEMA hcscan_it`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), `DROP SCHEMA IF EXISTS hcscan_it CASCADE`) })

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = "hcscan_it"
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

func seed(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("seed: %v\n%s", err, sql)
	}
}

func seedReferenceData(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	seed(t, pool, `
INSERT INTO t_mqpd_hc_table (id, process, machine, "type", budget_head_count, display_seq, hidden, update_time) VALUES
 ('Winder_520', 'Winder', 'Winder_520 ', 'Routine', 2, 2, false, '2026-11-01'),
 ('Winder_515', 'Winder', 'Winder_515', 'Routine', 1, 1, false, '2026-11-01'),
 ('Winder_9014', 'Winder', 'Winder_9014', 'Non-routine', NULL, 4, false, '2026-11-01'),
 ('Winder_999', 'Winder', 'Hidden one', 'Routine', 1, 5, true, '2026-11-01'),
 ('Slitter_105', 'Slitter', 'Slitter_105', 'Routine', 1, 1, false, '2026-11-01'),
 ('NWC_3301', 'NWC', 'NWC_3301', 'Routine', 1, 1, false, '2026-11-01'),
 ('CSV_2801', 'CSV', 'CSV_2801', 'Routine', 1, 2, false, '2026-11-01')`)

	seed(t, pool, `
INSERT INTO t_mtr_checkpoint (uid, row_id, emp_code, checkpoint_key, cim_process, cim_machine_id, machine, proficiency_level, updated_at, is_hidden) VALUES
 -- r1 was Lv1 and became Lv3: only the newest version counts
 ('u1', 1, 'AB12C01', 'k1', 'Winder', 'C515', 'Winder_515', 1, '2026-10-01 08:00', false),
 ('u2', 1, 'AB12C01', 'k1', 'Winder', 'C515', 'Winder_515', 3, '2026-11-02 08:00', false),
 -- r2 was Lv3 and was hidden later: it must not count
 ('u3', 2, 'AB12C01', 'k2', 'Winder', 'C520', 'Winder_520', 3, '2026-10-01 08:00', false),
 ('u4', 2, 'AB12C01', 'k2', 'Winder', 'C520', 'Winder_520', 3, '2026-11-02 08:00', true),
 -- two checkpoints on one machine: the highest level wins
 ('u5', 3, 'AB12C02', 'k3', 'Winder', 'C515', 'Winder_515', 1, '2026-11-03 08:00', false),
 ('u6', 4, 'AB12C02', 'k4', 'Winder', 'C515', 'Winder_515', 2, '2026-11-03 08:00', false),
 -- a level that is no number is ignored, a row without machine id is ignored, another process is ignored
 ('u7', 5, 'AB12C03', 'k5', 'Winder', 'C520', 'Winder_520', NULL, '2026-11-03 08:00', false),
 ('u8', 6, 'AB12C03', 'k6', 'Winder', '', 'Nothing', 3, '2026-11-03 08:00', false),
 ('u9', 7, 'AB12C04', 'k7', 'Slitter', 'S105', 'Slitter_105', 3, '2026-11-03 08:00', false)`)

	seed(t, pool, `
INSERT INTO t_mes_ee (uid, ee_code, ee_name, cost_center, section, division, direct_type, shift, resign_date, effective_mth, "group") VALUES
 ('m1', 'AB12C01', 'Alice One', '2085', 'x', 'SP9250', NULL, 'PA', NULL, '2026-10-01', 'SP9200'),
 ('m2', 'AB12C01', 'Alice One', '2085', 'x', 'SP9250', NULL, 'PA', NULL, '2026-11-01', 'SP9200'),
 ('m3', 'AB12C02', 'Bob Two (Winder)', '2085', 'x', 'SP9250', 'Shift Leader', 'PB', NULL, '2026-11-01', 'SP9200'),
 ('m4', 'AB12C03', 'Carl Three', '2086', 'x', 'SP9250', NULL, 'PA', '2026-11-10', '2026-11-01', 'SP9200'),
 ('m5', 'AB12C09', 'Other Group', '2085', 'x', 'SP9250', NULL, 'PA', NULL, '2026-11-01', 'SP9999'),
 ('m6', 'AB12C10', 'Only October', '2085', 'x', 'SP9250', NULL, 'PA', NULL, '2026-10-01', 'SP9200')`)

	seed(t, pool, `
INSERT INTO t_daily_attn_member (uid, ee_code, ee_name, process, shift, direct_type, effective_date, end_date, hidden, update_time) VALUES
 ('d1', 'AB12C02', 'Bob Moved', 'Slitter', 'PA', 'Direct', '2026-11-15', '2026-11-30', false, '2026-11-14'),
 ('d2', 'AB12C02', 'Bob Moved Old', 'Taping', 'PC', 'Direct', '2026-11-01', '2026-11-14', false, '2026-11-01'),
 ('d3', 'AB12C11', '', 'Winder', 'PA', 'Direct', '2026-11-01', '2026-12-31', false, '2026-11-01'),
 ('d4', 'AB12C12', 'Hidden', 'Winder', 'PA', 'Direct', '2026-11-01', '2026-12-31', true, '2026-11-01')`)

	seed(t, pool, `INSERT INTO t_daily_attn_calendar (prodn_date, "N", "PA", "PB", "RA", update_time) VALUES
 ('2026-11-19', 'W', 'W', 'PM', 'O', '2026-11-01'), ('2026-11-20', 'O', 'O', 'O', 'O', '2026-11-01')`)
}

func TestPGReads(t *testing.T) {
	pool := testPool(t)
	seedReferenceData(t, pool)
	r := NewPGRepository(pool)
	ctx := context.Background()

	machines, err := r.Machines(ctx, []string{"Winder"})
	if err != nil {
		t.Fatal(err)
	}
	if len(machines) != 3 || machines[0].ID != "Winder_515" || machines[1].ID != "Winder_520" || machines[1].Name != "Winder_520 " ||
		machines[1].Budget != 2 || machines[2].Budget != 0 || machines[2].Routine() {
		t.Fatalf("machines (hidden removed, display order, null budget 0): %+v", machines)
	}
	if cs, _ := r.Machines(ctx, MachineProcesses("CSV")); len(cs) != 2 {
		t.Fatalf("CSV and NWC share their machines: %+v", cs)
	}

	mtr, err := r.MTR(ctx, []string{"Winder"})
	if err != nil {
		t.Fatal(err)
	}
	if mtr.Levels["AB12C01"]["C515"] != 3 {
		t.Errorf("the newest version of a checkpoint row counts: %+v", mtr.Levels["AB12C01"])
	}
	if _, ok := mtr.Levels["AB12C01"]["C520"]; ok {
		t.Errorf("a hidden row must not count: %+v", mtr.Levels["AB12C01"])
	}
	if mtr.Levels["AB12C02"]["C515"] != 2 {
		t.Errorf("the highest level of several checkpoints counts: %+v", mtr.Levels["AB12C02"])
	}
	if len(mtr.Levels["AB12C03"]) != 0 || len(mtr.Levels["AB12C04"]) != 0 {
		t.Errorf("non numeric levels, rows without machine id and other processes are ignored: %+v %+v", mtr.Levels["AB12C03"], mtr.Levels["AB12C04"])
	}
	if mtr.NameToCIM["Winder_515"] != "C515" || mtr.NameToCIM["Winder_520"] != "C520" {
		t.Errorf("machine names map to MTR machine ids: %+v", mtr.NameToCIM)
	}
	if mtr.DataTime.IsZero() || !mtr.DataTime.Equal(time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("data time: %v", mtr.DataTime)
	}

	cal, err := r.CalendarDay(ctx, testDate)
	if err != nil || cal["PA"] != "W" || cal["PB"] != "PM" || cal["N"] != "W" || cal["RA"] != "O" {
		t.Fatalf("calendar: %+v %v", cal, err)
	}
	if none, err := r.CalendarDay(ctx, d(2030, 1, 1)); err != nil || none != nil {
		t.Fatalf("no calendar row gives nil: %+v %v", none, err)
	}

	mes, err := r.MESSnapshot(ctx, testDate)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, m := range mes {
		got[m.EE] = true
	}
	if !got["AB12C01"] || !got["AB12C02"] || !got["AB12C03"] || got["AB12C09"] || got["AB12C10"] || len(mes) != 3 {
		t.Fatalf("the November snapshot of group SP9200 only: %+v", got)
	}
	p, err := r.MESPerson(ctx, "ab12c10")
	if err != nil || p == nil || p.Name != "Only October" || p.Shift != "PA" {
		t.Fatalf("MESPerson falls back to the newest row of any month: %+v %v", p, err)
	}
	if none, err := r.MESPerson(ctx, "NOBODY1"); err != nil || none != nil {
		t.Fatalf("unknown person: %+v %v", none, err)
	}

	members, err := r.Members(ctx, testDate)
	if err != nil || len(members) != 2 {
		t.Fatalf("members valid on the date, hidden removed: %+v %v", members, err)
	}
	dir := BuildDirectory(testDate, mes, members)
	bob, _ := dir.Lookup("AB12C02")
	if bob.Process != "Slitter" || bob.Shift != "PA" || bob.Name != "Bob Moved" {
		t.Errorf("the membership valid on the date overrides the MES row: %+v", bob)
	}
	if nobody, ok := dir.Lookup("AB12C11"); !ok || nobody.Process != "Winder" || nobody.Name != "AB12C11" {
		t.Errorf("a member without MES row and without name: %+v", nobody)
	}
	carl, _ := dir.Lookup("AB12C03")
	before := BuildDirectory(d(2026, 11, 9), mes, members)
	if carl.Process != "LSL" || carl.ResignDate == nil || !dir.Resigned(carl) || before.Resigned(carl) {
		t.Errorf("Carl resigned on the 10th: resigned on the 19th, still employed on the 9th: %+v", carl)
	}
}

func TestPGRecordsAndWrite(t *testing.T) {
	pool := testPool(t)
	seedReferenceData(t, pool)
	r := NewPGRepository(pool)
	ctx := context.Background()

	seed(t, pool, `
INSERT INTO t_daily_attn_data (uid, ee_code, prodn_date, "shift", schedule_shift, actual_shift, machine_hc_json, remark, build_in_ot, addt_ot, total_ot, hidden, update_time, update_user) VALUES
 ('AB12C01_20261119', 'AB12C01', '2026-11-19', 'PA', 'W', 'AM', '[{"process":"Winder","machine":"MCL - Medical Leave","hc":0.5}]', 'old remark', 0.5, 2, 2.5, false, '2026-11-19 06:00', 'someone'),
 ('AB12C02_20261119', 'AB12C02', '2026-11-19', 'PB', 'PM', 'PM', '[{}]', NULL, NULL, NULL, NULL, false, '2026-11-19 06:00', 'someone'),
 ('AB12C03_20261119', 'AB12C03', '2026-11-19', 'PA', 'W', 'Delete Record', '[{}]', NULL, 0, 0, 0, true, '2026-11-19 06:00', 'someone'),
 ('AB12C01_20261118', 'AB12C01', '2026-11-18', 'PA', 'W', 'AM', '[{"process":"Winder","machine":"Winder_515","hc":1}]', NULL, 1, 0, 1, false, '2026-11-18 06:00', 'someone'),
 ('AB12C01_20261112', 'AB12C01', '2026-11-12', 'PA', 'W', 'AM', '[{"process":"Winder","machine":"Winder_515","hc":1}]', NULL, 1, 0, 1, false, '2026-11-12 06:00', 'someone')`)

	recs, err := r.DayRecords(ctx, testDate)
	if err != nil || len(recs) != 2 {
		t.Fatalf("visible records of the day (hidden removed): %+v %v", recs, err)
	}
	byEE := map[string]DayRecord{}
	for _, x := range recs {
		byEE[x.EE] = x
	}
	a := byEE["AB12C01"]
	if a.Actual != "AM" || a.Shift != "PA" || a.Schedule != "W" || len(a.Entries) != 1 || a.Entries[0].HC != 0.5 || a.Remark == nil || *a.Remark != "old remark" || a.AddtOT != 2 || !a.Date.Equal(testDate) {
		t.Fatalf("record: %+v", a)
	}
	if b := byEE["AB12C02"]; b.Remark != nil || b.BuildIn != 0 || len(b.Entries) != 1 || b.Entries[0].Machine != "" {
		t.Fatalf("null columns and the empty [{}] list are read without error: %+v", b)
	}

	hist, err := r.History(ctx, []string{"AB12C01"}, testDate.AddDate(0, 0, -7), testDate)
	if err != nil || len(hist) != 2 { // the 18th and the 12th, not today's row
		t.Fatalf("history window [from, to): %+v %v", hist, err)
	}

	// WriteDay: plan sees the locked current rows, hidden rows are not "current", the upsert replaces rows
	var seen map[string]DayRecord
	written, err := r.WriteDay(ctx, testDate, []string{"AB12C01", "AB12C03", "AB12C05"}, "Lead Er", func(current map[string]DayRecord) ([]DayRecord, error) {
		seen = current
		return []DayRecord{
			{UID: "AB12C01_20261119", EE: "AB12C01", Date: testDate, Shift: "PA", Schedule: "W", Actual: "AM", AddtOT: 2, BuildIn: 0.5, TotalOT: 2.5, Remark: current["AB12C01"].Remark,
				Entries: []HCEntry{{Process: "Winder", Machine: "MCL - Medical Leave", HC: 0.5}, {Process: "Winder", Machine: "Winder_515", HC: 0.5}}},
			{UID: "AB12C03_20261119", EE: "AB12C03", Date: testDate, Shift: "PA", Schedule: "W", Actual: "AM", BuildIn: 1, TotalOT: 1, Entries: []HCEntry{{Process: "Winder", Machine: "Winder_520", HC: 1}}},
			{UID: "AB12C05_20261119", EE: "AB12C05", Date: testDate, Shift: "PA", Schedule: "W", Actual: "PM", Entries: []HCEntry{{Process: "Winder", Machine: "Winder_520", HC: 1}}},
		}, nil
	})
	if err != nil || len(written) != 3 {
		t.Fatalf("write: %v %+v", err, written)
	}
	if _, ok := seen["AB12C01"]; !ok || len(seen) != 1 {
		t.Fatalf("plan must see only the visible current rows of the requested people: %v", seen)
	}

	after, _ := r.DayRecords(ctx, testDate)
	if len(after) != 4 {
		t.Fatalf("one updated, one unhidden, one inserted, one untouched: %d", len(after))
	}
	for _, x := range after {
		if x.EE == "AB12C01" && (len(x.Entries) != 2 || x.Entries[1].Machine != "Winder_515" || x.Remark == nil || *x.Remark != "old remark") {
			t.Fatalf("updated row: %+v", x)
		}
		if x.EE == "AB12C03" && x.Actual != "AM" {
			t.Fatalf("a hidden row is brought back by the upsert, like the Python writer does: %+v", x)
		}
	}
	var user string
	var stamp time.Time
	if err := pool.QueryRow(ctx, `SELECT update_user, update_time FROM t_daily_attn_data WHERE uid = 'AB12C05_20261119'`).Scan(&user, &stamp); err != nil || user != "Lead Er" || time.Since(stamp.Local()) > time.Minute && time.Since(stamp) > time.Minute {
		t.Fatalf("audit columns: %q %v %v", user, stamp, err)
	}

	// a failing plan rolls everything back
	boom := errors.New("boom")
	if _, err := r.WriteDay(ctx, testDate, []string{"AB12C01"}, "Lead Er", func(map[string]DayRecord) ([]DayRecord, error) {
		return []DayRecord{{UID: "AB12C01_20261119", EE: "AB12C01", Date: testDate, Shift: "PA", Schedule: "W", Actual: "PM", Entries: []HCEntry{{Machine: "X", HC: 1}}}}, boom
	}); !errors.Is(err, boom) {
		t.Fatalf("plan error must be returned: %v", err)
	}
	chk, _ := r.DayRecords(ctx, testDate)
	for _, x := range chk {
		if x.EE == "AB12C01" && x.Actual != "AM" {
			t.Fatalf("a failed write must not change anything: %+v", x)
		}
	}
}

// TestPGServiceFlow runs scan, manual change and confirm through the real repository.
func TestPGServiceFlow(t *testing.T) {
	pool := testPool(t)
	seedReferenceData(t, pool)
	seed(t, pool, `INSERT INTO t_daily_attn_data (uid, ee_code, prodn_date, "shift", schedule_shift, actual_shift, machine_hc_json, hidden, update_time)
 VALUES ('AB12C01_20261118', 'AB12C01', '2026-11-18', 'PA', 'W', 'AM', '[{"process":"Winder","machine":"Winder_515","hc":1}]', false, '2026-11-18 06:00')`)
	repo := NewPGRepository(pool)
	log := &recordingLog{}
	svc, err := NewService(Deps{
		Repo: repo, Drafts: NewMemoryDrafts(), ChangeLog: log, Perms: &fakePerms{procs: []string{"Winder"}}, Settings: DefaultSettings(),
		Processes: []string{"Winder"}, LeaveList: testLeave, Now: func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	user := auth.User{EE: "LD01A01", Name: "Lead Er"}

	res, err := svc.Scan(ctx, user, testKey, "ab12c01")
	if err != nil {
		t.Fatal(err)
	}
	e := res.Entry
	if e.MachineID != "Winder_515" || e.Level != 3 || e.RecentDays != 1 || e.Shift != "PA" || e.Schedule != "W" {
		t.Fatalf("scan through PostgreSQL: %+v", e)
	}
	// trailing spaces of a machine name in t_mqpd_hc_table still map to MTR ("Winder_520 " vs "Winder_520")
	v, err := svc.Assign(ctx, user, testKey, "AB12C01", "Winder_520")
	if err != nil || v.Level != 0 && v.Level != 2 {
		t.Fatalf("assign: %+v %v", v, err)
	}
	if _, err := svc.Assign(ctx, user, testKey, "AB12C01", "Winder_515"); err != nil {
		t.Fatal(err)
	}
	out, err := svc.Confirm(ctx, user, testKey, true)
	if err != nil || len(out.Written) != 1 {
		t.Fatalf("confirm: %+v %v", out, err)
	}
	var shift, sched, actual string
	var hc string
	if err := pool.QueryRow(ctx, `SELECT "shift", schedule_shift, actual_shift, machine_hc_json::text FROM t_daily_attn_data WHERE uid = 'AB12C01_20261119'`).Scan(&shift, &sched, &actual, &hc); err != nil {
		t.Fatal(err)
	}
	var stored []HCEntry
	if err := json.Unmarshal([]byte(hc), &stored); err != nil || shift != "PA" || sched != "W" || actual != "AM" ||
		len(stored) != 1 || stored[0] != (HCEntry{Process: "Winder", Machine: "Winder_515", HC: 1}) {
		t.Fatalf("stored row: %s %s %s %s", shift, sched, actual, hc)
	}
	board, err := svc.Board(ctx, user, testKey)
	if err != nil || board.Totals.Saved != 1 || board.Machines[0].Used != 1 || !board.Machines[0].Members[0].Saved {
		t.Fatalf("board after saving: %+v %v", board.Totals, err)
	}
}
