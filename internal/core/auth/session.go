package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	sessionIDBytes   = 32
	sessionKeyPrefix = "dash_go:session:"
)

// SessionStore persists authenticated dash_go sessions. RedisSessionStore shares sessions across multiple
// instances behind a load balancer; MemorySessionStore is the fallback for tests and single-instance development
// without Redis.
type SessionStore interface {
	// Create stores a new session for u with the given ttl and returns its opaque session ID.
	Create(ctx context.Context, u User, ttl time.Duration) (string, error)
	// GetAndRefresh looks up the session ID and, when found and valid, extends its TTL to ttl (sliding expiration).
	// Returns (User{}, false, nil) when the session is absent or expired.
	GetAndRefresh(ctx context.Context, id string, ttl time.Duration) (User, bool, error)
	// Delete removes the session ID and returns the user it belonged to when it existed.
	Delete(ctx context.Context, id string) (User, bool, error)
}

type sessionPayload struct {
	EE   string `json:"ee"`
	Name string `json:"name"`
}

func newSessionID() (string, error) {
	var raw [sessionIDBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// validSessionID checks the shape of an opaque session ID before touching Redis, so malformed values and legacy
// dot-separated signed cookies are rejected locally without a round trip.
func validSessionID(id string) bool {
	if len(id) < 16 || len(id) > 128 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}

// ---- in-memory session store (tests and development without Redis) ----

type memSession struct {
	user User
	exp  time.Time
}

type memSessions struct {
	mu       sync.Mutex
	now      func() time.Time
	sessions map[string]memSession
}

// NewMemorySessions keeps sessions in process memory (single instance development and tests).
func NewMemorySessions(now func() time.Time) SessionStore {
	if now == nil {
		now = time.Now
	}
	return &memSessions{now: now, sessions: map[string]memSession{}}
}

func (m *memSessions) Create(_ context.Context, u User, ttl time.Duration) (string, error) {
	if strings.TrimSpace(u.EE) == "" {
		return "", errors.New("auth: empty employee code")
	}
	if ttl <= 0 {
		return "", errors.New("auth: non-positive session ttl")
	}
	id, err := newSessionID()
	if err != nil {
		return "", err
	}
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sessions) >= 1024 {
		for k, s := range m.sessions {
			if !now.Before(s.exp) {
				delete(m.sessions, k)
			}
		}
	}
	m.sessions[id] = memSession{user: u, exp: now.Add(ttl)}
	return id, nil
}

func (m *memSessions) GetAndRefresh(_ context.Context, id string, ttl time.Duration) (User, bool, error) {
	if !validSessionID(id) || ttl <= 0 {
		return User{}, false, nil
	}
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return User{}, false, nil
	}
	if !now.Before(s.exp) {
		delete(m.sessions, id)
		return User{}, false, nil
	}
	s.exp = now.Add(ttl)
	m.sessions[id] = s
	return s.user, true, nil
}

func (m *memSessions) Delete(_ context.Context, id string) (User, bool, error) {
	if !validSessionID(id) {
		return User{}, false, nil
	}
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return User{}, false, nil
	}
	delete(m.sessions, id)
	if !now.Before(s.exp) {
		return User{}, false, nil
	}
	return s.user, true, nil
}

// ---- Redis session store ----

// sessionTouchScript atomically reads a session and refreshes its sliding TTL (works on Redis 2.6+, without
// requiring Redis 6.2 GETEX).
var sessionTouchScript = redis.NewScript(`
local v = redis.call('GET', KEYS[1])
if not v then
	return nil
end
redis.call('PEXPIRE', KEYS[1], tonumber(ARGV[1]))
return v
`)

// sessionDeleteScript atomically reads and deletes a session on logout (works on Redis 2.6+, without requiring
// Redis 6.2 GETDEL).
var sessionDeleteScript = redis.NewScript(`
local v = redis.call('GET', KEYS[1])
if v then
	redis.call('DEL', KEYS[1])
end
return v
`)

type redisSessions struct {
	rdb *redis.Client
}

// NewRedisSessions stores sessions in Redis below the prefix dash_go:session: so every dash_go instance shares them.
func NewRedisSessions(rdb *redis.Client) SessionStore {
	return &redisSessions{rdb: rdb}
}

func (r *redisSessions) Create(ctx context.Context, u User, ttl time.Duration) (string, error) {
	if strings.TrimSpace(u.EE) == "" {
		return "", errors.New("auth: empty employee code")
	}
	if ttl <= 0 {
		return "", errors.New("auth: non-positive session ttl")
	}
	id, err := newSessionID()
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(sessionPayload{EE: u.EE, Name: u.Name})
	if err != nil {
		return "", err
	}
	if err := r.rdb.Set(ctx, sessionKeyPrefix+id, raw, ttl).Err(); err != nil {
		return "", err
	}
	return id, nil
}

func (r *redisSessions) GetAndRefresh(ctx context.Context, id string, ttl time.Duration) (User, bool, error) {
	if !validSessionID(id) || ttl <= 0 {
		return User{}, false, nil
	}
	raw, err := sessionTouchScript.Run(ctx, r.rdb, []string{sessionKeyPrefix + id}, ttl.Milliseconds()).Text()
	if errors.Is(err, redis.Nil) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, err
	}
	var p sessionPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil || strings.TrimSpace(p.EE) == "" {
		return User{}, false, fmt.Errorf("auth: damaged session record")
	}
	return User{EE: p.EE, Name: p.Name}, true, nil
}

func (r *redisSessions) Delete(ctx context.Context, id string) (User, bool, error) {
	if !validSessionID(id) {
		return User{}, false, nil
	}
	raw, err := sessionDeleteScript.Run(ctx, r.rdb, []string{sessionKeyPrefix + id}).Text()
	if errors.Is(err, redis.Nil) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, err
	}
	var p sessionPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil || strings.TrimSpace(p.EE) == "" {
		return User{}, false, nil
	}
	return User{EE: p.EE, Name: p.Name}, true, nil
}
