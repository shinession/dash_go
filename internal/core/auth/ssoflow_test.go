package auth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"regexp"
	"strings"
	"testing"
	"time"
)

var ssoTokenShape = regexp.MustCompile(`^[a-z2-7]{24}$`)

// start begins a Windows sign-in from the browser at ip and returns the token.
func (r *rig) startSSO(ip, next string) (string, *httptest.ResponseRecorder) {
	r.t.Helper()
	rec := r.post("/auth/sso/start", map[string]string{"next": next}, fromIP(ip))
	if rec.Code != 200 {
		return "", rec
	}
	return decode(r.t, rec)["token"].(string), rec
}

func (r *rig) pollSSO(token, ip string, mutate ...func(*http.Request)) (string, map[string]any, *httptest.ResponseRecorder) {
	r.t.Helper()
	if ip != "" {
		mutate = append(mutate, fromIP(ip))
	}
	rec := r.post("/auth/sso/poll", map[string]string{"token": token}, mutate...)
	if rec.Code != 200 {
		r.t.Fatalf("poll: %d %s", rec.Code, rec.Body.String())
	}
	got := decode(r.t, rec)
	return got["status"].(string), got, rec
}

func okResult(ip string) SSOResult {
	return SSOResult{Status: "ok", ECode: "AB12C34", WindowsUser: `CO\AB12C34`, IP: ip, At: time.Unix(1700000000, 0)}
}

func TestSSOStartIssuesATokenAndTheSignOnAddress(t *testing.T) {
	r := newRig(t, nil)
	token, rec := r.startSSO("198.51.100.7", "/dash_go/hc_scan/")
	if !ssoTokenShape.MatchString(token) {
		t.Fatalf("the sign-on service accepts [a-z0-9]{16,40}; this server issues 24 characters of a-z2-7: %q", token)
	}
	got := decode(t, rec)
	if got["url"] != "http://sso.example/sso/api/login?token="+token || got["expires_in"] != float64(300) {
		t.Errorf("%v", got)
	}
	other, _ := r.startSSO("198.51.100.8", "")
	if other == token {
		t.Error("every sign-in gets its own token")
	}
}

func TestSSOEndpointsAreSwitchedOffWithoutTheService(t *testing.T) {
	for name, mutate := range map[string]func(*Settings){
		"no sign-on service": func(s *Settings) { s.SSO = nil },
		"no directory":       func(s *Settings) { s.Directory = nil },
	} {
		r := newRig(t, mutate)
		if rec := r.post("/auth/sso/start", map[string]string{}); rec.Code != 404 {
			t.Errorf("%s: start %d", name, rec.Code)
		}
		if rec := r.post("/auth/sso/poll", map[string]string{"token": "x"}); rec.Code != 404 {
			t.Errorf("%s: poll %d", name, rec.Code)
		}
		if r.svc.SSOEnabled() {
			t.Errorf("%s: SSOEnabled must be false", name)
		}
	}
}

func TestSSOEndpointsRefuseRequestsThatDoNotComeFromThePage(t *testing.T) {
	r := newRig(t, nil)
	for _, path := range []string{"/auth/sso/start", "/auth/sso/poll"} {
		rec := r.post(path, map[string]string{"token": "x"}, func(q *http.Request) { q.Header.Del("X-Requested-With") })
		if rec.Code != 403 {
			t.Errorf("%s without the header: %d", path, rec.Code)
		}
		rec = r.post(path, map[string]string{"token": "x"}, func(q *http.Request) { q.Header.Set("Origin", "http://evil.example") })
		if rec.Code != 403 {
			t.Errorf("%s from another origin: %d", path, rec.Code)
		}
	}
	if r.sso.asked != 0 {
		t.Errorf("the sign-on service was asked %d times", r.sso.asked)
	}
}

func TestSSOStartIsLimitedPerAddressAndInAll(t *testing.T) {
	r := newRig(t, nil)
	for i := 0; i < ssoMaxFlowsPerIP; i++ {
		if _, rec := r.startSSO("198.51.100.7", ""); rec.Code != 200 {
			t.Fatalf("flow %d: %d", i, rec.Code)
		}
	}
	rec := r.post("/auth/sso/start", map[string]string{}, fromIP("198.51.100.7"))
	if rec.Code != 429 || decode(t, rec)["code"] != "busy" {
		t.Errorf("the sixth waiting sign-in of one address: %d %s", rec.Code, rec.Body.String())
	}
	if _, rec := r.startSSO("198.51.100.8", ""); rec.Code != 200 {
		t.Errorf("another address is not affected: %d", rec.Code)
	}
	r.now = r.now.Add(ssoFlowTTL + time.Second)
	if _, rec := r.startSSO("198.51.100.7", ""); rec.Code != 200 {
		t.Errorf("expired flows free the slots: %d", rec.Code)
	}
}

