package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// The limits of failed password sign-ins. Company passwords can be short and a guesser may use many addresses, so
// there is a limit per client address and one per account.
const (
	maxLoginAttempts   = 5
	maxAccountAttempts = 10
	loginWindow        = 10 * time.Minute
	// maxTracked bounds the memory of an in-memory limiter. A real deployment has far fewer clients; beyond it new
	// keys are refused rather than forgotten.
	maxTracked = 100_000

	limitClientKeyPrefix  = "dash_go:auth:limit:ip:"
	limitAccountKeyPrefix = "dash_go:auth:limit:acct:"
)

// Throttle says that a sign-in attempt was refused because a limit is reached.
type Throttle struct {
	// Wait is how long until the limit lets the next attempt through.
	Wait time.Duration
	// ByAccount is true when the account hit its limit, false for the client address.
	ByAccount bool
}

// LoginAttempt is a sign-in attempt that has been counted against the limits of its client address and its account.
// Exactly one of Succeeded, Unavailable or nothing (the attempt failed) settles it.
type LoginAttempt interface {
	Succeeded(ctx context.Context)
	Unavailable(ctx context.Context)
}

// ThrottleStore tracks failed password sign-ins per client address and per account.
type ThrottleStore interface {
	// Begin counts an attempt before it is checked, so guesses that run in parallel cannot all pass the limits
	// before the first failure is known. When a limit is reached it returns the Throttle and counts nothing.
	Begin(ctx context.Context, client, login string) (LoginAttempt, *Throttle, error)
}

// ---- in-memory throttles (tests and development without Redis) ----

// throttles holds the in-memory limits of the password sign-in (the code is the one of the TimeVault project).
type throttles struct {
	clients  *limiter
	accounts *limiter
}

// NewMemoryThrottles keeps failed-login limits in process memory.
func NewMemoryThrottles(now func() time.Time) ThrottleStore {
	if now == nil {
		now = time.Now
	}
	return &throttles{
		clients:  newLimiter(maxLoginAttempts, loginWindow, now),
		accounts: newLimiter(maxAccountAttempts, loginWindow, now),
	}
}

type memAttempt struct {
	t    *throttles
	ip   string
	acct string
}

func (t *throttles) Begin(_ context.Context, client, login string) (LoginAttempt, *Throttle, error) {
	if wait, ok := t.clients.reserve(client); !ok {
		return nil, &Throttle{Wait: wait}, nil
	}
	key := accountKey(login)
	if wait, ok := t.accounts.reserve(key); !ok {
		t.clients.refund(client)
		return nil, &Throttle{Wait: wait, ByAccount: true}, nil
	}
	return &memAttempt{t: t, ip: client, acct: key}, nil, nil
}

// Succeeded settles an attempt that signed in: the address gets its slot back and the failures counted against the
// account are forgotten. The failures of the address stay, so signing in to one's own account does not buy more
// guesses at another.
func (a *memAttempt) Succeeded(_ context.Context) {
	a.t.clients.refund(a.ip)
	a.t.accounts.reset(a.acct)
}

// Unavailable settles an attempt that could not be checked because the user directory did not answer: the account
// is not blamed. The address stays counted, so an anonymous visitor cannot make the directory work without limit.
func (a *memAttempt) Unavailable(_ context.Context) { a.t.accounts.refund(a.acct) }

// accountKey compares accounts the way the directory finds one: without case and, for an e-mail address, by the
// part before the @.
func accountKey(login string) string {
	login = strings.ToLower(strings.TrimSpace(login))
	if i := strings.Index(login, "@"); i > 0 {
		login = login[:i]
	}
	return login
}

// accountKeyHash hashes accountKey before using it in a Redis key so a password typed into the username field by
// mistake is never written to Redis in plain text.
func accountKeyHash(login string) string {
	sum := sha256.Sum256([]byte(accountKey(login)))
	return hex.EncodeToString(sum[:])
}

type entry struct {
	count int
	first time.Time
}

type limiter struct {
	mu        sync.Mutex
	max       int
	window    time.Duration
	now       func() time.Time
	seen      map[string]*entry
	lastPurge time.Time
}

func newLimiter(max int, window time.Duration, now func() time.Time) *limiter {
	return &limiter{max: max, window: window, now: now, seen: map[string]*entry{}}
}

