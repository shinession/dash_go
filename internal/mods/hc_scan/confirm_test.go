package hcscan

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfirmWritesLikeAreaAssign(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.scan(t, "AB12C01")
	res, err := env.svc.Confirm(ctx, testUser, testKey, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Written) != 1 || len(res.NeedsAck) != 0 || len(env.repo.records) != 4 {
		t.Fatalf("result: %+v records=%d", res, len(env.repo.records))
	}
	row := env.repo.written[0][0]
	if row.UID != "AB12C01_20261119" || row.Actual != "AM" || row.Shift != "PA" || row.Schedule != "W" ||
		len(row.Entries) != 1 || row.Entries[0] != (HCEntry{Process: "Winder", Machine: "Winder_515", HC: 1, MachineID: "Winder_515"}) ||
		row.BuildIn != 1 || row.AddtOT != 0 || row.TotalOT != 1 {
		t.Fatalf("written row: %+v", row)
	}
	raw, err := json.Marshal(row.Entries)
	if err != nil {
		t.Fatal(err)
	}
	if want := `[{"process":"Winder","machine":"Winder_515","hc":1,"machine_id":"Winder_515"}]`; string(raw) != want {
		t.Fatalf("machine_hc_json = %s, want %s", raw, want)
	}
	if env.repo.users[0] != testUser.Name || env.log.calls != 1 || env.perms.fresh != 1 {
		t.Fatalf("user=%v logCalls=%d freshChecks=%d", env.repo.users, env.log.calls, env.perms.fresh)
	}
	b, _ := env.svc.Board(ctx, testUser, testKey)
	if b.Totals.Pending != 0 || b.Totals.Saved != 1 || !b.Machines[0].Members[0].Saved {
		t.Fatalf("after saving: %+v members=%+v", b.Totals, b.Machines[0].Members)
	}
	again := env.scan(t, "AB12C01")
	if again.Status != "saved" {
		t.Fatalf("a saved person who scans again must not be assigned again: %s", again.Status)
	}
}

func TestConfirmNeedsAcknowledgementForWarnings(t *testing.T) {
	env := newTestEnv(t)
	env.repo.mtr.Levels["AB12C06"] = map[string]int{"C520": 2}
	env.scan(t, "AB12C06") // scheduled PM, scanning AM: yellow
	ctx := context.Background()
	res, err := env.svc.Confirm(ctx, testUser, testKey, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.NeedsAck) != 1 || res.NeedsAck[0].EE != "AB12C06" || len(res.Written) != 0 || len(env.repo.written) != 0 {
		t.Fatalf("nothing may be written before the warning is acknowledged: %+v", res)
	}
	res, err = env.svc.Confirm(ctx, testUser, testKey, true)
	if err != nil || len(res.Written) != 1 {
		t.Fatalf("after the acknowledgement it is saved: %+v %v", res, err)
	}
	if got := env.repo.written[0][0]; got.Actual != "AM" || got.Schedule != "PM" || got.BuildIn != 1 {
		t.Fatalf("the actual shift is the scanned session, the schedule stays the calendar one: %+v", got)
	}
}

func TestConfirmKeepsPartDayLeaveAndReplacesFullDayLeave(t *testing.T) {
	env := newTestEnv(t)
	remark := "keep me"
	env.repo.records = append(env.repo.records,
		DayRecord{UID: "AB12C01_20261119", EE: "AB12C01", Date: testDate, Actual: "MCL - Medical Leave", Schedule: "W", Entries: []HCEntry{{Process: "Winder", Machine: "MCL - Medical Leave", HC: 1}}},
		DayRecord{UID: "AB12C02_20261119", EE: "AB12C02", Date: testDate, Actual: "AM", Schedule: "W", Remark: &remark, AddtOT: 2,
			Entries: []HCEntry{{Process: "Winder", Machine: "MCL - Medical Leave", HC: 0.5}}},
	)
	env.scan(t, "AB12C01")
	env.scan(t, "AB12C02")
	if _, err := env.svc.Confirm(context.Background(), testUser, testKey, true); err != nil {
		t.Fatal(err)
	}
	byEE := map[string]DayRecord{}
	for _, r := range env.repo.written[0] {
		byEE[r.EE] = r
	}
	full := byEE["AB12C01"]
	if len(full.Entries) != 1 || full.Entries[0] != (HCEntry{Process: "Winder", Machine: "Winder_515", HC: 1, MachineID: "Winder_515"}) || full.Actual != "AM" {
		t.Fatalf("a confirmed full-day leave exception is replaced by the machine: %+v", full)
	}
	half := byEE["AB12C02"]
	if len(half.Entries) != 2 || half.Entries[0] != (HCEntry{Process: "Winder", Machine: "MCL - Medical Leave", HC: 0.5}) ||
		half.Entries[1] != (HCEntry{Process: "Winder", Machine: "Winder_520", HC: 0.5, MachineID: "Winder_520"}) {
		t.Fatalf("a part-day leave stays (without machine_id) and the rest goes to the machine (with machine_id): %+v", half.Entries)
	}
	rawHalf, err := json.Marshal(half.Entries)
	if err != nil {
		t.Fatal(err)
	}
	var halfObjs []map[string]any
	if err := json.Unmarshal(rawHalf, &halfObjs); err != nil {
		t.Fatal(err)
	}
	if _, hasID := halfObjs[0]["machine_id"]; hasID {
		t.Fatalf("leave entry must omit machine_id in JSON: %s", rawHalf)
	}
	if halfObjs[1]["machine_id"] != "Winder_520" || halfObjs[1]["machine"] != "Winder_520" {
		t.Fatalf("machine entry must carry machine_id and machine snapshot: %s", rawHalf)
	}
	if half.Remark == nil || *half.Remark != "keep me" || half.AddtOT != 2 || half.BuildIn != 0.5 || half.TotalOT != 2.5 {
		t.Fatalf("remark and additional OT are kept, built-in OT follows the HC: %+v", half)
	}
}

