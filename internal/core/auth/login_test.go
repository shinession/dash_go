package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"dash_go/internal/core/i18n"
)

type fakeDirectory struct {
	mu          sync.Mutex
	accounts    map[string]string // account typed -> password
	people      map[string]Identity
	unavailable bool
	noEE        map[string]bool
	logins      int
}

func newFakeDirectory() *fakeDirectory {
	return &fakeDirectory{
		accounts: map[string]string{"AB12C34": "s3cret-pw", "ann@example.com": "ann-pw", "NOEE01": "pw"},
		people:   map[string]Identity{"AB12C34": {EE: "AB12C34", Name: "Test User"}, "ann@example.com": {EE: "AN01A01", Name: "Ann Lee"}, "AN01A01": {EE: "AN01A01", Name: "Ann Lee"}},
		noEE:     map[string]bool{"NOEE01": true},
	}
}

func (d *fakeDirectory) Login(_ context.Context, user, password string) (Identity, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.logins++
	if d.unavailable {
		return Identity{}, fmt.Errorf("%w: down", ErrUnavailable)
	}
	if pw, ok := d.accounts[user]; !ok || pw != password {
		return Identity{}, ErrInvalidCredentials
	}
	if d.noEE[user] {
		return Identity{}, ErrNoEmployeeCode
	}
	return d.people[user], nil
}

func (d *fakeDirectory) Identify(_ context.Context, ee string) (Identity, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.unavailable {
		return Identity{}, fmt.Errorf("%w: down", ErrUnavailable)
	}
	if id, ok := d.people[ee]; ok {
		return id, nil
	}
	return Identity{}, ErrInvalidCredentials
}

type fakeSSO struct {
	mu          sync.Mutex
	results     map[string]SSOResult
	unavailable bool
	unreadable  bool
	asked       int
}

func (f *fakeSSO) LoginURL(token string) string {
	return "http://sso.example/sso/api/login?token=" + token
}

func (f *fakeSSO) Result(_ context.Context, token string) (SSOResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked++
	switch {
	case f.unavailable:
		return SSOResult{}, fmt.Errorf("%w: down", ErrUnavailable)
	case f.unreadable:
		return SSOResult{}, ErrInvalidCredentials
	}
	if r, ok := f.results[token]; ok {
		return r, nil
	}
	return SSOResult{}, ErrSSOPending
}

func (f *fakeSSO) set(token string, r SSOResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.results == nil {
		f.results = map[string]SSOResult{}
	}
	f.results[token] = r
}

type rig struct {
	t    *testing.T
	svc  *Service
	mux  http.Handler
	dir  *fakeDirectory
	sso  *fakeSSO
	logs *bytes.Buffer
	now  time.Time
}

func (r *rig) clock() time.Time { return r.now }

func newRig(t *testing.T, mutate func(*Settings)) *rig {
	t.Helper()
	r := &rig{t: t, dir: newFakeDirectory(), sso: &fakeSSO{}, logs: &bytes.Buffer{}, now: time.Unix(1700000000, 0)}
	set := Settings{
		BasePath: "/dash_go", Now: r.clock,
		Log:       slog.New(slog.NewTextHandler(r.logs, nil)),
		Directory: r.dir, PasswordLogin: true, SSO: r.sso, SSORequireSameIP: true,
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
	}
	if mutate != nil {
		mutate(&set)
	}
	r.svc = NewService(mustSigner(t), NewMemoryNonces(), set)
	mux := http.NewServeMux()
	r.svc.Mount(mux)
	mux.Handle("GET /dash_go/hc_scan/api/me", r.svc.Require(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		u, _ := UserFrom(req.Context())
		_, _ = w.Write([]byte(u.EE + "|" + u.Name))
	})))
	r.mux = i18n.Middleware(mux)
	return r
}

// post sends a JSON request the way the page does: from the browser at 198.51.100.7, with the header of the page's
// own script.
func (r *rig) post(path string, body any, mutate ...func(*http.Request)) *httptest.ResponseRecorder {
	r.t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/dash_go"+path, bytes.NewReader(raw))
	req.RemoteAddr = "198.51.100.7:50000"
	req.Host = "dash.example"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "dash_go")
	for _, m := range mutate {
		m(req)
	}
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, req)
	return rec
}

