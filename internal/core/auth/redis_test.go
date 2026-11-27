package auth

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"dash_go/internal/core/i18n"
)

// newRedisCluster simulates two dash_go instances (s2 and s3) behind a load balancer sharing one Redis.
func newRedisCluster(t *testing.T) (s2, s3 *rig, mr *miniredis.Miniredis, dir *fakeDirectory, sso *fakeSSO) {
	t.Helper()
	mr = miniredis.RunT(t)
	rdb2 := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	rdb3 := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() {
		_ = rdb2.Close()
		_ = rdb3.Close()
	})
	dir = newFakeDirectory()
	sso = &fakeSSO{}
	sharedNow := time.Unix(1700000000, 0)
	mk := func(rdb *redis.Client) *rig {
		r := &rig{t: t, dir: dir, sso: sso, logs: &bytes.Buffer{}, now: sharedNow}
		set := Settings{
			BasePath: "/dash_go",
			Now:      r.clock,
			Log:      slog.New(slog.NewTextHandler(r.logs, nil)),
			Directory: dir, PasswordLogin: true, SSO: sso, SSORequireSameIP: true,
			TrustedProxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
			Sessions:       NewRedisSessions(rdb),
			SSOFlows:       NewRedisSSOFlows(rdb),
			Throttles:      NewRedisThrottles(rdb),
		}
		r.svc = NewService(mustSigner(t), NewRedisNonces(rdb), set)
		mux := http.NewServeMux()
		r.svc.Mount(mux)
		mux.Handle("GET /dash_go/hc_scan/api/me", r.svc.Require(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			u, _ := UserFrom(req.Context())
			_, _ = w.Write([]byte(u.EE + "|" + u.Name))
		})))
		r.mux = i18n.Middleware(mux)
		return r
	}
	return mk(rdb2), mk(rdb3), mr, dir, sso
}

func TestRedisSessionsSharedAcrossInstancesWithSliding24hTTL(t *testing.T) {
	s2, s3, mr, _, _ := newRedisCluster(t)

	// Login on s2
	rec := s2.post("/auth/login", map[string]string{"username": "AB12C34", "password": "s3cret-pw"})
	if rec.Code != http.StatusOK {
		t.Fatalf("login on s2: %d %s", rec.Code, rec.Body.String())
	}
	c := sessionCookie(rec)
	if c == nil || c.MaxAge != 24*3600 {
		t.Fatalf("expected 24h session cookie, got %+v", c)
	}
	key := sessionKeyPrefix + c.Value
	if !mr.Exists(key) {
		t.Fatalf("session key %q not found in Redis", key)
	}
	if ttl := mr.TTL(key); ttl <= 23*time.Hour || ttl > 24*time.Hour {
		t.Fatalf("initial Redis TTL = %v, want ~24h", ttl)
	}

	// Next request lands on s3 -> valid immediately
	if got := s3.me(c); got != "200 AB12C34|Test User" {
		t.Fatalf("s3 must recognize s2 session: %q", got)
	}

	// Advance 18 hours: still valid, and hitting s3 slides BOTH Redis TTL and browser cookie back to 24h
	mr.FastForward(18 * time.Hour)
	if ttl := mr.TTL(key); ttl > 7*time.Hour {
		t.Fatalf("expected ~6h TTL left before activity, got %v", ttl)
	}
	req := httptest.NewRequest(http.MethodGet, "/dash_go/hc_scan/api/me", nil)
	req.AddCookie(c)
	resp := httptest.NewRecorder()
	s3.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK || resp.Body.String() != "AB12C34|Test User" {
		t.Fatalf("active request on s3 at 18h: %d %q", resp.Code, resp.Body.String())
	}
	if refreshed := sessionCookie(resp); refreshed == nil || refreshed.Value != c.Value || refreshed.MaxAge != 24*3600 {
		t.Fatalf("browser cookie must refresh MaxAge to 24h on activity, got %+v", refreshed)
	}
	if ttl := mr.TTL(key); ttl <= 23*time.Hour {
		t.Fatalf("Redis TTL must slide back to ~24h on activity, got %v", ttl)
	}

	// Advance another 18 hours (36h total since login, past original 24h!) -> still alive on s2
	mr.FastForward(18 * time.Hour)
	if got := s2.me(c); got != "200 AB12C34|Test User" {
		t.Fatalf("session must stay alive past original 24h when active: %q", got)
	}

	// Idle for 25 hours -> expires everywhere
	mr.FastForward(25 * time.Hour)
	if got := s3.me(c); !strings.HasPrefix(got, "401 ") {
		t.Fatalf("idle session after 25h must expire, got %q", got)
	}
}

