package hcscan

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"dash_go/internal/core/auth"
)

// fakeRepo is an in-memory Repository for the service tests.
type fakeRepo struct {
	mu       sync.Mutex
	machines []Machine
	mtr      MTRData
	cal      map[string]string
	mes      []MESRow
	members  []MemberRow
	records  []DayRecord
	writeErr error
	written  [][]DayRecord
	users    []string
}

func (f *fakeRepo) Machines(_ context.Context, processes []string) ([]Machine, error) {
	var out []Machine
	for _, m := range f.machines {
		if contains(processes, m.Process) {
			out = append(out, m)
		}
	}
	return out, nil
}
func (f *fakeRepo) MTR(context.Context, []string) (MTRData, error) { return f.mtr, nil }
func (f *fakeRepo) CalendarDay(context.Context, time.Time) (map[string]string, error) {
	return f.cal, nil
}
func (f *fakeRepo) MESSnapshot(context.Context, time.Time) ([]MESRow, error) { return f.mes, nil }
func (f *fakeRepo) MESPerson(_ context.Context, ee string) (*MESRow, error) {
	for _, r := range f.mes {
		if strings.EqualFold(r.EE, ee) {
			r := r
			return &r, nil
		}
	}
	return nil, nil
}
func (f *fakeRepo) Members(context.Context, time.Time) ([]MemberRow, error) { return f.members, nil }
func (f *fakeRepo) DayRecords(_ context.Context, date time.Time) ([]DayRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []DayRecord
	for _, r := range f.records {
		if r.Date.Equal(date) {
			out = append(out, r)
		}
	}
	return out, nil
}
func (f *fakeRepo) History(_ context.Context, ees []string, from, to time.Time) ([]DayRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []DayRecord
	for _, r := range f.records {
		if contains(ees, r.EE) && !r.Date.Before(from) && r.Date.Before(to) {
			out = append(out, r)
		}
	}
	return out, nil
}
func (f *fakeRepo) WriteDay(_ context.Context, date time.Time, ees []string, user string, plan func(map[string]DayRecord) ([]DayRecord, error)) ([]DayRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return nil, f.writeErr
	}
	current := map[string]DayRecord{}
	for _, r := range f.records {
		if r.Date.Equal(date) && contains(ees, r.EE) {
			current[r.EE] = r
		}
	}
	rows, err := plan(current)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		replaced := false
		for i, r := range f.records {
			if r.UID == row.UID {
				f.records[i], replaced = row, true
			}
		}
		if !replaced {
			f.records = append(f.records, row)
		}
	}
	f.written = append(f.written, rows)
	f.users = append(f.users, user)
	return rows, nil
}

// recordingLog counts change log calls.
type recordingLog struct {
	calls int
	err   error
}

func (r *recordingLog) Append(string, []DayRecord) error { r.calls++; return r.err }

// fakePerms grants the listed processes, or fails.
type fakePerms struct {
	procs []string
	err   error
	fresh int
}

func (p *fakePerms) Processes(_ context.Context, _ auth.User, fresh bool) ([]string, error) {
	if fresh {
		p.fresh++
	}
	return p.procs, p.err
}

var (
	testDate  = time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	testNow   = time.Date(2026, 11, 19, 7, 30, 0, 0, time.UTC)
	testUser  = auth.User{EE: "LD01A01", Name: "Lead Er"}
	testKey   = Key{Process: "Winder", Date: testDate, Shift: ShiftAM}
	testLeave = []string{"MCL - Medical Leave", "ANL - Annual Leave"}
)

// testEnv is a Winder shop floor with four machines and a handful of people:
//
//	AB12C01 Lv3 on 515, Lv2 on 520          AB12C02 Lv1 on 515, Lv3 on 520 (worked 520 on three of the last days)
//	AB12C03 no MTR record at all            AB12C04 Lv1 on 520 only
//	AB12C05 Slitter member with Lv2 on 515  AB12C06 Winder member scheduled PM (shift PB)
type testEnv struct {
	repo  *fakeRepo
	perms *fakePerms
	log   *recordingLog
	svc   *Service
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	repo := &fakeRepo{
		machines: []Machine{
			{ID: "Winder_515", Name: "Winder_515", Process: "Winder", Type: "Routine", Budget: 1, Seq: 1},
			{ID: "Winder_520", Name: "Winder_520", Process: "Winder", Type: "Routine", Budget: 2, Seq: 2},
			{ID: "Winder_525", Name: "Winder_525", Process: "Winder", Type: "Routine", Budget: 1, Seq: 3}, // no MTR mapping
			{ID: "Winder_9014", Name: "Winder_9014", Process: "Winder", Type: "Non-routine", Budget: 0, Seq: 4},
			{ID: "Slitter_105", Name: "Slitter_105", Process: "Slitter", Type: "Routine", Budget: 1, Seq: 1},
		},
		mtr: MTRData{
			Levels: map[string]map[string]int{
				"AB12C01": {"C515": 3, "C520": 2},
				"AB12C02": {"C515": 1, "C520": 3},
				"AB12C04": {"C520": 1},
				"AB12C05": {"C515": 2},
			},
			NameToCIM: map[string]string{"Winder_515": "C515", "Winder_520": "C520"},
			DataTime:  time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC),
		},
		cal: map[string]string{"PA": "W", "PB": "PM", "RA": "O"},
	}
	mes := func(ee, name, cost, shift string) MESRow {
		return MESRow{EE: ee, Name: name, CostCenter: cost, Shift: shift, EffectiveMth: d(2026, 11, 1)}
	}
	repo.mes = []MESRow{
		mes("AB12C01", "Alice One", "2085", "PA"),
		mes("AB12C02", "Bob Two", "2085", "PA"),
		mes("AB12C03", "Carl Three", "2085", "PA"),
		mes("AB12C04", "Dina Four", "2085", "PA"),
		mes("AB12C05", "Eve Five (Slitter)", "2081", "PA"),
		mes("AB12C06", "Finn Six", "2085", "PB"),
		mes("AB12C07", "Gus Seven", "2085", "RA"), // day off today
		mes("LD01A01", "Lead Er", "2085", "PA"),
	}
	// AB12C02 worked Winder_520 on three of the last seven days
	for i := 1; i <= 3; i++ {
		day := testDate.AddDate(0, 0, -i)
		repo.records = append(repo.records, DayRecord{UID: fmt.Sprintf("AB12C02_%s", day.Format("20060102")), EE: "AB12C02", Date: day, Actual: "AM", Entries: []HCEntry{{Process: "Winder", Machine: "Winder_520", HC: 1}}})
	}
	perms := &fakePerms{procs: []string{"Winder"}}
	log := &recordingLog{}
	svc, err := NewService(Deps{
		Repo: repo, Drafts: NewMemoryDrafts(), ChangeLog: log, Perms: perms, Settings: DefaultSettings(),
		Processes: []string{"Slitter", "Winder"}, LeaveList: testLeave, Now: func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	return &testEnv{repo: repo, perms: perms, log: log, svc: svc}
}

func (e *testEnv) scan(t *testing.T, id string) *ScanResult {
	t.Helper()
	res, err := e.svc.Scan(context.Background(), testUser, testKey, id)
	if err != nil {
		t.Fatalf("scan %s: %v", id, err)
	}
	return res
}

func hasCode(flags []FlagView, code string) bool {
	for _, f := range flags {
		if f.Code == code {
			return true
		}
	}
	return false
}

func wantErr(t *testing.T, err error, status int, code string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Status != status || e.Code != code {
		t.Fatalf("error = %v, want status %d code %s", err, status, code)
	}
}
