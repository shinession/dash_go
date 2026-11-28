package weblog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"dash_go/internal/core/auth"
	"dash_go/internal/core/i18n"
	"dash_go/internal/version"
)

// Error is returned by Service; the HTTP layer maps it to a status code and localized JSON message.
type Error struct {
	Status int
	Code   string
	Key    string
	Args   i18n.P
}

func (e *Error) Error() string { return e.Code }

func newError(status int, code string, args ...i18n.P) *Error {
	e := &Error{Status: status, Code: code, Key: "wl.err." + code}
	if len(args) > 0 {
		e.Args = args[0]
	}
	return e
}

// Deps are the collaborators of Service.
type Deps struct {
	Repo     LogRepository
	Admin    AdminChecker
	Cache    Cache
	LiveLogs LiveLogStore
	Settings Settings
	Log      *slog.Logger
	Now      func() time.Time
}

// Service implements the Web Log analytics, Redis caching, and admin live log streaming logic.
type Service struct {
	Deps
}

// NewService validates dependencies and returns a Service.
func NewService(d Deps) (*Service, error) {
	if d.Repo == nil {
		return nil, fmt.Errorf("web_log: log repository is required")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	d.Settings = d.Settings.normalized()
	return &Service{Deps: d}, nil
}

// Meta returns available filter options (cached in Redis) together with the caller's optional admin status.
func (s *Service) Meta(ctx context.Context, user *auth.User, refresh bool) (MetaResponse, error) {
	const cacheKey = "meta"
	var (
		meta   MetaData
		cached bool
	)

	allowBypass := false
	if refresh {
		allowBypass = s.tryRefresh(ctx, cacheKey)
	}
	if !allowBypass && s.Cache != nil {
		if raw, ok, err := s.Cache.Get(ctx, cacheKey); err != nil {
			s.Log.Warn("web_log: redis meta cache get failed; falling back to PostgreSQL", "error", err)
		} else if ok {
			if err := json.Unmarshal(raw, &meta); err == nil {
				cached = true
			}
		}
	}

	if !cached {
		var err error
		meta, err = s.Repo.Meta(ctx)
		if err != nil {
			s.Log.Error("web_log: read metadata failed", "error", err)
			return MetaResponse{}, s.internal("read metadata")
		}
		meta = normalizeMeta(meta)
		meta.GeneratedAt = s.Now().Format("2006-01-02 15:04:05")
		if s.Cache != nil {
			if raw, err := json.Marshal(meta); err == nil {
				ttl := time.Duration(s.Settings.MetaCacheMinutes) * time.Minute
				if err := s.Cache.Set(ctx, cacheKey, raw, ttl); err != nil {
					s.Log.Warn("web_log: redis meta cache set failed", "error", err)
				}
			}
		}
	} else {
		meta = normalizeMeta(meta)
	}

	resp := MetaResponse{
		Apps:            meta.Apps,
		Hosts:           meta.Hosts,
		CategoriesByApp: meta.CategoriesByApp,
		AllCategories:   meta.AllCategories,
		MinDate:         meta.MinDate,
		MaxDate:         meta.MaxDate,
		DefaultTo:       s.today().Format("2006-01-02"),
		Cached:          cached,
		GeneratedAt:     meta.GeneratedAt,
		Version:         version.Version,
	}
	if user != nil {
		resp.User = &UserView{EE: user.EE, Name: user.Name}
		if s.Admin != nil {
			isAdmin, err := s.Admin.IsUserAdmin(ctx, user.EE)
			if err != nil {
				s.Log.Warn("web_log: T_USER_ALL admin check failed during meta; defaulting non-admin", "user", user.EE, "error", err)
			} else {
				resp.IsAdmin = isAdmin
			}
		}
	}
	return resp, nil
}

// Trend returns aggregated calendar-bucketed action counts and period-distinct visitor IP counts, cached in Redis.
func (s *Service) Trend(ctx context.Context, q TrendQuery) (TrendResponse, error) {
	norm, fromDate, toDate, err := s.normalizeQuery(q)
	if err != nil {
		return TrendResponse{}, err
	}
	effGroup := effectiveGroupBy(norm.GroupBy, norm.App)
	cacheKey := trendCacheKey(norm, effGroup)

	allowBypass := false
	if q.Refresh {
		allowBypass = s.tryRefresh(ctx, cacheKey)
	}
	if !allowBypass && s.Cache != nil {
		if raw, ok, err := s.Cache.Get(ctx, cacheKey); err != nil {
			s.Log.Warn("web_log: redis trend cache get failed; falling back to PostgreSQL", "error", err)
		} else if ok {
			var cachedResp TrendResponse
			if err := json.Unmarshal(raw, &cachedResp); err == nil {
				cachedResp.Cached = true
				return cachedResp, nil
			}
		}
	}

	raw, err := s.Repo.QueryTrend(ctx, TrendDBFilter{
		Mode:             norm.Mode,
		From:             fromDate,
		To:               toDate,
		App:              norm.App,
		EffectiveGroupBy: effGroup,
		Hosts:            norm.Hosts,
		Categories:       norm.Categories,
		ExcludeIdle:      norm.ExcludeIdle,
	})
	if err != nil {
		s.Log.Error("web_log: query trend failed", "mode", norm.Mode, "from", norm.From, "to", norm.To, "app", norm.App, "error", err)
		return TrendResponse{}, s.internal("query trend")
	}

	resp := s.buildTrendResponse(norm, effGroup, fromDate, toDate, raw)

	if s.Cache != nil {
		if encoded, err := json.Marshal(resp); err == nil {
			ttl := s.trendTTL(toDate)
			if err := s.Cache.Set(ctx, cacheKey, encoded, ttl); err != nil {
				s.Log.Warn("web_log: redis trend cache set failed", "error", err)
			}
		}
	}
	return resp, nil
}

// Live returns the newest Redis web_log entries strictly for active Auth."T_USER_ALL"."Type" = 'admin' users.
func (s *Service) Live(ctx context.Context, u auth.User, limit int) (LiveLogResponse, error) {
	if s.Admin == nil {
		return LiveLogResponse{}, newError(http.StatusForbidden, "forbidden_admin")
	}
	isAdmin, err := s.Admin.IsUserAdmin(ctx, u.EE)
	if err != nil {
		s.Log.Error("web_log: T_USER_ALL admin check failed for live log", "user", u.EE, "error", err)
		return LiveLogResponse{}, s.internal("check admin role")
	}
	if !isAdmin {
		return LiveLogResponse{}, newError(http.StatusForbidden, "forbidden_admin")
	}
	if s.LiveLogs == nil {
		return LiveLogResponse{}, newError(http.StatusServiceUnavailable, "live_unavailable")
	}
	if limit <= 0 {
		limit = DefaultLiveLimit
	}
	if limit > s.Settings.LiveLogMax {
		limit = s.Settings.LiveLogMax
	}
	entries, total, err := s.LiveLogs.Recent(ctx, limit)
	if err != nil {
		s.Log.Error("web_log: read redis live log failed", "user", u.EE, "error", err)
		return LiveLogResponse{}, s.internal("read live log")
	}
	if entries == nil {
		entries = []LiveLogEntry{}
	}
	return LiveLogResponse{
		Entries:   entries,
		Total:     total,
		Limit:     limit,
		FetchedAt: s.Now().Format("2006-01-02 15:04:05"),
	}, nil
}

func (s *Service) normalizeQuery(q TrendQuery) (TrendQuery, time.Time, time.Time, error) {
	mode := strings.ToLower(strings.TrimSpace(q.Mode))
	if mode == "" {
		mode = ModeDay
	}
	switch mode {
	case ModeDay, ModeWeek, ModeMonth:
	default:
		return TrendQuery{}, time.Time{}, time.Time{}, newError(http.StatusBadRequest, "invalid_mode", i18n.P{"mode": q.Mode})
	}

	groupBy := strings.ToLower(strings.TrimSpace(q.GroupBy))
	if groupBy == "" {
		groupBy = GroupByAuto
	}
	switch groupBy {
	case GroupByAuto, GroupByHost:
	default:
		return TrendQuery{}, time.Time{}, time.Time{}, newError(http.StatusBadRequest, "invalid_group_by", i18n.P{"group_by": q.GroupBy})
	}

	app := strings.TrimSpace(q.App)
	if strings.EqualFold(app, "all") {
		app = ""
	}
	hosts := cleanStringList(q.Hosts, true)
	cats := cleanStringList(q.Categories, false)

	today := s.today()
	var (
		fromDate, toDate time.Time
		err              error
	)
	toRaw := strings.TrimSpace(q.To)
	fromRaw := strings.TrimSpace(q.From)

	if toRaw != "" {
		toDate, err = parseDate(toRaw)
		if err != nil {
			return TrendQuery{}, time.Time{}, time.Time{}, newError(http.StatusBadRequest, "invalid_date", i18n.P{"value": toRaw})
		}
	}
	if fromRaw != "" {
		fromDate, err = parseDate(fromRaw)
		if err != nil {
			return TrendQuery{}, time.Time{}, time.Time{}, newError(http.StatusBadRequest, "invalid_date", i18n.P{"value": fromRaw})
		}
	}

	if toDate.IsZero() {
		if !fromDate.IsZero() && fromDate.After(today) {
			toDate = fromDate
		} else {
			toDate = today
		}
	}
	if fromDate.IsZero() {
		fromDate = defaultFromDate(mode, toDate)
	}
	if fromDate.After(toDate) {
		return TrendQuery{}, time.Time{}, time.Time{}, newError(http.StatusBadRequest, "invalid_date_range")
	}

	spanDays := int(toDate.Sub(fromDate)/(24*time.Hour)) + 1
	maxSpan := MaxPeriodSpanDays
	if mode == ModeDay {
		maxSpan = MaxDaySpanDays
	}
	if spanDays > maxSpan {
		return TrendQuery{}, time.Time{}, time.Time{}, newError(http.StatusBadRequest, "range_too_large", i18n.P{"max": maxSpan})
	}

	norm := TrendQuery{
		Mode:        mode,
		From:        fromDate.Format("2006-01-02"),
		To:          toDate.Format("2006-01-02"),
		App:         app,
		GroupBy:     groupBy,
		Hosts:       hosts,
		Categories:  cats,
		ExcludeIdle: q.ExcludeIdle,
		Refresh:     q.Refresh,
	}
	return norm, fromDate, toDate, nil
}

func (s *Service) buildTrendResponse(q TrendQuery, effGroup string, fromDate, toDate time.Time, raw TrendRawResult) TrendResponse {
	periods := buildPeriods(q.Mode, fromDate, toDate)
	n := len(periods)

	totalActions := make([]int64, n)
	visitorCounts := make([]int64, n)
	var (
		peakLabel   string
		peakActions int64
		sumActions  int64
		sumVisitors int64
	)
	for i, p := range periods {
		st := raw.PeriodTotals[p.Start]
		totalActions[i] = st.Actions
		visitorCounts[i] = st.Visitors
		sumActions += st.Actions
		sumVisitors += st.Visitors
		if st.Actions > peakActions {
			peakActions = st.Actions
			peakLabel = p.Label
		}
	}

	// Order series descending by total actions across the range, breaking ties alphabetically.
	names := make([]string, 0, len(raw.SeriesTotals))
	for name := range raw.SeriesTotals {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		ai, aj := raw.SeriesTotals[names[i]].Actions, raw.SeriesTotals[names[j]].Actions
		if ai != aj {
			return ai > aj
		}
		return names[i] < names[j]
	})

	overallActions := raw.Overall.Actions
	if overallActions == 0 && sumActions > 0 {
		overallActions = sumActions
	}
	breakdown := make([]BreakdownItem, 0, len(names))
	for _, name := range names {
		st := raw.SeriesTotals[name]
		var share float64
		if overallActions > 0 {
			share = math.Round(float64(st.Actions)*10000/float64(overallActions)) / 100
		}
		breakdown = append(breakdown, BreakdownItem{
			Name:     name,
			Actions:  st.Actions,
			Visitors: st.Visitors,
			SharePct: share,
		})
	}

	maxSeries := s.Settings.MaxSeries
	series := make([]ChartSeries, 0, min(len(names), maxSeries+1))
	directCount := len(names)
	if directCount > maxSeries {
		directCount = maxSeries
	}
	for _, name := range names[:directCount] {
		vals := make([]int64, n)
		for i, p := range periods {
			vals[i] = raw.PeriodSeries[p.Start][name]
		}
		series = append(series, ChartSeries{
			Name:   name,
			Total:  raw.SeriesTotals[name].Actions,
			Values: vals,
		})
	}
	if len(names) > maxSeries {
		tailNames := names[maxSeries:]
		vals := make([]int64, n)
		var tailTotal int64
		for _, name := range tailNames {
			tailTotal += raw.SeriesTotals[name].Actions
			for i, p := range periods {
				vals[i] += raw.PeriodSeries[p.Start][name]
			}
		}
		series = append(series, ChartSeries{
			Name:       OtherSeriesKey,
			IsOther:    true,
			OtherCount: len(tailNames),
			Total:      tailTotal,
			Values:     vals,
		})
	}

	var avgAct, avgVis float64
	if n > 0 {
		avgAct = math.Round(float64(sumActions)*10/float64(n)) / 10
		avgVis = math.Round(float64(sumVisitors)*10/float64(n)) / 10
	}

	return TrendResponse{
		Mode:             q.Mode,
		From:             q.From,
		To:               q.To,
		App:              q.App,
		GroupBy:          q.GroupBy,
		EffectiveGroupBy: effGroup,
		Hosts:            nonNilSlice(q.Hosts),
		Categories:       nonNilSlice(q.Categories),
		ExcludeIdle:      q.ExcludeIdle,
		Periods:          periods,
		Series:           series,
		TotalActions:     totalActions,
		VisitorCounts:    visitorCounts,
		Breakdown:        breakdown,
		Summary: TrendSummary{
			TotalActions:      overallActions,
			UniqueVisitors:    raw.Overall.Visitors,
			AvgActionsPeriod:  avgAct,
			AvgVisitorsPeriod: avgVis,
			PeakPeriodLabel:   peakLabel,
			PeakPeriodActions: peakActions,
			SeriesCount:       len(names),
		},
		Cached:      false,
		GeneratedAt: s.Now().Format("2006-01-02 15:04:05"),
	}
}

