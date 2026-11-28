package weblog

import "time"

// Aggregation granularities supported by the Web Log trend chart.
const (
	ModeDay   = "day"
	ModeWeek  = "week"
	ModeMonth = "month"
)

// Stacked bar breakdown modes.
const (
	GroupByAuto = "auto" // By App when App=All; by Category (tools) when a specific App is selected.
	GroupByHost = "host" // Stacked bars broken down by normalized server host.
)

// Canonical fallback values for NULL/blank columns in t_log_daily.
const (
	DefaultHost     = "prod_eng"
	DefaultApp      = "Others"
	DefaultCategory = "others"
	OtherSeriesKey  = "__others__"
)

// Validation and safety limits.
const (
	MaxDaySpanDays    = 366
	MaxPeriodSpanDays = 1830 // ~5 years for week / month views
	DefaultLiveLimit  = 500
	MaxLiveLimit      = 1000
)

// Settings are the optional module settings under config key modules.web_log.
type Settings struct {
	RecentCacheMinutes  int    `json:"recent_cache_minutes"`  // TTL for queries covering yesterday/today (default 10)
	HistoryCacheMinutes int    `json:"history_cache_minutes"` // TTL for historical-only queries (default 360)
	MetaCacheMinutes    int    `json:"meta_cache_minutes"`    // TTL for available apps/hosts/categories (default 15)
	RefreshCooldownSec  int    `json:"refresh_cooldown_sec"`  // Minimum interval between forced refreshes per key (default 15)
	MaxSeries           int    `json:"max_series"`            // Max stacked series before folding tail into Others (default 15)
	LiveLogKey          string `json:"redis_log_key"`         // Redis list key for real-time logs (default "web_log")
	LiveLogMax          int    `json:"live_log_max"`          // Max lines allowed from Redis live log (default 1000)
}

// DefaultSettings returns the default settings for web_log.
func DefaultSettings() Settings {
	return Settings{
		RecentCacheMinutes:  10,
		HistoryCacheMinutes: 360,
		MetaCacheMinutes:    15,
		RefreshCooldownSec:  15,
		MaxSeries:           15,
		LiveLogKey:          "web_log",
		LiveLogMax:          MaxLiveLimit,
	}
}

func (s Settings) normalized() Settings {
	def := DefaultSettings()
	if s.RecentCacheMinutes <= 0 {
		s.RecentCacheMinutes = def.RecentCacheMinutes
	}
	if s.HistoryCacheMinutes <= 0 {
		s.HistoryCacheMinutes = def.HistoryCacheMinutes
	}
	if s.MetaCacheMinutes <= 0 {
		s.MetaCacheMinutes = def.MetaCacheMinutes
	}
	if s.RefreshCooldownSec <= 0 {
		s.RefreshCooldownSec = def.RefreshCooldownSec
	}
	if s.MaxSeries <= 0 {
		s.MaxSeries = def.MaxSeries
	}
	if s.LiveLogKey == "" {
		s.LiveLogKey = def.LiveLogKey
	}
	if s.LiveLogMax <= 0 || s.LiveLogMax > MaxLiveLimit {
		s.LiveLogMax = def.LiveLogMax
	}
	return s
}

// UserView is the optional signed-in user summary returned by /api/meta.
type UserView struct {
	EE   string `json:"ee"`
	Name string `json:"name"`
}

// MetaData is the cacheable portion of /api/meta from t_log_daily.
type MetaData struct {
	Apps            []string            `json:"apps"`
	Hosts           []string            `json:"hosts"`
	CategoriesByApp map[string][]string `json:"categories_by_app"`
	AllCategories   []string            `json:"all_categories"`
	MinDate         string              `json:"min_date"`
	MaxDate         string              `json:"max_date"`
	GeneratedAt     string              `json:"generated_at"`
}

// MetaResponse is returned by GET /dash_go/web_log/api/meta.
type MetaResponse struct {
	User            *UserView           `json:"user,omitempty"`
	IsAdmin         bool                `json:"is_admin"`
	Apps            []string            `json:"apps"`
	Hosts           []string            `json:"hosts"`
	CategoriesByApp map[string][]string `json:"categories_by_app"`
	AllCategories   []string            `json:"all_categories"`
	MinDate         string              `json:"min_date"`
	MaxDate         string              `json:"max_date"`
	DefaultTo       string              `json:"default_to"`
	Cached          bool                `json:"cached"`
	GeneratedAt     string              `json:"generated_at"`
	Version         string              `json:"version"`
}

