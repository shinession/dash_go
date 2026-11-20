package auth

import (
	"strings"
	"sync"
	"time"
)

// The limits of failed password sign-ins. Company passwords can be short and a guesser may use many addresses, so
// there is a limit per client address and one per account.
const (
	maxLoginAttempts   = 5
	maxAccountAttempts = 10
	loginWindow        = 10 * time.Minute
	// maxTracked bounds the memory of a limiter. A real deployment has far fewer clients; beyond it new keys are
	// refused rather than forgotten.
	maxTracked = 100_000
)

// Throttle says that a sign-in attempt was refused because a limit is reached.
type Throttle struct {
	// Wait is how long until the limit lets the next attempt through.
	Wait time.Duration
	// ByAccount is true when the account hit its limit, false for the client address.
	ByAccount bool
}

// throttles holds the limits of the password sign-in (the code is the one of the TimeVault project).
type throttles struct {
	clients  *limiter
	accounts *limiter
}

func newThrottles(now func() time.Time) *throttles {
	return &throttles{clients: newLimiter(maxLoginAttempts, loginWindow, now), accounts: newLimiter(maxAccountAttempts, loginWindow, now)}
}

// attempt is a sign-in attempt that has been counted against the limits of its client address and its account.
// Exactly one of succeeded, unavailable or nothing (the attempt failed) settles it.
type attempt struct {
	t    *throttles
	ip   string
	acct string
}

// begin counts an attempt before it is checked, so guesses that run in parallel cannot all pass the limits before
// the first failure is known. When a limit is reached it returns the Throttle and counts nothing.
func (t *throttles) begin(client, login string) (*attempt, *Throttle) {
	if wait, ok := t.clients.reserve(client); !ok {
		return nil, &Throttle{Wait: wait}
	}
	key := accountKey(login)
	if wait, ok := t.accounts.reserve(key); !ok {
		t.clients.refund(client)
		return nil, &Throttle{Wait: wait, ByAccount: true}
	}
	return &attempt{t: t, ip: client, acct: key}, nil
}

// succeeded settles an attempt that signed in: the address gets its slot back and the failures counted against the
// account are forgotten. The failures of the address stay, so signing in to one's own account does not buy more
// guesses at another.
func (a *attempt) succeeded() {
	a.t.clients.refund(a.ip)
	a.t.accounts.reset(a.acct)
}

// unavailable settles an attempt that could not be checked because the user directory did not answer: the account
// is not blamed. The address stays counted, so an anonymous visitor cannot make the directory work without limit.
func (a *attempt) unavailable() { a.t.accounts.refund(a.acct) }

// accountKey compares accounts the way the directory finds one: without case and, for an e-mail address, by the
// part before the @.
func accountKey(login string) string {
	login = strings.ToLower(strings.TrimSpace(login))
	if i := strings.Index(login, "@"); i > 0 {
		login = login[:i]
	}
	return login
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