func TestRedisSessionLogoutRevokesAcrossAllInstances(t *testing.T) {
	s2, s3, mr, _, _ := newRedisCluster(t)
	rec := s2.post("/auth/login", map[string]string{"username": "AB12C34", "password": "s3cret-pw"})
	c := sessionCookie(rec)
	if c == nil {
		t.Fatal("missing session cookie")
	}

	// Logout lands on s3
	req := httptest.NewRequest(http.MethodPost, "/dash_go/auth/logout", nil)
	req.Header.Set("X-Requested-With", "dash_go")
	req.AddCookie(c)
	out := httptest.NewRecorder()
	s3.mux.ServeHTTP(out, req)
	if out.Code != http.StatusNoContent {
		t.Fatalf("logout on s3: %d", out.Code)
	}
	if mr.Exists(sessionKeyPrefix + c.Value) {
		t.Fatal("session key must be removed from Redis on logout")
	}
	// Replaying the copied cookie must fail immediately on BOTH s2 and s3
	if got := s2.me(c); !strings.HasPrefix(got, "401 ") {
		t.Fatalf("s2 must reject logged-out session: %q", got)
	}
	if got := s3.me(c); !strings.HasPrefix(got, "401 ") {
		t.Fatalf("s3 must reject logged-out session: %q", got)
	}
}

func TestRedisSessionUnavailableReturnsUnauthenticatedAndLogsError(t *testing.T) {
	s2, s3, mr, _, _ := newRedisCluster(t)
	rec := s2.post("/auth/login", map[string]string{"username": "AB12C34", "password": "s3cret-pw"})
	c := sessionCookie(rec)
	if c == nil {
		t.Fatal("missing session cookie")
	}

	// Shut down Redis: Session/Require must treat as unauthenticated (401) and log error without leaking session ID
	mr.Close()
	if got := s3.me(c); !strings.HasPrefix(got, "401 ") {
		t.Fatalf("Redis outage must return 401 unauthenticated, got %q", got)
	}
	logs := s3.logs.String()
	if !strings.Contains(logs, "session store lookup failed") {
		t.Errorf("Redis outage must be logged:\n%s", logs)
	}
	if strings.Contains(logs, c.Value) {
		t.Errorf("session ID must never appear in logs:\n%s", logs)
	}
}

func TestRedisDashHandoverLinkWorksAcrossInstancesAndIsOneTime(t *testing.T) {
	s2, s3, _, _, _ := newRedisCluster(t)
	tok := mustSign(t, s2.svc.signer, Claims{
		K: KindSSO, EE: "AB12C34", Name: "Test User",
		Iat: s2.now.Unix(), Exp: s2.now.Add(5 * time.Minute).Unix(), N: NewNonce(),
	})

	// Handover hits s2
	req := httptest.NewRequest(http.MethodGet, "/dash_go/auth/sso?t="+tok+"&next=/dash_go/hc_scan/", nil)
	rec := httptest.NewRecorder()
	s2.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("handover on s2: %d", rec.Code)
	}
	c := sessionCookie(rec)
	if c == nil || s3.me(c) != "200 AB12C34|Test User" {
		t.Fatalf("session issued by handover on s2 must work on s3: %+v", c)
	}

	// Replaying the one-time link on s3 is refused via shared Redis nonce
	rec2 := httptest.NewRecorder()
	s3.mux.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/dash_go/auth/sso?t="+tok, nil))
	if rec2.Header().Get("Location") != "/dash_go/login?next=%2Fdash_go%2F&error=link_used" {
		t.Fatalf("replay on s3 must be refused as link_used: %q", rec2.Header().Get("Location"))
	}
}

func TestRedisSSOFlowWorksAcrossInstances(t *testing.T) {
	s2, s3, mr, _, sso := newRedisCluster(t)

	// Start Windows SSO on s2
	token, rec := s2.startSSO("198.51.100.7", "/dash_go/hc_scan/")
	if rec.Code != http.StatusOK {
		t.Fatalf("start on s2: %d %s", rec.Code, rec.Body.String())
	}
	// Shared Redis flows do not hold back auto-update on local restart
	if n := s2.svc.PendingSignIns(); n != 0 {
		t.Fatalf("PendingSignIns with Redis store = %d, want 0", n)
	}

	// First poll lands on s3 while pending
	status, _, poll1 := s3.pollSSO(token, "198.51.100.7")
	if status != "pending" || sessionCookie(poll1) != nil {
		t.Fatalf("first poll on s3: %s", status)
	}

	// Poll immediately on s2 within 1s -> rate-limited across instances without asking SSO backend again
	asked := sso.asked
	status, _, _ = s2.pollSSO(token, "198.51.100.7")
	if status != "pending" || sso.asked != asked {
		t.Fatalf("cross-instance poll gap must block rapid polls: status=%s asked=%d (was %d)", status, sso.asked, asked)
	}

	// Advance clock by 2s, SSO completes, poll lands on s3 -> succeeds and issues Redis session valid on s2!
	s2.now = s2.now.Add(2 * time.Second)
	s3.now = s3.now.Add(2 * time.Second)
	sso.set(token, okResult("198.51.100.7"))
	status, got, poll2 := s3.pollSSO(token, "198.51.100.7")
	if status != "ok" || got["next"] != "/dash_go/hc_scan/" {
		t.Fatalf("completion poll on s3: %s %v", status, got)
	}
	c := sessionCookie(poll2)
	if c == nil || s2.me(c) != "200 AB12C34|Test User" {
		t.Fatalf("session issued by SSO on s3 must work on s2: %+v", c)
	}

	// Re-polling consumed token on s2 -> expired
	s2.now = s2.now.Add(2 * time.Second)
	if status, _, _ := s2.pollSSO(token, "198.51.100.7"); status != "expired" {
		t.Fatalf("re-polling consumed token on s2: %s", status)
	}

	// Per-IP cap (5) is enforced across s2 + s3
	for i := 0; i < 3; i++ {
		if _, rec := s2.startSSO("198.51.100.9", ""); rec.Code != http.StatusOK {
			t.Fatalf("s2 flow %d: %d", i, rec.Code)
		}
	}
	for i := 0; i < 2; i++ {
		if _, rec := s3.startSSO("198.51.100.9", ""); rec.Code != http.StatusOK {
			t.Fatalf("s3 flow %d: %d", i, rec.Code)
		}
	}
	if rec := s2.post("/auth/sso/start", map[string]string{}, fromIP("198.51.100.9")); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("6th flow across s2+s3 must be 429 busy, got %d", rec.Code)
	}

	// Redis outage during poll reports unavailable (not expired)
	mr.Close()
	if status, _, _ := s3.pollSSO(token, "198.51.100.7"); status != "unavailable" {
		t.Fatalf("Redis outage on poll must report unavailable, got %s", status)
	}
}

