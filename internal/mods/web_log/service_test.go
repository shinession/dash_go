package weblog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"dash_go/internal/core/auth"
)

type logRow struct {
	date     string
	ip       string
	app      string
	category string
	host     string
	actions  int64
}

type fakeLogRepo struct {
	rows        []logRow
	metaCalls   int
	trendCalls  int
	metaErr     error
	trendErr    error
	lastFilter  TrendDBFilter
}

func (r *fakeLogRepo) Meta(_ context.Context) (MetaData, error) {
	r.metaCalls++
	if r.metaErr != nil {
		return MetaData{}, r.metaErr
	}
	appsSet := map[string]bool{}
	hostsSet := map[string]bool{}
	allCatsSet := map[string]bool{}
	catsByApp := map[string]map[string]bool{}
	var minD, maxD string
	for _, row := range r.rows {
		app := strings.TrimSpace(row.app)
		if app == "" {
			app = DefaultApp
		}
		cat := strings.TrimSpace(row.category)
		if cat == "" {
			cat = DefaultCategory
		}
		host := strings.TrimSpace(row.host)
		if host == "" {
			host = DefaultHost
		}
		appsSet[app] = true
		hostsSet[host] = true
		allCatsSet[cat] = true
		if catsByApp[app] == nil {
			catsByApp[app] = map[string]bool{}
		}
		catsByApp[app][cat] = true
		if minD == "" || row.date < minD {
			minD = row.date
		}
		if maxD == "" || row.date > maxD {
			maxD = row.date
		}
	}
	out := MetaData{
		Apps:            sortedKeys(appsSet),
		Hosts:           sortedKeys(hostsSet),
		AllCategories:   sortedKeys(allCatsSet),
		CategoriesByApp: map[string][]string{},
		MinDate:         minD,
		MaxDate:         maxD,
	}
	for a, m := range catsByApp {
		out.CategoriesByApp[a] = sortedKeys(m)
	}
	return out, nil
}

func (r *fakeLogRepo) QueryTrend(_ context.Context, f TrendDBFilter) (TrendRawResult, error) {
	r.trendCalls++
	r.lastFilter = f
	if r.trendErr != nil {
		return TrendRawResult{}, r.trendErr
	}
	hostAllow := toSet(f.Hosts)
	catAllow := toSet(f.Categories)

	res := TrendRawResult{
		PeriodSeries: map[string]map[string]int64{},
		PeriodTotals: map[string]PeriodStat{},
		SeriesTotals: map[string]PeriodStat{},
	}
	periodIPs := map[string]map[string]bool{}
	seriesIPs := map[string]map[string]bool{}
	overallIPs := map[string]bool{}

	fromStr := f.From.Format("2006-01-02")
	toStr := f.To.Format("2006-01-02")

	for _, row := range r.rows {
		if row.date < fromStr || row.date > toStr {
			continue
		}
		app := strings.TrimSpace(row.app)
		if app == "" {
			app = DefaultApp
		}
		cat := strings.TrimSpace(row.category)
		if cat == "" {
			cat = DefaultCategory
		}
		host := strings.TrimSpace(row.host)
		if host == "" {
			host = DefaultHost
		}
		if f.App != "" && app != f.App {
			continue
		}
		if len(hostAllow) > 0 && !hostAllow[host] {
			continue
		}
		if len(catAllow) > 0 && !catAllow[cat] {
			continue
		}
		if f.ExcludeIdle && strings.EqualFold(cat, "idle") {
			continue
		}

		d, _ := parseDate(row.date)
		var pk string
		switch f.Mode {
		case ModeWeek:
			pk = mondayOf(d).Format("2006-01-02")
		case ModeMonth:
			pk = firstOfMonth(d).Format("2006-01-02")
		default:
			pk = d.Format("2006-01-02")
		}

		var seriesName string
		switch f.EffectiveGroupBy {
		case "host":
			seriesName = host
		case "category":
			seriesName = cat
		default:
			seriesName = app
		}

		if res.PeriodSeries[pk] == nil {
			res.PeriodSeries[pk] = map[string]int64{}
		}
		res.PeriodSeries[pk][seriesName] += row.actions

		ps := res.PeriodTotals[pk]
		ps.Actions += row.actions
		res.PeriodTotals[pk] = ps
		if periodIPs[pk] == nil {
			periodIPs[pk] = map[string]bool{}
		}
		periodIPs[pk][row.ip] = true

		ss := res.SeriesTotals[seriesName]
		ss.Actions += row.actions
		res.SeriesTotals[seriesName] = ss
		if seriesIPs[seriesName] == nil {
			seriesIPs[seriesName] = map[string]bool{}
		}
		seriesIPs[seriesName][row.ip] = true

		res.Overall.Actions += row.actions
		overallIPs[row.ip] = true
	}

	for pk, ips := range periodIPs {
		ps := res.PeriodTotals[pk]
		ps.Visitors = int64(len(ips))
		res.PeriodTotals[pk] = ps
	}
	for sn, ips := range seriesIPs {
		ss := res.SeriesTotals[sn]
		ss.Visitors = int64(len(ips))
		res.SeriesTotals[sn] = ss
	}
	res.Overall.Visitors = int64(len(overallIPs))
	return res, nil
}

