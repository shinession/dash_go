package weblog

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	redisCachePrefix    = "dash_go:web_log:v1:"
	redisCooldownPrefix = "dash_go:web_log:v1:cd:"
)

// redisCache stores JSON payloads in shared Redis so s2 and s3 instances share cached query results.
type redisCache struct {
	rdb *redis.Client
}

// NewRedisCache returns a Cache backed by the shared Redis client.
func NewRedisCache(rdb *redis.Client) Cache {
	if rdb == nil {
		return nil
	}
	return &redisCache{rdb: rdb}
}

func (c *redisCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	b, err := c.rdb.Get(ctx, redisCachePrefix+key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

func (c *redisCache) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	if ttl <= 0 {
		return nil
	}
	return c.rdb.Set(ctx, redisCachePrefix+key, val, ttl).Err()
}

func (c *redisCache) TryRefreshCooldown(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		return true, nil
	}
	return c.rdb.SetNX(ctx, redisCooldownPrefix+key, "1", ttl).Result()
}

// ---- in-memory cache (fallback when Redis is unconfigured, and for tests) ----

type memCacheEntry struct {
	data []byte
	exp  time.Time
}

type memoryCache struct {
	mu       sync.Mutex
	now      func() time.Time
	items    map[string]memCacheEntry
	cooldown map[string]time.Time
}

// NewMemoryCache returns a process-local Cache with TTL expiry.
func NewMemoryCache(now func() time.Time) Cache {
	if now == nil {
		now = time.Now
	}
	return &memoryCache{
		now:      now,
		items:    map[string]memCacheEntry{},
		cooldown: map[string]time.Time{},
	}
}

func (m *memoryCache) Get(_ context.Context, key string) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.items[key]
	if !ok {
		return nil, false, nil
	}
	if m.now().After(it.exp) {
		delete(m.items, key)
		return nil, false, nil
	}
	cp := make([]byte, len(it.data))
	copy(cp, it.data)
	return cp, true, nil
}

func (m *memoryCache) Set(_ context.Context, key string, val []byte, ttl time.Duration) error {
	if ttl <= 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(val))
	copy(cp, val)
	m.items[key] = memCacheEntry{data: cp, exp: m.now().Add(ttl)}
	return nil
}

func (m *memoryCache) TryRefreshCooldown(_ context.Context, key string, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		return true, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if exp, ok := m.cooldown[key]; ok && now.Before(exp) {
		return false, nil
	}
	m.cooldown[key] = now.Add(ttl)
	return true, nil
}
