package hcscan

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"dash_go/internal/core/auth"
	"dash_go/internal/core/config"
)

// TestDevCopyReadOnly runs the repository and the scan logic on a COPY of the real Auth database. It is skipped
// unless DASH_GO_TEST_PG_DEV holds a connection string, for example
//
//	postgres://<user>@127.0.0.1:5432/Auth?sslmode=disable
//
// The session is forced to read-only (default_transaction_read_only=on, checked below), the drafts live in memory
// and Confirm is never called, so nothing can be written. The output only has counters, no names or employee codes.
// DASH_GO_DASH_CONFIG_DIR (optional) supplies the process list and the leave options of the Python configuration.
func TestDevCopyReadOnly(t *testing.T) {
	dsn := os.Getenv("DASH_GO_TEST_PG_DEV")
	if dsn == "" {
		t.Skip("DASH_GO_TEST_PG_DEV is not set: the read-only test on a copy of the real database is skipped")
	}
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var readOnly string
	if err := pool.QueryRow(ctx, `SHOW transaction_read_only`).Scan(&readOnly); err != nil || readOnly != "on" {
		t.Fatalf("the session must be read-only, got %q (%v)", readOnly, err)
	}

	processes := []string{"Slitter", "Taping", "Winder", "LSL", "HSL", "CDC", "Packing", "QA", "CSV", "NWC"}
	leave := []string{"ANL - Annual Leave", "AWO - Absence without Approval", "CCL - Childcare Leave", "CIL - Critical Illness", "CPD - Company Paid Leave", "FXL - Flexible Leave",
		"HLL - Hospitalization Leave", "HLU - Unpaid Hospitalization Leave", "MCL - Medical Leave", "MCU - Unpaid Medical Leave", "MEF - Medical/Specialist Appointment", "TLC - Telecommute", "TNL - Training Leave", "UPL - Unpaid Day Leave"}
	if dir := os.Getenv("DASH_GO_DASH_CONFIG_DIR"); dir != "" {
		if c, err := config.Load(config.Options{ConfigDir: t.TempDir(), DashConfigDir: dir}); err == nil && len(c.Dash.HCProcessList) > 0 {
			processes, leave = c.Dash.HCProcessList, c.Dash.LeaveList
		}
	}

	today := ProductionDate(time.Now(), 7)
	svc, err := NewService(Deps{
		Repo: NewPGRepository(pool), Drafts: NewMemoryDrafts(), Perms: &fakePerms{procs: processes}, Settings: DefaultSettings(),
		Processes: processes, LeaveList: leave,
	})
	if err != nil {
		t.Fatal(err)
	}
	user := auth.User{EE: "TEST0000", Name: "Read Only"}

	for _, process := range processes {
		key := Key{Process: process, Date: today, Shift: ShiftAM}
		started := time.Now()
		board, err := svc.Board(ctx, user, key)
		boardTime := time.Since(started)
		if err != nil {
			t.Errorf("%s: board: %v", process, err)
			continue
		}
		mapped, routine := 0, 0
		for _, m := range board.Machines {
			if m.Routine {
				routine++
				if m.MTRMapped {
					mapped++
				}
			}
		}
		statuses := map[string]int{}
		scanned, leaders := 0, 0
		scanStarted := time.Now()
		for _, p := range board.NotScanned {
			if scanned >= 15 {
				break
			}
			res, err := svc.Scan(ctx, user, key, p.EE)
			scanned++
			if err != nil {
				code := "error"
				if e, ok := err.(*Error); ok {
					code = e.Code
				}
				statuses[code]++
				continue
			}
			statuses[res.Status]++
			e := res.Entry
			isLeader := e.Source == SourceAuto && (strings.Contains(e.Reason, "Shift Leader (direct type)") || strings.Contains(e.Reason, "at the last record"))
			if isLeader {
				leaders++
			}
			if e.Source == SourceAuto && !isLeader && (e.Level < 1 || e.MachineID == "") {
				t.Errorf("%s: an automatic line needs MTR level 1 or higher and a machine: level=%d machine=%q", process, e.Level, e.MachineID)
			}
			if isLeader && e.Level != -1 {
				t.Errorf("%s: a leader line is on an activity row, got level %d", process, e.Level)
			}
		}
		scanTime := time.Since(scanStarted)
		after, err := svc.Board(ctx, user, key)
		if err != nil {
			t.Errorf("%s: board after scans: %v", process, err)
			continue
		}
		autoOver := 0
		for _, m := range after.Machines {
			saved := 0.0
			for _, mem := range m.Members {
				if mem.Saved {
					saved += mem.HC
				}
			}
			if m.Used > m.Budget+0.01 && m.Budget > 0 && saved <= m.Budget+0.01 {
				autoOver++ // the pending lines pushed a machine over its budget
			}
		}
		if autoOver > 0 {
			t.Errorf("%s: automatic lines must never push a machine over its budget (%d machine(s))", process, autoOver)
		}
		perScan := time.Duration(0)
		if scanned > 0 {
			perScan = scanTime / time.Duration(scanned)
		}
		t.Logf("%-8s machines=%d routine=%d mtr_mapped=%d expected=%d scanned_now=%d statuses=%v leaders_on_leader_row=%d mtr_employees=%d first_board=%dms per_scan=%dms",
			process, len(board.Machines), routine, mapped, board.Totals.Expected, scanned, statuses, leaders, board.MTR.Employees, boardTime.Milliseconds(), perScan.Milliseconds())
		for status := range statuses {
			if strings.Contains(status, "internal") || status == "error" {
				t.Errorf("%s: a scan failed with %q", process, status)
			}
		}
	}
}
