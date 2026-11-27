package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// User is the authenticated person behind a request.
type User struct {
	EE   string
	Name string
}

type ctxKey struct{}

// UserFrom returns the user stored in the request context by Require.
func UserFrom(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(ctxKey{}).(User)
	return u, ok
}

// WithUser returns a context carrying the user (used by tests and Require).
func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

// NonceStore remembers consumed sso nonces so a link works once.
type NonceStore interface {
	// Consume returns false when the nonce was used before.
	Consume(ctx context.Context, nonce string, ttl time.Duration) (bool, error)
}

type memNonces struct {
	mu sync.Mutex
	m  map[string]time.Time
}

// NewMemoryNonces keeps nonces in the process memory (single instance deployments and tests).
func NewMemoryNonces() NonceStore { return &memNonces{m: map[string]time.Time{}} }

func (s *memNonces) Consume(_ context.Context, nonce string, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, exp := range s.m {
		if now.After(exp) {
			delete(s.m, k)
		}
	}
	if _, used := s.m[nonce]; used {
		return false, nil
	}
	s.m[nonce] = now.Add(ttl)
	return true, nil
}

type redisNonces struct{ rdb *redis.Client }

// NewRedisNonces stores nonces in Redis so several instances share them.
func NewRedisNonces(rdb *redis.Client) NonceStore { return &redisNonces{rdb: rdb} }

func (s *redisNonces) Consume(ctx context.Context, nonce string, ttl time.Duration) (bool, error) {
	return s.rdb.SetNX(ctx, "dash_go:sso:nonce:"+nonce, "1", ttl).Result()
}

// Settings configure the Service.
type Settings struct {
	BasePath   string // e.g. /dash_go, empty when mounted at the root
	CookieName string
	SessionTTL time.Duration
	Log        *slog.Logger
	Now        func() time.Time // tests only

	// Directory checks the accounts of the Dash application. Without it neither the password sign-in nor the
	// Windows sign-in works, and the login page only tells people to open the page from Dash.
	Directory Directory
	// PasswordLogin lets the login page ask for an account and a password (needs Directory).
	PasswordLogin bool
	// SSO is the Windows single sign-on service; nil switches that sign-in off (needs Directory).
	SSO SSOSource
	// SSORequireSameIP refuses a Windows sign-in whose result was recorded for another address than the one that
	// started it. It is what stops a sign-in being forced on a victim.
	SSORequireSameIP bool
	// TrustedProxies are the reverse proxies whose X-Forwarded-For header is believed.
	TrustedProxies []netip.Prefix
	// LocalAddrs lists the addresses of the computer that runs dash_go; nil means those of its network interfaces.
	// It is only asked for a Windows sign-in that started from a loopback address (see sameBrowser), and exists so
	// that tests do not depend on the computer they run on.
	LocalAddrs func() []netip.Addr

	// Sessions persists authenticated sessions (default: in-memory; pass NewRedisSessions for multi-instance).
	Sessions SessionStore
	// SSOFlows persists pending Windows sign-ins (default: in-memory; pass NewRedisSSOFlows for multi-instance).
	SSOFlows SSOFlowStore
	// Throttles tracks failed password logins (default: in-memory; pass NewRedisThrottles for multi-instance).
	Throttles ThrottleStore
}

// Service handles the sign-in (the hand-over from Dash, the password and the Windows sign-in), the session store
// and the request guard.
type Service struct {
	signer   *Signer
	nonces   NonceStore
	sessions SessionStore
	set      Settings
	limits   ThrottleStore
	flows    SSOFlowStore
}