func TestRedisPasswordThrottlesSharedAcrossInstances(t *testing.T) {
	s2, s3, mr, _, _ := newRedisCluster(t)

	// 3 failed logins on s2 + 2 failed logins on s3 from the same IP -> 6th on s2 is 429
	for i := 0; i < 3; i++ {
		if rec := s2.post("/auth/login", map[string]string{"username": fmt.Sprintf("u%d", i), "password": "bad"}, fromIP("198.51.100.50")); rec.Code != http.StatusUnauthorized {
			t.Fatalf("s2 attempt %d: %d", i, rec.Code)
		}
	}
	for i := 3; i < 5; i++ {
		if rec := s3.post("/auth/login", map[string]string{"username": fmt.Sprintf("u%d", i), "password": "bad"}, fromIP("198.51.100.50")); rec.Code != http.StatusUnauthorized {
			t.Fatalf("s3 attempt %d: %d", i, rec.Code)
		}
	}
	if rec := s2.post("/auth/login", map[string]string{"username": "AB12C34", "password": "s3cret-pw"}, fromIP("198.51.100.50")); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("6th attempt across s2+s3 must be 429, got %d %s", rec.Code, rec.Body.String())
	}

	// Verify typed username is never stored in plain text in Redis keys
	s2.post("/auth/login", map[string]string{"username": "mistyped-secret-pw", "password": "x"}, fromIP("198.51.100.51"))
	for _, k := range mr.Keys() {
		if strings.Contains(k, "mistyped-secret-pw") {
			t.Errorf("typed username leaked in Redis key %q", k)
		}
	}

	// Account limit (10) shared across instances and distinct IPs
	for i := 0; i < maxAccountAttempts; i++ {
		target := s2
		if i%2 == 1 {
			target = s3
		}
		if rec := target.post("/auth/login", map[string]string{"username": "Ab12c34", "password": "wrong"}, fromIP(fmt.Sprintf("203.0.113.%d", i+1))); rec.Code != http.StatusUnauthorized {
			t.Fatalf("acct attempt %d: %d", i, rec.Code)
		}
	}
	if rec := s3.post("/auth/login", map[string]string{"username": "AB12C34", "password": "s3cret-pw"}, fromIP("203.0.113.200")); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("account must be throttled across instances: %d %s", rec.Code, rec.Body.String())
	}

	// Window expiry clears the throttle
	mr.FastForward(loginWindow + time.Second)
	if rec := s3.post("/auth/login", map[string]string{"username": "AB12C34", "password": "s3cret-pw"}, fromIP("203.0.113.200")); rec.Code != http.StatusOK {
		t.Fatalf("login after window must succeed: %d %s", rec.Code, rec.Body.String())
	}

	// Redis outage fails closed with 503 unavailable
	mr.Close()
	if rec := s2.post("/auth/login", map[string]string{"username": "AB12C34", "password": "s3cret-pw"}, fromIP("203.0.113.201")); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("Redis outage on login must return 503, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestRedisSessionRejectsDamagedAndLegacyCookies(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	store := NewRedisSessions(rdb)
	ctx := context.Background()

	// Legacy dot-separated cookie is rejected locally
	if _, ok, err := store.GetAndRefresh(ctx, pythonToken, time.Hour); ok || err != nil {
		t.Fatalf("legacy token: ok=%v err=%v", ok, err)
	}
	// Damaged JSON in Redis is reported as an error
	id, err := store.Create(ctx, User{EE: "AB12C34", Name: "Test"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	mr.Set(sessionKeyPrefix+id, "{broken")
	if _, ok, err := store.GetAndRefresh(ctx, id, time.Hour); ok || err == nil {
		t.Fatalf("damaged record must return error: ok=%v err=%v", ok, err)
	}
}
