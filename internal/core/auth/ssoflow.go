package auth

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// The Windows sign-in works like this (the flow of the TimeVault project): the page asks /auth/sso/start for a token
// that this server makes up and remembers, opens the sign-on page of the sign-on service for it in a window of its own
// and then asks /auth/sso/poll for the outcome. Only a token this server issued is accepted, so nobody can make the
// service read an arbitrary result, and a result can be used once.
const (
	// ssoFlowTTL is how long a sign-in has to be completed, as long as the sign-on service keeps its result.
	ssoFlowTTL = 5 * time.Minute
	// ssoMaxFlows and ssoMaxFlowsPerIP bound the waiting flows an anonymous visitor can make the service hold.
	ssoMaxFlows      = 2000
	ssoMaxFlowsPerIP = 5
	// ssoPollGap is the least time between two looks at the result of one flow.
	ssoPollGap = time.Second
	// ssoTokenBytes random bytes make a token of 24 characters of a-z and 2-7, which the sign-on service accepts.
	ssoTokenBytes = 15
	ssoTokenLen   = 24

	ssoFlowKeyPrefix  = "dash_go:sso:flow:"
	ssoGlobalFlowsKey = "dash_go:sso:flows"
	ssoIPFlowsPrefix  = "dash_go:sso:flows:ip:"
)

var errSSOBusy = errors.New("auth: too many single sign-ons are waiting")

// ssoFlow is a sign-in that was started and waits for the sign-on service.
type ssoFlow struct {
	ip      string // the client address that started it
	next    string // where the browser goes when the sign-in succeeds
	created time.Time
	polled  time.Time
}

// SSOFlowStore keeps pending Windows sign-in flows between /auth/sso/start and /auth/sso/poll.
// RedisSSOFlows shares flows across dash_go instances so polling can land on any server; MemorySSOFlows is the
// fallback for tests and development without Redis.
type SSOFlowStore interface {
	Start(ctx context.Context, ip, next string, now time.Time) (string, error)
	Lookup(ctx context.Context, token string, now time.Time) (ssoFlow, bool, error)
	AllowPoll(ctx context.Context, token string, now time.Time) (bool, error)
	Drop(ctx context.Context, token, ip string) error
	// LocalPending returns the number of waiting flows held only in this process's memory (0 when backed by Redis,
	// because restarts do not lose shared flows).
	LocalPending(now time.Time) int
}

func newSSOToken() (string, error) {
	var raw [ssoTokenBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:])), nil
}

func validSSOToken(token string) bool {
	if len(token) != ssoTokenLen {
		return false
	}
	for i := 0; i < len(token); i++ {
		c := token[i]
		if (c >= 'a' && c <= 'z') || (c >= '2' && c <= '7') {
			continue
		}
		return false
	}
	return true
}

// ---- in-memory flow store (tests and development without Redis) ----

type ssoFlows struct {
	mu    sync.Mutex
	flows map[string]*ssoFlow
}

// NewMemorySSOFlows keeps pending Windows sign-ins in process memory.
func NewMemorySSOFlows() SSOFlowStore { return &ssoFlows{flows: map[string]*ssoFlow{}} }

func (f *ssoFlows) Start(_ context.Context, ip, next string, now time.Time) (string, error) {
	return f.start(ip, next, now)
}

func (f *ssoFlows) Lookup(_ context.Context, token string, now time.Time) (ssoFlow, bool, error) {
	fl, ok := f.lookup(token, now)
	return fl, ok, nil
}

func (f *ssoFlows) AllowPoll(_ context.Context, token string, now time.Time) (bool, error) {
	return f.allowPoll(token, now), nil
}

func (f *ssoFlows) Drop(_ context.Context, token, _ string) error {
	f.drop(token)
	return nil
}

func (f *ssoFlows) LocalPending(now time.Time) int { return f.live(now) }

func (f *ssoFlows) start(ip, next string, now time.Time) (string, error) {
	token, err := newSSOToken()
	if err != nil {
		return "", err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.flows == nil {
		f.flows = make(map[string]*ssoFlow)
	}
	perIP := 0
	for t, fl := range f.flows {
		if now.Sub(fl.created) >= ssoFlowTTL {
			delete(f.flows, t)
		} else if fl.ip == ip {
			perIP++
		}
	}
	if perIP >= ssoMaxFlowsPerIP || len(f.flows) >= ssoMaxFlows {
		return "", errSSOBusy
	}
	f.flows[token] = &ssoFlow{ip: ip, next: next, created: now}
	return token, nil
}

// lookup returns a copy of the flow of a token that is still valid.
func (f *ssoFlows) lookup(token string, now time.Time) (ssoFlow, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fl, ok := f.flows[token]
	if !ok || now.Sub(fl.created) >= ssoFlowTTL {
		delete(f.flows, token)
		return ssoFlow{}, false
	}
	return *fl, true
}

// allowPoll reports whether the result of a flow may be looked at now, and records the look.
func (f *ssoFlows) allowPoll(token string, now time.Time) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	fl, ok := f.flows[token]
	if !ok || now.Sub(fl.polled) < ssoPollGap {
		return false
	}
	fl.polled = now
	return true
}