func TestConfirmSkipsLinesThatChangedSinceTheScan(t *testing.T) {
	env := newTestEnv(t)
	env.scan(t, "AB12C01")
	// HC Input saves a machine for the person after the scan
	env.repo.records = append(env.repo.records, DayRecord{UID: "AB12C01_20261119", EE: "AB12C01", Date: testDate, Actual: "AM", Schedule: "W", Entries: []HCEntry{{Process: "Winder", Machine: "Winder_520", HC: 1}}})
	res, err := env.svc.Confirm(context.Background(), testUser, testKey, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Written) != 0 || len(res.Skipped) != 1 || res.Skipped[0].EE != "AB12C01" {
		t.Fatalf("an automatic line must not overwrite a newer record: %+v", res)
	}
}

func TestConfirmRefusalsAndFailures(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)
	_, err := env.svc.Confirm(ctx, testUser, testKey, true)
	wantErr(t, err, 409, "nothing_to_save")

	env.scan(t, "AB12C03") // unassigned only
	res, err := env.svc.Confirm(ctx, testUser, testKey, true)
	wantErr(t, err, 409, "nothing_to_save")
	_ = res

	locked := testKey
	locked.Date = d(2026, 10, 30)
	_, err = env.svc.Confirm(ctx, testUser, locked, true)
	wantErr(t, err, 409, "month_locked")

	env.scan(t, "AB12C01")
	env.repo.writeErr = errors.New("connection refused to secret-host")
	_, err = env.svc.Confirm(ctx, testUser, testKey, true)
	wantErr(t, err, 500, "save_failed")
	if msg := err.Error(); msg == "" || containsAny(msg, "secret-host", "connection refused") {
		t.Fatalf("technical details must not reach the browser: %q", msg)
	}
	b, _ := env.svc.Board(ctx, testUser, testKey)
	if b.Totals.Pending != 2 {
		t.Fatalf("a failed save keeps the draft: %+v", b.Totals)
	}

	env.repo.writeErr = nil
	env.log.err = errors.New("share not reachable")
	res, err = env.svc.Confirm(ctx, testUser, testKey, true)
	if err != nil || len(res.Written) != 1 || res.LogWarning == "" || res.Unassigned != 1 {
		t.Fatalf("a failing change log is only a warning: %+v %v", res, err)
	}
}

func TestConfirmWritesMultiSelectMachinesWithEqualHCSplit(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	// AB12C02 has 0.5 part-day leave, leaving 0.5 HC to split across 2 machines -> 0.25 each
	env.repo.records = append(env.repo.records, DayRecord{
		UID: "AB12C02_20261119", EE: "AB12C02", Date: testDate, Actual: "AM", Schedule: "W",
		Entries: []HCEntry{{Process: "Winder", Machine: "ANL - Annual Leave", HC: 0.5}},
	})
	env.scan(t, "AB12C01")
	env.scan(t, "AB12C02")
	// AB12C01 -> 3 machines (0.3333333333 each, includes activity Winder_9014)
	if _, err := env.svc.AssignMany(ctx, testUser, testKey, "AB12C01", []string{"Winder_515", "Winder_520", "Winder_9014"}); err != nil {
		t.Fatal(err)
	}
	// AB12C02 -> 2 machines with 0.5 leave -> 0.25 each
	if _, err := env.svc.AssignMany(ctx, testUser, testKey, "AB12C02", []string{"Winder_515", "Winder_520"}); err != nil {
		t.Fatal(err)
	}
	res, err := env.svc.Confirm(ctx, testUser, testKey, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Written) != 2 || len(res.NeedsAck) != 0 {
		t.Fatalf("confirm result: %+v", res)
	}
	byEE := map[string]DayRecord{}
	for _, r := range env.repo.written[0] {
		byEE[r.EE] = r
	}
	r1 := byEE["AB12C01"]
	if len(r1.Entries) != 3 || r1.BuildIn != 1 {
		t.Fatalf("3-machine row: %+v", r1)
	}
	for i, wantMach := range []string{"Winder_515", "Winder_520", "Winder_9014"} {
		if r1.Entries[i].Machine != wantMach || r1.Entries[i].MachineID != wantMach || r1.Entries[i].HC != 0.3333333333 {
			t.Fatalf("r1.Entries[%d] = %+v, want %s (id %s) 0.3333333333", i, r1.Entries[i], wantMach, wantMach)
		}
	}
	r2 := byEE["AB12C02"]
	if len(r2.Entries) != 3 || r2.Entries[0].Machine != "ANL - Annual Leave" || r2.Entries[0].MachineID != "" || r2.Entries[0].HC != 0.5 ||
		r2.Entries[1].Machine != "Winder_515" || r2.Entries[1].MachineID != "Winder_515" || r2.Entries[1].HC != 0.25 ||
		r2.Entries[2].Machine != "Winder_520" || r2.Entries[2].MachineID != "Winder_520" || r2.Entries[2].HC != 0.25 ||
		r2.BuildIn != 0.5 {
		t.Fatalf("part-day leave 2-machine row: %+v", r2)
	}
}

