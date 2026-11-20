package hcscan

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"dash_go/internal/core/auth"
	"dash_go/internal/core/config"
)

// TestDevCopyBoardAgreesWithTheDailyAttnSummary checks the board against a COPY of the real Auth database, with the
// logic of the Daily Attn Summary of the Python application (cal_daily_summary in LocalListV3.py) written again here
// from the raw rows: for every process and both shifts of one day, the people of the shift, the HC on machines, the
// leave and the people still to be recorded must be the ones the Python popup counts, and no card may show somebody of
// another process.
//
// It is skipped unless DASH_GO_TEST_PG_DEV holds a connection string (see TestDevCopyReadOnly: the session is read-only,
// drafts are in memory, nothing is written). DASH_GO_TEST_DAY (2026-11-17) is the day to look at; without it the
// newest day with at least 100 records is used. DASH_GO_DASH_CONFIG_DIR supplies the process list and the leave options.
// The output has counters and machine names, no employee codes and no names of people.
func TestDevCopyBoardAgreesWithTheDailyAttnSummary(t *testing.T) {
	dsn := os.Getenv("DASH_GO_TEST_PG_DEV")
	if dsn == "" {
		t.Skip("DASH_GO_TEST_PG_DEV is not set: the comparison with a copy of the real database is skipped")
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

	var day time.Time
	if v := os.Getenv("DASH_GO_TEST_DAY"); v != "" {
		if day, err = time.Parse("2006-01-02", v); err != nil {
			t.Fatal(err)
		}
	} else {
		var newest time.Time
		if err := pool.QueryRow(ctx, `SELECT max(prodn_date) FROM (SELECT prodn_date FROM t_daily_attn_data WHERE COALESCE(hidden,false)=false GROUP BY prodn_date HAVING count(*) >= 100) x`).Scan(&newest); err != nil {
			t.Fatal(err)
		}
		day = DateOnly(newest)
	}

	// the board is built for a day in the past: the clock of the service stands on that day, so the date is in range
	now := day.Add(12 * time.Hour)
	svc, err := NewService(Deps{
		Repo: NewPGRepository(pool), Drafts: NewMemoryDrafts(), Perms: &fakePerms{procs: processes}, Settings: DefaultSettings(),
		Processes: processes, LeaveList: leave, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	user := auth.User{EE: "TEST0000", Name: "Read Only"}

	var checked, foreignOnCards, unequal int
	for _, process := range processes {
		for _, shift := range []string{ShiftAM, ShiftPM} {
			k := Key{Process: process, Date: day, Shift: shift}
			d, err := svc.loadDay(ctx, k)
			if err != nil {
				t.Fatal(err)
			}
			board, err := svc.Board(ctx, user, k)
			if err != nil {
				t.Fatalf("%s %s: %v", process, shift, err)
			}
			checked++
			here := RosterProcesses(process)

			// 1. nobody of another process on a card: a member of the card either belongs to the process, or has an entry
			// that names the process as the process of its machine
			for _, m := range board.Machines {
				for _, p := range m.Members {
					person, known := d.dir.Lookup(p.EE)
					if known && inProcesses(here, person.Process) {
						if p.From != "" {
							t.Errorf("%s %s %s: a member of the process is tagged as from %q", process, shift, m.Name, p.From)
						}
						continue
					}
					named := false
					for _, h := range d.recByEE[p.EE].Entries {
						if inProcesses(here, h.Process) && strings.TrimSpace(h.Machine) != "" {
							named = true
						}
					}
					if !named {
						foreignOnCards++
						t.Errorf("%s %s: %q has somebody of another process (%q) whose entries do not name %s", process, shift, m.Name, person.Process, process)
					}
				}
			}

			// 2. the Daily Attn Summary logic, from the raw rows, for the people of the roster of the process. The summary
			// counts a person in the shift of the calendar; the board counts a person who has a task in the shift of the
			// record, because that is where the scan put them. They differ only for the people whose record is in another
			// shift than the calendar: a swap (scheduled AM, recorded PM) and overtime on a day off (day type R, recorded AM).
			py := pythonSummary(svc, d, shift)
			got := board.Totals
			out, in := swapped(svc, d, shift)
			mismatch := []string{}
			if len(out)+len(in) == 0 {
				if py.scheduled != got.Scheduled {
					mismatch = append(mismatch, fmt.Sprintf("scheduled python=%d board=%d", py.scheduled, got.Scheduled))
				}
				if !near(py.actual, got.PlacedHC) {
					mismatch = append(mismatch, fmt.Sprintf("assigned python=%v board=%v", py.actual, got.PlacedHC))
				}
				if !near(py.leave, got.LeaveHC) {
					mismatch = append(mismatch, fmt.Sprintf("leave python=%v board=%v", py.leave, got.LeaveHC))
				}
				// per machine: what the board has on a card for the people of the roster, against the summary by recorded name
				for _, m := range board.Machines {
					var sum float64
					for _, p := range m.Members {
						if person, known := d.dir.Lookup(p.EE); known {
							if ok, _ := d.dir.InRoster(person, process); ok {
								sum += p.HC
							}
						}
					}
					if want, ok := py.byMachine[strings.TrimSpace(m.Name)]; ok && !near(want, sum) {
						mismatch = append(mismatch, fmt.Sprintf("machine %q python=%v board=%v", m.Name, want, sum))
					}
				}
			} else if want := py.scheduled - len(out) + len(in); want != got.Scheduled {
				mismatch = append(mismatch, fmt.Sprintf("scheduled python=%d - %d swapped out + %d swapped in or overtime = %d, board=%d", py.scheduled, len(out), len(in), want, got.Scheduled))
			}
			if !near(py.toBeRecorded, got.ToAssignHC) {
				mismatch = append(mismatch, fmt.Sprintf("to assign python=%v board=%v", py.toBeRecorded, got.ToAssignHC))
			}
			sort.Strings(mismatch)
			if len(mismatch) > 0 {
				unequal++
				t.Errorf("%s %s %s: the board and the Daily Attn Summary logic disagree: %s", process, day.Format("2006-01-02"), shift, strings.Join(mismatch, "; "))
			}
			t.Logf("%-8s %s: scheduled=%d assigned=%v leave=%v to_assign=%v elsewhere=%d supporters_on_cards=%d | summary logic: %d %v %v %v | swapped out %d, in or overtime %d",
				process, shift, got.Scheduled, got.PlacedHC, got.LeaveHC, got.ToAssignHC, len(board.Elsewhere), supporters(board), py.scheduled, py.actual, py.leave, py.toBeRecorded, len(out), len(in))
		}
	}
	t.Logf("day %s: %d boards checked, %d with people of another process on a card, %d that differ from the summary logic", day.Format("2006-01-02"), checked, foreignOnCards, unequal)
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.011 }

// swapped lists, for the roster of the process, the people whose record has a task in another shift than the calendar
// says: out are scheduled for the shift and recorded in the other one, in are recorded in the shift and scheduled for the
// other one or for no shift (overtime on a day off).
func swapped(svc *Service, d *dayData, shift string) (out, in []string) {
	for _, p := range d.dir.Roster(d.key.Process) {
		rec, has := d.recByEE[p.EE]
		if !has {
			continue
		}
		if work, _, _ := svc.splitEntries(rec.Entries); len(work) == 0 {
			continue
		}
		cal, recorded := NormShift(d.schedule(p.Shift)), NormShift(firstNonEmpty(rec.Actual, rec.Schedule))
		switch {
		case cal == recorded || recorded == "":
		case cal == shift:
			out = append(out, p.EE)
		case recorded == shift:
			in = append(in, p.EE)
		}
	}
	return out, in
}

func supporters(b *BoardView) int {
	n := 0
	for _, m := range b.Machines {
		for _, p := range m.Members {
			if p.From != "" {
				n++
			}
		}
	}
	return n
}

type dailySummary struct {
	scheduled    int
	actual       float64
	leave        float64
	toBeRecorded float64
	byMachine    map[string]float64
}

// pythonSummary is cal_daily_summary for one process and one shift, written from the rows. The cells are the people of
// the roster (the Python code takes direct type "Direct" only; this module also keeps "Shift Leader", see Roster). The
// shift of a share of the day is the one of the day type of the calendar; on a day that is not a working day (overtime) it
// is the actual shift of the record.
func pythonSummary(svc *Service, d *dayData, shift string) dailySummary {
	out := dailySummary{byMachine: map[string]float64{}}
	for _, p := range d.dir.Roster(d.key.Process) {
		sched := NormShift(d.schedule(p.Shift))
		if sched == shift {
			out.scheduled++
		}
		rec, has := d.recByEE[p.EE]
		if !has || strings.TrimSpace(rec.Actual) == "" {
			if sched == shift {
				out.toBeRecorded++
			}
			continue
		}
		for _, h := range rec.Entries {
			if h.HC <= 0 || strings.TrimSpace(h.Machine) == "" {
				continue
			}
			if svc.isLeave(h.Machine) {
				if sched == shift {
					out.leave += h.HC
				}
				continue
			}
			attributed := sched == shift || (sched == "" && NormShift(rec.Actual) == shift)
			if attributed {
				out.actual += h.HC
				out.byMachine[strings.TrimSpace(h.Machine)] += h.HC
			}
		}
	}
	out.actual, out.leave, out.toBeRecorded = round2(out.actual), round2(out.leave), round2(out.toBeRecorded)
	return out
}
