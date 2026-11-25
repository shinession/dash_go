package hcscan

import (
	"context"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// These tests are about whose entries the board of a process shows and what it says about each person of the shift
// (version 0.1.4). t_daily_attn_data holds the records of every process, and machine names repeat between processes, so
// the board must look at the process an entry names, not only at its machine name.

func (e *testEnv) addRecord(ee, actual, schedule string, entries ...HCEntry) {
	e.repo.records = append(e.repo.records, DayRecord{UID: ee + "_20261119", EE: ee, Date: testDate, Actual: actual, Schedule: schedule, Entries: entries})
}

func (e *testEnv) board(t *testing.T) *BoardView {
	t.Helper()
	b, err := e.svc.Board(context.Background(), testUser, testKey)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func card(t *testing.T, b *BoardView, id string) MachineView {
	t.Helper()
	for _, m := range b.Machines {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("no machine card %q on the board", id)
	return MachineView{}
}

func memberEEs(m MachineView) []string {
	var out []string
	for _, p := range m.Members {
		out = append(out, p.EE)
	}
	sort.Strings(out)
	return out
}

func notAssignedEEs(b *BoardView) string {
	var out []string
	for _, p := range b.NotAssigned {
		out = append(out, p.EE)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// withSharedNames adds the machines the two processes have in common to the test floor: "Group Leader" exists in both
// Winder and Slitter, and Winder has a line named "Winder T20".
func withSharedNames(env *testEnv) {
	env.repo.machines = append(env.repo.machines,
		Machine{ID: "Winder_GL", Name: "Group Leader", Process: "Winder", Type: "Non-routine", Budget: 1, Seq: 5},
		Machine{ID: "Slitter_GL", Name: "Group Leader", Process: "Slitter", Type: "Non-routine", Budget: 1, Seq: 2},
		Machine{ID: "Winder_T20", Name: "Winder T20", Process: "Winder", Type: "Routine", Budget: 1, Seq: 6},
	)
}

// The defect found on 2026-11-20 with the Slitter page: the board showed the group leaders of every process on the
// Group Leader row of the page, and the budget of the row was used up by them.
func TestAMachineWithTheNameOfAnotherProcessDoesNotTakeItsPeople(t *testing.T) {
	env := newTestEnv(t)
	withSharedNames(env)
	env.addRecord("AB12C01", "AM", "W", HCEntry{Process: "Winder", Machine: "Group Leader", HC: 1})
	env.addRecord("AB12C05", "AM", "W", HCEntry{Process: "Slitter", Machine: "Group Leader", HC: 1}) // the group leader of Slitter

	b := env.board(t)
	gl := card(t, b, "Winder_GL")
	if got := memberEEs(gl); len(got) != 1 || got[0] != "AB12C01" || gl.Used != 1 || gl.Over {
		t.Fatalf("only the group leader of Winder is on the Winder row, and the row is not over its budget: %+v", gl)
	}
	if got := env.svc.usage(mustDay(t, env), map[string]Entry{}); got["Winder_GL"] != 1 {
		t.Fatalf("the budget of the row counts the group leader of Winder only: %v", got)
	}
}

// "T20" is how Winder records a line; "Winder T20" or "Taping T20" contain it, and the loose name match of the Python
// port took the Slitter people for people of the Winder line.
func TestAShortMachineNameOfAnotherProcessDoesNotMatchLoosely(t *testing.T) {
	env := newTestEnv(t)
	withSharedNames(env)
	env.addRecord("AB12C05", "AM", "W", HCEntry{Process: "Slitter", Machine: "T20", HC: 1})
	env.addRecord("AB12C02", "AM", "W", HCEntry{Process: "Winder", Machine: "T20", HC: 1}) // the same short name, recorded by Winder

	b := env.board(t)
	if got := memberEEs(card(t, b, "Winder_T20")); len(got) != 1 || got[0] != "AB12C02" {
		t.Fatalf("the loose match is for the entries of this process only: %v", got)
	}
}

// Somebody from another process (the "Others" group, or a person the shift leader placed with the yellow warning) who
// works on a machine of this process is shown on it, tagged with their process, and takes the seat.
func TestSupportFromAnotherProcessIsShownAndTakesTheSeat(t *testing.T) {
	env := newTestEnv(t)
	env.repo.machines[1].Budget = 1 // Winder_520 has one seat
	env.addRecord("AB12C05", "AM", "W", HCEntry{Process: "Winder", Machine: "Winder_520", HC: 1})

	b := env.board(t)
	m := card(t, b, "Winder_520")
	if len(m.Members) != 1 || m.Members[0].EE != "AB12C05" || m.Members[0].From != "Slitter" || m.Used != 1 {
		t.Fatalf("the Slitter member is on the Winder machine with their process: %+v", m)
	}
	if b.Totals.PlacedHC != 0 {
		t.Fatalf("a supporter is not an HC of the Winder shift: %+v", b.Totals)
	}
	res := env.scan(t, "AB12C04") // Lv1 on 520 only, and the seat is taken
	if res.Status != "unassigned" || !strings.Contains(res.Entry.Reason, "are full") {
		t.Fatalf("the seat of the supporter counts for the budget: %s %q", res.Status, res.Entry.Reason)
	}
	for _, p := range card(t, env.board(t), "Winder_515").Members {
		if p.From != "" {
			t.Fatalf("a member has no process tag: %+v", p)
		}
	}
}

func TestEntriesWithoutAProcessBelongToTheProcessOfThePerson(t *testing.T) {
	env := newTestEnv(t)
	withSharedNames(env)
	env.addRecord("AB12C01", "AM", "W", HCEntry{Process: AllProcess, Machine: "Group Leader", HC: 1}) // a Winder person
	env.addRecord("AB12C05", "AM", "W", HCEntry{Process: AllProcess, Machine: "Group Leader", HC: 1}) // a Slitter person
	env.addRecord("AB12C02", "AM", "W", HCEntry{Machine: "Winder_520", HC: 1})                        // no process at all

	b := env.board(t)
	if got := memberEEs(card(t, b, "Winder_GL")); len(got) != 1 || got[0] != "AB12C01" {
		t.Fatalf("an entry of All Process counts for the members of the process only: %v", got)
	}
	if got := memberEEs(card(t, b, "Winder_520")); len(got) != 1 || got[0] != "AB12C02" {
		t.Fatalf("an entry without a process belongs to the process of the person: %v", got)
	}
}

// The machine table has some names with two spaces ("Fix-Shipment/ Logistic  - 4pax") where the records have one. The
// people on them were on no card at all: found on 2026-11-20 with the dev copy (CSV, Packing, HSL).
func TestRunsOfSpacesInAMachineNameDoNotHideThePeopleOnIt(t *testing.T) {
	env := newTestEnv(t)
	env.repo.machines = append(env.repo.machines, Machine{ID: "Winder_9227", Name: "Fix-Shipment/ Logistic  - 4pax ( AM Shift only)", Process: "Winder", Type: "Non-routine", Budget: 4, Seq: 7})
	env.addRecord("AB12C01", "AM", "W", HCEntry{Process: "Winder", Machine: "Fix-Shipment/ Logistic - 4pax ( AM Shift only)", HC: 1})

	b := env.board(t)
	if got := memberEEs(card(t, b, "Winder_9227")); len(got) != 1 || got[0] != "AB12C01" || card(t, b, "Winder_9227").Used != 1 {
		t.Fatalf("one space or two is the same machine: %+v", card(t, b, "Winder_9227"))
	}
	if len(b.Elsewhere) != 0 {
		t.Fatalf("nobody is elsewhere: %+v", b.Elsewhere)
	}
	for in, want := range map[string]string{"  a  b ": "a b", "a  b": "a b", "": "", "x": "x"} {
		if got := normName(in); got != want {
			t.Errorf("normName(%q) = %q, want %q", in, got, want)
		}
	}
}

// A person of this process whose task is on a machine of another process has a task: they are not "not assigned", and
// the page lists them in the "Elsewhere" card because no machine card of this process can show them. The same happens
// when the name of the machine in their record is in no machine list (a name written wrongly).
func TestAMemberAssignedToAnotherProcessIsListedElsewhere(t *testing.T) {
	env := newTestEnv(t)
	env.addRecord("AB12C02", "AM", "W", HCEntry{Process: "Slitter", Machine: "Slitter_105", HC: 1})

	b := env.board(t)
	if len(b.Elsewhere) != 1 || b.Elsewhere[0].EE != "AB12C02" || b.Elsewhere[0].HC != 1 || len(b.Elsewhere[0].Where) != 1 || b.Elsewhere[0].Where[0] != "Slitter / Slitter_105" {
		t.Fatalf("elsewhere: %+v", b.Elsewhere)
	}
	if strings.Contains(notAssignedEEs(b), "AB12C02") || b.Totals.PlacedHC != 1 {
		t.Fatalf("somebody with a task elsewhere is placed: not assigned %s, %+v", notAssignedEEs(b), b.Totals)
	}
	for _, m := range b.Machines {
		if len(m.Members) > 0 {
			t.Fatalf("no card of this process shows them: %+v", m)
		}
	}
}

// Version 0.1.4: the people on leave are shown, all kinds together, and the counters add up: scheduled = assigned +
// leave + to assign (in HC).
func TestLeaveIsShownAndTheCountersAddUp(t *testing.T) {
	env := newTestEnv(t)
	env.addRecord("AB12C03", "MCL - Medical Leave", "W", HCEntry{Process: "Winder", Machine: "MCL - Medical Leave", HC: 1}) // the leave of a full day
	env.addRecord("AB12C04", "AM", "W", HCEntry{Process: "Winder", Machine: "ANL - Annual Leave", HC: 0.5}, HCEntry{Process: "Winder", Machine: "Winder_520", HC: 0.5})

	b := env.board(t)
	if len(b.Leave) != 2 {
		t.Fatalf("leave: %+v", b.Leave)
	}
	full, part := b.Leave[0], b.Leave[1] // by name: Carl Three, Dina Four
	if full.EE != "AB12C03" || full.HC != 1 || len(full.Items) != 1 || full.Items[0].Code != "MCL" || full.Items[0].Name != "MCL - Medical Leave" {
		t.Fatalf("full-day leave: %+v", full)
	}
	if part.EE != "AB12C04" || part.HC != 0.5 || part.Items[0].Code != "ANL" {
		t.Fatalf("part-day leave: %+v", part)
	}
	if got := memberEEs(card(t, b, "Winder_520")); len(got) != 1 || got[0] != "AB12C04" {
		t.Fatalf("the part-day leave person works the rest of the day on a machine: %v", got)
	}
	if strings.Contains(notAssignedEEs(b), "AB12C03") || strings.Contains(notAssignedEEs(b), "AB12C04") {
		t.Fatalf("somebody on leave or with a task is not 'not assigned': %s", notAssignedEEs(b))
	}
	tot := b.Totals
	if tot.Scheduled != 5 || tot.PlacedHC != 0.5 || tot.LeaveHC != 1.5 || tot.ToAssignHC != 3 || notAssignedEEs(b) != "AB12C01,AB12C02,LD01A01" {
		t.Fatalf("totals: %+v, not assigned %s", tot, notAssignedEEs(b))
	}
	if sum := float64(tot.PlacedHC + tot.LeaveHC + tot.ToAssignHC); sum != float64(tot.Scheduled) {
		t.Fatalf("scheduled %d must be assigned + leave + to assign = %v", tot.Scheduled, sum)
	}
}

func TestAPartDayLeaveWithoutATaskLeavesTheRestToAssign(t *testing.T) {
	env := newTestEnv(t)
	env.addRecord("AB12C02", "AM", "W", HCEntry{Process: "Winder", Machine: "ANL - Annual Leave", HC: 0.5})

	b := env.board(t)
	var found *PersonView
	for i := range b.NotAssigned {
		if b.NotAssigned[i].EE == "AB12C02" {
			found = &b.NotAssigned[i]
		}
	}
	if found == nil || found.HC != 0.5 || len(b.Leave) != 1 || b.Leave[0].HC != 0.5 {
		t.Fatalf("half a day is still to assign: %+v leave %+v", found, b.Leave)
	}
	if b.Totals.LeaveHC != 0.5 || b.Totals.ToAssignHC != 4.5 {
		t.Fatalf("totals: %+v", b.Totals)
	}
}

// The scan only helps to assign. A person whom the shift leader assigned on another page is placed, not "not scanned".
func TestSomebodyAlreadyAssignedIsNeverListedAsNotAssigned(t *testing.T) {
	env := newTestEnv(t)
	env.addRecord("AB12C04", "AM", "W", HCEntry{Process: "Winder", Machine: "Winder_520", HC: 1})

	b := env.board(t)
	if strings.Contains(notAssignedEEs(b), "AB12C04") || b.Totals.PlacedHC != 1 {
		t.Fatalf("assigned in the database: not assigned %s, %+v", notAssignedEEs(b), b.Totals)
	}
	// scanning them only shows the task; nothing is drafted for the database
	res := env.scan(t, "AB12C04")
	if res.Status != "existing" || res.Entry.MachineID != "Winder_520" {
		t.Fatalf("got %s %+v", res.Status, res.Entry)
	}
	// the scan puts a line in the draft list, but there is nothing to confirm: the Confirm button stays off (Assigned 0)
	if b = env.board(t); strings.Contains(notAssignedEEs(b), "AB12C04") || b.Totals.PlacedHC != 1 || b.Totals.Pending != 1 || b.Totals.Assigned != 0 || b.Totals.Unassigned != 0 {
		t.Fatalf("after the scan: not assigned %s, %+v", notAssignedEEs(b), b.Totals)
	}
}

func TestScanningSomebodyWhoseTaskIsOnAnotherProcessShowsWhere(t *testing.T) {
	env := newTestEnv(t)
	env.addRecord("AB12C04", "AM", "W", HCEntry{Process: "Slitter", Machine: "Slitter_105", HC: 1})

	res := env.scan(t, "AB12C04")
	if res.Status != "existing" || res.Entry.MachineID != "" || res.Entry.MachineName != "Slitter_105" {
		t.Fatalf("the machine of another process is named, not resolved: %s %+v", res.Status, res.Entry)
	}
}

// A line that the shift leader may still confirm replaces the saved record on the board: a full-day leave that they
// override is not leave any more (Confirm drops it), a part-day leave stays.
func TestAPendingLineReplacesTheSavedRecordOnTheBoard(t *testing.T) {
	env := newTestEnv(t)
	env.addRecord("AB12C01", "MCL - Medical Leave", "W", HCEntry{Process: "Winder", Machine: "MCL - Medical Leave", HC: 1})
	env.addRecord("AB12C02", "AM", "W", HCEntry{Process: "Winder", Machine: "ANL - Annual Leave", HC: 0.5})

	before := env.board(t)
	if before.Totals.LeaveHC != 1.5 || len(before.Leave) != 2 {
		t.Fatalf("before: %+v", before.Totals)
	}
	env.scan(t, "AB12C01") // Lv3 on 515: the leave of a whole day is overridden (yellow warning)
	env.scan(t, "AB12C02") // Lv3 on 520: 0.5 HC is left for the machine
	b := env.board(t)
	if b.Totals.LeaveHC != 0.5 || len(b.Leave) != 1 || b.Leave[0].EE != "AB12C02" {
		t.Fatalf("only the part-day leave is left: %+v %+v", b.Totals, b.Leave)
	}
	if b.Totals.PlacedHC != 1.5 || b.Totals.Assigned != 2 {
		t.Fatalf("two pending lines, 1 + 0.5 HC: %+v", b.Totals)
	}
}

// A person who works on this shift without being scheduled for it (overtime on a day off, a swap) belongs to the shift
// of their record.
func TestAPersonBelongsToTheShiftOfTheirRecord(t *testing.T) {
	env := newTestEnv(t)
	env.addRecord("AB12C06", "AM", "PM", HCEntry{Process: "Winder", Machine: "Winder_515", HC: 1}) // scheduled PM, works AM
	env.addRecord("AB12C07", "AM", "O", HCEntry{Process: "Winder", Machine: "Winder_520", HC: 1})  // a day off, works AM

	b := env.board(t)
	if b.Totals.Scheduled != 7 || b.Totals.PlacedHC != 2 {
		t.Fatalf("the two belong to the AM board: %+v", b.Totals)
	}
	pm := testKey
	pm.Shift = ShiftPM
	b, err := env.svc.Board(context.Background(), testUser, pm)
	if err != nil {
		t.Fatal(err)
	}
	if b.Totals.Scheduled != 0 {
		t.Fatalf("and not to the PM board: %+v", b.Totals)
	}
}

// The days of the last week that rank the machines are the days on machines of this process.
func TestRecentDaysCountOnlyEntriesOfThisProcess(t *testing.T) {
	env := newTestEnv(t)
	env.repo.records[0].Entries[0].Process = "Slitter"
	env.repo.records[1].Entries[0].Process = "Slitter"
	res := env.scan(t, "AB12C02")
	if res.Entry.MachineID != "Winder_520" || res.Entry.RecentDays != 1 {
		t.Fatalf("one of the three days was on a machine of Winder: %+v", res.Entry)
	}
}

func mustDay(t *testing.T, env *testEnv) *dayData {
	t.Helper()
	d, err := env.svc.loadDay(context.Background(), testKey)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestTheBoardJSONHasTheKeysThePageReads(t *testing.T) {
	h := newHTTPEnv(t)
	h.addRecord("AB12C03", "MCL - Medical Leave", "W", HCEntry{Process: "Winder", Machine: "MCL - Medical Leave", HC: 1})
	board := decode(t, h.do(http.MethodGet, httpPrefix+"/api/board?process=Winder&date=2026-11-19&shift=AM", "", true))
	for _, k := range []string{"machines", "entries", "not_assigned", "other_shift", "off_day", "leave", "elsewhere"} {
		if _, ok := board[k].([]any); !ok {
			t.Errorf("%q must be a list, never null: %v", k, board[k])
		}
	}
	if _, old := board["not_scanned"]; old {
		t.Error("not_scanned is gone: the list is not_assigned")
	}
	totals := board["totals"].(map[string]any)
	for _, k := range []string{"scheduled", "placed_hc", "leave_hc", "to_assign_hc", "pending", "saved", "assigned", "unassigned", "warnings"} {
		if _, ok := totals[k].(float64); !ok {
			t.Errorf("totals.%s is missing: %v", k, totals)
		}
	}
	leave := board["leave"].([]any)[0].(map[string]any)
	if leave["ee"] != "AB12C03" || leave["items"].([]any)[0].(map[string]any)["code"] != "MCL" {
		t.Errorf("leave: %v", leave)
	}
}

// Every key the script reads from the board must be one the server sends: a typo would only show as a blank card.
func TestTheScriptReadsOnlyKeysTheBoardHas(t *testing.T) {
	js, err := os.ReadFile("web/static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	h := newHTTPEnv(t)
	board := decode(t, h.do(http.MethodGet, httpPrefix+"/api/board?process=Winder&date=2026-11-19&shift=AM", "", true))
	totals := board["totals"].(map[string]any)
	boardKeys := regexp.MustCompile(`\bb\.([a-z_]+)`)
	totalKeys := regexp.MustCompile(`\btotals\.([a-z_]+)`)
	seen := 0
	for _, m := range boardKeys.FindAllStringSubmatch(string(js), -1) {
		seen++
		if _, ok := board[m[1]]; !ok {
			t.Errorf("app.js reads b.%s, which the board does not have", m[1])
		}
	}
	for _, m := range totalKeys.FindAllStringSubmatch(string(js), -1) {
		seen++
		if _, ok := totals[m[1]]; !ok {
			t.Errorf("app.js reads totals.%s, which the board does not have", m[1])
		}
	}
	if seen < 15 {
		t.Fatalf("only %d reads found: the test no longer sees the script", seen)
	}
}

// Version 0.1.9: supplementary candidate popups list unassigned people from the other shift and off/rest/PH days,
// marking anyone who already worked 6 consecutive previous days as blocked (cannot work a 7th day in a row).
func TestBoardCandidatesOtherShiftAndOffDayWithSixDayRule(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.repo.cal["RB"] = "R"
	env.repo.cal["RC"] = "PHO"
	env.repo.cal["RD"] = "PHR"
	env.repo.cal["RE"] = "PH"
	env.repo.cal["RF"] = "" // blank schedule: excluded
	mes := func(ee, name, shift string) MESRow {
		return MESRow{EE: ee, Name: name, CostCenter: "2085", Shift: shift, EffectiveMth: d(2026, 11, 1)}
	}
	env.repo.mes = append(env.repo.mes,
		mes("AB12C11", "Kira Blocked6", "RB"),     // R, worked all 6 previous days -> blocked
		mes("AB12C12", "Liam FiveDays", "RC"),     // PHO, worked 5 previous days (missed day -6) -> eligible
		mes("AB12C13", "Mona LeaveBreak", "RD"),   // PHR, full-day leave on day -3 breaks 6-day streak -> eligible
		mes("AB12C14", "Noah HalfDayWork", "RE"),  // PH, worked 6 days incl. 0.5 leave + 0.5 work -> blocked
		mes("AB12C15", "Owen BlankSched", "RF"),   // blank schedule -> excluded
		mes("AB12C16", "Pia PMPartLeave", "PB"),   // PM with 0.5 leave, no work -> other_shift with HC 0.5
		mes("AB12C17", "Quin PMFullLeave", "PB"),  // PM with full-day leave -> excluded
		mes("AB12C18", "Rita PMAssigned", "PB"),   // PM already assigned in DB -> excluded
		mes("AB12C19", "Sam PMDraft", "PB"),       // PM placed in PM draft -> excluded
	)
	// Previous 6 days: testDate - 6 .. testDate - 1
	for i := 1; i <= 6; i++ {
		day := testDate.AddDate(0, 0, -i)
		uid := func(ee string) string { return ee + "_" + day.Format("20060102") }
		// Kira: worked all 6 days (day -2 on Slitter to verify cross-process check)
		proc, mach := "Winder", "Winder_515"
		if i == 2 {
			proc, mach = "Slitter", "Slitter_105"
		}
		env.repo.records = append(env.repo.records, DayRecord{UID: uid("AB12C11"), EE: "AB12C11", Date: day, Actual: "AM", Entries: []HCEntry{{Process: proc, Machine: mach, HC: 1}}})
		// Liam: worked days -1..-5 only
		if i <= 5 {
			env.repo.records = append(env.repo.records, DayRecord{UID: uid("AB12C12"), EE: "AB12C12", Date: day, Actual: "AM", Entries: []HCEntry{{Process: "Winder", Machine: "Winder_515", HC: 1}}})
		}
		// Mona: worked 5 days, full-day MCL on day -3
		if i == 3 {
			env.repo.records = append(env.repo.records, DayRecord{UID: uid("AB12C13"), EE: "AB12C13", Date: day, Actual: "MCL - Medical Leave", Entries: []HCEntry{{Process: "Winder", Machine: "MCL - Medical Leave", HC: 1}}})
		} else {
			env.repo.records = append(env.repo.records, DayRecord{UID: uid("AB12C13"), EE: "AB12C13", Date: day, Actual: "AM", Entries: []HCEntry{{Process: "Winder", Machine: "Winder_515", HC: 1}}})
		}
		// Noah: worked all 6 days, day -4 had 0.5 ANL + 0.5 work
		if i == 4 {
			env.repo.records = append(env.repo.records, DayRecord{UID: uid("AB12C14"), EE: "AB12C14", Date: day, Actual: "AM", Entries: []HCEntry{{Process: "Winder", Machine: "ANL - Annual Leave", HC: 0.5}, {Process: "Winder", Machine: "Winder_520", HC: 0.5}}})
		} else {
			env.repo.records = append(env.repo.records, DayRecord{UID: uid("AB12C14"), EE: "AB12C14", Date: day, Actual: "AM", Entries: []HCEntry{{Process: "Winder", Machine: "Winder_520", HC: 1}}})
		}
	}
	// Today's records for PM members
	env.addRecord("AB12C16", "PM", "PM", HCEntry{Process: "Winder", Machine: "ANL - Annual Leave", HC: 0.5})
	env.addRecord("AB12C17", "MCL - Medical Leave", "PM", HCEntry{Process: "Winder", Machine: "MCL - Medical Leave", HC: 1})
	env.addRecord("AB12C18", "PM", "PM", HCEntry{Process: "Winder", Machine: "Winder_520", HC: 1})
	// Place Sam in PM draft
	pmKey := Key{Process: "Winder", Date: testDate, Shift: ShiftPM}
	if err := env.svc.Drafts.PutEntry(ctx, pmKey, Entry{EE: "AB12C19", Name: "Sam PMDraft", Source: SourceManual, MachineID: "Winder_520", MachineIDs: []string{"Winder_520"}, HC: 1}); err != nil {
		t.Fatal(err)
	}

	b := env.board(t)
	// OtherShift: Finn Six (AB12C06, HC 1) and Pia PMPartLeave (AB12C16, HC 0.5)
	if len(b.OtherShift) != 2 || b.OtherShift[0].EE != "AB12C06" || b.OtherShift[0].HC != 1 || b.OtherShift[1].EE != "AB12C16" || b.OtherShift[1].HC != 0.5 {
		t.Fatalf("other_shift: %+v", b.OtherShift)
	}
	// OffDay: eligible first (Gus AB12C07, Liam AB12C12, Mona AB12C13), then blocked (Kira AB12C11, Noah AB12C14). Owen (blank) excluded.
	if len(b.OffDay) != 5 {
		t.Fatalf("off_day len=%d: %+v", len(b.OffDay), b.OffDay)
	}
	wantOff := []struct {
		ee      string
		blocked bool
		days    int
	}{
		{"AB12C07", false, 0},
		{"AB12C12", false, 5},
		{"AB12C13", false, 2},
		{"AB12C11", true, 6},
		{"AB12C14", true, 6},
	}
	for i, w := range wantOff {
		got := b.OffDay[i]
		if got.EE != w.ee || got.Blocked != w.blocked || got.ConsecutiveDays != w.days {
			t.Errorf("off_day[%d] = %+v, want ee=%s blocked=%v days=%d", i, got, w.ee, w.blocked, w.days)
		}
	}

	// Clicking/scanning an other-shift person with NO MTR record (SourceNone) removes them immediately from other_shift
	res := env.scan(t, "AB12C06")
	if res.Status != "unassigned" || !hasCode(res.Entry.Flags, FlagShiftMismatch) {
		t.Fatalf("expected unassigned with shift mismatch: %s %+v", res.Status, res.Entry.Flags)
	}
	b2 := env.board(t)
	if len(b2.OtherShift) != 1 || b2.OtherShift[0].EE != "AB12C16" {
		t.Fatalf("AB12C06 must disappear from other_shift once scanned into draft: %+v", b2.OtherShift)
	}
	// Removing AB12C06 from draft returns them to other_shift
	if err := env.svc.Remove(ctx, testUser, testKey, "AB12C06"); err != nil {
		t.Fatal(err)
	}
	if b3 := env.board(t); len(b3.OtherShift) != 2 {
		t.Fatalf("AB12C06 must reappear after Remove: %+v", b3.OtherShift)
	}
}