func (s *Service) tryRefresh(ctx context.Context, key string) bool {
	if s.Cache == nil {
		return true
	}
	cd := time.Duration(s.Settings.RefreshCooldownSec) * time.Second
	ok, err := s.Cache.TryRefreshCooldown(ctx, key, cd)
	if err != nil {
		s.Log.Warn("web_log: refresh cooldown check failed; proceeding with live query", "error", err)
		return true
	}
	return ok
}

func (s *Service) trendTTL(toDate time.Time) time.Duration {
	yesterday := s.today().AddDate(0, 0, -1)
	if !toDate.Before(yesterday) {
		return time.Duration(s.Settings.RecentCacheMinutes) * time.Minute
	}
	return time.Duration(s.Settings.HistoryCacheMinutes) * time.Minute
}

func (s *Service) today() time.Time {
	now := s.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

func (s *Service) internal(step string) *Error {
	return newError(http.StatusInternalServerError, "internal", i18n.P{"step": stepKey(step)})
}

func stepKey(step string) string {
	return "wl.step." + strings.ReplaceAll(strings.ToLower(step), " ", "_")
}

func effectiveGroupBy(groupBy, app string) string {
	if groupBy == GroupByHost {
		return "host"
	}
	if app == "" {
		return "app"
	}
	return "category"
}

func trendCacheKey(q TrendQuery, effGroup string) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "m=%s|f=%s|t=%s|a=%s|g=%s|eg=%s|ei=%t|h=%s|c=%s",
		q.Mode, q.From, q.To, q.App, q.GroupBy, effGroup, q.ExcludeIdle,
		strings.Join(q.Hosts, "\x1f"), strings.Join(q.Categories, "\x1f"))
	return "trend:" + hex.EncodeToString(h.Sum(nil)[:16])
}