func (f *ssoFlows) drop(token string) {
	f.mu.Lock()
	delete(f.flows, token)
	f.mu.Unlock()
}

// live is the number of sign-ins that still wait for the sign-on service and have not run out of time.
func (f *ssoFlows) live(now time.Time) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, fl := range f.flows {
		if now.Sub(fl.created) < ssoFlowTTL {
			n++
		}
	}
	return n
}

// ---- Redis flow store (shared across instances) ----

// ssoStartScript prunes expired tokens from the global and per-IP active sets, enforces the flow caps, and stores the
// flow hash atomically (single-field HSET calls for Redis 2.6+ compatibility).
var ssoStartScript = redis.NewScript(`
local now = tonumber(ARGV[4])
local exp = tonumber(ARGV[5])
local ttl = tonumber(ARGV[6])
local maxAll = tonumber(ARGV[7])
local maxIP = tonumber(ARGV[8])
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', now)
redis.call('ZREMRANGEBYSCORE', KEYS[3], '-inf', now)
if redis.call('ZCARD', KEYS[3]) >= maxIP then
	return 0
end
if redis.call('ZCARD', KEYS[2]) >= maxAll then
	return 0
end
redis.call('HSET', KEYS[1], 'ip', ARGV[2])
redis.call('HSET', KEYS[1], 'next', ARGV[3])
redis.call('HSET', KEYS[1], 'created_ms', ARGV[4])
redis.call('HSET', KEYS[1], 'polled_ms', '0')
redis.call('PEXPIRE', KEYS[1], ttl)
redis.call('ZADD', KEYS[2], exp, ARGV[1])
redis.call('PEXPIRE', KEYS[2], ttl)
redis.call('ZADD', KEYS[3], exp, ARGV[1])
redis.call('PEXPIRE', KEYS[3], ttl)
return 1
`)

var ssoAllowPollScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then
	return 0
end
local polled = tonumber(redis.call('HGET', KEYS[1], 'polled_ms') or '0')
local now = tonumber(ARGV[1])
local gap = tonumber(ARGV[2])
if polled > 0 and (now - polled) < gap then
	return 0
end
redis.call('HSET', KEYS[1], 'polled_ms', ARGV[1])
return 1
`)

var ssoDropScript = redis.NewScript(`
redis.call('DEL', KEYS[1])
redis.call('ZREM', KEYS[2], ARGV[1])
if KEYS[3] then
	redis.call('ZREM', KEYS[3], ARGV[1])