// NewService returns a Service. Defaults: cookie dash_go_session, 24 hour sliding sessions, in-memory stores.
func NewService(signer *Signer, nonces NonceStore, set Settings) *Service {
	if set.CookieName == "" {
		set.CookieName = "dash_go_session"
	}
	if set.SessionTTL <= 0 {
		set.SessionTTL = 24 * time.Hour
	}
	if set.Log == nil {
		set.Log = slog.Default()
	}
	if set.Now == nil {
		set.Now = time.Now
	}
	sessions := set.Sessions
	if sessions == nil {
		sessions = NewMemorySessions(set.Now)
	}
	flows := set.SSOFlows
	if flows == nil {
		flows = NewMemorySSOFlows()
	}
	limits := set.Throttles
	if limits == nil {
		limits = NewMemoryThrottles(set.Now)
	}
	return &Service{signer: signer, nonces: nonces, sessions: sessions, set: set, limits: limits, flows: flows}
}

// Mount registers the sign-in routes below {base}/auth.
func (s *Service) Mount(mux *http.ServeMux) {
	base := s.set.BasePath + "/auth"
	mux.HandleFunc("GET "+base+"/sso", s.handleHandover)
	mux.HandleFunc("POST "+base+"/logout", s.handleLogout)
	mux.HandleFunc("POST "+base+"/login", s.handleLogin)
	mux.HandleFunc("POST "+base+"/sso/start", s.handleSSOStart)
	mux.HandleFunc("POST "+base+"/sso/poll", s.handleSSOPoll)
}

// PasswordEnabled reports whether the login page asks for an account and a password.
func (s *Service) PasswordEnabled() bool { return s.set.Directory != nil && s.set.PasswordLogin }

// SSOEnabled reports whether the login page offers the Windows sign-in.
func (s *Service) SSOEnabled() bool { return s.set.Directory != nil && s.set.SSO != nil }

// LoginPath is the address of the login page, with the page to come back to.
func (s *Service) LoginPath(next string) string {
	p := s.set.BasePath + "/login"
	if next != "" {
		p += "?next=" + url.QueryEscape(next)
	}
	return p
}

// ClientIP is the address of the browser (see clientIP).
func (s *Service) ClientIP(r *http.Request) string { return clientIP(r, s.set.TrustedProxies) }

