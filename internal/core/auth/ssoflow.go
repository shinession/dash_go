package auth

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

// The Windows sign-in works like this (the flow of the TimeVault project): the page asks /auth/sso/start for a token
// that this server makes up and remembers, opens the sign-on page of the sign-on service for it in a window of its own
// and then asks /auth/sso/poll for the outcome. Only a token this server issued is accepted, so nobody can make the
// service read an arbitrary result, and a result can be used once.
const (
	// ssoFlowTTL is how long a sign-in has to be completed, as long as the sign-on service keeps its result.
	ssoFlowTTL = 5 * time.Minute
	// ssoMaxFlows and ssoMaxFlowsPerIP bound the memory an anonymous visitor can make the server hold.
	ssoMaxFlows      = 2000
	ssoMaxFlowsPerIP = 5
	// ssoPollGap is the least time between two looks at the result of one flow.
	ssoPollGap = time.Second
	// ssoTokenBytes random bytes make a token of 24 characters of a-z and 2-7, which the sign-on service accepts.
	ssoTokenBytes = 15
)

var errSSOBusy = errors.New("auth: too many single sign-ons are waiting")

// ssoFlow is a sign-in that was started on this server and waits for the sign-on service.
type ssoFlow struct {
	ip      string // the client address that started it
	next    string // where the browser goes when the sign-in succeeds
	created time.Time
	polled  time.Time
}

type ssoFlows struct {
	mu    sync.Mutex
	flows map[string]*ssoFlow
}

func (f *ssoFlows) start(ip, next string, now time.Time) (string, error) {
	var raw [ssoTokenBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:]))

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

// PendingSignIns is the number of Windows sign-ins that were started and are not done: somebody is waiting for the
// sign-on window. They are only in the memory of this process, so a restart would lose them; the automatic update
// waits for them.
func (s *Service) PendingSignIns() int { return s.flows.live(s.set.Now()) }

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
	token, err := s.flows.start(s.ClientIP(r), s.SafeNext(req.Next), s.set.Now())
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
	flow, ok := s.flows.lookup(token, now)
	if !ok {
		reply("expired")
		return
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
		s.flows.drop(token)
		refuseAddress("browser_address_changed", "single sign-on refused: the address of the browser changed during the sign-in", "started_from", flow.ip)
		return
	}
	if !s.flows.allowPoll(token, now) {
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
		s.flows.drop(token)
		s.set.Log.Warn("single sign-on refused: the result cannot be read", "method", "sso", "reason", "result_unreadable", "ip", ip)
		reply("denied")
		return
	}
	if res.Status != "ok" || res.ECode == "" {
		s.flows.drop(token)
		s.set.Log.Warn("single sign-on refused: the browser sent no Windows user", "method", "sso", "reason", "no_windows_user", "status", res.Status, "ip", ip)
		reply("no_user")
		return
	}
	if s.set.SSORequireSameIP && !s.sameBrowser(res.IP, flow.ip) {
		s.flows.drop(token)
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
		s.flows.drop(token)
		s.set.Log.Warn("single sign-on refused: no active account", "method", "sso", "reason", "no_active_account", "employee_code", clip(res.ECode, 64), "windows_user", clip(res.WindowsUser, 64), "ip", ip)
		reply("denied")
		return
	}
	// From here the result is used up, whatever happens next.
	s.flows.drop(token)
	if err := s.startSession(w, r, id); err != nil {
		s.set.Log.Error("session signing failed", "error", err)
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