end
return 1
`)

type redisSSOFlows struct {
	rdb *redis.Client
}

// NewRedisSSOFlows stores pending Windows sign-in flows in Redis so /auth/sso/start and /auth/sso/poll can be served
// by different dash_go instances.
func NewRedisSSOFlows(rdb *redis.Client) SSOFlowStore {
	return &redisSSOFlows{rdb: rdb}
}

func (r *redisSSOFlows) Start(ctx context.Context, ip, next string, now time.Time) (string, error) {
	token, err := newSSOToken()
	if err != nil {
		return "", err
	}
	nowMs := now.UnixMilli()
	ttlMs := ssoFlowTTL.Milliseconds()
	expMs := nowMs + ttlMs
	keys := []string{ssoFlowKeyPrefix + token, ssoGlobalFlowsKey, ssoIPFlowsPrefix + ip}
	ok, err := ssoStartScript.Run(ctx, r.rdb, keys, token, ip, next, nowMs, expMs, ttlMs, ssoMaxFlows, ssoMaxFlowsPerIP).Int()
	if err != nil {
		return "", err
	}
	if ok == 0 {
		return "", errSSOBusy
	}
	return token, nil
}

func (r *redisSSOFlows) Lookup(ctx context.Context, token string, now time.Time) (ssoFlow, bool, error) {
	if !validSSOToken(token) {
		return ssoFlow{}, false, nil
	}
	m, err := r.rdb.HGetAll(ctx, ssoFlowKeyPrefix+token).Result()
	if err != nil {
		return ssoFlow{}, false, err
	}
	if len(m) == 0 {
		return ssoFlow{}, false, nil
	}
	createdMs, err := strconv.ParseInt(m["created_ms"], 10, 64)
	if err != nil {
		return ssoFlow{}, false, nil
	}
	created := time.UnixMilli(createdMs)
	if now.Sub(created) >= ssoFlowTTL {
		_ = r.Drop(ctx, token, m["ip"])
		return ssoFlow{}, false, nil
	}
	var polled time.Time
	if polledMs, err := strconv.ParseInt(m["polled_ms"], 10, 64); err == nil && polledMs > 0 {
		polled = time.UnixMilli(polledMs)
	}
	return ssoFlow{ip: m["ip"], next: m["next"], created: created, polled: polled}, true, nil
}

func (r *redisSSOFlows) AllowPoll(ctx context.Context, token string, now time.Time) (bool, error) {
	if !validSSOToken(token) {
		return false, nil
	}
	ok, err := ssoAllowPollScript.Run(ctx, r.rdb, []string{ssoFlowKeyPrefix + token}, now.UnixMilli(), ssoPollGap.Milliseconds()).Int()
	if err != nil {
		return false, err
	}
	return ok == 1, nil
}

func (r *redisSSOFlows) Drop(ctx context.Context, token, ip string) error {
	if !validSSOToken(token) {
		return nil
	}
	keys := []string{ssoFlowKeyPrefix + token, ssoGlobalFlowsKey}
	if ip != "" {
		keys = append(keys, ssoIPFlowsPrefix+ip)
	}
	return ssoDropScript.Run(ctx, r.rdb, keys, token).Err()
}

func (r *redisSSOFlows) LocalPending(_ time.Time) int { return 0 }

// PendingSignIns is the number of Windows sign-ins held only in this process's memory. When flows are stored in
// Redis this is always 0, because restarts do not lose shared flows and the automatic update does not need to wait.
func (s *Service) PendingSignIns() int { return s.flows.LocalPending(s.set.Now()) }

// handleSSOStart issues the token of a new Windows sign-in.
func (s *Service) handleSSOStart(w http.ResponseWriter, r *http.Request) {
	if !s.SSOEnabled() {
		http.NotFound(w, r)
		return
	}
	if !s.guard(w, r) {
		return
	}
	var req struct {
		Next string `json:"next"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req) // no body is fine: next is optional
	token, err := s.flows.Start(r.Context(), s.ClientIP(r), s.SafeNext(req.Next), s.set.Now())
	switch {
	case errors.Is(err, errSSOBusy):
		s.apiError(w, r, http.StatusTooManyRequests, "busy", "login.sso_busy")
		return
	case err != nil:
		s.set.Log.Error("start a single sign-on", "error", err)
		s.apiError(w, r, http.StatusInternalServerError, "internal", "err.internal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      token,
		"url":        s.set.SSO.LoginURL(token),
		"expires_in": int(ssoFlowTTL / time.Second),
	})
}

