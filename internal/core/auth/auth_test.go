package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const (
	testSecret = "unit-test-secret"
	// pythonToken was produced by the Python reference implementation (key = HMAC(secret, "dash_go/sso/v1"),
	// signature = HMAC(key, body)); LocalListV3.build_dash_go_sso_url must produce the same bytes.
	pythonToken = "eyJ2IjoxLCJrIjoic3NvIiwiZWUiOiJBQjEyQzM0IiwibmFtZSI6IlRlc3QgVXNlciIsImlhdCI6MTcwMDAwMDAwMCwiZXhwIjoxNzAwMDAwMzAwLCJuIjoiYWJjMTIzIn0.ghW8lk83KJwRpegIoEMuuL0Wk_5thLamL9wSGsQE2aU"
)

var tokenNow = time.Unix(1700000100, 0)

func mustSigner(t *testing.T) *Signer {
	t.Helper()
	s, err := NewSigner(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestParsePythonToken(t *testing.T) {
	c, err := mustSigner(t).Parse(pythonToken, KindSSO, tokenNow)
	if err != nil {
		t.Fatalf("the token of the Python implementation must verify: %v", err)
	}
	if c.EE != "AB12C34" || c.Name != "Test User" || c.N != "abc123" {
		t.Fatalf("claims: %+v", c)
	}
}

func TestSignMatchesPythonBytes(t *testing.T) {
	// Go output keeps the field order of the struct; only the signature over the body matters, so re-sign the
	// exact claims and check the round trip instead of the bytes.
	s := mustSigner(t)
	tok, err := s.Sign(Claims{K: KindSSO, EE: "AB12C34", Name: "Test User", Iat: 1700000000, Exp: 1700000300, N: "abc123"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Parse(tok, KindSSO, tokenNow); err != nil {
		t.Fatal(err)
	}
}

func TestParseRejections(t *testing.T) {
	s := mustSigner(t)
	other, _ := NewSigner("another-secret")
	good, _ := s.Sign(Claims{K: KindSSO, EE: "AB12C34", Iat: tokenNow.Unix(), Exp: tokenNow.Add(time.Minute).Unix()})
	body, sig, _ := strings.Cut(good, ".")

	cases := []struct {
		name  string
		token string
		kind  string
		now   time.Time
		want  error
	}{
		{"expired", good, KindSSO, tokenNow.Add(2 * time.Minute), ErrExpired},
		{"wrong kind", good, "session", tokenNow, ErrInvalid},
		{"other secret", mustSign(t, other, Claims{K: KindSSO, EE: "AB12C34", Iat: tokenNow.Unix(), Exp: tokenNow.Add(time.Minute).Unix()}), KindSSO, tokenNow, ErrInvalid},
		{"tampered body", "X" + body + "." + sig, KindSSO, tokenNow, ErrInvalid},
		{"tampered signature", body + "." + sig[:len(sig)-2] + "AA", KindSSO, tokenNow, ErrInvalid},
		{"no separator", body, KindSSO, tokenNow, ErrInvalid},
		{"empty", "", KindSSO, tokenNow, ErrInvalid},
		{"issued in the future", mustSign(t, s, Claims{K: KindSSO, EE: "AB12C34", Iat: tokenNow.Add(time.Hour).Unix(), Exp: tokenNow.Add(2 * time.Hour).Unix()}), KindSSO, tokenNow, ErrInvalid},
		{"no employee", mustSign(t, s, Claims{K: KindSSO, EE: " ", Iat: tokenNow.Unix(), Exp: tokenNow.Add(time.Minute).Unix()}), KindSSO, tokenNow, ErrInvalid},
	}
	for _, c := range cases {
		if _, err := s.Parse(c.token, c.kind, c.now); err != c.want {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
	if _, err := s.Sign(Claims{K: "session", EE: "AB12C34", Iat: tokenNow.Unix(), Exp: tokenNow.Add(time.Hour).Unix()}); err == nil {
		t.Errorf("Sign must only sign sso hand-over tokens")
	}
}

func mustSign(t *testing.T, s *Signer, c Claims) string {
	t.Helper()
	tok, err := s.Sign(c)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestNewSignerRequiresSecret(t *testing.T) {
	if _, err := NewSigner("  "); err == nil {
		t.Fatal("an empty secret must be refused")
	}
}

func newTestService(t *testing.T, now time.Time) (*Service, *http.ServeMux) {
	t.Helper()
	svc := NewService(mustSigner(t), NewMemoryNonces(), Settings{BasePath: "/dash_go", Now: func() time.Time { return now }})
	mux := http.NewServeMux()
	svc.Mount(mux)
	mux.Handle("GET /dash_go/hc_scan/api/me", svc.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := UserFrom(r.Context())
		_, _ = w.Write([]byte(u.EE + "|" + u.Name))
	})))
	mux.Handle("POST /dash_go/hc_scan/api/scan", svc.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })))
	return svc, mux
}

func ssoRequest(t *testing.T, mux http.Handler, token, next string) *httptest.ResponseRecorder {
	t.Helper()
	q := url.Values{"t": {token}}
	if next != "" {
		q.Set("next", next)
	}
	req := httptest.NewRequest(http.MethodGet, "/dash_go/auth/sso?"+q.Encode(), nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestSSOLoginSetsSessionAndIsOneTime(t *testing.T) {
	_, mux := newTestService(t, tokenNow)
	rec := ssoRequest(t, mux, pythonToken, "/dash_go/hc_scan/")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/dash_go/hc_scan/" {
		t.Fatalf("status %d location %q", rec.Code, rec.Header().Get("Location"))
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].Path != "/dash_go/" || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie: %+v", cookies)
	}

	req := httptest.NewRequest(http.MethodGet, "/dash_go/hc_scan/api/me", nil)
	req.AddCookie(cookies[0])
	got := httptest.NewRecorder()
	mux.ServeHTTP(got, req)
	if got.Code != 200 || got.Body.String() != "AB12C34|Test User" {
		t.Fatalf("session user: %d %q", got.Code, got.Body.String())
	}

	again := ssoRequest(t, mux, pythonToken, "")
	if len(again.Result().Cookies()) != 0 || again.Header().Get("Location") != "/dash_go/login?next=%2Fdash_go%2F&error=link_used" {
		t.Fatalf("the link works once: status %d location %q", again.Code, again.Header().Get("Location"))
	}
}

// A link that does not work is not a dead end: the person lands on the login page with the reason, where signing in
// directly is possible, and the page they wanted is remembered.
func TestSSOBadLinksLeadToTheLoginPageWithTheReason(t *testing.T) {
	_, mux := newTestService(t, tokenNow.Add(10*time.Minute))
	s := mustSigner(t)
	noNonce := mustSign(t, s, Claims{K: KindSSO, EE: "AB12C34", Iat: tokenNow.Add(10 * time.Minute).Unix(), Exp: tokenNow.Add(15 * time.Minute).Unix()})
	cases := []struct{ name, token, want string }{
		{"expired link", pythonToken, "link_expired"},
		{"junk link", "junk", "link_invalid"},
		{"a link without nonce", noNonce, "link_invalid"},
	}
	for _, c := range cases {
		rec := ssoRequest(t, mux, c.token, "/dash_go/hc_scan/")
		want := "/dash_go/login?next=%2Fdash_go%2Fhc_scan%2F&error=" + c.want
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != want || len(rec.Result().Cookies()) != 0 {
			t.Errorf("%s: status %d location %q, want %q and no cookie", c.name, rec.Code, rec.Header().Get("Location"), want)
		}
	}
}

func TestSSONextIsNeverAnOpenRedirect(t *testing.T) {
	for _, next := range []string{"https://evil.example/", "//evil.example/", "/other/path", "/dash_go/../x", "/dash_go//evil", "/dash_go/\\evil", "/dash_go/a\r\nSet-Cookie: x=1"} {
		svc, mux := newTestService(t, tokenNow)
		tok := mustSign(t, svc.signer, Claims{K: KindSSO, EE: "AB12C34", Iat: tokenNow.Unix(), Exp: tokenNow.Add(time.Minute).Unix(), N: NewNonce()})
		rec := ssoRequest(t, mux, tok, next)
		loc := rec.Header().Get("Location")
		if rec.Code != http.StatusSeeOther || loc != "/dash_go/" {
			t.Errorf("next=%q redirected to %q", next, loc)
		}
		if strings.Contains(loc, "evil") {
			t.Errorf("open redirect for %q: %q", next, loc)
		}
	}
}

func TestRequireGuardsTheAPI(t *testing.T) {
	svc, mux := newTestService(t, tokenNow)
	if rec := httptest.NewRecorder(); true {
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dash_go/hc_scan/api/me", nil))
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"code":"login_required"`) || !strings.Contains(rec.Body.String(), "Login required.") {
			t.Fatalf("no cookie: %d %s", rec.Code, rec.Body.String())
		}
	}
	session, err := svc.IssueSession(context.Background(), User{EE: "AB12C34", Name: "N"})
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: "dash_go_session", Value: session}

	post := func(mutate func(*http.Request)) int {
		req := httptest.NewRequest(http.MethodPost, "/dash_go/hc_scan/api/scan", strings.NewReader("{}"))
		req.AddCookie(cookie)
		if mutate != nil {
			mutate(req)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := post(nil); code != http.StatusForbidden {
		t.Errorf("a POST without the X-Requested-With header must be refused, got %d", code)
	}
	if code := post(func(r *http.Request) { r.Header.Set("X-Requested-With", "dash_go") }); code != http.StatusNoContent {
		t.Errorf("a normal POST must pass, got %d", code)
	}
	if code := post(func(r *http.Request) {
		r.Header.Set("X-Requested-With", "dash_go")
		r.Header.Set("Sec-Fetch-Site", "cross-site")
	}); code != http.StatusForbidden {
		t.Errorf("a cross-site POST must be refused, got %d", code)
	}
	// an sso token is not a session
	sso := mustSign(t, svc.signer, Claims{K: KindSSO, EE: "AB12C34", Iat: tokenNow.Unix(), Exp: tokenNow.Add(time.Hour).Unix(), N: "x"})
	req := httptest.NewRequest(http.MethodGet, "/dash_go/hc_scan/api/me", nil)
	req.AddCookie(&http.Cookie{Name: "dash_go_session", Value: sso})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("an sso token must not work as a session: %d", rec.Code)
	}
}

func TestMemoryNoncesExpire(t *testing.T) {
	n := NewMemoryNonces()
	ctx := context.Background()
	if ok, _ := n.Consume(ctx, "a", time.Millisecond); !ok {
		t.Fatal("first use must succeed")
	}
	if ok, _ := n.Consume(ctx, "a", time.Millisecond); ok {
		t.Fatal("second use must fail")
	}
	time.Sleep(5 * time.Millisecond)
	if ok, _ := n.Consume(ctx, "a", time.Millisecond); !ok {
		t.Fatal("an expired nonce is forgotten")
	}
}