func TestConfirmMachineIDSnapshotLeaveOmissionAndChangeLog(t *testing.T) {
	env := newTestEnv(t)
	env.svc.LeaveList = append([]string{"Delete Record"}, env.svc.LeaveList...)
	// Machine whose display name differs from its t_mqpd_hc_table.id
	env.repo.machines[3] = Machine{ID: "Winder_9014", Name: "Material handling", Process: "Winder", Type: "Non-routine", Budget: 0, Seq: 4}
	// Existing record carries a part-day leave (even with a stray machine_id) that Confirm must strip when rewriting.
	env.repo.records = append(env.repo.records, DayRecord{
		UID: "AB12C03_20261119", EE: "AB12C03", Date: testDate, Actual: "AM", Schedule: "W",
		Entries: []HCEntry{{Process: "Winder", Machine: "ANL - Annual Leave", HC: 0.5, MachineID: "should-be-stripped"}},
	})
	logDir := t.TempDir()
	env.svc.ChangeLog = NewFileChangeLog(logDir)

	ctx := context.Background()
	env.scan(t, "AB12C03")
	if _, err := env.svc.Assign(ctx, testUser, testKey, "AB12C03", "Winder_9014"); err != nil {
		t.Fatal(err)
	}
	res, err := env.svc.Confirm(ctx, testUser, testKey, true)
	if err != nil || len(res.Written) != 1 {
		t.Fatalf("confirm: %+v %v", res, err)
	}
	row := env.repo.written[0][0]
	if len(row.Entries) != 2 ||
		row.Entries[0] != (HCEntry{Process: "Winder", Machine: "ANL - Annual Leave", HC: 0.5}) ||
		row.Entries[1] != (HCEntry{Process: "Winder", Machine: "Material handling", HC: 0.5, MachineID: "Winder_9014"}) {
		t.Fatalf("written entries: %+v", row.Entries)
	}

	// Verify DailyAttnData.log carries machine_id on the activity entry and omits it on the leave entry.
	logBytes, err := os.ReadFile(filepath.Join(logDir, changeLogName))
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logBytes)
	if !strings.Contains(logText, `"machine":"Material handling","hc":0.5,"machine_id":"Winder_9014"`) {
		t.Fatalf("changelog must retain machine name snapshot and machine_id: %s", logText)
	}
	if !strings.Contains(logText, `{"process":"Winder","machine":"ANL - Annual Leave","hc":0.5}`) {
		t.Fatalf("changelog leave entry must omit machine_id: %s", logText)
	}

	// Verify JSON round-trip preserves machine_id on regular entries and omits it on leave & Delete Record entries.
	entries := []HCEntry{
		{Process: "Winder", Machine: "ANL - Annual Leave", HC: 0.5},
		{Process: "Winder", Machine: "Delete Record", HC: 1},
		{Process: "Winder", Machine: "Material handling", HC: 0.5, MachineID: "Winder_9014"},
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	var objs []map[string]any
	if err := json.Unmarshal(raw, &objs); err != nil {
		t.Fatal(err)
	}
	if _, has := objs[0]["machine_id"]; has {
		t.Fatalf("leave entry must omit machine_id: %s", raw)
	}
	if _, has := objs[1]["machine_id"]; has {
		t.Fatalf("Delete Record entry must omit machine_id: %s", raw)
	}
	if objs[2]["machine_id"] != "Winder_9014" || objs[2]["machine"] != "Material handling" {
		t.Fatalf("regular entry must keep machine_id and machine: %s", raw)
	}
	var decoded []HCEntry
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 3 || decoded[0] != entries[0] || decoded[1] != entries[1] || decoded[2] != entries[2] {
		t.Fatalf("round-trip lost fields: got %+v want %+v", decoded, entries)
	}
}

func containsAny(s string, parts ...string) bool {
	for _, p := range parts {
		if len(p) > 0 && strings.Contains(s, p) {
			return true
		}
	}
	return false
}