// TrendQuery is the normalized request for GET /dash_go/web_log/api/trend.
type TrendQuery struct {
	Mode        string   `json:"mode"`
	From        string   `json:"from"`
	To          string   `json:"to"`
	App         string   `json:"app"`          // empty = All
	GroupBy     string   `json:"group_by"`     // "auto" or "host"
	Hosts       []string `json:"hosts"`        // empty = All
	Categories  []string `json:"categories"`   // empty = All
	ExcludeIdle bool     `json:"exclude_idle"`
	Refresh     bool     `json:"-"`
}

// PeriodBucket describes one point on the X axis (calendar day, ISO week, or calendar month).
type PeriodBucket struct {
	Key   string `json:"key"`   // "2026-11-28", "2026-W48", or "2026-11"
	Label string `json:"label"` // display label on the X axis
	Start string `json:"start"` // YYYY-MM-DD inclusive start
	End   string `json:"end"`   // YYYY-MM-DD inclusive end
}

// ChartSeries is one stacked bar series in the trend chart.
type ChartSeries struct {
	Name       string  `json:"name"`
	IsOther    bool    `json:"is_other,omitempty"`
	OtherCount int     `json:"other_count,omitempty"`
	Total      int64   `json:"total"`
	Values     []int64 `json:"values"`
}

// BreakdownItem is one row in the ungrouped summary table below the chart.
type BreakdownItem struct {
	Name     string  `json:"name"`
	Actions  int64   `json:"actions"`
	Visitors int64   `json:"visitors"`
	SharePct float64 `json:"share_pct"`
}

// TrendSummary holds KPI headline figures for the selected filter window.
type TrendSummary struct {
	TotalActions      int64   `json:"total_actions"`
	UniqueVisitors    int64   `json:"unique_visitors"`
	AvgActionsPeriod  float64 `json:"avg_actions_period"`
	AvgVisitorsPeriod float64 `json:"avg_visitors_period"`
	PeakPeriodLabel   string  `json:"peak_period_label"`
	PeakPeriodActions int64   `json:"peak_period_actions"`
	SeriesCount       int     `json:"series_count"`
}

// TrendResponse is returned by GET /dash_go/web_log/api/trend.
type TrendResponse struct {
	Mode             string          `json:"mode"`
	From             string          `json:"from"`
	To               string          `json:"to"`
	App              string          `json:"app"`
	GroupBy          string          `json:"group_by"`
	EffectiveGroupBy string          `json:"effective_group_by"` // "app", "category", or "host"
	Hosts            []string        `json:"hosts"`
	Categories       []string        `json:"categories"`
	ExcludeIdle      bool            `json:"exclude_idle"`
	Periods          []PeriodBucket  `json:"periods"`
	Series           []ChartSeries   `json:"series"`
	TotalActions     []int64         `json:"total_actions"`
	VisitorCounts    []int64         `json:"visitor_counts"`
	Breakdown        []BreakdownItem `json:"breakdown"`
	Summary          TrendSummary    `json:"summary"`
	Cached           bool            `json:"cached"`
	GeneratedAt      string          `json:"generated_at"`
}

// TrendRawResult is the raw aggregated database output before period filling and top-N folding.
type TrendRawResult struct {
	// PeriodSeries maps period_start (YYYY-MM-DD) -> seriesName -> actions.
	PeriodSeries map[string]map[string]int64
	// PeriodTotals maps period_start (YYYY-MM-DD) -> (actions, distinct visitors).
	PeriodTotals map[string]PeriodStat
	// SeriesTotals maps seriesName -> (total actions, distinct visitors across entire range).
	SeriesTotals map[string]PeriodStat
	// Overall holds total actions and distinct visitors across the entire range.
	Overall PeriodStat
}

// PeriodStat holds an action count and deduplicated visitor IP count.
type PeriodStat struct {
	Actions  int64
	Visitors int64
}

// LiveLogEntry is one parsed line from the Redis web_log list (admin only).
type LiveLogEntry struct {
	Seq       int    `json:"seq"`
	Raw       string `json:"raw"`
	IP        string `json:"ip,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
	App       string `json:"app,omitempty"`
	Action    string `json:"action,omitempty"`
	Message   string `json:"message"`
	Status    string `json:"status,omitempty"`
	Level     string `json:"level"`
	Host      string `json:"host,omitempty"`
}

// LiveLogResponse is returned by GET /dash_go/web_log/api/live (admin only).
type LiveLogResponse struct {
	Entries   []LiveLogEntry `json:"entries"`
	Total     int64          `json:"total"`
	Limit     int            `json:"limit"`
	FetchedAt string         `json:"fetched_at"`
}

// TrendDBFilter is the normalized filter passed to LogRepository.QueryTrend.
type TrendDBFilter struct {
	Mode             string
	From             time.Time
	To               time.Time
	App              string // empty = All
	EffectiveGroupBy string // "app", "category", or "host"
	Hosts            []string
	Categories       []string
	ExcludeIdle      bool
}