func fromIP(ip string) func(*http.Request) {
	return func(q *http.Request) { q.RemoteAddr = ip + ":50000" }
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("not JSON: %q", rec.Body.String())
	}
	return m
}

func sessionCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == "dash_go_session" && c.Value != "" {
			return c
		}
	}
	return nil
}

func (r *rig) me(c *http.Cookie) string {
	req := httptest.NewRequest(http.MethodGet, "/dash_go/hc_scan/api/me", nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, req)
	return fmt.Sprintf("%d %s", rec.Code, rec.Body.String())
}

func TestPasswordLoginStartsASession(t *testing.T) {
	r := newRig(t, nil)
	rec := r.post("/auth/login", map[string]string{"username": " AB12C34 ", "password": "s3cret-pw", "next": "/dash_go/hc_scan/"})
	if rec.Code != 200 {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	if got := decode(t, rec); got["ok"] != true || got["next"] != "/dash_go/hc_scan/" {
		t.Errorf("answer %v", got)
	}
	c := sessionCookie(rec)
	if c == nil || !c.HttpOnly || c.Path != "/dash_go/" || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie %+v", c)
	}
	if got := r.me(c); got != "200 AB12C34|Test User" {
		t.Errorf("the session is the identity of the directory: %q", got)
	}
}

