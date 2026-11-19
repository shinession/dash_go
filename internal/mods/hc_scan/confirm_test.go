package hcscan

import (
	"context"
	"errors"
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
		len(row.Entries) != 1 || row.Entries[0] != (HCEntry{Process: "Winder", Machine: "Winder_515", HC: 1}) ||
		row.BuildIn != 1 || row.AddtOT != 0 || row.TotalOT != 1 {
		t.Fatalf("written row: %+v", row)
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
	if len(full.Entries) != 1 || full.Entries[0].Machine != "Winder_515" || full.Entries[0].HC != 1 || full.Actual != "AM" {
		t.Fatalf("a confirmed full-day leave exception is replaced by the machine: %+v", full)
	}
	half := byEE["AB12C02"]
	if len(half.Entries) != 2 || half.Entries[0].Machine != "MCL - Medical Leave" || half.Entries[0].HC != 0.5 ||
		half.Entries[1].Machine != "Winder_520" || half.Entries[1].HC != 0.5 {
		t.Fatalf("a part-day leave stays and the rest goes to the machine: %+v", half.Entries)
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

func containsAny(s string, parts ...string) bool {
	for _, p := range parts {
		if len(p) > 0 && strings.Contains(s, p) {
			return true
		}
	}
	return false
}