// handleSSOPoll reports how a Windows sign-in went. The answer is always 200 with a status: pending (ask again), ok
// (the session cookie is set and next is where to go), or a final failure: expired, mismatch, no_user, denied,
// unavailable.
func (s *Service) handleSSOPoll(w http.ResponseWriter, r *http.Request) {
	if !s.SSOEnabled() {
		http.NotFound(w, r)
		return
	}
	if !s.guard(w, r) {
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		s.apiError(w, r, http.StatusBadRequest, "bad_request", "err.bad_request")
		return
	}
	reply := func(status string, kv ...any) {
		out := map[string]any{"status": status}
		for i := 0; i+1 < len(kv); i += 2 {
			out[kv[i].(string)] = kv[i+1]
		}
		writeJSON(w, http.StatusOK, out)
	}

	token := req.Token
	now, ip := s.set.Now(), s.ClientIP(r)
	flow, ok, err := s.flows.Lookup(r.Context(), token, now)
	if err != nil {
		s.set.Log.Warn("single sign-on failed: flow store unavailable", "error", err)
		reply("unavailable")
		return
	}
	if !ok {
		reply("expired")
		return
	}
	dropFlow := func() {
		if derr := s.flows.Drop(r.Context(), token, flow.ip); derr != nil {
			s.set.Log.Warn("single sign-on flow drop failed", "error", derr)
		}
	}
	// refuseAddress ends a sign-in whose addresses do not match. The log says which addresses were compared and how
	// this request reached the server, which tells a proxy that hides the browser from a proxy that is not believed.
	refuseAddress := func(reason, message string, extra ...any) {
		attrs := append([]any{"method", "sso", "reason", reason}, extra...)
		attrs = append(attrs, addressDetail(r, s.set.TrustedProxies)...)
		attrs = append(attrs, "hint", addressHint)
		s.set.Log.Warn(message, attrs...)
		reply("mismatch")
	}
	if s.set.SSORequireSameIP && ip != flow.ip && !sameAddress(ip, flow.ip) {
		dropFlow()
		refuseAddress("browser_address_changed", "single sign-on refused: the address of the browser changed during the sign-in", "started_from", flow.ip)
		return
	}
	allowed, err := s.flows.AllowPoll(r.Context(), token, now)
	if err != nil {
		s.set.Log.Warn("single sign-on failed: flow store unavailable", "error", err)
		reply("unavailable")
		return
	}
	if !allowed {
		reply("pending")
		return
	}

	res, err := s.set.SSO.Result(r.Context(), token)
	switch {
	case errors.Is(err, ErrSSOPending):
		reply("pending")
		return
	case errors.Is(err, ErrUnavailable):
		s.set.Log.Warn("single sign-on failed: the sign-on service cannot be reached", "error", err)
		reply("unavailable")
		return
	case err != nil:
		dropFlow()
		s.set.Log.Warn("single sign-on refused: the result cannot be read", "method", "sso", "reason", "result_unreadable", "ip", ip)
		reply("denied")
		return
	}
	if res.Status != "ok" || res.ECode == "" {
		dropFlow()
		s.set.Log.Warn("single sign-on refused: the browser sent no Windows user", "method", "sso", "reason", "no_windows_user", "status", res.Status, "ip", ip)
		reply("no_user")
		return
	}
	if s.set.SSORequireSameIP && !s.sameBrowser(res.IP, flow.ip) {
		dropFlow()
		refuseAddress("address_mismatch", "single sign-on refused: the sign-on service saw another address for the browser than dash_go did",
			"sso_address", clip(res.IP, 64), "started_from", flow.ip, "windows_user", clip(res.WindowsUser, 64))
		return
	}
	id, err := s.set.Directory.Identify(r.Context(), res.ECode)
	switch {
	case errors.Is(err, ErrUnavailable):
		// The result is still there, so the next look tries again.
		s.set.Log.Warn("single sign-on failed: the user directory cannot be reached", "error", err)
		reply("unavailable")
		return
	case err != nil:
		dropFlow()
		s.set.Log.Warn("single sign-on refused: no active account", "method", "sso", "reason", "no_active_account", "employee_code", clip(res.ECode, 64), "windows_user", clip(res.WindowsUser, 64), "ip", ip)
		reply("denied")
		return
	}
	// From here the result is used up, whatever happens next.
	dropFlow()
	if err := s.startSession(w, r, id); err != nil {
		s.set.Log.Error("session creation failed", "error", err)
		s.apiError(w, r, http.StatusInternalServerError, "internal", "err.internal")
		return
	}
	s.set.Log.Info("login", "method", "sso", "ee", id.EE, "windows_user", clip(res.WindowsUser, 64), "ip", ip)
	reply("ok", "next", flow.next)
}

// sameBrowser reports whether the address the sign-on service recorded for the browser is the address dash_go took
// for it when the sign-in started. They differ for a browser on the computer that runs dash_go, opened as localhost:
// dash_go sees a loopback address, the sign-on service the address of this computer. That is one browser on one
// computer, so an address of this computer matches a loopback one. A browser on another computer never comes from a
// loopback address (the left side of X-Forwarded-For is not believed), so no other sign-in is relaxed.
func (s *Service) sameBrowser(ssoAddr, startedFrom string) bool {
	if sameAddress(ssoAddr, startedFrom) {
		return true
	}
	from, ok := parseAddr(startedFrom)
	if !ok || !from.IsLoopback() {
		return false
	}
	seen, ok := parseAddr(ssoAddr)
	if !ok {
		return false
	}
	local := s.set.LocalAddrs
	if local == nil {
		local = ownAddresses
	}
	for _, a := range local() {
		if a.WithZone("").Unmap() == seen {
			return true
		}
	}
	return false
}

// addressHint is logged with a sign-in that was refused because two addresses did not match.
const addressHint = "started_from is the address dash_go took for the browser. If it is the address of a server, a proxy in front " +
	"of dash_go hides the browser: let the nginx next to dash_go trust it (set_real_ip_from, real_ip_header X-Forwarded-For) " +
	"or list it in trusted_proxies. If trusted_proxy is false and forwarded is set, trusted_proxies does not list the proxy that " +
	"connects. If it is 127.0.0.1 or ::1, the page was opened from this computer and sso_address is not one of its addresses " +
	"(docs/shell_and_login.md)."
