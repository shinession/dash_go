package hcscan

import (
	"context"
	"strings"
	"testing"
)

func TestScanAssignsByLevelFirst(t *testing.T) {
	env := newTestEnv(t)
	res := env.scan(t, "AB12C01")
	e := res.Entry
	if res.Status != "assigned" || e.MachineID != "Winder_515" || e.Level != 3 || e.Source != SourceAuto || e.HC != 1 {
		t.Fatalf("Lv3 on 515 must win over Lv2 on 520: %+v", e)
	}
	if hasWarnFlags(e.Flags) {
		t.Fatalf("a rostered, scheduled person must have no warning: %+v", e.Flags)
	}
}

func hasWarnFlags(flags []FlagView) bool {
	for _, f := range flags {
		if f.Severity == SeverityWarn {
			return true
		}
	}
	return false
}

func TestScanRecentDaysAreShownAndCounted(t *testing.T) {
	env := newTestEnv(t)
	res := env.scan(t, "AB12C02") // Lv3 on 520 and worked it on 3 of the last 7 days
	e := res.Entry
	if e.MachineID != "Winder_520" || e.RecentDays != 3 || !strings.Contains(e.Reason, "worked there 3 of the last 7 days") {
		t.Fatalf("unexpected assignment: %+v", e)
	}
}

func TestScanFullMachineGoesToTheNextQualifiedOne(t *testing.T) {
	env := newTestEnv(t)
	env.scan(t, "AB12C01")        // takes the only seat of 515
	res := env.scan(t, "AB12C02") // Lv1 on 515 (full), Lv3 on 520
	if res.Entry.MachineID != "Winder_520" {
		t.Fatalf("got %+v", res.Entry)
	}
}

func TestScanWithoutMTRRecordIsNeverAssignedAutomatically(t *testing.T) {
	env := newTestEnv(t)
	res := env.scan(t, "AB12C03")
	e := res.Entry
	if res.Status != "unassigned" || e.Source != SourceNone || e.MachineID != "" {
		t.Fatalf("no MTR record must not be assigned: %+v", e)
	}
	if !hasCode(e.Flags, FlagNoMachine) || !strings.Contains(e.Reason, "No MTR record") {
		t.Fatalf("the reason must say why: %q %+v", e.Reason, e.Flags)
	}
}

func TestScanAllQualifiedMachinesFull(t *testing.T) {
	env := newTestEnv(t)
	env.repo.machines[1].Budget = 1 // Winder_520 has one seat only
	env.scan(t, "AB12C02")          // takes it
	res := env.scan(t, "AB12C04")   // Lv1 on 520 only
	if res.Status != "unassigned" || !strings.Contains(res.Entry.Reason, "are full (no room for 1 HC)") {
		t.Fatalf("got %s %q", res.Status, res.Entry.Reason)
	}
}

func TestScanDuplicateDoesNotAssignTwice(t *testing.T) {
	env := newTestEnv(t)
	env.scan(t, "AB12C01")
	res := env.scan(t, "ab12c01\r")
	if res.Status != "duplicate" || res.Entry.MachineID != "Winder_515" {
		t.Fatalf("got %s %+v", res.Status, res.Entry)
	}
	b, err := env.svc.Board(context.Background(), testUser, testKey)
	if err != nil {
		t.Fatal(err)
	}
	if b.Totals.Pending != 1 || b.Machines[0].Used != 1 {
		t.Fatalf("a repeated scan must not change the board: %+v", b.Totals)
	}
}

func TestScanRejectsUnknownAndInvalidInput(t *testing.T) {
	env := newTestEnv(t)
	_, err := env.svc.Scan(context.Background(), testUser, testKey, "ZZ99Z99")
	wantErr(t, err, 404, "unknown_employee")
	_, err = env.svc.Scan(context.Background(), testUser, testKey, "hello")
	wantErr(t, err, 400, "invalid_id")
}

func TestScanFlagsPeopleOutsideTheRoster(t *testing.T) {
	env := newTestEnv(t)
	env.scan(t, "AB12C01") // fills 515
	res := env.scan(t, "AB12C05")
	e := res.Entry
	if !hasCode(e.Flags, FlagNotInRoster) {
		t.Fatalf("a Slitter member on the Winder page must be flagged: %+v", e.Flags)
	}
	if !strings.Contains(flagText(e.Flags, FlagNotInRoster), "member of Slitter") {
		t.Fatalf("the reason must name the process: %q", flagText(e.Flags, FlagNotInRoster))
	}
	if e.Source != SourceNone {
		t.Fatalf("515 is full, nothing is left for Lv2 on 515: %+v", e)
	}
}

