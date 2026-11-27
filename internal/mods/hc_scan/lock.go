package hcscan

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	defaultLockTTL        = 30 * time.Second
	defaultLockWait       = 10 * time.Second
	defaultLockRetryDelay = 25 * time.Millisecond
	lockKeyPrefix         = "dash_go:hc_scan:v1:lock:"
)

// SessionLocker serialises write operations of one scanning session (process:yyyymmdd:shift) so two terminals cannot
// take the last budget vacancy twice. RedisLocker shares the lock across multiple dash_go instances; MemoryLocker is
// the fallback for tests and single-instance development with draft_store=memory.
type SessionLocker interface {
	Lock(ctx context.Context, key string) (unlock func(), err error)
}

// ---- in-memory locker ----

type memoryLocker struct {
	mu sync.Mutex
	m  map[string]*refMutex
}

type refMutex struct {
	sync.Mutex
	refs int
}

// NewMemoryLocker returns a per-key mutex that works within one process.
func NewMemoryLocker() SessionLocker {
	return &memoryLocker{m: map[string]*refMutex{}}
}

func (k *memoryLocker) Lock(ctx context.Context, key string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	k.mu.Lock()
	if k.m == nil {
		k.m = map[string]*refMutex{}
	}
	rm := k.m[key]
	if rm == nil {
		rm = &refMutex{}
		k.m[key] = rm
	}
	rm.refs++
	k.mu.Unlock()
	rm.Lock()
	var once sync.Once
	return func() {
		once.Do(func() {
			rm.Unlock()
			k.mu.Lock()
			rm.refs--
			if rm.refs == 0 {
				delete(k.m, key)
			}
			k.mu.Unlock()
		})
	}, nil
}

// ---- Redis distributed locker ----

var lockExtendScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
	return redis.call('PEXPIRE', KEYS[1], tonumber(ARGV[2]))
end
return 0
`)

var lockReleaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
	return redis.call('DEL', KEYS[1])
end
return 0
`)

type redisLocker struct {
	rdb        *redis.Client
	ttl        time.Duration
	wait       time.Duration
	retryDelay time.Duration
}

// NewRedisLocker returns a distributed per-session lock backed by Redis (SET NX PX + ownership-checked Lua extend
// and release). Zero durations fall back to defaults (30s TTL, 10s wait timeout).
func NewRedisLocker(rdb *redis.Client, ttl, waitTimeout time.Duration) SessionLocker {
	if ttl <= 0 {
		ttl = defaultLockTTL
	}
	if waitTimeout <= 0 {
		waitTimeout = defaultLockWait
	}
	return &redisLocker{
		rdb:        rdb,
		ttl:        ttl,
		wait:       waitTimeout,
		retryDelay: defaultLockRetryDelay,
	}
}

func newLockToken() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func (l *redisLocker) Lock(ctx context.Context, key string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	token, err := newLockToken()
	if err != nil {
		return nil, err
	}
	rkey := lockKeyPrefix + key
	waitCtx, cancel := context.WithTimeout(ctx, l.wait)
	defer cancel()

	for {
		ok, err := l.rdb.SetNX(waitCtx, rkey, token, l.ttl).Result()
		if err != nil {
			return nil, fmt.Errorf("acquire session lock %s: %w", key, err)
		}
		if ok {
			return l.startLease(rkey, token), nil
		}
		timer := time.NewTimer(l.retryDelay)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			return nil, fmt.Errorf("timed out waiting for session lock %s: %w", key, waitCtx.Err())
		case <-timer.C:
		}
	}
}

func (l *redisLocker) startLease(rkey, token string) func() {
	done := make(chan struct{})
	var wg sync.WaitGroup
	extendEvery := l.ttl / 3
	if extendEvery < 10*time.Millisecond {
		extendEvery = 10 * time.Millisecond
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(extendEvery)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				rctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				ok, err := lockExtendScript.Run(rctx, l.rdb, []string{rkey}, token, l.ttl.Milliseconds()).Int()
				cancel()
				if err != nil || ok == 0 {
					return
				}
			}
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			wg.Wait()
			rctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = lockReleaseScript.Run(rctx, l.rdb, []string{rkey}, token).Err()
		})
	}
}
