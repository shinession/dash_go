package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
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
	DashHome   string
	Now        func() time.Time // tests only
}

// Service handles the sso hand-over, the session cookie and the request guard.
type Service struct {
	signer *Signer
	nonces NonceStore
	set    Settings
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
	return &Service{signer: signer, nonces: nonces, set: set}
}

// Mount registers {base}/auth/sso and {base}/auth/logout.
func (s *Service) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET "+s.set.BasePath+"/auth/sso", s.handleSSO)
	mux.HandleFunc("POST "+s.set.BasePath+"/auth/logout", s.handleLogout)
}

func (s *Service) handleSSO(w http.ResponseWriter, r *http.Request) {
	now := s.set.Now()
	claims, err := s.signer.Parse(r.URL.Query().Get("t"), KindSSO, now)
	if err != nil {
		msg := "The login link is not valid."
		if errors.Is(err, ErrExpired) {
			msg = "The login link has expired."
		}
		s.set.Log.Warn("sso rejected", "reason", err.Error())
		s.fail(w, http.StatusUnauthorized, msg)
		return
	}
	if claims.N == "" {
		s.set.Log.Warn("sso rejected", "reason", "missing nonce")
		s.fail(w, http.StatusUnauthorized, "The login link is not valid.")
		return
	}
	fresh, err := s.nonces.Consume(r.Context(), claims.N, 10*time.Minute)
	if err != nil {
		s.set.Log.Error("sso nonce store failed", "error", err)
		s.fail(w, http.StatusServiceUnavailable, "The login service is temporarily unavailable.")
		return
	}
	if !fresh {
		s.set.Log.Warn("sso rejected", "reason", "link already used", "ee", claims.EE)
		s.fail(w, http.StatusUnauthorized, "The login link was already used.")
		return
	}
	token, err := s.signer.Sign(Claims{K: KindSession, EE: claims.EE, Name: claims.Name, Iat: now.Unix(), Exp: now.Add(s.set.SessionTTL).Unix()})
	if err != nil {
		s.set.Log.Error("session signing failed", "error", err)
		s.fail(w, http.StatusInternalServerError, "Internal error.")
		return
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
	s.set.Log.Info("login", "ee", claims.EE)
	http.Redirect(w, r, s.safeNext(r.URL.Query().Get("next")), http.StatusSeeOther)
}

func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: s.set.CookieName, Value: "", Path: s.cookiePath(), MaxAge: -1, HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
}

// Require rejects requests without a valid session and stores the user in the request context. Unsafe methods
// must also carry the X-Requested-With header (browsers do not add it to cross-site form posts) and must not be
// marked cross-site by the browser.
func (s *Service) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(s.set.CookieName)
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, "login required")
			return
		}
		claims, err := s.signer.Parse(cookie.Value, KindSession, s.set.Now())
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, "login required")
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if r.Header.Get("X-Requested-With") != "dash_go" || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				writeJSONError(w, http.StatusForbidden, "forbidden")
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), User{EE: claims.EE, Name: claims.Name})))
	})
}

func (s *Service) cookiePath() string {
	if s.set.BasePath == "" {
		return "/"
	}
	return s.set.BasePath + "/"
}

// safeNext only accepts local paths below the base path, so the sso link cannot be used as an open redirect.
func (s *Service) safeNext(next string) string {
	def := s.set.BasePath + "/"
	if next == "" || !strings.HasPrefix(next, def) || strings.Contains(next, "//") || strings.Contains(next, "..") || strings.Contains(next, "\\") || strings.ContainsAny(next, "\r\n") {
		return def
	}
	if u, err := url.Parse(next); err != nil || u.Host != "" || u.Scheme != "" {
		return def
	}
	return next
}

func (s *Service) fail(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	home := s.set.DashHome
	if home == "" {
		home = "/"
	}
	_, _ = w.Write([]byte("<!doctype html><meta charset=utf-8><title>Login</title>" +
		"<body style=\"font-family:sans-serif;background:#111;color:#eee;padding:2rem\"><h2>" + htmlEscape(msg) + "</h2>" +
		"<p>Open the page again from the Dash application: MQPD &gt; Head Count &gt; Scan Assign.</p>" +
		"<p><a style=\"color:#7cc4ff\" href=\"" + htmlEscape(home) + "\">Go to Dash</a></p></body>"))
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

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":"` + msg + `"}`))
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&#39;").Replace(s)
}
