package weblog

import (
	"context"
	"time"
)

// LogRepository reads aggregated web log statistics from the PostgreSQL Log database (t_log_daily).
type LogRepository interface {
	Meta(ctx context.Context) (MetaData, error)
	QueryTrend(ctx context.Context, f TrendDBFilter) (TrendRawResult, error)
}

// AdminChecker checks whether a signed-in user is an active system administrator in Auth."T_USER_ALL".
type AdminChecker interface {
	IsUserAdmin(ctx context.Context, eeCode string) (bool, error)
}

// Cache stores serialized JSON query and metadata results in Redis (shared across instances).
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Set(ctx context.Context, key string, val []byte, ttl time.Duration) error
	// TryRefreshCooldown claims a short cooldown slot for key; returns true when a forced refresh may proceed.
	TryRefreshCooldown(ctx context.Context, key string, ttl time.Duration) (bool, error)
}

// LiveLogStore reads recent real-time entries from the Redis web_log list.
type LiveLogStore interface {
	Recent(ctx context.Context, limit int) (entries []LiveLogEntry, total int64, err error)
}