func TestSSOPollPendingThenSignedIn(t *testing.T) {
	r := newRig(t, nil)
	token, _ := r.startSSO("198.51.100.7", "/dash_go/hc_scan/")
	if status, _, rec := r.pollSSO(token, "198.51.100.7"); status != "pending" || sessionCookie(rec) != nil {
		t.Fatalf("nothing recorded yet: %s", status)
	}
	r.now = r.now.Add(2 * time.Second)
	r.sso.set(token, okResult("198.51.100.7"))
	status, got, rec := r.pollSSO(token, "198.51.100.7")
	if status != "ok" || got["next"] != "/dash_go/hc_scan/" {
		t.Fatalf("%s %v", status, got)
	}
	c := sessionCookie(rec)
	if c == nil || r.me(c) != "200 AB12C34|Test User" {
		t.Fatalf("the session must be the account of the employee code: %+v", c)
	}
	// a result is used once: the flow is gone
	r.now = r.now.Add(2 * time.Second)
	if status, _, rec := r.pollSSO(token, "198.51.100.7"); status != "expired" || sessionCookie(rec) != nil {
		t.Errorf("the second poll of a used token: %s", status)
	}
	if !strings.Contains(r.logs.String(), "method=sso") {
		t.Errorf("the sign-in must be logged:\n%s", r.logs.String())
	}
}

func TestSSOPollOnlyKnowsTokensThisServerIssued(t *testing.T) {
	r := newRig(t, nil)
	r.sso.set("abcdefghijklmnopqrstuvwx", okResult("198.51.100.7"))
	for _, token := range []string{"abcdefghijklmnopqrstuvwx", "", "x", strings.Repeat("a", 3000)} {
		if status, _, rec := r.pollSSO(token, "198.51.100.7"); status != "expired" || sessionCookie(rec) != nil {
			t.Errorf("token %q: %s", token[:min(len(token), 10)], status)
		}
	}
	if r.sso.asked != 0 {
		t.Errorf("a token that was never issued must not reach the sign-on service (%d asks)", r.sso.asked)
	}
}

func TestSSOPollIsRateLimitedPerFlow(t *testing.T) {
	r := newRig(t, nil)
	token, _ := r.startSSO("198.51.100.7", "")
	r.now = r.now.Add(2 * time.Second)
	r.pollSSO(token, "198.51.100.7")
	asked := r.sso.asked
	for i := 0; i < 5; i++ {
		if status, _, _ := r.pollSSO(token, "198.51.100.7"); status != "pending" {
			t.Fatal(status)
		}
	}
	if r.sso.asked != asked {
		t.Errorf("polls within a second must not ask the sign-on service again (%d more)", r.sso.asked-asked)
	}
}

func TestSSOExpiredFlow(t *testing.T) {
	r := newRig(t, nil)
	token, _ := r.startSSO("198.51.100.7", "")
	r.now = r.now.Add(ssoFlowTTL + time.Second)
	r.sso.set(token, okResult("198.51.100.7"))
	if status, _, rec := r.pollSSO(token, "198.51.100.7"); status != "expired" || sessionCookie(rec) != nil {
		t.Errorf("%s", status)
	}
}

// The attack of the sign-on flow: somebody starts a flow, gets a victim to open the sign-on link with the token, and
// polls for the identity of the victim. The address of the browser that started the flow must be the address the
// sign-on service recorded.
func TestSSORefusesAResultRecordedForAnotherAddress(t *testing.T) {
	r := newRig(t, nil)
	token, _ := r.startSSO("198.51.100.7", "")
	r.now = r.now.Add(2 * time.Second)
	r.sso.set(token, okResult("203.0.113.99")) // the victim's computer
	status, _, rec := r.pollSSO(token, "198.51.100.7")
	if status != "mismatch" || sessionCookie(rec) != nil {
		t.Fatalf("%s", status)
	}
	if !strings.Contains(r.logs.String(), "address_mismatch") || !strings.Contains(r.logs.String(), "started_from=198.51.100.7") {
		t.Errorf("the log must say which addresses differ:\n%s", r.logs.String())
	}
	// the flow is gone: the identity cannot be fetched with a second try
	r.now = r.now.Add(2 * time.Second)
	if status, _, _ := r.pollSSO(token, "198.51.100.7"); status != "expired" {
		t.Errorf("the refused flow must end: %s", status)
	}
}