// handleHandover takes the one-time link the Dash application builds (Scan Assign button). A link that does not
// work leads to the login page with the reason, where the person can sign in directly.
func (s *Service) handleHandover(w http.ResponseWriter, r *http.Request) {
	now := s.set.Now()
	next := s.SafeNext(r.URL.Query().Get("next"))
	refuse := func(code string) {
		http.Redirect(w, r, s.LoginPath(next)+"&error="+code, http.StatusSeeOther)
	}
	claims, err := s.signer.Parse(r.URL.Query().Get("t"), KindSSO, now)
	if err != nil {
		code := "link_invalid"
		if err == ErrExpired {
			code = "link_expired"
		}
		s.set.Log.Warn("sso rejected", "reason", err.Error())
		refuse(code)
		return
	}
	if claims.N == "" {
		s.set.Log.Warn("sso rejected", "reason", "missing nonce")
		refuse("link_invalid")
		return
	}
	fresh, err := s.nonces.Consume(r.Context(), claims.N, 10*time.Minute)
	if err != nil {
		s.set.Log.Error("sso nonce store failed", "error", err)
		refuse("unavailable")
		return
	}
	if !fresh {
		s.set.Log.Warn("sso rejected", "reason", "link already used", "ee", claims.EE)
		refuse("link_used")
		return
	}
	if err := s.startSession(w, r, Identity{EE: claims.EE, Name: claims.Name}); err != nil {
		s.set.Log.Error("session creation failed", "error", err)
		refuse("unavailable")
		return
	}
	s.set.Log.Info("login", "method", "dash_link", "ee", claims.EE)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// IssueSession creates a session in the session store and returns its cookie value (used by tests across packages).
func (s *Service) IssueSession(ctx context.Context, u User) (string, error) {
	return s.sessions.Create(ctx, u, s.set.SessionTTL)
}

// startSession stores a fresh session in the session store and sets the session cookie.
func (s *Service) startSession(w http.ResponseWriter, r *http.Request, id Identity) error {
	sid, err := s.sessions.Create(r.Context(), User{EE: id.EE, Name: id.Name}, s.set.SessionTTL)
	if err != nil {
		return err
	}
	s.setSessionCookie(w, r, sid)
	return nil
}

func (s *Service) setSessionCookie(w http.ResponseWriter, r *http.Request, sid string) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.set.CookieName,
		Value:    sid,
		Path:     s.cookiePath(),
		MaxAge:   int(s.set.SessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// handleLogout ends the session of dash_go only: it removes the session from the session store and clears the
// browser cookie; the login of the Dash application in the browser is a different thing and stays as it is.
func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) {
		return
	}
	if cookie, err := r.Cookie(s.set.CookieName); err == nil && cookie.Value != "" {
		u, ok, delErr := s.sessions.Delete(r.Context(), cookie.Value)
		if delErr != nil {
			s.set.Log.Warn("session delete failed on logout", "error", delErr)
		} else if ok {
			s.set.Log.Info("logout", "ee", u.EE)
		}
	}
	http.SetCookie(w, &http.Cookie{Name: s.set.CookieName, Value: "", Path: s.cookiePath(), MaxAge: -1, HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) sessionWithID(r *http.Request) (User, string, bool) {
	cookie, err := r.Cookie(s.set.CookieName)
	if err != nil || cookie.Value == "" {
		return User{}, "", false
	}
	u, ok, err := s.sessions.GetAndRefresh(r.Context(), cookie.Value, s.set.SessionTTL)
	if err != nil {
		s.set.Log.Warn("session store lookup failed", "error", err)
		return User{}, "", false
	}
	if !ok {
		return User{}, "", false
	}
	return u, cookie.Value, true
}

// Session returns the person of the session cookie of the request, refreshing the server-side sliding TTL when
// valid. If the session store cannot be reached, it logs the error and reports unauthenticated.
func (s *Service) Session(r *http.Request) (User, bool) {
	u, _, ok := s.sessionWithID(r)
	return u, ok
}

// Authenticate validates the session, refreshes its sliding TTL in the session store, and refreshes the browser
// cookie's MaxAge so client expiry stays in sync with server-side sliding expiration.
func (s *Service) Authenticate(w http.ResponseWriter, r *http.Request) (User, bool) {
	u, sid, ok := s.sessionWithID(r)
	if !ok {
		return User{}, false
	}
	if w != nil {
		s.setSessionCookie(w, r, sid)
	}
	return u, true
}

// Require rejects requests without a valid session, refreshes the sliding session TTL and cookie, and stores the
// user in the request context. Unsafe methods must also carry the X-Requested-With header (browsers do not add it to
// cross-site form posts) and must not be marked cross-site by the browser.
func (s *Service) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, sid, ok := s.sessionWithID(r)
		if !ok {
			s.apiError(w, r, http.StatusUnauthorized, "login_required", "err.login_required")
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if r.Header.Get("X-Requested-With") != "dash_go" || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				s.apiError(w, r, http.StatusForbidden, "forbidden", "err.forbidden")
				return
			}
		}
		s.setSessionCookie(w, r, sid)
		next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), u)))
	})
}

func (s *Service) cookiePath() string {
	if s.set.BasePath == "" {
		return "/"
	}
	return s.set.BasePath + "/"
}

// SafeNext only accepts local paths below the base path, so a link with a next parameter cannot be used as an open
// redirect. Anything else becomes the start page of the service.
func (s *Service) SafeNext(next string) string {
	def := s.set.BasePath + "/"
	if next == "" || !strings.HasPrefix(next, def) || strings.Contains(next, "//") || strings.Contains(next, "..") || strings.Contains(next, "\\") || strings.ContainsAny(next, "\r\n") {
		return def
	}
	if u, err := url.Parse(next); err != nil || u.Host != "" || u.Scheme != "" {
		return def
	}
	return next
}

// NewNonce returns a random one-time nonce (used by tests; production nonces come from the Dash side).
func NewNonce() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