func TestScanFlagsShiftMismatchAndDaysOff(t *testing.T) {
	env := newTestEnv(t)
	env.repo.mtr.Levels["AB12C06"] = map[string]int{"C520": 2}
	env.repo.mtr.Levels["AB12C07"] = map[string]int{"C520": 2}
	pm := env.scan(t, "AB12C06")
	if !strings.Contains(flagText(pm.Entry.Flags, FlagShiftMismatch), "Scheduled PM") || pm.Entry.MachineID != "Winder_520" {
		t.Fatalf("a PM person scanning in the AM shift is flagged but still assigned: %+v", pm.Entry)
	}
	off := env.scan(t, "AB12C07")
	if !strings.Contains(flagText(off.Entry.Flags, FlagShiftMismatch), "Not scheduled to work") {
		t.Fatalf("a day off must be flagged: %+v", off.Entry.Flags)
	}
}

func TestScanLeaveHandling(t *testing.T) {
	env := newTestEnv(t)
	env.repo.records = append(env.repo.records,
		DayRecord{UID: "AB12C01_20261119", EE: "AB12C01", Date: testDate, Actual: "MCL - Medical Leave", Schedule: "W", Entries: []HCEntry{{Process: "Winder", Machine: "MCL - Medical Leave", HC: 1}}},
		DayRecord{UID: "AB12C02_20261119", EE: "AB12C02", Date: testDate, Actual: "AM", Schedule: "W", Entries: []HCEntry{{Process: "Winder", Machine: "MCL - Medical Leave", HC: 0.5}}},
	)
	full := env.scan(t, "AB12C01")
	if !hasCode(full.Entry.Flags, FlagFullDayLeave) || full.Entry.HC != 1 || full.Entry.MachineID != "Winder_515" {
		t.Fatalf("a full-day leave person is flagged and assigned the whole day: %+v", full.Entry)
	}
	half := env.scan(t, "AB12C02")
	if hasWarnFlags(half.Entry.Flags) || !hasCode(half.Entry.Flags, FlagPartDayLeave) || half.Entry.HC != 0.5 {
		t.Fatalf("a part-day leave is only informational and leaves 0.5 HC: %+v", half.Entry)
	}
}

func TestScanKeepsAnExistingAssignment(t *testing.T) {
	env := newTestEnv(t)
	env.repo.records = append(env.repo.records, DayRecord{UID: "AB12C04_20261119", EE: "AB12C04", Date: testDate, Actual: "AM", Schedule: "W", Entries: []HCEntry{{Process: "Winder", Machine: "Winder_520", HC: 1}}})
	res := env.scan(t, "AB12C04")
	if res.Status != "existing" || res.Entry.Source != SourceExisting || res.Entry.MachineID != "Winder_520" {
		t.Fatalf("got %s %+v", res.Status, res.Entry)
	}
	b, _ := env.svc.Board(context.Background(), testUser, testKey)
	if b.Machines[1].Used != 1 {
		t.Fatalf("the existing record counts once on its machine: %+v", b.Machines[1])
	}
}

func TestPermissionIsChecked(t *testing.T) {
	env := newTestEnv(t)
	env.perms.procs = []string{"Slitter"}
	_, err := env.svc.Scan(context.Background(), testUser, testKey, "AB12C01")
	wantErr(t, err, 403, "forbidden")
	env.perms.procs = []string{"Winder"}
	bad := testKey
	bad.Process = "Nope"
	_, err = env.svc.Board(context.Background(), testUser, bad)
	wantErr(t, err, 400, "bad_process")
	bad = testKey
	bad.Shift = "XX"
	_, err = env.svc.Board(context.Background(), testUser, bad)
	wantErr(t, err, 400, "bad_shift")
}