func TestSSORefusesAPollFromAnotherAddressThanTheStart(t *testing.T) {
	r := newRig(t, nil)
	token, _ := r.startSSO("198.51.100.7", "")
	r.sso.set(token, okResult("198.51.100.7"))
	r.now = r.now.Add(2 * time.Second)
	if status, _, rec := r.pollSSO(token, "203.0.113.5"); status != "mismatch" || sessionCookie(rec) != nil {
		t.Errorf("another browser polling with the token: %s", status)
	}
	if !strings.Contains(r.logs.String(), "browser_address_changed") {
		t.Errorf("%s", r.logs.String())
	}
}

func TestSSOAddressCheckCanBeSwitchedOff(t *testing.T) {
	r := newRig(t, func(s *Settings) { s.SSORequireSameIP = false })
	token, _ := r.startSSO("198.51.100.7", "")
	r.now = r.now.Add(2 * time.Second)
	r.sso.set(token, okResult("10.99.99.99"))
	if status, _, _ := r.pollSSO(token, "203.0.113.5"); status != "ok" {
		t.Errorf("sso_require_same_ip=false: %s", status)
	}
}

func TestSSOUsesTheAddressBehindATrustedProxy(t *testing.T) {
	r := newRig(t, nil)
	viaProxy := func(browser string) func(*http.Request) {
		return func(q *http.Request) {
			q.RemoteAddr = "127.0.0.1:41000" // nginx on the same computer
			q.Header.Set("X-Forwarded-For", browser)
		}
	}
	rec := r.post("/auth/sso/start", map[string]string{}, viaProxy("198.51.100.7"))
	token := decode(t, rec)["token"].(string)
	r.now = r.now.Add(2 * time.Second)
	r.sso.set(token, okResult("198.51.100.7"))
	status, _, _ := r.pollSSO(token, "", viaProxy("198.51.100.7"))
	if status != "ok" {
		t.Errorf("the browser behind the proxy must match what the sign-on service saw: %s", status)
	}

	// a forged header from a client that is not a proxy is ignored: the browser cannot pick its own address
	token2, _ := r.startSSO("203.0.113.5", "")
	r.now = r.now.Add(2 * time.Second)
	r.sso.set(token2, okResult("198.51.100.7")) // the identity of somebody else
	forged := func(q *http.Request) { q.Header.Set("X-Forwarded-For", "198.51.100.7") }
	if status, _, _ := r.pollSSO(token2, "203.0.113.5", forged); status != "mismatch" {
		t.Errorf("a forged X-Forwarded-For must not help: %s", status)
	}
}

// A browser on the computer that runs dash_go, opened as localhost, is a loopback address for dash_go but the address
// of that computer for the sign-on service. It is one browser on one computer.
func TestSSOBrowserOnThisComputerMatchesTheAddressOfThisComputer(t *testing.T) {
	local := func(s *Settings) {
		s.TrustedProxies = append(s.TrustedProxies, netip.MustParsePrefix("::1/128")) // the default of trusted_proxies
		s.LocalAddrs = func() []netip.Addr {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("::1"), netip.MustParseAddr("10.135.36.89")}
		}
	}
	direct := func(q *http.Request) { q.RemoteAddr = "127.0.0.1:50000" }
	throughNginx := func(q *http.Request) { // nginx on this computer, the browser came from ::1
		q.RemoteAddr = "127.0.0.1:41000"
		q.Header.Set("X-Forwarded-For", "::1")
	}
	cases := []struct {
		name    string
		browser func(*http.Request)
		ssoSaw  string
		want    string
	}{
		{"localhost, the sign-on service saw the address of this computer", direct, "10.135.36.89", "ok"},
		{"localhost through nginx on this computer", throughNginx, "10.135.36.89", "ok"},
		{"localhost, the sign-on service saw the other loopback address", direct, "::1", "ok"},
		{"localhost, the sign-on service saw another computer", direct, "10.135.36.90", "mismatch"},
		{"localhost, the sign-on service saw no address", direct, "", "mismatch"},
	}
	for _, c := range cases {
		r := newRig(t, local)
		token := decode(t, r.post("/auth/sso/start", map[string]string{}, c.browser))["token"].(string)
		r.now = r.now.Add(2 * time.Second)
		r.sso.set(token, okResult(c.ssoSaw))
		if status, _, _ := r.pollSSO(token, "", c.browser); status != c.want {
			t.Errorf("%s: %s, want %s", c.name, status, c.want)
		}
	}
}

// The relaxation is only for a browser that dash_go sees as loopback. A browser on another computer cannot take over an
// identity that was recorded for this computer: that is the forced sign-in the address check exists to stop.
func TestSSOAnAddressOfThisComputerDoesNotHelpABrowserOnAnotherComputer(t *testing.T) {
	r := newRig(t, func(s *Settings) {
		s.LocalAddrs = func() []netip.Addr { return []netip.Addr{netip.MustParseAddr("10.135.36.89")} }
	})
	token, _ := r.startSSO("198.51.100.7", "")
	r.now = r.now.Add(2 * time.Second)
	r.sso.set(token, okResult("10.135.36.89"))
	if status, _, rec := r.pollSSO(token, "198.51.100.7"); status != "mismatch" || sessionCookie(rec) != nil {
		t.Errorf("%s", status)
	}
}

