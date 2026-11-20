package auth

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

func prefixes(t *testing.T, texts ...string) []netip.Prefix {
	t.Helper()
	var out []netip.Prefix
	for _, s := range texts {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func request(remote string, forwarded ...string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remote
	for _, f := range forwarded {
		r.Header.Add("X-Forwarded-For", f)
	}
	return r
}

func TestClientIP(t *testing.T) {
	local := []string{"127.0.0.1/32", "::1/128"}
	chain := []string{"127.0.0.1/32", "10.0.0.5/32"} // nginx here and the nginx in front of it
	cases := []struct {
		name    string
		proxies []string
		req     *http.Request
		want    string
	}{
		{"direct connection, no proxies configured", nil, request("198.51.100.7:5000"), "198.51.100.7"},
		{"no proxies configured ignores the header", nil, request("198.51.100.7:5000", "1.2.3.4"), "198.51.100.7"},
		{"a client that is no proxy cannot set the header", local, request("198.51.100.7:5000", "1.2.3.4"), "198.51.100.7"},
		{"nginx on the same computer", local, request("127.0.0.1:41000", "198.51.100.7"), "198.51.100.7"},
		{"nginx over IPv6 loopback", local, request("[::1]:41000", "198.51.100.7"), "198.51.100.7"},
		{"a proxy without the header", local, request("127.0.0.1:41000"), "127.0.0.1"},
		{"the left side of the header is the browser's own claim", local, request("127.0.0.1:41000", "6.6.6.6, 198.51.100.7"), "198.51.100.7"},
		{"two proxies in a row", chain, request("127.0.0.1:41000", "198.51.100.7, 10.0.0.5"), "198.51.100.7"},
		{"two proxies, the front one adds its own entry", chain, request("127.0.0.1:41000", "6.6.6.6", "198.51.100.7, 10.0.0.5"), "198.51.100.7"},
		{"a hop that is no address is not guessed", local, request("127.0.0.1:41000", "198.51.100.7, unknown"), "127.0.0.1"},
		{"an IPv4 address mapped into IPv6", local, request("127.0.0.1:41000", "::ffff:198.51.100.7"), "198.51.100.7"},
		{"only proxies in the header", local, request("127.0.0.1:41000", "127.0.0.1"), "127.0.0.1"},
		{"a remote address without port", nil, request("198.51.100.7"), "198.51.100.7"},
	}
	for _, c := range cases {
		if got := clientIP(c.req, prefixes(t, c.proxies...)); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSameAddress(t *testing.T) {
	yes := [][2]string{
		{"198.51.100.7", "198.51.100.7"},
		{"198.51.100.7", "198.51.100.7:51234"},
		{"::ffff:198.51.100.7", "198.51.100.7"},
		{"[2001:db8::1]:8080", "2001:db8::1"},
		{"2001:DB8::1", "2001:db8:0:0:0:0:0:1"},
		{"fe80::1%eth0", "fe80::1"},
	}
	for _, p := range yes {
		if !sameAddress(p[0], p[1]) {
			t.Errorf("%q and %q are the same address", p[0], p[1])
		}
	}
	no := [][2]string{
		{"198.51.100.7", "198.51.100.8"},
		{"", ""},
		{"unknown", "unknown"},
		{"198.51.100.7", ""},
		{"2001:db8::1", "198.51.100.7"},
	}
	for _, p := range no {
		if sameAddress(p[0], p[1]) {
			t.Errorf("%q and %q must not match", p[0], p[1])
		}
	}
}

func TestAddressDetailSaysHowTheRequestArrived(t *testing.T) {
	got := addressDetail(request("10.9.9.9:4000", "198.51.100.7"), prefixes(t, "127.0.0.1/32"))
	want := []any{"connection", "10.9.9.9", "trusted_proxy", false, "forwarded", "198.51.100.7"}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: got %v want %v", i, got[i], want[i])
		}
	}
	long := make([]byte, 600)
	for i := range long {
		long[i] = 'a'
	}
	if d := addressDetail(request("127.0.0.1:1", string(long)), prefixes(t, "127.0.0.1/32")); len(d[5].(string)) > 300 {
		t.Error("text from the network must be cut short before it goes to the log")
	}
}

func TestLimiter(t *testing.T) {
	now := time.Unix(1700000000, 0)
	l := newLimiter(3, time.Minute, func() time.Time { return now })
	for i := 0; i < 3; i++ {
		if _, ok := l.reserve("a"); !ok {
			t.Fatalf("attempt %d must pass", i)
		}
	}
	wait, ok := l.reserve("a")
	if ok || wait <= 0 || wait > time.Minute {
		t.Fatalf("the fourth attempt: ok=%v wait=%v", ok, wait)
	}
	if _, ok := l.reserve("b"); !ok {
		t.Error("another key is not affected")
	}
	l.refund("a")
	if _, ok := l.reserve("a"); !ok {
		t.Error("a refunded slot can be used again")
	}
	if _, ok := l.reserve("a"); ok {
		t.Error("and only one")
	}
	now = now.Add(time.Minute)
	if _, ok := l.reserve("a"); !ok {
		t.Error("the window ends")
	}
	l.reset("a")
	for i := 0; i < 3; i++ {
		if _, ok := l.reserve("a"); !ok {
			t.Errorf("after a reset attempt %d must pass", i)
		}
	}
}

func TestLimiterForgetsEntriesWhoseWindowEnded(t *testing.T) {
	now := time.Unix(1700000000, 0)
	l := newLimiter(1, time.Minute, func() time.Time { return now })
	for _, k := range []string{"a", "b", "c"} {
		l.reserve(k)
	}
	now = now.Add(2 * time.Minute)
	l.reserve("fresh") // purges
	if len(l.seen) != 1 {
		t.Errorf("a flood of distinct keys must not stay in memory: %d entries", len(l.seen))
	}
}

func TestAccountKey(t *testing.T) {
	for in, want := range map[string]string{
		"AB12C34":          "ab12c34",
		" ab12c34 ":        "ab12c34",
		"Ann.Lee@Corp.com": "ann.lee",
		"AB12C34@corp.com": "ab12c34",
		"Ann Lee":          "ann lee",
		"@odd":             "@odd",
	} {
		if got := accountKey(in); got != want {
			t.Errorf("accountKey(%q) = %q, want %q", in, got, want)
		}
	}
}
