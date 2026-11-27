package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"dash_go/internal/core/i18n"
)

const (
	maxAccountRunes  = 128
	maxPasswordRunes = 256
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// apiError answers with the message of a catalog key in the language of the request.
func (s *Service) apiError(w http.ResponseWriter, r *http.Request, status int, code, key string) {
	writeJSON(w, status, map[string]string{"error": i18n.T(r.Context(), key, nil), "code": code})
}

// guard is the check of the sign-in endpoints, which have no session to carry a token: the request must carry the
// header a browser only adds to a request of the page's own script (a cross-site form post cannot) and must not come
// from another site.
func (s *Service) guard(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("X-Requested-With") != "dash_go" || !sameOrigin(r) {
		s.apiError(w, r, http.StatusForbidden, "forbidden", "login.csrf")
		return false
	}
	return true
}

// sameOrigin rejects a request that the browser marks as cross-site or whose Origin names another host. The host is
// compared without the port, the way the browser decides what is the same site.
func sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Hostname(), hostname(r.Host))
}

func hostname(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return strings.Trim(hostport, "[]")
}

// acctRef names an account in the log without writing what was typed: a person may type a password into the
// account field by mistake. It is enough to see that the same account fails again and again.
func acctRef(login string) string {
	sum := sha256.Sum256([]byte(accountKey(login)))
	return hex.EncodeToString(sum[:4])
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Next     string `json:"next"`
}

// handleLogin signs in with an account and a password, which the Python backend checks.
func (s *Service) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.PasswordEnabled() {
		http.NotFound(w, r)
		return
	}
	if !s.guard(w, r) {
		return
	}
	var req loginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		s.apiError(w, r, http.StatusBadRequest, "bad_request", "err.bad_request")
		return
	}
	user := strings.TrimSpace(req.Username)
	if user == "" || req.Password == "" || utf8.RuneCountInString(user) > maxAccountRunes || utf8.RuneCountInString(req.Password) > maxPasswordRunes {
		s.apiError(w, r, http.StatusBadRequest, "bad_request", "err.bad_request")
		return
	}

	ip := s.ClientIP(r)
	att, throttle, err := s.limits.Begin(r.Context(), ip, user)
	if err != nil {
		s.set.Log.Warn("login failed: rate limit store unavailable", "method", "password", "ip", ip, "error", err)
		s.apiError(w, r, http.StatusServiceUnavailable, "unavailable", "login.unavailable")
		return
	}
	if throttle != nil {
		reason := "throttled"
		if throttle.ByAccount {
			reason = "account_throttled"
		}
		s.set.Log.Warn("login refused", "method", "password", "reason", reason, "ip", ip, "acct", acctRef(user), "retry_after_s", int(throttle.Wait.Seconds()))
		w.Header().Set("Retry-After", strconv.Itoa(int(throttle.Wait.Seconds())+1))
		s.apiError(w, r, http.StatusTooManyRequests, "throttled", "login.throttled")
		return
	}

	id, err := s.set.Directory.Login(r.Context(), user, req.Password)
	switch {
	case errors.Is(err, ErrUnavailable):
		att.Unavailable(r.Context()) // not the fault of the account
		s.set.Log.Warn("login failed: the user directory cannot be reached", "method", "password", "ip", ip, "error", err)
		s.apiError(w, r, http.StatusServiceUnavailable, "unavailable", "login.unavailable")
		return
	case errors.Is(err, ErrNoEmployeeCode):
		att.Succeeded(r.Context()) // the password was right
		s.set.Log.Warn("login refused: the account has no employee code", "method", "password", "ip", ip, "acct", acctRef(user))
		s.apiError(w, r, http.StatusForbidden, "no_employee_code", "login.no_employee_code")
		return
	case err != nil:
		s.set.Log.Warn("login failed", "method", "password", "reason", "invalid", "ip", ip, "acct", acctRef(user))
		s.apiError(w, r, http.StatusUnauthorized, "invalid", "login.invalid")
		return
	}
	att.Succeeded(r.Context())
	if err := s.startSession(w, r, id); err != nil {
		s.set.Log.Error("session creation failed", "error", err)
		s.apiError(w, r, http.StatusInternalServerError, "internal", "err.internal")
		return
	}
	s.set.Log.Info("login", "method", "password", "ee", id.EE, "ip", ip)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "next": s.SafeNext(req.Next)})
}