// reserve counts an attempt of key. When the limit is reached it returns how long to wait and counts nothing.
func (l *limiter) reserve(key string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.purge(now)
	e, ok := l.seen[key]
	if ok && now.Sub(e.first) >= l.window {
		delete(l.seen, key)
		ok = false
	}
	if !ok {
		if len(l.seen) >= maxTracked {
			return l.window, false
		}
		l.seen[key] = &entry{count: 1, first: now}
		return 0, true
	}
	if e.count >= l.max {
		return l.window - now.Sub(e.first), false
	}
	e.count++
	return 0, true
}

// purge drops the entries whose window has ended, at most once per tenth of a window, so a flood of distinct keys
// cannot make every call walk the map.
func (l *limiter) purge(now time.Time) {
	if now.Sub(l.lastPurge) < l.window/10 {
		return
	}
	l.lastPurge = now
	for k, e := range l.seen {
		if now.Sub(e.first) >= l.window {
			delete(l.seen, k)
		}
	}
}

// refund gives back one counted attempt.
func (l *limiter) refund(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.seen[key]; ok {
		if e.count <= 1 {
			delete(l.seen, key)
		} else {
			e.count--
		}
	}
}

func (l *limiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.seen, key)
}

// ---- Redis throttles (shared across instances) ----

var limitReserveScript = redis.NewScript(`
local cur = tonumber(redis.call('GET', KEYS[1]) or '0')
local max = tonumber(ARGV[1])
local win = tonumber(ARGV[2])
if cur >= max then
	local pttl = redis.call('PTTL', KEYS[1])
	if pttl < 0 then
		pttl = win
	end
	return {0, pttl}
end
local nextVal = redis.call('INCR', KEYS[1])
if nextVal == 1 or redis.call('PTTL', KEYS[1]) < 0 then
	redis.call('PEXPIRE', KEYS[1], win)
end
return {1, 0}
`)

var limitRefundScript = redis.NewScript(`
local cur = tonumber(redis.call('GET', KEYS[1]) or '0')
if cur <= 1 then
	redis.call('DEL', KEYS[1])
else
	redis.call('DECR', KEYS[1])
end
return 1
`)

type redisThrottles struct {
	rdb    *redis.Client
	window time.Duration
}

// NewRedisThrottles stores failed password login counters in Redis so all dash_go instances enforce the same limits.
func NewRedisThrottles(rdb *redis.Client) ThrottleStore {
	return &redisThrottles{rdb: rdb, window: loginWindow}
}

func (t *redisThrottles) reserve(ctx context.Context, key string, max int) (time.Duration, bool, error) {
	vals, err := limitReserveScript.Run(ctx, t.rdb, []string{key}, max, t.window.Milliseconds()).Int64Slice()
	if err != nil {
		return 0, false, err
	}
	if len(vals) < 2 || vals[0] == 0 {
		wait := t.window
		if len(vals) >= 2 && vals[1] > 0 {
			wait = time.Duration(vals[1]) * time.Millisecond
		}
		return wait, false, nil
	}
	return 0, true, nil
}

func (t *redisThrottles) refund(ctx context.Context, key string) {
	_ = limitRefundScript.Run(ctx, t.rdb, []string{key}).Err()
}

func (t *redisThrottles) reset(ctx context.Context, key string) {
	_ = t.rdb.Del(ctx, key).Err()
}

func (t *redisThrottles) Begin(ctx context.Context, client, login string) (LoginAttempt, *Throttle, error) {
	ipKey := limitClientKeyPrefix + client
	wait, ok, err := t.reserve(ctx, ipKey, maxLoginAttempts)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, &Throttle{Wait: wait}, nil
	}
	acctKey := limitAccountKeyPrefix + accountKeyHash(login)
	wait, ok, err = t.reserve(ctx, acctKey, maxAccountAttempts)
	if err != nil {
		t.refund(ctx, ipKey)
		return nil, nil, err
	}
	if !ok {
		t.refund(ctx, ipKey)
		return nil, &Throttle{Wait: wait, ByAccount: true}, nil
	}
	return &redisAttempt{t: t, ipKey: ipKey, acctKey: acctKey}, nil, nil
}

type redisAttempt struct {
	t       *redisThrottles
	ipKey   string
	acctKey string
}

func (a *redisAttempt) Succeeded(_ context.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	a.t.refund(ctx, a.ipKey)
	a.t.reset(ctx, a.acctKey)
}

func (a *redisAttempt) Unavailable(_ context.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	a.t.refund(ctx, a.acctKey)
}