type fakeAdmin struct {
	admins map[string]bool
	err    error
}

func (a *fakeAdmin) IsUserAdmin(_ context.Context, ee string) (bool, error) {
	if a.err != nil {
		return false, a.err
	}
	return a.admins[strings.ToUpper(strings.TrimSpace(ee))], nil
}

type fakeLive struct {
	entries []LiveLogEntry
	total   int64
	err     error
	lastLim int
}

func (l *fakeLive) Recent(_ context.Context, limit int) ([]LiveLogEntry, int64, error) {
	l.lastLim = limit
	if l.err != nil {
		return nil, 0, l.err
	}
	if limit < len(l.entries) {
		return l.entries[:limit], l.total, nil
	}
	return l.entries, l.total, nil
}

type failingCache struct{}

func (failingCache) Get(context.Context, string) ([]byte, bool, error) {
	return nil, false, errors.New("redis down")
}
func (failingCache) Set(context.Context, string, []byte, time.Duration) error {
	return errors.New("redis down")
}
func (failingCache) TryRefreshCooldown(context.Context, string, time.Duration) (bool, error) {
	return false, errors.New("redis down")
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func toSet(in []string) map[string]bool {
	out := make(map[string]bool, len(in))
	for _, s := range in {
		out[s] = true
	}
	return out
}

func sampleRows() []logRow {
	return []logRow{
		// Week 2026-W47 (Mon 2026-11-16 .. Sun 2026-11-22)
		{date: "2026-11-16", ip: "10.0.0.1/32", app: "IE", category: "tab1", host: "", actions: 100},             // empty host -> prod_eng
		{date: "2026-11-16", ip: "10.0.0.1/32", app: "IE", category: "idle", host: "prod_eng", actions: 50},      // same IP same day
		{date: "2026-11-17", ip: "10.0.0.1/32", app: "QDB", category: "tab2", host: "sgmescimai", actions: 200},  // same IP next day (same week!)
		{date: "2026-11-17", ip: "10.0.0.2/32", app: "Gitea", category: "git", host: "sgmescimemg", actions: 80},
		// Week 2026-W48 (Mon 2026-11-23 .. Sun 2026-11-29)
		{date: "2026-11-25", ip: "10.0.0.2/32", app: "IE", category: "tab9", host: "sgmescimai", actions: 300},
		{date: "2026-11-25", ip: "10.0.0.3/32", app: "TimeVault", category: "explorer", host: "sgmescimemg", actions: 120},
		// Next month: Dec 2026
		{date: "2026-12-01", ip: "10.0.0.1/32", app: "IE", category: "tab1", host: "prod_eng", actions: 60},
	}
}

func newTestService(t *testing.T, rows []logRow, cache Cache, now time.Time) (*Service, *fakeLogRepo, *fakeAdmin, *fakeLive) {
	t.Helper()
	repo := &fakeLogRepo{rows: rows}
	admin := &fakeAdmin{admins: map[string]bool{"AD01": true}}
	live := &fakeLive{
		entries: []LiveLogEntry{{Seq: 1, Raw: "10.0.0.1 | 2026-11-28 10:00:00.123456 | IE: tab11 | 200 | info | sgmescimai", Level: "info", Host: "sgmescimai"}},
		total:   42,
	}
	svc, err := NewService(Deps{
		Repo:     repo,
		Admin:    admin,
		Cache:    cache,
		LiveLogs: live,
		Settings: DefaultSettings(),
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc, repo, admin, live
}

func TestTrendStandardCalendarWeekAndDistinctPeriodVisitors(t *testing.T) {
	now := time.Date(2026, 11, 28, 15, 0, 0, 0, time.Local)
	svc, _, _, _ := newTestService(t, sampleRows(), NewMemoryCache(func() time.Time { return now }), now)

	res, err := svc.Trend(context.Background(), TrendQuery{
		Mode: ModeWeek,
		From: "2026-11-16",
		To:   "2026-11-29",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Periods) != 2 {
		t.Fatalf("got %d weeks, want 2: %+v", len(res.Periods), res.Periods)
	}
	if res.Periods[0].Key != "2026-W47" || res.Periods[0].Start != "2026-11-16" || res.Periods[0].End != "2026-11-22" {
		t.Errorf("week 0 = %+v", res.Periods[0])
	}
	if res.Periods[1].Key != "2026-W48" || res.Periods[1].Start != "2026-11-23" || res.Periods[1].End != "2026-11-29" {
		t.Errorf("week 1 = %+v", res.Periods[1])
	}
	// Week 47: 10.0.0.1 visited on Nov 16 and Nov 17 across IE & QDB; 10.0.0.2 visited on Nov 17 -> 2 distinct IPs (NOT 3 visitor-days!).
	if res.VisitorCounts[0] != 2 {
		t.Errorf("week 47 distinct visitors = %d, want 2", res.VisitorCounts[0])
	}
	if res.TotalActions[0] != 430 {
		t.Errorf("week 47 actions = %d, want 430", res.TotalActions[0])
	}
	// Week 48: 10.0.0.2 and 10.0.0.3 -> 2 distinct IPs, 420 actions.
	if res.VisitorCounts[1] != 2 || res.TotalActions[1] != 420 {
		t.Errorf("week 48 visitors=%d actions=%d, want 2 and 420", res.VisitorCounts[1], res.TotalActions[1])
	}
	// Range overall: 10.0.0.1, 10.0.0.2, 10.0.0.3 -> 3 unique visitors across both weeks.
	if res.Summary.UniqueVisitors != 3 || res.Summary.TotalActions != 850 {
		t.Errorf("summary = %+v", res.Summary)
	}
}

func TestTrendStandardCalendarMonth(t *testing.T) {
	now := time.Date(2026, 12, 5, 10, 0, 0, 0, time.Local)
	svc, _, _, _ := newTestService(t, sampleRows(), nil, now)

	res, err := svc.Trend(context.Background(), TrendQuery{
		Mode: ModeMonth,
		From: "2026-11-01",
		To:   "2026-12-05",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Periods) != 2 || res.Periods[0].Key != "2026-11" || res.Periods[1].Key != "2026-12" {
		t.Fatalf("periods = %+v", res.Periods)
	}
	// Standard calendar: 2026-11-30 stays in Nov, 2026-12-01 is in Dec (no legacy +1 day shift).
	if res.TotalActions[0] != 850 || res.VisitorCounts[0] != 3 {
		t.Errorf("Nov actions=%d visitors=%d, want 850 and 3", res.TotalActions[0], res.VisitorCounts[0])
	}
	if res.TotalActions[1] != 60 || res.VisitorCounts[1] != 1 {
		t.Errorf("Dec actions=%d visitors=%d, want 60 and 1", res.TotalActions[1], res.VisitorCounts[1])
	}
}

func TestTrendHostNormalizationAndGroupByHost(t *testing.T) {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.Local)
	svc, _, _, _ := newTestService(t, sampleRows(), nil, now)

	res, err := svc.Trend(context.Background(), TrendQuery{
		Mode:    ModeDay,
		From:    "2026-11-16",
		To:      "2026-11-25",
		GroupBy: GroupByHost,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.EffectiveGroupBy != "host" {
		t.Fatalf("effective_group_by = %q, want host", res.EffectiveGroupBy)
	}
	gotTotals := map[string]int64{}
	for _, s := range res.Series {
		gotTotals[s.Name] = s.Total
	}
	// Empty host row (100) + explicit prod_eng row (50) => prod_eng = 150.
	if gotTotals["prod_eng"] != 150 || gotTotals["sgmescimai"] != 500 || gotTotals["sgmescimemg"] != 200 {
		t.Errorf("host totals = %+v, want prod_eng=150 sgmescimai=500 sgmescimemg=200", gotTotals)
	}

	// Filter to prod_eng only + exclude idle: only the empty-host tab1 row (100) remains.
	filtered, err := svc.Trend(context.Background(), TrendQuery{
		Mode:        ModeDay,
		From:        "2026-11-16",
		To:          "2026-11-25",
		Hosts:       []string{"prod_eng"},
		ExcludeIdle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if filtered.Summary.TotalActions != 100 || filtered.Summary.UniqueVisitors != 1 {
		t.Errorf("filtered summary = %+v, want actions=100 visitors=1", filtered.Summary)
	}
}

func TestTrendTopNFoldingKeepsFullBreakdown(t *testing.T) {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.Local)
	rows := make([]logRow, 0, 20)
	for i := 1; i <= 20; i++ {
		rows = append(rows, logRow{
			date:     "2026-11-28",
			ip:       fmt.Sprintf("10.0.0.%d/32", i),
			app:      "IE",
			category: fmt.Sprintf("tool_%02d", i),
			host:     "sgmescimai",
			actions:  int64(i * 10),
		})
	}
	svc, _, _, _ := newTestService(t, rows, nil, now)
	svc.Settings.MaxSeries = 5

	res, err := svc.Trend(context.Background(), TrendQuery{
		Mode: ModeDay,
		From: "2026-11-28",
		To:   "2026-11-28",
		App:  "IE",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.EffectiveGroupBy != "category" {
		t.Fatalf("effective_group_by = %q, want category", res.EffectiveGroupBy)
	}
	if len(res.Breakdown) != 20 {
		t.Fatalf("breakdown len = %d, want 20", len(res.Breakdown))
	}
	if len(res.Series) != 6 {
		t.Fatalf("chart series len = %d, want 5 + 1 Others", len(res.Series))
	}
	last := res.Series[5]
	if !last.IsOther || last.Name != OtherSeriesKey || last.OtherCount != 15 {
		t.Errorf("folded tail = %+v", last)
	}
	// Sum of 10..150 = 15 * 160 / 2 = 1200.
	if last.Total != 1200 {
		t.Errorf("folded tail total = %d, want 1200", last.Total)
	}
}

func TestRedisCacheTTLRefreshAndFallback(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.Local)
	svc, repo, _, _ := newTestService(t, sampleRows(), NewRedisCache(rdb), now)

	// 1. Recent query (to = today) -> miss then hit, TTL = 10m.
	r1, err := svc.Trend(context.Background(), TrendQuery{Mode: ModeDay, From: "2026-11-25", To: "2026-11-28"})
	if err != nil || r1.Cached || repo.trendCalls != 1 {
		t.Fatalf("first recent query: err=%v cached=%v calls=%d", err, r1.Cached, repo.trendCalls)
	}
	r2, err := svc.Trend(context.Background(), TrendQuery{Mode: ModeDay, From: "2026-11-25", To: "2026-11-28"})
	if err != nil || !r2.Cached || repo.trendCalls != 1 {
		t.Fatalf("second recent query should hit Redis cache: err=%v cached=%v calls=%d", err, r2.Cached, repo.trendCalls)
	}

	keys := mr.Keys()
	var recentKey string
	for _, k := range keys {
		if strings.HasPrefix(k, redisCachePrefix+"trend:") {
			recentKey = k
		}
	}
	if recentKey == "" {
		t.Fatalf("expected trend key in Redis, got %v", keys)
	}
	if ttl := mr.TTL(recentKey); ttl <= 0 || ttl > 10*time.Minute {
		t.Errorf("recent TTL = %v, want <= 10m", ttl)
	}

	// 2. Historical query (to < yesterday) -> TTL = 6h.
	if _, err := svc.Trend(context.Background(), TrendQuery{Mode: ModeDay, From: "2026-11-16", To: "2026-11-17"}); err != nil {
		t.Fatal(err)
	}
	var histTTL time.Duration
	for _, k := range mr.Keys() {
		if strings.HasPrefix(k, redisCachePrefix+"trend:") && k != recentKey {
			histTTL = mr.TTL(k)
		}
	}
	if histTTL < 5*time.Hour || histTTL > 6*time.Hour {
		t.Errorf("historical TTL = %v, want ~6h", histTTL)
	}

	// 3. Force refresh bypasses cache once, then cooldown returns cached result on immediate repeat.
	r3, err := svc.Trend(context.Background(), TrendQuery{Mode: ModeDay, From: "2026-11-25", To: "2026-11-28", Refresh: true})
	if err != nil || r3.Cached || repo.trendCalls != 3 {
		t.Fatalf("forced refresh: err=%v cached=%v calls=%d", err, r3.Cached, repo.trendCalls)
	}
	r4, err := svc.Trend(context.Background(), TrendQuery{Mode: ModeDay, From: "2026-11-25", To: "2026-11-28", Refresh: true})
	if err != nil || !r4.Cached || repo.trendCalls != 3 {
		t.Fatalf("cooldown should serve cached result: err=%v cached=%v calls=%d", err, r4.Cached, repo.trendCalls)
	}

	// 4. Redis failure falls back gracefully to PostgreSQL without failing the request.
	svcFallback, repoFB, _, _ := newTestService(t, sampleRows(), failingCache{}, now)
	rFB, err := svcFallback.Trend(context.Background(), TrendQuery{Mode: ModeDay, From: "2026-11-25", To: "2026-11-28"})
	if err != nil || rFB.Cached || repoFB.trendCalls != 1 {
		t.Fatalf("fallback on redis error failed: err=%v cached=%v calls=%d", err, rFB.Cached, repoFB.trendCalls)
	}
}

func TestQueryValidationRejectsBadDatesModesAndExcessiveSpan(t *testing.T) {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.Local)
	svc, _, _, _ := newTestService(t, sampleRows(), nil, now)

	cases := []struct {
		name string
		q    TrendQuery
		code string
	}{
		{"bad mode", TrendQuery{Mode: "hourly"}, "invalid_mode"},
		{"bad group_by", TrendQuery{GroupBy: "ip"}, "invalid_group_by"},
		{"bad from date", TrendQuery{From: "2026/11/01"}, "invalid_date"},
		{"from after to", TrendQuery{From: "2026-11-28", To: "2026-11-20"}, "invalid_date_range"},
		{"day span too large", TrendQuery{Mode: ModeDay, From: "2025-01-01", To: "2026-11-28"}, "range_too_large"},
	}
	for _, tc := range cases {
		_, err := svc.Trend(context.Background(), tc.q)
		var se *Error
		if !errors.As(err, &se) || se.Status != http.StatusBadRequest || se.Code != tc.code {
			t.Errorf("%s: got err=%v, want 400 %s", tc.name, err, tc.code)
		}
	}
}

func TestLiveRequiresAdminAndFailsClosedOnDBError(t *testing.T) {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.Local)
	svc, _, admin, live := newTestService(t, sampleRows(), nil, now)

	// Non-admin user -> 403
	_, err := svc.Live(context.Background(), auth.User{EE: "US01", Name: "Regular User"}, 100)
	var se *Error
	if !errors.As(err, &se) || se.Status != http.StatusForbidden || se.Code != "forbidden_admin" {
		t.Fatalf("non-admin: got %v, want 403 forbidden_admin", err)
	}

	// Admin DB error -> 500 (fail closed)
	admin.err = errors.New("auth db down")
	_, err = svc.Live(context.Background(), auth.User{EE: "AD01", Name: "Admin"}, 100)
	if !errors.As(err, &se) || se.Status != http.StatusInternalServerError {
		t.Fatalf("db error: got %v, want 500", err)
	}
	admin.err = nil

	// Active admin -> 200 and limit clamped to Settings.LiveLogMax
	res, err := svc.Live(context.Background(), auth.User{EE: "AD01", Name: "Admin"}, 5000)
	if err != nil {
		t.Fatalf("admin live failed: %v", err)
	}
	if live.lastLim != MaxLiveLimit || res.Total != 42 || len(res.Entries) != 1 {
		t.Errorf("live result = %+v (lastLim=%d)", res, live.lastLim)
	}
}