func parseDate(s string) (time.Time, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), nil
}

func mondayOf(d time.Time) time.Time {
	offset := (int(d.Weekday()) + 6) % 7 // Monday = 0 ... Sunday = 6
	return d.AddDate(0, 0, -offset)
}

func firstOfMonth(d time.Time) time.Time {
	return time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func defaultFromDate(mode string, to time.Time) time.Time {
	switch mode {
	case ModeWeek:
		return mondayOf(to.AddDate(0, 0, -11*7))
	case ModeMonth:
		return firstOfMonth(to).AddDate(0, -11, 0)
	default:
		return to.AddDate(0, 0, -29)
	}
}

// buildPeriods generates the contiguous standard-calendar X-axis buckets covering [from, to].
func buildPeriods(mode string, from, to time.Time) []PeriodBucket {
	out := []PeriodBucket{}
	switch mode {
	case ModeWeek:
		start := mondayOf(from)
		end := mondayOf(to)
		for cur := start; !cur.After(end); cur = cur.AddDate(0, 0, 7) {
			iy, iw := cur.ISOWeek()
			key := fmt.Sprintf("%04d-W%02d", iy, iw)
			out = append(out, PeriodBucket{
				Key:   key,
				Label: key,
				Start: cur.Format("2006-01-02"),
				End:   cur.AddDate(0, 0, 6).Format("2006-01-02"),
			})
		}
	case ModeMonth:
		start := firstOfMonth(from)
		end := firstOfMonth(to)
		for cur := start; !cur.After(end); cur = cur.AddDate(0, 1, 0) {
			key := cur.Format("2006-01")
			lastDay := cur.AddDate(0, 1, -1)
			out = append(out, PeriodBucket{
				Key:   key,
				Label: key,
				Start: cur.Format("2006-01-02"),
				End:   lastDay.Format("2006-01-02"),
			})
		}
	default:
		for cur := from; !cur.After(to); cur = cur.AddDate(0, 0, 1) {
			key := cur.Format("2006-01-02")
			out = append(out, PeriodBucket{
				Key:   key,
				Label: key,
				Start: key,
				End:   key,
			})
		}
	}
	return out
}

func cleanStringList(in []string, dropAll bool) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, raw := range in {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		if dropAll && strings.EqualFold(v, "all") {
			continue
		}
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func nonNilSlice(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func normalizeMeta(m MetaData) MetaData {
	if m.Apps == nil {
		m.Apps = []string{}
	}
	if m.Hosts == nil {
		m.Hosts = []string{}
	}
	if m.AllCategories == nil {
		m.AllCategories = []string{}
	}
	if m.CategoriesByApp == nil {
		m.CategoriesByApp = map[string][]string{}
	}
	return m
}
