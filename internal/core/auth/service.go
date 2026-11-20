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
}

// Service handles the sign-in (the hand-over from Dash, the password and the Windows sign-in), the session cookie
// and the request guard.
type Service struct {
	signer *Signer
	nonces NonceStore
	set    Settings
	limits *throttles
	flows  ssoFlows
}

// NewService returns a Service. Defaults: cookie dash_go_session, 12 hour sessions.
func NewService(signer *Signer, nonces NonceStore, set Settings) *Service {
	if set.CookieName == "" {
		set.CookieName = "dash_go_session"
	}
	if set.SessionTTL <= 0 {
		set.SessionTTL = 12 * time.Hour
	}
	if set.Log == nil {
		set.Log = slog.Default()
	}
	if set.Now == nil {
		set.Now = time.Now
	}
	return &Service{signer: signer, nonces: nonces, set: set, limits: newThrottles(set.Now)}
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
		s.set.Log.Error("session signing failed", "error", err)
		refuse("unavailable")
		return
	}
	s.set.Log.Info("login", "method", "dash_link", "ee", claims.EE)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// startSession sets a fresh session cookie for the person.
func (s *Service) startSession(w http.ResponseWriter, r *http.Request, id Identity) error {
	now := s.set.Now()
	token, err := s.signer.Sign(Claims{K: KindSession, EE: id.EE, Name: id.Name, Iat: now.Unix(), Exp: now.Add(s.set.SessionTTL).Unix()})
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.set.CookieName,
		Value:    token,
		Path:     s.cookiePath(),
		MaxAge:   int(s.set.SessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// handleLogout ends the session of dash_go only: the login of the Dash application in the browser is a different
// thing and stays as it is.
func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) {
		return
	}
	http.SetCookie(w, &http.Cookie{Name: s.set.CookieName, Value: "", Path: s.cookiePath(), MaxAge: -1, HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode})
	if u, ok := s.Session(r); ok {
		s.set.Log.Info("logout", "ee", u.EE)
	}
	w.WriteHeader(http.StatusNoContent)
}

// Session returns the person of the session cookie of the request, if the cookie is valid. It checks nothing else;
// the pages use it to show who is signed in, Require guards the API.
func (s *Service) Session(r *http.Request) (User, bool) {
	cookie, err := r.Cookie(s.set.CookieName)
	if err != nil {
		return User{}, false
	}
	claims, err := s.signer.Parse(cookie.Value, KindSession, s.set.Now())
	if err != nil {
		return User{}, false
	}
	return User{EE: claims.EE, Name: claims.Name}, true
}

// Require rejects requests without a valid session and stores the user in the request context. Unsafe methods
// must also carry the X-Requested-With header (browsers do not add it to cross-site form posts) and must not be
// marked cross-site by the browser.
func (s *Service) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := s.Session(r)
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