func TestPasswordLoginByEmailUsesTheEmployeeCodeOfTheAccount(t *testing.T) {
	r := newRig(t, nil)
	rec := r.post("/auth/login", map[string]string{"username": "ann@example.com", "password": "ann-pw"})
	if rec.Code != 200 || r.me(sessionCookie(rec)) != "200 AN01A01|Ann Lee" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestPasswordLoginNextIsNeverAnOpenRedirect(t *testing.T) {
	r := newRig(t, nil)
	for _, next := range []string{"https://evil.example/", "//evil.example", "/elsewhere", "/dash_go/../x"} {
		rec := r.post("/auth/login", map[string]string{"username": "AB12C34", "password": "s3cret-pw", "next": next}, fromIP("198.51.100."+fmt.Sprint(len(next)+10)))
		if got := decode(t, rec)["next"]; got != "/dash_go/" {
			t.Errorf("next=%q answered %v", next, got)
		}
	}
}

func TestPasswordLoginWrongPasswordIsRefusedInTheLanguageOfTheRequest(t *testing.T) {
	r := newRig(t, nil)
	rec := r.post("/auth/login", map[string]string{"username": "AB12C34", "password": "nope"})
	got := decode(t, rec)
	if rec.Code != 401 || got["code"] != "invalid" || got["error"] != "Invalid account or password." || sessionCookie(rec) != nil {
		t.Errorf("%d %v", rec.Code, got)
	}
	rec = r.post("/auth/login", map[string]string{"username": "AB12C34", "password": "nope"}, func(q *http.Request) { q.Header.Set("Accept-Language", "zh-CN,zh;q=0.9") })
	if got := decode(t, rec); got["error"] != "账号或密码不正确。" {
		t.Errorf("the message follows the language: %v", got)
	}
	// an unknown account looks exactly like a wrong password
	rec = r.post("/auth/login", map[string]string{"username": "NOBODY", "password": "x"})
	if rec.Code != 401 || decode(t, rec)["code"] != "invalid" {
		t.Errorf("unknown account: %d %s", rec.Code, rec.Body.String())
	}
}

func TestLoginEndpointsRefuseRequestsThatDoNotComeFromThePage(t *testing.T) {
	r := newRig(t, nil)
	body := map[string]string{"username": "AB12C34", "password": "s3cret-pw"}
	cases := map[string]func(*http.Request){
		"without the header":  func(q *http.Request) { q.Header.Del("X-Requested-With") },
		"another header":      func(q *http.Request) { q.Header.Set("X-Requested-With", "XMLHttpRequest") },
		"marked cross-site":   func(q *http.Request) { q.Header.Set("Sec-Fetch-Site", "cross-site") },
		"another origin host": func(q *http.Request) { q.Header.Set("Origin", "http://evil.example") },
		"a broken origin":     func(q *http.Request) { q.Header.Set("Origin", "http://%zz") },
	}
	for name, mutate := range cases {
		rec := r.post("/auth/login", body, mutate)
		if rec.Code != 403 || decode(t, rec)["code"] != "forbidden" || sessionCookie(rec) != nil {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if r.dir.logins != 0 {
		t.Errorf("the directory was asked %d times for refused requests", r.dir.logins)
	}
	// the same host with another port is the same site for the browser, and a proxy may drop the port from Host
	for _, origin := range []string{"http://dash.example", "http://dash.example:8080", "HTTP://DASH.EXAMPLE"} {
		if rec := r.post("/auth/login", body, func(q *http.Request) { q.Header.Set("Origin", origin) }, fromIP("198.51.100."+fmt.Sprint(len(origin)))); rec.Code != 200 {
			t.Errorf("origin %q: %d %s", origin, rec.Code, rec.Body.String())
		}
	}
}

func TestPasswordLoginSwitchedOff(t *testing.T) {
	for name, mutate := range map[string]func(*Settings){
		"login_password false": func(s *Settings) { s.PasswordLogin = false },
		"no directory":         func(s *Settings) { s.Directory = nil },
	} {
		r := newRig(t, mutate)
		if rec := r.post("/auth/login", map[string]string{"username": "AB12C34", "password": "s3cret-pw"}); rec.Code != 404 {
			t.Errorf("%s: %d", name, rec.Code)
		}
		if r.svc.PasswordEnabled() {
			t.Errorf("%s: PasswordEnabled must be false", name)
		}
	}
}

func TestPasswordLoginRejectsMalformedRequests(t *testing.T) {
	r := newRig(t, nil)
	for name, body := range map[string]any{
		"empty":          map[string]string{},
		"no password":    map[string]string{"username": "AB12C34"},
		"blank username": map[string]string{"username": "   ", "password": "x"},
		"huge username":  map[string]string{"username": strings.Repeat("a", 500), "password": "x"},
		"huge password":  map[string]string{"username": "AB12C34", "password": strings.Repeat("a", 500)},
		"not an object":  []int{1},
	} {
		if rec := r.post("/auth/login", body); rec.Code != 400 || decode(t, rec)["code"] != "bad_request" {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if r.dir.logins != 0 {
		t.Errorf("malformed requests must not reach the directory (%d)", r.dir.logins)
	}
}

func TestPasswordLoginDirectoryDownIsNotBlamedOnTheAccount(t *testing.T) {
	r := newRig(t, nil)
	r.dir.unavailable = true
	body := map[string]string{"username": "AB12C34", "password": "s3cret-pw"}
	// more attempts than an account is allowed, each from another address: while the directory is down none of them
	// is counted against the account
	for i := 0; i < maxAccountAttempts+2; i++ {
		rec := r.post("/auth/login", body, fromIP(fmt.Sprintf("198.51.100.%d", 20+i)))
		if rec.Code != 503 || decode(t, rec)["code"] != "unavailable" {
			t.Fatalf("attempt %d: %d %s", i, rec.Code, rec.Body.String())
		}
	}
	r.dir.unavailable = false
	if rec := r.post("/auth/login", body, fromIP("198.51.100.99")); rec.Code != 200 {
		t.Errorf("the account was blamed for the outage: %d %s", rec.Code, rec.Body.String())
	}
}

func TestPasswordLoginAccountWithoutEmployeeCode(t *testing.T) {
	r := newRig(t, nil)
	rec := r.post("/auth/login", map[string]string{"username": "NOEE01", "password": "pw"})
	if rec.Code != 403 || decode(t, rec)["code"] != "no_employee_code" || sessionCookie(rec) != nil {
		t.Errorf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestPasswordLoginThrottlesTheAddress(t *testing.T) {
	r := newRig(t, nil)
	bad := map[string]string{"username": "AB12C34", "password": "wrong"}
	for i := 0; i < maxLoginAttempts; i++ {
		if rec := r.post("/auth/login", map[string]string{"username": fmt.Sprintf("guess%d", i), "password": "x"}); rec.Code != 401 {
			t.Fatalf("attempt %d: %d", i, rec.Code)
		}
	}
	rec := r.post("/auth/login", bad)
	if rec.Code != 429 || decode(t, rec)["code"] != "throttled" || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("the sixth attempt of one address: %d %s", rec.Code, rec.Body.String())
	}
	// even the right password does not pass while the address is blocked, and the directory is not asked
	asked := r.dir.logins
	if rec := r.post("/auth/login", map[string]string{"username": "AB12C34", "password": "s3cret-pw"}); rec.Code != 429 || r.dir.logins != asked {
		t.Errorf("blocked address: %d, directory asked %d times more", rec.Code, r.dir.logins-asked)
	}
	// another address is not affected
	if rec := r.post("/auth/login", map[string]string{"username": "AB12C34", "password": "s3cret-pw"}, fromIP("198.51.100.200")); rec.Code != 200 {
		t.Errorf("another address: %d", rec.Code)
	}
	r.now = r.now.Add(loginWindow + time.Second)
	if rec := r.post("/auth/login", map[string]string{"username": "AB12C34", "password": "s3cret-pw"}); rec.Code != 200 {
		t.Errorf("after the window: %d", rec.Code)
	}
}

func TestPasswordLoginThrottlesTheAccountAcrossAddresses(t *testing.T) {
	r := newRig(t, nil)
	for i := 0; i < maxAccountAttempts; i++ {
		rec := r.post("/auth/login", map[string]string{"username": "Ab12c34", "password": "wrong"}, fromIP(fmt.Sprintf("203.0.113.%d", i+1)))
		if rec.Code != 401 {
			t.Fatalf("attempt %d: %d", i, rec.Code)
		}
	}
	// the account is blocked for everybody now, whatever the case of the name, even from a fresh address
	rec := r.post("/auth/login", map[string]string{"username": "AB12C34", "password": "s3cret-pw"}, fromIP("203.0.113.250"))
	if rec.Code != 429 {
		t.Errorf("the account must be blocked: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(r.logs.String(), "account_throttled") {
		t.Errorf("the log must say the account was throttled:\n%s", r.logs.String())
	}
}

func TestASuccessfulLoginForgetsTheFailuresOfTheAccount(t *testing.T) {
	r := newRig(t, nil)
	for i := 0; i < maxLoginAttempts-1; i++ {
		r.post("/auth/login", map[string]string{"username": "AB12C34", "password": "wrong"})
	}
	if rec := r.post("/auth/login", map[string]string{"username": "AB12C34", "password": "s3cret-pw"}); rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	// one slot of the address was given back and the account is clean again
	if rec := r.post("/auth/login", map[string]string{"username": "AB12C34", "password": "wrong"}); rec.Code != 401 {
		t.Errorf("%d", rec.Code)
	}
}

func TestLoginNeverWritesThePasswordOrTheTypedAccountToTheLog(t *testing.T) {
	r := newRig(t, nil)
	r.post("/auth/login", map[string]string{"username": "typed-by-mistake-hunter2", "password": "my-very-secret-password"})
	r.post("/auth/login", map[string]string{"username": "AB12C34", "password": "s3cret-pw"})
	r.dir.unavailable = true
	r.post("/auth/login", map[string]string{"username": "AB12C34", "password": "another-secret"}, fromIP("198.51.100.77"))
	logs := r.logs.String()
	for _, secret := range []string{"my-very-secret-password", "hunter2", "typed-by-mistake", "s3cret-pw", "another-secret"} {
		if strings.Contains(logs, secret) {
			t.Errorf("the log contains %q:\n%s", secret, logs)
		}
	}
	if !strings.Contains(logs, "acct=") || !strings.Contains(logs, "ee=AB12C34") {
		t.Errorf("the log should still say who and from where:\n%s", logs)
	}
}

func TestLogoutEndsTheSessionOfDashGoOnly(t *testing.T) {
	r := newRig(t, nil)
	rec := r.post("/auth/login", map[string]string{"username": "AB12C34", "password": "s3cret-pw"})
	c := sessionCookie(rec)

	// a cross-site form post cannot log somebody out
	req := httptest.NewRequest(http.MethodPost, "/dash_go/auth/logout", nil)
	req.AddCookie(c)
	forged := httptest.NewRecorder()
	r.mux.ServeHTTP(forged, req)
	if forged.Code != 403 {
		t.Errorf("a logout without the header must be refused: %d", forged.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/dash_go/auth/logout", nil)
	req.Header.Set("X-Requested-With", "dash_go")
	req.AddCookie(c)
	out := httptest.NewRecorder()
	r.mux.ServeHTTP(out, req)
	if out.Code != 204 {
		t.Fatalf("logout: %d", out.Code)
	}
	var cleared bool
	for _, ck := range out.Result().Cookies() {
		if ck.Name == "dash_go_session" && ck.MaxAge < 0 && ck.Path == "/dash_go/" {
			cleared = true
		}
		if ck.Name != "dash_go_session" {
			t.Errorf("logout must not touch other cookies, it set %q", ck.Name)
		}
	}
	if !cleared {
		t.Error("the session cookie must be cleared")
	}
	getReq := httptest.NewRequest(http.MethodGet, "/dash_go/auth/logout", nil)
	get := httptest.NewRecorder()
	r.mux.ServeHTTP(get, getReq)
	if get.Code != http.StatusMethodNotAllowed {
		t.Errorf("a link must not log anybody out: %d", get.Code)
	}
}

func TestSessionAndLoginPath(t *testing.T) {
	r := newRig(t, nil)
	if _, ok := r.svc.Session(httptest.NewRequest(http.MethodGet, "/", nil)); ok {
		t.Error("no cookie, no session")
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "dash_go_session", Value: "junk"})
	if _, ok := r.svc.Session(req); ok {
		t.Error("a damaged cookie is no session")
	}
	good := mustSign(t, r.svc.signer, Claims{K: KindSession, EE: "AB12C34", Name: "N", Iat: r.now.Unix(), Exp: r.now.Add(time.Hour).Unix()})
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "dash_go_session", Value: good})
	if u, ok := r.svc.Session(req); !ok || u.EE != "AB12C34" || u.Name != "N" {
		t.Errorf("%+v %v", u, ok)
	}
	r.now = r.now.Add(2 * time.Hour)
	if _, ok := r.svc.Session(req); ok {
		t.Error("an expired session is no session")
	}
	if got := r.svc.LoginPath("/dash_go/hc_scan/?a=1"); got != "/dash_go/login?next=%2Fdash_go%2Fhc_scan%2F%3Fa%3D1" {
		t.Errorf("%s", got)
	}
	if got := r.svc.LoginPath(""); got != "/dash_go/login" {
		t.Errorf("%s", got)
	}
}

func TestSameOrigin(t *testing.T) {
	mk := func(host string, h map[string]string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.Host = host
		for k, v := range h {
			r.Header.Set(k, v)
		}
		return r
	}
	cases := []struct {
		name string
		req  *http.Request
		want bool
	}{
		{"no origin (older browsers, scripts)", mk("a.example", nil), true},
		{"same host", mk("a.example", map[string]string{"Origin": "http://a.example"}), true},
		{"same host with port on one side", mk("a.example", map[string]string{"Origin": "http://a.example:8080"}), true},
		{"ipv6", mk("[::1]:8750", map[string]string{"Origin": "http://[::1]:8750"}), true},
		{"other host", mk("a.example", map[string]string{"Origin": "http://b.example"}), false},
		{"a host that only ends the same", mk("a.example", map[string]string{"Origin": "http://evil-a.example"}), false},
		{"cross-site fetch metadata", mk("a.example", map[string]string{"Sec-Fetch-Site": "cross-site"}), false},
		{"same-site fetch metadata", mk("a.example", map[string]string{"Sec-Fetch-Site": "same-origin"}), true},
		{"null origin", mk("a.example", map[string]string{"Origin": "null"}), false},
	}
	for _, c := range cases {
		if got := sameOrigin(c.req); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}