func TestOwnAddressesListsTheLoopbackInterface(t *testing.T) {
	loopback := false
	for _, a := range ownAddresses() {
		if a.Zone() != "" || a.Is4In6() {
			t.Errorf("%v must be written the way parseAddr writes an address", a)
		}
		loopback = loopback || a.IsLoopback()
	}
	if !loopback {
		t.Error("every computer has a loopback address")
	}
}

func TestSSOOtherOutcomes(t *testing.T) {
	cases := []struct {
		name   string
		result SSOResult
		setup  func(r *rig)
		want   string
	}{
		{"the browser sent no Windows user", SSOResult{Status: "no_user", IP: "198.51.100.7"}, nil, "no_user"},
		{"an ok result without employee code", SSOResult{Status: "ok", IP: "198.51.100.7"}, nil, "no_user"},
		{"no active account for the employee code", SSOResult{Status: "ok", ECode: "ZZ99Z99", IP: "198.51.100.7"}, nil, "denied"},
		{"the sign-on service cannot be read", okResult("198.51.100.7"), func(r *rig) { r.sso.unreadable = true }, "denied"},
		{"the sign-on service is down", okResult("198.51.100.7"), func(r *rig) { r.sso.unavailable = true }, "unavailable"},
		{"the user directory is down", okResult("198.51.100.7"), func(r *rig) { r.dir.unavailable = true }, "unavailable"},
	}
	for _, c := range cases {
		r := newRig(t, nil)
		token, _ := r.startSSO("198.51.100.7", "")
		r.now = r.now.Add(2 * time.Second)
		r.sso.set(token, c.result)
		if c.setup != nil {
			c.setup(r)
		}
		status, _, rec := r.pollSSO(token, "198.51.100.7")
		if status != c.want || sessionCookie(rec) != nil {
			t.Errorf("%s: %s", c.name, status)
		}
	}
}

func TestSSOTemporaryFailureKeepsTheFlowSoTheNextLookTriesAgain(t *testing.T) {
	r := newRig(t, nil)
	token, _ := r.startSSO("198.51.100.7", "")
	r.sso.set(token, okResult("198.51.100.7"))
	r.dir.unavailable = true
	r.now = r.now.Add(2 * time.Second)
	if status, _, _ := r.pollSSO(token, "198.51.100.7"); status != "unavailable" {
		t.Fatal(status)
	}
	r.dir.unavailable = false
	r.now = r.now.Add(2 * time.Second)
	if status, _, rec := r.pollSSO(token, "198.51.100.7"); status != "ok" || sessionCookie(rec) == nil {
		t.Errorf("the second look must sign in: %s", status)
	}
}

func TestSSODeniedAccountDoesNotLeakWhetherTheEmployeeCodeExists(t *testing.T) {
	r := newRig(t, nil)
	for i, code := range []string{"ZZ99Z99", "NOEE01"} { // unknown, and known without employee code in Login
		token, _ := r.startSSO(fmt.Sprintf("198.51.100.%d", 10+i), "")
		r.now = r.now.Add(2 * time.Second)
		r.sso.set(token, SSOResult{Status: "ok", ECode: code, IP: fmt.Sprintf("198.51.100.%d", 10+i)})
		if status, _, _ := r.pollSSO(token, fmt.Sprintf("198.51.100.%d", 10+i)); status != "denied" {
			t.Errorf("%s: %s", code, status)
		}
	}
}

func TestSSOStartUsesTheSafeNext(t *testing.T) {
	r := newRig(t, nil)
	token, _ := r.startSSO("198.51.100.7", "https://evil.example/")
	r.now = r.now.Add(2 * time.Second)
	r.sso.set(token, okResult("198.51.100.7"))
	if _, got, _ := r.pollSSO(token, "198.51.100.7"); got["next"] != "/dash_go/" {
		t.Errorf("next: %v", got["next"])
	}
}

func TestSSOFlowsAreBoundedInAll(t *testing.T) {
	var f ssoFlows
	now := time.Unix(1700000000, 0)
	for i := 0; i < ssoMaxFlows; i++ {
		ip := netip.AddrFrom4([4]byte{10, byte(i >> 16), byte(i >> 8), byte(i)}).String()
		if _, err := f.start(ip, "", now); err != nil {
			t.Fatalf("flow %d: %v", i, err)
		}
	}
	if _, err := f.start("192.0.2.1", "", now); err != errSSOBusy {
		t.Errorf("the memory a visitor can make the server hold is bounded: %v", err)
	}
}