func TestManualAssignmentFlags(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.scan(t, "AB12C01") // 515 is now full
	env.scan(t, "AB12C03") // unassigned

	v, err := env.svc.Assign(ctx, testUser, testKey, "AB12C03", "Winder_520")
	if err != nil {
		t.Fatal(err)
	}
	if v.Source != SourceManual || !hasCode(v.Flags, FlagNoMTR) {
		t.Fatalf("a manual choice without MTR record is allowed but flagged: %+v", v.Flags)
	}
	v, _ = env.svc.Assign(ctx, testUser, testKey, "AB12C03", "Winder_515")
	if !hasCode(v.Flags, FlagOverBudget) || !hasWarnFlags(v.Flags) {
		t.Fatalf("515 would be over its budget: %+v", v.Flags)
	}
	v, _ = env.svc.Assign(ctx, testUser, testKey, "AB12C03", "Winder_525")
	if !strings.Contains(flagText(v.Flags, FlagNoMTR), "no MTR mapping") {
		t.Fatalf("a routine machine without MTR mapping is flagged: %+v", v.Flags)
	}
	v, _ = env.svc.Assign(ctx, testUser, testKey, "AB12C03", "Winder_9014")
	if hasCode(v.Flags, FlagNoMTR) || hasWarnFlags(v.Flags) {
		t.Fatalf("an activity needs no MTR record and has no budget warning: %+v", v.Flags)
	}
	v, _ = env.svc.Assign(ctx, testUser, testKey, "AB12C03", "")
	if v.Source != SourceNone || v.MachineID != "" {
		t.Fatalf("an empty machine removes the assignment: %+v", v.Entry)
	}
	_, err = env.svc.Assign(ctx, testUser, testKey, "AB12C03", "Nope")
	wantErr(t, err, 400, "bad_machine")
	_, err = env.svc.Assign(ctx, testUser, testKey, "AB12C04", "Winder_520")
	wantErr(t, err, 404, "not_in_draft")
}

func TestRemoveAndResetForgetTheScan(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.scan(t, "AB12C01")
	env.scan(t, "AB12C02")
	if err := env.svc.Remove(ctx, testUser, testKey, "AB12C01"); err != nil {
		t.Fatal(err)
	}
	b, _ := env.svc.Board(ctx, testUser, testKey)
	if b.Totals.Pending != 1 || b.Totals.Scanned != 1 {
		t.Fatalf("after Remove: %+v", b.Totals)
	}
	if err := env.svc.Reset(ctx, testUser, testKey); err != nil {
		t.Fatal(err)
	}
	b, _ = env.svc.Board(ctx, testUser, testKey)
	if b.Totals.Pending != 0 || b.Totals.Scanned != 0 || len(b.NotScanned) != b.Totals.Expected {
		t.Fatalf("after Reset: %+v notScanned=%d", b.Totals, len(b.NotScanned))
	}
}

func TestBoardTotalsAndLists(t *testing.T) {
	env := newTestEnv(t)
	env.scan(t, "AB12C01")
	env.scan(t, "AB12C03")
	b, err := env.svc.Board(context.Background(), testUser, testKey)
	if err != nil {
		t.Fatal(err)
	}
	if b.Totals.Expected != 5 || b.Totals.ExpectedScanned != 2 || b.Totals.Assigned != 1 || b.Totals.Unassigned != 1 || b.Totals.Warnings != 1 {
		t.Fatalf("totals: %+v", b.Totals)
	}
	for _, p := range b.NotScanned {
		if p.EE == "AB12C01" || p.EE == "AB12C03" || p.EE == "AB12C06" || p.EE == "AB12C07" || p.EE == "AB12C05" {
			t.Fatalf("%s must not be listed as expected-but-missing", p.EE)
		}
	}
	m := b.Machines[0]
	if m.ID != "Winder_515" || m.Used != 1 || len(m.Members) != 1 || m.Members[0].Saved || !m.MTRMapped {
		t.Fatalf("machine card: %+v", m)
	}
	if b.Machines[2].MTRMapped || b.MTR.Unmapped != 1 || b.MTR.Employees != 4 || b.MTR.DataTime != "2026-11-18 09:00" {
		t.Fatalf("MTR info: %+v unmapped=%v", b.MTR, b.Machines[2].MTRMapped)
	}
	if b.Locked {
		t.Fatal("today is not locked")
	}
}

func TestMeListsOnlyPermittedProcessesInConfigOrder(t *testing.T) {
	env := newTestEnv(t)
	env.perms.procs = []string{"Winder", "Slitter", "Bogus"}
	me, err := env.svc.Me(context.Background(), testUser)
	if err != nil {
		t.Fatal(err)
	}
	if len(me.Processes) != 2 || me.Processes[0] != "Slitter" || me.Config.ProductionDate != "2026-11-19" || me.Config.DefaultShift != ShiftAM {
		t.Fatalf("%+v", me)
	}
}

func flagText(flags []FlagView, code string) string {
	for _, f := range flags {
		if f.Code == code {
			return f.Text
		}
	}
	return ""
}
