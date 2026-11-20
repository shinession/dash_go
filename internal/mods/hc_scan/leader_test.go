package hcscan

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// newLeaderEnv adds a "Group Leader" row (budget 2) and these people to the usual test floor:
//
//	AB12C20 direct type Shift Leader (membership)    AB12C21 last record on the Group Leader row
//	AB12C22 was on the row, last record on a machine AB12C23 last record is a leave day, the one before is the row
//	AB12C24 last record on the row (a third leader)  AB12C25 operator without any history
func newLeaderEnv(t *testing.T) *testEnv {
	t.Helper()
	env := newTestEnv(t)
	env.repo.machines = append(env.repo.machines, Machine{ID: "Winder_9001", Name: "Group Leader", Process: "Winder", Type: "Non-routine", Budget: 2, Seq: 5})
	for _, ee := range []string{"AB12C20", "AB12C21", "AB12C22", "AB12C23", "AB12C24", "AB12C25"} {
		env.repo.mes = append(env.repo.mes, MESRow{EE: ee, Name: "Person " + ee[5:], CostCenter: "2085", Shift: "PA", EffectiveMth: d(2026, 11, 1)})
	}
	env.repo.members = append(env.repo.members, MemberRow{EE: "AB12C20", Name: "Person 20", Process: "Winder", Direct: "Shift Leader", Shift: "PA", Effective: d(2026, 11, 1), End: d(2099, 12, 31)})

	day := func(n int) time.Time { return testDate.AddDate(0, 0, -n) }
	add := func(ee string, daysAgo int, machine string) {
		date := day(daysAgo)
		env.repo.records = append(env.repo.records, DayRecord{UID: fmt.Sprintf("%s_%s", ee, date.Format("20060102")), EE: ee, Date: date, Actual: "AM", Entries: []HCEntry{{Process: "Winder", Machine: machine, HC: 1}}})
	}
	add("AB12C21", 3, "Winder_515")
	add("AB12C21", 1, "Group Leader")
	add("AB12C22", 3, "Group Leader")
	add("AB12C22", 1, "Winder_520")
	add("AB12C23", 2, "Group Leader")
	add("AB12C23", 1, "MCL - Medical Leave")
	add("AB12C24", 1, "Group Leader")
	return env
}

func TestLeaderByDirectTypePlacedOnTheLeaderRow(t *testing.T) {
	env := newLeaderEnv(t)
	res := env.scan(t, "AB12C20")
	e := res.Entry
	if res.Status != "assigned" || e.MachineID != "Winder_9001" || e.Source != SourceAuto || e.Level != -1 || e.HC != 1 || !strings.Contains(e.Reason, "Shift Leader (direct type)") {
		t.Fatalf("a Shift Leader goes to the Group Leader row: %s %+v", res.Status, e)
	}
	if hasWarnFlags(e.Flags) {
		t.Fatalf("a Shift Leader of the process is on the roster and needs no warning: %+v", e.Flags)
	}
}

func TestLeaderByHistoryUsesTheMostRecentRecord(t *testing.T) {
	env := newLeaderEnv(t)
	last := env.scan(t, "AB12C21")
	if last.Entry.MachineID != "Winder_9001" || !strings.Contains(last.Entry.Reason, "Group Leader at the last record") {
		t.Fatalf("last record on the row: %+v", last.Entry)
	}
	relief := env.scan(t, "AB12C22")
	if relief.Status != "unassigned" || relief.Entry.MachineID != "" {
		t.Fatalf("somebody who went back to a machine is no leader (and has no MTR record): %+v", relief.Entry)
	}
	afterLeave := env.scan(t, "AB12C23")
	if afterLeave.Entry.MachineID != "Winder_9001" {
		t.Fatalf("a leave day says nothing, the record before it counts: %+v", afterLeave.Entry)
	}
	operator := env.scan(t, "AB12C25")
	if operator.Status != "unassigned" {
		t.Fatalf("an operator without history is never put on the leader row: %+v", operator.Entry)
	}
}

