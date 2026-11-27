package hcbudget

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"dash_go/internal/core/auth"
	"dash_go/internal/core/hc"
	"dash_go/internal/core/i18n"
)

func intPtr(v int) *int    { return &v }
func boolPtr(v bool) *bool { return &v }

var (
	testNow   = time.Date(2026, 11, 27, 9, 30, 0, 0, time.Local)
	adminUser = auth.User{EE: "AD01A01", Name: "Alice Admin"}
	stdUser   = auth.User{EE: "US01A01", Name: "Bob User"}
)

type memRepo struct {
	rows     map[string]Row  // all rows across all processes by id
	admins   map[string]bool // simulated T_USER_ALL Type = admin lookup by upper(ee_code)
	adminErr error
	failErr  error
}

func newMemRepo(seed ...Row) *memRepo {
	m := &memRepo{rows: map[string]Row{}, admins: map[string]bool{}}
	for _, r := range seed {
		m.rows[r.ID] = r
	}
	return m
}

func (m *memRepo) IsUserAdmin(_ context.Context, eeCode string) (bool, error) {
	if m.adminErr != nil {
		return false, m.adminErr
	}
	return m.admins[strings.ToUpper(strings.TrimSpace(eeCode))], nil
}

func (m *memRepo) ProcessRows(_ context.Context, process string) ([]Row, error) {
	if m.failErr != nil {
		return nil, m.failErr
	}
	out := []Row{}
	for _, r := range m.rows {
		if r.Process == process {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		di, dj := 1<<30, 1<<30
		if out[i].DisplaySeq != nil {
			di = *out[i].DisplaySeq
		}
		if out[j].DisplaySeq != nil {
			dj = *out[j].DisplaySeq
		}
		if di != dj {
			return di < dj
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (m *memRepo) Apply(ctx context.Context, process string, candidateIDs []string, plan CommitPlan) ([]Row, error) {
	if m.failErr != nil {
		return nil, m.failErr
	}
	existing, err := m.ProcessRows(ctx, process)
	if err != nil {
		return nil, err
	}
	taken := map[string]bool{}
	for _, id := range candidateIDs {
		if _, ok := m.rows[id]; ok {
			taken[id] = true
		}
	}
	inserts, updates, err := plan(existing, taken)
	if err != nil {
		return nil, err
	}
	written := make([]Row, 0, len(inserts)+len(updates))
	for _, r := range inserts {
		m.rows[r.ID] = r
		written = append(written, r)
	}
	for _, r := range updates {
		m.rows[r.ID] = r
		written = append(written, r)
	}
	return written, nil
}

type stubPerms struct {
	byEE map[string]hc.Access
	err  error
}

func (s *stubPerms) Access(_ context.Context, u auth.User, _ bool) (hc.Access, error) {
	if s.err != nil {
		return hc.Access{}, s.err
	}
	return s.byEE[u.EE], nil
}

func (s *stubPerms) Processes(ctx context.Context, u auth.User, fresh bool) ([]string, error) {
	acc, err := s.Access(ctx, u, fresh)
	return acc.Processes, err
}

type stubChangeLog struct {
	calls int
	rows  []Row
	err   error
}

func (c *stubChangeLog) Append(_ string, rows []Row) error {
	c.calls++
	c.rows = append([]Row(nil), rows...)
	return c.err
}

type testEnv struct {
	repo  *memRepo
	perms *stubPerms
	clog  *stubChangeLog
	svc   *Service
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	repo := newMemRepo(
		Row{ID: "Winder_515", Process: "Winder", CostCenter: "2085", ProcessCode: intPtr(10), Type: TypeRoutine, StationCode: intPtr(515), Machine: "Winder 515", BudgetHC: 2, Category: "Main", DisplaySeq: intPtr(1), Hidden: false, UpdateAt: "2026-11-20 08:00:00", UpdateUser: "Init"},
		Row{ID: "Winder_520", Process: "Winder", CostCenter: "2085", ProcessCode: intPtr(10), Type: TypeRoutine, StationCode: intPtr(520), Machine: "Winder 520", BudgetHC: 1.5, Category: "Main", DisplaySeq: intPtr(2), Hidden: false, UpdateAt: "2026-11-20 08:00:00", UpdateUser: "Init"},
		Row{ID: "Winder_901", Process: "Winder", CostCenter: "2085", ProcessCode: intPtr(10), Type: TypeNonRoutine, StationCode: intPtr(901), Machine: "Group Leader", BudgetHC: 1, Category: "Support", DisplaySeq: intPtr(3), Hidden: false, UpdateAt: "2026-11-20 08:00:00", UpdateUser: "Init"},
		Row{ID: "Winder_999", Process: "Winder", CostCenter: "2085", ProcessCode: intPtr(10), Type: TypeRoutine, StationCode: intPtr(999), Machine: "Retired Winder", BudgetHC: 0, Category: "Legacy", DisplaySeq: intPtr(4), Hidden: true, UpdateAt: "2026-10-01 08:00:00", UpdateUser: "Init"},
		Row{ID: "Slitter_105", Process: "Slitter", CostCenter: "2086", ProcessCode: intPtr(20), Type: TypeRoutine, StationCode: intPtr(105), Machine: "Slitter 105", BudgetHC: 1, Category: "Main", DisplaySeq: intPtr(1), Hidden: false, UpdateAt: "2026-11-20 08:00:00", UpdateUser: "Init"},
	)
	repo.admins[adminUser.EE] = true
	perms := &stubPerms{byEE: map[string]hc.Access{
		adminUser.EE: {Processes: []string{"Winder", "Slitter"}, IsAdmin: false}, // admin comes from T_USER_ALL, not hc_user_processes/t_auth_group
		stdUser.EE:   {Processes: []string{"Winder"}, IsAdmin: false},
	}}
	clog := &stubChangeLog{}
	svc, err := NewService(Deps{
		Repo:      repo,
		ChangeLog: clog,
		Perms:     perms,
		Processes: []string{"Slitter", "Taping", "Winder"},
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:       func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	return &testEnv{repo: repo, perms: perms, clog: clog, svc: svc}
}

func assertServiceErr(t *testing.T, err error, wantStatus int, wantCode string) {
	t.Helper()
	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("expected *Error (%d %s), got %v", wantStatus, wantCode, err)
	}
	if se.Status != wantStatus || se.Code != wantCode {
		t.Fatalf("got (%d %q), want (%d %q)", se.Status, se.Code, wantStatus, wantCode)
	}
}

func TestMeOrdersProcessesAndExposesAdmin(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	meAdmin, err := env.svc.Me(ctx, adminUser)
	if err != nil {
		t.Fatal(err)
	}
	if !meAdmin.IsAdmin || !reflect.DeepEqual(meAdmin.Processes, []string{"Slitter", "Winder"}) {
		t.Fatalf("admin me: %+v", meAdmin)
	}

	meStd, err := env.svc.Me(ctx, stdUser)
	if err != nil {
		t.Fatal(err)
	}
	if meStd.IsAdmin || !reflect.DeepEqual(meStd.Processes, []string{"Winder"}) {
		t.Fatalf("std me: %+v", meStd)
	}

	// User without permissions gets non-nil empty process slice.
	meNone, err := env.svc.Me(ctx, auth.User{EE: "NONE", Name: "Nobody"})
	if err != nil || meNone.Processes == nil || len(meNone.Processes) != 0 || meNone.IsAdmin {
		t.Fatalf("none me: %#v %v", meNone, err)
	}
}

func TestAdminRoleComesStrictlyFromTUserAllNotAuthGroup(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	// Even if stubPerms.Access reports IsAdmin=true for stdUser (e.g. group admin),
	// T_USER_ALL says stdUser is NOT admin -> hc_budget must treat stdUser as non-admin.
	env.perms.byEE[stdUser.EE] = hc.Access{Processes: []string{"Winder"}, IsAdmin: true}
	meStd, err := env.svc.Me(ctx, stdUser)
	if err != nil || meStd.IsAdmin {
		t.Fatalf("stdUser must NOT be admin when T_USER_ALL is false: %+v %v", meStd, err)
	}
	_, err = env.svc.Save(ctx, stdUser, SaveRequest{
		Process: "Winder",
		Creates: []CreateRow{{StationCode: intPtr(540), Machine: "Blocked", Type: TypeRoutine}},
	})
	assertServiceErr(t, err, 403, "forbidden_admin")

	// Conversely, adminUser has T_USER_ALL Type=admin even with Perms.IsAdmin=false -> allowed.
	meAdm, err := env.svc.Me(ctx, adminUser)
	if err != nil || !meAdm.IsAdmin {
		t.Fatalf("adminUser must be admin from T_USER_ALL: %+v %v", meAdm, err)
	}

	// Database error checking T_USER_ALL fails closed with HTTP 500.
	env.repo.adminErr = errors.New("db down")
	_, err = env.svc.Me(ctx, adminUser)
	assertServiceErr(t, err, 500, "internal")
	_, err = env.svc.Table(ctx, adminUser, "Winder", false)
	assertServiceErr(t, err, 500, "internal")
	_, err = env.svc.Save(ctx, adminUser, SaveRequest{Process: "Winder"})
	assertServiceErr(t, err, 500, "internal")
}

func TestTableFiltersHiddenAndComputesDefaults(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	// Standard user requesting include_hidden=true is still restricted to visible rows.
	tblStd, err := env.svc.Table(ctx, stdUser, "Winder", true)
	if err != nil {
		t.Fatal(err)
	}
	if tblStd.IsAdmin || tblStd.IncludeHidden || len(tblStd.Rows) != 3 {
		t.Fatalf("std table: isAdmin=%v inc=%v rows=%d", tblStd.IsAdmin, tblStd.IncludeHidden, len(tblStd.Rows))
	}
	if tblStd.Totals.VisibleCount != 3 || tblStd.Totals.HiddenCount != 1 || tblStd.Totals.TotalBudgetHC != 4.5 {
		t.Fatalf("totals: %+v", tblStd.Totals)
	}
	if tblStd.Defaults.CostCenter != "2085" || tblStd.Defaults.ProcessCode == nil || *tblStd.Defaults.ProcessCode != 10 {
		t.Fatalf("defaults: %+v", tblStd.Defaults)
	}
	if !reflect.DeepEqual(tblStd.Defaults.Categories, []string{"Legacy", "Main", "Support"}) {
		t.Fatalf("categories: %v", tblStd.Defaults.Categories)
	}
	if !reflect.DeepEqual(tblStd.Defaults.UsedStationCodes, []int{515, 520, 901, 999}) || tblStd.Defaults.NextSeq != 5 {
		t.Fatalf("station codes / next seq: %+v", tblStd.Defaults)
	}

	// Admin requesting include_hidden=true gets all 4 rows.
	tblAdmin, err := env.svc.Table(ctx, adminUser, "Winder", true)
	if err != nil {
		t.Fatal(err)
	}
	if !tblAdmin.IsAdmin || !tblAdmin.IncludeHidden || len(tblAdmin.Rows) != 4 {
		t.Fatalf("admin table: isAdmin=%v inc=%v rows=%d", tblAdmin.IsAdmin, tblAdmin.IncludeHidden, len(tblAdmin.Rows))
	}

	// Standard user cannot access unpermitted process.
	_, err = env.svc.Table(ctx, stdUser, "Slitter", false)
	assertServiceErr(t, err, 403, "no_process_permission")
}

func TestStandardUserCanEditButCannotCreateHideOrReorder(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	// Allowed: edit machine, type, category, budget_head_count.
	res, err := env.svc.Save(ctx, stdUser, SaveRequest{
		Process: "Winder",
		Updates: []UpdateRow{{
			ID:       "Winder_515",
			Machine:  "Winder 515A",
			Type:     TypeNonRoutine,
			Category: "Special",
			BudgetHC: "2.5",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Inserted != 0 || res.Updated != 1 || env.clog.calls != 1 {
		t.Fatalf("res=%+v clog=%d", res, env.clog.calls)
	}
	got := env.repo.rows["Winder_515"]
	if got.Machine != "Winder 515A" || got.Type != TypeNonRoutine || got.Category != "Special" || got.BudgetHC != 2.5 || got.UpdateUser != stdUser.Name {
		t.Fatalf("updated row: %+v", got)
	}

	// Forbidden: create row.
	_, err = env.svc.Save(ctx, stdUser, SaveRequest{
		Process: "Winder",
		Creates: []CreateRow{{StationCode: intPtr(530), Machine: "Winder 530", Type: TypeRoutine, BudgetHC: "1"}},
	})
	assertServiceErr(t, err, 403, "forbidden_admin")

	// Forbidden: hide row.
	_, err = env.svc.Save(ctx, stdUser, SaveRequest{
		Process: "Winder",
		Updates: []UpdateRow{{ID: "Winder_520", Machine: "Winder 520", Type: TypeRoutine, Category: "Main", BudgetHC: "1.5", Hidden: boolPtr(true)}},
	})
	assertServiceErr(t, err, 403, "forbidden_admin")

	// Forbidden: edit an already-hidden row.
	_, err = env.svc.Save(ctx, stdUser, SaveRequest{
		Process: "Winder",
		Updates: []UpdateRow{{ID: "Winder_999", Machine: "Sneaky Edit", Type: TypeRoutine, Category: "Legacy", BudgetHC: "0"}},
	})
	assertServiceErr(t, err, 403, "forbidden_admin")

	// Forbidden: reorder display_seq.
	_, err = env.svc.Save(ctx, stdUser, SaveRequest{
		Process: "Winder",
		Order:   []string{"Winder_520", "Winder_515", "Winder_901"},
	})
	assertServiceErr(t, err, 403, "forbidden_admin")
}

func TestAdminAddHideRestoreAndReorderInSingleBatch(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	res, err := env.svc.Save(ctx, adminUser, SaveRequest{
		Process: "Winder",
		Creates: []CreateRow{{
			StationCode: intPtr(530),
			CostCenter:  "2085",
			ProcessCode: intPtr(10),
			Type:        TypeRoutine,
			Machine:     "Winder 530",
			Category:    "Main",
			BudgetHC:    "3",
		}},
		Updates: []UpdateRow{
			{ID: "Winder_520", Machine: "Winder 520", Type: TypeRoutine, Category: "Main", BudgetHC: "1.5", Hidden: boolPtr(true)},
			{ID: "Winder_999", Machine: "Restored Winder", Type: TypeRoutine, Category: "Main", BudgetHC: "1", Hidden: boolPtr(false)},
		},
		// Put new Winder_530 first, then Winder_999, Winder_515, Winder_901, Winder_520.
		Order: []string{"Winder_530", "Winder_999", "Winder_515", "Winder_901", "Winder_520"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Inserted != 1 || res.Updated != 4 {
		t.Fatalf("expected 1 inserted and 4 updated (all 4 existing rows changed seq/fields): %+v", res)
	}
	if r := env.repo.rows["Winder_530"]; r.ID != "Winder_530" || *r.DisplaySeq != 1 || r.BudgetHC != 3 || r.Hidden {
		t.Fatalf("new row: %+v", r)
	}
	if r := env.repo.rows["Winder_999"]; r.Hidden || r.Machine != "Restored Winder" || *r.DisplaySeq != 2 {
		t.Fatalf("restored row: %+v", r)
	}
	if r := env.repo.rows["Winder_515"]; *r.DisplaySeq != 3 {
		t.Fatalf("Winder_515 seq: %+v", r)
	}
	if r := env.repo.rows["Winder_901"]; *r.DisplaySeq != 4 {
		t.Fatalf("Winder_901 seq: %+v", r)
	}
	if r := env.repo.rows["Winder_520"]; !r.Hidden || *r.DisplaySeq != 5 {
		t.Fatalf("hidden row: %+v", r)
	}
}

func TestAdminReorderWithHiddenRowsHiddenPreservesHiddenRowsAtEnd(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	// Admin only views visible rows (515, 520, 901) and swaps 520 above 515.
	// Hidden row Winder_999 already had seq=4, so it should remain seq=4 (not updated!).
	res, err := env.svc.Save(ctx, adminUser, SaveRequest{
		Process: "Winder",
		Order:   []string{"Winder_520", "Winder_515", "Winder_901"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Only 520 (2->1) and 515 (1->2) actually changed; 901 (stays 3) and 999 (stays 4) are untouched.
	if res.Inserted != 0 || res.Updated != 2 {
		t.Fatalf("expected only 2 changed rows, got %+v", res)
	}
	if *env.repo.rows["Winder_520"].DisplaySeq != 1 || *env.repo.rows["Winder_515"].DisplaySeq != 2 || *env.repo.rows["Winder_901"].DisplaySeq != 3 || *env.repo.rows["Winder_999"].DisplaySeq != 4 {
		t.Fatalf("unexpected sequences")
	}
	if env.repo.rows["Winder_999"].UpdateUser != "Init" {
		t.Errorf("unchanged hidden row must not be touched: %+v", env.repo.rows["Winder_999"])
	}
}

func TestValidationErrorsAndUniqueness(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	// Duplicate station code against hidden row 999.
	_, err := env.svc.Save(ctx, adminUser, SaveRequest{
		Process: "Winder",
		Creates: []CreateRow{{StationCode: intPtr(999), Machine: "Dup Hidden", Type: TypeRoutine, BudgetHC: "1"}},
	})
	assertServiceErr(t, err, 409, "duplicate_station_code")

	// Duplicate station code within the same batch.
	_, err = env.svc.Save(ctx, adminUser, SaveRequest{
		Process: "Winder",
		Creates: []CreateRow{
			{StationCode: intPtr(601), Machine: "M1", Type: TypeRoutine, BudgetHC: "1"},
			{StationCode: intPtr(601), Machine: "M2", Type: TypeRoutine, BudgetHC: "1"},
		},
	})
	assertServiceErr(t, err, 409, "duplicate_station_code")

	// Global ID collision with another process's row (edge case if an ID already exists).
	env.repo.rows["Winder_777"] = Row{ID: "Winder_777", Process: "Other", Machine: "Legacy Collision"}
	_, err = env.svc.Save(ctx, adminUser, SaveRequest{
		Process: "Winder",
		Creates: []CreateRow{{StationCode: intPtr(777), Machine: "New 777", Type: TypeRoutine, BudgetHC: "1"}},
	})
	assertServiceErr(t, err, 409, "duplicate_id")

	// Invalid station code (missing or <= 0 or > 32767).
	for _, sc := range []*int{nil, intPtr(0), intPtr(-5), intPtr(40000)} {
		_, err = env.svc.Save(ctx, adminUser, SaveRequest{
			Process: "Winder",
			Creates: []CreateRow{{StationCode: sc, Machine: "Bad SC", Type: TypeRoutine}},
		})
		assertServiceErr(t, err, 400, "invalid_station_code")
	}

	// Invalid budget format.
	for _, bad := range []string{"-1", "abc", "1.2.3", "123456789", "1e2"} {
		_, err = env.svc.Save(ctx, adminUser, SaveRequest{
			Process: "Winder",
			Updates: []UpdateRow{{ID: "Winder_515", Machine: "Winder 515", Type: TypeRoutine, BudgetHC: bad}},
		})
		assertServiceErr(t, err, 400, "invalid_budget")
	}

	// Blank budget saves as 0.
	res, err := env.svc.Save(ctx, adminUser, SaveRequest{
		Process: "Winder",
		Updates: []UpdateRow{{ID: "Winder_515", Machine: "Winder 515", Type: TypeRoutine, Category: "Main", BudgetHC: ""}},
	})
	if err != nil || res.Updated != 1 || env.repo.rows["Winder_515"].BudgetHC != 0 {
		t.Fatalf("blank budget should save as 0: res=%+v err=%v row=%+v", res, err, env.repo.rows["Winder_515"])
	}

	// No-op save returns 409 nothing_to_save.
	_, err = env.svc.Save(ctx, adminUser, SaveRequest{
		Process: "Winder",
		Updates: []UpdateRow{{ID: "Winder_515", Machine: "Winder 515", Type: TypeRoutine, Category: "Main", BudgetHC: "0"}},
	})
	assertServiceErr(t, err, 409, "nothing_to_save")

	// Cost center > 4 chars rejected.
	_, err = env.svc.Save(ctx, adminUser, SaveRequest{
		Process: "Winder",
		Creates: []CreateRow{{StationCode: intPtr(602), CostCenter: "20855", Machine: "Bad CC", Type: TypeRoutine}},
	})
	assertServiceErr(t, err, 400, "invalid_cost_center")

	// Invalid type rejected on create and on type change.
	_, err = env.svc.Save(ctx, adminUser, SaveRequest{
		Process: "Winder",
		Creates: []CreateRow{{StationCode: intPtr(603), Machine: "Bad Type", Type: "Other"}},
	})
	assertServiceErr(t, err, 400, "invalid_type")

	// Invalid machine empty rejected.
	_, err = env.svc.Save(ctx, adminUser, SaveRequest{
		Process: "Winder",
		Updates: []UpdateRow{{ID: "Winder_515", Machine: "   ", Type: TypeRoutine, BudgetHC: "0"}},
	})
	assertServiceErr(t, err, 400, "invalid_machine")
}

func TestFileChangeLogAppendsToBothFoldersAndWarnsOnFailure(t *testing.T) {
	dir1, dir2 := t.TempDir(), t.TempDir()
	cl := NewFileChangeLog(dir1, dir2, dir1) // duplicate dir deduplicated
	if err := cl.Append("Alice Admin", []Row{{ID: "Winder_515", Process: "Winder", Machine: "Winder 515", Type: TypeRoutine, BudgetHC: 2}}); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{dir1, dir2} {
		raw, err := os.ReadFile(filepath.Join(dir, changeLogName))
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		if !strings.Contains(text, "Alice Admin update mqpd hc budget data successfully (dash_go hc_budget):") || !strings.Contains(text, `"Winder_515"`) {
			t.Errorf("%s unexpected log content: %s", dir, text)
		}
	}
	if err := NewFileChangeLog("", "").Append("Alice", nil); !errors.Is(err, ErrNoChangeLogFolder) {
		t.Errorf("empty dirs must return ErrNoChangeLogFolder, got %v", err)
	}

	// Non-fatal warning when changelog append fails.
	env := newTestEnv(t)
	env.clog.err = errors.New("disk full")
	ctx := i18n.WithLang(context.Background(), i18n.ZH)
	res, err := env.svc.Save(ctx, stdUser, SaveRequest{
		Process: "Winder",
		Updates: []UpdateRow{{ID: "Winder_515", Machine: "Winder 515", Type: TypeRoutine, Category: "Main", BudgetHC: "3"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated != 1 || !strings.Contains(res.LogWarning, "MQPDHeadCount.log") {
		t.Fatalf("expected non-fatal Chinese log warning, got %+v", res)
	}
}