func TestLeaderRowRespectsItsBudget(t *testing.T) {
	env := newLeaderEnv(t)
	env.scan(t, "AB12C20")
	env.scan(t, "AB12C21")
	third := env.scan(t, "AB12C24")
	if third.Status != "unassigned" || !strings.Contains(third.Entry.Reason, "row is full (2 / 2 HC)") {
		t.Fatalf("the third leader finds the row full: %s %q", third.Status, third.Entry.Reason)
	}
	if !hasCode(third.Entry.Flags, FlagNoMachine) {
		t.Fatalf("it is flagged like every line without machine: %+v", third.Entry.Flags)
	}
}

func TestLeaderByHistoryCanBeSwitchedOff(t *testing.T) {
	env := newLeaderEnv(t)
	env.svc.Settings.LeaderByHistory = false
	if res := env.scan(t, "AB12C21"); res.Status != "unassigned" {
		t.Fatalf("with leader_by_history off only the direct type counts: %+v", res.Entry)
	}
	if res := env.scan(t, "AB12C20"); res.Entry.MachineID != "Winder_9001" {
		t.Fatalf("the direct type always counts: %+v", res.Entry)
	}
}

func TestLeaderTakesTheLeaderRowEvenWithMTRRecords(t *testing.T) {
	env := newLeaderEnv(t)
	env.repo.mtr.Levels["AB12C21"] = map[string]int{"C515": 3}
	if res := env.scan(t, "AB12C21"); res.Entry.MachineID != "Winder_9001" {
		t.Fatalf("a leader is placed on the leader row, not by MTR level: %+v", res.Entry)
	}
}

func TestWithoutALeaderRowTheRankingStillRuns(t *testing.T) {
	env := newLeaderEnv(t)
	env.svc.Settings.LeaderActivityNames = []string{"No such row"}
	env.repo.mtr.Levels["AB12C20"] = map[string]int{"C520": 2}
	if res := env.scan(t, "AB12C20"); res.Entry.MachineID != "Winder_520" || res.Entry.Level != 2 {
		t.Fatalf("no leader row: the normal ranking applies: %+v", res.Entry)
	}
}

func TestLeaderRowNameIsMatchedLoosely(t *testing.T) {
	env := newLeaderEnv(t)
	env.svc.Settings.LeaderActivityNames = []string{"  group LEADER "}
	if res := env.scan(t, "AB12C20"); res.Entry.MachineID != "Winder_9001" {
		t.Fatalf("name match ignores case and spaces: %+v", res.Entry)
	}
}

func TestExistingRecordBeatsTheLeaderRule(t *testing.T) {
	env := newLeaderEnv(t)
	env.repo.records = append(env.repo.records, DayRecord{UID: "AB12C20_20261119", EE: "AB12C20", Date: testDate, Actual: "AM", Schedule: "W", Entries: []HCEntry{{Process: "Winder", Machine: "Winder_520", HC: 1}}})
	res := env.scan(t, "AB12C20")
	if res.Status != "existing" || res.Entry.MachineID != "Winder_520" {
		t.Fatalf("a record in the database is never replaced by the automatic rule: %s %+v", res.Status, res.Entry)
	}
}

func TestLeaderLineIsSavedWithoutAcknowledgement(t *testing.T) {
	env := newLeaderEnv(t)
	env.scan(t, "AB12C20")
	res, err := env.svc.Confirm(context.Background(), testUser, testKey, false)
	if err != nil || len(res.Written) != 1 || len(res.NeedsAck) != 0 {
		t.Fatalf("a clean leader line needs no acknowledgement: %+v %v", res, err)
	}
	row := env.repo.written[0][0]
	if len(row.Entries) != 1 || row.Entries[0] != (HCEntry{Process: "Winder", Machine: "Group Leader", HC: 1}) || row.Actual != "AM" {
		t.Fatalf("written row: %+v", row)
	}
}
